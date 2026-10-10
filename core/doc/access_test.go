package doc

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// A file without the permission to write it opens read-only (SRC-2)
func TestOpenNotWritableIsReadOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the read-only attribute is not a permission on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root writes any file")
	}
	path := writeFile(t, t.TempDir(), []byte("0123456789"))
	if err := os.Chmod(path, 0444); err != nil {
		t.Fatal(err)
	}
	d, err := Open(path, Options{JournalDir: filepath.Join(t.TempDir(), "j")})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if !d.ReadOnly() || d.WriteDenied() == nil {
		t.Fatalf("read-only %v, denied %v", d.ReadOnly(), d.WriteDenied())
	}
	if err := d.Overwrite(0, []byte("x"), EditOptions{}); err != ErrReadOnly {
		t.Fatalf("edited: %v", err)
	}
	if string(readAll(t, d)) != "0123456789" {
		t.Fatal("the bytes are not read")
	}

	// Writable: opened for editing; asked read-only: not denied
	os.Chmod(path, 0644)
	w, err := Open(path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if w.ReadOnly() || w.WriteDenied() != nil {
		t.Fatalf("writable: read-only %v, denied %v", w.ReadOnly(), w.WriteDenied())
	}
	r, err := Open(path, Options{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if !r.ReadOnly() || r.WriteDenied() != nil {
		t.Fatalf("asked read-only: %v, denied %v", r.ReadOnly(), r.WriteDenied())
	}
}

// The file changed on the disk is told about, the edits not saved stay (SRC-7)
func TestFileChangedKeepsEdits(t *testing.T) {
	path := writeFile(t, t.TempDir(), []byte("0123456789"))
	d, err := Open(path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	d.Overwrite(1, []byte("ab"), EditOptions{})
	if ch, err := d.FileChanged(); ch || err != nil {
		t.Fatalf("edits are a change of the file: %v %v", ch, err)
	}
	if err := os.WriteFile(path, []byte("changed by another program"), 0644); err != nil {
		t.Fatal(err)
	}
	if ch, err := d.FileChanged(); !ch || err != nil {
		t.Fatalf("changed: %v %v", ch, err)
	}
	if !d.Modified() {
		t.Fatal("the edits are lost")
	}
	// Reopened, the file is the new one
	d2, err := Open(path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer d2.Close()
	if ch, _ := d2.FileChanged(); ch || d2.Modified() {
		t.Fatal("a reopened file is changed or modified")
	}
}

// Writes tells what saving writes: the changed ranges, or the whole file
func TestWrites(t *testing.T) {
	path := writeFile(t, t.TempDir(), []byte("0123456789abcdef"))
	d, err := Open(path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if rs, rewrite := d.Writes(); len(rs) != 0 || rewrite {
		t.Fatalf("nothing edited: %v %v", rs, rewrite)
	}
	d.Overwrite(2, []byte("xy"), EditOptions{})
	d.Overwrite(10, []byte("z"), EditOptions{})
	rs, rewrite := d.Writes()
	if rewrite || len(rs) != 2 || rs[0] != (Range{2, 2}) || rs[1] != (Range{10, 1}) {
		t.Fatalf("overwritten: %v %v", rs, rewrite)
	}
	d.Insert(4, []byte("i"), EditOptions{})
	if _, rewrite := d.Writes(); !rewrite {
		t.Fatal("an insert is written in place")
	}
	n, _ := New(4, Options{})
	defer n.Close()
	if _, rewrite := n.Writes(); !rewrite {
		t.Fatal("a new document is written in place")
	}
}
