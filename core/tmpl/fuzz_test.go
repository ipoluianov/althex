package tmpl

import (
	"context"
	"testing"
	"time"

	"github.com/ipoluianov/althex/internal/fuzzguard"
)

// fuzzEnv runs a template over fuzzed data: small limits, so an input is fast
func fuzzEnv(data []byte, w Writer) Env {
	return Env{Data: &mem{b: data}, Writer: w, MaxSteps: 1_000_000, MaxNodes: 20_000}
}

// fuzzWriter is a document a fuzzed template may change: offsets out of
// it are errors, and it does not grow past a limit
type fuzzWriter struct{ m *mem }

const fuzzMaxSize = 1 << 20

func (w fuzzWriter) Overwrite(off int64, b []byte) error {
	if off < 0 || off > int64(len(w.m.b)) || int64(len(b)) > int64(len(w.m.b))-off {
		return errFuzzRange
	}
	copy(w.m.b[off:], b)
	return nil
}
func (w fuzzWriter) Insert(off int64, b []byte) error {
	if off < 0 || off > int64(len(w.m.b)) || len(w.m.b)+len(b) > fuzzMaxSize {
		return errFuzzRange
	}
	return w.m.Insert(off, b)
}
func (w fuzzWriter) Delete(off, n int64) error {
	if off < 0 || n < 0 || off > int64(len(w.m.b)) || n > int64(len(w.m.b))-off {
		return errFuzzRange
	}
	return w.m.Delete(off, n)
}

var errFuzzRange = &Error{Msg: "out of the document"}

// walkTree reads what the tree view and the API read of every node: no
// panic; the visits are limited, a tree may have many nodes
func walkTree(t *testing.T, root *Node, size int64) {
	visits := 0
	var walk func(n *Node, depth int)
	walk = func(n *Node, depth int) {
		visits++
		if visits > 50_000 {
			return
		}
		if depth > 2*maxDepth+16 {
			t.Fatalf("a tree %d deep", depth)
		}
		_ = n.Value()
		_ = n.Path()
		if n.Size < 0 {
			t.Fatalf("%s: size %d", n.Path(), n.Size)
		}
		count := n.ChildCount()
		if count < 0 {
			t.Fatalf("%s: %d children", n.Path(), count)
		}
		if n.Kind == NodeArray {
			for _, i := range []int64{0, 1, count / 2, count - 1, count, -1} {
				if c := n.Child(i); c != nil {
					if i < 0 || i >= count {
						t.Fatalf("%s: element %d of %d", n.Path(), i, count)
					}
					walk(c, depth+1)
				}
			}
		} else {
			for _, c := range n.Children() {
				walk(c, depth+1)
			}
			n.Child(count)
			n.Child(-1)
		}
	}
	walk(root, 0)
	for _, off := range []int64{0, 1, size / 3, size / 2, size - 1, size, -1} {
		if n := root.NodeAt(off); n != nil {
			_ = n.Path()
			_ = n.Value()
			if p := n.Path(); p != "" {
				root.Find(p)
			}
		}
	}
}

// runFuzzed runs a program with the limits, a deadline and the hang guard
func runFuzzed(t *testing.T, name string, p *Program, env Env) *Result {
	stop := fuzzguard.Start(name)
	defer stop()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res := p.Run(ctx, env)
	walkTree(t, res.Root, env.Data.Size())
	return res
}

// librarySeeds are the samples of the tests of the built-in templates, cut
// to a size the fuzzer works well with
func librarySeeds() [][]byte {
	out := [][]byte{
		makeBMP(), bootFIT(), makeFAT12(), makeFAT32(), makeExFAT(), makeISO(), fs2NTFS(), fs2Ext(),
		fs2HFS(), fs2APFS(), fwDfuSe(), fwESP32(16), partMBR(), partGPT(), makeExif(true), mdMakeZIP64(),
		mdMakePDF(0), mdMakeWAV(false), mdMakeMP4(), makeELF64(), makeELF32BE(), makePE64(),
	}
	for i, b := range out {
		out[i] = b[:min(len(b), 64<<10)]
	}
	return out
}

// FuzzLibrary runs every built-in template (the one of the index) over
// any data, then reads its tree as the window does
func FuzzLibrary(f *testing.F) {
	lib := Library()
	index := map[*Template]uint16{}
	for i, tm := range lib {
		index[tm] = uint16(i)
		if _, err := tm.Program(); err != nil {
			f.Fatalf("%s: %v", tm.File, err)
		}
		// A sample of each: its magic in zeros
		b := make([]byte, 1024)
		for _, m := range tm.Meta.Magic {
			off := m.Off
			if off < 0 {
				off += int64(len(b))
			}
			if off >= 0 && off+int64(len(m.Pattern)) <= int64(len(b)) {
				copy(b[off:], m.Pattern)
			}
		}
		f.Add(uint16(i), b)
	}
	for _, s := range librarySeeds() {
		if tm := Detect(lib, &mem{b: s}, ""); tm != nil {
			f.Add(index[tm], s)
		}
	}
	f.Fuzz(func(t *testing.T, idx uint16, data []byte) {
		tm := lib[int(idx)%len(lib)]
		p, _ := tm.Program()
		Detect(lib, &mem{b: data}, tm.File)
		runFuzzed(t, tm.File, p, fuzzEnv(data, nil))
	})
}

// FuzzRun compiles any template and runs it over any data, with a document
// it may change
func FuzzRun(f *testing.F) {
	f.Add(`struct A { uint32 x; if (x) uchar d[x]; } a; while(!FEof()) uchar b;`, []byte{2, 0, 0, 0, 1, 2, 3})
	f.Add(`enum <uchar> E { A, B = 3 } e; local int i; for (i=0;i<3;i++) Printf("%d", i);`, []byte{3})
	f.Add(`BigEndian(); uint16 n; struct R { uchar t; uchar l; uchar v[l]; } r[n] <optimize=false>;`, []byte{0, 2, 1, 1, 9, 2, 0})
	f.Add(`uchar a; a = 7; InsertBytes(0, 2, 0xAA); DeleteBytes(1, 1); uint16 w @ 0 <format=hex>;`, []byte{1, 2, 3, 4})
	f.Add(`typedef struct { uchar k : 4; uchar v : 4; } KV; KV kv[4]; string s; wstring ws; FSeek(0); char c[FileSize()];`, []byte("hello\x00w\x00\x00\x00"))
	f.Add(`int f(int n) { if (n <= 0) return 0; return n + f(n - 1); } Printf("%d", f(ReadUByte(0)));`, []byte{200})
	f.Add(`local uint32 c = Checksum("crc32", 0, FileSize()); Printf("%x %d", c, FindHex("AA ??", 0));`, []byte{0xAA, 1, 0xAA})
	f.Fuzz(func(t *testing.T, src string, data []byte) {
		p, err := Compile("fuzz", src)
		if err != nil {
			return
		}
		m := &mem{b: append([]byte(nil), data...)}
		env := fuzzEnv(nil, fuzzWriter{m})
		env.Data = m
		runFuzzed(t, "fuzz", p, env)
		p.Functions()
	})
}
