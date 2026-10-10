package forms

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/ipoluianov/althex/app"
	"github.com/ipoluianov/althex/core/bootimg"
	"github.com/ipoluianov/althex/core/disasm"
	"github.com/ipoluianov/althex/core/doc"
	"github.com/ipoluianov/althex/core/fs"
	diskimg "github.com/ipoluianov/althex/core/image"
	"github.com/ipoluianov/althex/core/part"
	"github.com/ipoluianov/althex/core/source"
	"github.com/ipoluianov/althex/forms/hexview"
	"github.com/ipoluianov/nui/ui"
)

// openNested opens a part of something as a document of its own in a new
// tab: a partition, a file of a file system, the disk inside an image
func (c *MainForm) openNested(src source.Source, name string) {
	d, err := doc.OpenSource(src, name)
	if err != nil {
		ui.ShowMessageBox(c, T().Error, err.Error())
		return
	}
	c.detectTemplate(c.addTab(d))
}

// offerDiskImage asks whether to open the disk inside a container file
// (VHD, VMDK...) when one is opened (BOOT-1)
func (c *MainForm) offerDiskImage(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	st, _ := f.Stat()
	format, ok := diskimg.Detect(f, st.Size())
	f.Close()
	if !ok {
		return
	}
	ui.ShowQuestionMessageBoxYesNo(c, app.DisplayName, T().OpenDiskInside(filepath.Base(path), format), func() {
		disk, err := diskimg.Open(path)
		if err != nil {
			ui.ShowMessageBox(c, T().Error, err.Error())
			return
		}
		c.openNested(disk, filepath.Base(path)+" ["+disk.Format()+"]")
	}, nil)
}

// ---- Opening a disk (SRC-3) ----

// OpenDiskDialog lists the disks and partitions of the computer; the one
// chosen opens read-only
type OpenDiskDialog struct {
	ui.DialogContent

	table *ui.Table
	disks []source.DiskInfo
	open  func(path string)
}

func NewOpenDiskDialog(open func(path string)) *OpenDiskDialog {
	var c OpenDiskDialog
	c.InitWidget()
	c.open = open
	all, _ := source.ListDisks()
	for _, d := range all {
		parts := d.Partitions
		d.Partitions = nil
		c.disks = append(c.disks, d)
		for _, p := range parts {
			p.Name = "    " + p.Name
			c.disks = append(c.disks, p)
		}
	}
	c.table = ui.NewTable()
	c.table.SetColumnCount(4)
	for i, n := range []string{T().ColName, T().ColSize, T().ColModel, T().ColPath} {
		c.table.SetColumnName(i, n)
	}
	c.table.SetColumnWidth(0, 160)
	c.table.SetColumnWidth(1, 130)
	c.table.SetColumnWidth(2, 200)
	c.table.SetStretchLastColumn(true)
	c.table.SetXExpandable(true)
	c.table.SetYExpandable(true)
	c.table.SetRowCount(len(c.disks))
	for i, d := range c.disks {
		c.table.SetCellText2(i, 0, d.Name)
		c.table.SetCellText2(i, 1, formatSize(d.Size))
		c.table.SetCellText2(i, 2, d.Model)
		c.table.SetCellText2(i, 3, d.Path)
	}
	c.table.SetOnCellMouseDblClick(c.accept)
	c.AddWidget(0, 0, c.table)
	note := ui.NewLabel(T().DiskReadOnlyNote)
	note.SetForegroundColor(colorSecondaryText.get())
	c.AddWidget(1, 0, note)
	buttons := ui.NewPanel()
	ok := ui.NewButton(T().MenuOpen)
	ok.SetOnClick(c.accept)
	cancel := ui.NewButton(ui.UIText().Cancel)
	cancel.SetOnClick(func() { c.Form().Close() })
	buttons.AddWidget(0, 0, ui.NewHSpacer())
	buttons.AddWidget(0, 1, ok)
	buttons.AddWidget(0, 2, cancel)
	c.AddWidget(2, 0, buttons)
	c.OnDialogShow = func() {
		c.Form().SetTitle(T().MenuOpenDisk)
		c.Form().SetSize(720, 420)
		c.Form().MoveToCenterOfParent()
		c.Form().SetAcceptButton(ok)
		c.Form().SetCancelButton(cancel)
	}
	return &c
}

func (c *OpenDiskDialog) accept() {
	rows := c.table.SelectedRows()
	if len(rows) == 0 || rows[0] >= len(c.disks) {
		return
	}
	p := c.disks[rows[0]].Path
	c.RunInParent(func() { c.open(p) })
	c.Form().Close()
}

// openDisk opens a disk or a partition read-only; writing it asks twice
func (c *MainForm) openDisk(path string) {
	d, err := doc.Open(path, doc.Options{ReadOnly: true})
	if err != nil {
		msg := err.Error()
		if errors.Is(err, os.ErrPermission) {
			msg = T().DiskNeedsRights(path)
		}
		ui.ShowMessageBox(c, T().Error, msg)
		return
	}
	t := c.addTab(d)
	c.side.SetVisible(true)
	c.side.show(c.pages.disk)
	_ = t
	if f := c.Form(); f != nil {
		f.UpdateLayout()
	}
}

// confirmDeviceWrite asks twice before a disk is made writable or saved
// (6.2: the device and the range are named)
func (c *MainForm) confirmDeviceWrite(d *doc.Document, what string, then func()) {
	c.confirmDeviceWriteOf(d, what, "", then)
}

// confirmDeviceWriteOf asks twice, both questions naming the device and
// the ranges to be written (ranges: "" - none known yet)
func (c *MainForm) confirmDeviceWriteOf(d *doc.Document, what, ranges string, then func()) {
	ui.ShowQuestionMessageBoxYesNo(c, T().DiskWriteTitle, T().DiskWriteAsk1(d.Path(), what)+ranges, func() {
		ui.ShowQuestionMessageBoxYesNo(c, T().DiskWriteTitle, T().DiskWriteAsk2(d.Path())+ranges, then, nil)
	}, nil)
}

// confirmDeviceSave asks twice before the edits of a disk are saved, naming
// the device and the ranges of it saving writes (6.2)
func (c *MainForm) confirmDeviceSave(d *doc.Document, then func()) {
	rs, rewrite := d.Writes()
	if rewrite {
		// A disk keeps its size: saving would fail, nothing is asked
		ui.ShowMessageBox(c, T().Error, T().SaveFailed(doc.ErrDeviceSize.Error()))
		return
	}
	c.confirmDeviceWriteOf(d, T().DiskWriteSave, describeWrites(rs, 8), then)
}

// describeWrites tells the ranges written: the first ones of them as
// offsets in hex, how many there are and their bytes in all
func describeWrites(rs []doc.Range, show int) string {
	var total int64
	for _, r := range rs {
		total += r.Len
	}
	parts := make([]string, 0, min(len(rs), show))
	for _, r := range rs[:min(len(rs), show)] {
		parts = append(parts, fmt.Sprintf("0x%X..0x%X", r.Off, r.Off+r.Len-1))
	}
	return T().DiskWriteRanges(len(rs), total, strings.Join(parts, ", "), len(rs)-len(parts))
}

// ---- The disk page: partitions and their problems (BOOT-2, BOOT-3) ----

// DiskPage shows the partition table of the current document and the
// problems found in it; a partition opens as a document of its own
type DiskPage struct {
	ui.Widget

	c        *MainForm
	lblInfo  *ui.Label
	parts    *ui.Table
	issues   *ui.Table
	table    *part.Table
	shownFor *doc.Snapshot
}

func newDiskPage(c *MainForm) *DiskPage {
	var p DiskPage
	p.InitWidget()
	p.SetPanelPadding(0)
	p.c = c
	t := T()
	p.lblInfo = ui.NewLabel("")
	p.AddWidget(0, 0, p.lblInfo)
	p.parts = ui.NewTable()
	p.parts.SetColumnCount(5)
	for i, n := range []string{"#", t.ColType, t.ColOffset, t.ColSize, t.ColName} {
		p.parts.SetColumnName(i, n)
	}
	p.parts.SetColumnWidth(0, 40)
	p.parts.SetColumnWidth(1, 160)
	p.parts.SetColumnWidth(2, 100)
	p.parts.SetColumnWidth(3, 100)
	p.parts.SetStretchLastColumn(true)
	p.parts.SetXExpandable(true)
	p.parts.SetYExpandable(true)
	p.parts.SetOnCellMouseDblClick(p.goToPartition)
	p.AddWidget(1, 0, p.parts)

	buttons := ui.NewPanel()
	buttons.SetPanelPadding(0)
	add := func(col int, text string, f func()) {
		b := ui.NewButton(text)
		b.SetOnClick(f)
		buttons.AddWidget(0, col, b)
	}
	add(0, t.OpenPartition, p.openPartition)
	add(1, t.ShowFiles, p.showFiles)
	add(2, t.BootCode, p.bootCode)
	add(3, t.Refresh, func() { p.shownFor = nil; p.refresh() })
	buttons.AddWidget(0, 4, ui.NewHSpacer())
	p.AddWidget(2, 0, buttons)

	p.issues = ui.NewTable()
	p.issues.SetColumnCount(2)
	p.issues.SetColumnName(0, t.ColOffset)
	p.issues.SetColumnName(1, t.ColProblem)
	p.issues.SetColumnWidth(0, 100)
	p.issues.SetStretchLastColumn(true)
	p.issues.SetXExpandable(true)
	p.issues.SetMinHeight(120)
	p.issues.SetOnCellMouseDblClick(p.goToIssue)
	p.AddWidget(3, 0, p.issues)
	return &p
}

func (p *DiskPage) refresh() {
	t := p.c.tab()
	if t == nil {
		p.lblInfo.SetText("")
		p.parts.SetRowCount(0)
		p.issues.SetRowCount(0)
		return
	}
	snap := t.doc.Snapshot()
	if snap == p.shownFor {
		return
	}
	p.shownFor = snap
	tbl, err := part.Read(snap, snap.Size(), 0)
	p.table = tbl
	t.diskIssues = nil
	if err != nil {
		p.lblInfo.SetText(T().NoPartitionTable)
		p.parts.SetRowCount(0)
		p.issues.SetRowCount(0)
		return
	}
	info := tbl.Scheme
	if tbl.DiskGUID != "" {
		info += ", " + tbl.DiskGUID
	}
	p.lblInfo.SetText(T().PartitionInfo(info, len(tbl.Partitions), len(tbl.Issues)))
	p.parts.SetRowCount(len(tbl.Partitions))
	for i, pt := range tbl.Partitions {
		idx := strconv.Itoa(pt.Index)
		if pt.Bootable {
			idx += "*"
		}
		p.parts.SetCellText2(i, 0, idx)
		p.parts.SetCellText2(i, 1, pt.Type)
		p.parts.SetCellText2(i, 2, fmt.Sprintf("%X", pt.Start))
		p.parts.SetCellText2(i, 3, formatSize(pt.Size))
		p.parts.SetCellText2(i, 4, pt.Name)
	}
	p.issues.SetRowCount(len(tbl.Issues))
	for i, is := range tbl.Issues {
		p.issues.SetCellText2(i, 0, fmt.Sprintf("%X", is.Off))
		p.issues.SetCellText2(i, 1, is.Message)
		marks := hexview.Highlight{Off: is.Off, Len: max(is.Len, 1), Color: colorChanged.get(), Frame: true}
		t.diskIssues = append(t.diskIssues, marks)
	}
	p.c.update()
}

// selected returns the partition of the current row
func (p *DiskPage) selected() *part.Partition {
	rows := p.parts.SelectedRows()
	if p.table == nil || len(rows) == 0 || rows[0] >= len(p.table.Partitions) {
		return nil
	}
	return &p.table.Partitions[rows[0]]
}

func (p *DiskPage) goToPartition() {
	if pt := p.selected(); pt != nil {
		if t := p.c.tab(); t != nil {
			t.jump(pt.Start)
			t.view().Select(pt.Start, min(pt.Size, t.doc.Size()-pt.Start))
		}
	}
}

func (p *DiskPage) goToIssue() {
	rows := p.issues.SelectedRows()
	if p.table == nil || len(rows) == 0 || rows[0] >= len(p.table.Issues) {
		return
	}
	is := p.table.Issues[rows[0]]
	if t := p.c.tab(); t != nil {
		t.jump(is.Off)
		t.view().Select(is.Off, is.Len)
	}
}

// partitionSource returns the bytes of the partition chosen, or of the
// whole document when there are no partitions (a volume)
func (p *DiskPage) partitionSource() (source.Source, string, bool) {
	t := p.c.tab()
	if t == nil {
		return nil, "", false
	}
	snap := t.doc.Snapshot()
	if pt := p.selected(); pt != nil {
		size := min(pt.Size, snap.Size()-pt.Start)
		name := fmt.Sprintf("%s [%d: %s]", t.name(), pt.Index, pt.Type)
		return source.NewSection(snap, pt.Start, size), name, true
	}
	return source.NewSection(snap, 0, snap.Size()), t.name(), true
}

func (p *DiskPage) openPartition() {
	if p.selected() == nil {
		ui.ShowToast(p.c, T().ChoosePartition, ui.ToastInfo)
		return
	}
	src, name, ok := p.partitionSource()
	if ok {
		p.c.openNested(src, name)
	}
}

func (p *DiskPage) showFiles() {
	src, name, ok := p.partitionSource()
	if !ok {
		return
	}
	base := int64(0)
	if pt := p.selected(); pt != nil {
		base = pt.Start
	}
	fsys, err := fs.Open(src, src.Size())
	if err != nil {
		ui.ShowMessageBox(p.c, T().Error, T().NoFileSystem(name))
		return
	}
	p.c.files.show(p.c.tab(), fsys, base, name)
	p.c.side.show(p.c.pages.files)
}

// bootCode disassembles the boot code of the first sector of the disk or
// of the partition (BOOT-4): x86 real mode at 0x7C00
func (p *DiskPage) bootCode() {
	base := int64(0)
	if pt := p.selected(); pt != nil {
		base = pt.Start
	}
	p.c.disasm.showBootSector(base)
	p.c.side.show(p.c.pages.disasm)
}

// ---- Files of a file system (BOOT-5) ----

// FilesPage shows the tree of a file system of a partition: a file is
// extracted, opened as a document, or its bytes shown in the hex view
type FilesPage struct {
	ui.Widget

	c       *MainForm
	lblInfo *ui.Label
	tree    *ui.TreeView
	fsys    fs.FS
	base    int64 // where the volume is in the document
	tab     *Tab
}

func newFilesPage(c *MainForm) *FilesPage {
	var p FilesPage
	p.InitWidget()
	p.SetPanelPadding(0)
	p.c = c
	t := T()
	p.lblInfo = ui.NewLabel(t.NoFileSystemShown)
	p.AddWidget(0, 0, p.lblInfo)
	p.tree = ui.NewTreeView()
	p.tree.SetColumnCount(3)
	p.tree.SetColumnName(0, t.ColName)
	p.tree.SetColumnName(1, t.ColSize)
	p.tree.SetColumnName(2, t.ColModified)
	p.tree.SetColumnWidth(0, 220)
	p.tree.SetColumnWidth(1, 90)
	p.tree.SetStretchLastColumn(true)
	p.tree.SetXExpandable(true)
	p.tree.SetYExpandable(true)
	p.tree.SetOnExpand(p.expand)
	p.AddWidget(1, 0, p.tree)
	buttons := ui.NewPanel()
	buttons.SetPanelPadding(0)
	add := func(col int, text string, f func()) {
		b := ui.NewButton(text)
		b.SetOnClick(f)
		buttons.AddWidget(0, col, b)
	}
	add(0, t.GoToBytes, p.goTo)
	add(1, t.Extract, p.extract)
	add(2, t.OpenAsDocument, p.openFile)
	buttons.AddWidget(0, 3, ui.NewHSpacer())
	p.AddWidget(2, 0, buttons)
	return &p
}

func (p *FilesPage) refresh() {}

// show shows the file system of a volume at base in the document of the tab
func (p *FilesPage) show(t *Tab, fsys fs.FS, base int64, name string) {
	p.tab, p.fsys, p.base = t, fsys, base
	label := fsys.Type()
	if l := fsys.Label(); l != "" {
		label += " \"" + l + "\""
	}
	p.lblInfo.SetText(name + ": " + label)
	p.tree.Clear()
	p.addDir(nil, "/")
}

func (p *FilesPage) addDir(parent *ui.TreeNode, dir string) {
	entries, err := p.fsys.ReadDir(dir)
	if err != nil {
		// A damaged directory: the problem, then the entries read before it
		row := p.addRow(parent, T().Error+": "+err.Error())
		row.SetData(nil)
	}
	// Directories first, then by name
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].IsDir != entries[j].IsDir {
			return entries[i].IsDir
		}
		return strings.ToLower(entries[i].Name) < strings.ToLower(entries[j].Name)
	})
	for _, e := range entries {
		if e.Name == "." || e.Name == ".." {
			continue
		}
		row := p.addRow(parent, e.Name)
		e := e
		row.SetData(&e)
		if !e.IsDir {
			row.SetText(1, formatSize(e.Size))
		}
		if !e.ModTime.IsZero() {
			row.SetText(2, e.ModTime.Format("2006-01-02 15:04"))
		}
		if e.IsDir {
			row.SetHasChildren(true)
		}
	}
}

func (p *FilesPage) addRow(parent *ui.TreeNode, text string) *ui.TreeNode {
	if parent == nil {
		return p.tree.AddNode(nil, text)
	}
	return parent.AddChild(text)
}

func (p *FilesPage) expand(tn *ui.TreeNode) {
	e, _ := tn.Data().(*fs.Entry)
	if e == nil || tn.ChildCount() > 0 {
		return
	}
	p.addDir(tn, e.Path)
}

// current returns the file of the current row
func (p *FilesPage) current() *fs.Entry {
	tn := p.tree.CurrentNode()
	if tn == nil || p.fsys == nil {
		return nil
	}
	e, _ := tn.Data().(*fs.Entry)
	return e
}

// goTo selects the first run of the file's bytes and marks all of them
func (p *FilesPage) goTo() {
	e := p.current()
	if e == nil || p.tab == nil {
		return
	}
	exts, err := p.fsys.Extents(e.Path)
	if err != nil || len(exts) == 0 {
		ui.ShowToast(p.c, T().NoExtents, ui.ToastInfo)
		return
	}
	var marks []hexview.Highlight
	for _, x := range exts {
		if x.DiskOff >= 0 {
			marks = append(marks, hexview.Highlight{Off: p.base + x.DiskOff, Len: x.Len, Color: colorFound.get()})
		}
	}
	sort.Slice(marks, func(i, j int) bool { return marks[i].Off < marks[j].Off })
	p.tab.marks = markProvider(marks)
	if i := p.c.indexOf(p.tab); i >= 0 {
		p.c.selectTab(i)
	}
	for _, x := range exts {
		if x.DiskOff >= 0 {
			p.tab.jump(p.base + x.DiskOff)
			p.tab.view().Select(p.base+x.DiskOff, x.Len)
			break
		}
	}
	ui.ShowToast(p.c, T().ExtentsCount(len(exts)), ui.ToastInfo)
}

// extract saves the file (or the directory, recursively) to the disk of the computer
func (p *FilesPage) extract() {
	e := p.current()
	if e == nil {
		return
	}
	fsys := p.fsys
	if e.IsDir {
		p.c.Form().ShowSelectDirectoryDialog(ui.SelectDirectoryDialogOptions{Title: T().Extract}, func(dir string, err error) {
			if err != nil || dir == "" {
				return
			}
			n, err := extractDir(fsys, e.Path, filepath.Join(dir, e.Name))
			if err != nil {
				ui.ShowMessageBox(p.c, T().Error, err.Error())
			}
			ui.ShowToast(p.c, T().Extracted(n), ui.ToastSuccess)
		})
		return
	}
	p.c.Form().ShowSaveFileDialog(ui.SaveFileDialogOptions{Title: T().Extract, DefaultFileName: e.Name}, func(out string, err error) {
		if err != nil || out == "" {
			return
		}
		if err := extractFile(fsys, e.Path, out); err != nil {
			ui.ShowMessageBox(p.c, T().Error, err.Error())
			return
		}
		ui.ShowToast(p.c, T().Extracted(1), ui.ToastSuccess)
	})
}

func extractFile(fsys fs.FS, name, out string) error {
	r, size, err := fsys.Open(name)
	if err != nil {
		return err
	}
	f, err := os.Create(out)
	if err != nil {
		return err
	}
	_, err = io.Copy(f, io.NewSectionReader(r, 0, size))
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	return err
}

// extractDir saves a directory with all it has; returns how many files
func extractDir(fsys fs.FS, dir, out string) (int, error) {
	if err := os.MkdirAll(out, 0755); err != nil {
		return 0, err
	}
	entries, err := fsys.ReadDir(dir)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, e := range entries {
		if e.Name == "." || e.Name == ".." || strings.ContainsAny(e.Name, `/\`) {
			continue
		}
		target := filepath.Join(out, e.Name)
		if e.IsDir {
			k, err := extractDir(fsys, path.Join(dir, e.Name), target)
			n += k
			if err != nil {
				return n, err
			}
			continue
		}
		if err := extractFile(fsys, path.Join(dir, e.Name), target); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

func (p *FilesPage) openFile() {
	e := p.current()
	if e == nil || e.IsDir {
		return
	}
	r, size, err := p.fsys.Open(e.Path)
	if err != nil {
		ui.ShowMessageBox(p.c, T().Error, err.Error())
		return
	}
	p.c.openNested(source.NewSection(r, 0, size), e.Name)
}

// ---- Boot images (BOOT-7) ----

// BootPage shows the parts of a boot image: the kernel, the ramdisk, the
// device tree; each is gone to, extracted or opened as a document
type BootPage struct {
	ui.Widget

	c       *MainForm
	lblInfo *ui.Label
	info    *ui.Table
	parts   *ui.Table
	img     *bootimg.Image
	tab     *Tab
}

func newBootPage(c *MainForm) *BootPage {
	var p BootPage
	p.InitWidget()
	p.SetPanelPadding(0)
	p.c = c
	t := T()
	p.lblInfo = ui.NewLabel("")
	p.AddWidget(0, 0, p.lblInfo)
	p.info = ui.NewTable()
	p.info.SetColumnCount(2)
	p.info.SetColumnName(0, t.ColName)
	p.info.SetColumnName(1, t.ColValue)
	p.info.SetColumnWidth(0, 140)
	p.info.SetStretchLastColumn(true)
	p.info.SetXExpandable(true)
	p.info.SetMinHeight(140)
	p.AddWidget(1, 0, p.info)
	p.parts = ui.NewTable()
	p.parts.SetColumnCount(4)
	for i, n := range []string{t.ColName, t.ColOffset, t.ColSize, t.ColCompression} {
		p.parts.SetColumnName(i, n)
	}
	p.parts.SetColumnWidth(0, 180)
	p.parts.SetColumnWidth(1, 90)
	p.parts.SetColumnWidth(2, 90)
	p.parts.SetStretchLastColumn(true)
	p.parts.SetXExpandable(true)
	p.parts.SetYExpandable(true)
	p.parts.SetOnCellMouseDblClick(p.goTo)
	p.AddWidget(2, 0, p.parts)
	buttons := ui.NewPanel()
	buttons.SetPanelPadding(0)
	add := func(col int, text string, f func()) {
		b := ui.NewButton(text)
		b.SetOnClick(f)
		buttons.AddWidget(0, col, b)
	}
	add(0, t.GoToBytes, p.goTo)
	add(1, t.Extract, p.extract)
	add(2, t.OpenAsDocument, p.open)
	add(3, t.ExtractDecompressed, p.extractDecompressed)
	add(4, t.OpenDecompressed, p.openDecompressed)
	add(5, t.Refresh, func() { p.tab = nil; p.refresh() })
	buttons.AddWidget(0, 6, ui.NewHSpacer())
	p.AddWidget(3, 0, buttons)
	return &p
}

func (p *BootPage) refresh() {
	t := p.c.tab()
	if t == p.tab {
		return
	}
	p.tab = t
	p.img = nil
	if t != nil {
		snap := t.doc.Snapshot()
		if img, err := bootimg.Detect(snap, snap.Size()); err == nil {
			p.img = img
		} else if found := bootimg.FindInPartitions(snap, snap.Size()); len(found) > 0 {
			// Not at the start: in a partition of a disk
			p.img = found[0].Image
		}
	}
	if p.img == nil {
		p.lblInfo.SetText(T().NoBootImage)
		p.info.SetRowCount(0)
		p.parts.SetRowCount(0)
		return
	}
	p.lblInfo.SetText(p.img.Format)
	p.info.SetRowCount(len(p.img.Info))
	for i, kv := range p.img.Info {
		p.info.SetCellText2(i, 0, kv.Key)
		p.info.SetCellText2(i, 1, kv.Value)
	}
	p.parts.SetRowCount(len(p.img.Parts))
	for i, pt := range p.img.Parts {
		p.parts.SetCellText2(i, 0, pt.Name)
		p.parts.SetCellText2(i, 1, fmt.Sprintf("%X", pt.Off))
		p.parts.SetCellText2(i, 2, formatSize(pt.Size))
		p.parts.SetCellText2(i, 3, pt.Compression)
	}
}

func (p *BootPage) selected() *bootimg.Part {
	rows := p.parts.SelectedRows()
	if p.img == nil || len(rows) == 0 || rows[0] >= len(p.img.Parts) {
		return nil
	}
	return &p.img.Parts[rows[0]]
}

func (p *BootPage) goTo() {
	if pt := p.selected(); pt != nil && p.tab != nil {
		p.tab.jump(pt.Off)
		p.tab.view().Select(pt.Off, pt.Size)
	}
}

func (p *BootPage) extract() {
	pt := p.selected()
	if pt == nil || p.tab == nil {
		return
	}
	snap := p.tab.doc.Snapshot()
	name := strings.NewReplacer("/", "_", ":", "_", " ", "_").Replace(pt.Name)
	p.c.Form().ShowSaveFileDialog(ui.SaveFileDialogOptions{Title: T().Extract, DefaultFileName: name}, func(out string, err error) {
		if err != nil || out == "" {
			return
		}
		f, err := os.Create(out)
		if err == nil {
			_, err = io.Copy(f, io.NewSectionReader(snap, pt.Off, pt.Size))
			if closeErr := f.Close(); err == nil {
				err = closeErr
			}
		}
		if err != nil {
			ui.ShowMessageBox(p.c, T().Error, err.Error())
			return
		}
		ui.ShowToast(p.c, T().Extracted(1), ui.ToastSuccess)
	})
}

// openDecompressedLimit: a part opened decompressed is kept in memory
const openDecompressedLimit = 1 << 30

// extractDecompressed saves a part decompressed (as it is when it is not
// compressed)
func (p *BootPage) extractDecompressed() {
	pt := p.selected()
	if pt == nil || p.tab == nil {
		return
	}
	snap := p.tab.doc.Snapshot()
	part := *pt
	name := strings.NewReplacer("/", "_", ":", "_", " ", "_").Replace(part.Name)
	p.c.Form().ShowSaveFileDialog(ui.SaveFileDialogOptions{Title: T().ExtractDecompressed, DefaultFileName: name}, func(out string, err error) {
		if err != nil || out == "" {
			return
		}
		r, err := bootimg.OpenPart(snap, part, bootimg.MaxDecompressed)
		if err == nil {
			var f *os.File
			if f, err = os.Create(out); err == nil {
				_, err = io.Copy(f, r)
				if closeErr := f.Close(); err == nil {
					err = closeErr
				}
				if err != nil {
					os.Remove(out)
				}
			}
			r.Close()
		}
		if err != nil {
			ui.ShowMessageBox(p.c, T().Error, err.Error())
			return
		}
		ui.ShowToast(p.c, T().Extracted(1), ui.ToastSuccess)
	})
}

// openDecompressed opens a part decompressed as a document: the cpio of a
// compressed ramdisk shows its files on this page there
func (p *BootPage) openDecompressed() {
	pt := p.selected()
	if pt == nil || p.tab == nil {
		return
	}
	if pt.Compression == "" {
		p.open()
		return
	}
	b, err := bootimg.DecompressPart(p.tab.doc.Snapshot(), *pt, openDecompressedLimit)
	if err != nil {
		ui.ShowMessageBox(p.c, T().Error, err.Error())
		return
	}
	p.c.openNested(source.NewMemory(b), p.tab.name()+" ["+pt.Name+", "+pt.Compression+"]")
}

func (p *BootPage) open() {
	if pt := p.selected(); pt != nil && p.tab != nil {
		p.c.openNested(source.NewSection(p.tab.doc.Snapshot(), pt.Off, pt.Size), p.tab.name()+" ["+pt.Name+"]")
	}
}

// ---- Disassembly (ANL-9, BOOT-4) ----

// DisasmPage lists the instructions of the code at the caret (or at an
// offset given) for an architecture; a double click goes to the bytes of
// an instruction, or to where a jump goes
type DisasmPage struct {
	ui.Widget

	c       *MainForm
	cmbArch *ui.ComboBox
	txtBase *ui.TextBox
	table   *ui.Table
	insts   []disasm.Inst
	off     int64  // where the listing starts in the document
	base    uint64 // the address of the byte at off
}

const disasmCount = 400

func newDisasmPage(c *MainForm) *DisasmPage {
	var p DisasmPage
	p.InitWidget()
	p.SetPanelPadding(0)
	p.c = c
	t := T()
	top := ui.NewPanel()
	top.SetPanelPadding(0)
	p.cmbArch = ui.NewComboBox()
	for _, a := range disasm.Archs() {
		p.cmbArch.AddItem(a.Name(), a)
	}
	p.cmbArch.SetSelectedIndex(0)
	p.cmbArch.SetXExpandable(true)
	top.AddWidget(0, 0, p.cmbArch)
	top.AddWidget(0, 1, ui.NewLabel(t.LoadAddress))
	p.txtBase = ui.NewTextBox()
	p.txtBase.SetText("0x0")
	p.txtBase.SetMinWidth(90)
	top.AddWidget(0, 2, p.txtBase)
	run := ui.NewButton(t.FromCaret)
	run.SetOnClick(p.fromCaret)
	top.AddWidget(0, 3, run)
	p.AddWidget(0, 0, top)
	p.table = ui.NewTable()
	p.table.SetColumnCount(4)
	for i, n := range []string{t.ColAddress, t.ColBytes, t.ColInstruction, t.ColComment} {
		p.table.SetColumnName(i, n)
	}
	p.table.SetColumnWidth(0, 90)
	p.table.SetColumnWidth(1, 130)
	p.table.SetColumnWidth(2, 200)
	p.table.SetStretchLastColumn(true)
	p.table.SetXExpandable(true)
	p.table.SetYExpandable(true)
	p.table.SetOnCellMouseDblClick(p.activate)
	p.AddWidget(1, 0, p.table)
	return &p
}

func (p *DisasmPage) refresh() {}

// fromCaret lists from the caret, the address of the caret counted from the
// load address of the start of the document
func (p *DisasmPage) fromCaret() {
	a, _ := p.cmbArch.SelectedItemData().(disasm.Arch)
	t := p.c.tab()
	if a == nil || t == nil {
		return
	}
	base, ok := ParseOffset(p.txtBase.Text())
	if !ok {
		ui.ShowToast(p.c, T().BytesBad, ui.ToastError)
		return
	}
	caret := t.view().Caret()
	p.list(a, caret, uint64(base)+uint64(caret), disasmCount)
}

// show lists n bytes of code at off of the document, loaded at the address
func (p *DisasmPage) show(a disasm.Arch, off int64, addr uint64, n int64) {
	if a == nil {
		return
	}
	for i := 0; i < p.cmbArch.ItemCount(); i++ {
		p.cmbArch.SetSelectedIndex(i)
		if x, _ := p.cmbArch.SelectedItemData().(disasm.Arch); x == a {
			break
		}
	}
	p.txtBase.SetText(fmt.Sprintf("0x%X", int64(addr)-off))
	p.list(a, off, addr, int(n))
}

func (p *DisasmPage) list(a disasm.Arch, off int64, addr uint64, count int) {
	t := p.c.tab()
	if t == nil {
		return
	}
	p.off, p.base = off, addr
	snap := t.doc.Snapshot()
	insts := disasm.Linear(a, snap, off, addr, count)
	// Not past a given length: the boot code is one sector
	if count <= 512 {
		var cut []disasm.Inst
		for _, in := range insts {
			if int(in.Addr-addr)+in.Len > count {
				break
			}
			cut = append(cut, in)
		}
		insts = cut
	}
	p.render(insts)
}

// showBootSector lists the boot sector at off: the code at 0x7C00, the
// BPB a VBR jumps over and the partition table of an MBR as data (BOOT-4)
func (p *DisasmPage) showBootSector(off int64) {
	t := p.c.tab()
	a := disasm.ByID("x86-16")
	if t == nil || a == nil {
		return
	}
	for i := 0; i < p.cmbArch.ItemCount(); i++ {
		p.cmbArch.SetSelectedIndex(i)
		if x, _ := p.cmbArch.SelectedItemData().(disasm.Arch); x == a {
			break
		}
	}
	p.txtBase.SetText(fmt.Sprintf("0x%X", disasm.BootSectorAddr-off))
	p.off, p.base = off, disasm.BootSectorAddr
	p.render(disasm.BootSector(t.doc.Snapshot(), off))
}

func (p *DisasmPage) render(insts []disasm.Inst) {
	p.insts = insts
	p.table.SetRowCount(len(insts))
	for i, in := range insts {
		p.table.SetCellText2(i, 0, fmt.Sprintf("%X", in.Addr))
		p.table.SetCellText2(i, 1, disasm.FormatBytes(in.Bytes))
		p.table.SetCellText2(i, 2, in.Text())
		comment := in.Comment
		if in.HasTarget {
			if comment != "" {
				comment += "; "
			}
			comment += fmt.Sprintf("-> %X", in.Target)
		}
		p.table.SetCellText2(i, 3, comment)
	}
}

// activate goes to the target of a jump (when it is in the document), or
// to the bytes of the instruction
func (p *DisasmPage) activate() {
	rows := p.table.SelectedRows()
	t := p.c.tab()
	if len(rows) == 0 || rows[0] >= len(p.insts) || t == nil {
		return
	}
	in := p.insts[rows[0]]
	if in.HasTarget && in.Target >= p.base-uint64(p.off) {
		target := int64(in.Target-p.base) + p.off
		if target >= 0 && target < t.doc.Size() {
			t.jump(target)
			return
		}
	}
	at := int64(in.Addr-p.base) + p.off
	t.jump(at)
	t.view().Select(at, int64(in.Len))
}
