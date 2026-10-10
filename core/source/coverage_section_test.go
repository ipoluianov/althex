package source

import (
	"errors"
	"io"
	"runtime"
	"strings"
	"testing"
)

func TestSection(t *testing.T) {
	base := NewMemory([]byte("0123456789"))
	s := NewSection(base, 2, 5) // "23456"
	if s.Size() != 5 {
		t.Fatal(s.Size())
	}
	buf := make([]byte, 3)
	if n, err := s.ReadAt(buf, 1); n != 3 || err != nil || string(buf) != "345" {
		t.Fatalf("%d %v %q", n, err, buf)
	}
	// Not past its end, though the source has more
	buf = make([]byte, 4)
	if n, err := s.ReadAt(buf, 3); n != 2 || err != io.EOF || string(buf[:n]) != "56" {
		t.Fatalf("across the end: %d %v %q", n, err, buf[:n])
	}
	if n, err := s.ReadAt(buf, 5); n != 0 || err != io.EOF {
		t.Fatalf("at the end: %d %v", n, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	// Close leaves the source under it alone
	if n, err := base.ReadAt(buf, 0); n != 4 || err != nil {
		t.Fatal("the source is closed")
	}
	var _ Source = s
}

func TestSectionPastSource(t *testing.T) {
	// A section larger than its source: the source's error comes through
	s := NewSection(NewMemory([]byte("abc")), 1, 10)
	buf := make([]byte, 5)
	if n, err := s.ReadAt(buf, 0); n != 2 || err != io.EOF || string(buf[:n]) != "bc" {
		t.Fatalf("%d %v", n, err)
	}
	// Across the section's end, the source's error wins over io.EOF
	bad := errors.New("bad")
	s = NewSection(errReader{bad}, 0, 4)
	if _, err := s.ReadAt(make([]byte, 8), 0); err != bad {
		t.Fatalf("error lost: %v", err)
	}
	if _, err := s.ReadAt(make([]byte, 2), 0); err != bad {
		t.Fatalf("error lost: %v", err)
	}
}

type errReader struct{ err error }

func (e errReader) ReadAt(p []byte, off int64) (int, error) { return 0, e.err }

func TestSectionOfCache(t *testing.T) {
	data := pattern(3 * PageSize)
	c := NewCache(NewMemory(data), 4)
	s := NewSection(c, PageSize-2, 4)
	buf := make([]byte, 4)
	if n, err := s.ReadAt(buf, 0); n != 4 || err != nil || string(buf) != string(data[PageSize-2:PageSize+2]) {
		t.Fatalf("%d %v", n, err)
	}
}

func TestListDisks(t *testing.T) {
	disks, err := ListDisks()
	if runtime.GOOS != "linux" {
		// Elsewhere it may need privileges; it must not panic
		t.Logf("%d disks, %v", len(disks), err)
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range disks {
		if d.Size <= 0 || d.Size%512 != 0 {
			t.Errorf("%s: size %d", d.Name, d.Size)
		}
		if d.Path != "/dev/"+d.Name || d.Name == "" {
			t.Errorf("path %q name %q", d.Path, d.Name)
		}
		if strings.HasPrefix(d.Name, "ram") || strings.HasPrefix(d.Name, "zram") {
			t.Errorf("%s listed", d.Name)
		}
		if d.Model != strings.TrimSpace(d.Model) {
			t.Errorf("%s: model %q not trimmed", d.Name, d.Model)
		}
		for _, p := range d.Partitions {
			if !strings.HasPrefix(p.Name, d.Name) || p.Path != "/dev/"+p.Name {
				t.Errorf("partition %q of %q", p.Name, d.Name)
			}
			if p.Size < 0 || p.Size > d.Size {
				t.Errorf("partition %s size %d of %d", p.Name, p.Size, d.Size)
			}
			if len(p.Partitions) != 0 {
				t.Errorf("partition %s has partitions", p.Name)
			}
		}
	}
}
