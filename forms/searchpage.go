package forms

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/ipoluianov/althex/core/codec"
	"github.com/ipoluianov/althex/core/doc"
	"github.com/ipoluianov/althex/core/interp"
	"github.com/ipoluianov/althex/core/search"
	"github.com/ipoluianov/althex/forms/hexview"
	"github.com/ipoluianov/nui/ui"
)

// maxResults is how many results the list keeps (FIND-7)
const maxResults = 100000

// result is a found range of a document
type result struct {
	tab  *Tab
	off  int64
	n    int
	text string // a string found by Strings; "" - a match
}

// The kinds of what is searched for, in the order of the combo box
const (
	findHex = iota
	findText
	findNumber // followed by the number types
)

var numKinds = []string{"int8", "uint8", "int16", "uint16", "int32", "uint32", "int64", "uint64", "float32", "float64"}

// SearchPage finds bytes, text or numbers in the current document or in
// all of them, replaces them, and extracts strings (FIND-1..8)
type SearchPage struct {
	ui.Widget

	c          *MainForm
	cmbKind    *ui.ComboBox
	txtFind    *ui.TextBox
	txtReplace *ui.TextBox
	chkCase    *ui.Checkbox
	chkBE      *ui.Checkbox
	chkAll     *ui.Checkbox
	txtMinLen  *ui.TextBox
	progress   *ui.ProgressBar
	lblInfo    *ui.Label
	table      *ui.Table
	btnStop    *ui.Button

	results []result
	cancel  context.CancelFunc
}

func newSearchPage(c *MainForm) *SearchPage {
	var p SearchPage
	p.InitWidget()
	p.SetPanelPadding(0)
	p.c = c
	t := T()

	grid := ui.NewPanel()
	grid.SetPanelPadding(0)
	p.cmbKind = ui.NewComboBox()
	p.cmbKind.AddItem(t.FindHex, findHex)
	p.cmbKind.AddItem(t.FindText, findText)
	for i, k := range numKinds {
		p.cmbKind.AddItem(k, findNumber+i)
	}
	p.cmbKind.SetSelectedIndex(0)
	grid.AddWidget(0, 0, p.cmbKind)
	p.txtFind = ui.NewTextBox()
	p.txtFind.SetXExpandable(true)
	grid.AddWidget(0, 1, p.txtFind)
	grid.AddWidget(1, 0, ui.NewLabel(t.ReplaceWith))
	p.txtReplace = ui.NewTextBox()
	p.txtReplace.SetXExpandable(true)
	grid.AddWidget(1, 1, p.txtReplace)
	p.AddWidget(0, 0, grid)

	opts := ui.NewPanel()
	opts.SetPanelPadding(0)
	p.chkCase = ui.NewCheckbox(t.IgnoreCase)
	p.chkBE = ui.NewCheckbox(t.BigEndian)
	p.chkAll = ui.NewCheckbox(t.AllDocuments)
	opts.AddWidget(0, 0, p.chkCase)
	opts.AddWidget(0, 1, p.chkBE)
	opts.AddWidget(0, 2, p.chkAll)
	opts.AddWidget(0, 3, ui.NewHSpacer())
	p.AddWidget(1, 0, opts)

	buttons := ui.NewPanel()
	buttons.SetPanelPadding(0)
	col := 0
	addButton := func(row int, text string, f func()) {
		b := ui.NewButton(text)
		b.SetOnClick(f)
		buttons.AddWidget(row, col, b)
		col++
	}
	addButton(0, t.FindNext, func() { p.findNext(false) })
	addButton(0, t.FindPrev, func() { p.findNext(true) })
	addButton(0, t.FindAll, p.findAll)
	col = 0
	addButton(1, t.Replace, p.replaceOne)
	addButton(1, t.ReplaceAll, p.replaceAll)
	addButton(1, t.Strings, p.strings)
	p.txtMinLen = ui.NewTextBox()
	p.txtMinLen.SetText("6")
	p.txtMinLen.SetMinWidth(40)
	p.txtMinLen.SetMaxWidth(50)
	buttons.AddWidget(1, col, p.txtMinLen)
	buttons.AddWidget(0, 4, ui.NewHSpacer())
	p.AddWidget(2, 0, buttons)

	run := ui.NewPanel()
	run.SetPanelPadding(0)
	p.progress = ui.NewProgressBar(0, 100, 0)
	p.progress.SetXExpandable(true)
	run.AddWidget(0, 0, p.progress)
	p.btnStop = ui.NewButton(t.Cancel)
	p.btnStop.SetOnClick(func() {
		if p.cancel != nil {
			p.cancel()
		}
	})
	run.AddWidget(0, 1, p.btnStop)
	export := ui.NewButton(t.Export)
	export.SetOnClick(p.export)
	run.AddWidget(0, 2, export)
	p.AddWidget(3, 0, run)
	p.lblInfo = ui.NewLabel("")
	p.AddWidget(4, 0, p.lblInfo)

	p.table = ui.NewTable()
	p.table.SetColumnCount(3)
	p.table.SetColumnName(0, t.ColDocument)
	p.table.SetColumnName(1, t.ColOffset)
	p.table.SetColumnName(2, t.ColFound)
	p.table.SetColumnWidth(0, 110)
	p.table.SetColumnWidth(1, 100)
	p.table.SetStretchLastColumn(true)
	p.table.SetXExpandable(true)
	p.table.SetYExpandable(true)
	p.table.SetOnCellMouseDblClick(p.jumpToSelected)
	p.AddWidget(5, 0, p.table)
	return &p
}

func (p *SearchPage) refresh() {}

// focus puts the cursor into the field of what to find, with the selection in it
func (p *SearchPage) focus() {
	if v := p.c.view(); v != nil {
		if off, n := v.Selection(); n > 0 && n <= 64 {
			b := make([]byte, n)
			v.Document().ReadAt(b, off)
			if v.InTextColumn() {
				p.cmbKind.SetSelectedIndex(1)
				p.txtFind.SetText(string(b))
			} else {
				p.cmbKind.SetSelectedIndex(0)
				p.txtFind.SetText(codec.FormatHex(b, " "))
			}
		}
	}
	p.txtFind.Focus()
	p.txtFind.SelectAllText()
}

// pattern makes what to find from the fields
func (p *SearchPage) pattern() (search.Pattern, error) {
	s := p.txtFind.Text()
	if strings.TrimSpace(s) == "" {
		return search.Pattern{}, errors.New(T().NothingToFind)
	}
	kind := p.cmbKind.SelectedItemData().(int)
	switch {
	case kind == findHex:
		return search.ParseHex(s)
	case kind == findText:
		return search.Text(s, currentEncoding().Encode, p.chkCase.Checked())
	}
	k, _ := search.ParseNumKind(numKinds[kind-findNumber])
	return search.Number(k, strings.TrimSpace(s), p.chkBE.Checked())
}

// replacement makes the new bytes from the field: hex for hex, text in the
// encoding of the view for text, the number for numbers
func (p *SearchPage) replacement() ([]byte, error) {
	s := p.txtReplace.Text()
	kind := p.cmbKind.SelectedItemData().(int)
	switch {
	case kind == findHex:
		return codec.ParseHex(s)
	case kind == findText:
		return currentEncoding().Encode(s)
	}
	return interp.Encode(numKinds[kind-findNumber], strings.TrimSpace(s), p.chkBE.Checked())
}

func (p *SearchPage) fail(err error) {
	ui.ShowToast(p.c, err.Error(), ui.ToastError)
}

// background runs a search, with the progress shown and the Cancel button
func (p *SearchPage) background(total int64, work func(ctx context.Context, progress func(done int64)) error, done func(err error, took time.Duration)) {
	if p.cancel != nil {
		p.cancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel
	form := p.c.Form()
	p.progress.SetValue(0)
	var lastShown atomic.Int64
	progress := func(d int64) {
		now := time.Now().UnixMilli()
		if now-lastShown.Load() < 100 || total <= 0 {
			return
		}
		lastShown.Store(now)
		pct := float64(d) * 100 / float64(total)
		form.Invoke(func() { p.progress.SetValue(pct) })
	}
	go func() {
		start := time.Now()
		err := work(ctx, progress)
		took := time.Since(start)
		form.Invoke(func() {
			if p.cancel != nil {
				p.cancel()
				p.cancel = nil
			}
			p.progress.SetValue(100)
			if ctx.Err() != nil && err != nil {
				err = nil // canceled: what was found stays
			}
			done(err, took)
		})
	}()
}

// findNext finds the next match after the caret (or the previous one),
// wrapping around the end of the document
func (p *SearchPage) findNext(back bool) {
	t := p.c.tab()
	if t == nil {
		return
	}
	pat, err := p.pattern()
	if err != nil {
		p.fail(err)
		return
	}
	v := t.view()
	snap := t.doc.Snapshot()
	from := v.Caret()
	if off, n := v.Selection(); n > 0 && !back {
		from = off + 1
	} else if n > 0 {
		from = off
	}
	var hit search.Hit
	var found, wrapped bool
	p.background(snap.Size(), func(ctx context.Context, _ func(int64)) error {
		var err error
		hit, found, err = search.FindNext(ctx, snap, snap.Size(), pat, from, back)
		if err == nil && !found {
			wrapped = true
			start := int64(0)
			if back {
				start = snap.Size()
			}
			hit, found, err = search.FindNext(ctx, snap, snap.Size(), pat, start, back)
		}
		return err
	}, func(err error, _ time.Duration) {
		switch {
		case err != nil:
			p.fail(err)
		case !found:
			ui.ShowToast(p.c, T().NotFound, ui.ToastInfo)
		default:
			if wrapped {
				ui.ShowToast(p.c, T().Wrapped, ui.ToastInfo)
			}
			t.jump(hit.Off)
			t.view().Select(hit.Off, int64(hit.Len))
		}
	})
}

// tabsToSearch are the documents to search: the current one or all (FIND-5)
func (p *SearchPage) tabsToSearch() []*Tab {
	if p.chkAll.Checked() {
		return append([]*Tab{}, p.c.tabs...)
	}
	if t := p.c.tab(); t != nil {
		return []*Tab{t}
	}
	return nil
}

// findAll lists all the matches and marks them in the documents
func (p *SearchPage) findAll() {
	tabs := p.tabsToSearch()
	if len(tabs) == 0 {
		return
	}
	pat, err := p.pattern()
	if err != nil {
		p.fail(err)
		return
	}
	snaps := make([]*doc.Snapshot, len(tabs))
	total := int64(0)
	for i, t := range tabs {
		snaps[i] = t.doc.Snapshot()
		total += snaps[i].Size()
	}
	var found []result
	truncated := false
	p.background(total, func(ctx context.Context, progress func(int64)) error {
		base := int64(0)
		for i, snap := range snaps {
			left := maxResults - len(found)
			hits, more, err := search.Collect(ctx, snap, snap.Size(), pat, search.Options{}, left,
				func(d int64) { progress(base + d) })
			for _, h := range hits {
				found = append(found, result{tab: tabs[i], off: h.Off, n: h.Len})
			}
			if err != nil {
				return err
			}
			if more || len(found) >= maxResults {
				truncated = true
				return nil
			}
			base += snap.Size()
		}
		return nil
	}, func(err error, took time.Duration) {
		if err != nil {
			p.fail(err)
		}
		p.show(found, truncated, took)
	})
}

// show puts the results into the list and the marks on the documents
func (p *SearchPage) show(found []result, truncated bool, took time.Duration) {
	p.results = found
	p.lblInfo.SetText(T().FoundCount(len(found), truncated, took.Seconds()))
	p.table.SetRowCount(len(found))
	for i, r := range found {
		p.table.SetCellText2(i, 0, r.tab.name())
		p.table.SetCellText2(i, 1, fmt.Sprintf("%X", r.off))
		text := r.text
		if text == "" {
			b := make([]byte, min(r.n, 32))
			r.tab.doc.ReadAt(b, r.off)
			text = codec.FormatHex(b, " ")
		}
		p.table.SetCellText2(i, 2, text)
	}
	// The marks of each document, sorted for the views
	byTab := map[*Tab][]hexview.Highlight{}
	for _, r := range found {
		byTab[r.tab] = append(byTab[r.tab], hexview.Highlight{Off: r.off, Len: int64(max(r.n, 1)), Color: colorFound.get()})
	}
	for _, t := range p.c.tabs {
		marks := byTab[t]
		sort.Slice(marks, func(i, j int) bool { return marks[i].Off < marks[j].Off })
		t.marks = markProvider(marks)
	}
	p.c.update()
}

// markProvider returns the marks of a range of sorted marks
func markProvider(marks []hexview.Highlight) func(from, to int64) []hexview.Highlight {
	if len(marks) == 0 {
		return nil
	}
	return func(from, to int64) []hexview.Highlight {
		// The marks are sorted by offset; one ending in the range starts before it by its length at most
		i := sort.Search(len(marks), func(i int) bool { return marks[i].Off+marks[i].Len > from })
		var out []hexview.Highlight
		for ; i < len(marks) && marks[i].Off < to; i++ {
			out = append(out, marks[i])
		}
		return out
	}
}

func (p *SearchPage) jumpToSelected() {
	rows := p.table.SelectedRows()
	if len(rows) == 0 || rows[0] >= len(p.results) {
		return
	}
	r := p.results[rows[0]]
	i := p.c.indexOf(r.tab)
	if i < 0 {
		return
	}
	p.c.selectTab(i)
	r.tab.jump(r.off)
	r.tab.view().Select(r.off, int64(r.n))
}

// replaceOne replaces the selection if it is a match, then finds the next one
func (p *SearchPage) replaceOne() {
	t := p.c.tab()
	if t == nil {
		return
	}
	pat, err := p.pattern()
	if err != nil {
		p.fail(err)
		return
	}
	repl, err := p.replacement()
	if err != nil {
		p.fail(err)
		return
	}
	v := t.view()
	off, n := v.Selection()
	if n == int64(pat.Len()) {
		hit, ok, _ := search.FindNext(context.Background(), t.doc.Snapshot(), t.doc.Size(), pat, off, false)
		if ok && hit.Off == off {
			if err := replaceAt(t.doc, off, int(n), repl); err != nil {
				p.fail(err)
				return
			}
			v.GoTo(off + int64(len(repl)))
		}
	}
	p.findNext(false)
}

func replaceAt(d *doc.Document, off int64, n int, repl []byte) error {
	opt := doc.EditOptions{Kind: "replace"}
	if len(repl) == n {
		return d.Overwrite(off, repl, opt)
	}
	d.BeginGroup("replace")
	defer d.EndGroup()
	if err := d.Delete(off, int64(n), opt); err != nil {
		return err
	}
	return d.Insert(off, repl, opt)
}

// replaceAll replaces every match of the current document, as one edit
func (p *SearchPage) replaceAll() {
	t := p.c.tab()
	if t == nil {
		return
	}
	pat, err := p.pattern()
	if err != nil {
		p.fail(err)
		return
	}
	repl, err := p.replacement()
	if err != nil {
		p.fail(err)
		return
	}
	count := 0
	p.c.runOp("replace", t.doc.Size(), func(ctx context.Context, d *doc.Document, progress func(int64)) error {
		snap := d.Snapshot()
		var hits []search.Hit
		next := int64(0)
		err := search.FindAll(ctx, snap, snap.Size(), pat, search.Options{}, func(h search.Hit) bool {
			// Overlapping matches: the first one wins
			if h.Off >= next {
				hits = append(hits, h)
				next = h.Off + int64(h.Len)
			}
			return true
		}, progress)
		if err != nil {
			return err
		}
		// From the end, so the offsets of the ones before do not move
		for i := len(hits) - 1; i >= 0; i-- {
			if err := replaceAt(d, hits[i].Off, hits[i].Len, repl); err != nil {
				return err
			}
		}
		count = len(hits)
		p.c.Form().Invoke(func() { ui.ShowToast(p.c, T().ReplacedCount(count), ui.ToastSuccess) })
		return nil
	})
}

// strings lists the strings of the documents (FIND-8): ASCII and UTF-16 LE
func (p *SearchPage) strings() {
	tabs := p.tabsToSearch()
	if len(tabs) == 0 {
		return
	}
	minLen := 6
	fmt.Sscan(p.txtMinLen.Text(), &minLen)
	snaps := make([]*doc.Snapshot, len(tabs))
	total := int64(0)
	for i, t := range tabs {
		snaps[i] = t.doc.Snapshot()
		total += snaps[i].Size()
	}
	var found []result
	truncated := false
	p.background(total, func(ctx context.Context, progress func(int64)) error {
		base := int64(0)
		for i, snap := range snaps {
			opt := search.StringsOptions{MinLen: max(minLen, 2), ASCII: true, UTF16LE: true}
			err := search.Strings(ctx, snap, snap.Size(), opt, func(f search.Found) bool {
				found = append(found, result{tab: tabs[i], off: f.Off, n: f.Len, text: f.Text})
				truncated = len(found) >= maxResults
				return !truncated
			}, func(d int64) { progress(base + d) })
			if err != nil || truncated {
				return err
			}
			base += snap.Size()
		}
		return nil
	}, func(err error, took time.Duration) {
		if err != nil {
			p.fail(err)
		}
		p.show(found, truncated, took)
	})
}

// export saves the results to a text file: the document, the offset, what was found
func (p *SearchPage) export() {
	if len(p.results) == 0 {
		return
	}
	p.c.Form().ShowSaveFileDialog(ui.SaveFileDialogOptions{Title: T().Export, DefaultFileName: "results.txt"},
		func(path string, err error) {
			if err != nil || path == "" {
				return
			}
			var sb strings.Builder
			for _, r := range p.results {
				text := r.text
				if text == "" {
					b := make([]byte, min(r.n, 64))
					r.tab.doc.ReadAt(b, r.off)
					text = codec.FormatHex(b, " ")
				}
				fmt.Fprintf(&sb, "%s\t%X\t%d\t%s\n", r.tab.doc.Path(), r.off, r.n, text)
			}
			if err := os.WriteFile(path, []byte(sb.String()), 0644); err != nil {
				ui.ShowMessageBox(p.c, T().Error, err.Error())
			}
		})
}
