package config

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// WindowState is the main window layout, restored on the next start
type WindowState struct {
	X, Y          int
	Width, Height int
	Maximized     bool
}

func windowStatePath() string {
	return filepath.Join(ConfigDirectory(), "window.json")
}

// LoadWindowState returns the saved window layout; ok is false when there is none
func LoadWindowState() (state WindowState, ok bool) {
	bs, err := os.ReadFile(windowStatePath())
	if err != nil {
		return state, false
	}
	if json.Unmarshal(bs, &state) != nil || state.Width <= 0 || state.Height <= 0 {
		return WindowState{}, false
	}
	return state, true
}

func SaveWindowState(state WindowState) error {
	bs, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(windowStatePath(), bs)
}
