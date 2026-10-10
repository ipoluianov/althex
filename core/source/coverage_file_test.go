package source

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func writeTemp(t *testing.T, data []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "f.bin")
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestOpenFileReads(t *testing.T) {
	data := []byte("0123456789abcdef")
	p := writeTemp(t, data)
	f, err := OpenFile(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if f.Path() != p {
		t.Fatalf("path %q", f.Path())
	}
	if f.Device() {
		t.Fatal("a regular file is a device")
	}
	if f.Size() != int64(len(data)) {
		t.Fatalf("size %d", f.Size())
	}
	if st := f.Stat(); st.Size != int64(len(data)) || st.ModTime.IsZero() {
		t.Fatalf("stat %+v", st)
	}
	if runtime.GOOS != "windows" {
		if st := f.Stat(); st.Ino == 0 {
			t.Fatal("no inode")
		}
	}

	buf := make([]byte, 4)
	n, err := f.ReadAt(buf, 2)
	if err != nil || n != 4 || string(buf) != "2345" {
		t.Fatalf("read %d %v %q", n, err, buf)
	}
	// A read across the end returns the rest with io.EOF
	buf = make([]byte, 10)
	n, err = f.ReadAt(buf, 12)
	if n != 4 || err != io.EOF || string(buf[:n]) != "cdef" {
		t.Fatalf("read at the end: %d %v %q", n, err, buf[:n])
	}
	// At or past the end
	if n, err := f.ReadAt(buf, 16); n != 0 || err != io.EOF {
		t.Fatalf("read at size: %d %v", n, err)
	}
	if n, err := f.ReadAt(buf, 1000); n != 0 || err != io.EOF {
		t.Fatalf("read past size: %d %v", n, err)
	}
	// An empty read within the file
	if n, err := f.ReadAt(nil, 3); n != 0 || err != nil {
		t.Fatalf("empty read: %d %v", n, err)
	}
}

func TestOpenFileEmpty(t *testing.T) {
	f, err := OpenFile(writeTemp(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if f.Size() != 0 {
		t.Fatalf("size %d", f.Size())
	}
	if n, err := f.ReadAt(make([]byte, 1), 0); n != 0 || err != io.EOF {
		t.Fatalf("read of empty: %d %v", n, err)
	}
}

func TestOpenFileErrors(t *testing.T) {
	dir := t.TempDir()
	if _, err := OpenFile(filepath.Join(dir, "missing")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing file: %v", err)
	}
	if _, err := OpenFile(dir); err == nil {
		t.Fatal("a directory opens")
	}
	if _, err := StatFile(filepath.Join(dir, "missing")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("stat of missing file: %v", err)
	}
}

func TestOpenFileUnreadable(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("permissions are not enforced")
	}
	p := writeTemp(t, []byte("secret"))
	if err := os.Chmod(p, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenFile(p); !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("unreadable file: %v", err)
	}
}

func TestOpenFileReadOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip()
	}
	p := writeTemp(t, []byte("ro"))
	if err := os.Chmod(p, 0o444); err != nil {
		t.Fatal(err)
	}
	f, err := OpenFile(p)
	if err != nil {
		t.Fatalf("a read-only file: %v", err)
	}
	defer f.Close()
	buf := make([]byte, 2)
	if _, err := f.ReadAt(buf, 0); err != nil || string(buf) != "ro" {
		t.Fatalf("read %v %q", err, buf)
	}
}

func TestFileClosed(t *testing.T) {
	f, err := OpenFile(writeTemp(t, []byte("abc")))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
	if _, err := f.ReadAt(make([]byte, 1), 0); err != ErrClosed {
		t.Fatalf("read after close: %v", err)
	}
	if err := f.Refresh(); err != ErrClosed {
		t.Fatalf("refresh after close: %v", err)
	}
	if _, err := f.StatNow(); err != ErrClosed {
		t.Fatalf("stat after close: %v", err)
	}
}

func TestFileRefresh(t *testing.T) {
	p := writeTemp(t, []byte("abc"))
	f, err := OpenFile(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	before := f.Stat()

	// Written in place: the same file, larger and newer
	w, err := os.OpenFile(p, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("defgh")); err != nil {
		t.Fatal(err)
	}
	w.Close()
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(p, later, later); err != nil {
		t.Fatal(err)
	}

	// Until refreshed the old size holds
	if f.Size() != 3 {
		t.Fatalf("size before refresh %d", f.Size())
	}
	buf := make([]byte, 8)
	if n, err := f.ReadAt(buf, 0); n != 3 || err != io.EOF {
		t.Fatalf("read before refresh: %d %v", n, err)
	}
	now, err := f.StatNow()
	if err != nil {
		t.Fatal(err)
	}
	if now.Same(before) || now.Size != 8 {
		t.Fatalf("StatNow does not see the change: %+v", now)
	}
	if !f.Stat().Same(before) {
		t.Fatal("Stat changed without refresh")
	}

	if err := f.Refresh(); err != nil {
		t.Fatal(err)
	}
	if f.Size() != 8 || !f.Stat().Same(now) {
		t.Fatalf("after refresh: size %d stat %+v", f.Size(), f.Stat())
	}
	if n, err := f.ReadAt(buf, 0); n != 8 || err != nil || string(buf) != "abcdefgh" {
		t.Fatalf("read after refresh: %d %v %q", n, err, buf)
	}
	// The file by name is the same file
	byName, err := StatFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !byName.Same(f.Stat()) {
		t.Fatalf("StatFile %+v differs from %+v", byName, f.Stat())
	}
}

func TestStatFileReplaced(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("renaming over an open file")
	}
	p := writeTemp(t, []byte("abc"))
	f, err := OpenFile(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	// Saving makes a new file with the same content and time in its place
	tmp := p + ".new"
	if err := os.WriteFile(tmp, []byte("abc"), 0o644); err != nil {
		t.Fatal(err)
	}
	mt := f.Stat().ModTime
	if err := os.Chtimes(tmp, mt, mt); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, p); err != nil {
		t.Fatal(err)
	}
	byName, err := StatFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if byName.Same(f.Stat()) {
		t.Fatal("a replaced file is the same")
	}
	// The opened file is still the old one
	now, err := f.StatNow()
	if err != nil || !now.Same(f.Stat()) {
		t.Fatalf("the open file changed: %+v %v", now, err)
	}
	buf := make([]byte, 3)
	if _, err := f.ReadAt(buf, 0); err != nil || string(buf) != "abc" {
		t.Fatalf("read of the old file: %v %q", err, buf)
	}
}

func TestStatSame(t *testing.T) {
	tm := time.Unix(1000, 0)
	s := Stat{Size: 10, ModTime: tm, Dev: 1, Ino: 2}
	if !s.Same(s) {
		t.Fatal("not the same as itself")
	}
	// The same instant in another zone is the same time
	if !s.Same(Stat{Size: 10, ModTime: tm.In(time.FixedZone("x", 3600)), Dev: 1, Ino: 2}) {
		t.Fatal("zone matters")
	}
	for _, o := range []Stat{
		{Size: 11, ModTime: tm, Dev: 1, Ino: 2},
		{Size: 10, ModTime: tm.Add(time.Nanosecond), Dev: 1, Ino: 2},
		{Size: 10, ModTime: tm, Dev: 3, Ino: 2},
		{Size: 10, ModTime: tm, Dev: 1, Ino: 3},
	} {
		if s.Same(o) {
			t.Fatalf("%+v same as %+v", s, o)
		}
	}
}

func TestOpenCharDevice(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip()
	}
	f, err := OpenFile("/dev/zero")
	if err != nil {
		t.Skipf("no /dev/zero: %v", err)
	}
	defer f.Close()
	if !f.Device() {
		t.Fatal("/dev/zero is not a device")
	}
	if f.Size() < 0 || f.Stat().Size != f.Size() {
		t.Fatalf("size %d stat %+v", f.Size(), f.Stat())
	}
	// A device keeps its size and stat
	st := f.Stat()
	if err := f.Refresh(); err != nil {
		t.Fatal(err)
	}
	now, err := f.StatNow()
	if err != nil || !now.Same(st) || !f.Stat().Same(st) {
		t.Fatalf("device stat changed: %+v %v", now, err)
	}
}

func TestOpenDeviceSize(t *testing.T) {
	// A file sought to its end gives its size, as a block device does
	p := writeTemp(t, make([]byte, 1234))
	h, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	f, err := openDevice(h, p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if !f.Device() || f.Size() != 1234 || f.Stat().Size != 1234 {
		t.Fatalf("device %v size %d", f.Device(), f.Size())
	}
	buf := make([]byte, 10)
	if n, err := f.ReadAt(buf, 1230); n != 4 || err != io.EOF {
		t.Fatalf("read at the end: %d %v", n, err)
	}

	// A closed handle: the size cannot be asked and the error names the path
	h2, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	h2.Close()
	if _, err := openDevice(h2, p); err == nil || !bytes.Contains([]byte(err.Error()), []byte(p)) {
		t.Fatalf("size of a closed handle: %v", err)
	}
}

func TestMemory(t *testing.T) {
	m := NewMemory([]byte("hello"))
	if m.Size() != 5 {
		t.Fatal(m.Size())
	}
	buf := make([]byte, 3)
	if n, err := m.ReadAt(buf, 1); n != 3 || err != nil || string(buf) != "ell" {
		t.Fatalf("%d %v %q", n, err, buf)
	}
	if n, err := m.ReadAt(buf, 3); n != 2 || err != io.EOF || string(buf[:2]) != "lo" {
		t.Fatalf("%d %v", n, err)
	}
	if n, err := m.ReadAt(buf, 5); n != 0 || err != io.EOF {
		t.Fatalf("%d %v", n, err)
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	if NewMemory(nil).Size() != 0 {
		t.Fatal("empty memory has a size")
	}
}
