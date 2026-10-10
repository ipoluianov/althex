package forms

import (
	"testing"

	"github.com/ipoluianov/althex/core/doc"
)

// An undo and then another edit make a branch: both lines are listed and
// any state can be gone to (EDIT-3)
func TestHistoryRows(t *testing.T) {
	d, err := doc.FromBytes([]byte("abcdef"), doc.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	d.Overwrite(0, []byte("X"), doc.EditOptions{Kind: "one"})
	d.Overwrite(1, []byte("Y"), doc.EditOptions{Kind: "two"})
	d.Undo()
	d.Overwrite(2, []byte("Z"), doc.EditOptions{Kind: "three"})

	root, cur := d.History()
	rows := historyRows(root)
	var got []string
	for _, r := range rows {
		got = append(got, r.n.Kind+string(rune('0'+r.depth)))
	}
	// root, one, then its two children: the branch made first indented
	if len(rows) != 4 || rows[0].n != root || rows[1].n.Kind != "one" || cur.Kind != "three" {
		t.Fatalf("rows: %v", got)
	}
	depth := map[string]int{}
	for _, r := range rows {
		depth[r.n.Kind] = r.depth
	}
	if depth["two"] == depth["three"] {
		t.Errorf("the branches are at the same depth: %v", got)
	}
	// Going to the state of the other branch gives its bytes
	for _, r := range rows {
		if r.n.Kind == "two" {
			if err := d.Goto(r.n.ID); err != nil {
				t.Fatal(err)
			}
		}
	}
	b := make([]byte, 6)
	d.ReadAt(b, 0)
	if string(b) != "XYcdef" {
		t.Errorf("after going to the other branch: %q", b)
	}
}
