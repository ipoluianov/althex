package main

import (
	"os"

	"github.com/ipoluianov/althex/app"
	"github.com/ipoluianov/althex/config"
	"github.com/ipoluianov/althex/forms"
	"github.com/ipoluianov/althex/install"
	"github.com/ipoluianov/althex/instance"
)

// uninstall removes the installed application, leaving its settings. It has
// no window: the questions are system message boxes. quiet
// removes it without asking or reporting.
func uninstall(quiet bool) {
	// Only the language is needed; nothing is written
	config.LoadSettings()
	forms.SetLanguage(config.GetSettings().Language)
	t := forms.T()

	// The running copy shows its window, so it is clear what to close
	inst, ok := instance.Acquire(config.ConfigDirectory())
	if !ok {
		if !quiet {
			install.Inform(app.DisplayName, t.UninstallRunning)
		}
		os.Exit(1)
	}
	defer inst.Close()

	if !quiet && !install.Confirm(app.DisplayName, t.UninstallAsk(config.ConfigDirectory())) {
		return
	}
	if err := install.Uninstall(); err != nil {
		if !quiet {
			install.Inform(app.DisplayName, t.Error+": "+err.Error())
		}
		inst.Close()
		os.Exit(1)
	}
	if !quiet {
		install.Inform(app.DisplayName, t.Uninstalled)
	}
}
