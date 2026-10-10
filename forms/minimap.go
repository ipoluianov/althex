package forms

import (
	"fmt"
	"image/color"
	"time"

	"github.com/ipoluianov/althex/config"
	"github.com/ipoluianov/althex/core/doc"
	"github.com/ipoluianov/althex/core/part"
	"github.com/ipoluianov/althex/core/source"
	"github.com/ipoluianov/althex/forms/hexview"
	"github.com/ipoluianov/nui/ui"
)

// ---- The minimap of a tab (BOOT-2) ----

// tabMap is the minimap of a tab with the partitions it shows
type tabMap struct {
	w *hexview.Minimap
	// parts are the partitions of the document as of partsSnap
	parts     []part.Partition
	partsSnap *doc.Snapshot
	partsBusy bool
	partsAt   time.Time
}

// partColors are the colors of the partitions in turn
var partColors = []color.RGBA{
	{0x3d, 0x7f, 0xd1, 150}, {0x3f, 0x9e, 0x5a, 150}, {0xc9, 0xa2, 0x27, 150},
	{0xb3, 0x54, 0x9b, 150}, {0xd0, 0x70, 0x3a, 150}, {0x2a, 0xa1, 0xa1, 150},
}

// widget is what the tab shows: its views (the box) with the minimap next
// to them, made when the tab is shown first
func (t *Tab) widget(c *MainForm) ui.Widgeter {
	if t.frame == nil {
		t.frame = ui.NewPanel()
		t.frame.SetPanelPadding(0)
		t.frame.SetCellPadding(0)
		t.frame.SetXExpandable(true)
		t.frame.SetYExpandable(true)
		t.frame.AddWidget(0, 0, t.box)
		t.initMinimap(c)
	}
	return t.frame
}

// initMinimap puts the minimap next to the views of the tab
func (t *Tab) initMinimap(c *MainForm) {
	m := hexview.NewMinimap()
	t.mm = &tabMap{w: m}
	m.View = t.view
	m.Data = t.mapData
	m.OnIdle = func() { t.minimapIdle(c) }
	m.OnActivate = func(r hexview.MapRange) { t.goToRange(r) }
	m.OnContextMenu = func(r hexview.MapRange, x, y int) {
		menu := ui.NewContextMenu(c)
		menu.AddItem(T().GoToBytes, func() { t.goToRange(r) })
		if pt := t.partitionAt(r.Off); pt != nil {
			p := *pt
			menu.AddItem(T().OpenPartition, func() { c.openPartitionOf(t, p) })
		}
		menu.ShowMenu(x, y)
	}
	m.SetVisible(false)
	t.frame.AddWidget(0, 1, m)
}

// goToRange goes to a range of the minimap and selects it
func (t *Tab) goToRange(r hexview.MapRange) {
	t.jump(r.Off)
	t.view().Select(r.Off, min(r.Len, t.doc.Size()-r.Off))
}

func (t *Tab) partitionAt(off int64) *part.Partition {
	var best *part.Partition
	for i := range t.mm.parts {
		p := &t.mm.parts[i]
		if off >= p.Start && off < p.Start+p.Size && (best == nil || p.Size < best.Size) {
			best = p
		}
	}
	return best
}

// openPartitionOf opens a partition of the tab as a document of its own
func (c *MainForm) openPartitionOf(t *Tab, pt part.Partition) {
	snap := t.doc.Snapshot()
	if pt.Start >= snap.Size() {
		return
	}
	size := min(pt.Size, snap.Size()-pt.Start)
	name := fmt.Sprintf("%s [%d: %s]", t.name(), pt.Index, pt.Type)
	c.openNested(source.NewSection(snap, pt.Start, size), name)
}

// minimapWanted tells whether the tab shows its minimap: as set, or for a
// disk and a document with partitions
func (t *Tab) minimapWanted() bool {
	switch config.GetSettings().View.Minimap {
	case config.MinimapOn:
		return true
	case config.MinimapOff:
		return false
	}
	return len(t.mm.parts) > 0 || t.doc.IsDevice()
}

// minimapIdle reads the partition table again when the document changed
// (in the background, at most every second) and shows or hides the minimap
func (t *Tab) minimapIdle(c *MainForm) {
	if want := t.minimapWanted(); want != t.mm.w.IsVisible() {
		t.mm.w.SetVisible(want)
		if f := c.Form(); f != nil {
			f.UpdateLayout()
		}
	}
	snap := t.doc.Snapshot()
	if t.mm.partsBusy || snap == t.mm.partsSnap || time.Since(t.mm.partsAt) < time.Second {
		return
	}
	form := c.Form()
	if form == nil {
		return
	}
	t.mm.partsBusy = true
	go func() {
		var parts []part.Partition
		if tbl, err := part.Read(snap, snap.Size(), 0); err == nil {
			parts = tbl.Partitions
		}
		form.Invoke(func() {
			t.mm.parts, t.mm.partsSnap = parts, snap
			t.mm.partsBusy, t.mm.partsAt = false, time.Now()
			t.mm.w.Refresh()
		})
	}()
}

// mapData are the ranges the minimap of the tab shows
func (t *Tab) mapData() hexview.MapData {
	var d hexview.MapData
	snap := t.doc.Snapshot()
	size := snap.Size()
	for i, p := range t.mm.parts {
		col := partColors[i%len(partColors)]
		if p.Extended {
			col.A = 60
		}
		name := fmt.Sprintf("%d: %s", p.Index, p.Type)
		if p.Name != "" {
			name += " \"" + p.Name + "\""
		}
		name += ", " + formatSize(p.Size)
		d.Areas = append(d.Areas, hexview.MapRange{Off: p.Start, Len: p.Size, Color: col, Name: name})
	}
	for _, h := range t.templateMarks(0, size) {
		d.Areas = append(d.Areas, hexview.MapRange{Off: h.Off, Len: h.Len, Color: h.Color})
	}
	add := func(hs []hexview.Highlight) {
		for _, h := range hs {
			d.Marks = append(d.Marks, hexview.MapRange{Off: h.Off, Len: h.Len, Color: h.Color})
		}
	}
	for _, r := range snap.EditedRanges(4096) {
		d.Marks = append(d.Marks, hexview.MapRange{Off: r.Off, Len: r.Len, Color: colorChanged.get()})
	}
	if t.marks != nil {
		add(t.marks(0, size))
	}
	add(t.externalMarks(0, size))
	add(t.diskIssues)
	for _, b := range t.bookmarks {
		d.Marks = append(d.Marks, hexview.MapRange{Off: b.Off, Len: max(b.Len, 1), Color: ui.ColorFromHex(b.Color)})
	}
	for _, r := range snap.UnreadableRanges() {
		d.Marks = append(d.Marks, hexview.MapRange{Off: r.Off, Len: r.Len, Color: color.RGBA{0xff, 0x20, 0x20, 0xff}})
	}
	return d
}

// toggleMinimap shows or hides the minimaps, remembered in the settings
func (c *MainForm) toggleMinimap() {
	shown := c.tab() != nil && c.tab().mm != nil && c.tab().mm.w.IsVisible()
	c.changeView(func(vs *config.ViewSettings) {
		vs.Minimap = config.MinimapOn
		if shown {
			vs.Minimap = config.MinimapOff
		}
	})
	for _, t := range c.tabs {
		if t.mm != nil {
			t.mm.w.SetVisible(t.minimapWanted())
		}
	}
	if f := c.Form(); f != nil {
		f.UpdateLayout()
	}
}
