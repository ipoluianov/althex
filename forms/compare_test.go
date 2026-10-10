package forms

import (
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/ipoluianov/althex/core/diff"
	"github.com/ipoluianov/althex/core/doc"
)

func TestNextRegion(t *testing.T) {
	rs := []diff.Span{{Off: 10, Len: 2}, {Off: 20, Len: 4}, {Off: 40, Len: 1}}
	// From the start: the first one; after selecting it, the second one
	if k := nextRegion(rs, 0, 0, -1, false); k != 0 {
		t.Fatalf("first: %d", k)
	}
	if k := nextRegion(rs, 10, 12, 0, false); k != 1 {
		t.Fatalf("after the first: %d", k)
	}
	// A difference at the caret is gone to once
	if k := nextRegion(rs, 10, 10, -1, false); k != 0 {
		t.Fatalf("at the caret: %d", k)
	}
	if k := nextRegion(rs, 40, 41, 2, false); k != -1 {
		t.Fatalf("after the last: %d", k)
	}
	// Back from the second one selected: the first one, not itself
	if k := nextRegion(rs, 20, 24, 1, true); k != 0 {
		t.Fatalf("back: %d", k)
	}
	if k := nextRegion(rs, 10, 12, 0, true); k != -1 {
		t.Fatalf("back from the first: %d", k)
	}
}

func TestDescribeWrites(t *testing.T) {
	var rs []doc.Range
	for i := range 10 {
		rs = append(rs, doc.Range{Off: int64(i) * 0x10000, Len: 4096})
	}
	s := describeWrites(rs, 3)
	for _, want := range []string{"0x0..0xFFF", "0x20000..0x20FFF", "10", "40960"} {
		if !strings.Contains(s, want) {
			t.Fatalf("%q has no %q", s, want)
		}
	}
	if strings.Contains(s, "0x30000") {
		t.Fatalf("%q shows more than 3 ranges", s)
	}
}

func TestWriteDeniedReason(t *testing.T) {
	err := &os.PathError{Op: "open", Path: "/secret/file", Err: errors.New("permission denied")}
	if got := writeDeniedReason(err); got != "permission denied" {
		t.Fatalf("%q", got)
	}
}

// A compare tab byte by byte: the documents as they are, edits included
func TestCompareReportOfSnapshots(t *testing.T) {
	a, _ := doc.FromBytes([]byte("0123456789"), doc.Options{})
	b, _ := doc.FromBytes([]byte("0123456789"), doc.Options{})
	defer a.Close()
	defer b.Close()
	b.Overwrite(3, []byte("x"), doc.EditOptions{})
	ta, tb := &Tab{doc: a}, &Tab{doc: b}
	da, err := snapshotOf(ta)
	if err != nil {
		t.Fatal(err)
	}
	db, err := snapshotOf(tb)
	if err != nil {
		t.Fatal(err)
	}
	cmp := &Compare{docs: []*doc.Document{da, db}, from: []*doc.Document{a, b}, cur: -1}
	defer cmp.close()
	defer da.Close()
	// Later edits do not change the comparison
	b.Overwrite(5, []byte("y"), doc.EditOptions{})
	spans, err := diff.CompareN(t.Context(), []io.ReaderAt{da.Snapshot(), db.Snapshot()}, []int64{10, 10}, diff.Options{}, nil)
	if err != nil || len(spans) != 1 || spans[0].Off != 3 {
		t.Fatalf("%+v %v", spans, err)
	}
	cmp.spans, cmp.regions, cmp.done = spans, diff.Regions(spans), true
	if cmp.count() != 1 {
		t.Fatalf("count %d", cmp.count())
	}
	var sb strings.Builder
	if err := writeCompareReport(&sb, cmp); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sb.String(), "#1 0x00000003+1") || !strings.Contains(sb.String(), "78") {
		t.Fatalf("report:\n%s", sb.String())
	}
}
