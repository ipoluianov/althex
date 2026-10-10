package analysis

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

// callGraph: Reset calls sub_10 and sub_20, sub_10 calls sub_20 and
// itself, sub_20 calls sub_10 (a cycle of two)
func callGraph(t *testing.T) *Result {
	m := make(mem, 64)
	copy(m[0:], []byte{0x02, 0x10, 0x02, 0x20, 0x04})
	copy(m[0x10:], []byte{0x02, 0x20, 0x02, 0x10, 0x04})
	copy(m[0x20:], []byte{0x02, 0x10, 0x04})
	res := Analyze(context.Background(), m, Options{Arch: toy{}, Entries: []Entry{{Addr: 0, Name: "Reset"}}})
	if len(res.Funcs) != 3 {
		t.Fatalf("functions: %v", res.Funcs)
	}
	return res
}

func TestWriteDOT(t *testing.T) {
	res := callGraph(t)
	var b bytes.Buffer
	if err := res.WriteDOT(&b, map[uint64]string{0x20: `my "func"`}); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	for _, want := range []string{
		"digraph calls {",
		`f_0 [label="Reset\n0x0"];`,
		`f_10 [label="sub_10\n0x10"];`,
		`f_20 [label="my \"func\"\n0x20"];`,
		"f_0 -> f_10;", "f_0 -> f_20;", "f_10 -> f_10;", "f_10 -> f_20;", "f_20 -> f_10;",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("no %q in\n%s", want, out)
		}
	}
	if !strings.HasSuffix(out, "}\n") || strings.Count(out, "->") != 5 {
		t.Errorf("edges or the end:\n%s", out)
	}
}

func TestCallChildren(t *testing.T) {
	res := callGraph(t)
	got := res.CallChildren([]uint64{0, 0x10}, false)
	if len(got) != 2 || got[0] != (CallNode{Addr: 0x10, Cycle: true}) || got[1] != (CallNode{Addr: 0x20}) {
		t.Errorf("callees of sub_10 under Reset: %+v", got)
	}
	got = res.CallChildren([]uint64{0x20}, true)
	if len(got) != 2 || got[0] != (CallNode{Addr: 0}) || got[1] != (CallNode{Addr: 0x10}) {
		t.Errorf("callers of sub_20 as the root: %+v", got)
	}
	got = res.CallChildren([]uint64{0x20, 0x10}, true)
	if len(got) != 3 || !got[1].Cycle || !got[2].Cycle || got[0].Cycle {
		t.Errorf("callers of sub_10 under sub_20: %+v", got)
	}
	if res.CallChildren(nil, false) != nil || res.CallChildren([]uint64{0x30}, false) != nil {
		t.Error("no function")
	}
}
