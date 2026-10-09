package diff

import (
	"bytes"
	"context"
	"errors"
	"math/rand/v2"
	"strings"
	"testing"
)

// mutate makes a copy of src with random inserts, deletes and overwrites
func mutate(rng *rand.Rand, src []byte, edits, maxLen int) []byte {
	dst := append([]byte(nil), src...)
	for range edits {
		n := 1 + rng.IntN(maxLen)
		off := 0
		if len(dst) > 0 {
			off = rng.IntN(len(dst))
		}
		switch rng.IntN(3) {
		case 0:
			ins := randBytes(rng, n)
			dst = append(dst[:off], append(ins, dst[off:]...)...)
		case 1:
			dst = append(dst[:off], dst[min(off+n, len(dst)):]...)
		case 2:
			for i := off; i < min(off+n, len(dst)); i++ {
				dst[i] = byte(rng.Uint32())
			}
		}
	}
	return dst
}

func randBytes(rng *rand.Rand, n int) []byte {
	p := make([]byte, n)
	for i := range p {
		p[i] = byte(rng.Uint32())
	}
	return p
}

// check tells whether the ranges are an edit script of a into b
func check(t *testing.T, rs []Range, a, b []byte, shifts bool) {
	t.Helper()
	var ao, bo int64
	var out []byte
	for i, r := range rs {
		if r.AOff != ao || r.BOff != bo {
			t.Fatalf("range %d %+v does not follow %d/%d", i, r, ao, bo)
		}
		if r.ALen == 0 && r.BLen == 0 {
			t.Fatalf("empty range %d", i)
		}
		if r.Kind != Equal && r.Kind != kindOf(r.ALen, r.BLen) {
			t.Fatalf("range %d %+v has a wrong kind", i, r)
		}
		x, y := a[r.AOff:r.AEnd()], b[r.BOff:r.BEnd()]
		if r.Kind == Equal {
			if !bytes.Equal(x, y) {
				t.Fatalf("equal range %d %+v differs", i, r)
			}
			out = append(out, x...)
		} else {
			if !shifts && r.Kind == Changed && r.AOff != r.BOff {
				t.Fatalf("shifted range %d %+v byte by byte", i, r)
			}
			out = append(out, y...)
		}
		ao, bo = r.AEnd(), r.BEnd()
	}
	if ao != int64(len(a)) || bo != int64(len(b)) {
		t.Fatalf("ranges end at %d/%d, sizes %d/%d", ao, bo, len(a), len(b))
	}
	if !bytes.Equal(out, b) {
		t.Fatal("the ranges do not make B")
	}
}

func compare(t *testing.T, a, b []byte, opt Options) []Range {
	t.Helper()
	rs, err := Compare(context.Background(), bytes.NewReader(a), int64(len(a)), bytes.NewReader(b), int64(len(b)), opt, nil)
	if err != nil {
		t.Fatal(err)
	}
	check(t, rs, a, b, opt.Shifts)
	return rs
}

func TestBytewise(t *testing.T) {
	a := []byte("hello, world! 0123456789")
	b := []byte("hellO, world? 0123456789xyz")
	rs := compare(t, a, b, Options{})
	want := []Range{
		{Equal, 0, 4, 0, 4},
		{Changed, 4, 1, 4, 1},
		{Equal, 5, 7, 5, 7},
		{Changed, 12, 1, 12, 1},
		{Equal, 13, 11, 13, 11},
		{Inserted, 24, 0, 24, 3},
	}
	if len(rs) != len(want) {
		t.Fatalf("got %+v", rs)
	}
	for i := range rs {
		if rs[i] != want[i] {
			t.Fatalf("range %d: got %+v want %+v", i, rs[i], want[i])
		}
	}
	rs = compare(t, b, a, Options{MergeGap: 8})
	if s := Summarize(rs); s.Ranges != 2 || s.Changed != 9 || s.Deleted != 3 {
		t.Fatalf("summary %+v", s)
	}
}

func TestBytewiseRandom(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	for i := range 20 {
		a := randBytes(rng, rng.IntN(8<<20))
		b := mutate(rng, a, rng.IntN(50), 5000)
		if i%3 == 0 {
			// overwrites only, so most of it is in place
			b = append([]byte(nil), a...)
			for range 30 {
				o := rng.IntN(len(b) + 1)
				for j := o; j < min(len(b), o+rng.IntN(100)); j++ {
					b[j]++
				}
			}
		}
		compare(t, a, b, Options{MergeGap: rng.IntN(3) * 10})
	}
}

func TestShifts(t *testing.T) {
	a := []byte(strings.Repeat("The quick brown fox jumps over the lazy dog. ", 20))
	b := append([]byte("NEW"), a[:100]...)
	b = append(b, a[110:]...)
	rs := compare(t, a, b, Options{Shifts: true})
	s := Summarize(rs)
	if s.Inserted != 3 || s.Deleted != 10 || s.Changed != 0 {
		t.Fatalf("summary %+v %+v", s, rs)
	}
}

func TestShiftsRandom(t *testing.T) {
	rng := rand.New(rand.NewPCG(3, 4))
	for i := range 30 {
		var a []byte
		switch i % 3 {
		case 0:
			a = randBytes(rng, rng.IntN(4<<20))
		case 1: // repeated data
			a = bytes.Repeat(randBytes(rng, 1+rng.IntN(200)), 1+rng.IntN(10000))
		case 2:
			a = make([]byte, rng.IntN(1<<20))
		}
		b := mutate(rng, a, rng.IntN(60), 1+rng.IntN(20000))
		rs := compare(t, a, b, Options{Shifts: true, MergeGap: rng.IntN(2) * 16, BlockSize: []int{0, 8, 64}[rng.IntN(3)]})
		if i%3 == 0 {
			// random data with few edits: almost all of it is found
			if s := Summarize(rs); s.Equal < int64(len(a))/2 && len(a) > 1<<20 {
				t.Fatalf("too little found equal: %+v of %d", s, len(a))
			}
		}
	}
}

func TestShiftsSmallEdits(t *testing.T) {
	rng := rand.New(rand.NewPCG(5, 6))
	a := randBytes(rng, 100000)
	b := mutate(rng, a, 10, 20)
	rs := compare(t, a, b, Options{Shifts: true})
	s := Summarize(rs)
	if s.Changed+s.Inserted+s.Deleted > 400 {
		t.Fatalf("the edits are not found precisely: %+v", s)
	}
}

func TestMyers(t *testing.T) {
	rng := rand.New(rand.NewPCG(7, 8))
	for range 2000 {
		x := randBytes(rng, rng.IntN(40))
		for i := range x {
			x[i] %= 4
		}
		y := mutate(rng, x, rng.IntN(5), 5)
		for i := range y {
			y[i] %= 4
		}
		ops, ok := myers(x, y, 1000, new([]int))
		if !ok {
			t.Fatal("no script")
		}
		var out []byte
		xi, yi, d := 0, 0, 0
		for _, o := range ops {
			switch o.kind {
			case Equal:
				if !bytes.Equal(x[xi:xi+o.n], y[yi:yi+o.n]) {
					t.Fatal("not equal")
				}
				out = append(out, x[xi:xi+o.n]...)
				xi += o.n
				yi += o.n
			case Deleted:
				xi++
				d++
			case Inserted:
				out = append(out, y[yi])
				yi++
				d++
			}
		}
		if xi != len(x) || !bytes.Equal(out, y) {
			t.Fatalf("bad script %v for %v -> %v", ops, x, y)
		}
		if lcs := lcsLen(x, y); d != len(x)+len(y)-2*lcs {
			t.Fatalf("not the shortest: %d", d)
		}
	}
	if _, ok := myers(make([]byte, 100), bytes.Repeat([]byte{1}, 100), 10, new([]int)); ok {
		t.Fatal("the limit is not kept")
	}
}

func lcsLen(x, y []byte) int {
	dp := make([][]int, len(x)+1)
	for i := range dp {
		dp[i] = make([]int, len(y)+1)
	}
	for i := 1; i <= len(x); i++ {
		for j := 1; j <= len(y); j++ {
			if x[i-1] == y[j-1] {
				dp[i][j] = dp[i-1][j-1] + 1
			} else {
				dp[i][j] = max(dp[i-1][j], dp[i][j-1])
			}
		}
	}
	return dp[len(x)][len(y)]
}

func TestMaxRanges(t *testing.T) {
	a := make([]byte, 1000)
	b := make([]byte, 1000)
	for i := 0; i < 1000; i += 10 {
		b[i] = 1
	}
	for _, shifts := range []bool{false, true} {
		rs, err := Compare(context.Background(), bytes.NewReader(a), 1000, bytes.NewReader(b), 1000, Options{Shifts: shifts, MaxRanges: 5}, nil)
		if !errors.Is(err, ErrMaxRanges) {
			t.Fatal(err)
		}
		if s := Summarize(rs); s.Ranges != 5 {
			t.Fatalf("shifts %v: %d ranges", shifts, s.Ranges)
		}
	}
}

func TestCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	a := make([]byte, 10<<20)
	for _, shifts := range []bool{false, true} {
		_, err := Compare(ctx, bytes.NewReader(a), int64(len(a)), bytes.NewReader(a), int64(len(a)), Options{Shifts: shifts}, nil)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("shifts %v: %v", shifts, err)
		}
	}
}

func TestProgress(t *testing.T) {
	a := make([]byte, 5<<20)
	b := make([]byte, 5<<20)
	for _, shifts := range []bool{false, true} {
		last := int64(-1)
		_, err := Compare(context.Background(), bytes.NewReader(a), int64(len(a)), bytes.NewReader(b), int64(len(b)), Options{Shifts: shifts}, func(done int64) {
			if done < last || done > int64(len(a)) {
				t.Fatalf("progress %d after %d", done, last)
			}
			last = done
		})
		if err != nil || last != int64(len(a)) {
			t.Fatal(err, last)
		}
	}
}

func TestNavigation(t *testing.T) {
	rs := []Range{
		{Equal, 0, 10, 0, 10},
		{Changed, 10, 2, 10, 2},
		{Equal, 12, 10, 12, 10},
		{Inserted, 22, 0, 22, 5},
		{Equal, 22, 10, 27, 10},
		{Deleted, 32, 3, 37, 0},
	}
	cases := []struct {
		f    func([]Range, int64) int
		off  int64
		want int
	}{
		{NextDiff, 0, 1}, {NextDiff, 10, 3}, {NextDiff, 22, 5}, {NextDiff, 32, -1},
		{PrevDiff, 40, 5}, {PrevDiff, 32, 3}, {PrevDiff, 22, 1}, {PrevDiff, 10, -1},
		{NextDiffB, 22, 5}, {PrevDiffB, 37, 3},
	}
	for i, c := range cases {
		if got := c.f(rs, c.off); got != c.want {
			t.Errorf("case %d: got %d want %d", i, got, c.want)
		}
	}
}

func TestReport(t *testing.T) {
	a := []byte("abcdefgh")
	b := []byte("abXdefghij")
	rs := compare(t, a, b, Options{})
	var sb strings.Builder
	if err := WriteReport(&sb, rs, bytes.NewReader(a), bytes.NewReader(b), 1); err != nil {
		t.Fatal(err)
	}
	r := sb.String()
	for _, w := range []string{"Differing ranges: 2", "#1 changed A 0x00000002+1 B 0x00000002+1", "A: 63", "B: 58", "#2 inserted", "B: 69 ..."} {
		if !strings.Contains(r, w) {
			t.Fatalf("no %q in\n%s", w, r)
		}
	}
}
