package forms

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ipoluianov/althex/core/diff"
	"github.com/ipoluianov/althex/core/doc"
	"github.com/ipoluianov/althex/core/patch"
	"github.com/ipoluianov/althex/forms/hexview"
	"github.com/ipoluianov/nui/ui"
)

// compareMaxSpans bounds the differences kept of a comparison byte by byte
const compareMaxSpans = 1 << 20

// Compare is the state of a tab that compares documents (DIFF-1..3): the
// document of the tab with one other (with shifts) or with several others
// byte by byte, each in a view of its own. The documents are compared as
// they were when the comparison started, with their edits not saved.
type Compare struct {
	// docs are the documents compared, as the views show them; the first
	// one is the document of the tab, the others are compared with it
	docs []*doc.Document
	// from are the documents of the tabs that docs[i] are taken of (nil -
	// a file opened for the comparison): the comparison closes with them
	from []*doc.Document
	// end cancels the comparison running; nil - none runs
	end func()

	shifts bool
	// With shifts: the ranges of the two documents, diffs the indexes of
	// the differing ones
	ranges []diff.Range
	diffs  []int
	// Byte by byte: the spans where any document differs from the first
	// one, and the differences one after another (diff.Regions)
	spans   []diff.Span
	regions []diff.Span
	// done: the differences are found
	done bool
	cur  int // the difference gone to last; -1 - none
}

// count is how many differences there are
func (cmp *Compare) count() int {
	if cmp.shifts {
		return len(cmp.diffs)
	}
	return len(cmp.regions)
}

// names are the names of the documents compared after the first one
func (cmp *Compare) names() string {
	var ns []string
	for _, d := range cmp.docs[1:] {
		ns = append(ns, docName(d))
	}
	return strings.Join(ns, ", ")
}

// close cancels the comparison and closes the documents of the views but
// the first one, which is the document of the tab
func (cmp *Compare) close() {
	if cmp.end != nil {
		cmp.end()
		cmp.end = nil
	}
	for _, d := range cmp.docs[1:] {
		d.Close()
	}
}

func docName(d *doc.Document) string {
	if n := d.Name(); n != "" {
		return n
	}
	return T().Untitled
}

// compareTarget is what a document is compared with: an open tab or a file
type compareTarget struct {
	tab  *Tab
	path string
}

// compareDialog asks for the files to compare the current document with:
// several byte by byte, one with shifts
func (c *MainForm) compareDialog(shifts bool) func() {
	return func() {
		t := c.tab()
		if t == nil {
			return
		}
		title := T().MenuCompare
		if shifts {
			title = T().MenuCompareShifts
		}
		c.Form().ShowOpenFileDialog(ui.OpenFileDialogOptions{Title: title, AllowMultiple: !shifts},
			func(paths []string, err error) {
				if err != nil || len(paths) == 0 {
					return
				}
				var targets []compareTarget
				for _, p := range paths {
					targets = append(targets, compareTarget{path: p})
				}
				c.compare(t, targets, shifts)
			})
	}
}

// compareOpenDialog asks which of the open documents (and files) to compare
// the current document with
func (c *MainForm) compareOpenDialog() {
	t := c.tab()
	if t == nil {
		return
	}
	var tabs []*Tab
	var names []string
	for _, x := range c.tabs {
		if x != t {
			tabs = append(tabs, x)
			names = append(names, x.name())
		}
	}
	c.ShowDialog(NewCompareDialog(t.name(), names, func(chosen []int, paths []string, shifts bool) {
		var targets []compareTarget
		for _, i := range chosen {
			if c.indexOf(tabs[i]) >= 0 {
				targets = append(targets, compareTarget{tab: tabs[i]})
			}
		}
		for _, p := range paths {
			targets = append(targets, compareTarget{path: p})
		}
		if len(targets) > 0 && c.indexOf(t) >= 0 {
			c.compare(t, targets, shifts)
		}
	}))
}

// snapshotSource is a snapshot of a document as a source: a document of
// its own over it shows the document as it was then
type snapshotSource struct{ *doc.Snapshot }

func (snapshotSource) Close() error { return nil }

// snapshotOf makes a read-only document of the tab's document as it is now
func snapshotOf(t *Tab) (*doc.Document, error) {
	return doc.OpenSource(snapshotSource{t.doc.Snapshot()}, t.name())
}

// compare opens a tab with the document and the others side by side and
// finds their differences in the background
func (c *MainForm) compare(t *Tab, targets []compareTarget, shifts bool) {
	if len(targets) == 0 {
		return
	}
	if shifts && len(targets) != 1 {
		ui.ShowMessageBox(c, T().Error, T().CompareShiftsOne)
		return
	}
	if len(targets)+1 > diff.MaxInputs {
		ui.ShowMessageBox(c, T().Error, T().CompareTooMany(diff.MaxInputs))
		return
	}
	cmp := &Compare{cur: -1, shifts: shifts}
	fail := func(err error) {
		for _, d := range cmp.docs {
			d.Close()
		}
		ui.ShowMessageBox(c, T().Error, err.Error())
	}
	base, err := snapshotOf(t)
	if err != nil {
		fail(err)
		return
	}
	cmp.docs, cmp.from = append(cmp.docs, base), append(cmp.from, t.doc)
	for _, tg := range targets {
		// A file open in a tab is compared as it is there, with its edits
		if tg.tab == nil {
			if abs, err := filepath.Abs(tg.path); err == nil {
				tg.path = abs
			}
			for _, x := range c.tabs {
				if x.doc.Path() == tg.path {
					tg.tab = x
				}
			}
		}
		if tg.tab != nil {
			d, err := snapshotOf(tg.tab)
			if err != nil {
				fail(err)
				return
			}
			cmp.docs, cmp.from = append(cmp.docs, d), append(cmp.from, tg.tab.doc)
			continue
		}
		d, err := doc.Open(tg.path, doc.Options{ReadOnly: true})
		if err != nil {
			fail(errors.New(T().OpenFailed(tg.path, err.Error())))
			return
		}
		cmp.docs, cmp.from = append(cmp.docs, d), append(cmp.from, nil)
	}

	ct := newTab(base, c.viewOptions(), c.onView)
	ct.cmp = cmp
	for _, d := range cmp.docs[1:] {
		v := hexview.NewView(d, c.viewOptions())
		c.onView(ct, v)
		ct.views = append(ct.views, v)
	}
	for i, v := range ct.views {
		v.OnFocused = func() { ct.cur = i }
		if shifts {
			v.Highlights = ct.compareMarks(i == 1)
			// Scrolled together, the offsets matched through the differences
			other := ct.views[1-i]
			v.OnScrolled = func(int64) { ct.syncCompare(v, other, i == 1) }
			continue
		}
		v.Highlights = ct.spanMarks(i)
		// The same offsets in all
		v.OnScrolled = func(int64) {
			if ct.syncing {
				return
			}
			ct.syncing = true
			for _, w := range ct.views {
				if w != v {
					w.ScrollToOffset(v.TopOffset())
				}
			}
			ct.syncing = false
		}
	}
	ct.box.RemoveAllWidgets()
	ct.box.AddWidget(0, 0, comparePanes(ct))
	c.tabs = append(c.tabs, ct)
	c.selectTab(len(c.tabs) - 1)
	c.runCompare(ct)
}

// comparePanes lays out the views of a compare tab, each under the name of
// its document: two side by side, more one above another
func comparePanes(ct *Tab) ui.Widgeter {
	panes := make([]ui.Widgeter, len(ct.views))
	for i, v := range ct.views {
		p := ui.NewPanel()
		p.SetPanelPadding(0)
		p.SetXExpandable(true)
		p.SetYExpandable(true)
		p.AddWidget(0, 0, ui.NewLabel(docName(ct.cmp.docs[i])))
		p.AddWidget(1, 0, v)
		panes[i] = p
	}
	if len(panes) == 2 {
		ct.split = ui.NewHSplitter()
		ct.split.SetWidgets(panes[0], panes[1])
		return ct.split
	}
	stack := ui.NewPanel()
	stack.SetPanelPadding(0)
	stack.SetXExpandable(true)
	stack.SetYExpandable(true)
	for i, p := range panes {
		stack.AddWidget(i, 0, p)
	}
	return stack
}

// runCompare finds the differences of a compare tab, with the progress in
// the status bar and the Cancel link
func (c *MainForm) runCompare(ct *Tab) {
	cmp := ct.cmp
	snaps := make([]io.ReaderAt, len(cmp.docs))
	sizes := make([]int64, len(cmp.docs))
	total := int64(0)
	for i, d := range cmp.docs {
		s := d.Snapshot()
		snaps[i], sizes[i] = s, s.Size()
		total = max(total, s.Size())
	}
	if cmp.shifts {
		total = sizes[0]
	}
	start := time.Now()
	var ranges []diff.Range
	var spans []diff.Span
	work := func(ctx context.Context, progress func(int64)) error {
		var err error
		if cmp.shifts {
			ranges, err = diff.Compare(ctx, snaps[0], sizes[0], snaps[1], sizes[1], diff.Options{Shifts: true}, progress)
		} else {
			spans, err = diff.CompareN(ctx, snaps, sizes, diff.Options{MaxRanges: compareMaxSpans}, progress)
		}
		if errors.Is(err, diff.ErrMaxRanges) {
			err = nil // the differences found so far are shown
		}
		return err
	}
	cmp.end = c.background(T().Comparing, total, work, func(err error) {
		cmp.end = nil
		if c.indexOf(ct) < 0 {
			return
		}
		if errors.Is(err, context.Canceled) {
			ui.ShowToast(c, T().Canceled, ui.ToastInfo)
			c.dropTab(c.indexOf(ct))
			return
		}
		if err != nil {
			ui.ShowMessageBox(c, T().Error, err.Error())
			return
		}
		took := time.Since(start).Seconds()
		cmp.done = true
		if cmp.shifts {
			cmp.ranges = ranges
			for i, r := range ranges {
				if r.Kind != diff.Equal {
					cmp.diffs = append(cmp.diffs, i)
				}
			}
			s := diff.Summarize(ranges)
			ui.ShowToast(c, T().CompareSummary(s.Ranges, s.Changed, s.Inserted, s.Deleted, took), ui.ToastInfo)
		} else {
			cmp.spans, cmp.regions = spans, diff.Regions(spans)
			n := int64(0)
			for _, r := range cmp.regions {
				n += r.Len
			}
			ui.ShowToast(c, T().CompareSummaryN(len(cmp.regions), n, took), ui.ToastInfo)
		}
		c.updateStatus()
		c.update()
	})
}

// spanMarks gives view i of a compare tab byte by byte its differences:
// the first view all of them, the others where they differ from the first
func (t *Tab) spanMarks(i int) func(from, to int64) []hexview.Highlight {
	return func(from, to int64) []hexview.Highlight {
		var hs []hexview.Highlight
		if i == 0 {
			hs = t.highlights(from, to)
		}
		if t.cmp == nil {
			return hs
		}
		sp := t.cmp.spans
		col := colorChanged.get()
		for k := diff.SpansIn(sp, from); k < len(sp) && sp[k].Off < to; k++ {
			if sp[k].Has(i) {
				hs = append(hs, hexview.Highlight{Off: sp[k].Off, Len: sp[k].Len, Color: col})
			}
		}
		return hs
	}
}

// compareMarks gives a view of a compare tab with shifts its side of the
// differences
func (t *Tab) compareMarks(sideB bool) func(from, to int64) []hexview.Highlight {
	return func(from, to int64) []hexview.Highlight {
		if t.cmp == nil {
			return nil
		}
		rs := t.cmp.ranges
		// The ranges are in order on both sides
		i := sort.Search(len(rs), func(i int) bool {
			if sideB {
				return rs[i].BEnd() > from
			}
			return rs[i].AEnd() > from
		})
		var hs []hexview.Highlight
		for ; i < len(rs); i++ {
			r := rs[i]
			off, n := r.AOff, r.ALen
			if sideB {
				off, n = r.BOff, r.BLen
			}
			if off >= to {
				break
			}
			if r.Kind == diff.Equal || n == 0 {
				continue
			}
			col := colorChanged.get()
			if r.Kind == diff.Inserted || r.Kind == diff.Deleted {
				col = colorShifted.get()
			}
			hs = append(hs, hexview.Highlight{Off: off, Len: n, Color: col})
		}
		return hs
	}
}

// mapOffset returns the offset on the other side matching off
func (cmp *Compare) mapOffset(off int64, fromB bool) int64 {
	rs := cmp.ranges
	i := sort.Search(len(rs), func(i int) bool {
		if fromB {
			return rs[i].BEnd() > off
		}
		return rs[i].AEnd() > off
	})
	if i >= len(rs) {
		return off
	}
	r := rs[i]
	if fromB {
		return r.AOff + min(off-r.BOff, r.ALen)
	}
	return r.BOff + min(off-r.AOff, r.BLen)
}

// syncCompare scrolls the other view of a compare tab to the matching place
func (t *Tab) syncCompare(from, to *hexview.View, fromB bool) {
	if t.syncing || t.cmp == nil {
		return
	}
	t.syncing = true
	to.ScrollToOffset(t.cmp.mapOffset(from.TopOffset(), fromB))
	t.syncing = false
}

// nextRegion returns the index of the difference after the one at
// [start, caret) (or before it); -1 - none
func nextRegion(rs []diff.Span, start, caret int64, cur int, back bool) int {
	if back {
		for k := len(rs) - 1; k >= 0; k-- {
			if rs[k].Off < start {
				return k
			}
		}
		return -1
	}
	for k, r := range rs {
		if r.Off > caret || (r.Off == caret && k > cur) {
			return k
		}
	}
	return -1
}

// nextDiff goes to the next difference (or the previous one) in all views
func (c *MainForm) nextDiff(back bool) {
	t := c.tab()
	if t == nil || t.cmp == nil || t.cmp.count() == 0 {
		return
	}
	cmp := t.cmp
	if !cmp.shifts {
		v := t.view()
		start, _ := v.Selection()
		idx := nextRegion(cmp.regions, start, v.Caret(), cmp.cur, back)
		if idx < 0 {
			ui.ShowToast(c, T().NoMoreDiffs, ui.ToastInfo)
			return
		}
		cmp.cur = idx
		r := cmp.regions[idx]
		for _, w := range t.views {
			w.SelectFound(r.Off, r.Len)
		}
		c.updateStatus()
		return
	}
	vA, vB := t.views[0], t.views[1]
	caret := vA.Caret()
	start, _ := vA.Selection()
	idx := -1
	if back {
		for k := len(cmp.diffs) - 1; k >= 0; k-- {
			if cmp.ranges[cmp.diffs[k]].AOff < start {
				idx = k
				break
			}
		}
	} else {
		for k, i := range cmp.diffs {
			if cmp.ranges[i].AOff > caret || (cmp.ranges[i].AOff == caret && k > cmp.cur) {
				idx = k
				break
			}
		}
	}
	if idx < 0 {
		ui.ShowToast(c, T().NoMoreDiffs, ui.ToastInfo)
		return
	}
	cmp.cur = idx
	r := cmp.ranges[cmp.diffs[idx]]
	vA.SelectFound(r.AOff, r.ALen)
	vB.SelectFound(r.BOff, r.BLen)
	c.updateStatus()
}

// exportReport saves the report of the comparison (DIFF-3)
func (c *MainForm) exportReport() {
	t := c.tab()
	if t == nil || t.cmp == nil || !t.cmp.done {
		return
	}
	cmp := t.cmp
	c.Form().ShowSaveFileDialog(ui.SaveFileDialogOptions{Title: T().MenuCompareReport, DefaultFileName: "compare.txt"},
		func(path string, err error) {
			if err != nil || path == "" {
				return
			}
			f, err := os.Create(path)
			if err == nil {
				w := bufio.NewWriter(f)
				err = writeCompareReport(w, cmp)
				if err == nil {
					err = w.Flush()
				}
				if closeErr := f.Close(); err == nil {
					err = closeErr
				}
			}
			if err != nil {
				ui.ShowMessageBox(c, T().Error, err.Error())
			}
		})
}

func writeCompareReport(w io.Writer, cmp *Compare) error {
	if cmp.shifts {
		return diff.WriteReport(w, cmp.ranges, cmp.docs[0].Snapshot(), cmp.docs[1].Snapshot(), 64)
	}
	names := make([]string, len(cmp.docs))
	snaps := make([]io.ReaderAt, len(cmp.docs))
	sizes := make([]int64, len(cmp.docs))
	for i, d := range cmp.docs {
		s := d.Snapshot()
		names[i], snaps[i], sizes[i] = docName(d), s, s.Size()
	}
	return diff.WriteReportN(w, names, cmp.spans, snaps, sizes, 64)
}

// dropComparesOf closes the compare tabs that compare the document of the
// tab, which is being closed: they read it
func (c *MainForm) dropComparesOf(t *Tab) {
	for i := 0; i < len(c.tabs); i++ {
		x := c.tabs[i]
		if x.cmp == nil || x == t {
			continue
		}
		for _, d := range x.cmp.from {
			if d == t.doc {
				c.tabs = append(c.tabs[:i], c.tabs[i+1:]...)
				x.close()
				i--
				break
			}
		}
	}
}

// ---- The dialog of the documents to compare ----

// CompareDialog asks which open documents (and files) to compare one with
type CompareDialog struct {
	ui.DialogContent

	list      *ui.Panel
	checks    []*ui.Checkbox
	paths     []string // the files added, after the open documents
	open      int      // how many of checks are open documents
	chkShifts *ui.Checkbox
	lblError  *ui.Label
	btnOK     *ui.Button
	btnCancel *ui.Button
	onAccept  func(chosen []int, paths []string, shifts bool)
}

// NewCompareDialog offers the names of the open documents; onAccept gets
// the indexes of the ones chosen and the files added, on the goroutine of
// the window below
func NewCompareDialog(name string, names []string, onAccept func(chosen []int, paths []string, shifts bool)) *CompareDialog {
	var c CompareDialog
	c.InitWidget()
	c.onAccept = onAccept
	c.AddWidget(0, 0, ui.NewLabel(T().CompareWith(name)))
	c.list = ui.NewPanel()
	c.AddWidget(1, 0, c.list)
	for _, n := range names {
		c.addCheck(n)
	}
	c.open = len(names)
	btnAdd := ui.NewButton(T().CompareAddFiles)
	btnAdd.SetOnClick(c.addFiles)
	c.AddWidget(2, 0, btnAdd)
	c.chkShifts = ui.NewCheckbox(T().CompareShiftsOne)
	c.AddWidget(3, 0, c.chkShifts)
	c.lblError = ui.NewLabel("")
	c.lblError.SetForegroundColor(colorModified.get())
	c.AddWidget(4, 0, c.lblError)
	c.AddWidget(5, 0, ui.NewVSpacer())
	buttons := ui.NewPanel()
	c.AddWidget(6, 0, buttons)
	c.btnOK = ui.NewButton(ui.UIText().OK)
	c.btnOK.SetOnClick(c.accept)
	c.btnCancel = ui.NewButton(ui.UIText().Cancel)
	c.btnCancel.SetOnClick(func() { c.Form().Close() })
	buttons.AddWidget(0, 0, ui.NewHSpacer())
	buttons.AddWidget(0, 1, c.btnOK)
	buttons.AddWidget(0, 2, c.btnCancel)

	c.OnDialogShow = func() {
		c.Form().SetTitle(T().MenuCompareOpen)
		c.Form().SetSize(480, min(240+28*len(names), 640))
		c.Form().MoveToCenterOfParent()
		c.Form().SetAcceptButton(c.btnOK)
		c.Form().SetCancelButton(c.btnCancel)
	}
	return &c
}

func (c *CompareDialog) addCheck(text string) *ui.Checkbox {
	chk := ui.NewCheckbox(text)
	c.list.AddWidget(len(c.checks), 0, chk)
	c.checks = append(c.checks, chk)
	return chk
}

// addFiles adds files to compare with, chosen
func (c *CompareDialog) addFiles() {
	c.Form().ShowOpenFileDialog(ui.OpenFileDialogOptions{Title: T().CompareAddFiles, AllowMultiple: true},
		func(paths []string, err error) {
			if err != nil {
				return
			}
			for _, p := range paths {
				c.addCheck(p).SetChecked(true)
				c.paths = append(c.paths, p)
			}
			c.Form().UpdateLayout()
		})
}

func (c *CompareDialog) accept() {
	var chosen []int
	var paths []string
	for i, chk := range c.checks {
		if !chk.Checked() {
			continue
		}
		if i < c.open {
			chosen = append(chosen, i)
		} else {
			paths = append(paths, c.paths[i-c.open])
		}
	}
	n := len(chosen) + len(paths)
	shifts := c.chkShifts.Checked()
	switch {
	case n == 0:
		c.lblError.SetText(T().CompareChooseOne)
		return
	case shifts && n != 1:
		c.lblError.SetText(T().CompareShiftsOne)
		return
	case n+1 > diff.MaxInputs:
		c.lblError.SetText(T().CompareTooMany(diff.MaxInputs))
		return
	}
	c.RunInParent(func() { c.onAccept(chosen, paths, shifts) })
	c.Form().Close()
}

// ---- Patches (EXT-3) ----

// patchSides returns what a patch turns into what: the file as saved and
// the document now, or the first two documents of a compare tab
func (c *MainForm) patchSides(t *Tab) (src, dst *doc.Snapshot, closeSrc func(), err error) {
	if t.cmp != nil {
		return t.doc.Snapshot(), t.cmp.docs[1].Snapshot(), func() {}, nil
	}
	path := t.doc.Path()
	if path == "" {
		return nil, nil, nil, errors.New(T().PatchNoFile)
	}
	orig, err := doc.Open(path, doc.Options{ReadOnly: true})
	if err != nil {
		return nil, nil, nil, err
	}
	return orig.Snapshot(), t.doc.Snapshot(), func() { orig.Close() }, nil
}

// makePatch saves the changes of the current document as a patch
func (c *MainForm) makePatch(f patch.Format) func() {
	return func() {
		t := c.tab()
		if t == nil {
			return
		}
		src, dst, closeSrc, err := c.patchSides(t)
		if err != nil {
			ui.ShowMessageBox(c, T().Error, err.Error())
			return
		}
		name := t.name() + f.Ext()
		c.Form().ShowSaveFileDialog(ui.SaveFileDialogOptions{Title: T().MenuMakePatch, DefaultFileName: name},
			func(path string, err error) {
				if err != nil || path == "" {
					closeSrc()
					return
				}
				c.background(T().Working, dst.Size(), func(ctx context.Context, progress func(int64)) error {
					return writePatch(ctx, f, path, src, dst, progress)
				}, func(err error) {
					closeSrc()
					switch {
					case errors.Is(err, context.Canceled):
						ui.ShowToast(c, T().Canceled, ui.ToastInfo)
					case err != nil:
						ui.ShowMessageBox(c, T().Error, err.Error())
					default:
						ui.ShowToast(c, T().PatchSaved, ui.ToastSuccess)
					}
				})
			})
	}
}

func writePatch(ctx context.Context, f patch.Format, path string, src, dst *doc.Snapshot, progress func(int64)) error {
	out, err := os.Create(path)
	if err != nil {
		return err
	}
	w := bufio.NewWriterSize(out, 1<<20)
	err = patch.Make(ctx, f, w, src, src.Size(), dst, dst.Size(), progress)
	if err == nil {
		err = w.Flush()
	}
	if closeErr := out.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		os.Remove(path)
	}
	return err
}

// applyPatch applies a patch to the file of the current document and opens
// the result, saved under a name asked for
func (c *MainForm) applyPatch() {
	t := c.tab()
	if t == nil {
		return
	}
	src := t.doc.Snapshot()
	c.Form().ShowOpenFileDialog(ui.OpenFileDialogOptions{Title: T().MenuApplyPatch},
		func(paths []string, err error) {
			if err != nil || len(paths) == 0 {
				return
			}
			patchPath := paths[0]
			pf, err := os.Open(patchPath)
			if err != nil {
				ui.ShowMessageBox(c, T().Error, err.Error())
				return
			}
			head := make([]byte, 64)
			n, _ := io.ReadFull(pf, head)
			pf.Close()
			format, ok := patch.Detect(head[:n])
			if !ok {
				ui.ShowMessageBox(c, T().Error, T().PatchUnknown)
				return
			}
			dir := filepath.Dir(t.doc.Path())
			name := t.name() + ".patched"
			c.Form().ShowSaveFileDialog(ui.SaveFileDialogOptions{Title: T().MenuApplyPatch, DefaultDirectory: dir, DefaultFileName: name},
				func(outPath string, err error) {
					if err != nil || outPath == "" {
						return
					}
					c.background(T().Working, 0, func(ctx context.Context, _ func(int64)) error {
						return applyPatchFile(ctx, format, patchPath, src, outPath)
					}, func(err error) {
						switch {
						case errors.Is(err, context.Canceled):
							ui.ShowToast(c, T().Canceled, ui.ToastInfo)
						case err != nil:
							ui.ShowMessageBox(c, T().Error, err.Error())
						default:
							c.OpenFile(outPath)
						}
					})
				})
		})
}

func applyPatchFile(ctx context.Context, f patch.Format, patchPath string, src *doc.Snapshot, outPath string) error {
	pf, err := os.Open(patchPath)
	if err != nil {
		return err
	}
	defer pf.Close()
	out, err := os.Create(outPath)
	if err != nil {
		return err
	}
	// BPS reads back what it wrote: the file is given as it is
	err = patch.Apply(ctx, f, bufio.NewReader(pf), out, src, src.Size())
	if closeErr := out.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		os.Remove(outPath)
	}
	return err
}

func (c *MainForm) registerCompareCommands() {
	t := T
	c.cmd("compare.bytes", func() string { return t().MenuCompare }, "", c.compareDialog(false))
	c.cmd("compare.open", func() string { return t().MenuCompareOpen }, "", c.compareOpenDialog)
	c.cmd("compare.shifts", func() string { return t().MenuCompareShifts }, "", c.compareDialog(true))
	c.cmd("compare.next", func() string { return t().MenuNextDiff }, "F8", func() { c.nextDiff(false) })
	c.cmd("compare.prev", func() string { return t().MenuPrevDiff }, "Shift+F8", func() { c.nextDiff(true) })
	c.cmd("compare.report", func() string { return t().MenuCompareReport }, "", c.exportReport)
	for _, f := range patch.Formats() {
		c.cmd("patch.make."+f.String(), fixed(f.String()+" ("+f.Ext()+")..."), "", c.makePatch(f))
	}
	c.cmd("patch.apply", func() string { return t().MenuApplyPatch }, "", c.applyPatch)
}

func (c *MainForm) buildCompareMenu(menu *ui.ContextMenu) {
	t := T
	c.addItems(menu, "compare.bytes", "compare.open", "compare.shifts", "-", "compare.next", "compare.prev", "compare.report", "-")
	var makes []string
	for _, f := range patch.Formats() {
		makes = append(makes, "patch.make."+f.String())
	}
	c.subMenu(menu, func() string { return t().MenuMakePatch }, makes...)
	c.addItems(menu, "patch.apply")
}
