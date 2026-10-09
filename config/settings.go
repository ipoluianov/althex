package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// Settings are the application options
type Settings struct {
	// Backup keeps the file as it was in <name>.bak when saving (SAVE-3)
	Backup bool `json:",omitempty"`

	// FontSize of the bytes; 0 - the default
	FontSize float64 `json:",omitempty"`

	// View is how the documents are shown
	View ViewSettings

	// AutoTemplate applies the template of the format of a file opened (ANL-4)
	AutoTemplate bool

	// InspectorBigEndian reads the values of the data interpreter big-endian
	InspectorBigEndian bool `json:",omitempty"`
	// Hashes are the IDs of the algorithms ticked in the hashes panel
	Hashes []string `json:",omitempty"`

	AlwaysOnTop bool `json:",omitempty"`

	// Language of the interface as a tag like "ru"; "" - the system's
	Language string `json:",omitempty"`

	// Color theme: "light"; "" - the dark one
	Theme string `json:",omitempty"`

	// Files opened last, the newest first
	RecentFiles []string `json:",omitempty"`
}

// ViewSettings are the options of the hex view (see hexview.Options)
type ViewSettings struct {
	BytesPerRow  int // 0 - as many as fit
	Group        int
	LittleEndian bool
	Mode         int // hex, binary, octal, decimal
	OffsetBase   int
	Encoding     string // the ID of the encoding of the text column; "" - ASCII
	SyncScroll   bool   // the views of a split scroll together
	// ColorRules paint ranges of byte values: "00-1F=#ff8800; 80-FF=#4488ff"
	ColorRules string `json:",omitempty"`
}

// MaxRecentFiles is how many files the recent list keeps
const MaxRecentFiles = 10

// TemplatesDirectory is where the user's own templates are
func TemplatesDirectory() string {
	return filepath.Join(ConfigDirectory(), "templates")
}

// JournalDirectory is where the journals of the open documents are kept,
// to recover their edits after a crash
func JournalDirectory() string {
	return filepath.Join(ConfigDirectory(), "journal")
}

var (
	settingsMtx sync.Mutex
	settings    = defaultSettings()
)

func defaultSettings() Settings {
	return Settings{
		AutoTemplate: true,
		View:         ViewSettings{BytesPerRow: 16, Group: 1, OffsetBase: 16, SyncScroll: true},
	}
}

func settingsPath() string {
	return filepath.Join(ConfigDirectory(), "settings.json")
}

// LoadSettings reads the settings file; missing values get the defaults
func LoadSettings() {
	s := defaultSettings()
	if bs, err := os.ReadFile(settingsPath()); err == nil {
		_ = json.Unmarshal(bs, &s)
	}
	settingsMtx.Lock()
	settings = s
	settingsMtx.Unlock()
}

// GetSettings returns the current settings; safe to call from any goroutine
func GetSettings() Settings {
	settingsMtx.Lock()
	defer settingsMtx.Unlock()
	return settings
}

// SetSettings applies and saves the settings
func SetSettings(s Settings) error {
	settingsMtx.Lock()
	settings = s
	settingsMtx.Unlock()

	bs, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(settingsPath(), bs)
}

// AddRecentFile puts the file first in the recent list and saves the settings
func AddRecentFile(path string) error {
	s := GetSettings()
	recent := []string{path}
	for _, f := range s.RecentFiles {
		if f != path && len(recent) < MaxRecentFiles {
			recent = append(recent, f)
		}
	}
	s.RecentFiles = recent
	return SetSettings(s)
}
