package codec

import (
	"errors"
	"fmt"
	"io"
)

// srecAddrLen is the length of the address field of each S-record type;
// 0 - the type is not defined (S4)
var srecAddrLen = [10]int{2, 2, 3, 4, 0, 2, 3, 4, 3, 2}

// ParseSRecord reads a Motorola S-record file (S19, S28, S37). The header
// (S0) and the entry point (S7..S9) are not kept; a record count (S5, S6)
// must match the data records before it. Records after the termination
// record are ignored; a file without one is an error, as it is likely cut.
func ParseSRecord(r io.Reader) ([]Segment, error) {
	lr := newLineReader(r)
	var b segBuilder
	count := 0 // data records so far
	for {
		s, more := lr.next()
		if !more {
			break
		}
		if s == "" {
			continue
		}
		n := lr.line
		if len(s) < 2 || (s[0] != 'S' && s[0] != 's') || s[1] < '0' || s[1] > '9' {
			return nil, fmt.Errorf("line %d: a record must start with S0..S9", n)
		}
		typ := int(s[1] - '0')
		alen := srecAddrLen[typ]
		if alen == 0 {
			return nil, fmt.Errorf("line %d: record type S%d is not defined", n, typ)
		}
		rec, err := decodeHexBytes(s[2:], n)
		if err != nil {
			return nil, err
		}
		// rec: count, address, data, checksum; count covers all but itself
		if len(rec) < 2 || len(rec) != 1+int(rec[0]) || len(rec) < 2+alen {
			return nil, fmt.Errorf("line %d: wrong record length", n)
		}
		var sum byte
		for _, c := range rec[:len(rec)-1] {
			sum += c
		}
		if ^sum != rec[len(rec)-1] {
			return nil, fmt.Errorf("line %d: checksum is %02X, should be %02X", n, rec[len(rec)-1], ^sum)
		}
		var addr uint64
		for _, c := range rec[1 : 1+alen] {
			addr = addr<<8 | uint64(c)
		}
		data := rec[1+alen : len(rec)-1]
		switch typ {
		case 0:
		case 1, 2, 3:
			if err := b.add(addr, data, n); err != nil {
				return nil, err
			}
			count++
		case 5, 6:
			if len(data) != 0 {
				return nil, fmt.Errorf("line %d: count record with data", n)
			}
			if addr != uint64(count) {
				return nil, fmt.Errorf("line %d: the count record says %d data records, there are %d", n, addr, count)
			}
		case 7, 8, 9:
			if len(data) != 0 {
				return nil, fmt.Errorf("line %d: termination record with data", n)
			}
			if err := lr.err(); err != nil {
				return nil, err
			}
			return b.result()
		}
	}
	if err := lr.err(); err != nil {
		return nil, err
	}
	return nil, errors.New("no termination record (S7, S8 or S9): the file may be cut")
}

// WriteSRecord writes the segments as Motorola S-records: a header record
// (S0) when header is not "", data records of recordLen bytes (0 - 16) with
// the shortest address field the highest address fits in (S1, S2 or S3), a
// count record (S5 or S6; none above 16M records), and the matching
// termination record (S9, S8 or S7) with the entry point 0.
func WriteSRecord(w io.Writer, segs []Segment, recordLen int, header string) error {
	if recordLen == 0 {
		recordLen = 16
	}
	_, hi := Bounds(segs)
	if hi > maxAddr {
		return fmt.Errorf("data beyond the 4 GiB address space")
	}
	dataType, alen := 1, 2
	switch {
	case hi > 0x1000000:
		dataType, alen = 3, 4
	case hi > 0x10000:
		dataType, alen = 2, 3
	}
	// The count byte covers the address, the data and the checksum
	if recordLen < 0 || recordLen > 255-alen-1 {
		return fmt.Errorf("record length %d is not in 1..%d", recordLen, 255-alen-1)
	}
	if len(header) > 255-3 {
		return fmt.Errorf("header is longer than %d bytes", 255-3)
	}
	var line []byte
	write := func(typ, alen int, addr uint64, data []byte) error {
		line = append(line[:0], 'S', byte('0'+typ))
		cnt := byte(alen + len(data) + 1)
		line = appendHexByte(line, cnt)
		sum := cnt
		for i := alen - 1; i >= 0; i-- {
			c := byte(addr >> (8 * i))
			line = appendHexByte(line, c)
			sum += c
		}
		for _, c := range data {
			line = appendHexByte(line, c)
			sum += c
		}
		line = appendHexByte(line, ^sum)
		line = append(line, '\n')
		_, err := w.Write(line)
		return err
	}
	if header != "" {
		if err := write(0, 2, 0, []byte(header)); err != nil {
			return err
		}
	}
	count := 0
	for _, s := range segs {
		addr, data := s.Addr, s.Data
		for len(data) > 0 {
			n := min(recordLen, len(data))
			if err := write(dataType, alen, addr, data[:n]); err != nil {
				return err
			}
			count++
			addr += uint64(n)
			data = data[n:]
		}
	}
	switch {
	case count <= 0xFFFF:
		if err := write(5, 2, uint64(count), nil); err != nil {
			return err
		}
	case count <= 0xFFFFFF:
		if err := write(6, 3, uint64(count), nil); err != nil {
			return err
		}
	}
	return write(10-dataType, alen, 0, nil)
}
