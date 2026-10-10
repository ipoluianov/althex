package diff

import (
	"bytes"
	"context"
	"errors"
	"io"
	"math/rand/v2"
	"strings"
	"testing"
)

// naiveSpans is CompareN byte by byte
func naiveSpans(ins [][]byte) []Span {
	total := 0
	for _, in := range ins {
		total = max(total, len(in))
	}
	var out []Span
	for p := range total {
		mask := uint64(0)
		for i := 1; i < len(ins); i++ {
			inA, inI := p < len(ins[0]), p < len(ins[i])
			if inA != inI || inA && ins[0][p] != ins[i][p] {
				mask |= 1 << uint(i)
			}
		}
		if mask == 0 {
			continue
		}
		mask |= 1
		if k := len(out) - 1; k >= 0 && out[k].End() == int64(p) && out[k].Mask == mask {
			out[k].Len++
			continue
		}
		out = append(out, Span{Off: int64(p), Len: 1, Mask: mask})
	}
	return out
}

func compareN(t *testing.T, ins [][]byte, opt Options) ([]Span, error) {
	t.Helper()
	rs := make([]io.ReaderAt, len(ins))
	sizes := make([]int64, len(ins))
	for i, in := range ins {
		rs[i] = bytes.NewReader(in)
		sizes[i] = int64(len(in))
	}
	return CompareN(context.Background(), rs, sizes, opt, nil)
}

func TestCompareNSmall(t *testing.T) {
	a := []byte("0123456789")
	b := []byte("0123x56789AB")
	c := []byte("0yz3x567")
	got, err := compareN(t, [][]byte{a, b, c}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	want := []Span{
		{Off: 1, Len: 2, Mask: 0b101},
		{Off: 4, Len: 1, Mask: 0b111},
		{Off: 8, Len: 2, Mask: 0b101},
		{Off: 10, Len: 2, Mask: 0b011},
	}
	if !equalSpans(got, want) {
		t.Fatalf("spans %+v, want %+v", got, want)
	}
	regs := Regions(got)
	if len(regs) != 3 || regs[2].Off != 8 || regs[2].Len != 4 || regs[2].Mask != 0b111 {
		t.Fatalf("regions %+v", regs)
	}
	if i := SpansIn(got, 4); i != 1 {
		t.Fatalf("SpansIn 4 = %d", i)
	}
	var sb strings.Builder
	err = WriteReportN(&sb, []string{"a", "b", "c"}, got, []io.ReaderAt{bytes.NewReader(a), bytes.NewReader(b), bytes.NewReader(c)},
		[]int64{int64(len(a)), int64(len(b)), int64(len(c))}, 16)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"[2] c: 8 bytes", "Differing ranges: 3, 7 bytes", "#3 0x00000008+4", "[2] c: -", "[1] b: 38 39 41 42"} {
		if !strings.Contains(sb.String(), s) {
			t.Fatalf("the report has no %q:\n%s", s, sb.String())
		}
	}
}

func TestCompareNSame(t *testing.T) {
	a := []byte("same bytes")
	got, err := compareN(t, [][]byte{a, a, a, a}, Options{})
	if err != nil || len(got) != 0 {
		t.Fatalf("%+v %v", got, err)
	}
	got, err = compareN(t, [][]byte{nil, nil}, Options{})
	if err != nil || len(got) != 0 {
		t.Fatalf("empty: %+v %v", got, err)
	}
}

// TestCompareNRandom compares inputs over several chunks with the naive way
func TestCompareNRandom(t *testing.T) {
	rng := rand.New(rand.NewPCG(7, 9))
	base := randBytes(rng, chunkSize*2+12345)
	ins := [][]byte{base}
	for k := range 4 {
		in := append([]byte(nil), base...)
		for range 200 {
			off := rng.IntN(len(in))
			for i := off; i < min(off+1+rng.IntN(40), len(in)); i++ {
				in[i] = byte(rng.Uint32())
			}
		}
		// Changes across the bound of the chunks
		in[chunkSize-1] ^= 0xFF
		in[chunkSize] ^= 0xFF
		switch k {
		case 1:
			in = in[:chunkSize+7]
		case 2:
			in = append(in, randBytes(rng, 100)...)
		}
		ins = append(ins, in)
	}
	got, err := compareN(t, ins, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if want := naiveSpans(ins); !equalSpans(got, want) {
		t.Fatalf("%d spans, want %d", len(got), len(want))
	}
}

func TestCompareNLimits(t *testing.T) {
	a := []byte("aaaaaaaaaa")
	b := []byte("abababababab")
	got, err := compareN(t, [][]byte{a, b}, Options{MaxRanges: 2})
	if !errors.Is(err, ErrMaxRanges) || len(got) != 2 {
		t.Fatalf("%+v %v", got, err)
	}
	if _, err := compareN(t, [][]byte{a}, Options{}); err == nil {
		t.Fatal("one input compared")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = CompareN(ctx, []io.ReaderAt{bytes.NewReader(a), bytes.NewReader(b)}, []int64{10, 12}, Options{}, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled: %v", err)
	}
}

func equalSpans(a, b []Span) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// FuzzCompareN compares three inputs as the naive way does
func FuzzCompareN(f *testing.F) {
	f.Add([]byte("hello"), []byte("hellO, world"), []byte("he"))
	f.Add([]byte{}, []byte{1}, []byte{})
	f.Fuzz(func(t *testing.T, a, b, c []byte) {
		ins := [][]byte{a, b, c}
		got, err := compareN(t, ins, Options{})
		if err != nil {
			t.Fatal(err)
		}
		if want := naiveSpans(ins); !equalSpans(got, want) {
			t.Fatalf("spans %+v, want %+v", got, want)
		}
	})
}
