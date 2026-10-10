package forms

import (
	"path/filepath"

	"github.com/ipoluianov/althex/config"
	"github.com/ipoluianov/nui/ui"
)

// welcome is what the window shows with no document open: the hint, the
// buttons to open a file or a disk and the files opened last
type welcome struct {
	ui.Widget
	c      *MainForm
	recent []*ui.Label // the slots of the recent files, filled by refresh
	title  *ui.Label
}

func newWelcome(c *MainForm) *welcome {
	w := &welcome{c: c}
	w.InitWidget()
	w.SetXExpandable(true)
	w.SetYExpandable(true)
	w.AddWidget(0, 0, ui.NewVSpacer())

	c.lblHint = ui.NewLabel("")
	c.lblHint.SetTextFunc(func() string { return T().DropHint })
	c.lblHint.SetTextAlign(ui.HAlignCenter)
	c.lblHint.SetForegroundColor(colorSecondaryText.get())
	c.lblHint.SetXExpandable(true)
	w.AddWidget(1, 0, c.lblHint)

	buttons := ui.NewPanel()
	buttons.AddWidget(0, 0, ui.NewHSpacer())
	open := ui.NewButton("")
	open.SetTextFunc(func() string { return T().MenuOpen })
	open.SetOnClick(c.openDialog)
	buttons.AddWidget(0, 1, open)
	disk := ui.NewButton("")
	disk.SetTextFunc(func() string { return T().MenuOpenDisk })
	disk.SetOnClick(func() { c.runCommand("file.openDisk") })
	buttons.AddWidget(0, 2, disk)
	buttons.AddWidget(0, 3, ui.NewHSpacer())
	w.AddWidget(2, 0, buttons)

	list := ui.NewPanel()
	w.title = ui.NewLabel("")
	w.title.SetTextFunc(func() string { return T().MenuRecent })
	w.title.SetForegroundColor(colorSecondaryText.get())
	list.AddWidget(0, 1, w.title)
	list.AddWidget(0, 0, ui.NewHSpacer())
	list.AddWidget(0, 2, ui.NewHSpacer())
	for i := 0; i < config.MaxRecentFiles; i++ {
		i := i
		lbl := newLinkLabel(func() string { return "" }, func() { w.openRecent(i) })
		lbl.SetTextFunc(nil)
		w.recent = append(w.recent, lbl)
		list.AddWidget(1+i, 1, lbl)
	}
	w.AddWidget(3, 0, list)
	w.AddWidget(4, 0, ui.NewVSpacer())
	return w
}

// refresh shows the files opened last
func (w *welcome) refresh() {
	files := config.GetSettings().RecentFiles
	w.title.SetVisible(len(files) > 0)
	for i, lbl := range w.recent {
		if i < len(files) {
			lbl.SetText(filepath.Base(files[i]) + "   " + filepath.Dir(files[i]))
			lbl.SetVisible(true)
		} else {
			lbl.SetVisible(false)
		}
	}
}

func (w *welcome) openRecent(i int) {
	if files := config.GetSettings().RecentFiles; i < len(files) {
		w.c.OpenFile(files[i])
	}
}

// showWelcome shows the welcome in the place of the documents
func (c *MainForm) showWelcome() {
	c.welcome.refresh()
	c.area.RemoveAllWidgets()
	c.area.AddWidget(0, 0, c.welcome)
}
