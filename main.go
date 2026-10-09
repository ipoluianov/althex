package main

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/ipoluianov/althex/app"
	"github.com/ipoluianov/althex/config"
	"github.com/ipoluianov/althex/forms"
	"github.com/ipoluianov/althex/install"
	"github.com/ipoluianov/althex/instance"
	"github.com/ipoluianov/nui/ui"
)

// fileArgs returns the files of the command line as absolute paths
func fileArgs(args []string) []string {
	var files []string
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			continue
		}
		if abs, err := filepath.Abs(a); err == nil {
			a = abs
		}
		files = append(files, a)
	}
	return files
}

func main() {
	install.SetIcon(iconPNG)
	// Started from "Installed apps" to remove it
	if install.HasArg(install.UninstallArg) {
		uninstall(install.HasArg(install.QuietArg))
		return
	}

	files := fileArgs(os.Args[1:])
	// Taken before anything is read or written: the files belong to one copy
	inst, ok := instance.Acquire(config.ConfigDirectory(), files...)
	if !ok {
		return // the running copy opens the files instead
	}
	defer inst.Close()

	config.Init()
	forms.SetLanguage(config.GetSettings().Language)
	forms.ApplyTheme(config.GetSettings().Theme)
	ui.SetAppIcon(appIcon())
	form := ui.NewForm()
	mainForm := forms.NewMainForm()
	form.Panel().SetPanelPadding(0)
	form.Panel().AddWidget(0, 0, mainForm)
	form.SetMenuBar(mainForm.BuildMenuBar())
	mainForm.UpdateTitle()
	maximized := mainForm.RestoreWindowState(form)
	form.OnClose = mainForm.RequestClose
	form.SetOnGlobalKeyDown(mainForm.OnGlobalKeyDown)
	form.SetOnLanguageChanged(mainForm.ApplyLanguage)
	form.SetOnFilesDropped(func(files []string, x, y int) { mainForm.OpenFiles(files) })
	form.Show()
	inst.Serve(func(files []string) {
		form.Invoke(func() {
			mainForm.BringToFront()
			mainForm.OpenFiles(files)
		})
	})
	// The form is handled by its own goroutine once shown
	form.Invoke(func() {
		if maximized {
			form.Maximize()
		}
		form.SetAlwaysOnTop(config.GetSettings().AlwaysOnTop)
		// Edits left by a crash first; the files of the command line then
		mainForm.RecoverJournals(func() { mainForm.OpenFiles(files) })
		if install.HasArg(install.InstalledArg) {
			mainForm.ShowInstalled()
		}
	})
	form.Exec()

	// Installed from this copy: the installed one takes over, so the lock goes first
	if install.RelaunchPending() {
		inst.Close()
		if err := install.StartInstalled(); err != nil {
			install.Inform(app.DisplayName, err.Error())
		}
	}
}
