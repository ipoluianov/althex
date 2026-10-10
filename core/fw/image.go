// Package fw reads firmware images (FW-1): raw binaries, Intel HEX,
// S-records, ELF, UF2, DfuSe and the images of the ESP32 and the ESP8266,
// as a sparse address space of segments at the addresses of the device. It
// guesses the architecture and the load address of a firmware (FW-3),
// decodes configuration areas: option bytes, fuses, eFuses (FW-11), and
// checks and recomputes the checksums of the images (FW-14).
package fw

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
)

var (
	// ErrNoData is a read of an address no segment has
	ErrNoData = errors.New("no data at the address")
	// ErrFormat is a file that is not of the format it looks like
	ErrFormat = errors.New("not a valid image")
)

// Segment is bytes of the image at an address of the device
type Segment struct {
	Name string // "flash", ".text", "segment 2", "iram"...
	Addr uint64 // the address on the device
	// Off is where its bytes are in the file; -1 when they are not stored
	// as they are (Intel HEX text, UF2 blocks: see AddrToOff)
	Off  int64
	Size int64
	Data io.ReaderAt // its bytes (from the file, or decoded)
	Exec bool

	// parts map runs of a decoded segment to the file when they are
	// stored there as they are (the payloads of UF2 blocks)
	parts []part
}

// part is a run of a segment stored as is in the file
type part struct {
	addr uint64
	off  int64
	size int64
}

// End is the address after the last byte of the segment
func (s *Segment) End() uint64 { return s.Addr + uint64(s.Size) }

// KV is a field of a header worth showing
type KV struct{ Key, Value string }

// Image is a firmware: its segments and what its format tells about it
type Image struct {
	Format   string    // "Raw binary", "Intel HEX", "ELF (ARM, 32-bit)", "UF2 (family RP2040)"...
	Segments []Segment // sorted by address
	Entry    uint64
	HasEntry bool
	Arch     string // the disasm ID when the format tells it ("arm", "xtensa"...), "" if not
	Info     []KV   // header fields worth showing
	Issues   []string

	// Family is the chip family when the format tells it ("RP2040", "ESP32-C3")
	Family string

	// Symbols are the names the format carries (the ELF symbol table),
	// sorted by address (FW-12)
	Symbols []Symbol

	src  io.ReaderAt
	size int64
	kind formatKind

	// espSubs are the ESP images in the file whose checksums Verify checks
	espSubs []espSub
}

type formatKind int

const (
	kindRaw formatKind = iota
	kindIHex
	kindSRec
	kindELF
	kindUF2
	kindDfuSe
	kindESP32
	kindESP8266
)

// Raw tells whether the image is a raw binary, whose load address is not known
func (im *Image) Raw() bool { return im.kind == kindRaw }

func (im *Image) info(k, format string, args ...any) {
	im.Info = append(im.Info, KV{k, fmt.Sprintf(format, args...)})
}

func (im *Image) issue(format string, args ...any) {
	im.Issues = append(im.Issues, fmt.Sprintf(format, args...))
}

// maxText is the largest Intel HEX or S-record file read
const maxText = 256 << 20

// Open reads a firmware image of size bytes. The format is told by the
// content, and by the extension of name for the text formats; a file of no
// known format is a raw binary: one segment at address 0.
func Open(r io.ReaderAt, size int64, name string) (im *Image, err error) {
	defer func() {
		// debug/elf and the parsers must not take the program down on a
		// broken file
		if p := recover(); p != nil {
			im, err = nil, fmt.Errorf("%w: %v", ErrFormat, p)
		}
	}()
	if size < 0 {
		return nil, ErrFormat
	}
	head := make([]byte, 64)
	n, _ := r.ReadAt(head, 0)
	head = head[:n]
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(name), "."))

	var opener func(io.ReaderAt, int64) (*Image, error)
	switch {
	case len(head) >= 12 && string(head[:4]) == "UF2\n" && le32(head[4:]) == uf2Magic1:
		opener = openUF2
	case len(head) >= 11 && string(head[:5]) == "DfuSe":
		opener = openDfuSe
	case len(head) >= 4 && string(head[:4]) == "\x7FELF":
		opener = openELF
	case looksIHex(head, ext):
		opener = openIHex
	case looksSRec(head, ext):
		opener = openSRec
	case len(head) >= 8 && (head[0] == espMagic || head[0] == esp8266V2Magic):
		if e, err := openESP(r, size); err == nil {
			return finish(e, r, size), nil
		}
	}
	if opener != nil {
		e, err := opener(r, size)
		if err != nil {
			return nil, err
		}
		return finish(e, r, size), nil
	}
	// A flash dump of an ESP32 from address 0: the bootloader at 0x1000
	if f := espFindFlash(r, size); f != nil {
		return finish(openESPFlash(r, size, f), r, size), nil
	}
	return finish(openRaw(r, size), r, size), nil
}

func finish(im *Image, r io.ReaderAt, size int64) *Image {
	im.src, im.size = r, size
	sort.SliceStable(im.Segments, func(i, j int) bool { return im.Segments[i].Addr < im.Segments[j].Addr })
	for i := 1; i < len(im.Segments); i++ {
		a, b := &im.Segments[i-1], &im.Segments[i]
		// Segments of different UF2 families may share addresses: that is
		// what such files are for
		if b.Addr < a.End() && !(im.kind == kindUF2 && a.Name != b.Name) {
			im.issue("%s at 0x%X overlaps %s (to 0x%X)", b.Name, b.Addr, a.Name, a.End())
		}
	}
	return im
}

func openRaw(r io.ReaderAt, size int64) *Image {
	im := &Image{Format: "Raw binary", kind: kindRaw}
	if size > 0 {
		im.Segments = []Segment{fileSeg("flash", 0, r, 0, size, true)}
	}
	return im
}

// fileSeg is a segment stored as is in the file
func fileSeg(name string, addr uint64, r io.ReaderAt, off, size int64, exec bool) Segment {
	return Segment{Name: name, Addr: addr, Off: off, Size: size, Data: io.NewSectionReader(r, off, size), Exec: exec}
}

// memSeg is a decoded segment
func memSeg(name string, addr uint64, data []byte, exec bool) Segment {
	return Segment{Name: name, Addr: addr, Off: -1, Size: int64(len(data)), Data: bytes.NewReader(data), Exec: exec}
}

// find returns the index of the segment with the address; -1 if none.
// Where segments overlap, the first one wins.
func (im *Image) find(addr uint64) int {
	i := sort.Search(len(im.Segments), func(i int) bool { return im.Segments[i].End() > addr })
	for ; i < len(im.Segments); i++ {
		s := &im.Segments[i]
		if s.Addr > addr {
			break
		}
		if addr < s.End() {
			return i
		}
	}
	// Overlapping segments break the order of the ends: look at them all
	for j := range im.Segments {
		s := &im.Segments[j]
		if addr >= s.Addr && addr < s.End() {
			return j
		}
	}
	return -1
}

// Contains tells whether a segment has the address
func (im *Image) Contains(addr uint64) bool { return im.find(addr) >= 0 }

// ReadAddr reads the bytes at a device address across segments, like
// ReadAt over the address space. A gap before p is full is ErrNoData.
func (im *Image) ReadAddr(p []byte, addr uint64) (int, error) {
	n := 0
	for n < len(p) {
		i := im.find(addr)
		if i < 0 {
			return n, ErrNoData
		}
		s := &im.Segments[i]
		want := len(p) - n
		if left := s.End() - addr; uint64(want) > left {
			want = int(left)
		}
		m, err := s.Data.ReadAt(p[n:n+want], int64(addr-s.Addr))
		n += m
		addr += uint64(m)
		if m < want {
			if err == nil || err == io.EOF {
				err = io.ErrUnexpectedEOF
			}
			return n, err
		}
	}
	return n, nil
}

// AddrToOff returns the file offset of a device address when its byte is
// stored as it is in the file
func (im *Image) AddrToOff(addr uint64) (int64, bool) {
	i := im.find(addr)
	if i < 0 {
		return 0, false
	}
	s := &im.Segments[i]
	if s.Off >= 0 {
		return s.Off + int64(addr-s.Addr), true
	}
	for _, p := range s.parts {
		if addr >= p.addr && addr < p.addr+uint64(p.size) {
			return p.off + int64(addr-p.addr), true
		}
	}
	return 0, false
}

// OffToAddr returns the device address of the byte at a file offset
func (im *Image) OffToAddr(off int64) (uint64, bool) {
	for i := range im.Segments {
		s := &im.Segments[i]
		if s.Off >= 0 {
			if off >= s.Off && off < s.Off+s.Size {
				return s.Addr + uint64(off-s.Off), true
			}
			continue
		}
		for _, p := range s.parts {
			if off >= p.off && off < p.off+p.size {
				return p.addr + uint64(off-p.off), true
			}
		}
	}
	return 0, false
}

// Bounds returns the lowest address and the address after the highest byte
func (im *Image) Bounds() (lo, hi uint64) {
	for i, s := range im.Segments {
		if i == 0 || s.Addr < lo {
			lo = s.Addr
		}
		if s.End() > hi {
			hi = s.End()
		}
	}
	return lo, hi
}

// readSeg reads at most max bytes from the start of a segment
func readSeg(s *Segment, max int64) []byte {
	n := s.Size
	if n > max {
		n = max
	}
	if n <= 0 {
		return nil
	}
	b := make([]byte, n)
	m, _ := s.Data.ReadAt(b, 0)
	return b[:m]
}

// readAt reads n bytes at off; nil if the file is shorter
func readAt(r io.ReaderAt, off int64, n int) []byte {
	if off < 0 || n < 0 {
		return nil
	}
	b := make([]byte, n)
	if m, _ := r.ReadAt(b, off); m != n {
		return nil
	}
	return b
}

func le16(b []byte) uint16 { return uint16(b[0]) | uint16(b[1])<<8 }
func le32(b []byte) uint32 {
	return uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24
}
