package forms

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/ipoluianov/althex/app"
	"github.com/ipoluianov/althex/config"
	"github.com/ipoluianov/althex/core/doc"
	"github.com/ipoluianov/althex/forms/hexview"
	"github.com/ipoluianov/nui/ui"
)

// syncing is set while the journal is being made durable in the background
var syncing atomic.Bool

func docOptions() doc.Options {
	return doc.Options{JournalDir: config.JournalDirectory()}
}

// openDialog asks for a file to open
func (c *MainForm) openDialog() {
	dir := ""
	if d := c.doc(); d != nil && d.Path() != "" {
		dir = filepath.Dir(d.Path())
	}
	c.Form().ShowOpenFileDialog(ui.OpenFileDialogOptions{Title: T().MenuOpen, DefaultDirectory: dir},
		func(paths []string, err error) {
			if err != nil {
				ui.ShowMessageBox(c, T().Error, err.Error())
				return
			}
			if len(paths) > 0 {
				c.OpenFile(paths[0])
			}
		})
}

// OpenFiles opens the files of the command line, a drop or a second start
func (c *MainForm) OpenFiles(paths []string) {
	// One document at a time for now: the last one wins
	if len(paths) > 0 {
		c.OpenFile(paths[len(paths)-1])
	}
}

// OpenFile opens the file in place of the open document
func (c *MainForm) OpenFile(path string) {
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	if d := c.doc(); d != nil && d.Path() == path {
		return
	}
	c.closeDocument(func() {
		d, err := doc.Open(path, docOptions())
		if err != nil {
			ui.ShowMessageBox(c, T().Error, T().OpenFailed(path, err.Error()))
			return
		}
		_ = config.AddRecentFile(path)
		c.showDocument(d)
	})
}

// showDocument puts the view of the document in the window
func (c *MainForm) showDocument(d *doc.Document) {
	v := hexview.NewView(d)
	if size := config.GetSettings().FontSize; size > 0 {
		v.SetFontSize(size)
	}
	v.OnStateChanged = c.updateStatus
	v.OnError = func(err error) { ui.ShowToast(c, err.Error(), ui.ToastError) }
	c.RemoveWidget(c.lblHint)
	c.AddWidget(0, 0, v)
	c.view = v
	c.fileSeen = false
	v.Focus()
	c.updateStatus()
	c.Form().UpdateLayout()
}

// closeDocument closes the document, asking about its edits first, and
// then calls then (nil - nothing); canceled - not called
func (c *MainForm) closeDocument(then func()) {
	if c.saving {
		return
	}
	d := c.doc()
	if d == nil || !d.Modified() {
		c.dropDocument()
		if then != nil {
			then()
		}
		return
	}
	c.ShowDialog(NewSaveChangesDialog(c.docName(), func() {
		c.save(false, func() {
			c.dropDocument()
			if then != nil {
				then()
			}
		})
	}, func() {
		c.dropDocument()
		if then != nil {
			then()
		}
	}))
}

// dropDocument closes the document without asking: its edits are lost
func (c *MainForm) dropDocument() {
	if c.view == nil {
		return
	}
	d := c.view.Document()
	c.view.Close()
	c.RemoveWidget(c.view)
	c.view = nil
	d.Close()
	c.AddWidget(0, 0, c.lblHint)
	c.updateStatus()
	if f := c.Form(); f != nil {
		f.UpdateLayout()
	}
}

// docName is the name of the document for the title and the questions
func (c *MainForm) docName() string {
	d := c.doc()
	if d == nil {
		return ""
	}
	if p := d.Path(); p != "" {
		return filepath.Base(p)
	}
	return T().Untitled
}

// save saves the document in the background, asking for a name if it has
// none or saveAs, and then calls then (nil - nothing) if it was saved
func (c *MainForm) save(saveAs bool, then func()) {
	d := c.doc()
	if d == nil || c.saving {
		return
	}
	if saveAs || d.Path() == "" {
		dir, name := "", ""
		if p := d.Path(); p != "" {
			dir, name = filepath.Split(p)
		}
		c.Form().ShowSaveFileDialog(ui.SaveFileDialogOptions{Title: T().MenuSaveAs, DefaultDirectory: dir, DefaultFileName: name},
			func(path string, err error) {
				if err != nil {
					ui.ShowMessageBox(c, T().Error, err.Error())
					return
				}
				if path != "" {
					c.saveTo(path, then)
				}
			})
		return
	}
	if !d.Modified() {
		if then != nil {
			then()
		}
		return
	}
	c.saveTo("", then)
}

func (c *MainForm) saveTo(path string, then func()) {
	d := c.doc()
	form := c.Form()
	c.saving = true
	c.view.SetBusy(true)
	c.status.SetInfo(T().Saving(0))
	var lastShown atomic.Int64
	opts := doc.SaveOptions{
		Path:   path,
		Backup: config.GetSettings().Backup,
		Progress: func(done, total int64) {
			// Shown at most ten times a second
			now := time.Now().UnixMilli()
			if now-lastShown.Load() < 100 || total == 0 {
				return
			}
			lastShown.Store(now)
			pct := int(done * 100 / total)
			form.Invoke(func() { c.status.SetInfo(T().Saving(pct)) })
		},
	}
	go func() {
		err := d.Save(opts)
		form.Invoke(func() {
			c.saving = false
			if c.view != nil {
				c.view.SetBusy(false)
			}
			c.updateStatus()
			if err != nil {
				ui.ShowMessageBox(c, T().Error, T().SaveFailed(err.Error()))
				return
			}
			_ = config.AddRecentFile(d.Path())
			ui.ShowToast(c, T().Saved, ui.ToastSuccess)
			if then != nil {
				then()
			}
		})
	}()
}

// updateStatus shows the state of the document in the status bar and the title
func (c *MainForm) updateStatus() {
	c.UpdateTitle()
	v := c.view
	if v == nil {
		c.status.SetInfo("")
		return
	}
	if c.saving {
		return
	}
	d := v.Document()
	t := T()
	caret := v.Caret()
	parts := []string{fmt.Sprintf("%s: %X (%d)", t.StatusOffset, caret, caret)}
	if _, n := v.Selection(); n > 0 {
		parts = append(parts, fmt.Sprintf("%s: %d (%Xh)", t.StatusSelected, n, n))
	}
	var b [1]byte
	if n, _ := d.ReadAt(b[:], caret); n == 1 {
		parts = append(parts, fmt.Sprintf("%s: %02X (%d)", t.StatusValue, b[0], b[0]))
	}
	parts = append(parts, fmt.Sprintf("%s: %s", t.StatusSize, formatSize(d.Size())))
	if v.InsertMode() {
		parts = append(parts, t.ModeInsert)
	} else {
		parts = append(parts, t.ModeOverwrite)
	}
	if d.ReadOnly() {
		parts = append(parts, t.ReadOnly)
	}
	c.status.SetInfo(strings.Join(parts, "   ·   "))
}

// formatSize writes a number of bytes with the thousands set apart: 1 234 567
func formatSize(n int64) string {
	s := fmt.Sprint(n)
	var sb strings.Builder
	for i, ch := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			sb.WriteRune(' ')
		}
		sb.WriteRune(ch)
	}
	return sb.String()
}

// checkDocument runs now and then: makes the journal durable and asks
// about a file changed by another program (SRC-7)
func (c *MainForm) checkDocument() {
	d := c.doc()
	if d == nil || c.saving {
		return
	}
	if d.Modified() && syncing.CompareAndSwap(false, true) {
		go func() {
			d.Sync()
			syncing.Store(false)
		}()
	}
	if c.fileSeen {
		return
	}
	changed, err := d.FileChanged()
	if err != nil || !changed {
		return
	}
	c.fileSeen = true
	path := d.Path()
	ui.ShowQuestionMessageBoxYesNo(c, app.DisplayName, T().FileChangedAsk(filepath.Base(path)), func() {
		c.dropDocument()
		c.OpenFile(path)
	}, nil)
}

// RecoverJournals offers to recover the edits left by a session that
// crashed; call it once the window is shown, before opening files
func (c *MainForm) RecoverJournals(then func()) {
	list, err := doc.FindJournals(config.JournalDirectory())
	if err != nil || len(list) == 0 {
		then()
		return
	}
	info := list[0]
	name := T().Untitled
	if info.Path != "" {
		name = info.Path
	}
	ui.ShowQuestionMessageBoxYesNo(c, T().RecoverTitle, T().RecoverAsk(name, info.Modified.Format("2006-01-02 15:04")), func() {
		d, err := doc.Recover(info, docOptions())
		if err != nil {
			msg := err.Error()
			if errors.Is(err, doc.ErrSourceChanged) || errors.Is(err, doc.ErrSourceLost) {
				doc.DiscardJournal(info)
			}
			ui.ShowMessageBox(c, T().Error, T().RecoverFailed(msg))
			c.RecoverJournals(then)
			return
		}
		c.dropDocument()
		c.showDocument(d)
		// The other journals are offered at the next start
	}, func() {
		doc.DiscardJournal(info)
		c.RecoverJournals(then)
	})
}
