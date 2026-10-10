// Package config keeps the settings and the window layout in ~/.altbins/.althex
package config

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
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

// APIEndpoint is where the window serves the local API (EXT-7): a unix
// socket of the user, a named pipe on Windows. A unix socket path is
// limited to about 100 bytes, so the runtime directory is used when there
// is one (Linux).
func APIEndpoint() (network, address string) {
	if runtime.GOOS == "windows" {
		name := "user"
		if u, err := user.Current(); err == nil {
			name = u.Username
		}
		return "pipe", "althex-api-" + strings.NewReplacer(`\`, "-", "/", "-", " ", "-").Replace(name)
	}
	dir := ConfigDirectory()
	if runtimeDir := os.Getenv("XDG_RUNTIME_DIR"); runtimeDir != "" {
		sum := sha256.Sum256([]byte(dir))
		return "unix", filepath.Join(runtimeDir, "althex-api-"+hex.EncodeToString(sum[:8])+".sock")
	}
	// A directory of its own, private: the socket is made before its mode is set
	return "unix", filepath.Join(dir, "run", "api.sock")
}
