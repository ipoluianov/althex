package patch

import (
	"bytes"
	"context"
	"testing"
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
		var out bytes.Buffer
		Apply(context.Background(), format, bytes.NewReader(p), &out, bytes.NewReader(src), int64(len(src)))
	})
}

func FuzzApplyIPS(f *testing.F)     { fuzzApply(f, IPS) }
func FuzzApplyBPS(f *testing.F)     { fuzzApply(f, BPS) }
func FuzzApplyAltHex(f *testing.F)  { fuzzApply(f, AltHex) }
func FuzzApplyHexDiff(f *testing.F) { fuzzApply(f, HexDiff) }
