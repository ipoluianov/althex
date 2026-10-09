package patch

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"math/rand/v2"
	"strings"
	"testing"
)

func randBytes(rng *rand.Rand, n int) []byte {
	p := make([]byte, n)
	for i := range p {
		p[i] = byte(rng.Uint32())
	}
	return p
}

// mutate makes a copy of src with random inserts, deletes and overwrites;
// inPlace keeps the offsets (overwrites, growth and truncation only)
func mutate(rng *rand.Rand, src []byte, edits int, inPlace bool) []byte {
	dst := append([]byte(nil), src...)
	for range edits {
		n := 1 + rng.IntN(3000)
		off := 0
		if len(dst) > 0 {
			off = rng.IntN(len(dst))
		}
		kind := rng.IntN(3)
		if inPlace {
			kind = 2
		}
		switch kind {
		case 0:
			dst = append(dst[:off], append(randBytes(rng, n), dst[off:]...)...)
		case 1:
			dst = append(dst[:off], dst[min(off+n, len(dst)):]...)
		case 2:
			v := byte(rng.Uint32())
			for i := off; i < min(off+n, len(dst)); i++ {
				if rng.IntN(4) == 0 {
					dst[i] = v // runs, for RLE
				} else {
					dst[i] = byte(rng.Uint32())
				}
			}
		}
	}
	if inPlace {
		switch rng.IntN(3) {
		case 0:
			dst = append(dst, randBytes(rng, rng.IntN(5000))...)
		case 1:
			dst = dst[:rng.IntN(len(dst)+1)]
		}
	}
	return dst
}

func makePatch(t testing.TB, f Format, src, dst []byte) []byte {
	t.Helper()
	var p bytes.Buffer
	if err := Make(context.Background(), f, &p, bytes.NewReader(src), int64(len(src)), bytes.NewReader(dst), int64(len(dst)), nil); err != nil {
		t.Fatalf("%v: make: %v", f, err)
	}
	return p.Bytes()
}

func apply(f Format, p, src []byte) ([]byte, error) {
	var out bytes.Buffer
	err := Apply(context.Background(), f, bytes.NewReader(p), &out, bytes.NewReader(src), int64(len(src)))
	return out.Bytes(), err
}

func roundTrip(t *testing.T, f Format, src, dst []byte) []byte {
	t.Helper()
	p := makePatch(t, f, src, dst)
	if g, ok := Detect(p); !ok || g != f {
		t.Fatalf("%v: detected as %v %v", f, g, ok)
	}
	out, err := apply(f, p, src)
	if err != nil {
		t.Fatalf("%v: apply: %v", f, err)
	}
	if !bytes.Equal(out, dst) {
		t.Fatalf("%v: the result differs: %d bytes, want %d", f, len(out), len(dst))
	}
	return p
}

func TestRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	for i := range 40 {
		src := randBytes(rng, rng.IntN(1<<20))
		if i%5 == 0 {
			src = bytes.Repeat(randBytes(rng, 1+rng.IntN(100)), rng.IntN(5000))
		}
		for _, f := range Formats() {
			dst := mutate(rng, src, rng.IntN(30), f == IPS)
			roundTrip(t, f, src, dst)
		}
	}
}

func TestEdgeCases(t *testing.T) {
	cases := [][2]string{
		{"", ""}, {"", "abc"}, {"abc", ""}, {"abc", "abc"}, {"a", "b"},
		{"hello world", "hello brave new world"}, {"0123456789", "01234"},
		{strings.Repeat("x", 100), strings.Repeat("y", 100)},
	}
	for _, c := range cases {
		for _, f := range Formats() {
			roundTrip(t, f, []byte(c[0]), []byte(c[1]))
		}
	}
}

func TestIPSLimits(t *testing.T) {
	// a record cannot start at "EOF"
	src := make([]byte, ipsEOF+100)
	dst := append([]byte(nil), src...)
	dst[ipsEOF] = 1
	dst[ipsEOF+10] = 2
	dst[ipsEOF+20] = 3
	p := roundTrip(t, IPS, src, dst)
	if bytes.Contains(p[5:len(p)-3], []byte("EOF")) {
		t.Fatal("a record starts at EOF")
	}
	for i := ipsEOF + 30; i < ipsEOF+60; i++ {
		dst[i] = 7 // a run at EOF
	}
	roundTrip(t, IPS, src, dst)

	big := make([]byte, ipsMaxOff+100)
	changed := append([]byte(nil), big...)
	changed[ipsMaxOff+50] = 1
	var buf bytes.Buffer
	err := Make(context.Background(), IPS, &buf, bytes.NewReader(big), int64(len(big)), bytes.NewReader(changed), int64(len(changed)), nil)
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("over 16 MB: %v", err)
	}
	// a change below 16 MB of a larger file is fine
	changed[ipsMaxOff+50] = 0
	changed[10] = 1
	roundTrip(t, IPS, big, changed)
}

func TestBPSByHand(t *testing.T) {
	if b := bpsNum(128); !bytes.Equal(b, []byte{0x00, 0x80}) {
		t.Fatalf("128 is % X", b)
	}
	tiny := func(src, dst string, actions ...byte) []byte {
		p := []byte("BPS1")
		p = append(p, 0x80|byte(len(src)), 0x80|byte(len(dst)), 0x80)
		p = append(p, actions...)
		p = binary.LittleEndian.AppendUint32(p, crc32.ChecksumIEEE([]byte(src)))
		p = binary.LittleEndian.AppendUint32(p, crc32.ChecksumIEEE([]byte(dst)))
		return binary.LittleEndian.AppendUint32(p, crc32.ChecksumIEEE(p))
	}
	cases := []struct {
		src, dst string
		patch    []byte
	}{
		// SourceRead 3, TargetRead 3 "XYZ", SourceCopy 3 from +3
		{"abcdef", "abcXYZdef", tiny("abcdef", "abcXYZdef", 0x88, 0x89, 'X', 'Y', 'Z', 0x8A, 0x86)},
		// SourceRead 3, TargetCopy 6 from 0 (overlapping)
		{"abc", "abcabcabc", tiny("abc", "abcabcabc", 0x88, 0x97, 0x80)},
	}
	for _, c := range cases {
		out, err := apply(BPS, c.patch, []byte(c.src))
		if err != nil || string(out) != c.dst {
			t.Fatalf("%q: %q %v", c.dst, out, err)
		}
	}
	// what Make writes for the first case
	if p := makePatch(t, BPS, []byte("abcdef"), []byte("abcXYZdef")); !bytes.Equal(p, cases[0].patch) {
		t.Fatalf("make wrote % X\nwant % X", p, cases[0].patch)
	}
	// a wrong source
	if _, err := apply(BPS, cases[0].patch, []byte("abcdeX")); !errors.Is(err, ErrSourceMismatch) {
		t.Fatal(err)
	}
}

func bpsNum(v uint64) []byte {
	var b bytes.Buffer
	putNum(&b, v)
	return b.Bytes()
}

func TestNums(t *testing.T) {
	rng := rand.New(rand.NewPCG(3, 4))
	for range 10000 {
		v := rng.Uint64() >> rng.IntN(64)
		v &= 1<<62 - 1
		got, err := getNum(bytes.NewReader(bpsNum(v)))
		if err != nil || got != v {
			t.Fatalf("%d: %d %v", v, got, err)
		}
	}
}

func TestHexDiffText(t *testing.T) {
	src := []byte("MZ\x90\x00 some data")
	dst := []byte("MZ\x91\x00 some data!")
	var p bytes.Buffer
	if err := MakeHexDiff(context.Background(), &p, "old.exe", bytes.NewReader(src), int64(len(src)), "new.exe", bytes.NewReader(dst), int64(len(dst)), nil); err != nil {
		t.Fatal(err)
	}
	want := "--- a/old.exe 14\n+++ b/new.exe 15\n@@ -0x00000002,1 +0x00000002,1 @@\n-90\n+91\n@@ -0x0000000E,0 +0x0000000E,1 @@\n+21\n"
	if p.String() != want {
		t.Fatalf("got\n%s\nwant\n%s", p.String(), want)
	}
	// the '-' bytes are checked
	other := []byte("MZ\x92\x00 some data")
	if _, err := apply(HexDiff, p.Bytes(), other); !errors.Is(err, ErrSourceMismatch) {
		t.Fatal(err)
	}
}

func TestWrongSource(t *testing.T) {
	rng := rand.New(rand.NewPCG(5, 6))
	src := randBytes(rng, 10000)
	dst := mutate(rng, src, 5, false)
	other := append([]byte(nil), src...)
	other[9000]++
	for _, f := range []Format{BPS, AltHex} {
		p := makePatch(t, f, src, dst)
		if _, err := apply(f, p, other); !errors.Is(err, ErrSourceMismatch) {
			t.Fatalf("%v: %v", f, err)
		}
		if _, err := apply(f, p, src[:9999]); !errors.Is(err, ErrSourceMismatch) {
			t.Fatalf("%v: %v", f, err)
		}
	}
}

func TestCorrupt(t *testing.T) {
	rng := rand.New(rand.NewPCG(7, 8))
	src := randBytes(rng, 50000)
	for _, f := range Formats() {
		dst := mutate(rng, src, 10, f == IPS)
		p := makePatch(t, f, src, dst)
		// checksums catch every change in BPS and AltHex
		strict := f == BPS || f == AltHex
		for range 300 {
			q := append([]byte(nil), p...)
			if rng.IntN(2) == 0 {
				q = q[:rng.IntN(len(q))]
			} else {
				q[rng.IntN(len(q))] ^= byte(1 + rng.IntN(255))
			}
			// IPS and HexDiff may stay valid (a changed byte of data), so for
			// them it is only that there is no panic
			if _, err := apply(f, q, src); strict && err == nil {
				t.Fatalf("%v: a corrupt patch is applied", f)
			}
		}
	}
}

func TestCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	src := make([]byte, 4<<20)
	dst := make([]byte, 4<<20)
	dst[100] = 1
	for _, f := range Formats() {
		var p bytes.Buffer
		err := Make(ctx, f, &p, bytes.NewReader(src), int64(len(src)), bytes.NewReader(dst), int64(len(dst)), nil)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("%v: %v", f, err)
		}
		good := makePatch(t, f, src, dst)
		var out bytes.Buffer
		err = Apply(ctx, f, bytes.NewReader(good), &out, bytes.NewReader(src), int64(len(src)))
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("%v apply: %v", f, err)
		}
	}
}

func TestFormats(t *testing.T) {
	for _, f := range Formats() {
		if f.String() == "" || f.Ext() == "" || f.Ext()[0] != '.' {
			t.Fatal(f)
		}
	}
	if _, ok := Detect([]byte("hello")); ok {
		t.Fatal("detected text")
	}
	if _, err := apply(Format(99), nil, nil); !errors.Is(err, ErrFormat) {
		t.Fatal(err)
	}
}
