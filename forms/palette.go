package forms

import (
	"sort"
	"strings"

	"github.com/ipoluianov/nui/ui"
)

// PaletteDialog finds a command by words of its name and runs it (EXT-5)
type PaletteDialog struct {
	ui.DialogContent

	txt   *ui.TextBox
	table *ui.Table
	all   []*Command
	shown []*Command
	run   func(cmd *Command)
}

func NewPaletteDialog(commands map[string]*Command, run func(cmd *Command)) *PaletteDialog {
	var c PaletteDialog
	c.InitWidget()
	c.run = run
	for _, cmd := range commands {
		c.all = append(c.all, cmd)
	}
	sort.Slice(c.all, func(i, j int) bool { return c.all[i].Text() < c.all[j].Text() })
	c.txt = ui.NewTextBox()
	c.txt.SetXExpandable(true)
	c.txt.SetOnTextChanged(c.filter)
	c.AddWidget(0, 0, c.txt)
	c.table = ui.NewTable()
	c.table.SetColumnCount(2)
	c.table.SetColumnName(0, T().ColCommand)
	c.table.SetColumnName(1, T().ColShortcut)
	c.table.SetColumnWidth(0, 380)
	c.table.SetStretchLastColumn(true)
	c.table.SetXExpandable(true)
	c.table.SetYExpandable(true)
	c.table.SetOnCellMouseDblClick(c.accept)
	c.AddWidget(1, 0, c.table)

	ok := ui.NewButton(ui.UIText().OK)
	ok.SetOnClick(c.accept)
	cancel := ui.NewButton(ui.UIText().Cancel)
	cancel.SetOnClick(func() { c.Form().Close() })
	c.OnDialogShow = func() {
		c.Form().SetTitle(T().MenuPalette)
		c.Form().SetSize(600, 420)
		c.Form().MoveToCenterOfParent()
		c.Form().SetAcceptButton(ok)
		c.Form().SetCancelButton(cancel)
		c.txt.Focus()
	}
	c.filter()
	return &c
}

// filter shows the commands whose name has all the words typed
func (c *PaletteDialog) filter() {
	words := strings.Fields(strings.ToLower(c.txt.Text()))
	c.shown = c.shown[:0]
	for _, cmd := range c.all {
		text := strings.ToLower(cmd.Text() + " " + cmd.ID)
		match := true
		for _, w := range words {
			if !strings.Contains(text, w) {
				match = false
				break
			}
		}
		if match {
			c.shown = append(c.shown, cmd)
		}
	}
	c.table.SetRowCount(len(c.shown))
	for i, cmd := range c.shown {
		c.table.SetCellText2(i, 0, cmd.Text())
		keys := ""
		if len(cmd.Keys) > 0 {
			keys = cmd.Keys[0].String()
		}
		c.table.SetCellText2(i, 1, keys)
	}
	if len(c.shown) > 0 {
		c.table.SetCurrentCell2(0, 0)
	}
}

// accept runs the command of the current row, or the only one shown
func (c *PaletteDialog) accept() {
	row := -1
	if rows := c.table.SelectedRows(); len(rows) > 0 {
		row = rows[0]
	}
	if row < 0 || row >= len(c.shown) {
		if len(c.shown) == 0 {
			return
		}
		row = 0
	}
	cmd := c.shown[row]
	c.RunInParent(func() { c.run(cmd) })
	c.Form().Close()
}
