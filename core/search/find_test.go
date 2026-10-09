package search

import (
	"bytes"
	"context"
	"errors"
	"io"
	"math/rand/v2"
	"slices"
	"sync/atomic"
	"testing"
)

func naiveFind(data []byte, p Pattern, from, to int64, align int) []Hit {
	var hits []Hit
	align = max(align, 1)
	for i := from; i+int64(p.Len()) <= to; i++ {
		if i%int64(align) == 0 && p.Match(data[i:to]) {
			hits = append(hits, Hit{Off: i, Len: p.Len()})
		}
	}
	return hits
}

func findAll(t *testing.T, data []byte, p Pattern, opt Options) []Hit {
	t.Helper()
	var hits []Hit
	err := FindAll(t.Context(), bytes.NewReader(data), int64(len(data)), p, opt, func(h Hit) bool {
		hits = append(hits, h)
		return true
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return hits
}

// randomPattern makes a pattern of every kind the matcher has a way for:
// exact, masked with a fixed anchor, case sets only, wide sets only
func randomPattern(rng *rand.Rand, data []byte) Pattern {
	n := 1 + rng.IntN(6)
	off := rng.IntN(len(data) - n + 1)
	src := data[off : off+n]
	p := Exact(src)
	switch rng.IntN(5) {
	case 0: // exact
	case 1, 2: // masks
		for i := range p.sets {
			var set byteSet
			b := src[i]
			switch rng.IntN(4) {
			case 0:
				set = p.sets[i]
			case 1:
				for v := 0; v < 256; v++ {
					set.add(byte(v))
				}
			case 2:
				for v := 0; v < 16; v++ {
					set.add(b&0xf0 | byte(v))
				}
			case 3:
				for v := 0; v < 16; v++ {
					set.add(byte(v)<<4 | b&15)
				}
			}
			p.sets[i] = set
		}
	case 3: // case-insensitive over letters and others
		for i := range p.sets {
			b := src[i] | 0x20
			if b >= 'a' && b <= 'z' {
				p.sets[i].add(b)
				p.sets[i].add(b - 0x20)
			}
		}
	case 4: // two or three values everywhere
		for i := range p.sets {
			for range 1 + rng.IntN(2) {
				p.sets[i].add(alphabet[rng.IntN(len(alphabet))])
			}
		}
	}
	return p
}

// A small alphabet gives many matches and many on the borders
var alphabet = []byte{'A', 'a', 'B', 'b', 0, 0x4f, 0xf4}

func randomData(rng *rand.Rand, n int) []byte {
	data := make([]byte, n)
	for i := range data {
		data[i] = alphabet[rng.IntN(len(alphabet))]
	}
	return data
}

func TestFindAllRandom(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	for iter := range 2000 {
		data := randomData(rng, 1+rng.IntN(600))
		p := randomPattern(rng, append(data, make([]byte, 8)...))
		opt := Options{
			ChunkSize: 7 + rng.IntN(58),
			Workers:   1 + rng.IntN(8),
		}
		if rng.IntN(3) == 0 {
			opt.From = int64(rng.IntN(len(data)))
			opt.To = opt.From + int64(rng.IntN(len(data)-int(opt.From)+1))
		}
		if rng.IntN(4) == 0 {
			opt.Align = 1 + rng.IntN(4)
		}
		from, to := span(opt.From, opt.To, int64(len(data)))
		want := naiveFind(data, p, from, to, opt.Align)
		got := findAll(t, data, p, opt)
		if !slices.Equal(got, want) {
			t.Fatalf("iter %d: pattern %v opt %+v size %d:\n got %v\nwant %v", iter, p, opt, len(data), got, want)
		}
		if len(want) > 0 {
			opt.MaxHits = 1 + rng.IntN(len(want))
			got = findAll(t, data, p, opt)
			if !slices.Equal(got, want[:opt.MaxHits]) {
				t.Fatalf("iter %d: MaxHits %d: got %v want %v", iter, opt.MaxHits, got, want[:opt.MaxHits])
			}
		}
	}
}

func TestFindAllOverlap(t *testing.T) {
	got := findAll(t, []byte("AAAA"), Exact([]byte("AA")), Options{ChunkSize: 1})
	want := []Hit{{0, 2}, {1, 2}, {2, 2}}
	if !slices.Equal(got, want) {
		t.Fatalf("got %v", got)
	}
	// A pattern longer than the chunk
	data := []byte("xxABCDEFGHxxABCDEFGH")
	got = findAll(t, data, Exact([]byte("ABCDEFGH")), Options{ChunkSize: 3})
	if !slices.Equal(got, []Hit{{2, 8}, {12, 8}}) {
		t.Fatalf("got %v", got)
	}
}

func TestFindAllStop(t *testing.T) {
	data := bytes.Repeat([]byte("ab"), 1000)
	n := 0
	err := FindAll(t.Context(), bytes.NewReader(data), int64(len(data)), Exact([]byte("b")), Options{ChunkSize: 16}, func(h Hit) bool {
		n++
		return n < 5
	}, nil)
	if err != nil || n != 5 {
		t.Fatalf("n %d err %v", n, err)
	}
	if err := FindAll(t.Context(), bytes.NewReader(data), 2000, Pattern{}, Options{}, nil, nil); !errors.Is(err, ErrEmptyPattern) {
		t.Fatalf("empty: %v", err)
	}
}

func TestFindAllProgress(t *testing.T) {
	data := make([]byte, 1000)
	var last int64
	err := FindAll(t.Context(), bytes.NewReader(data), 1000, Exact([]byte{1, 2}), Options{From: 100, ChunkSize: 64}, func(Hit) bool { return true }, func(done int64) {
		if done < last {
			t.Errorf("progress went back: %d after %d", done, last)
		}
		last = done
	})
	if err != nil || last != 900 {
		t.Fatalf("last %d err %v", last, err)
	}
}

// slowReader counts the reads and cancels after some
type slowReader struct {
	r      io.ReaderAt
	reads  atomic.Int64
	cancel func()
	after  int64
}

func (s *slowReader) ReadAt(p []byte, off int64) (int, error) {
	if s.reads.Add(1) == s.after {
		s.cancel()
	}
	return s.r.ReadAt(p, off)
}

func TestFindAllCancel(t *testing.T) {
	data := make([]byte, 1<<20)
	ctx, cancel := context.WithCancel(t.Context())
	sr := &slowReader{r: bytes.NewReader(data), cancel: cancel, after: 10}
	err := FindAll(ctx, sr, int64(len(data)), Exact([]byte{1}), Options{ChunkSize: 64, Workers: 4}, func(Hit) bool { return true }, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err %v", err)
	}
	if n := sr.reads.Load(); n > 100 {
		t.Fatalf("%d reads after the cancel", n)
	}
	err = Strings(ctx, bytes.NewReader(data), int64(len(data)), StringsOptions{ASCII: true}, func(Found) bool { return true }, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("strings err %v", err)
	}
}

type errReader struct{}

func (errReader) ReadAt(p []byte, off int64) (int, error) { return 0, errors.New("bad sector") }

func TestFindAllReadError(t *testing.T) {
	err := FindAll(t.Context(), errReader{}, 1000, Exact([]byte{1}), Options{}, func(Hit) bool { return true }, nil)
	if err == nil {
		t.Fatal("no error")
	}
}

func TestFindNext(t *testing.T) {
	rng := rand.New(rand.NewPCG(3, 4))
	for iter := range 500 {
		data := randomData(rng, 1+rng.IntN(3000))
		p := randomPattern(rng, append(data, make([]byte, 8)...))
		all := naiveFind(data, p, 0, int64(len(data)), 1)
		from := int64(rng.IntN(len(data) + 2))
		r := bytes.NewReader(data)

		h, ok, err := FindNext(t.Context(), r, int64(len(data)), p, from, false)
		i := slices.IndexFunc(all, func(h Hit) bool { return h.Off >= from })
		if err != nil || ok != (i >= 0) || ok && h != all[i] {
			t.Fatalf("iter %d forward from %d: %v %v %v, want index %d of %v", iter, from, h, ok, err, i, all)
		}

		h, ok, err = FindNext(t.Context(), r, int64(len(data)), p, from, true)
		i = -1
		for k, a := range all {
			if a.Off < from {
				i = k
			}
		}
		if err != nil || ok != (i >= 0) || ok && h != all[i] {
			t.Fatalf("iter %d backward from %d: %v %v %v, want index %d of %v", iter, from, h, ok, err, i, all)
		}
	}
}

// Backward over chunks: a match at the start of a big file is found, and a
// match just before from is found after reading little
func TestFindNextBackwardBig(t *testing.T) {
	data := make([]byte, 20<<20)
	copy(data[5:], "MZ")
	copy(data[len(data)-100:], "MZ")
	sr := &slowReader{r: bytes.NewReader(data), cancel: func() {}}
	p := Exact([]byte("MZ"))
	h, ok, err := FindNext(t.Context(), sr, int64(len(data)), p, int64(len(data)), true)
	if err != nil || !ok || h.Off != int64(len(data)-100) {
		t.Fatalf("%v %v %v", h, ok, err)
	}
	if n := sr.reads.Load(); n > 20 {
		t.Fatalf("%d reads", n)
	}
	h, ok, err = FindNext(t.Context(), sr, int64(len(data)), p, int64(len(data)-100), true)
	if err != nil || !ok || h.Off != 5 {
		t.Fatalf("%v %v %v", h, ok, err)
	}
}

func TestCollect(t *testing.T) {
	data := bytes.Repeat([]byte("x"), 100)
	hits, trunc, err := Collect(t.Context(), bytes.NewReader(data), 100, Exact([]byte("x")), Options{ChunkSize: 7}, 10, nil)
	if err != nil || !trunc || len(hits) != 10 || hits[9].Off != 9 {
		t.Fatalf("%v %v %v", hits, trunc, err)
	}
	hits, trunc, err = Collect(t.Context(), bytes.NewReader(data), 100, Exact([]byte("x")), Options{}, 100, nil)
	if err != nil || trunc || len(hits) != 100 {
		t.Fatalf("%d %v %v", len(hits), trunc, err)
	}
}
