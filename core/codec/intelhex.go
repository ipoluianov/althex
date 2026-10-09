package codec

import (
	"errors"
	"fmt"
	"io"
)

// Intel HEX record types
const (
	ihexData         = 0x00
	ihexEOF          = 0x01
	ihexSegment      = 0x02 // extended segment address: base = value * 16
	ihexStartSegment = 0x03 // CS:IP of the entry point, not kept
	ihexLinear       = 0x04 // extended linear address: base = value << 16
	ihexStartLinear  = 0x05 // EIP of the entry point, not kept
)

// ParseIntelHex reads an Intel HEX file. The data is returned sorted by
// address, contiguous records merged. Records after the end-of-file record
// are ignored; a file without one is an error, as it is likely cut.
// Addresses do not wrap at 64K: a record that crosses the end of its
// segment goes on into the next one, as most tools write it.
func ParseIntelHex(r io.Reader) ([]Segment, error) {
	lr := newLineReader(r)
	var b segBuilder
	var base uint64
	for {
		s, more := lr.next()
		if !more {
			break
		}
		if s == "" {
			continue
		}
		n := lr.line
		if s[0] != ':' {
			return nil, fmt.Errorf("line %d: a record must start with ':'", n)
		}
		rec, err := decodeHexBytes(s[1:], n)
		if err != nil {
			return nil, err
		}
		if len(rec) < 5 || len(rec) != 5+int(rec[0]) {
			return nil, fmt.Errorf("line %d: wrong record length", n)
		}
		var sum byte
		for _, c := range rec {
			sum += c
		}
		if sum != 0 {
			want := rec[len(rec)-1] - sum
			return nil, fmt.Errorf("line %d: checksum is %02X, should be %02X", n, rec[len(rec)-1], want)
		}
		off := uint64(rec[1])<<8 | uint64(rec[2])
		data := rec[4 : len(rec)-1]
		wantLen := -1
		switch rec[3] {
		case ihexData:
			if err := b.add(base+off, data, n); err != nil {
				return nil, err
			}
		case ihexEOF:
			if len(data) != 0 {
				return nil, fmt.Errorf("line %d: end-of-file record with data", n)
			}
			if err := lr.err(); err != nil {
				return nil, err
			}
			return b.result()
		case ihexSegment:
			wantLen = 2
			if len(data) == 2 {
				base = (uint64(data[0])<<8 | uint64(data[1])) << 4
			}
		case ihexLinear:
			wantLen = 2
			if len(data) == 2 {
				base = (uint64(data[0])<<8 | uint64(data[1])) << 16
			}
		case ihexStartSegment, ihexStartLinear:
			wantLen = 4
		default:
			return nil, fmt.Errorf("line %d: unknown record type %02X", n, rec[3])
		}
		if wantLen >= 0 && len(data) != wantLen {
			return nil, fmt.Errorf("line %d: record type %02X must have %d bytes of data", n, rec[3], wantLen)
		}
	}
	if err := lr.err(); err != nil {
		return nil, err
	}
	return nil, errors.New("no end-of-file record: the file may be cut")
}

// WriteIntelHex writes the segments as Intel HEX: data records of
// recordLen bytes (0 - 16, at most 255), an extended linear address record
// where the upper 16 bits of the address change, the end-of-file record
// last. Records do not cross 64K boundaries, so they read the same in tools
// that wrap addresses and in those that do not.
func WriteIntelHex(w io.Writer, segs []Segment, recordLen int) error {
	if recordLen == 0 {
		recordLen = 16
	}
	if recordLen < 0 || recordLen > 255 {
		return fmt.Errorf("record length %d is not in 1..255", recordLen)
	}
	var upper uint64
	var line []byte
	write := func(typ byte, off uint16, data []byte) error {
		line = append(line[:0], ':')
		sum := byte(len(data)) + byte(off>>8) + byte(off) + typ
		line = appendHexByte(line, byte(len(data)))
		line = appendHexByte(line, byte(off>>8))
		line = appendHexByte(line, byte(off))
		line = appendHexByte(line, typ)
		for _, c := range data {
			line = appendHexByte(line, c)
			sum += c
		}
		line = appendHexByte(line, -sum)
		line = append(line, '\n')
		_, err := w.Write(line)
		return err
	}
	for _, s := range segs {
		if s.Addr+uint64(len(s.Data)) > maxAddr {
			return fmt.Errorf("segment at 0x%X goes beyond the 4 GiB address space", s.Addr)
		}
		addr, data := s.Addr, s.Data
		for len(data) > 0 {
			if hi := addr >> 16; hi != upper {
				upper = hi
				if err := write(ihexLinear, 0, []byte{byte(hi >> 8), byte(hi)}); err != nil {
					return err
				}
			}
			n := min(recordLen, len(data), int(0x10000-addr&0xFFFF))
			if err := write(ihexData, uint16(addr), data[:n]); err != nil {
				return err
			}
			addr += uint64(n)
			data = data[n:]
		}
	}
	return write(ihexEOF, 0, nil)
}
