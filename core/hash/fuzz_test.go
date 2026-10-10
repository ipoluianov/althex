package checksum

import (
	"testing"

	"github.com/ipoluianov/althex/internal/fuzzguard"
)

// FuzzCRC makes a CRC of any parameters (the user types them, templates
// pass them) and checks it against the bit by bit definition
func FuzzCRC(f *testing.F) {
	for _, p := range CRCPresets() {
		f.Add(p.Width, p.Poly, p.Init, p.RefIn, p.RefOut, p.XorOut, []byte("123456789"), uint8(3))
	}
	f.Add(0, uint64(0), uint64(0), false, false, uint64(0), []byte{}, uint8(0))
	f.Add(-5, uint64(1), ^uint64(0), true, false, uint64(7), []byte{0xFF}, uint8(1))
	f.Fuzz(func(t *testing.T, width int, poly, init uint64, refIn, refOut bool, xorOut uint64, data []byte, split uint8) {
		defer fuzzguard.Start(t.Name())()
		p := CRCParams{Width: width, Poly: poly, Init: init, RefIn: refIn, RefOut: refOut, XorOut: xorOut}
		h := NewCRC(p)
		// In two writes: the tables and the tails of slicing by 8
		cut := int(split) % (len(data) + 1)
		h.Write(data[:cut])
		h.Write(data[cut:])
		w := width
		if w < 1 || w > 64 {
			w = 64
		}
		sum := h.Sum(nil)
		if len(sum) != (w+7)/8 || h.Size() != len(sum) {
			t.Fatalf("width %d: %d bytes of sum, Size %d", width, len(sum), h.Size())
		}
		var got uint64
		for _, b := range sum {
			got = got<<8 | uint64(b)
		}
		if want := crcBitwise(w, poly, init, refIn, refOut, xorOut, data); got != want {
			t.Fatalf("%+v of % x: 0x%X, want 0x%X", p, data, got, want)
		}
		CRCAlgorithm(p).New().Write(data)
	})
}

// crcBitwise is the Rocksoft model computed one bit at a time
func crcBitwise(width int, poly, init uint64, refIn, refOut bool, xorOut uint64, data []byte) uint64 {
	mask := ^uint64(0) >> (64 - width)
	top := uint64(1) << (width - 1)
	reg := init & mask
	poly &= mask
	for _, b := range data {
		for i := range 8 {
			bit := b >> (7 - i) & 1
			if refIn {
				bit = b >> i & 1
			}
			msb := reg&top != 0
			reg = reg << 1 & mask
			if msb != (bit != 0) {
				reg ^= poly
			}
		}
	}
	if refOut {
		var r uint64
		for i := range width {
			if reg>>i&1 != 0 {
				r |= 1 << (width - 1 - i)
			}
		}
		reg = r
	}
	return (reg ^ xorOut) & mask
}
