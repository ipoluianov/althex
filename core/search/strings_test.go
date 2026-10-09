package search

import (
	"bytes"
	"cmp"
	"math/rand/v2"
	"slices"
	"testing"
	"unicode/utf8"
)

// naiveStrings parses the range from From to the end in one go
func naiveStrings(data []byte, opt StringsOptions) []Found {
	from, to := span(opt.From, opt.To, int64(len(data)))
	d := data[:to]
	minLen := opt.MinLen
	if minLen <= 0 {
		minLen = 4
	}
	var out []Found
	add := func(start, end int64, enc string, runes []rune) {
		if len(runes) >= minLen {
			out = append(out, Found{Off: start, Len: int(end - start), Enc: enc, Text: string(runes[:min(len(runes), MaxStringText)])})
		}
	}
	if opt.ASCII || opt.UTF8 {
		var runes []rune
		start, ascii := int64(-1), true
		end := func(i int64) {
			if start >= 0 {
				enc := EncASCII
				if opt.UTF8 && !(opt.ASCII && ascii) {
					enc = EncUTF8
				}
				add(start, i, enc, runes)
			}
			start, runes, ascii = -1, nil, true
		}
		for i := from; i < to; {
			r, size := rune(d[i]), 1
			ok := d[i] < 0x80 && (d[i] >= 0x20 && d[i] < 0x7f || d[i] == '\t')
			if opt.UTF8 && d[i] >= 0x80 {
				r, size = utf8.DecodeRune(d[i:])
				ok = size > 1 && printableRune(r)
			}
			if ok {
				if start < 0 {
					start = i
				}
				runes = append(runes, r)
				ascii = ascii && r < 0x80
			} else {
				end(i)
			}
			i += int64(size)
		}
		end(to)
	}
	if opt.UTF16LE {
		for parity := int64(0); parity < 2; parity++ {
			var runes []rune
			start := int64(-1)
			i := from
			if i%2 != parity {
				i++
			}
			for ; i+2 <= to; i += 2 {
				u := rune(d[i]) | rune(d[i+1])<<8
				if u < 0x800 && printableRune(u) {
					if start < 0 {
						start = i
					}
					runes = append(runes, u)
				} else {
					if start >= 0 {
						add(start, i, EncUTF16LE, runes)
					}
					start, runes = -1, nil
				}
			}
			if start >= 0 {
				add(start, i, EncUTF16LE, runes)
			}
		}
	}
	slices.SortStableFunc(out, func(a, b Found) int { return cmp.Compare(a.Off, b.Off) })
	return out
}

func stringsOf(t *testing.T, data []byte, opt StringsOptions) []Found {
	t.Helper()
	var out []Found
	err := Strings(t.Context(), bytes.NewReader(data), int64(len(data)), opt, func(f Found) bool {
		out = append(out, f)
		return true
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// stringData has ASCII, UTF-8, UTF-16LE strings and noise mixed
func stringData(rng *rand.Rand, n int) []byte {
	var b []byte
	for len(b) < n {
		switch rng.IntN(6) {
		case 0:
			for range rng.IntN(12) {
				b = append(b, byte(0x20+rng.IntN(95)))
			}
		case 1:
			for range rng.IntN(8) {
				b = utf8.AppendRune(b, []rune("жЖé€\t😀a")[rng.IntN(7)])
			}
		case 2:
			for range rng.IntN(8) {
				r := []rune("aZ ж\tΩ")[rng.IntN(6)]
				b = append(b, byte(r), byte(r>>8))
			}
		case 3:
			for range rng.IntN(4) {
				b = append(b, byte(rng.IntN(256)))
			}
		case 4:
			b = append(b, 0)
		case 5:
			// The middle of UTF-8 characters
			b = append(b, 0x80|byte(rng.IntN(64)), 0xd0)
		}
	}
	return b[:n]
}

func TestStringsRandom(t *testing.T) {
	rng := rand.New(rand.NewPCG(5, 6))
	for iter := range 3000 {
		data := stringData(rng, 1+rng.IntN(500))
		opt := StringsOptions{
			MinLen:    rng.IntN(6),
			ASCII:     rng.IntN(2) == 0,
			UTF8:      rng.IntN(2) == 0,
			UTF16LE:   rng.IntN(2) == 0,
			ChunkSize: 1 + rng.IntN(64),
			Workers:   1 + rng.IntN(8),
		}
		if rng.IntN(3) == 0 {
			opt.From = int64(rng.IntN(len(data)))
			opt.To = opt.From + int64(rng.IntN(len(data)-int(opt.From)+1))
		}
		want := naiveStrings(data, opt)
		got := stringsOf(t, data, opt)
		if !slices.Equal(got, want) {
			t.Fatalf("iter %d: opt %+v data %q:\n got %+v\nwant %+v", iter, opt, data, got, want)
		}
	}
}

// A string over many chunks is found once, by the chunk it starts in, and
// its text is capped
func TestStringsLong(t *testing.T) {
	data := append([]byte{0}, bytes.Repeat([]byte("abc"), 100000)...)
	data = append(data, 0, 'x', 'y', 'z', 'w')
	got := stringsOf(t, data, StringsOptions{ASCII: true, ChunkSize: 1000, Workers: 4})
	if len(got) != 2 || got[0].Off != 1 || got[0].Len != 300000 || utf8.RuneCountInString(got[0].Text) != MaxStringText || got[1].Text != "xyzw" {
		t.Fatalf("%d found, %v", len(got), got[min(len(got)-1, 1)])
	}
}

func TestStringsKinds(t *testing.T) {
	data := []byte("\x00hello\x00привет\x00w\x00o\x00r\x00d\x00\x00")
	got := stringsOf(t, data, StringsOptions{ASCII: true, UTF8: true, UTF16LE: true, ChunkSize: 3})
	want := []Found{
		{1, 5, EncASCII, "hello"},
		{7, 12, EncUTF8, "привет"},
		{20, 8, EncUTF16LE, "word"},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("got %+v", got)
	}
}

func TestStringsStop(t *testing.T) {
	data := bytes.Repeat([]byte("abcd\x00"), 100)
	n := 0
	err := Strings(t.Context(), bytes.NewReader(data), int64(len(data)), StringsOptions{ASCII: true, ChunkSize: 7}, func(Found) bool {
		n++
		return n < 3
	}, nil)
	if err != nil || n != 3 {
		t.Fatalf("n %d err %v", n, err)
	}
}

// Long UTF-8 and UTF-16 strings go over many window pieces
func TestStringsLongMixed(t *testing.T) {
	rng := rand.New(rand.NewPCG(7, 8))
	var data []byte
	for range 20 {
		data = append(data, stringData(rng, 1000)...)
		for range 30000 + rng.IntN(1000) {
			data = utf8.AppendRune(data, []rune("жé😀a")[rng.IntN(4)])
		}
		for range 30000 {
			data = append(data, 'Q', 0)
		}
	}
	for _, cs := range []int{100, 4097, 1 << 20} {
		opt := StringsOptions{ASCII: true, UTF8: true, UTF16LE: true, ChunkSize: cs}
		want := naiveStrings(data, opt)
		got := stringsOf(t, data, opt)
		if !slices.Equal(got, want) {
			t.Fatalf("chunk %d: %d found, want %d", cs, len(got), len(want))
		}
	}
}
