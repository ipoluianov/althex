package diff

import (
	"bytes"
	"context"
	"math/rand/v2"
	"testing"
)

func benchCompare(b *testing.B, size int, opt Options) {
	rng := rand.New(rand.NewPCG(9, 10))
	x := randBytes(rng, size)
	y := append([]byte(nil), x...)
	if opt.Shifts {
		y = mutate(rng, x, 1000, 1000)
	} else {
		for range 1000 {
			y[rng.IntN(size)]++
		}
	}
	b.SetBytes(int64(size))
	b.ResetTimer()
	for b.Loop() {
		if _, err := Compare(context.Background(), bytes.NewReader(x), int64(len(x)), bytes.NewReader(y), int64(len(y)), opt, nil); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkCompareBytes compares 256 MB byte by byte
func BenchmarkCompareBytes(b *testing.B) { benchCompare(b, 256<<20, Options{}) }

// BenchmarkCompareShifts compares 64 MB with 1000 random edits
func BenchmarkCompareShifts(b *testing.B) { benchCompare(b, 64<<20, Options{Shifts: true}) }
