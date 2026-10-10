package api_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ipoluianov/althex/core/api"
	"github.com/ipoluianov/althex/core/tmpl"
	"github.com/ipoluianov/althex/internal/fuzzguard"
)

// FuzzCall calls two tools with any parameters over a document of any
// bytes; edits are allowed (they stay in memory), saving is not called
func FuzzCall(f *testing.F) {
	var methods []string
	for _, t := range api.New(&api.Files{}, tmpl.Library).Tools() {
		if t.Name != "save_document" && t.Name != "open_document" {
			methods = append(methods, t.Name)
		}
	}
	index := func(name string) uint8 {
		for i, m := range methods {
			if m == name {
				return uint8(i)
			}
		}
		f.Fatalf("no tool %s", name)
		return 0
	}
	seeds := []struct {
		m1, p1, m2, p2 string
	}{
		{"read", `{"offset": 54, "length": 5, "format": "hex"}`, "read", `{"format": "text", "encoding": "utf16be"}`},
		{"search", `{"hex": "48 ?? 6C", "max_results": 3}`, "search", `{"text": "ALTHEX", "ignore_case": true, "align": 2}`},
		{"hash", `{"algorithms": ["crc32", "sha256", "crc:CRC-16/ARC", "nosuch"], "offset": 2}`, "list_documents", `{}`},
		{"apply_template", `{"template": "BMP"}`, "get_structure", `{"path": "info.bitCount", "depth": 8}`},
		{"list_templates", `{"document": "1"}`, "get_structure", `{"offset": 60, "max_children": 1}`},
		{"propose_edits", `{"description": "x", "edits": [{"offset": 1, "overwrite": "AA BB"}, {"offset": 0, "insert": "00"}, {"offset": 3, "delete": 2}]}`, "read", `{"format": "dump"}`},
		{"show", `{"offset": 3, "length": 9}`, "read", `{"offset": 99999999999}`},
	}
	for _, s := range seeds {
		f.Add(bmp(), index(s.m1), s.p1, index(s.m2), s.p2)
	}
	path := filepath.Join(f.TempDir(), "x.bin")
	f.Fuzz(func(t *testing.T, data []byte, m1 uint8, p1 string, m2 uint8, p2 string) {
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
		ws := &api.Files{AllowWrite: true}
		if _, err := ws.Add(path); err != nil {
			t.Fatal(err)
		}
		defer ws.Close()
		s := api.New(ws, tmpl.Library)
		stop := fuzzguard.Start("api")
		defer stop()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		for _, c := range []struct {
			m uint8
			p string
		}{{m1, p1}, {m2, p2}} {
			res, err := s.Call(ctx, methods[int(c.m)%len(methods)], json.RawMessage(c.p))
			if err == nil {
				if _, err := json.Marshal(res); err != nil {
					t.Fatalf("%s %s: the result is not JSON: %v", methods[int(c.m)%len(methods)], c.p, err)
				}
			}
		}
	})
}
