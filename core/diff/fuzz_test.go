package diff

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/ipoluianov/althex/internal/fuzzguard"
)

// FuzzCompare compares any two inputs: the ranges cover both in order, the
// equal ones are equal, and the report is written
func FuzzCompare(f *testing.F) {
	f.Add([]byte("hello, world"), []byte("hello, brave new world"), uint8(1), uint8(0), uint8(4))
	f.Add([]byte("abcdefgh"), []byte("abXdefgh!"), uint8(0), uint8(2), uint8(0))
	f.Add(bytes.Repeat([]byte("0123456789"), 20), bytes.Repeat([]byte("01234x6789"), 21), uint8(1), uint8(3), uint8(8))
	f.Fuzz(func(t *testing.T, a, b []byte, shifts, gap, block uint8) {
		defer fuzzguard.Start(t.Name())()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		opt := Options{Shifts: shifts&1 != 0, MergeGap: int(gap % 16), BlockSize: int(block % 64), MaxRanges: int(shifts >> 1)}
		rs, err := Compare(ctx, bytes.NewReader(a), int64(len(a)), bytes.NewReader(b), int64(len(b)), opt, nil)
		if err != nil && !(errors.Is(err, ErrMaxRanges) && opt.MaxRanges > 0) {
			t.Fatal(err)
		}
		var ao, bo int64
		for _, r := range rs {
			if r.AOff != ao || r.BOff != bo || r.ALen < 0 || r.BLen < 0 || r.ALen+r.BLen == 0 {
				t.Fatalf("range %+v after A %d B %d (%+v)", r, ao, bo, opt)
			}
			if r.Kind == Equal && (r.ALen != r.BLen || !bytes.Equal(a[r.AOff:r.AEnd()], b[r.BOff:r.BEnd()])) {
				t.Fatalf("equal range %+v differs", r)
			}
			ao, bo = r.AEnd(), r.BEnd()
		}
		if opt.MaxRanges == 0 && (ao != int64(len(a)) || bo != int64(len(b))) {
			t.Fatalf("the ranges end at A %d B %d of %d, %d (%+v)", ao, bo, len(a), len(b), opt)
		}
		Summarize(rs)
		for off := range int64(len(a)) + 1 {
			NextDiff(rs, off)
			PrevDiff(rs, off)
		}
		if err := WriteReport(io.Discard, rs, bytes.NewReader(a), bytes.NewReader(b), int(gap)); err != nil {
			t.Fatal(err)
		}
	})
}
