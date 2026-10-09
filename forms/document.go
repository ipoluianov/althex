package forms

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/ipoluianov/althex/app"
	"github.com/ipoluianov/althex/config"
	"github.com/ipoluianov/althex/core/diff"
	"github.com/ipoluianov/althex/core/doc"
	"github.com/ipoluianov/althex/forms/hexview"
	"github.com/ipoluianov/nui/ui"
)

// syncing is set while the journals are being made durable in the background
var syncing atomic.Bool

func docOptions() doc.Options {
	return doc.Options{JournalDir: config.JournalDirectory()}
}

// openDialog asks for files to open
func (c *MainForm) openDialog() {
	dir := ""
	if d := c.doc(); d != nil && d.Path() != "" {
		dir = filepath.Dir(d.Path())
	}
	c.Form().ShowOpenFileDialog(ui.OpenFileDialogOptions{Title: T().MenuOpen, DefaultDirectory: dir, AllowMultiple: true},
		func(paths []string, err error) {
			if err != nil {
				ui.ShowMessageBox(c, T().Error, err.Error())
				return
			}
			c.OpenFiles(paths)
		})
}

// OpenFiles opens the files of the command line, a drop or a second start
func (c *MainForm) OpenFiles(paths []string) {
	for _, p := range paths {
		c.OpenFile(p)
	}
}

// OpenFile opens the file in a new tab, or shows the tab it is open in
func (c *MainForm) OpenFile(path string) {
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	for i, t := range c.tabs {
		if t.doc.Path() == path {
			c.selectTab(i)
			return
		}
	}
	d, err := doc.Open(path, docOptions())
	if err != nil {
		ui.ShowMessageBox(c, T().Error, T().OpenFailed(path, err.Error()))
		return
	}
	_ = config.AddRecentFile(path)
	c.detectTemplate(c.addTab(d))
}

// newDocument makes an empty document (SRC-5)
func (c *MainForm) newDocument() {
	d, err := doc.New(0, docOptions())
	if err != nil {
		ui.ShowMessageBox(c, T().Error, err.Error())
		return
	}
	c.addTab(d)
}

// newDocumentOfSize makes a document of zero bytes of a size asked for
func (c *MainForm) newDocumentOfSize() {
	c.ShowDialog(NewValueDialog(T().MenuNewSize, T().SizeLabel, "4096", func(s string) error {
		n, ok := ParseOffset(s)
		if !ok {
			return errors.New(T().GoToBad)
		}
		d, err := doc.New(n, docOptions())
		if err != nil {
			return err
		}
		c.addTab(d)
		return nil
	}))
}

// newDocumentFromClipboard makes a document of the clipboard: hex digits
// as bytes, other text as its UTF-8
func (c *MainForm) newDocumentFromClipboard() {
	text, err := ui.ClipboardGetText()
	if err != nil {
		ui.ShowMessageBox(c, T().Error, err.Error())
		return
	}
	data, ok := hexview.ParseHex(text)
	if !ok {
		data = []byte(text)
	}
	d, err := doc.FromBytes(data, docOptions())
	if err != nil {
		ui.ShowMessageBox(c, T().Error, err.Error())
		return
	}
	c.addTab(d)
}

// onView sets up a new view of a tab
func (c *MainForm) onView(t *Tab, v *hexview.View) {
	if size := config.GetSettings().FontSize; size > 0 {
		v.SetFontSize(size)
	}
	v.OnStateChanged = c.updateStatus
	v.OnError = func(err error) { ui.ShowToast(c, err.Error(), ui.ToastError) }
}

// addTab shows the document in a new tab and makes it the current one
func (c *MainForm) addTab(d *doc.Document) *Tab {
	t := newTab(d, c.viewOptions(), c.onView)
	t.syncScroll = config.GetSettings().View.SyncScroll
	d.Subscribe(func() { c.updateTabs() })
	c.tabs = append(c.tabs, t)
	c.selectTab(len(c.tabs) - 1)
	return t
}

// selectTab shows the i-th tab
func (c *MainForm) selectTab(i int) {
	if i < 0 || i >= len(c.tabs) {
		return
	}
	c.cur = i
	c.area.RemoveAllWidgets()
	t := c.tabs[i]
	c.area.AddWidget(0, 0, t.box)
	t.view().Focus()
	c.updateTabs()
	c.updateStatus()
	if f := c.Form(); f != nil {
		f.UpdateLayout()
	}
}

// cycleTab goes to the next tab (or the previous one), wrapping around
func (c *MainForm) cycleTab(step int) {
	if n := len(c.tabs); n > 1 {
		c.selectTab((c.cur + step + n) % n)
	}
}

// moveTab puts a tab at another place (dragged on the tab bar)
func (c *MainForm) moveTab(from, to int) {
	if from == to || from < 0 || to < 0 || from >= len(c.tabs) || to >= len(c.tabs) {
		return
	}
	t := c.tabs[from]
	c.tabs = append(c.tabs[:from], c.tabs[from+1:]...)
	c.tabs = append(c.tabs[:to], append([]*Tab{t}, c.tabs[to:]...)...)
	c.cur = to
	c.updateTabs()
}

// updateTabs shows the tabs in the tab bar and the title
func (c *MainForm) updateTabs() {
	items := make([]TabItem, len(c.tabs))
	for i, t := range c.tabs {
		items[i] = TabItem{Title: t.name(), Tooltip: t.doc.Path(), Modified: t.doc.Modified(), ReadOnly: t.doc.ReadOnly()}
	}
	c.tabBar.SetItems(items, c.cur)
	c.tabBar.SetVisible(len(c.tabs) > 0)
	c.UpdateTitle()
}

// closeTab closes the i-th tab, asking about its edits first, and then
// calls then (nil - nothing); canceled - not called
func (c *MainForm) closeTab(i int, then func()) {
	if c.saving || i < 0 || i >= len(c.tabs) {
		return
	}
	t := c.tabs[i]
	done := func() {
		c.dropTab(c.indexOf(t))
		if then != nil {
			then()
		}
	}
	if !t.doc.Modified() {
		done()
		return
	}
	c.selectTab(i)
	c.ShowDialog(NewSaveChangesDialog(t.name(), func() { c.save(t, false, done) }, done))
}

// closeAll closes the tabs one after another, then calls then
func (c *MainForm) closeAll(then func()) {
	if len(c.tabs) == 0 {
		if then != nil {
			then()
		}
		return
	}
	c.closeTab(len(c.tabs)-1, func() { c.closeAll(then) })
}

func (c *MainForm) indexOf(t *Tab) int {
	for i, x := range c.tabs {
		if x == t {
			return i
		}
	}
	return -1
}

// dropTab closes the tab without asking: its edits are lost
func (c *MainForm) dropTab(i int) {
	if i < 0 || i >= len(c.tabs) {
		return
	}
	t := c.tabs[i]
	c.tabs = append(c.tabs[:i], c.tabs[i+1:]...)
	t.close()
	if len(c.tabs) == 0 {
		c.cur = 0
		c.area.RemoveAllWidgets()
		c.area.AddWidget(0, 0, c.lblHint)
		c.updateTabs()
		c.updateStatus()
		if f := c.Form(); f != nil {
			f.UpdateLayout()
		}
		return
	}
	c.selectTab(min(i, len(c.tabs)-1))
}

// save saves the document of the tab in the background, asking for a name
// if it has none or saveAs, and then calls then (nil - nothing) if it was saved
func (c *MainForm) save(t *Tab, saveAs bool, then func()) {
	if t == nil || c.saving {
		return
	}
	d := t.doc
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
					c.saveTo(t, path, then)
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
	c.saveTo(t, "", then)
}

func (c *MainForm) saveTo(t *Tab, path string, then func()) {
	d := t.doc
	form := c.Form()
	c.saving = true
	for _, v := range t.views {
		v.SetBusy(true)
	}
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
			for _, v := range t.views {
				v.SetBusy(false)
			}
			c.updateTabs()
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

// updateStatus shows the state of the current document in the status bar
func (c *MainForm) updateStatus() {
	c.UpdateTitle()
	v := c.view()
	if v == nil {
		c.status.SetInfo("")
		c.side.refresh()
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
	parts = append(parts, c.encodingName())
	if v.Options().LittleEndian {
		parts = append(parts, "LE")
	} else {
		parts = append(parts, "BE")
	}
	if d.ReadOnly() {
		parts = append(parts, t.ReadOnly)
	}
	if tb := c.tab(); tb != nil && tb.cmp != nil && tb.cmp.ranges != nil {
		s := diff.Summarize(tb.cmp.ranges)
		parts = append(parts, t.DiffStatus(tb.cmp.cur+1, s.Ranges))
	}
	c.status.SetInfo(strings.Join(parts, "   ·   "))
	c.side.refresh()
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

// fileSeen holds the documents whose change on the disk was asked about
var fileSeen = map[*doc.Document]bool{}

// checkDocuments runs now and then: makes the journals durable and asks
// about files changed by another program (SRC-7)
func (c *MainForm) checkDocuments() {
	if c.saving {
		return
	}
	var modified []*doc.Document
	for _, t := range c.tabs {
		if t.doc.Modified() {
			modified = append(modified, t.doc)
		}
	}
	if len(modified) > 0 && syncing.CompareAndSwap(false, true) {
		go func() {
			for _, d := range modified {
				d.Sync()
			}
			syncing.Store(false)
		}()
	}
	for _, t := range c.tabs {
		d := t.doc
		if fileSeen[d] {
			continue
		}
		changed, err := d.FileChanged()
		if err != nil || !changed {
			continue
		}
		fileSeen[d] = true
		path := d.Path()
		ui.ShowQuestionMessageBoxYesNo(c, app.DisplayName, T().FileChangedAsk(filepath.Base(path)), func() {
			if i := c.indexOf(t); i >= 0 {
				c.dropTab(i)
			}
			c.OpenFile(path)
		}, nil)
		return // one question at a time
	}
}

// RecoverJournals offers to recover the edits left by a session that
// crashed, one journal after another; call it once the window is shown,
// before opening files. then is called at the end.
func (c *MainForm) RecoverJournals(then func()) {
	list, err := doc.FindJournals(config.JournalDirectory())
	// The journals of the documents open now are not offered
	var left []doc.JournalInfo
	for _, info := range list {
		open := false
		for _, t := range c.tabs {
			if t.doc.JournalDir() == info.Dir {
				open = true
			}
		}
		if !open {
			left = append(left, info)
		}
	}
	if err != nil || len(left) == 0 {
		then()
		return
	}
	info := left[0]
	name := T().Untitled
	if info.Path != "" {
		name = info.Path
	}
	ui.ShowQuestionMessageBoxYesNo(c, T().RecoverTitle, T().RecoverAsk(name, info.Modified.Format("2006-01-02 15:04")), func() {
		d, err := doc.Recover(info, docOptions())
		if err != nil {
			if errors.Is(err, doc.ErrSourceChanged) || errors.Is(err, doc.ErrSourceLost) {
				doc.DiscardJournal(info)
			}
			ui.ShowMessageBox(c, T().Error, T().RecoverFailed(err.Error()))
		} else {
			c.addTab(d)
		}
		c.RecoverJournals(then)
	}, func() {
		doc.DiscardJournal(info)
		c.RecoverJournals(then)
	})
}

// exportBookmarks saves the bookmarks of the current document to a JSON file
func (c *MainForm) exportBookmarks() {
	t := c.tab()
	if t == nil {
		return
	}
	c.Form().ShowSaveFileDialog(ui.SaveFileDialogOptions{Title: T().MenuExportBookmarks, DefaultFileName: t.name() + ".bookmarks.json"},
		func(path string, err error) {
			if err != nil || path == "" {
				return
			}
			bs, _ := json.MarshalIndent(t.bookmarks, "", "  ")
			if err := os.WriteFile(path, bs, 0644); err != nil {
				ui.ShowMessageBox(c, T().Error, err.Error())
			}
		})
}

// importBookmarks adds the bookmarks of a JSON file to the current document
func (c *MainForm) importBookmarks() {
	t := c.tab()
	if t == nil {
		return
	}
	c.Form().ShowOpenFileDialog(ui.OpenFileDialogOptions{Title: T().MenuImportBookmarks},
		func(paths []string, err error) {
			if err != nil || len(paths) == 0 {
				return
			}
			bs, err := os.ReadFile(paths[0])
			var list []Bookmark
			if err == nil {
				err = json.Unmarshal(bs, &list)
			}
			if err != nil {
				ui.ShowMessageBox(c, T().Error, err.Error())
				return
			}
			t.bookmarks = append(t.bookmarks, list...)
			c.update()
		})
}
