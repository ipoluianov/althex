package forms

import (
	"fmt"

	"github.com/ipoluianov/althex/app"
	"github.com/ipoluianov/althex/config"
	"github.com/ipoluianov/althex/core/doc"
	"github.com/ipoluianov/althex/forms/hexview"
	"github.com/ipoluianov/nui/ui"
)

// MainForm is the content of the main window: the tabs of the documents,
// the side panels and the status bar
type MainForm struct {
	ui.Widget

	tabBar *TabBar
	// external serves the interfaces for other programs (EXT-6, 7)
	external *external
	area     *ui.Panel // the box of the current tab, or the hint
	lblHint  *ui.Label
	welcome  *welcome
	status   *StatusBar

	side      *SidePanel
	sideSplit *ui.Splitter
	pages     struct {
		inspector, structure, console, bookmarks, hashes, history, search, disk, files, boot, disasm, firmware, functions int
	}
	inspector *InspectorPage
	search    *SearchPage
	structure *StructurePage
	console   *ConsolePage
	files     *FilesPage
	disasm    *DisasmPage
	firmware  *FirmwarePage
	functions *FunctionsPage
	history   *HistoryPage

	tabs []*Tab
	cur  int

	saving bool

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

	c.tabBar = NewTabBar()
	c.tabBar.OnSelect = c.selectTab
	c.tabBar.OnClose = func(i int) { c.closeTab(i, nil) }
	c.tabBar.OnMove = c.moveTab
	c.tabBar.SetVisible(false)
	c.AddWidget(0, 0, c.tabBar)

	c.area = ui.NewPanel()
	c.area.SetPanelPadding(0)
	c.area.SetXExpandable(true)
	c.area.SetYExpandable(true)
	c.side = NewSidePanel()
	c.inspector = newInspectorPage(&c)
	c.pages.inspector = c.side.add(func() string { return T().PageInspector }, c.inspector)
	c.addPages()
	c.side.SetVisible(false)
	c.side.OnClose = func() {
		c.side.SetVisible(false)
		if f := c.Form(); f != nil {
			f.UpdateLayout()
		}
	}
	c.sideSplit = ui.NewHSplitter()
	c.sideSplit.SetWidgets(c.area, c.side)
	c.sideSplit.SetSecondSize(panelWidth)
	c.AddWidget(1, 0, c.sideSplit)

	c.welcome = newWelcome(&c)
	c.showWelcome()

	c.status = NewStatusBar()
	c.AddWidget(2, 0, c.status)
	c.AddTimer(2000, c.checkDocuments)
	c.external = newExternal(&c)
	lastCreatedMainWidget = &c
	return &c
}

// tab returns the current tab; nil - none
func (c *MainForm) tab() *Tab {
	if c.cur < 0 || c.cur >= len(c.tabs) {
		return nil
	}
	return c.tabs[c.cur]
}

// view returns the view with the focus; nil - no document
func (c *MainForm) view() *hexview.View {
	if t := c.tab(); t != nil {
		return t.view()
	}
	return nil
}

// doc returns the current document; nil - none
func (c *MainForm) doc() *doc.Document {
	if t := c.tab(); t != nil {
		return t.doc
	}
	return nil
}

// withView returns a command that runs f on the view, if there is one
func (c *MainForm) withView(f func(v *hexview.View)) func() {
	return func() {
		if v := c.view(); v != nil {
			f(v)
		}
	}
}

// withTab returns a command that runs f on the current tab, if there is one
func (c *MainForm) withTab(f func(t *Tab)) func() {
	return func() {
		if t := c.tab(); t != nil {
			f(t)
			c.updateStatus()
			c.update()
		}
	}
}

func (c *MainForm) update() {
	if f := c.Form(); f != nil {
		f.Update()
	}
}

// registerCommands makes the commands of the menus and the shortcuts
func (c *MainForm) registerCommands() {
	t := T
	c.cmd("file.new", func() string { return t().MenuNew }, "Ctrl+N", c.newDocument)
	c.cmd("file.newSize", func() string { return t().MenuNewSize }, "", c.newDocumentOfSize)
	c.cmd("file.newClipboard", func() string { return t().MenuNewClipboard }, "", c.newDocumentFromClipboard)
	c.cmd("file.open", func() string { return t().MenuOpen }, "Ctrl+O", c.openDialog)
	c.cmd("file.openDisk", func() string { return t().MenuOpenDisk }, "", func() { c.ShowDialog(NewOpenDiskDialog(c.openDisk)) })
	c.cmd("file.save", func() string { return t().MenuSave }, "Ctrl+S", func() { c.save(c.tab(), false, nil) })
	c.cmd("file.saveAs", func() string { return t().MenuSaveAs }, "Ctrl+Shift+S", func() { c.save(c.tab(), true, nil) })
	c.cmd("file.close", func() string { return t().MenuClose }, "Ctrl+W", func() { c.closeTab(c.cur, nil) })
	c.cmd("file.closeAll", func() string { return t().MenuCloseAll }, "Ctrl+Shift+W", func() { c.closeAll(nil) })
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
	insert.Checked = func() bool { return c.view() != nil && c.view().InsertMode() }
	readOnly := c.cmd("edit.readOnly", func() string { return t().MenuReadOnly }, "", func() {
		d := c.doc()
		if d == nil {
			return
		}
		// A disk becomes writable after two questions (6.2)
		if d.ReadOnly() && d.IsDevice() {
			c.confirmDeviceWrite(d, T().DiskWriteEdits, func() { d.SetReadOnly(false) })
			return
		}
		d.SetReadOnly(!d.ReadOnly())
	})
	readOnly.Checked = func() bool { return c.doc() != nil && c.doc().ReadOnly() }

	c.cmd("nav.goto", func() string { return t().MenuGoTo }, "Ctrl+G", c.withTab(func(tb *Tab) {
		c.ShowDialog(NewGoToDialog(tb.view(), tb.jump))
	}))
	c.cmd("nav.back", func() string { return t().MenuBack }, "Alt+Left", c.withTab((*Tab).goBack))
	c.cmd("nav.forward", func() string { return t().MenuForward }, "Alt+Right", c.withTab((*Tab).goForward))
	c.cmd("nav.bookmark", func() string { return t().MenuToggleBookmark }, "Ctrl+B", c.withTab((*Tab).toggleBookmark))
	c.cmd("nav.nextBookmark", func() string { return t().MenuNextBookmark }, "F2", c.withTab(func(tb *Tab) { tb.nextBookmark(false) }))
	c.cmd("nav.prevBookmark", func() string { return t().MenuPrevBookmark }, "Shift+F2", c.withTab(func(tb *Tab) { tb.nextBookmark(true) }))
	c.cmd("nav.exportBookmarks", func() string { return t().MenuExportBookmarks }, "", c.exportBookmarks)
	c.cmd("nav.importBookmarks", func() string { return t().MenuImportBookmarks }, "", c.importBookmarks)
	c.cmd("nav.nextTab", func() string { return t().MenuNextTab }, "Ctrl+Tab", func() { c.cycleTab(1) })
	c.cmd("nav.prevTab", func() string { return t().MenuPrevTab }, "Ctrl+Shift+Tab", func() { c.cycleTab(-1) })

	c.registerViewCommands()
	c.registerEditOps()
	c.registerCompareCommands()

	c.cmd("panel.inspector", func() string { return t().PageInspector }, "F4", func() { c.togglePage(c.pages.inspector) }).
		Checked = func() bool { return c.pageShown(c.pages.inspector) }
	c.registerPageCommands()

	c.cmd("help.palette", func() string { return t().MenuPalette }, "Ctrl+Shift+P", func() {
		c.ShowDialog(NewPaletteDialog(c.commands, func(cmd *Command) { cmd.Run() }))
	})
	c.cmd("options.settings", func() string { return t().MenuSettings }, "", c.ShowSettings)
	c.cmd("options.external", func() string { return t().MenuExternal }, "", c.ShowExternal)
	c.cmd("help.docs", func() string { return t().MenuDocs }, "F1", func() { openDocs(c, "help_f1") })
	c.cmd("help.about", func() string { return t().MenuAbout }, "", func() { c.ShowDialog(NewAboutDialog()) })
}

// BuildMenuBar makes the main menu of the window
func (c *MainForm) BuildMenuBar() *ui.MenuBar {
	c.requireDocument()
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
	c.addItems(file, "file.new")
	c.subMenu(file, func() string { return t().MenuNewMore }, "file.newSize", "file.newClipboard")
	c.addItems(file, "file.open", "file.openDisk")
	c.recentMenu(file)
	c.addItems(file, "file.save", "file.saveAs", "-")
	c.buildImportExportMenu(file)
	c.addItems(file, "-", "file.close", "file.closeAll", "-", "file.exit")

	edit := add(func() string { return t().MenuEdit })
	c.addItems(edit, "edit.undo", "edit.redo", "-", "edit.cut", "edit.copy", "edit.paste", "edit.selectAll")
	c.buildEditOpsMenu(edit)
	c.addItems(edit, "-", "edit.insertMode", "edit.readOnly")

	view := add(func() string { return t().MenuView })
	c.buildViewMenu(view)
	c.subMenu(view, func() string { return t().MenuPanels }, c.pageCommandIDs()...)
	c.addItems(view, "template.apply")

	find := add(func() string { return t().MenuSearch })
	c.addItems(find, c.findCommandIDs()...)

	cmpMenu := add(func() string { return t().MenuCompareMenu })
	c.buildCompareMenu(cmpMenu)

	nav := add(func() string { return t().MenuNavigate })
	c.addItems(nav, "nav.goto", "nav.back", "nav.forward", "-", "nav.bookmark", "nav.nextBookmark", "nav.prevBookmark",
		"nav.exportBookmarks", "nav.importBookmarks", "-", "nav.nextTab", "nav.prevTab")

	options := add(func() string { return t().MenuOptions })
	c.addItems(options, "options.settings", "options.external")

	help := add(func() string { return t().MenuHelp })
	c.addItems(help, "help.palette", "-", "help.docs", "-", "help.about")
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

// UpdateTitle shows the current file in the window title
func (c *MainForm) UpdateTitle() {
	title := app.DisplayName
	if t := c.tab(); t != nil {
		mark := ""
		if t.doc.Modified() {
			mark = "*"
		}
		title = mark + t.name() + " - " + title
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
	c.changeView(func(*config.ViewSettings) {})
}

// ApplyLanguage updates the texts that do not follow the language by themselves
func (c *MainForm) ApplyLanguage() {
	c.side.applyLanguage()
	c.updateTabs()
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

// RequestClose is the OnClose of the window: documents with edits ask first
func (c *MainForm) RequestClose() bool {
	if c.saving {
		return false
	}
	for _, t := range c.tabs {
		if t.doc.Modified() {
			c.exit()
			return false
		}
	}
	c.SaveWindowState()
	for len(c.tabs) > 0 {
		c.dropTab(0)
	}
	return true
}

// exit closes the window after the documents
func (c *MainForm) exit() {
	c.closeAll(func() {
		c.external.stop()
		c.SaveWindowState()
		c.Form().Close()
	})
}
