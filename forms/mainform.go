package forms

import (
	"fmt"

	"github.com/ipoluianov/althex/app"
	"github.com/ipoluianov/althex/config"
	"github.com/ipoluianov/althex/core/doc"
	"github.com/ipoluianov/althex/forms/hexview"
	"github.com/ipoluianov/nui/ui"
)

// MainForm is the content of the main window: the document and the status bar
type MainForm struct {
	ui.Widget

	lblHint *ui.Label
	view    *hexview.View // nil - no document
	status  *StatusBar

	saving   bool
	fileSeen bool // a change of the file on the disk was asked about

	commands   map[string]*Command
	shortcuts  map[Shortcut]*Command
	menuChecks []menuCheck
}

const (
	defaultWindowWidth  = 1100
	defaultWindowHeight = 750
	recentMenuSize      = config.MaxRecentFiles
)

var lastCreatedMainWidget *MainForm

func NewMainForm() *MainForm {
	var c MainForm
	c.InitWidget()
	c.SetPanelPadding(0)
	c.commands = make(map[string]*Command)
	c.shortcuts = make(map[Shortcut]*Command)
	c.registerCommands()

	c.lblHint = ui.NewLabel("")
	c.lblHint.SetTextFunc(func() string { return T().DropHint })
	c.lblHint.SetTextAlign(ui.HAlignCenter)
	c.lblHint.SetForegroundColor(colorSecondaryText.get())
	c.lblHint.SetXExpandable(true)
	c.lblHint.SetYExpandable(true)
	c.AddWidget(0, 0, c.lblHint)
	c.status = NewStatusBar()
	c.AddWidget(1, 0, c.status)
	c.AddTimer(2000, c.checkDocument)
	lastCreatedMainWidget = &c
	return &c
}

// doc returns the open document; nil - none
func (c *MainForm) doc() *doc.Document {
	if c.view == nil {
		return nil
	}
	return c.view.Document()
}

// withView returns a command that runs f on the view, if there is one
func (c *MainForm) withView(f func(v *hexview.View)) func() {
	return func() {
		if c.view != nil {
			f(c.view)
		}
	}
}

// registerCommands makes the commands of the menus and the shortcuts
func (c *MainForm) registerCommands() {
	t := T
	c.cmd("file.open", func() string { return t().MenuOpen }, "Ctrl+O", c.openDialog)
	c.cmd("file.save", func() string { return t().MenuSave }, "Ctrl+S", func() { c.save(false, nil) })
	c.cmd("file.saveAs", func() string { return t().MenuSaveAs }, "Ctrl+Shift+S", func() { c.save(true, nil) })
	c.cmd("file.close", func() string { return t().MenuClose }, "Ctrl+W", func() { c.closeDocument(nil) })
	c.cmd("file.exit", func() string { return t().MenuExit }, "Alt+X", c.exit)

	c.cmd("edit.undo", func() string { return t().MenuUndo }, "Ctrl+Z", c.withView((*hexview.View).Undo))
	c.cmd("edit.redo", func() string { return t().MenuRedo }, "Ctrl+Y | Ctrl+Shift+Z", c.withView((*hexview.View).Redo))
	c.cmd("edit.cut", func() string { return t().MenuCut }, "Ctrl+X", c.withView((*hexview.View).Cut))
	c.cmd("edit.copy", func() string { return t().MenuCopy }, "Ctrl+C", c.withView((*hexview.View).Copy))
	c.cmd("edit.paste", func() string { return t().MenuPaste }, "Ctrl+V", c.withView((*hexview.View).Paste))
	c.cmd("edit.selectAll", func() string { return t().MenuSelectAll }, "Ctrl+A", c.withView((*hexview.View).SelectAll))
	insert := c.cmd("edit.insertMode", func() string { return t().MenuInsertMode }, "", c.withView(func(v *hexview.View) {
		v.SetInsertMode(!v.InsertMode())
	}))
	insert.Checked = func() bool { return c.view != nil && c.view.InsertMode() }
	readOnly := c.cmd("edit.readOnly", func() string { return t().MenuReadOnly }, "", func() {
		if d := c.doc(); d != nil {
			d.SetReadOnly(!d.ReadOnly())
		}
	})
	readOnly.Checked = func() bool { return c.doc() != nil && c.doc().ReadOnly() }

	c.cmd("view.goto", func() string { return t().MenuGoTo }, "Ctrl+G", c.withView(func(v *hexview.View) {
		c.ShowDialog(NewGoToDialog(v.Caret(), v.GoTo))
	}))
	c.cmd("view.zoomIn", func() string { return t().MenuZoomIn }, "Ctrl+=", func() { c.zoom(1) })
	c.cmd("view.zoomOut", func() string { return t().MenuZoomOut }, "Ctrl+-", func() { c.zoom(-1) })
	c.cmd("view.zoomReset", func() string { return t().MenuZoomReset }, "Ctrl+0", func() { c.zoom(0) })

	c.cmd("options.settings", func() string { return t().MenuSettings }, "", c.ShowSettings)
	c.cmd("help.docs", func() string { return t().MenuDocs }, "F1", func() { openDocs(c, "help_f1") })
	c.cmd("help.about", func() string { return t().MenuAbout }, "", func() { c.ShowDialog(NewAboutDialog()) })
}

// BuildMenuBar makes the main menu of the window
func (c *MainForm) BuildMenuBar() *ui.MenuBar {
	t := T
	bar := ui.NewMenuBar()
	add := func(text func() string) *ui.ContextMenu {
		menu := ui.NewContextMenu(c)
		item := bar.AddMenuItem(text(), menu)
		item.SetTextFunc(text)
		menu.SetOnShow(c.updateChecks)
		return menu
	}

	file := add(func() string { return t().MenuFile })
	c.addItems(file, "file.open")
	c.recentMenu(file)
	c.addItems(file, "file.save", "file.saveAs", "file.close", "-", "file.exit")

	edit := add(func() string { return t().MenuEdit })
	c.addItems(edit, "edit.undo", "edit.redo", "-", "edit.cut", "edit.copy", "edit.paste", "edit.selectAll", "-",
		"edit.insertMode", "edit.readOnly")

	view := add(func() string { return t().MenuView })
	c.addItems(view, "view.goto", "-", "view.zoomIn", "view.zoomOut", "view.zoomReset")

	options := add(func() string { return t().MenuOptions })
	c.addItems(options, "options.settings")

	help := add(func() string { return t().MenuHelp })
	c.addItems(help, "help.docs", "-", "help.about")
	return bar
}

// recentMenu adds the submenu of the recent files
func (c *MainForm) recentMenu(file *ui.ContextMenu) {
	sub := ui.NewContextMenu(c)
	items := make([]*ui.ContextMenuItem, recentMenuSize)
	for i := range items {
		items[i] = sub.AddItem("", nil)
		items[i].SetVisible(false)
	}
	sub.SetOnShow(func() {
		recent := config.GetSettings().RecentFiles
		for i, item := range items {
			if i < len(recent) {
				path := recent[i]
				item.SetText(fmt.Sprintf("%d: %s", i+1, path))
				item.OnClick = func() { c.OpenFile(path) }
				item.SetVisible(true)
			} else {
				item.SetVisible(false)
			}
		}
	})
	item := file.AddItemWithSubmenu(T().MenuRecent, sub)
	item.SetTextFunc(func() string { return T().MenuRecent })
}

func (c *MainForm) zoom(step int) {
	if c.view == nil {
		return
	}
	size := c.view.FontSize() + float64(step)
	if step == 0 {
		size = hexview.DefaultFontSize
	}
	c.view.SetFontSize(size)
	s := config.GetSettings()
	s.FontSize = c.view.FontSize()
	config.SetSettings(s)
}

// UpdateTitle shows the open file in the window title
func (c *MainForm) UpdateTitle() {
	title := app.DisplayName
	if d := c.doc(); d != nil {
		mark := ""
		if d.Modified() {
			mark = "*"
		}
		title = mark + c.docName() + " - " + title
	}
	c.Form().SetTitle(title)
}

// ApplySettings saves the settings and applies what they change in the window
func (c *MainForm) ApplySettings(s config.Settings) {
	languageChanged := s.Language != config.GetSettings().Language
	themeChanged := s.Theme != config.GetSettings().Theme
	if err := config.SetSettings(s); err != nil {
		ui.ShowMessageBox(c, T().Error, err.Error())
	}
	if languageChanged {
		SetLanguage(s.Language)
	}
	if themeChanged {
		ApplyTheme(s.Theme)
		c.lblHint.SetForegroundColor(colorSecondaryText.get())
	}
	c.Form().SetAlwaysOnTop(s.AlwaysOnTop)
	c.updateStatus()
}

// ApplyLanguage updates the texts that do not follow the language by themselves
func (c *MainForm) ApplyLanguage() {
	c.UpdateTitle()
	c.updateStatus()
}

// ShowSettings opens the settings dialog
func (c *MainForm) ShowSettings() {
	c.ShowDialog(NewSettingsDialog(config.GetSettings(), c.ApplySettings))
}

// ShowInstalled tells that this copy has just been installed and started in place of the downloaded one
func (c *MainForm) ShowInstalled() {
	ui.ShowToast(c, T().Installed, ui.ToastSuccess)
}

// RestoreWindowState applies the saved window layout before the form is shown.
// Returns whether the window should be maximized once shown.
func (c *MainForm) RestoreWindowState(form *ui.Form) (maximized bool) {
	form.SetSize(defaultWindowWidth, defaultWindowHeight)
	state, ok := config.LoadWindowState()
	if !ok {
		return false
	}
	form.SetSize(state.Width, state.Height)
	// 0,0 means the position was not known (the window manager did not report it)
	if state.X != 0 || state.Y != 0 {
		form.Move(state.X, state.Y)
	}
	return state.Maximized
}

// SaveWindowState remembers the window layout for the next start
func (c *MainForm) SaveWindowState() {
	form := c.Form()
	state, _ := config.LoadWindowState()
	state.Maximized = form.IsMaximized()
	// The size of a maximized window is the screen size: keep the normal one
	if !state.Maximized {
		state.X, state.Y = form.Position()
		state.Width, state.Height = form.Size()
	}
	config.SaveWindowState(state)
}

// BringToFront shows the window on top of the others, wherever it is:
// minimized or under other windows. Called when the application is started again.
func (c *MainForm) BringToFront() {
	raiseWindow(c.Form())
}

// RequestClose is the OnClose of the window: a document with edits asks first
func (c *MainForm) RequestClose() bool {
	if c.saving {
		return false
	}
	if d := c.doc(); d != nil && d.Modified() {
		c.exit()
		return false
	}
	c.SaveWindowState()
	c.dropDocument()
	return true
}

// exit closes the window after the document
func (c *MainForm) exit() {
	c.closeDocument(func() {
		c.SaveWindowState()
		c.Form().Close()
	})
}
