package patch

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/ipoluianov/althex/internal/fuzzguard"
)

// fuzzApply feeds any bytes to Apply: it must give an error or a result,
// never panic or hang
func fuzzApply(f *testing.F, format Format) {
	src := []byte("The quick brown fox jumps over the lazy dog. 0123456789")
	dsts := [][]byte{
		[]byte("The quick red fox jumps over the lazy dog. 0123456789!"),
		[]byte("The quick brown fox"),
		bytes.Repeat([]byte{0xAA}, 100),
		nil,
	}
	for _, dst := range dsts {
		if format == IPS && len(dst) < len(src) && len(dst) > 0 {
			continue
		}
		f.Add(makePatch(f, format, src, dst))
	}
	f.Fuzz(func(t *testing.T, p []byte) {
		defer fuzzguard.Start(t.Name())()
		// A patch may claim a target of any size: the writing stops at a
		// limit, as on a full disk
		out := &limitedWriter{n: 16 << 20}
		Apply(context.Background(), format, bytes.NewReader(p), out, bytes.NewReader(src), int64(len(src)))
	})
}

// limitedWriter takes n bytes, then fails
type limitedWriter struct{ n int64 }

func (w *limitedWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > w.n {
		k := w.n
		w.n = 0
		return int(k), errFull
	}
	w.n -= int64(len(p))
	return len(p), nil
}

var errFull = errors.New("the disk is full")

func FuzzApplyIPS(f *testing.F)     { fuzzApply(f, IPS) }
func FuzzApplyBPS(f *testing.F)     { fuzzApply(f, BPS) }
func FuzzApplyAltHex(f *testing.F)  { fuzzApply(f, AltHex) }
func FuzzApplyHexDiff(f *testing.F) { fuzzApply(f, HexDiff) }
