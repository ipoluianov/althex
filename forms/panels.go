package forms

import (
	"github.com/ipoluianov/althex/config"
	"github.com/ipoluianov/althex/core/doc"
	"github.com/ipoluianov/althex/core/interp"
	"github.com/ipoluianov/nui/ui"
)

// page is a page of the side panel
type page interface {
	ui.Widgeter
	// refresh shows the state of the current document; called when it
	// changes while the page is shown
	refresh()
}

// SidePanel shows one page at a time (the interpreter, the search, the
// hashes...), chosen in the list on top. It is hidden until asked for, so
// the window starts with the bytes alone.
type SidePanel struct {
	ui.Widget

	cmbPage *ui.ComboBox
	content *ui.Panel
	pages   []page
	titles  []func() string
	cur     int
	filling bool

	// OnClose hides the panel
	OnClose func()
}

func NewSidePanel() *SidePanel {
	var c SidePanel
	c.InitWidget()
	c.SetPanelPadding(0)
	header := ui.NewPanel()
	header.SetPanelPadding(2)
	c.cmbPage = ui.NewComboBox()
	c.cmbPage.SetXExpandable(true)
	c.cmbPage.SetOnSelectedIndexChanged(func() {
		if !c.filling && c.cmbPage.SelectedIndex() >= 0 {
			c.show(c.cmbPage.SelectedIndex())
		}
	})
	header.AddWidget(0, 0, c.cmbPage)
	closeBtn := ui.NewButton("×")
	closeBtn.SetOnClick(func() {
		if c.OnClose != nil {
			c.OnClose()
		}
	})
	header.AddWidget(0, 1, closeBtn)
	c.AddWidget(0, 0, header)
	c.content = ui.NewPanel()
	c.content.SetPanelPadding(4)
	c.content.SetXExpandable(true)
	c.content.SetYExpandable(true)
	c.AddWidget(1, 0, c.content)
	c.cur = -1
	return &c
}

// add adds a page; returns its index
func (c *SidePanel) add(title func() string, p page) int {
	i := len(c.pages)
	c.pages = append(c.pages, p)
	c.titles = append(c.titles, title)
	c.cmbPage.AddItem(title(), i)
	return i
}

// show makes the i-th page the one shown
func (c *SidePanel) show(i int) {
	if i == c.cur {
		c.pages[i].refresh()
		return
	}
	c.content.RemoveAllWidgets()
	c.content.AddWidget(0, 0, c.pages[i])
	c.cur = i
	c.filling = true
	c.cmbPage.SetSelectedIndex(i)
	c.filling = false
	c.pages[i].refresh()
	if f := c.Form(); f != nil {
		f.UpdateLayout()
	}
}

// refresh refreshes the page shown, if the panel is
func (c *SidePanel) refresh() {
	if c.IsVisible() && c.cur >= 0 {
		c.pages[c.cur].refresh()
	}
}

// applyLanguage renames the pages in the list
func (c *SidePanel) applyLanguage() {
	for i, t := range c.titles {
		c.cmbPage.SetItemText(i, t())
	}
}

// panelWidth is the width of the side panel when it is first shown
const panelWidth = 420

// togglePage shows the page in the side panel, or hides the panel when it
// shows that page already
func (c *MainForm) togglePage(i int) {
	if c.side.IsVisible() && c.side.cur == i {
		c.side.SetVisible(false)
	} else {
		c.side.SetVisible(true)
		c.side.show(i)
	}
	if f := c.Form(); f != nil {
		f.UpdateLayout()
	}
}

// pageShown tells whether the page is on the screen
func (c *MainForm) pageShown(i int) bool {
	return c.side.IsVisible() && c.side.cur == i
}

// ---- The data interpreter (ANL-1) ----

// InspectorPage shows the bytes at the caret in every type; a value can be
// edited in place, which writes its bytes as an edit
type InspectorPage struct {
	ui.Widget

	c        *MainForm
	chkBE    *ui.Checkbox
	table    *ui.Table
	values   []interp.Value
	caret    int64
	filling  bool
	editable bool
}

func newInspectorPage(c *MainForm) *InspectorPage {
	var p InspectorPage
	p.InitWidget()
	p.SetPanelPadding(0)
	p.c = c
	p.chkBE = ui.NewCheckbox("")
	p.chkBE.SetTextFunc(func() string { return T().BigEndian })
	p.chkBE.SetChecked(config.GetSettings().InspectorBigEndian)
	p.chkBE.SetOnStateChanged(func() {
		s := config.GetSettings()
		s.InspectorBigEndian = p.chkBE.Checked()
		config.SetSettings(s)
		p.refresh()
	})
	p.AddWidget(0, 0, p.chkBE)
	p.table = ui.NewTable()
	p.table.SetColumnCount(2)
	p.table.SetColumnName(0, T().ColType)
	p.table.SetColumnName(1, T().ColValue)
	p.table.SetColumnWidth(0, 150)
	p.table.SetStretchLastColumn(true)
	p.table.SetXExpandable(true)
	p.table.SetYExpandable(true)
	p.table.SetEditTriggerDoubleClick(true)
	p.table.SetEditTriggerEnter(true)
	p.table.SetEditTriggerF2(true)
	p.table.SetOnCellChanged(p.cellChanged)
	p.AddWidget(1, 0, p.table)
	return &p
}

func (p *InspectorPage) refresh() {
	v := p.c.view()
	p.filling = true
	defer func() { p.filling = false }()
	if v == nil {
		p.table.SetRowCount(0)
		return
	}
	p.caret = v.Caret()
	buf := make([]byte, interp.MaxBytes)
	n, _ := v.Document().ReadAt(buf, p.caret)
	p.values = interp.Interpret(buf[:n], p.chkBE.Checked())
	p.editable = !v.Document().ReadOnly()
	p.table.SetRowCount(len(p.values))
	for i, val := range p.values {
		p.table.SetCellText2(i, 0, val.Type.Name)
		p.table.SetCellText2(i, 1, val.Text)
		p.table.SetCellEditTriggerDoubleClick(i, 0, false)
		p.table.SetCellEditTriggerEnter(i, 0, false)
		p.table.SetCellEditTriggerF2(i, 0, false)
		p.table.SetCellEditTriggerDoubleClick(i, 1, p.editable)
		p.table.SetCellEditTriggerEnter(i, 1, p.editable)
		p.table.SetCellEditTriggerF2(i, 1, p.editable)
	}
}

// cellChanged writes an edited value at the caret
func (p *InspectorPage) cellChanged(row, col int, text string, data interface{}) bool {
	if p.filling || col != 1 || row >= len(p.values) {
		return true
	}
	v := p.c.view()
	if v == nil {
		return false
	}
	bs, err := interp.Encode(p.values[row].Type.ID, text, p.chkBE.Checked())
	if err != nil {
		ui.ShowToast(p.c, err.Error(), ui.ToastError)
		return false
	}
	if err := v.Document().Overwrite(p.caret, bs, doc.EditOptions{Kind: "value"}); err != nil {
		ui.ShowToast(p.c, err.Error(), ui.ToastError)
		return false
	}
	p.refresh()
	return true
}
