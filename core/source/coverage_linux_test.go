//go:build linux

package source

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLinuxSysHelpers(t *testing.T) {
	if isDevicePath("/dev/sda") || isDevicePath("x") {
		t.Fatal("Linux devices are told by their mode, not their name")
	}
	dir := t.TempDir()
	if readSys(filepath.Join(dir, "missing")) != "" {
		t.Fatal("a missing file reads")
	}
	if sysSize(dir) != 0 {
		t.Fatal("a missing size")
	}
	for in, want := range map[string]int64{"8\n": 4096, " 3 ": 1536, "0\n": 0, "junk": 0, "": 0} {
		if err := os.WriteFile(filepath.Join(dir, "size"), []byte(in), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := sysSize(dir); got != want {
			t.Errorf("size %q: %d, want %d", in, got, want)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "model"), []byte("Disk X  \n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if readSys(filepath.Join(dir, "model")) != "Disk X  \n" {
		t.Fatal("readSys changes the content")
	}
}

func TestLinuxDeviceSize(t *testing.T) {
	f, err := os.Open(filepath.Join(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	if _, err := deviceSize(f); err == nil {
		t.Fatal("size of a closed file")
	}
}
