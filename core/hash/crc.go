package checksum

import (
	"encoding/binary"
	"fmt"
	gohash "hash"
	"math/bits"
	"strconv"
	"strings"
)

// CRCParams is a CRC in the Rocksoft model, as the CRC catalogue
// (reveng.sourceforge.io/crc-catalogue) writes them
type CRCParams struct {
	Name   string
	Width  int // 1..64
	Poly   uint64
	Init   uint64
	RefIn  bool
	RefOut bool
	XorOut uint64
	Check  uint64 // the CRC of "123456789", to verify the parameters
}

// CRCPresets returns the common CRCs with their catalogue check values
func CRCPresets() []CRCParams {
	return []CRCParams{
		{Name: "CRC-8", Width: 8, Poly: 0x07, Check: 0xF4},
		{Name: "CRC-8/MAXIM", Width: 8, Poly: 0x31, RefIn: true, RefOut: true, Check: 0xA1},
		{Name: "CRC-16/ARC", Width: 16, Poly: 0x8005, RefIn: true, RefOut: true, Check: 0xBB3D},
		{Name: "CRC-16/MODBUS", Width: 16, Poly: 0x8005, Init: 0xFFFF, RefIn: true, RefOut: true, Check: 0x4B37},
		{Name: "CRC-16/USB", Width: 16, Poly: 0x8005, Init: 0xFFFF, RefIn: true, RefOut: true, XorOut: 0xFFFF, Check: 0xB4C8},
		{Name: "CRC-16/XMODEM", Width: 16, Poly: 0x1021, Check: 0x31C3},
		{Name: "CRC-16/CCITT-FALSE", Width: 16, Poly: 0x1021, Init: 0xFFFF, Check: 0x29B1},
		{Name: "CRC-16/KERMIT", Width: 16, Poly: 0x1021, RefIn: true, RefOut: true, Check: 0x2189},
		{Name: "CRC-16/X-25", Width: 16, Poly: 0x1021, Init: 0xFFFF, RefIn: true, RefOut: true, XorOut: 0xFFFF, Check: 0x906E},
		{Name: "CRC-24/OPENPGP", Width: 24, Poly: 0x864CFB, Init: 0xB704CE, Check: 0x21CF02},
		{Name: "CRC-32", Width: 32, Poly: 0x04C11DB7, Init: 0xFFFFFFFF, RefIn: true, RefOut: true, XorOut: 0xFFFFFFFF, Check: 0xCBF43926},
		{Name: "CRC-32C", Width: 32, Poly: 0x1EDC6F41, Init: 0xFFFFFFFF, RefIn: true, RefOut: true, XorOut: 0xFFFFFFFF, Check: 0xE3069283},
		{Name: "CRC-32/BZIP2", Width: 32, Poly: 0x04C11DB7, Init: 0xFFFFFFFF, XorOut: 0xFFFFFFFF, Check: 0xFC891918},
		{Name: "CRC-32/MPEG-2", Width: 32, Poly: 0x04C11DB7, Init: 0xFFFFFFFF, Check: 0x0376E6E7},
		{Name: "CRC-64/ECMA-182", Width: 64, Poly: 0x42F0E1EBA9EA3693, Check: 0x6C40DF5F0B497347},
		{Name: "CRC-64/XZ", Width: 64, Poly: 0x42F0E1EBA9EA3693, Init: ^uint64(0), RefIn: true, RefOut: true, XorOut: ^uint64(0), Check: 0x995DC9BBDF1939FA},
		{Name: "CRC-64/GO-ISO", Width: 64, Poly: 0x1B, Init: ^uint64(0), RefIn: true, RefOut: true, XorOut: ^uint64(0), Check: 0xB90956C775A41001},
	}
}

// CRCPreset finds a preset by its name
func CRCPreset(name string) (CRCParams, bool) {
	for _, p := range CRCPresets() {
		if p.Name == name {
			return p, true
		}
	}
	return CRCParams{}, false
}

// crcTables are the slicing-by-8 tables of one CRC; t[0] is the usual
// byte table, t[k] moves a byte k more bytes ahead
type crcTables [8][256]uint64

// NewCRC makes a table driven CRC. Sum appends the CRC big-endian in
// ceil(Width/8) bytes; the result is also a gohash.Hash64 (Sum64 gives the
// CRC as a number). A Width out of 1..64 is taken as 64.
func NewCRC(p CRCParams) gohash.Hash {
	if p.Width < 1 || p.Width > 64 {
		p.Width = 64
	}
	c := &crc{p: p, mask: ^uint64(0) >> (64 - p.Width)}
	c.p.Poly &= c.mask
	// The register is kept so that a byte is always taken from one end:
	// the low byte for a reflected CRC, the top byte (the register shifted
	// to the top of 64 bits) otherwise. Then any width uses the same code.
	t := &c.t
	if p.RefIn {
		poly := reflect(c.p.Poly, p.Width)
		for i := range 256 {
			r := uint64(i)
			for range 8 {
				if r&1 != 0 {
					r = r>>1 ^ poly
				} else {
					r >>= 1
				}
			}
			t[0][i] = r
		}
		for k := 1; k < 8; k++ {
			for i := range 256 {
				r := t[k-1][i]
				t[k][i] = r>>8 ^ t[0][byte(r)]
			}
		}
	} else {
		poly := c.p.Poly << (64 - p.Width)
		for i := range 256 {
			r := uint64(i) << 56
			for range 8 {
				if r&(1<<63) != 0 {
					r = r<<1 ^ poly
				} else {
					r <<= 1
				}
			}
			t[0][i] = r
		}
		for k := 1; k < 8; k++ {
			for i := range 256 {
				r := t[k-1][i]
				t[k][i] = r<<8 ^ t[0][r>>56]
			}
		}
	}
	c.Reset()
	return c
}

type crc struct {
	p    CRCParams
	mask uint64
	reg  uint64
	t    crcTables
}

func reflect(v uint64, width int) uint64 {
	return bits.Reverse64(v) >> (64 - width)
}

func (c *crc) Reset() {
	init := c.p.Init & c.mask
	if c.p.RefIn {
		c.reg = reflect(init, c.p.Width)
	} else {
		c.reg = init << (64 - c.p.Width)
	}
}

func (c *crc) Size() int      { return (c.p.Width + 7) / 8 }
func (c *crc) BlockSize() int { return 1 }

func (c *crc) Write(b []byte) (int, error) {
	n := len(b)
	r := c.reg
	t := &c.t
	// Eight bytes at a time: a CRC is linear, so xoring the eight bytes in
	// at once and then shifting them out is the same as one by one
	if c.p.RefIn {
		for len(b) >= 8 {
			r ^= binary.LittleEndian.Uint64(b)
			r = t[7][byte(r)] ^ t[6][byte(r>>8)] ^ t[5][byte(r>>16)] ^ t[4][byte(r>>24)] ^
				t[3][byte(r>>32)] ^ t[2][byte(r>>40)] ^ t[1][byte(r>>48)] ^ t[0][r>>56]
			b = b[8:]
		}
		for _, v := range b {
			r = t[0][byte(r)^v] ^ r>>8
		}
	} else {
		for len(b) >= 8 {
			r ^= binary.BigEndian.Uint64(b)
			r = t[7][r>>56] ^ t[6][byte(r>>48)] ^ t[5][byte(r>>40)] ^ t[4][byte(r>>32)] ^
				t[3][byte(r>>24)] ^ t[2][byte(r>>16)] ^ t[1][byte(r>>8)] ^ t[0][byte(r)]
			b = b[8:]
		}
		for _, v := range b {
			r = t[0][byte(r>>56)^v] ^ r<<8
		}
	}
	c.reg = r
	return n, nil
}

// Sum64 returns the CRC
func (c *crc) Sum64() uint64 {
	v := c.reg
	if !c.p.RefIn {
		v >>= 64 - c.p.Width
	}
	// The register is reflected when RefIn; RefOut wants it the other way
	if c.p.RefIn != c.p.RefOut {
		v = reflect(v, c.p.Width)
	}
	return (v ^ c.p.XorOut) & c.mask
}

func (c *crc) Sum(b []byte) []byte {
	v := c.Sum64()
	for i := c.Size() - 1; i >= 0; i-- {
		b = append(b, byte(v>>(8*i)))
	}
	return b
}

// ParseCRC reads the parameters of a CRC as "width=16 poly=0x1021
// init=0xFFFF refin=false refout=false xorout=0" (the Rocksoft model;
// check, residue and name are taken and ignored)
func ParseCRC(s string) (CRCParams, error) {
	p := CRCParams{Name: "CRC (" + strings.TrimSpace(s) + ")"}
	seen := false
	for _, f := range strings.Fields(s) {
		k, v, ok := strings.Cut(strings.ToLower(f), "=")
		if !ok {
			return p, fmt.Errorf("%q: name=value expected", f)
		}
		num := func() (uint64, error) { return strconv.ParseUint(v, 0, 64) }
		var err error
		var n uint64
		switch k {
		case "width":
			n, err = num()
			p.Width = int(n)
			seen = true
		case "poly":
			p.Poly, err = num()
		case "init":
			p.Init, err = num()
		case "xorout":
			p.XorOut, err = num()
		case "refin":
			p.RefIn, err = strconv.ParseBool(v)
		case "refout":
			p.RefOut, err = strconv.ParseBool(v)
		case "check", "residue", "name":
		default:
			return p, fmt.Errorf("%q: unknown parameter", k)
		}
		if err != nil {
			return p, fmt.Errorf("%s: %v", k, err)
		}
	}
	if !seen || p.Width < 1 || p.Width > 64 {
		return p, fmt.Errorf("width=1..64 expected")
	}
	return p, nil
}
