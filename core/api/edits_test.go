package api

import (
	"bytes"
	"testing"

	"github.com/ipoluianov/althex/core/doc"
)

// The edits of a proposal are one step of the history; a failing one
// leaves nothing of it (EXT-8)
func TestApplyEditsOneStep(t *testing.T) {
	orig := []byte("0123456789")
	d, err := doc.FromBytes(orig, doc.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	d.Overwrite(0, []byte("A"), doc.EditOptions{}) // the user's edit before
	p := Proposal{Edits: []Edit{{Offset: 1, Overwrite: []byte("BC")}, {Offset: 5, Insert: []byte("xy")}, {Offset: 0, Delete: 1}}}
	if err := applyEdits(d, p, "external: test"); err != nil {
		t.Fatal(err)
	}
	read := func() []byte {
		b := make([]byte, d.Size())
		d.ReadAt(b, 0)
		return b
	}
	if got := read(); string(got) != "BC34xy56789" {
		t.Fatalf("applied: %q", got)
	}
	d.Undo()
	if got := read(); string(got) != "A123456789" {
		t.Fatalf("one undo: %q", got)
	}
	// A failing edit: the ones before it are undone, the user's edit stays
	bad := Proposal{Edits: []Edit{{Offset: 1, Overwrite: []byte("Z")}, {Offset: 100, Delete: 1}}}
	if err := applyEdits(d, bad, "external: test"); err == nil {
		t.Fatal("want an error")
	}
	if got := read(); !bytes.Equal(got, []byte("A123456789")) {
		t.Fatalf("after a failure: %q", got)
	}
}
