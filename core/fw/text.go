package fw

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/ipoluianov/althex/core/codec"
)

// looksIHex tells an Intel HEX file by its first record or its extension
func looksIHex(head []byte, ext string) bool {
	s := strings.TrimLeft(string(head), " \t\r\n")
	if len(s) >= 11 && s[0] == ':' && isHex(s[1:11]) {
		return true
	}
	switch ext {
	case "hex", "ihex", "ihx", "h86", "a43":
		return len(s) > 0 && s[0] == ':'
	}
	return false
}

// looksSRec tells an S-record file by its first record or its extension
func looksSRec(head []byte, ext string) bool {
	s := strings.TrimLeft(string(head), " \t\r\n")
	if len(s) >= 10 && (s[0] == 'S' || s[0] == 's') && s[1] >= '0' && s[1] <= '9' && s[1] != '4' && isHex(s[2:10]) {
		return true
	}
	switch ext {
	case "srec", "s19", "s28", "s37", "mot", "mhx", "sx", "s":
		return len(s) > 0 && (s[0] == 'S' || s[0] == 's')
	}
	return false
}

func isHex(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return false
		}
	}
	return true
}

func textReader(r io.ReaderAt, size int64) (io.Reader, error) {
	if size > maxText {
		return nil, fmt.Errorf("%w: a text image of more than %d MiB", ErrFormat, maxText>>20)
	}
	return io.NewSectionReader(r, 0, size), nil
}

func openIHex(r io.ReaderAt, size int64) (*Image, error) {
	tr, err := textReader(r, size)
	if err != nil {
		return nil, err
	}
	segs, err := codec.ParseIntelHex(tr)
	if err != nil {
		return nil, fmt.Errorf("%w: Intel HEX: %v", ErrFormat, err)
	}
	im := &Image{Format: "Intel HEX", kind: kindIHex}
	textSegments(im, segs)
	// The start address: record 05 (EIP) or 03 (CS:IP)
	scanLines(r, size, func(s string) bool {
		if len(s) < 11 || s[0] != ':' {
			return true
		}
		typ := s[7:9]
		if (typ == "05" || typ == "03") && len(s) >= 19 {
			v, err := strconv.ParseUint(s[9:17], 16, 32)
			if err != nil {
				return true
			}
			if typ == "03" {
				im.Entry = (v>>16)<<4 + v&0xFFFF
				im.info("Start segment address", "%04X:%04X", v>>16, v&0xFFFF)
			} else {
				im.Entry = v
				im.info("Start linear address", "0x%08X", v)
			}
			im.HasEntry = true
		}
		return typ != "01"
	})
	return im, nil
}

func openSRec(r io.ReaderAt, size int64) (*Image, error) {
	tr, err := textReader(r, size)
	if err != nil {
		return nil, err
	}
	segs, err := codec.ParseSRecord(tr)
	if err != nil {
		return nil, fmt.Errorf("%w: S-record: %v", ErrFormat, err)
	}
	im := &Image{Format: "S-record", kind: kindSRec}
	textSegments(im, segs)
	scanLines(r, size, func(s string) bool {
		if len(s) < 4 || (s[0] != 'S' && s[0] != 's') {
			return true
		}
		switch s[1] {
		case '0':
			if b, ok := hexBytes(s[2:]); ok && len(b) > 3 {
				im.info("Header", "%q", strings.TrimRight(string(b[3:len(b)-1]), "\x00"))
			}
		case '7', '8', '9':
			alen := int('9'-s[1]) + 2 // S9: 2, S8: 3, S7: 4
			if b, ok := hexBytes(s[2:]); ok && len(b) >= 2+alen {
				var a uint64
				for _, c := range b[1 : 1+alen] {
					a = a<<8 | uint64(c)
				}
				im.Entry, im.HasEntry = a, true
				im.info("Start address", "0x%X", a)
			}
			return false
		}
		return true
	})
	return im, nil
}

func textSegments(im *Image, segs []codec.Segment) {
	for i, s := range segs {
		im.Segments = append(im.Segments, memSeg(fmt.Sprintf("segment %d", i), s.Addr, s.Data, true))
	}
	im.info("Segments", "%d", len(segs))
}

// scanLines calls f with each trimmed line until it returns false
func scanLines(r io.ReaderAt, size int64, f func(string) bool) {
	sc := bufio.NewScanner(io.NewSectionReader(r, 0, size))
	sc.Buffer(make([]byte, 0, 4096), 1<<20)
	for sc.Scan() {
		if !f(strings.TrimSpace(sc.Text())) {
			return
		}
	}
}

func hexBytes(s string) ([]byte, bool) {
	if len(s)%2 != 0 || !isHex(s) {
		return nil, false
	}
	out := make([]byte, len(s)/2)
	for i := range out {
		v, _ := strconv.ParseUint(s[2*i:2*i+2], 16, 8)
		out[i] = byte(v)
	}
	return out, true
}
