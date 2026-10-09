// Package install puts the application into ~/.altbins and registers it in
// the system, so a downloaded copy can install itself with one click and be
// removed the usual way. Only Windows is supported: Linux has its installer
// script and packages, macOS its .dmg.
package install

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// Status is what the running copy can do about the installation
type Status int

const (
	// StatusNone: nothing, installation is not supported here
	StatusNone Status = iota
	// StatusInstall: the application is not installed
	StatusInstall
	// StatusUpdate: an older version is installed
	StatusUpdate
	// StatusUninstall: this version or a newer one is installed, or this is the installed copy
	StatusUninstall
)

const (
	// appName is the file name of the installed binary
	appName = "althex"

	// UninstallArg starts the application as its own uninstaller
	UninstallArg = "--uninstall"
	// QuietArg removes it without asking (with UninstallArg)
	QuietArg = "--quiet"
	// InstalledArg tells the installed copy that it has just been installed
	InstalledArg = "--installed"
)

// iconPNG is the application icon, set by SetIcon
var iconPNG []byte

// relaunch is set once the application is installed: the installed copy is
// started when this one quits
var relaunch bool

// SetIcon sets the icon of the shortcuts and of the entry in the list of apps
func SetIcon(png []byte) {
	iconPNG = png
}

// HasArg tells whether the application was started with the argument
func HasArg(arg string) bool {
	return slices.Contains(os.Args[1:], arg)
}

// Dir is where the application is installed, shared by all altbins utilities
func Dir() string {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		homeDir = "."
	}
	return filepath.Join(homeDir, ".altbins")
}

// RelaunchAfterExit asks to start the installed copy when this one quits
func RelaunchAfterExit() {
	relaunch = true
}

// RelaunchPending tells whether the installed copy must be started on exit
func RelaunchPending() bool {
	return relaunch
}

// versionNewer tells whether version a is newer than b. Versions come from
// git describe: v1.2.3, or v1.2.3-4-gabc1234 four commits after the tag. Ones
// not of this form (dev, a bare hash) cannot be ordered, so any difference
// counts as newer: installing such a copy is the user's choice.
func versionNewer(a string, b string) bool {
	a, b = strings.TrimPrefix(a, "v"), strings.TrimPrefix(b, "v")
	if a == b {
		return false
	}
	va, okA := parseVersion(a)
	vb, okB := parseVersion(b)
	if !okA || !okB {
		return true
	}
	return slices.Compare(va, vb) > 0
}

// parseVersion turns 1.2.3-4-gabc1234 into [1 2 3 4]: the tag, padded to
// three numbers, and the commits after it
func parseVersion(v string) ([]int, bool) {
	tag, rest, _ := strings.Cut(v, "-")
	parts := strings.Split(tag, ".")
	if len(parts) > 3 {
		return nil, false
	}
	nums := make([]int, 4)
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return nil, false
		}
		nums[i] = n
	}
	if commits, _, ok := strings.Cut(rest, "-"); ok {
		if n, err := strconv.Atoi(commits); err == nil {
			nums[3] = n
		}
	}
	return nums, true
}
