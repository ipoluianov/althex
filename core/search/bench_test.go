package search

import (
	"bytes"
	"math/rand/v2"
	"runtime"
	"sync"
	"testing"
	"time"
)

var (
	benchOnce sync.Once
	benchData []byte
)

// randomBytes is 512 MB of random bytes, made once
func randomBytes() []byte {
	benchOnce.Do(func() {
		benchData = make([]byte, 512<<20)
		rand.NewChaCha8([32]byte{1}).Read(benchData)
	})
	return benchData
}

func benchFind(b *testing.B, p Pattern) {
	data := randomBytes()
	r := bytes.NewReader(data)
	b.SetBytes(int64(len(data)))
	b.ResetTimer()
	for b.Loop() {
		err := FindAll(b.Context(), r, int64(len(data)), p, Options{}, func(Hit) bool { return true }, nil)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkFindExact8(b *testing.B) {
	benchFind(b, Exact([]byte("AltHex!\x00")))
}

func BenchmarkFindMasked(b *testing.B) {
	p, err := ParseHex("4D 5A ?? ?? 50 45 0? 00")
	if err != nil {
		b.Fatal(err)
	}
	benchFind(b, p)
}

func BenchmarkFindCaseInsensitive(b *testing.B) {
	p, err := Text("althex", utf8Enc, true)
	if err != nil {
		b.Fatal(err)
	}
	benchFind(b, p)
}

func BenchmarkStrings(b *testing.B) {
	data := randomBytes()[:64<<20]
	r := bytes.NewReader(data)
	b.SetBytes(int64(len(data)))
	for b.Loop() {
		err := Strings(b.Context(), r, int64(len(data)), StringsOptions{ASCII: true, UTF8: true, UTF16LE: true}, func(Found) bool { return true }, nil)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// FIND-6/PERF-6: at least 1 GB/s for a plain pattern on data in memory
func TestFindSpeed(t *testing.T) {
	if raceEnabled {
		t.Skip("timings are not checked with the race detector")
	}
	if testing.Short() {
		t.Skip("-short")
	}
	if runtime.NumCPU() < 4 {
		t.Skip("fewer than 4 cores")
	}
	data := randomBytes()
	r := bytes.NewReader(data)
	p := Exact([]byte("AltHex!\x00"))
	best := time.Duration(1 << 62)
	for range 3 {
		start := time.Now()
		if err := FindAll(t.Context(), r, int64(len(data)), p, Options{}, func(Hit) bool { return true }, nil); err != nil {
			t.Fatal(err)
		}
		best = min(best, time.Since(start))
	}
	speed := float64(len(data)) / best.Seconds() / 1e9
	t.Logf("%.2f GB/s", speed)
	if speed < 1 {
		t.Fatalf("%.2f GB/s, want at least 1", speed)
	}
}
