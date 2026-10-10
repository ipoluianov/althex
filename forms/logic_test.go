package forms

import (
	"testing"

	"github.com/ipoluianov/althex/core/diff"
	"github.com/ipoluianov/althex/forms/hexview"
)

func TestParseColorRules(t *testing.T) {
	rules := parseColorRules("00-1F=#ff8800; 7f=#888888, bad, 20-2=#zz, 30-31=#00ff00")
	if len(rules) != 3 {
		t.Fatalf("rules: %+v", rules)
	}
	if rules[0].From != 0 || rules[0].To != 0x1F || rules[1].From != 0x7F || rules[1].To != 0x7F {
		t.Fatalf("ranges: %+v", rules)
	}
}

func TestParseCRC(t *testing.T) {
	p, err := parseCRC("width=16 poly=0x1021 init=0xffff refin=false refout=false xorout=0x0000 check=0x29b1")
	if err != nil || p.Width != 16 || p.Poly != 0x1021 || p.Init != 0xFFFF {
		t.Fatalf("%+v %v", p, err)
	}
	if _, err := parseCRC("poly=0x1021"); err == nil {
		t.Fatal("no width accepted")
	}
	if _, err := parseCRC("width=8 foo=1"); err == nil {
		t.Fatal("an unknown parameter accepted")
	}
}

func TestEvalOffset(t *testing.T) {
	for _, c := range []struct {
		s    string
		want int64
		ok   bool
	}{
		{"0x400 + 3*512", 0x400 + 1536, true},
		{"+0x10", 116, true},
		{"-4", 96, true},
		{"size", 5000, true},
		{"size+1", 0, false},
		{"-200", 0, false},
		// Hex as the view shows it, without 0x
		{"8A3", 0x8A3, true},
		{"fa", 0xFA, true},
		{"1000", 1000, true}, // digits only: decimal
		{"8A3G", 0, false},
	} {
		got, err := evalOffset(c.s, 100, 5000)
		if (err == nil) != c.ok || (c.ok && got != c.want) {
			t.Errorf("%q: %d, %v", c.s, got, err)
		}
	}
}

func TestMarkProvider(t *testing.T) {
	marks := []hexview.Highlight{{Off: 0, Len: 10}, {Off: 20, Len: 5}, {Off: 100, Len: 1}}
	get := markProvider(marks)
	if got := get(5, 21); len(got) != 2 {
		t.Fatalf("marks in [5,21): %+v", got)
	}
	if got := get(30, 100); len(got) != 0 {
		t.Fatalf("marks in [30,100): %+v", got)
	}
}

func TestCompareMapOffset(t *testing.T) {
	// A: 10 equal, 4 deleted, 10 equal; B: 10 equal, 10 equal
	cmp := &Compare{ranges: []diff.Range{
		{Kind: diff.Equal, AOff: 0, ALen: 10, BOff: 0, BLen: 10},
		{Kind: diff.Deleted, AOff: 10, ALen: 4, BOff: 10, BLen: 0},
		{Kind: diff.Equal, AOff: 14, ALen: 10, BOff: 10, BLen: 10},
	}}
	if got := cmp.mapOffset(16, false); got != 12 {
		t.Fatalf("A 16 -> B %d", got)
	}
	if got := cmp.mapOffset(12, true); got != 16 {
		t.Fatalf("B 12 -> A %d", got)
	}
}

// NAV-2: bookmarks go out and come back; the old format (a bare list) is
// read; what does not fit the document is cut or left out
func TestBookmarksExportImport(t *testing.T) {
	list := []Bookmark{
		{Name: "header", Off: 0, Len: 16, Color: "#3f9e5a", Comment: "magic"},
		{Name: "tail", Off: 90, Len: 50, Color: "nope"},
		{Name: "outside", Off: 500, Len: 1, Color: "#ABC"},
	}
	got, err := decodeBookmarks(encodeBookmarks(list), 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != list[0] || got[1].Len != 10 || got[1].Color != bookmarkColors[0] {
		t.Errorf("round trip: %+v", got)
	}
	old, err := decodeBookmarks([]byte(`[{"Name": "a", "Off": 4, "Len": 2, "Color": "#ABC"}]`), 100)
	if err != nil || len(old) != 1 || old[0].Color != "#aabbcc" {
		t.Errorf("old format: %+v %v", old, err)
	}
	if _, err := decodeBookmarks([]byte(`{bad`), 100); err == nil {
		t.Error("want an error")
	}
	for in, want := range map[string]string{"#FF8800": "#ff8800", "0a0b0c": "#0a0b0c", "#abc": "#aabbcc", "#12345": "", "#zzzzzz": ""} {
		if got, ok := parseColor(in); got != want || ok != (want != "") {
			t.Errorf("parseColor(%q) = %q, %v", in, got, ok)
		}
	}
}

// A field chosen in the structure tree selects its bytes and the caret goes
// past them: the tree must stay on that field, not the next one
func TestSyncOffset(t *testing.T) {
	if got := syncOffset(0x14, 0x12, 2); got != 0x12 {
		t.Errorf("with a selection: %#x", got)
	}
	if got := syncOffset(0x14, 0x14, 0); got != 0x14 {
		t.Errorf("without one: %#x", got)
	}
}
