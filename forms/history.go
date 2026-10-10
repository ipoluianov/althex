package forms

import (
	"fmt"
	"strings"

	"github.com/ipoluianov/althex/core/doc"
	"github.com/ipoluianov/nui/ui"
)

// HistoryPage shows the history of the edits of the current document as
// the tree it is: an undo after which something else was done starts a
// branch, and every state stays reachable (EDIT-3). A double click goes to
// a state.
type HistoryPage struct {
	ui.Widget

	c     *MainForm
	table *ui.Table
	rows  []*doc.HistNode
}

// maxHistoryRows keeps the table light: the latest states are shown
const maxHistoryRows = 5000

func newHistoryPage(c *MainForm) *HistoryPage {
	p := &HistoryPage{c: c}
	p.InitWidget()
	p.SetPanelPadding(0)
	p.table = ui.NewTable()
	p.table.SetColumnCount(3)
	for i, name := range []string{T().ColEdit, T().ColTime, T().ColOffset} {
		p.table.SetColumnName(i, name)
	}
	p.table.SetColumnWidth(0, 200)
	p.table.SetColumnWidth(1, 80)
	p.table.SetStretchLastColumn(true)
	p.table.SetXExpandable(true)
	p.table.SetYExpandable(true)
	p.table.SetOnCellMouseDblClick(p.goTo)
	p.AddWidget(0, 0, p.table)

	buttons := ui.NewPanel()
	buttons.SetPanelPadding(0)
	btn := ui.NewButton("")
	btn.SetTextFunc(func() string { return T().HistoryGoTo })
	btn.SetOnClick(p.goTo)
	buttons.AddWidget(0, 0, btn)
	buttons.AddWidget(0, 1, ui.NewHSpacer())
	p.AddWidget(1, 0, buttons)
	return p
}

// historyRow is a state with how deep in the branches it is
type historyRow struct {
	n     *doc.HistNode
	depth int
}

// historyRows lists the states in order: each branch after the states it
// grew from, the branch made last (the one redo follows) first
func historyRows(root *doc.HistNode) []historyRow {
	var rows []historyRow
	type item struct {
		n     *doc.HistNode
		depth int
	}
	stack := []item{{root, 0}}
	for len(stack) > 0 {
		it := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		rows = append(rows, historyRow{it.n, it.depth})
		kids := it.n.Children
		// The main line continues at the same depth, the other branches are indented
		for i := 0; i < len(kids); i++ {
			depth := it.depth
			if i < len(kids)-1 {
				depth++
			}
			stack = append(stack, item{kids[i], depth})
		}
	}
	return rows
}

func (p *HistoryPage) refresh() {
	t := p.c.tab()
	if t == nil {
		p.rows = nil
		p.table.SetRowCount(0)
		return
	}
	root, cur := t.doc.History()
	all := historyRows(root)
	if len(all) > maxHistoryRows {
		all = all[len(all)-maxHistoryRows:]
	}
	p.rows = p.rows[:0]
	p.table.SetRowCount(len(all))
	curRow := -1
	for i, r := range all {
		p.rows = append(p.rows, r.n)
		name := r.n.Kind
		if r.n == root {
			name = T().HistoryOriginal
		}
		mark := "  "
		if r.n == cur {
			mark, curRow = "• ", i
		}
		p.table.SetCellText2(i, 0, mark+strings.Repeat("  ", r.depth)+name)
		when := ""
		if !r.n.Time.IsZero() && r.n != root {
			when = r.n.Time.Format("15:04:05")
		}
		p.table.SetCellText2(i, 1, when)
		where := ""
		if r.n != root {
			where = fmt.Sprintf("%X", r.n.Off)
			if r.n.Len > 0 {
				where += fmt.Sprintf(" (%d)", r.n.Len)
			}
		}
		p.table.SetCellText2(i, 2, where)
		for col := 0; col < 3; col++ {
			p.table.SetCellEditTriggerF2(i, col, false)
			p.table.SetCellEditTriggerEnter(i, col, false)
			p.table.SetCellEditTriggerDoubleClick(i, col, false)
		}
	}
	if curRow >= 0 {
		p.table.SetCurrentCell2(curRow, 0)
		p.table.ScrollToCell2(curRow, 0)
	}
}

// goTo makes the state of the selected row the current one
func (p *HistoryPage) goTo() {
	t := p.c.tab()
	rows := p.table.SelectedRows()
	if t == nil || len(rows) == 0 || rows[0] >= len(p.rows) {
		return
	}
	n := p.rows[rows[0]]
	if err := t.doc.Goto(n.ID); err != nil && err != doc.ErrNothingToDo {
		ui.ShowMessageBox(p.c, T().Error, err.Error())
		return
	}
	if n.Parent != nil {
		t.jump(n.Off)
	}
	p.refresh()
}
