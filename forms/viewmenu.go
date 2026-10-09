package forms

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/ipoluianov/althex/config"
	"github.com/ipoluianov/althex/forms/hexview"
	"github.com/ipoluianov/nui/ui"
)

// viewOptions are the options of the views from the settings, with the
// mark of the current tab
func (c *MainForm) viewOptions() hexview.Options {
	vs := config.GetSettings().View
	return hexview.Options{
		BytesPerRow:  vs.BytesPerRow,
		Group:        vs.Group,
		LittleEndian: vs.LittleEndian,
		Mode:         hexview.Mode(vs.Mode),
		OffsetBase:   vs.OffsetBase,
		Encoding:     encodingByID(vs.Encoding),
		ColorRules:   parseColorRules(vs.ColorRules),
	}
}

// parseColorRules reads "00-1F=#ff8800; 7F=#888888": byte values or ranges
// in hex with a color each; the wrong ones are skipped
func parseColorRules(s string) []hexview.ColorRule {
	var rules []hexview.ColorRule
	for _, part := range strings.FieldsFunc(s, func(r rune) bool { return r == ';' || r == ',' || r == '\n' }) {
		rng, col, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			continue
		}
		lo, hi, isRange := strings.Cut(strings.TrimSpace(rng), "-")
		if !isRange {
			hi = lo
		}
		from, err1 := strconv.ParseUint(strings.TrimSpace(lo), 16, 8)
		to, err2 := strconv.ParseUint(strings.TrimSpace(hi), 16, 8)
		col = strings.TrimSpace(col)
		if err1 != nil || err2 != nil || len(col) != 7 || col[0] != '#' {
			continue
		}
		rules = append(rules, hexview.ColorRule{From: byte(from), To: byte(to), Color: ui.ColorFromHex(col)})
	}
	return rules
}

// changeView changes the view settings, saves them and shows every
// document with them; the marks of the tabs stay
func (c *MainForm) changeView(f func(vs *config.ViewSettings)) {
	s := config.GetSettings()
	f(&s.View)
	config.SetSettings(s)
	opts := c.viewOptions()
	for _, t := range c.tabs {
		t.syncScroll = s.View.SyncScroll
		for _, v := range t.views {
			o := opts
			old := v.Options()
			o.Relative, o.RelativeTo = old.Relative, old.RelativeTo
			v.SetOptions(o)
		}
	}
	c.updateStatus()
}

// radio registers a command of a choice: checked when is() holds
func (c *MainForm) radio(id string, text func() string, is func(vs config.ViewSettings) bool, set func(vs *config.ViewSettings)) {
	cmd := c.cmd(id, text, "", func() { c.changeView(set) })
	cmd.Checked = func() bool { return is(config.GetSettings().View) }
}

func fixed(s string) func() string { return func() string { return s } }

func (c *MainForm) registerViewCommands() {
	t := T
	for _, n := range []int{8, 16, 24, 32, 48, 64} {
		c.radio(fmt.Sprintf("view.bpr%d", n), fixed(fmt.Sprint(n)),
			func(vs config.ViewSettings) bool { return vs.BytesPerRow == n },
			func(vs *config.ViewSettings) { vs.BytesPerRow = n })
	}
	c.radio("view.bprFit", func() string { return t().MenuFitWidth },
		func(vs config.ViewSettings) bool { return vs.BytesPerRow == 0 },
		func(vs *config.ViewSettings) { vs.BytesPerRow = 0 })
	for _, g := range []int{1, 2, 4, 8, 16} {
		c.radio(fmt.Sprintf("view.group%d", g), fixed(fmt.Sprint(g)),
			func(vs config.ViewSettings) bool { return vs.Group == g },
			func(vs *config.ViewSettings) { vs.Group = g })
	}
	c.radio("view.bigEndian", fixed("Big-endian (BE)"),
		func(vs config.ViewSettings) bool { return !vs.LittleEndian },
		func(vs *config.ViewSettings) { vs.LittleEndian = false })
	c.radio("view.littleEndian", fixed("Little-endian (LE)"),
		func(vs config.ViewSettings) bool { return vs.LittleEndian },
		func(vs *config.ViewSettings) { vs.LittleEndian = true })
	modes := []struct {
		mode hexview.Mode
		text func() string
	}{
		{hexview.ModeHex, func() string { return t().ModeHex }},
		{hexview.ModeBin, func() string { return t().ModeBin }},
		{hexview.ModeOct, func() string { return t().ModeOct }},
		{hexview.ModeDec, func() string { return t().ModeDec }},
	}
	for _, m := range modes {
		c.radio(fmt.Sprintf("view.mode%d", m.mode), m.text,
			func(vs config.ViewSettings) bool { return hexview.Mode(vs.Mode) == m.mode },
			func(vs *config.ViewSettings) { vs.Mode = int(m.mode) })
	}
	for _, b := range []struct {
		base int
		text func() string
	}{{16, func() string { return t().ModeHex }}, {10, func() string { return t().ModeDec }}, {8, func() string { return t().ModeOct }}} {
		c.radio(fmt.Sprintf("view.offsets%d", b.base), b.text,
			func(vs config.ViewSettings) bool { return vs.OffsetBase == b.base },
			func(vs *config.ViewSettings) { vs.OffsetBase = b.base })
	}
	relative := c.cmd("view.relative", func() string { return t().MenuRelativeOffsets }, "", c.withView(func(v *hexview.View) {
		o := v.Options()
		o.Relative = !o.Relative
		v.SetOptions(o)
	}))
	relative.Checked = func() bool { return c.view() != nil && c.view().Options().Relative }
	c.cmd("view.setMark", func() string { return t().MenuSetMark }, "Ctrl+M", c.withView(func(v *hexview.View) {
		o := v.Options()
		o.Relative, o.RelativeTo = true, v.Caret()
		v.SetOptions(o)
	}))
	split := c.cmd("view.split", func() string { return t().MenuSplit }, "Ctrl+\\", c.withTab(func(tb *Tab) {
		tb.setSplit(len(tb.views) == 1, c.onView)
		if f := c.Form(); f != nil {
			f.UpdateLayout()
		}
	}))
	split.Checked = func() bool { return c.tab() != nil && len(c.tab().views) == 2 }
	sync := c.cmd("view.syncScroll", func() string { return t().MenuSyncScroll }, "", func() {
		c.changeView(func(vs *config.ViewSettings) { vs.SyncScroll = !vs.SyncScroll })
	})
	sync.Checked = func() bool { return config.GetSettings().View.SyncScroll }
	c.cmd("view.zoomIn", func() string { return t().MenuZoomIn }, "Ctrl+=", func() { c.zoom(1) })
	c.cmd("view.zoomOut", func() string { return t().MenuZoomOut }, "Ctrl+-", func() { c.zoom(-1) })
	c.cmd("view.zoomReset", func() string { return t().MenuZoomReset }, "Ctrl+0", func() { c.zoom(0) })
	c.registerEncodingCommands()
}

func (c *MainForm) buildViewMenu(view *ui.ContextMenu) {
	t := T
	c.subMenu(view, func() string { return t().MenuBytesPerRow },
		"view.bpr8", "view.bpr16", "view.bpr24", "view.bpr32", "view.bpr48", "view.bpr64", "-", "view.bprFit")
	c.subMenu(view, func() string { return t().MenuGroup },
		"view.group1", "view.group2", "view.group4", "view.group8", "view.group16", "-", "view.bigEndian", "view.littleEndian")
	c.subMenu(view, func() string { return t().MenuMode },
		fmt.Sprintf("view.mode%d", hexview.ModeHex), fmt.Sprintf("view.mode%d", hexview.ModeBin),
		fmt.Sprintf("view.mode%d", hexview.ModeOct), fmt.Sprintf("view.mode%d", hexview.ModeDec))
	c.subMenu(view, func() string { return t().MenuOffsets },
		"view.offsets16", "view.offsets10", "view.offsets8", "-", "view.relative", "view.setMark")
	c.buildEncodingMenu(view)
	c.addItems(view, "-", "view.split", "view.syncScroll", "-", "view.zoomIn", "view.zoomOut", "view.zoomReset")
}

func (c *MainForm) zoom(step int) {
	v := c.view()
	if v == nil {
		return
	}
	size := v.FontSize() + float64(step)
	if step == 0 {
		size = hexview.DefaultFontSize
	}
	for _, t := range c.tabs {
		for _, w := range t.views {
			w.SetFontSize(size)
		}
	}
	s := config.GetSettings()
	s.FontSize = v.FontSize()
	config.SetSettings(s)
}
