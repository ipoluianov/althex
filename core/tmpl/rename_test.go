package tmpl

import (
	"bytes"
	"context"
	"testing"
)

// A field renamed by an attribute is found by the path the tree shows
func TestFindRenamed(t *testing.T) {
	p, err := Compile("t", `struct H { uint a <name="first">; uint b; } h;`)
	if err != nil {
		t.Fatal(err)
	}
	res := p.Run(context.Background(), Env{Data: bytes.NewReader(make([]byte, 8))})
	if res.Err != nil {
		t.Fatal(res.Err)
	}
	n := res.Root.Find("h.a")
	if n == nil || n.Path() != "h.first" {
		t.Fatalf("h.a: %+v", n)
	}
	if res.Root.Find(n.Path()) != n {
		t.Errorf("Find(%q) does not find the node", n.Path())
	}
	if res.Root.Find("h.b") == nil || res.Root.Find("h.nope") != nil {
		t.Error("the other lookups")
	}
}
