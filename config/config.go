// Package config keeps the settings and the window layout in ~/.altbins/.althex
package config

import (
	"os"
	"path/filepath"
)

// ConfigDirectory is where the settings are kept
func ConfigDirectory() string {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		homeDir = "."
	}
	return filepath.Join(homeDir, ".altbins", ".althex")
}

// Init reads the settings; call it once at start
func Init() {
	LoadSettings()
}

// writeFileAtomic writes to a temporary file and renames it over the target,
// so a crash or a power loss leaves either the old file or the new one, never a torn one
func writeFileAtomic(fullPath string, bs []byte) error {
	if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
		return err
	}
	tmpPath := fullPath + ".tmp"
	f, err := os.Create(tmpPath)
	if err != nil {
		return err
	}
	_, err = f.Write(bs)
	if err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(tmpPath, fullPath)
	}
	if err != nil {
		os.Remove(tmpPath)
	}
	return err
}
