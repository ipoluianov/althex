package forms

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ipoluianov/althex/config"
	"github.com/ipoluianov/althex/core/analysis"
	"github.com/ipoluianov/althex/core/disasm"
	"github.com/ipoluianov/althex/core/fw"
	"github.com/ipoluianov/althex/core/periph"
	"github.com/ipoluianov/althex/forms/hexview"
	"github.com/ipoluianov/nui/ui"
)

// Firmware is the analysis of the firmware of a tab (FW-1..9)
type Firmware struct {
	img     *fw.Image
	mem     *fwMemory
	device  *periph.Device
	arch    disasm.Arch
	res     *analysis.Result
	notes   *analysis.Notes
	symbols []fw.Symbol // from the ELF symbol table and the map file
	items   []analysis.Item
	lines   []fwLine
	byAddr  map[uint64]int // the line of an address (its instruction or data line)

	listing *hexview.Listing
	split   *ui.Splitter
}

// fwLine is a line of the listing: an item, or the label before it
type fwLine struct {
	item  int
	label bool
}

// fwMemory is the address space of the firmware: the image's segments, a
// raw binary moved to the base address
type fwMemory struct {
	img  *fw.Image
	base uint64 // added to the addresses of a raw binary
	raw  bool
}

func (m *fwMemory) ReadAddr(p []byte, addr uint64) (int, error) {
	if m.raw {
		if addr < m.base {
			return 0, fw.ErrNoData
		}
		addr -= m.base
	}
	return m.img.ReadAddr(p, addr)
}

func (m *fwMemory) Contains(addr uint64) bool {
	if m.raw {
		if addr < m.base {
			return false
		}
		addr -= m.base
	}
	return m.img.Contains(addr)
}

// toOff returns the offset in the document of a device address
func (m *fwMemory) toOff(addr uint64) (int64, bool) {
	if m.raw {
		if addr < m.base {
			return 0, false
		}
		addr -= m.base
	}
	return m.img.AddrToOff(addr)
}

func (m *fwMemory) toAddr(off int64) (uint64, bool) {
	a, ok := m.img.OffToAddr(off)
	if ok && m.raw {
		a += m.base
	}
	return a, ok
}

func (m *fwMemory) ranges() []analysis.Range {
	var rs []analysis.Range
	for _, s := range m.img.Segments {
		addr := s.Addr
		if m.raw {
			addr += m.base
		}
		rs = append(rs, analysis.Range{Name: s.Name, Addr: addr, Size: uint64(s.Size)})
	}
	return rs
}

// notesPath is where the notes of the document of the tab are kept
func notesPath(t *Tab) string {
	name := t.doc.Path()
	if name == "" {
		name = t.doc.Name()
	}
	return analysis.NotesPath(filepath.Join(config.ConfigDirectory(), "firmware"), name)
}

// ---- The firmware page ----

// FirmwarePage chooses the device and the architecture of the current
// document, shows its vector table and configuration areas and starts the
// analysis
type FirmwarePage struct {
	ui.Widget

	c         *MainForm
	lblInfo   *ui.Label
	txtDevice *ui.TextBox
	cmbDevice *ui.ComboBox
	devRow    *ui.Panel
	cmbArch   *ui.ComboBox
	txtBase   *ui.TextBox
	vectors   *ui.Table
	vecs      []fw.Vector
	cmbConfig *ui.ComboBox
	config    *ui.Table
	tab       *Tab
	// memmap lists the regions of the device (FW-2)
	memmap  *ui.Table
	regions []periph.Region
}

func newFirmwarePage(c *MainForm) *FirmwarePage {
	var p FirmwarePage
	p.InitWidget()
	p.SetPanelPadding(0)
	p.c = c
	t := T()
	p.lblInfo = ui.NewLabel(t.FirmwareHint)
	p.AddWidget(0, 0, p.lblInfo)

	grid := ui.NewPanel()
	grid.SetPanelPadding(0)
	grid.AddWidget(0, 0, ui.NewLabel(t.Device))
	p.txtDevice = ui.NewTextBox()
	p.txtDevice.SetXExpandable(true)
	p.txtDevice.SetOnTextChanged(p.searchDevices)
	grid.AddWidget(0, 1, p.txtDevice)
	grid.AddWidget(1, 0, ui.NewLabel(""))
	p.devRow = ui.NewPanel()
	p.devRow.SetPanelPadding(0)
	grid.AddWidget(1, 1, p.devRow)
	grid.AddWidget(2, 0, ui.NewLabel(t.Architecture))
	p.cmbArch = ui.NewComboBox()
	for _, a := range disasm.Archs() {
		p.cmbArch.AddItem(a.Name(), a)
	}
	p.cmbArch.SetSelectedIndex(0)
	grid.AddWidget(2, 1, p.cmbArch)
	grid.AddWidget(3, 0, ui.NewLabel(t.LoadAddress))
	p.txtBase = ui.NewTextBox()
	p.txtBase.SetText("0x0")
	grid.AddWidget(3, 1, p.txtBase)
	p.AddWidget(1, 0, grid)
	p.searchDevices()

	buttons := ui.NewPanel()
	buttons.SetPanelPadding(0)
	add := func(col int, text string, f func()) {
		b := ui.NewButton(text)
		b.SetOnClick(f)
		buttons.AddWidget(0, col, b)
	}
	add(0, t.Detect, p.detect)
	add(1, t.Analyze, p.analyze)
	add(2, t.ExportAsm, p.exportAsm)
	buttons.AddWidget(0, 3, ui.NewHSpacer())
	p.AddWidget(2, 0, buttons)

	p.vectors = ui.NewTable()
	p.vectors.SetColumnCount(3)
	p.vectors.SetColumnName(0, "#")
	p.vectors.SetColumnName(1, t.ColName)
	p.vectors.SetColumnName(2, t.ColAddress)
	p.vectors.SetColumnWidth(0, 40)
	p.vectors.SetColumnWidth(1, 200)
	p.vectors.SetStretchLastColumn(true)
	p.vectors.SetXExpandable(true)
	p.vectors.SetYExpandable(true)
	p.vectors.SetOnCellMouseDblClick(p.goToVector)
	p.AddWidget(3, 0, p.vectors)

	cfg := ui.NewPanel()
	cfg.SetPanelPadding(0)
	p.cmbConfig = ui.NewComboBox()
	for _, k := range fw.ConfigKinds() {
		p.cmbConfig.AddItem(k, k)
	}
	p.cmbConfig.SetSelectedIndex(0)
	p.cmbConfig.SetXExpandable(true)
	cfg.AddWidget(0, 0, p.cmbConfig)
	decode := ui.NewButton(t.DecodeSelection)
	decode.SetOnClick(p.decodeConfig)
	cfg.AddWidget(0, 1, decode)
	p.AddWidget(4, 0, cfg)
	p.config = ui.NewTable()
	p.config.SetColumnCount(3)
	p.config.SetColumnName(0, t.ColName)
	p.config.SetColumnName(1, t.ColValue)
	p.config.SetColumnName(2, t.ColComment)
	p.config.SetColumnWidth(0, 120)
	p.config.SetColumnWidth(1, 70)
	p.config.SetStretchLastColumn(true)
	p.config.SetXExpandable(true)
	p.config.SetMinHeight(120)
	p.AddWidget(5, 0, p.config)
	p.addMemoryMap(6)
	return &p
}

func (p *FirmwarePage) refresh() {
	t := p.c.tab()
	if t == p.tab {
		return
	}
	p.tab = t
	if t == nil {
		return
	}
	notes := analysis.LoadNotes(notesPath(t))
	if notes.Device != "" {
		p.txtDevice.SetText(notes.Device)
	}
	if notes.Arch != "" {
		p.selectArch(notes.Arch)
	}
	if notes.Base != 0 {
		p.txtBase.SetText(fmt.Sprintf("0x%X", notes.Base))
	}
	if t.fw != nil {
		p.showVectors(t)
	}
}

// searchDevices lists the devices whose names have what is typed
func (p *FirmwarePage) searchDevices() {
	q := strings.TrimSpace(p.txtDevice.Text())
	var list []periph.DeviceInfo
	if q == "" {
		list = periph.Devices()
	} else {
		list = periph.Search(q)
	}
	// The combo box cannot be emptied: it is made anew
	p.devRow.RemoveAllWidgets()
	p.cmbDevice = ui.NewComboBox()
	p.cmbDevice.SetXExpandable(true)
	p.cmbDevice.AddItem(T().NoDevice, "")
	for i, d := range list {
		if i >= 300 {
			break
		}
		p.cmbDevice.AddItem(d.Name+" ("+d.Family+")", d.Name)
	}
	p.cmbDevice.SetSelectedIndex(0)
	if len(list) > 0 && q != "" {
		p.cmbDevice.SetSelectedIndex(1)
	}
	p.cmbDevice.SetOnSelectedIndexChanged(p.deviceChosen)
	p.devRow.AddWidget(0, 0, p.cmbDevice)
	if f := p.Form(); f != nil {
		f.UpdateLayout()
	}
}

// deviceChosen takes the architecture and the load address of the device (FW-2)
func (p *FirmwarePage) deviceChosen() {
	name, _ := p.cmbDevice.SelectedItemData().(string)
	if name == "" {
		p.showMemoryMap(nil)
		return
	}
	dev, err := periph.Load(name)
	if err != nil {
		return
	}
	p.showMemoryMap(dev)
	p.selectArch(dev.Arch)
	p.selectConfig(dev.Arch, dev.Name)
	p.txtBase.SetText(fmt.Sprintf("0x%X", dev.LoadAddr))
}

func (p *FirmwarePage) selectArch(id string) {
	for i := 0; i < p.cmbArch.ItemCount(); i++ {
		p.cmbArch.SetSelectedIndex(i)
		if a, _ := p.cmbArch.SelectedItemData().(disasm.Arch); a != nil && a.ID() == id {
			p.selectConfig(id, strings.TrimSpace(p.txtDevice.Text()))
			return
		}
	}
}

// selectConfig chooses the configuration area that fits the chip
func (p *FirmwarePage) selectConfig(arch, device string) {
	kind := fw.ConfigKindFor(device, "")
	dev := strings.ToUpper(device)
	switch {
	case kind != "":
	case arch == "avr":
		kind = "avr-fuses"
	case arch == "xtensa" || strings.HasPrefix(dev, "ESP32"):
		kind = "esp32-efuse"
	case strings.HasPrefix(dev, "STM32F4") || strings.HasPrefix(dev, "STM32F2"):
		kind = "stm32f4-option-bytes"
	case strings.HasPrefix(dev, "STM32"):
		kind = "stm32f1-option-bytes"
	}
	for i, k := range fw.ConfigKinds() {
		if k == kind {
			p.cmbConfig.SetSelectedIndex(i)
		}
	}
}

// autoAnalyze analyzes a document just opened when it is surely a firmware:
// a format of firmware, or a vector table found (FW-3), as the spec wants a
// firmware to show its code at once; onlyMCU - only for the formats of
// firmware of microcontrollers, not for the programs of a desktop
func (p *FirmwarePage) autoAnalyze(t *Tab, onlyMCU bool) {
	img, err := p.openImage(t)
	if err != nil {
		return
	}
	if onlyMCU {
		if img.Raw() {
			return // a file of a format of its own, not of a firmware
		}
		arch := img.Arch
		if arch == "" {
			if gs := fw.Detect(img); len(gs) > 0 {
				arch = gs[0].Arch
			}
		}
		switch arch {
		case "arm", "avr", "riscv32", "xtensa":
		default:
			return
		}
	}
	gs := fw.Detect(img)
	if len(gs) == 0 || gs[0].Score < 0.8 || gs[0].Arch == "" || disasm.ByID(gs[0].Arch) == nil {
		return
	}
	notes := analysis.LoadNotes(notesPath(t))
	g := gs[0]
	p.tab = t
	p.selectArch(g.Arch)
	p.txtBase.SetText(fmt.Sprintf("0x%X", g.Base))
	device := notes.Device
	if device == "" && g.Family != "" {
		// "ATmega328P/168/88" -> ATmega328P, when the base knows it exactly
		name := strings.Split(g.Family, "/")[0]
		if list := periph.Search(name); len(list) > 0 && strings.EqualFold(list[0].Name, name) {
			device = list[0].Name
		}
	}
	if device != "" {
		p.txtDevice.SetText(device)
		p.searchDevices()
		p.selectConfig(g.Arch, device)
	}
	p.lblInfo.SetText(fmt.Sprintf("%s — %s", img.Format, g.Reason))
	p.analyze()
	p.c.side.SetVisible(true)
	p.c.side.show(p.c.pages.firmware)
	if f := p.c.Form(); f != nil {
		f.UpdateLayout()
	}
}

// openImage reads the firmware of the tab: its format and segments (FW-1)
func (p *FirmwarePage) openImage(t *Tab) (*fw.Image, error) {
	snap := t.doc.Snapshot()
	return fw.Open(snap, snap.Size(), t.doc.Name())
}

// detect guesses the architecture, the base and the device (FW-3)
func (p *FirmwarePage) detect() {
	t := p.c.tab()
	if t == nil {
		return
	}
	img, err := p.openImage(t)
	if err != nil {
		ui.ShowMessageBox(p.c, T().Error, err.Error())
		return
	}
	info := img.Format
	guesses := fw.Detect(img)
	if len(guesses) == 0 {
		p.lblInfo.SetText(info + " — " + T().NothingDetected)
		return
	}
	g := guesses[0]
	p.selectArch(g.Arch)
	if img.Arch == "" || isRaw(img) {
		p.txtBase.SetText(fmt.Sprintf("0x%X", g.Base))
	}
	p.lblInfo.SetText(fmt.Sprintf("%s — %s (%.0f%%)", info, g.Reason, g.Score*100))
	if g.Family != "" && strings.TrimSpace(p.txtDevice.Text()) == "" {
		p.txtDevice.SetText(g.Family)
		p.searchDevices()
	}
}

func isRaw(img *fw.Image) bool { return strings.HasPrefix(img.Format, "Raw") }

// analyze disassembles the firmware from its entry and vectors (FW-6) and
// shows the listing next to the hex view
func (p *FirmwarePage) analyze() {
	t := p.c.tab()
	if t == nil {
		return
	}
	arch, _ := p.cmbArch.SelectedItemData().(disasm.Arch)
	base, ok := ParseOffset(p.txtBase.Text())
	if arch == nil || !ok {
		ui.ShowToast(p.c, T().BytesBad, ui.ToastError)
		return
	}
	img, err := p.openImage(t)
	if err != nil {
		ui.ShowMessageBox(p.c, T().Error, err.Error())
		return
	}
	var dev *periph.Device
	if name, _ := p.cmbDevice.SelectedItemData().(string); name != "" {
		dev, _ = periph.Load(name)
	}
	f := &Firmware{img: img, arch: arch, device: dev, mem: &fwMemory{img: img, base: uint64(base), raw: isRaw(img)}}
	f.symbols = loadSymbols(t, img)
	f.notes = analysis.LoadNotes(notesPath(t))
	f.notes.Arch = arch.ID()
	f.notes.Base = uint64(base)
	if dev != nil {
		f.notes.Device = dev.Name
	}
	entries := p.entries(f)
	form := p.c.Form()
	p.lblInfo.SetText(T().Analyzing)
	// Canceled by the link of the status bar (6.1): nothing is shown
	ctx, endTask := p.c.status.startTask()
	go func() {
		res := analysis.Analyze(ctx, f.mem, analysis.Options{
			Arch: arch, Entries: entries, Labels: symbolLabels(arch.ID(), f.symbols), Ranges: f.mem.ranges(), Chip: chipOf(f), Annotate: annotator(dev, dataSymbols(arch.ID(), f.symbols)),
		})
		form.Invoke(func() {
			canceled := ctx.Err() != nil
			endTask()
			if canceled {
				p.lblInfo.SetText(T().Canceled)
				return
			}
			f.res = res
			f.notes.Apply(res)
			f.notes.Save(notesPath(t))
			p.c.showFirmware(t, f)
			p.lblInfo.SetText(T().AnalysisDone(len(res.Insts), len(res.Funcs)))
			p.showVectors(t)
		})
	}()
}

// entries are where the code starts: the vectors, the entry of the image,
// the ones the user added
func (p *FirmwarePage) entries(f *Firmware) []analysis.Entry {
	var es []analysis.Entry
	mode := disasm.ModeDefault
	if f.arch.ID() == "arm" {
		mode = disasm.ModeThumb
	}
	if f.img.HasEntry {
		es = append(es, analysis.Entry{Addr: f.img.Entry &^ 1, Mode: mode, Name: "_start"})
	}
	if guesses := fw.Detect(rebased(f)); len(guesses) > 0 {
		g := guesses[0]
		// A handler of many vectors is the default one, not the last of them
		shared := make(map[uint64]int)
		for _, v := range g.Vectors {
			shared[v.Addr&^1]++
		}
		for _, v := range g.Vectors {
			if f.arch.ID() == "avr" && v.Addr != uint64(v.Off)+g.Base {
				// The vector is a jmp: code, the table of the listing
				name := ""
				if v.Index == 0 {
					name = "__vectors"
				}
				es = append(es, analysis.Entry{Addr: g.Base + uint64(v.Off), Name: name, Code: true})
			}
			if v.Addr == 0 || v.Index == 0 && f.arch.ID() == "arm" {
				continue // the initial stack pointer
			}
			name := vectorName(f, v)
			if shared[v.Addr&^1] > 1 {
				name = "default_handler"
			}
			es = append(es, analysis.Entry{Addr: v.Addr &^ 1, Mode: mode, Name: name})
		}
	}
	for _, sym := range f.symbols {
		if sym.Func {
			m := mode
			if f.arch.ID() == "arm" && !sym.Thumb && f.img.Arch == "arm" {
				m = disasm.ModeARM
			}
			es = append(es, analysis.Entry{Addr: sym.Addr, Mode: m, Name: sym.Name})
		}
	}
	es = append(es, f.notes.Entries...)
	return es
}

// vectorName is the name of a vector: the interrupt of the device when it
// is known
func vectorName(f *Firmware, v fw.Vector) string {
	if f.device == nil {
		return v.Name
	}
	n := v.Index // AVR: the number of the vector
	if f.arch.ID() == "arm" {
		n = v.Index - 16 // Cortex-M: IRQn, the exceptions are negative
	}
	for _, irq := range f.device.Interrupts {
		if irq.Number == n && irq.Name != "" {
			if f.arch.ID() == "arm" && n >= 0 {
				return irq.Name + "_IRQHandler"
			}
			return irq.Name
		}
	}
	return v.Name
}

// chipOf is what the analysis is told of the chip: the core of the device
// (cortex-m0+ has few 32-bit instructions), else the family the image or
// its vector table tells (ESP8266, ESP32-C3, RP2350)
func chipOf(f *Firmware) string {
	if f.device != nil && f.device.Core != "" {
		return f.device.Name + " " + f.device.Core
	}
	if f.img.Family != "" {
		return f.img.Family
	}
	if gs := fw.Detect(rebased(f)); len(gs) > 0 && gs[0].Family != "" {
		return gs[0].Family
	}
	if strings.Contains(f.img.Format, "RP2350") {
		return "RP2350"
	}
	return ""
}

// symbolLabels names the data of the symbol tables
func symbolLabels(arch string, syms []fw.Symbol) map[uint64]string {
	labels := make(map[uint64]string)
	for _, s := range syms {
		if !s.Func && (arch != "avr" || s.Addr < 0x800000) { // AVR data is not code space
			labels[s.Addr] = s.Name
		}
	}
	return labels
}

// loadSymbols are the symbols of the image and of a GNU ld map file next to
// it: fw.elf -> fw.map or fw.elf.map (FW-12)
func loadSymbols(t *Tab, img *fw.Image) []fw.Symbol {
	syms := img.Symbols
	path := t.doc.Path()
	if path == "" {
		return syms
	}
	for _, name := range []string{strings.TrimSuffix(path, filepath.Ext(path)) + ".map", path + ".map"} {
		if name == path {
			continue
		}
		if file, err := os.Open(name); err == nil {
			syms = append(append([]fw.Symbol(nil), syms...), fw.ParseMap(file)...)
			file.Close()
			break
		}
	}
	return syms
}

// rebased is the image of a firmware with a raw binary at its base address
func rebased(f *Firmware) *fw.Image {
	if !f.mem.raw || f.mem.base == 0 {
		return f.img
	}
	im := *f.img
	im.Segments = append([]fw.Segment(nil), f.img.Segments...)
	for i := range im.Segments {
		im.Segments[i].Addr += f.mem.base
	}
	return &im
}

// annotator names the peripheral registers reached by the code (FW-8) and
// the variables of the symbol tables (data: address -> name)
func annotator(dev *periph.Device, data map[uint64]string) func(addr uint64, store bool, value uint64, known bool) string {
	if dev == nil && len(data) == 0 {
		return nil
	}
	return func(addr uint64, store bool, value uint64, known bool) string {
		var reg *periph.Register
		if dev != nil {
			if r, ok := dev.RegisterAt(addr); ok {
				reg = r
			}
		}
		if reg == nil {
			name := data[addr]
			if name == "" && dev != nil {
				// Not a register: the region, "SRAM+0x1C" (FW-2)
				name = dev.RegionLabel(addr)
			}
			if name == "" {
				return ""
			}
			if store && known {
				return fmt.Sprintf("%s = 0x%X", name, value)
			} else if store {
				return name + " <-"
			}
			return name
		}
		label := reg.Label()
		whole := addr == reg.Addr && dev.Arch != "avr" || reg.Size <= 8
		switch {
		case dev.Arch == "avr" && reg.Size == 16 && addr-reg.Addr < 2:
			// The bytes of a 16-bit AVR register: SPL, SPH, TCNT1L...
			label += [...]string{"L", "H"}[addr-reg.Addr]
		case addr != reg.Addr:
			label += fmt.Sprintf("+%d", addr-reg.Addr)
		}
		if store && known {
			var set []string
			if whole {
				for _, fv := range reg.Decode(value) {
					if fv.Value != 0 {
						set = append(set, fv.Text)
					}
				}
			}
			if len(set) == 0 {
				label += fmt.Sprintf(" = 0x%X", value)
			} else {
				label += " = " + strings.Join(set, " | ")
			}
		} else if store {
			label += " <-"
		}
		return label
	}
}

// dataSymbols are the variables of the symbol tables by their data address:
// AVR keeps the data space at 0x800000 in its ELF files
func dataSymbols(arch string, syms []fw.Symbol) map[uint64]string {
	data := make(map[uint64]string)
	for _, s := range syms {
		addr := s.Addr
		switch {
		case s.Func:
			continue
		case arch == "avr":
			if s.Addr < 0x800000 || s.Addr >= 0x810000 {
				continue
			}
			addr -= 0x800000
		}
		data[addr] = s.Name
		// The bytes of a small variable: timer0_millis+2
		for k := uint64(1); k < min(s.Size, 64); k++ {
			if _, taken := data[addr+k]; !taken {
				data[addr+k] = fmt.Sprintf("%s+%d", s.Name, k)
			}
		}
	}
	return data
}

// showVectors lists the vector table of the firmware (FW-5)
func (p *FirmwarePage) showVectors(t *Tab) {
	if t.fw != nil && t.fw.device != nil {
		p.showMemoryMap(t.fw.device)
	}
	p.vecs = nil
	if t.fw != nil {
		if gs := fw.Detect(rebased(t.fw)); len(gs) > 0 {
			p.vecs = gs[0].Vectors
		}
	}
	p.vectors.SetRowCount(len(p.vecs))
	for i, v := range p.vecs {
		name := v.Name
		if t.fw != nil {
			name = vectorName(t.fw, v)
			if t.fw.res != nil {
				if l := t.fw.res.Labels[v.Addr&^1]; l != "" && l != analysis.Ident(name) {
					name += " -> " + l
				}
			}
		}
		p.vectors.SetCellText2(i, 0, fmt.Sprint(v.Index))
		p.vectors.SetCellText2(i, 1, name)
		p.vectors.SetCellText2(i, 2, fmt.Sprintf("%X", v.Addr))
	}
}

func (p *FirmwarePage) goToVector() {
	rows := p.vectors.SelectedRows()
	t := p.c.tab()
	if len(rows) == 0 || rows[0] >= len(p.vecs) || t == nil || t.fw == nil {
		return
	}
	p.c.firmwareGoTo(t, p.vecs[rows[0]].Addr&^1)
}

// decodeConfig decodes the selection as a configuration area (FW-11)
func (p *FirmwarePage) decodeConfig() {
	t := p.c.tab()
	kind, _ := p.cmbConfig.SelectedItemData().(string)
	if t == nil || kind == "" {
		return
	}
	off, n := t.view().Selection()
	if n == 0 {
		ui.ShowToast(p.c, T().SelectConfig, ui.ToastInfo)
		return
	}
	b := make([]byte, min(n, 4096))
	t.doc.ReadAt(b, off)
	fields, err := fw.DecodeConfig(kind, b)
	if err != nil {
		ui.ShowMessageBox(p.c, T().Error, err.Error())
		return
	}
	p.config.SetRowCount(len(fields))
	for i, f := range fields {
		p.config.SetCellText2(i, 0, f.Name)
		p.config.SetCellText2(i, 1, fmt.Sprintf("0x%X", f.Value))
		p.config.SetCellText2(i, 2, f.Meaning)
	}
}

// exportAsm writes the listing as an assembler source (FW-9)
func (p *FirmwarePage) exportAsm() {
	t := p.c.tab()
	if t == nil || t.fw == nil || t.fw.res == nil {
		ui.ShowToast(p.c, T().AnalyzeFirst, ui.ToastInfo)
		return
	}
	f := t.fw
	name := strings.TrimSuffix(t.name(), filepath.Ext(t.name())) + ".s"
	p.c.Form().ShowSaveFileDialog(ui.SaveFileDialogOptions{Title: T().ExportAsm, DefaultFileName: name}, func(path string, err error) {
		if err != nil || path == "" {
			return
		}
		out, err := os.Create(path)
		if err == nil {
			header, footer, comment := analysis.AsmSyntax(f.arch.ID())
			err = f.res.WriteAsm(out, f.mem.ranges(), analysis.AsmOptions{
				Header:   header,
				Footer:   footer,
				Comment:  comment,
				Comments: f.notes.CommentMap(),
				Read:     func(b []byte, addr uint64) { f.mem.ReadAddr(b, addr) },
			})
			if closeErr := out.Close(); err == nil {
				err = closeErr
			}
		}
		if err != nil {
			ui.ShowMessageBox(p.c, T().Error, err.Error())
		}
	})
}

// ---- The listing next to the hex view ----

// showFirmware puts the listing of the analysis next to the hex view of the tab
func (c *MainForm) showFirmware(t *Tab, f *Firmware) {
	old := t.fw
	t.fw = f
	f.buildLines()
	if old != nil && old.listing != nil {
		f.listing, f.split = old.listing, old.split
	} else {
		f.listing = hexview.NewListing()
		f.split = ui.NewHSplitter()
		t.box.RemoveAllWidgets()
		f.split.SetWidgets(t.views[0], f.listing)
		t.box.AddWidget(0, 0, f.split)
		if form := c.Form(); form != nil {
			form.UpdateLayout()
		}
		f.split.SetSecondSize(t.box.Width() / 2)
	}
	f.listing.SetSource(&fwSource{f: f})
	f.listing.OnSelect = func(i int) { c.listingSelected(t, i) }
	f.listing.OnActivate = func(i int) { c.listingActivated(t, i) }
	f.listing.OnKey = func(key ui.Key, mods ui.KeyModifiers) bool { return c.listingKey(t, key, mods) }
	c.functions.refresh()
	c.update()
}

// buildLines makes the lines of the listing from the analysis
func (f *Firmware) buildLines() {
	f.items = f.res.Items(f.mem.ranges())
	f.lines = f.lines[:0]
	f.byAddr = make(map[uint64]int, len(f.items))
	for i, it := range f.items {
		if f.res.Labels[it.Addr] != "" {
			f.lines = append(f.lines, fwLine{item: i, label: true})
		}
		f.byAddr[it.Addr] = len(f.lines)
		f.lines = append(f.lines, fwLine{item: i})
	}
}

// lineOf returns the line of the item holding the address
func (f *Firmware) lineOf(addr uint64) (int, bool) {
	i := sort.Search(len(f.items), func(i int) bool { return f.items[i].Addr > addr }) - 1
	if i < 0 {
		return 0, false
	}
	it := f.items[i]
	if addr >= it.Addr+uint64(it.Len) {
		return 0, false
	}
	return f.byAddr[it.Addr], true
}

type fwSource struct{ f *Firmware }

func (s *fwSource) Len() int { return len(s.f.lines) }

func (s *fwSource) Line(i int) hexview.ListingLine {
	f := s.f
	ln := f.lines[i]
	it := f.items[ln.item]
	if ln.label {
		kind := hexview.LineLabel
		if _, isFunc := f.res.Funcs[it.Addr]; isFunc {
			kind = hexview.LineFunc
		}
		return hexview.ListingLine{Addr: it.Addr, Label: true, Text: f.res.Labels[it.Addr], Kind: kind}
	}
	out := hexview.ListingLine{Addr: it.Addr, Comment: f.notes.Comment(it.Addr)}
	if it.Code != nil {
		b := it.Code.Bytes
		out.Bytes = disasm.FormatBytes(b[:min(len(b), 6)])
		out.Text = it.Code.Mnemonic
		if ops := f.res.OperandText(it.Code); ops != "" {
			out.Text += " " + ops
		}
		if it.Code.Note != "" {
			out.Comment = joinNote(it.Code.Note, out.Comment)
		}
		out.Kind = hexview.LineCode
		return out
	}
	b := make([]byte, it.Len)
	f.mem.ReadAddr(b, it.Addr)
	out.Bytes = disasm.FormatBytes(b[:min(len(b), 6)])
	if it.Literal && len(b) == 4 {
		v := uint64(b[0]) | uint64(b[1])<<8 | uint64(b[2])<<16 | uint64(b[3])<<24
		out.Text = fmt.Sprintf(".word 0x%08X", v)
		if l := f.res.Labels[v&^1]; l != "" {
			out.Text += " (" + l + ")"
		} else if f.device != nil {
			if reg, ok := f.device.RegisterAt(v); ok {
				out.Text += " (" + reg.Label() + ")"
			} else if r, ok := f.device.RegionAt(v); ok && r.Kind != "flash" && r.Kind != "rom" {
				out.Text += " (" + f.device.RegionLabel(v) + ")"
			}
		}
	} else {
		out.Text = ".byte " + disasm.FormatBytes(b)
	}
	out.Kind = hexview.LineData
	return out
}

func joinNote(a, b string) string {
	if b == "" {
		return a
	}
	return a + "; " + b
}

// listingSelected selects the bytes of the line in the hex view (FW-9)
func (c *MainForm) listingSelected(t *Tab, i int) {
	f := t.fw
	it := f.items[f.lines[i].item]
	if off, ok := f.mem.toOff(it.Addr); ok {
		t.syncingListing = true
		t.views[0].Select(off, int64(it.Len))
		t.syncingListing = false
	}
	c.functions.showRefs(t, it.Addr)
}

// listingActivated goes to the target of a jump or a call, or to the
// address a literal holds
func (c *MainForm) listingActivated(t *Tab, i int) {
	f := t.fw
	it := f.items[f.lines[i].item]
	if it.Code != nil && it.Code.HasTarget {
		c.firmwareGoTo(t, it.Code.Target)
		return
	}
	if it.Literal {
		b := make([]byte, 4)
		f.mem.ReadAddr(b, it.Addr)
		v := uint64(b[0]) | uint64(b[1])<<8 | uint64(b[2])<<16 | uint64(b[3])<<24
		c.firmwareGoTo(t, v&^1)
	}
}

// firmwareGoTo shows an address in the listing and in the hex view
func (c *MainForm) firmwareGoTo(t *Tab, addr uint64) {
	f := t.fw
	if f == nil {
		return
	}
	if i, ok := f.lineOf(addr); ok {
		// The label line before the item, if there is one
		if i > 0 && f.lines[i-1].label && f.items[f.lines[i-1].item].Addr == addr {
			i--
		}
		if f.listing != nil {
			f.listing.SetCurrent(i)
		}
	}
	if off, ok := f.mem.toOff(addr); ok {
		t.jump(off)
	}
}

// syncListing shows the line of the caret of the hex view in the listing
func (c *MainForm) syncListing() {
	t := c.tab()
	if t == nil || t.fw == nil || t.fw.listing == nil || t.syncingListing {
		return
	}
	// The bytes of a line chosen in the listing are selected, the caret past
	// them: the line is the one the selection starts in
	start, n := t.views[0].Selection()
	addr, ok := t.fw.mem.toAddr(syncOffset(t.views[0].Caret(), start, n))
	if !ok {
		return
	}
	if i, ok := t.fw.lineOf(addr); ok && i != t.fw.listing.Current() {
		t.fw.listing.SetCurrent(i)
	}
}

// listingKey renames a label (N), comments a line (;), marks code (C) (FW-7)
func (c *MainForm) listingKey(t *Tab, key ui.Key, mods ui.KeyModifiers) bool {
	f := t.fw
	i := f.listing.Current()
	if i < 0 {
		return false
	}
	it := f.items[f.lines[i].item]
	addr := it.Addr
	switch key {
	case ui.KeyN:
		target := addr
		if it.Code != nil && it.Code.HasTarget && !f.lines[i].label {
			target = it.Code.Target // the name of the function called
		}
		c.ShowDialog(NewValueDialog(T().Rename, T().ColName, f.res.Labels[target], func(s string) error {
			s = strings.TrimSpace(s)
			s = analysis.Ident(s)
			f.notes.SetLabel(target, s)
			if s == "" {
				delete(f.res.Labels, target)
			} else {
				f.res.Labels[target] = s
			}
			f.notes.Save(notesPath(t))
			f.buildLines()
			f.listing.SetSource(&fwSource{f: f})
			c.firmwareGoTo(t, addr)
			c.functions.refresh()
			return nil
		}))
	case ui.KeySemicolon:
		c.ShowDialog(NewValueDialog(T().CommentTitle, T().ColComment, f.notes.Comment(addr), func(s string) error {
			f.notes.SetComment(addr, strings.TrimSpace(s))
			f.notes.Save(notesPath(t))
			c.update()
			return nil
		}))
	case ui.KeyC:
		// Code here: an entry for the analysis (FW-6, code the flow did not reach)
		mode := disasm.ModeDefault
		if f.arch.ID() == "arm" {
			mode = disasm.ModeThumb
		}
		f.notes.Entries = append(f.notes.Entries, analysis.Entry{Addr: addr, Mode: mode})
		f.notes.Save(notesPath(t))
		c.firmware.analyze()
	default:
		return false
	}
	return true
}

// ---- Functions and references (FW-7) ----

// FunctionsPage lists the functions found and the references to the line
// chosen in the listing
type FunctionsPage struct {
	ui.Widget

	c      *MainForm
	funcs  *ui.Table
	refs   *ui.Table
	list   []*analysis.Func
	refsOf []analysis.Ref
	// The call tree of the function chosen (FW-7)
	cmbCalls *ui.ComboBox
	calls    *ui.TreeView
	callRoot uint64
}

func newFunctionsPage(c *MainForm) *FunctionsPage {
	var p FunctionsPage
	p.InitWidget()
	p.SetPanelPadding(0)
	p.c = c
	t := T()
	p.funcs = ui.NewTable()
	p.funcs.SetColumnCount(4)
	for i, n := range []string{t.ColName, t.ColAddress, t.ColSize, t.ColCallers} {
		p.funcs.SetColumnName(i, n)
	}
	p.funcs.SetColumnWidth(0, 180)
	p.funcs.SetColumnWidth(1, 90)
	p.funcs.SetColumnWidth(2, 60)
	p.funcs.SetStretchLastColumn(true)
	p.funcs.SetXExpandable(true)
	p.funcs.SetYExpandable(true)
	p.funcs.SetOnCellMouseDblClick(p.goToFunc)
	p.AddWidget(0, 0, p.funcs)
	p.AddWidget(1, 0, ui.NewLabel(t.ReferencesTo))
	p.refs = ui.NewTable()
	p.refs.SetColumnCount(3)
	p.refs.SetColumnName(0, t.ColAddress)
	p.refs.SetColumnName(1, t.ColType)
	p.refs.SetColumnName(2, t.ColFunction)
	p.refs.SetColumnWidth(0, 90)
	p.refs.SetColumnWidth(1, 70)
	p.refs.SetStretchLastColumn(true)
	p.refs.SetXExpandable(true)
	p.refs.SetMinHeight(140)
	p.refs.SetOnCellMouseDblClick(p.goToRef)
	p.AddWidget(2, 0, p.refs)
	p.addCallTree(3)
	return &p
}

func (p *FunctionsPage) refresh() {
	t := p.c.tab()
	if t == nil || t.fw == nil || t.fw.res == nil {
		p.list = nil
		p.funcs.SetRowCount(0)
		p.calls.Clear()
		return
	}
	res := t.fw.res
	p.list = res.SortedFuncs()
	p.funcs.SetRowCount(len(p.list))
	for i, f := range p.list {
		p.funcs.SetCellText2(i, 0, res.Labels[f.Addr])
		p.funcs.SetCellText2(i, 1, fmt.Sprintf("%X", f.Addr))
		p.funcs.SetCellText2(i, 2, fmt.Sprint(f.End-f.Addr))
		p.funcs.SetCellText2(i, 3, fmt.Sprint(len(f.Callers)))
	}
	p.refreshCallTree()
}

func (p *FunctionsPage) goToFunc() {
	rows := p.funcs.SelectedRows()
	if t := p.c.tab(); t != nil && len(rows) > 0 && rows[0] < len(p.list) {
		p.c.firmwareGoTo(t, p.list[rows[0]].Addr)
	}
}

// showRefs lists the references to an address (the cross references)
func (p *FunctionsPage) showRefs(t *Tab, addr uint64) {
	res := t.fw.res
	p.refsOf = res.Refs[addr]
	p.refs.SetRowCount(len(p.refsOf))
	for i, r := range p.refsOf {
		p.refs.SetCellText2(i, 0, fmt.Sprintf("%X", r.From))
		p.refs.SetCellText2(i, 1, r.Kind.String())
		fn := ""
		if f, ok := res.FuncAt(r.From); ok {
			fn = res.Labels[f.Addr]
		}
		p.refs.SetCellText2(i, 2, fn)
	}
}

func (p *FunctionsPage) goToRef() {
	rows := p.refs.SelectedRows()
	if t := p.c.tab(); t != nil && len(rows) > 0 && rows[0] < len(p.refsOf) {
		p.c.firmwareGoTo(t, p.refsOf[rows[0]].From)
	}
}
