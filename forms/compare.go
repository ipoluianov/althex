package forms

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync/atomic"
	"time"

	"github.com/ipoluianov/althex/core/diff"
	"github.com/ipoluianov/althex/core/doc"
	"github.com/ipoluianov/althex/core/patch"
	"github.com/ipoluianov/althex/forms/hexview"
	"github.com/ipoluianov/nui/ui"
)

// Compare is the state of a tab that compares two documents (DIFF-1..3):
// the document of the tab on the left, docB on the right
type Compare struct {
	docB   *doc.Document
	ranges []diff.Range
	diffs  []int // the indexes of the differing ranges
	cur    int   // the difference gone to last; -1 - none
	shifts bool
}

// compareDialog asks for the file to compare the current document with
func (c *MainForm) compareDialog(shifts bool) func() {
	return func() {
		t := c.tab()
		if t == nil {
			return
		}
		c.Form().ShowOpenFileDialog(ui.OpenFileDialogOptions{Title: T().MenuCompare},
			func(paths []string, err error) {
				if err != nil || len(paths) == 0 {
					return
				}
				c.compare(t, paths[0], shifts)
			})
	}
}

// compare opens a tab with the document and the file side by side and
// finds their differences in the background
func (c *MainForm) compare(t *Tab, pathB string, shifts bool) {
	var dA *doc.Document
	var err error
	if p := t.doc.Path(); p != "" && !t.doc.Modified() {
		dA, err = doc.Open(p, doc.Options{ReadOnly: true})
	} else {
		err = errors.New(T().CompareSaveFirst)
	}
	if err != nil {
		ui.ShowMessageBox(c, T().Error, err.Error())
		return
	}
	dB, err := doc.Open(pathB, doc.Options{ReadOnly: true})
	if err != nil {
		dA.Close()
		ui.ShowMessageBox(c, T().Error, T().OpenFailed(pathB, err.Error()))
		return
	}
	ct := newTab(dA, c.viewOptions(), c.onView)
	ct.cmp = &Compare{docB: dB, cur: -1, shifts: shifts}
	vA := ct.views[0]
	vB := hexview.NewView(dB, c.viewOptions())
	c.onView(ct, vB)
	ct.views = append(ct.views, vB)
	vB.OnFocused = func() { ct.cur = 1 }
	vA.OnFocused = func() { ct.cur = 0 }
	vA.Highlights = ct.compareMarks(false)
	vB.Highlights = ct.compareMarks(true)
	// Scrolled together, the offsets matched through the differences
	vA.OnScrolled = func(int64) { ct.syncCompare(vA, vB, false) }
	vB.OnScrolled = func(int64) { ct.syncCompare(vB, vA, true) }
	ct.box.RemoveAllWidgets()
	ct.split = ui.NewHSplitter()
	ct.split.SetWidgets(vA, vB)
	ct.box.AddWidget(0, 0, ct.split)
	c.tabs = append(c.tabs, ct)
	c.selectTab(len(c.tabs) - 1)
	c.runCompare(ct)
}

// runCompare finds the differences of a compare tab
func (c *MainForm) runCompare(ct *Tab) {
	form := c.Form()
	sa, sb := ct.doc.Snapshot(), ct.cmp.docB.Snapshot()
	total := sa.Size()
	var lastShown atomic.Int64
	c.status.SetInfo(T().Comparing(0))
	go func() {
		start := time.Now()
		rs, err := diff.Compare(context.Background(), sa, sa.Size(), sb, sb.Size(),
			diff.Options{Shifts: ct.cmp.shifts}, func(done int64) {
				now := time.Now().UnixMilli()
				if now-lastShown.Load() < 100 || total == 0 {
					return
				}
				lastShown.Store(now)
				pct := int(done * 100 / total)
				form.Invoke(func() { c.status.SetInfo(T().Comparing(pct)) })
			})
		took := time.Since(start)
		form.Invoke(func() {
			if err != nil && !errors.Is(err, diff.ErrMaxRanges) {
				ui.ShowMessageBox(c, T().Error, err.Error())
				return
			}
			ct.cmp.ranges = rs
			ct.cmp.diffs = ct.cmp.diffs[:0]
			for i, r := range rs {
				if r.Kind != diff.Equal {
					ct.cmp.diffs = append(ct.cmp.diffs, i)
				}
			}
			s := diff.Summarize(rs)
			ui.ShowToast(c, T().CompareSummary(s.Ranges, s.Changed, s.Inserted, s.Deleted, took.Seconds()), ui.ToastInfo)
			c.updateStatus()
			c.update()
		})
	}()
}

// compareMarks gives a view of a compare tab its side of the differences
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
		if fromB {
			return off
		}
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

// nextDiff goes to the next difference (or the previous one) in both views
func (c *MainForm) nextDiff(back bool) {
	t := c.tab()
	if t == nil || t.cmp == nil || len(t.cmp.diffs) == 0 {
		return
	}
	cmp := t.cmp
	vA, vB := t.views[0], t.views[1]
	caret := vA.Caret()
	idx := -1
	if back {
		for k := len(cmp.diffs) - 1; k >= 0; k-- {
			if cmp.ranges[cmp.diffs[k]].AOff < caret {
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
	vA.Select(r.AOff, r.ALen)
	vB.Select(r.BOff, r.BLen)
	c.updateStatus()
}

// exportReport saves the report of the comparison (DIFF-3)
func (c *MainForm) exportReport() {
	t := c.tab()
	if t == nil || t.cmp == nil {
		return
	}
	c.Form().ShowSaveFileDialog(ui.SaveFileDialogOptions{Title: T().MenuCompareReport, DefaultFileName: "compare.txt"},
		func(path string, err error) {
			if err != nil || path == "" {
				return
			}
			f, err := os.Create(path)
			if err == nil {
				w := bufio.NewWriter(f)
				err = diff.WriteReport(w, t.cmp.ranges, t.doc.Snapshot(), t.cmp.docB.Snapshot(), 64)
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

// ---- Patches (EXT-3) ----

// patchSides returns what a patch turns into what: the file as saved and
// the document now, or the two documents of a compare tab
func (c *MainForm) patchSides(t *Tab) (src, dst *doc.Snapshot, closeSrc func(), err error) {
	if t.cmp != nil {
		return t.doc.Snapshot(), t.cmp.docB.Snapshot(), func() {}, nil
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
				form := c.Form()
				c.status.SetInfo(T().Working(0))
				go func() {
					err := writePatch(f, path, src, dst)
					closeSrc()
					form.Invoke(func() {
						c.updateStatus()
						if err != nil {
							ui.ShowMessageBox(c, T().Error, err.Error())
							return
						}
						ui.ShowToast(c, T().PatchSaved, ui.ToastSuccess)
					})
				}()
			})
	}
}

func writePatch(f patch.Format, path string, src, dst *doc.Snapshot) error {
	out, err := os.Create(path)
	if err != nil {
		return err
	}
	w := bufio.NewWriterSize(out, 1<<20)
	err = patch.Make(context.Background(), f, w, src, src.Size(), dst, dst.Size(), nil)
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
					form := c.Form()
					go func() {
						err := applyPatchFile(format, patchPath, src, outPath)
						form.Invoke(func() {
							if err != nil {
								ui.ShowMessageBox(c, T().Error, err.Error())
								return
							}
							c.OpenFile(outPath)
						})
					}()
				})
		})
}

func applyPatchFile(f patch.Format, patchPath string, src *doc.Snapshot, outPath string) error {
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
	err = patch.Apply(context.Background(), f, bufio.NewReader(pf), out, src, src.Size())
	if closeErr := out.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		os.Remove(outPath)
	}
	return err
}

func (c *MainForm) registerCompareCommands() {
	t := T
	c.cmd("compare.bytes", func() string { return t().MenuCompare }, "", c.compareDialog(false))
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
	c.addItems(menu, "compare.bytes", "compare.shifts", "-", "compare.next", "compare.prev", "compare.report", "-")
	var makes []string
	for _, f := range patch.Formats() {
		makes = append(makes, "patch.make."+f.String())
	}
	c.subMenu(menu, func() string { return t().MenuMakePatch }, makes...)
	c.addItems(menu, "patch.apply")
}
