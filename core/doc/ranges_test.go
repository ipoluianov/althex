package doc

import (
	"errors"
	"testing"

	"github.com/ipoluianov/althex/core/source"
)

func TestEditedRanges(t *testing.T) {
	d, err := Open(writeFile(t, t.TempDir(), []byte("0123456789abcdef")), Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	d.Overwrite(2, []byte("ab"), EditOptions{})
	d.Overwrite(6, []byte("x"), EditOptions{})
	d.Insert(12, []byte("y"), EditOptions{})
	got := d.Snapshot().EditedRanges(0)
	// After the insert the bytes 12.. are at other offsets than in the file
	want := []Range{{2, 2}, {6, 1}, {12, 5}}
	if len(got) != len(want) {
		t.Fatalf("%v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%v, want %v", got, want)
		}
	}
	// A limit: the last range covers the rest
	if got := d.Snapshot().EditedRanges(2); len(got) != 2 || got[1] != (Range{6, 11}) {
		t.Fatalf("limited: %v", got)
	}
}

// badSource fails to read its second page, like a bad sector
type badSource struct{ *source.Memory }

func (b badSource) ReadAt(p []byte, off int64) (int, error) {
	if off < 2*source.PageSize && off+int64(len(p)) > source.PageSize {
		return 0, errors.New("bad sector")
	}
	return b.Memory.ReadAt(p, off)
}

func TestUnreadableRanges(t *testing.T) {
	d, err := OpenSource(badSource{source.NewMemory(make([]byte, 3*source.PageSize))}, "disk")
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	snap := d.Snapshot()
	if got := snap.UnreadableRanges(); len(got) != 0 {
		t.Fatalf("before reading: %v", got)
	}
	buf := make([]byte, 16)
	snap.ReadAt(buf, source.PageSize+5)
	snap.ReadAt(buf, 0)
	got := snap.UnreadableRanges()
	if len(got) != 1 || got[0] != (Range{source.PageSize, source.PageSize}) {
		t.Fatalf("%v", got)
	}
}
