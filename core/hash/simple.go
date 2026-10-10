package checksum

import (
	"encoding/binary"
	gohash "hash"
)

// simpleSum is the XOR or the sum of the bytes, of the width of the result:
// the checksums of many firmware formats and protocols. Sum64 returns it.
type simpleSum struct {
	xor   bool
	width int // bytes of the result: 1, 2 or 4
	v     uint64
	init  uint64
}

func newSimple(xor bool, width int, init uint64) func() gohash.Hash {
	return func() gohash.Hash { return &simpleSum{xor: xor, width: width, v: init, init: init} }
}

func (s *simpleSum) Write(b []byte) (int, error) {
	if s.xor {
		x := byte(s.v)
		for _, c := range b {
			x ^= c
		}
		s.v = uint64(x)
		return len(b), nil
	}
	for _, c := range b {
		s.v += uint64(c)
	}
	return len(b), nil
}

func (s *simpleSum) mask() uint64   { return 1<<(uint(s.width)*8) - 1 }
func (s *simpleSum) Sum64() uint64  { return s.v & s.mask() }
func (s *simpleSum) Reset()         { s.v = s.init }
func (s *simpleSum) Size() int      { return s.width }
func (s *simpleSum) BlockSize() int { return 1 }

func (s *simpleSum) Sum(b []byte) []byte {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], s.Sum64())
	return append(b, buf[8-s.width:]...)
}
