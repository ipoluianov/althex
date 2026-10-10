package forms

import (
	"context"
	"errors"
	"fmt"
	"image/color"
	"os"
	"strings"
	"time"

	"github.com/ipoluianov/althex/app"
	"github.com/ipoluianov/althex/config"
	"github.com/ipoluianov/althex/core/doc"
	"github.com/ipoluianov/althex/core/tmpl"
	"github.com/ipoluianov/althex/forms/hexview"
	"github.com/ipoluianov/nui/ui"
)

// TemplateState is the template applied to a document and what it made
type TemplateState struct {
	tmpl    *tmpl.Template
	prog    *tmpl.Program
	result  *tmpl.Result
	cancel  context.CancelFunc
	running bool
}

// templates returns the built-in templates and the user's (ANL-5, BOOT-10)
func templates() []*tmpl.Template {
	all := append([]*tmpl.Template{}, tmpl.Library()...)
	return append(all, tmpl.LoadDir(config.TemplatesDirectory())...)
}

// docWriter makes the writes of a template edits of the document
type docWriter struct{ d *doc.Document }

func (w docWriter) Overwrite(off int64, b []byte) error {
	return w.d.Overwrite(off, b, doc.EditOptions{Kind: "template"})
}
func (w docWriter) Insert(off int64, b []byte) error {
	return w.d.Insert(off, b, doc.EditOptions{Kind: "template"})
}
func (w docWriter) Delete(off, n int64) error {
	return w.d.Delete(off, n, doc.EditOptions{Kind: "template"})
}

// tmplEnv is the environment of a template over the document of the tab
func tmplEnv(t *Tab, writer bool) tmpl.Env {
	v := t.view()
	selStart, selSize := v.Selection()
	// A template that only reads sees the document as it is now, whatever
	// is edited meanwhile; one that writes reads its own writes
	env := tmpl.Env{Data: t.doc.Snapshot(), SelStart: selStart, SelSize: selSize, Cursor: v.Caret()}
	if writer {
		env.Data = t.doc
		env.Writer = docWriter{t.doc}
	}
	return env
}

// detectTemplate applies the template of the format of a newly opened
// document, if one fits (ANL-4)
func (c *MainForm) detectTemplate(t *Tab) {
	if !config.GetSettings().AutoTemplate {
		return
	}
	tm := tmpl.Detect(templates(), t.doc, t.doc.Path())
	if tm == nil {
		// Not a format of a template: maybe a firmware to analyze
		c.firmware.autoAnalyze(t, false)
		return
	}
	c.applyTemplate(t, tm, false, func() {
		if t == c.tab() && !c.side.IsVisible() {
			c.side.SetVisible(true)
			c.side.show(c.pages.structure)
			if f := c.Form(); f != nil {
				f.UpdateLayout()
			}
		}
	})
	// A format of firmware (ELF, UF2, DfuSe, ESP images) is analyzed too,
	// when it is of a microcontroller
	c.firmware.autoAnalyze(t, true)
}

// applyTemplate runs the template over the document of the tab in the
// background; then (nil - nothing) is called when it is done. A template
// that writes is asked about first and its writes are one edit.
func (c *MainForm) applyTemplate(t *Tab, tm *tmpl.Template, write bool, then func()) {
	prog, err := tm.Program()
	if err != nil {
		ui.ShowMessageBox(c, T().Error, T().TemplateError(tm.Name, err.Error()))
		return
	}
	if t.tmpl != nil && t.tmpl.cancel != nil {
		t.tmpl.cancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	st := &TemplateState{tmpl: tm, prog: prog, cancel: cancel, running: true}
	t.tmpl = st
	env := tmplEnv(t, write)
	form := c.Form()
	if write {
		t.doc.BeginGroup("template")
	}
	c.structure.refresh()
	go func() {
		res := prog.Run(ctx, env)
		form.Invoke(func() {
			if write {
				t.doc.EndGroup()
			}
			cancel()
			st.running = false
			st.cancel = nil
			if t.tmpl != st {
				return // another one was started meanwhile
			}
			if !write && res.Err != nil && strings.Contains(res.Err.Error(), "read-only here") {
				ui.ShowQuestionMessageBoxYesNo(c, app.DisplayName, T().TemplateWrites(tm.Name), func() {
					c.applyTemplate(t, tm, true, then)
				}, nil)
				return
			}
			st.result = res
			c.console.show(tm.Name, res.Output, res.Err)
			c.structure.refresh()
			c.update()
			if then != nil {
				then()
			}
		})
	}()
}

// templateMarks gives the views the colors of the fields of the template
func (t *Tab) templateMarks(from, to int64) []hexview.Highlight {
	if t.tmpl == nil || t.tmpl.result == nil {
		return nil
	}
	var hs []hexview.Highlight
	var visit func(n *tmpl.Node, depth int)
	visit = func(n *tmpl.Node, depth int) {
		if depth > 64 || len(hs) > 2000 {
			return
		}
		for _, ch := range n.Children() {
			if ch.Off >= to || ch.Off+ch.Size <= from {
				continue
			}
			if ch.Color != 0 {
				c := ch.Color & 0xffffff
				hs = append(hs, hexview.Highlight{Off: ch.Off, Len: ch.Size,
					Color: rgba(byte(c>>16), byte(c>>8), byte(c), 90)})
			}
			visit(ch, depth+1)
		}
	}
	visit(t.tmpl.result.Root, 0)
	return hs
}

// ---- The structure page (ANL-6) ----

// StructurePage shows the tree of the fields of the template applied to
// the current document, in sync with the hex view
type StructurePage struct {
	ui.Widget

	c         *MainForm
	cmbTmpl   *ui.ComboBox
	tree      *ui.TreeView
	lblInfo   *ui.Label
	funcsRow  *ui.Panel
	cmbFuncs  *ui.ComboBox
	shownFor  *tmpl.Result
	selecting bool // the tree selects a node because the caret moved
	lastNode  *tmpl.Node
}

func newStructurePage(c *MainForm) *StructurePage {
	var p StructurePage
	p.InitWidget()
	p.SetPanelPadding(0)
	p.c = c
	t := T()

	top := ui.NewPanel()
	top.SetPanelPadding(0)
	p.cmbTmpl = ui.NewComboBox()
	p.cmbTmpl.SetXExpandable(true)
	top.AddWidget(0, 0, p.cmbTmpl)
	run := ui.NewButton(t.Apply)
	run.SetOnClick(p.apply)
	top.AddWidget(0, 1, run)
	folder := ui.NewButton("...")
	folder.SetOnClick(p.openFolder)
	top.AddWidget(0, 2, folder)
	p.AddWidget(0, 0, top)

	p.tree = ui.NewTreeView()
	p.tree.SetColumnCount(5)
	for i, name := range []string{t.ColName, t.ColValue, t.ColType, t.ColOffset, t.ColSize} {
		p.tree.SetColumnName(i, name)
	}
	p.tree.SetColumnWidth(0, 170)
	p.tree.SetColumnWidth(1, 150)
	p.tree.SetColumnWidth(2, 90)
	p.tree.SetColumnWidth(3, 80)
	p.tree.SetStretchLastColumn(true)
	p.tree.SetXExpandable(true)
	p.tree.SetYExpandable(true)
	p.tree.SetOnExpand(p.expand)
	p.tree.SetOnSelectionChanged(p.selected)
	// A value is typed in its cell: F2 or a double click
	for _, col := range []int{0, 2, 3, 4} {
		p.tree.SetColumnReadOnly(col, true)
	}
	p.tree.SetEditTriggerF2(true)
	p.tree.SetEditTriggerDoubleClick(true)
	p.tree.SetOnCellChanged(p.edited)
	p.AddWidget(1, 0, p.tree)

	p.lblInfo = ui.NewLabel("")
	p.AddWidget(2, 0, p.lblInfo)

	p.fillTemplates()
	p.fillFunctions(nil)
	return &p
}

// fillTemplates lists the templates: "Auto" first
func (p *StructurePage) fillTemplates() {
	// The list is made once: the user's templates added later are found by "Auto"
	p.cmbTmpl.AddItem(T().AutoTemplate, nil)
	for _, tm := range templates() {
		name := tm.Name
		if !tm.Builtin {
			name += " *"
		}
		p.cmbTmpl.AddItem(name, tm)
	}
	p.cmbTmpl.SetSelectedIndex(0)
}

// apply runs the template chosen (or the one detected) on the current document
func (p *StructurePage) apply() {
	t := p.c.tab()
	if t == nil {
		return
	}
	tm, _ := p.cmbTmpl.SelectedItemData().(*tmpl.Template)
	if tm == nil {
		// Templates added to the folder since are seen too
		tm = tmpl.Detect(templates(), t.doc, t.doc.Path())
		if tm == nil {
			ui.ShowToast(p.c, T().NoTemplate, ui.ToastInfo)
			return
		}
	}
	p.c.applyTemplate(t, tm, false, nil)
}

// openFolder opens the folder of the user's templates, made if missing
func (p *StructurePage) openFolder() {
	dir := config.TemplatesDirectory()
	os.MkdirAll(dir, 0755)
	if err := app.OpenURL(dir); err != nil {
		ui.ShowMessageBox(p.c, T().Error, err.Error())
	}
}

func (p *StructurePage) refresh() {
	t := p.c.tab()
	var st *TemplateState
	if t != nil {
		st = t.tmpl
	}
	switch {
	case st == nil:
		p.lblInfo.SetText(T().NoTemplateApplied)
	case st.running:
		p.lblInfo.SetText(T().TemplateRunning(st.tmpl.Name))
	case st.result != nil:
		res := st.result
		info := T().TemplateDone(st.tmpl.Name, res.Nodes, res.Duration.Seconds())
		if res.Err != nil {
			info += " — " + res.Err.Error()
		}
		p.lblInfo.SetText(info)
	}
	var res *tmpl.Result
	if st != nil {
		res = st.result
	}
	if res != p.shownFor {
		p.shownFor = res
		p.lastNode = nil
		p.tree.Clear()
		if res != nil {
			p.addChildren(nil, res.Root)
		}
		p.fillFunctions(st)
	}
	p.syncToCaret()
}

// fillFunctions lists the transforms and checksums of the template
// (ANL-12); the row is made anew, as a combo box cannot be emptied
func (p *StructurePage) fillFunctions(st *TemplateState) {
	if p.funcsRow != nil {
		p.RemoveWidget(p.funcsRow)
	}
	row := ui.NewPanel()
	row.SetPanelPadding(0)
	p.cmbFuncs = ui.NewComboBox()
	p.cmbFuncs.SetXExpandable(true)
	n := 0
	if st != nil && st.prog != nil {
		for _, f := range st.prog.Functions() {
			label := f.Title
			if f.Kind == "checksum" {
				label = T().ChecksumPrefix + label
			}
			p.cmbFuncs.AddItem(label, f)
			n++
		}
	}
	p.cmbFuncs.SetSelectedIndex(0)
	row.AddWidget(0, 0, p.cmbFuncs)
	btn := ui.NewButton(T().RunFunction)
	btn.SetOnClick(p.runFunction)
	row.AddWidget(0, 1, btn)
	row.SetVisible(n > 0)
	p.funcsRow = row
	p.AddWidget(3, 0, row)
}

// addChildren adds the tree nodes of the children of a field; their own
// children are added when they are expanded
func (p *StructurePage) addChildren(parent *ui.TreeNode, n *tmpl.Node) {
	const shownMax = 2000 // elements of an array shown at once
	count := n.ChildCount()
	for i := int64(0); i < count && i < shownMax; i++ {
		ch := n.Child(i)
		if ch == nil {
			continue
		}
		p.addNode(parent, ch)
	}
	if count > shownMax {
		more := p.addRow(parent, T().MoreElements(count-shownMax))
		more.SetData(nil)
	}
}

func (p *StructurePage) addRow(parent *ui.TreeNode, text string) *ui.TreeNode {
	if parent == nil {
		return p.tree.AddNode(nil, text)
	}
	return parent.AddChild(text)
}

func (p *StructurePage) addNode(parent *ui.TreeNode, n *tmpl.Node) *ui.TreeNode {
	tn := p.addRow(parent, n.Name)
	tn.SetText(1, n.Value())
	tn.SetText(2, n.TypeName)
	tn.SetText(3, fmt.Sprintf("%X", n.Off))
	tn.SetText(4, fmt.Sprint(n.Size))
	tn.SetData(n)
	tn.SetReadOnly(1, !n.Editable())
	if n.Comment != "" {
		tn.SetText(1, n.Value()+"  // "+n.Comment)
	}
	if n.ChildCount() > 0 {
		tn.SetHasChildren(true)
		if n.Open {
			tn.Expand()
		}
	}
	return tn
}

func (p *StructurePage) expand(tn *ui.TreeNode) {
	n, _ := tn.Data().(*tmpl.Node)
	if n == nil || tn.ChildCount() > 0 {
		return
	}
	p.addChildren(tn, n)
}

// selected selects the bytes of the field chosen in the tree
func (p *StructurePage) selected(tn *ui.TreeNode) {
	if p.selecting || tn == nil {
		return
	}
	n, _ := tn.Data().(*tmpl.Node)
	t := p.c.tab()
	if n == nil || t == nil {
		return
	}
	p.lastNode = n
	t.view().Select(n.Off, n.Size)
}

// edited writes the value typed in the tree into the document as one edit,
// then runs the template again to show it
func (p *StructurePage) edited(tn *ui.TreeNode, col int, text string) bool {
	n, _ := tn.Data().(*tmpl.Node)
	t := p.c.tab()
	if n == nil || t == nil || col != 1 {
		return false
	}
	cur := make([]byte, n.Size)
	if _, err := t.doc.ReadAt(cur, n.Off); err != nil {
		ui.ShowToast(p.c, err.Error(), ui.ToastError)
		return false
	}
	b, err := n.Encode(text, cur)
	if err == nil {
		err = t.doc.Overwrite(n.Off, b, doc.EditOptions{Kind: "value"})
	}
	if err != nil {
		ui.ShowToast(p.c, err.Error(), ui.ToastError)
		return false
	}
	t.view().Select(n.Off, n.Size)
	if st := t.tmpl; st != nil {
		p.c.applyTemplate(t, st.tmpl, false, nil)
	}
	return true
}

// syncOffset is the byte a panel following the hex view shows: the start
// of the selection when there is one (the caret is past its end, in the
// next field or instruction), else the caret
func syncOffset(caret, selStart, selSize int64) int64 {
	if selSize > 0 {
		return selStart
	}
	return caret
}

// syncToCaret shows the field under the caret in the tree
func (p *StructurePage) syncToCaret() {
	t := p.c.tab()
	if t == nil || p.shownFor == nil || !p.IsVisible() {
		return
	}
	// The bytes of the field chosen in the tree are selected: it stays
	// chosen (inside a struct the deepest field at its start would win)
	v := t.view()
	selStart, selSize := v.Selection()
	if p.lastNode != nil && selSize > 0 && selStart == p.lastNode.Off && selSize == p.lastNode.Size {
		return
	}
	n := p.shownFor.Root.NodeAt(syncOffset(v.Caret(), selStart, selSize))
	// An element of an array of a simple type: the array, not opened for one byte
	if n != nil && n.Parent != nil && n.Parent.Kind == tmpl.NodeArray {
		n = n.Parent
	}
	if n == nil || p.lastNode != nil && n.Off == p.lastNode.Off && n.Name == p.lastNode.Name {
		return
	}
	p.lastNode = n
	// The path from the top, expanded on the way
	var path []*tmpl.Node
	for x := n; x != nil && x.Kind != tmpl.NodeRoot; x = x.Parent {
		path = append([]*tmpl.Node{x}, path...)
	}
	var cur *ui.TreeNode
	level := p.tree.Nodes()
	for _, x := range path {
		var found *ui.TreeNode
		for _, tn := range level {
			if d, _ := tn.Data().(*tmpl.Node); d != nil && d.Off == x.Off && d.Name == x.Name {
				found = tn
				break
			}
		}
		if found == nil {
			break
		}
		cur = found
		if x != n {
			found.Expand()
		}
		level = found.Children()
	}
	if cur != nil {
		p.selecting = true
		p.tree.SetCurrentNode(cur)
		p.tree.ScrollToNode(cur)
		p.selecting = false
	}
}

// runFunction runs the transform or checksum chosen over the selection
func (p *StructurePage) runFunction() {
	t := p.c.tab()
	f, ok := p.cmbFuncs.SelectedItemData().(tmpl.Func)
	if t == nil || !ok || t.tmpl == nil || t.tmpl.prog == nil {
		return
	}
	prog, last := t.tmpl.prog, t.tmpl.result
	off, n := target(t.view())
	if f.Kind == "checksum" {
		v, out, err := prog.Call(context.Background(), tmplEnv(t, false), last, f.Name, tmpl.Int(off), tmpl.Int(n))
		if err != nil {
			ui.ShowMessageBox(p.c, T().Error, err.Error())
			return
		}
		p.c.console.show(f.Title, append(out, f.Title+" = "+v.Text()), nil)
		ui.ShowToast(p.c, f.Title+" = "+v.Text(), ui.ToastInfo)
		return
	}
	p.c.runOp("transform", n, func(ctx context.Context, d *doc.Document, progress func(int64)) error {
		_, out, err := prog.Call(ctx, tmplEnv(t, true), last, f.Name, tmpl.Int(off), tmpl.Int(n))
		p.c.Form().Invoke(func() { p.c.console.show(f.Title, out, err) })
		return err
	})
}

// ---- The console (ANL-13) ----

// ConsolePage evaluates expressions over the current document, with the
// fields and the functions of its template, and shows what templates print
type ConsolePage struct {
	ui.Widget

	c       *MainForm
	out     *ui.Table
	lines   []string
	input   *ui.TextBox
	history []string
	hpos    int
	console *tmpl.Console
}

const consoleMaxLines = 5000

func newConsolePage(c *MainForm) *ConsolePage {
	var p ConsolePage
	p.InitWidget()
	p.SetPanelPadding(0)
	p.c = c
	p.console = tmpl.NewConsole()
	p.out = ui.NewTable()
	p.out.SetColumnCount(1)
	p.out.SetColumnName(0, T().PageConsole)
	p.out.SetStretchLastColumn(true)
	p.out.SetXExpandable(true)
	p.out.SetYExpandable(true)
	p.AddWidget(0, 0, p.out)
	row := ui.NewPanel()
	row.SetPanelPadding(0)
	p.input = ui.NewTextBox()
	p.input.SetXExpandable(true)
	p.input.SetOnKeyDown(p.key)
	row.AddWidget(0, 0, p.input)
	run := ui.NewButton(T().Evaluate)
	run.SetOnClick(p.eval)
	row.AddWidget(0, 1, run)
	clear := ui.NewButton(T().Clear)
	clear.SetOnClick(func() { p.lines = nil; p.out.SetRowCount(0) })
	row.AddWidget(0, 2, clear)
	p.AddWidget(1, 0, row)
	p.print(T().ConsoleHint)
	return &p
}

func (p *ConsolePage) refresh() {}

func (p *ConsolePage) print(lines ...string) {
	for _, l := range lines {
		for _, s := range strings.Split(l, "\n") {
			p.lines = append(p.lines, s)
		}
	}
	if over := len(p.lines) - consoleMaxLines; over > 0 {
		p.lines = p.lines[over:]
	}
	p.out.SetRowCount(len(p.lines))
	for i, l := range p.lines {
		p.out.SetCellText2(i, 0, l)
	}
	if len(p.lines) > 0 {
		p.out.SetCurrentCell2(len(p.lines)-1, 0)
	}
}

// show prints what a run of a template printed and how it ended
func (p *ConsolePage) show(name string, out []string, err error) {
	if len(out) == 0 && err == nil {
		return
	}
	p.print("— " + name)
	p.print(out...)
	if err != nil {
		p.print(T().Error + ": " + err.Error())
	}
}

// key goes through the history with the arrows and runs the line with Enter
func (p *ConsolePage) key(key ui.Key, mods ui.KeyModifiers) bool {
	switch key {
	case ui.KeyEnter:
		p.eval()
		return true
	case ui.KeyArrowUp:
		if p.hpos > 0 {
			p.hpos--
			p.input.SetText(p.history[p.hpos])
		}
		return true
	case ui.KeyArrowDown:
		if p.hpos < len(p.history)-1 {
			p.hpos++
			p.input.SetText(p.history[p.hpos])
		} else {
			p.hpos = len(p.history)
			p.input.SetText("")
		}
		return true
	}
	return false
}

func (p *ConsolePage) eval() {
	line := strings.TrimSpace(p.input.Text())
	if line == "" {
		return
	}
	p.history = append(p.history, line)
	p.hpos = len(p.history)
	p.input.SetText("")
	p.print("> " + line)
	t := p.c.tab()
	if t == nil {
		p.print(T().NoDocument)
		return
	}
	var prog *tmpl.Program
	var last *tmpl.Result
	if t.tmpl != nil {
		prog, last = t.tmpl.prog, t.tmpl.result
	}
	// The console may write (WriteUByte...): one edit
	t.doc.BeginGroup("console")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	ans, out, err := p.console.Eval(ctx, tmplEnv(t, !t.doc.ReadOnly()), prog, last, line)
	cancel()
	t.doc.EndGroup()
	p.print(out...)
	switch {
	case err != nil:
		var te *tmpl.Error
		if errors.As(err, &te) {
			p.print(T().Error + ": " + te.Msg)
		} else {
			p.print(T().Error + ": " + err.Error())
		}
	case ans != "":
		p.print(ans)
	}
}

func rgba(r, g, b, a byte) color.RGBA { return color.RGBA{r, g, b, a} }
