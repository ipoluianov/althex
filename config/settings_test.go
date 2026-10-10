package config

import (
	"encoding/json"
	"testing"
)

// Settings written before the format had a version are read with their
// values and get the current version (6.4)
func TestMigrateOld(t *testing.T) {
	old := []byte(`{"Backup": true, "AutoTemplate": false, "View": {"BytesPerRow": 32}, "RecentFiles": ["/a"]}`)
	bs, err := migrate(old)
	if err != nil {
		t.Fatal(err)
	}
	s := defaultSettings()
	if err := json.Unmarshal(bs, &s); err != nil {
		t.Fatal(err)
	}
	if s.Version != SettingsVersion || !s.Backup || s.AutoTemplate || s.View.BytesPerRow != 32 || s.RecentFiles[0] != "/a" {
		t.Errorf("migrated: %+v", s)
	}
}

func TestMigrateNewer(t *testing.T) {
	bs, err := migrate([]byte(`{"Version": 99, "Backup": true, "Future": 1}`))
	if err != nil {
		t.Fatal(err)
	}
	var s Settings
	json.Unmarshal(bs, &s)
	if s.Version != 99 || !s.Backup {
		t.Errorf("newer: %+v", s)
	}
}

func TestMigrateBroken(t *testing.T) {
	if _, err := migrate([]byte(`{not json`)); err == nil {
		t.Error("want an error")
	}
}
