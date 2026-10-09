package forms

import (
	"context"
	"encoding/hex"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/ipoluianov/althex/config"
	checksum "github.com/ipoluianov/althex/core/hash"
	"github.com/ipoluianov/nui/ui"
)

// addPages adds the pages of the side panel after the interpreter
func (c *MainForm) addPages() {
	c.structure = newStructurePage(c)
	c.pages.structure = c.side.add(func() string { return T().PageStructure }, c.structure)
	c.console = newConsolePage(c)
	c.pages.console = c.side.add(func() string { return T().PageConsole }, c.console)
	c.pages.bookmarks = c.side.add(func() string { return T().PageBookmarks }, newBookmarksPage(c))
	c.pages.hashes = c.side.add(func() string { return T().PageHashes }, newHashesPage(c))
	c.search = newSearchPage(c)
	c.pages.search = c.side.add(func() string { return T().PageSearch }, c.search)
}

func (c *MainForm) registerPageCommands() {
	t := T
	c.cmd("panel.structure", func() string { return t().PageStructure }, "F6", func() { c.togglePage(c.pages.structure) }).
		Checked = func() bool { return c.pageShown(c.pages.structure) }
	c.cmd("panel.console", func() string { return t().PageConsole }, "Ctrl+`", func() { c.togglePage(c.pages.console) }).
		Checked = func() bool { return c.pageShown(c.pages.console) }
	c.cmd("template.apply", func() string { return t().MenuApplyTemplate }, "F5", func() {
		c.side.SetVisible(true)
		c.side.show(c.pages.structure)
		if f := c.Form(); f != nil {
			f.UpdateLayout()
		}
		c.structure.apply()
	})
	c.cmd("panel.bookmarks", func() string { return t().PageBookmarks }, "Ctrl+Shift+B", func() { c.togglePage(c.pages.bookmarks) }).
		Checked = func() bool { return c.pageShown(c.pages.bookmarks) }
	c.cmd("panel.hashes", func() string { return t().PageHashes }, "Ctrl+Shift+H", func() { c.togglePage(c.pages.hashes) }).
		Checked = func() bool { return c.pageShown(c.pages.hashes) }
	c.cmd("find.find", func() string { return t().MenuFind }, "Ctrl+F", c.showSearch)
	c.cmd("find.replace", func() string { return t().MenuReplace }, "Ctrl+H", c.showSearch)
	c.cmd("find.next", func() string { return t().FindNext }, "F3", func() { c.search.findNext(false) })
	c.cmd("find.prev", func() string { return t().FindPrev }, "Shift+F3", func() { c.search.findNext(true) })
	c.cmd("find.all", func() string { return t().FindAll }, "", func() { c.showSearch(); c.search.findAll() })
	c.cmd("find.strings", func() string { return t().Strings }, "", func() { c.showSearch(); c.search.strings() })
	c.cmd("find.clear", func() string { return t().ClearMarks }, "", func() {
		for _, tb := range c.tabs {
			tb.marks = nil
		}
		c.update()
	})
}

// showSearch shows the search page with the cursor in its field
func (c *MainForm) showSearch() {
	c.side.SetVisible(true)
	c.side.show(c.pages.search)
	if f := c.Form(); f != nil {
		f.UpdateLayout()
	}
	c.search.focus()
}

// pageCommandIDs are the commands of the Panels menu
func (c *MainForm) pageCommandIDs() []string {
	return []string{"panel.inspector", "panel.structure", "panel.console", "panel.bookmarks", "panel.hashes"}
}

// findCommandIDs are the commands of the Search menu
func (c *MainForm) findCommandIDs() []string {
	return []string{"find.find", "find.replace", "find.next", "find.prev", "-", "find.all", "find.strings", "find.clear"}
}

// ---- Bookmarks (NAV-2) ----

// BookmarksPage lists the bookmarks of the current document: a double click
// goes to one, the name and the comment are edited in place
type BookmarksPage struct {
	ui.Widget

	c       *MainForm
	table   *ui.Table
	filling bool
}

func newBookmarksPage(c *MainForm) *BookmarksPage {
	var p BookmarksPage
	p.InitWidget()
	p.SetPanelPadding(0)
	p.c = c
	p.table = ui.NewTable()
	p.table.SetColumnCount(4)
	for i, name := range []string{T().ColName, T().ColOffset, T().ColLength, T().ColComment} {
		p.table.SetColumnName(i, name)
	}
	p.table.SetColumnWidth(0, 120)
	p.table.SetColumnWidth(1, 100)
	p.table.SetColumnWidth(2, 70)
	p.table.SetStretchLastColumn(true)
	p.table.SetXExpandable(true)
	p.table.SetYExpandable(true)
	p.table.SetOnCellChanged(p.cellChanged)
	p.table.SetOnCellMouseDblClick(p.jump)
	p.AddWidget(0, 0, p.table)

	buttons := ui.NewPanel()
	buttons.SetPanelPadding(0)
	add := ui.NewButton("")
	add.SetTextFunc(func() string { return T().MenuToggleBookmark })
	add.SetOnClick(func() { c.runCommand("nav.bookmark"); p.refresh() })
	remove := ui.NewButton("")
	remove.SetTextFunc(func() string { return T().Remove })
	remove.SetOnClick(p.remove)
	buttons.AddWidget(0, 0, add)
	buttons.AddWidget(0, 1, remove)
	buttons.AddWidget(0, 2, ui.NewHSpacer())
	p.AddWidget(1, 0, buttons)
	return &p
}

func (p *BookmarksPage) refresh() {
	p.filling = true
	defer func() { p.filling = false }()
	t := p.c.tab()
	if t == nil {
		p.table.SetRowCount(0)
		return
	}
	p.table.SetRowCount(len(t.bookmarks))
	for i, b := range t.bookmarks {
		p.table.SetCellText2(i, 0, b.Name)
		p.table.SetCellText2(i, 1, fmt.Sprintf("%X", b.Off))
		p.table.SetCellText2(i, 2, strconv.FormatInt(b.Len, 10))
		p.table.SetCellText2(i, 3, b.Comment)
		p.table.SetCellColor(i, 0, bookmarkColor(b.Color))
		for col := 0; col < 4; col++ {
			editable := col == 0 || col == 3
			p.table.SetCellEditTriggerF2(i, col, editable)
			p.table.SetCellEditTriggerEnter(i, col, false)
			p.table.SetCellEditTriggerDoubleClick(i, col, false)
		}
	}
}

// selected returns the index of the bookmark of the current row; -1 - none
func (p *BookmarksPage) selected() int {
	t := p.c.tab()
	rows := p.table.SelectedRows()
	if t == nil || len(rows) == 0 || rows[0] >= len(t.bookmarks) {
		return -1
	}
	return rows[0]
}

func (p *BookmarksPage) jump() {
	if i := p.selected(); i >= 0 {
		t := p.c.tab()
		b := t.bookmarks[i]
		t.jump(b.Off)
		t.view().Select(b.Off, b.Len)
	}
}

func (p *BookmarksPage) remove() {
	if i := p.selected(); i >= 0 {
		t := p.c.tab()
		t.bookmarks = append(t.bookmarks[:i], t.bookmarks[i+1:]...)
		p.refresh()
		p.c.update()
	}
}

func (p *BookmarksPage) cellChanged(row, col int, text string, data interface{}) bool {
	t := p.c.tab()
	if p.filling || t == nil || row >= len(t.bookmarks) {
		return true
	}
	switch col {
	case 0:
		t.bookmarks[row].Name = text
	case 3:
		t.bookmarks[row].Comment = text
	default:
		return false
	}
	return true
}

// ---- Hashes and statistics (ANL-7, ANL-8) ----

// HashesPage computes the checked checksums and the entropy and histogram
// of the selection or of the whole document, in the background
type HashesPage struct {
	ui.Widget

	c        *MainForm
	cmbRange *ui.ComboBox
	checks   []*ui.Checkbox
	algs     []checksum.Algorithm
	txtCRC   *ui.TextBox
	btnRun   *ui.Button
	progress *ui.ProgressBar
	results  *ui.Table
	lblStats *ui.Label
	histo    *ui.Space

	cancel context.CancelFunc
	stats  *checksum.Stats
	lines  []string // the results as text, for copying
}

func newHashesPage(c *MainForm) *HashesPage {
	var p HashesPage
	p.InitWidget()
	p.SetPanelPadding(0)
	p.c = c

	top := ui.NewPanel()
	top.SetPanelPadding(0)
	top.AddWidget(0, 0, ui.NewLabel(T().Range))
	p.cmbRange = ui.NewComboBox()
	p.cmbRange.AddItem(T().RangeSelection, "sel")
	p.cmbRange.AddItem(T().RangeDocument, "all")
	p.cmbRange.SetSelectedIndex(0)
	top.AddWidget(0, 1, p.cmbRange)
	top.AddWidget(0, 2, ui.NewHSpacer())
	p.AddWidget(0, 0, top)

	// The algorithms, then the CRC presets the list does not have
	p.algs = checksum.Algorithms()
	for _, preset := range checksum.CRCPresets() {
		dup := false
		for _, a := range p.algs {
			if a.Name == preset.Name {
				dup = true
			}
		}
		if !dup {
			p.algs = append(p.algs, checksum.CRCAlgorithm(preset))
		}
	}
	ticked := config.GetSettings().Hashes
	if ticked == nil {
		ticked = []string{"crc32", "md5", "sha1", "sha256"}
	}
	list := ui.NewScrollArea()
	list.SetMinHeight(150)
	list.SetMaxHeight(220)
	for i, a := range p.algs {
		chk := ui.NewCheckbox(a.Name)
		chk.SetChecked(slices.Contains(ticked, a.ID) || slices.Contains(ticked, a.Name))
		chk.SetOnStateChanged(p.saveTicked)
		p.checks = append(p.checks, chk)
		list.AddWidget(i/2, i%2, chk)
	}
	p.AddWidget(1, 0, list)

	crcRow := ui.NewPanel()
	crcRow.SetPanelPadding(0)
	crcRow.AddWidget(0, 0, ui.NewLabel(T().CustomCRC))
	p.txtCRC = ui.NewTextBox()
	p.txtCRC.SetXExpandable(true)
	crcRow.AddWidget(0, 1, p.txtCRC)
	p.AddWidget(2, 0, crcRow)

	run := ui.NewPanel()
	run.SetPanelPadding(0)
	p.btnRun = ui.NewButton(T().Calculate)
	p.btnRun.SetOnClick(p.run)
	run.AddWidget(0, 0, p.btnRun)
	p.progress = ui.NewProgressBar(0, 100, 0)
	p.progress.SetXExpandable(true)
	run.AddWidget(0, 1, p.progress)
	copyBtn := ui.NewButton(T().MenuCopy)
	copyBtn.SetOnClick(func() { ui.ClipboardSetText(strings.Join(p.lines, "\n")) })
	run.AddWidget(0, 2, copyBtn)
	p.AddWidget(3, 0, run)

	p.results = ui.NewTable()
	p.results.SetColumnCount(2)
	p.results.SetColumnName(0, T().ColAlgorithm)
	p.results.SetColumnName(1, T().ColValue)
	p.results.SetColumnWidth(0, 130)
	p.results.SetStretchLastColumn(true)
	p.results.SetXExpandable(true)
	p.results.SetYExpandable(true)
	p.AddWidget(4, 0, p.results)

	p.lblStats = ui.NewLabel("")
	p.AddWidget(5, 0, p.lblStats)
	p.histo = ui.NewSpace()
	p.histo.SetMinHeight(80)
	p.histo.SetMaxHeight(80)
	p.histo.SetXExpandable(true)
	p.histo.SetOnPaint(p.paintHistogram)
	p.AddWidget(6, 0, p.histo)
	return &p
}

func (p *HashesPage) refresh() {}

func (p *HashesPage) saveTicked() {
	var ids []string
	for i, chk := range p.checks {
		if chk.Checked() {
			ids = append(ids, p.algs[i].ID)
		}
	}
	s := config.GetSettings()
	s.Hashes = append([]string{}, ids...)
	if s.Hashes == nil {
		s.Hashes = []string{}
	}
	config.SetSettings(s)
}

// parseCRC reads a CRC in the notation of the CRC catalogue:
// "width=16 poly=0x1021 init=0xffff refin=false refout=false xorout=0x0000"
func parseCRC(s string) (checksum.CRCParams, error) {
	p := checksum.CRCParams{Name: "CRC (" + strings.TrimSpace(s) + ")"}
	seen := false
	for _, f := range strings.Fields(s) {
		k, v, ok := strings.Cut(strings.ToLower(f), "=")
		if !ok {
			return p, fmt.Errorf("%q: name=value expected", f)
		}
		num := func() (uint64, error) { return strconv.ParseUint(v, 0, 64) }
		var err error
		var n uint64
		switch k {
		case "width":
			n, err = num()
			p.Width = int(n)
			seen = true
		case "poly":
			p.Poly, err = num()
		case "init":
			p.Init, err = num()
		case "xorout":
			p.XorOut, err = num()
		case "refin":
			p.RefIn, err = strconv.ParseBool(v)
		case "refout":
			p.RefOut, err = strconv.ParseBool(v)
		case "check", "residue", "name":
		default:
			return p, fmt.Errorf("%q: unknown parameter", k)
		}
		if err != nil {
			return p, fmt.Errorf("%s: %v", k, err)
		}
	}
	if !seen || p.Width < 1 || p.Width > 64 {
		return p, fmt.Errorf("width=1..64 expected")
	}
	return p, nil
}

// run starts computing, or cancels it
func (p *HashesPage) run() {
	if p.cancel != nil {
		p.cancel()
		return
	}
	t := p.c.tab()
	if t == nil {
		return
	}
	var algs []checksum.Algorithm
	for i, chk := range p.checks {
		if chk.Checked() {
			algs = append(algs, p.algs[i])
		}
	}
	if s := strings.TrimSpace(p.txtCRC.Text()); s != "" {
		params, err := parseCRC(s)
		if err != nil {
			ui.ShowToast(p.c, err.Error(), ui.ToastError)
			return
		}
		algs = append(algs, checksum.CRCAlgorithm(params))
	}
	snap := t.doc.Snapshot()
	off, n := int64(0), snap.Size()
	if sel, k := t.view().Selection(); k > 0 && p.cmbRange.SelectedItemData() == "sel" {
		off, n = sel, k
	}
	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel
	p.btnRun.SetText(T().Cancel)
	p.progress.SetValue(0)
	form := p.c.Form()

	// Two passes at once over the same bytes: the hashes and the statistics;
	// the progress is of the slower one
	var hashDone, statDone, lastShown atomic.Int64
	show := func() {
		now := time.Now().UnixMilli()
		if now-lastShown.Load() < 100 || n == 0 {
			return
		}
		lastShown.Store(now)
		pct := float64(min(hashDone.Load(), statDone.Load())) * 100 / float64(n)
		form.Invoke(func() { p.progress.SetValue(pct) })
	}
	go func() {
		start := time.Now()
		var sums [][]byte
		var stats checksum.Stats
		var hashErr, statErr error
		done := make(chan struct{})
		go func() {
			stats, statErr = checksum.Analyze(ctx, snap, off, n, func(d int64) { statDone.Store(d); show() })
			close(done)
		}()
		if len(algs) > 0 {
			sums, hashErr = checksum.Compute(ctx, snap, off, n, algs, func(d int64) { hashDone.Store(d); show() })
		} else {
			hashDone.Store(n)
		}
		<-done
		took := time.Since(start)
		form.Invoke(func() {
			p.cancel = nil
			cancel()
			p.btnRun.SetText(T().Calculate)
			err := hashErr
			if err == nil {
				err = statErr
			}
			if err != nil {
				if ctx.Err() == nil {
					ui.ShowMessageBox(p.c, T().Error, err.Error())
				}
				p.progress.SetValue(0)
				return
			}
			p.progress.SetValue(100)
			p.show(algs, sums, &stats, off, n, took)
		})
	}()
}

func (p *HashesPage) show(algs []checksum.Algorithm, sums [][]byte, stats *checksum.Stats, off, n int64, took time.Duration) {
	p.results.SetRowCount(len(algs))
	p.lines = p.lines[:0]
	for i, a := range algs {
		v := strings.ToUpper(hex.EncodeToString(sums[i]))
		p.results.SetCellText2(i, 0, a.Name)
		p.results.SetCellText2(i, 1, v)
		p.lines = append(p.lines, a.Name+": "+v)
	}
	p.stats = stats
	text := T().StatsLine(off, n, stats.Entropy(), took.Seconds())
	p.lblStats.SetText(text)
	p.lines = append(p.lines, text)
	p.c.update()
}

// paintHistogram draws how many times each byte value occurs
func (p *HashesPage) paintHistogram(cnv *ui.Canvas) {
	w, h := p.histo.Width(), p.histo.Height()
	pal := ui.CurrentPalette()
	cnv.FillRect(0, 0, w, h, pal.Base)
	if p.stats == nil || p.stats.Count == 0 {
		return
	}
	top := int64(1)
	for _, k := range p.stats.Histogram {
		top = max(top, k)
	}
	for b, k := range p.stats.Histogram {
		bh := int(int64(h-2) * k / top)
		if k > 0 && bh == 0 {
			bh = 1
		}
		x0 := b * w / 256
		x1 := (b + 1) * w / 256
		cnv.FillRect(x0, h-bh, max(1, x1-x0), bh, pal.Highlight)
	}
}
