package forms

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ipoluianov/althex/core/analysis"
	"github.com/ipoluianov/althex/core/periph"
	"github.com/ipoluianov/nui/ui"
)

// ---- The memory map of the device (FW-2) ----

// addMemoryMap puts the table of the regions of the device at the row
func (p *FirmwarePage) addMemoryMap(row int) {
	t := T()
	p.AddWidget(row, 0, ui.NewLabel(t.MemoryMap))
	p.memmap = ui.NewTable()
	p.memmap.SetColumnCount(4)
	for i, n := range []string{t.ColName, t.ColAddress, t.ColSize, t.ColType} {
		p.memmap.SetColumnName(i, n)
	}
	p.memmap.SetColumnWidth(0, 140)
	p.memmap.SetColumnWidth(1, 90)
	p.memmap.SetColumnWidth(2, 90)
	p.memmap.SetStretchLastColumn(true)
	p.memmap.SetXExpandable(true)
	p.memmap.SetMinHeight(120)
	p.memmap.SetOnCellMouseDblClick(p.goToRegion)
	p.AddWidget(row+1, 0, p.memmap)
}

// showMemoryMap lists the regions of the device: Flash, SRAM, peripherals
func (p *FirmwarePage) showMemoryMap(dev *periph.Device) {
	p.regions = nil
	if dev != nil {
		p.regions = dev.MapRegions()
	}
	p.memmap.SetRowCount(len(p.regions))
	for i, r := range p.regions {
		p.memmap.SetCellText2(i, 0, r.Name)
		p.memmap.SetCellText2(i, 1, fmt.Sprintf("%X", r.Start))
		p.memmap.SetCellText2(i, 2, formatSize(int64(r.Size)))
		kind := r.Kind
		if r.Space != "" {
			kind += " (" + r.Space + ")"
		}
		p.memmap.SetCellText2(i, 3, kind)
	}
}

// goToRegion goes to the start of the region chosen when the image has it
func (p *FirmwarePage) goToRegion() {
	rows := p.memmap.SelectedRows()
	t := p.c.tab()
	if len(rows) == 0 || rows[0] >= len(p.regions) || t == nil {
		return
	}
	r := p.regions[rows[0]]
	var mem *fwMemory
	if t.fw != nil {
		mem = t.fw.mem
	} else if img, err := p.openImage(t); err == nil {
		if base, ok := ParseOffset(p.txtBase.Text()); ok {
			mem = &fwMemory{img: img, base: uint64(base), raw: isRaw(img)}
		}
	}
	// The data spaces of AVR are not in the image of the program
	inCode := r.Space == "" || r.Space == "prog"
	var off int64
	ok := false
	if mem != nil && inCode {
		// The image may start inside the region (a bootloader before it)
		for _, rg := range mem.ranges() {
			if rg.Addr+rg.Size > r.Start && rg.Addr < r.End() {
				off, ok = mem.toOff(max(r.Start, rg.Addr))
				if ok {
					break
				}
			}
		}
	}
	if !ok {
		ui.ShowToast(p.c, T().AddrNotInImage, ui.ToastInfo)
		return
	}
	if t.fw != nil {
		if addr, found := mem.toAddr(off); found {
			p.c.firmwareGoTo(t, addr)
			return
		}
	}
	t.jump(off)
}

// ---- The call tree of a function (FW-7) ----

// callTreeDepth is how deep the call tree goes
const callTreeDepth = 32

// callItem is a node of the call tree: the path of functions from the root
type callItem struct {
	path  []uint64
	cycle bool
}

// addCallTree puts the tree of the callers or callees of the function
// chosen at the row, with the export of the call graph
func (p *FunctionsPage) addCallTree(row int) {
	t := T()
	bar := ui.NewPanel()
	bar.SetPanelPadding(0)
	bar.AddWidget(0, 0, ui.NewLabel(t.CallTreeOf))
	p.cmbCalls = ui.NewComboBox()
	p.cmbCalls.AddItem(t.CallTreeCallees, false)
	p.cmbCalls.AddItem(t.CallTreeCallers, true)
	p.cmbCalls.SetSelectedIndex(0)
	p.cmbCalls.SetOnSelectedIndexChanged(func() { p.showCallTree(p.callRoot, true) })
	bar.AddWidget(0, 1, p.cmbCalls)
	bar.AddWidget(0, 2, ui.NewHSpacer())
	export := ui.NewButton(t.ExportCallGraph)
	export.SetOnClick(p.exportDOT)
	bar.AddWidget(0, 3, export)
	p.AddWidget(row, 0, bar)
	p.calls = ui.NewTreeView()
	p.calls.SetColumnCount(2)
	p.calls.SetColumnName(0, t.ColFunction)
	p.calls.SetColumnName(1, t.ColAddress)
	p.calls.SetColumnWidth(0, 240)
	p.calls.SetStretchLastColumn(true)
	p.calls.SetXExpandable(true)
	p.calls.SetYExpandable(true)
	p.calls.SetMinHeight(160)
	p.calls.SetOnExpand(p.expandCalls)
	p.calls.SetOnNodeActivated(func(n *ui.TreeNode) {
		it, _ := n.Data().(*callItem)
		if tab := p.c.tab(); it != nil && tab != nil {
			p.c.firmwareGoTo(tab, it.path[len(it.path)-1])
		}
	})
	p.AddWidget(row+1, 0, p.calls)
	p.funcs.SetOnSelectionChanged(func(r, col int) {
		if r >= 0 && r < len(p.list) {
			p.showCallTree(p.list[r].Addr, false)
		}
	})
}

func (p *FunctionsPage) result() *analysis.Result {
	if t := p.c.tab(); t != nil && t.fw != nil {
		return t.fw.res
	}
	return nil
}

func funcName(res *analysis.Result, addr uint64) string {
	if l := res.Labels[addr]; l != "" {
		return l
	}
	return fmt.Sprintf("sub_%X", addr)
}

// showCallTree shows the tree of the function at addr; again: even when it
// is shown already (the direction changed)
func (p *FunctionsPage) showCallTree(addr uint64, again bool) {
	res := p.result()
	if res == nil {
		p.calls.Clear()
		return
	}
	if addr == p.callRoot && !again && len(p.calls.Nodes()) > 0 {
		return
	}
	p.callRoot = addr
	p.calls.Clear()
	if _, ok := res.Funcs[addr]; !ok {
		return
	}
	n := p.calls.AddNode(nil, funcName(res, addr))
	n.SetText(1, fmt.Sprintf("%X", addr))
	n.SetData(&callItem{path: []uint64{addr}})
	n.SetHasChildren(len(res.CallChildren([]uint64{addr}, p.callers())) > 0)
	n.Expand()
}

// refreshCallTree shows the tree again after an analysis: of the function
// it showed, else of the one chosen in the list, else of the first one
func (p *FunctionsPage) refreshCallTree() {
	res := p.result()
	if res == nil {
		p.calls.Clear()
		return
	}
	root := p.callRoot
	if _, ok := res.Funcs[root]; !ok && len(p.list) > 0 {
		root = p.list[0].Addr
		if rows := p.funcs.SelectedRows(); len(rows) > 0 && rows[0] < len(p.list) {
			root = p.list[rows[0]].Addr
		}
	}
	p.showCallTree(root, true)
}

func (p *FunctionsPage) callers() bool {
	b, _ := p.cmbCalls.SelectedItemData().(bool)
	return b
}

// expandCalls adds the callers or callees of a node as it is opened
func (p *FunctionsPage) expandCalls(n *ui.TreeNode) {
	it, _ := n.Data().(*callItem)
	res := p.result()
	if it == nil || res == nil || it.cycle || n.ChildCount() > 0 || len(it.path) >= callTreeDepth {
		return
	}
	callers := p.callers()
	secondary := colorSecondaryText.get()
	for _, ch := range res.CallChildren(it.path, callers) {
		path := append(append(make([]uint64, 0, len(it.path)+1), it.path...), ch.Addr)
		text := funcName(res, ch.Addr)
		if ch.Cycle {
			text += " " + T().CallCycle
		}
		node := n.AddChild(text)
		node.SetText(1, fmt.Sprintf("%X", ch.Addr))
		node.SetData(&callItem{path: path, cycle: ch.Cycle})
		if ch.Cycle {
			node.SetColor(secondary)
			continue
		}
		if len(path) < callTreeDepth && len(res.CallChildren(path, callers)) > 0 {
			node.SetHasChildren(true)
		}
	}
}

// exportDOT writes the call graph for Graphviz
func (p *FunctionsPage) exportDOT() {
	t := p.c.tab()
	if t == nil || t.fw == nil || t.fw.res == nil {
		ui.ShowToast(p.c, T().AnalyzeFirst, ui.ToastInfo)
		return
	}
	res := t.fw.res
	name := strings.TrimSuffix(t.name(), filepath.Ext(t.name())) + ".dot"
	p.c.Form().ShowSaveFileDialog(ui.SaveFileDialogOptions{Title: T().ExportCallGraph, DefaultFileName: name}, func(path string, err error) {
		if err != nil || path == "" {
			return
		}
		out, err := os.Create(path)
		if err == nil {
			err = res.WriteDOT(out)
			if closeErr := out.Close(); err == nil {
				err = closeErr
			}
		}
		if err != nil {
			ui.ShowMessageBox(p.c, T().Error, err.Error())
		}
	})
}
