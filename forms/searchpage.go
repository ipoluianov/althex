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
	cmbEnc     *ui.ComboBox // the encoding of a text: the view's by default
	encTouched bool         // the user chose the encoding: it stays
	progress   *ui.ProgressBar
	lblInfo    *ui.Label
	table      *ui.Table
	btnStop    *ui.Button

	results []result
	cancel  context.CancelFunc
	// countCancel stops counting the matches of the last search
	countCancel context.CancelFunc
	// matchTab is the tab the "Match 2 of 5" shown is of
	matchTab *Tab
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
	p.cmbKind.SetOnSelectedIndexChanged(p.kindChanged)
	grid.AddWidget(0, 0, p.cmbKind)
	p.txtFind = ui.NewTextBox()
	p.txtFind.SetXExpandable(true)
	p.txtFind.SetHintFunc(p.hint)
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
	p.cmbEnc = ui.NewComboBox()
	for _, e := range codec.Encodings() {
		p.cmbEnc.AddItem(e.Name(), e.ID())
	}
	p.cmbEnc.SetOnSelectedIndexChanged(func() { p.encTouched = true })
	// Two rows: the options of a kind do not widen the panel
	opts.AddWidget(0, 0, p.chkCase)
	opts.AddWidget(0, 1, p.cmbEnc)
	opts.AddWidget(0, 2, p.chkBE)
	opts.AddWidget(0, 3, ui.NewHSpacer())
	opts.AddWidget(1, 0, p.chkAll)
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
	// The shortest string Strings lists
	minLen := ui.NewLabel(t.MinLengthLabel)
	buttons.AddWidget(1, col, minLen)
	col++
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
	// Enter in the fields finds the next match, Shift+Enter the previous one
	p.SetOnKeyDown(func(key ui.Key, mods ui.KeyModifiers) bool {
		if key != ui.KeyEnter || mods.Ctrl || mods.Alt {
			return false
		}
		p.findNext(mods.Shift)
		return true
	})
	p.kindChanged()
	return &p
}

// hint is what the field of what to find takes, by the kind
func (p *SearchPage) hint() string {
	switch p.kind() {
	case findHex:
		return T().FindHintHex
	case findText:
		return T().FindHintText
	}
	return T().FindHintNumber
}

func (p *SearchPage) kind() int {
	k, _ := p.cmbKind.SelectedItemData().(int)
	return k
}

// kindChanged shows the options that mean something for the kind: the case
// and the encoding for text, the byte order for numbers
func (p *SearchPage) kindChanged() {
	k := p.kind()
	p.txtFind.SetHintFunc(p.hint) // set again: it is read when set
	p.chkCase.SetVisible(k == findText)
	p.cmbEnc.SetVisible(k == findText)
	p.chkBE.SetVisible(k >= findNumber)
	if f := p.Form(); f != nil {
		f.UpdateLayout()
	}
}

// encoding is the encoding of the text searched for
func (p *SearchPage) encoding() codec.Encoding {
	if id, _ := p.cmbEnc.SelectedItemData().(string); id != "" {
		if e := codec.ByID(id); e != nil {
			return e
		}
	}
	return currentEncoding()
}

func (p *SearchPage) refresh() {
	// "Match 2 of 5" of another document is not shown
	if p.matchTab != nil && p.matchTab != p.c.tab() {
		p.matchTab = nil
		p.lblInfo.SetText("")
	}
}

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
	if !p.encTouched {
		cur := currentEncoding().ID()
		for i, e := range codec.Encodings() { // the order of the items
			if e.ID() == cur {
				p.cmbEnc.SetSelectedIndex(i)
			}
		}
		p.encTouched = false
	}
	p.kindChanged()
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
		return search.Text(s, p.encoding().Encode, p.chkCase.Checked())
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
		return p.encoding().Encode(s)
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
			p.progress.SetValue(0) // a full bar at rest looks like a button
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
			t.view().SelectFound(hit.Off, int64(hit.Len))
			p.countMatches(snap, pat, hit.Off)
		}
	})
}

// maxCounted is how many matches countMatches counts before it stops
const maxCounted = 100000

// countMatches shows which of the matches of the document the one at off
// is, counted in the background: "Match 2 of 5"
func (p *SearchPage) countMatches(snap *doc.Snapshot, pat search.Pattern, off int64) {
	if p.countCancel != nil {
		p.countCancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	p.countCancel = cancel
	p.lblInfo.SetText("")
	p.matchTab = nil
	tab := p.c.tab()
	form := p.c.Form()
	go func() {
		index, total := 0, 0
		err := search.FindAll(ctx, snap, snap.Size(), pat, search.Options{MaxHits: maxCounted}, func(h search.Hit) bool {
			total++
			if h.Off <= off {
				index++
			}
			return true
		}, nil)
		if err != nil || ctx.Err() != nil || form == nil {
			return
		}
		form.Invoke(func() {
			if ctx.Err() == nil && tab == p.c.tab() {
				p.lblInfo.SetText(T().MatchOf(index, total, total >= maxCounted))
				p.matchTab = tab
			}
		})
	}()
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
	r.tab.view().SelectFound(r.off, int64(r.n))
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
