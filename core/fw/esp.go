package fw

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
)

// The images of Espressif chips (esptool's "image format"): a header, the
// extended header of the ESP32 family, segments (load address, length,
// data), the checksum byte at the end of a 16-byte block and, when the
// header says so, the SHA-256 of all that.
const (
	espMagic       = 0xE9
	esp8266V2Magic = 0xEA
	espChecksum0   = 0xEF
	espHeaderLen   = 8
	espExtLen      = 16
	espMaxSegs     = 16
)

// ESPChip is a chip ID of the extended header
type ESPChip struct {
	ID   uint16
	Name string
	Arch string
}

// ESPChips are the chip IDs of esptool
var ESPChips = []ESPChip{
	{0, "ESP32", "xtensa"},
	{2, "ESP32-S2", "xtensa"},
	{5, "ESP32-C3", "riscv32"},
	{9, "ESP32-S3", "xtensa"},
	{12, "ESP32-C2", "riscv32"},
	{13, "ESP32-C6", "riscv32"},
	{16, "ESP32-H2", "riscv32"},
	{18, "ESP32-P4", "riscv32"},
	{20, "ESP32-C61", "riscv32"},
	{23, "ESP32-C5", "riscv32"},
}

func espChip(id uint16) (ESPChip, bool) {
	for _, c := range ESPChips {
		if c.ID == id {
			return c, true
		}
	}
	return ESPChip{}, false
}

// espLayout is where the parts of an ESP image are
type espLayout struct {
	esp32    bool // the extended header is there
	chip     ESPChip
	segs     [][3]uint32 // load address, offset of the data, length
	csumOff  int64
	hashed   bool  // the SHA-256 follows the checksum
	hashOff  int64 // of the SHA-256
	imageEnd int64 // after the checksum or the hash
}

// espParse finds the parts of an ESP32 image (esp32 true) or an ESP8266 v1
// image in data read from r
func espParse(r io.ReaderAt, size int64, esp32 bool) (*espLayout, error) {
	h := readAt(r, 0, espHeaderLen+espExtLen)
	if h == nil {
		if h = readAt(r, 0, espHeaderLen); h == nil || esp32 {
			return nil, ErrFormat
		}
	}
	if h[0] != espMagic {
		return nil, ErrFormat
	}
	n := int(h[1])
	if n == 0 || n > espMaxSegs {
		return nil, fmt.Errorf("%w: %d segments", ErrFormat, n)
	}
	l := &espLayout{esp32: esp32}
	off := int64(espHeaderLen)
	if esp32 {
		c, ok := espChip(le16(h[12:]))
		if !ok {
			return nil, fmt.Errorf("%w: chip ID %d", ErrFormat, le16(h[12:]))
		}
		for _, b := range h[19:23] {
			if b != 0 {
				return nil, fmt.Errorf("%w: reserved bytes of the extended header", ErrFormat)
			}
		}
		if h[23] > 1 {
			return nil, ErrFormat
		}
		l.chip, l.hashed = c, h[23] == 1
		off += espExtLen
	}
	for i := 0; i < n; i++ {
		sh := readAt(r, off, 8)
		if sh == nil {
			return nil, fmt.Errorf("%w: segment %d is cut", ErrFormat, i)
		}
		addr, ln := le32(sh), le32(sh[4:])
		off += 8
		if name, _ := espSegName(addr); !esp32 && name == "" {
			return nil, fmt.Errorf("%w: segment %d at 0x%X", ErrFormat, i, addr)
		}
		if int64(ln) > size-off {
			return nil, fmt.Errorf("%w: segment %d of %d bytes goes beyond the file", ErrFormat, i, ln)
		}
		l.segs = append(l.segs, [3]uint32{addr, uint32(off), ln})
		off += int64(ln)
	}
	// Zeros to the last byte of a 16-byte block, the checksum there
	l.csumOff = off | 15
	if l.csumOff >= size {
		return nil, fmt.Errorf("%w: no checksum", ErrFormat)
	}
	l.imageEnd = l.csumOff + 1
	if l.hashed {
		l.hashOff = l.imageEnd
		l.imageEnd += sha256.Size
	}
	return l, nil
}

// espSum computes the checksum byte of the segments
func espSum(r io.ReaderAt, l *espLayout) byte {
	sum := byte(espChecksum0)
	buf := make([]byte, 32<<10)
	for _, s := range l.segs {
		off, left := int64(s[1]), int64(s[2])
		for left > 0 {
			n := int64(len(buf))
			if n > left {
				n = left
			}
			m, _ := r.ReadAt(buf[:n], off)
			for _, c := range buf[:m] {
				sum ^= c
			}
			if int64(m) < n {
				break
			}
			off += n
			left -= n
		}
	}
	return sum
}

func espSHA(r io.ReaderAt, l *espLayout) []byte {
	h := sha256.New()
	io.Copy(h, io.NewSectionReader(r, 0, l.csumOff+1))
	return h.Sum(nil)
}

// espSegName names a segment of an ESP image by its address
func espSegName(addr uint32) (string, bool) {
	switch {
	case addr >= 0x40370000 && addr < 0x40400000, // IRAM of the C3, S3
		addr >= 0x40000000 && addr < 0x400C2000, // IRAM of the ESP32
		addr >= 0x40100000 && addr < 0x40200000, // IRAM of the ESP8266
		addr >= 0x40800000 && addr < 0x40880000: // HP SRAM of the C6, H2
		return "iram", true
	case addr >= 0x400C2000 && addr < 0x40C00000, addr >= 0x42000000 && addr < 0x44000000:
		return "irom", true
	case addr >= 0x3F400000 && addr < 0x3F800000, addr >= 0x3C000000 && addr < 0x3E000000:
		return "drom", false
	case addr >= 0x3FC00000 && addr < 0x40000000:
		return "dram", false
	case addr >= 0x50000000 && addr < 0x50002000, addr >= 0x600FE000 && addr < 0x60100000:
		return "rtc", true
	}
	return "", false
}

var spiModes = []string{"QIO", "QOUT", "DIO", "DOUT", "FAST_READ", "SLOW_READ"}

func openESP(r io.ReaderAt, size int64) (*Image, error) {
	h := readAt(r, 0, espHeaderLen)
	if h == nil {
		return nil, ErrFormat
	}
	if h[0] == esp8266V2Magic {
		return openESP8266V2(r, size, h)
	}
	l, err := espParse(r, size, true)
	esp32 := err == nil
	if esp32 {
		if f := espFindFlash(r, size); f != nil {
			return openESPFlash(r, size, f), nil
		}
	}
	if !esp32 {
		var err2 error
		if l, err2 = espParse(r, size, false); err2 != nil {
			return nil, err
		}
	}
	im := &Image{kind: kindESP32}
	if esp32 {
		im.Format = "ESP32 app image"
		if l.chip.ID != 0 {
			im.Format = l.chip.Name + " app image"
		}
		im.Arch, im.Family = l.chip.Arch, l.chip.Name
	} else {
		im.Format, im.kind = "ESP8266 image", kindESP8266
		im.Arch, im.Family = "xtensa", "ESP8266"
	}
	im.Entry, im.HasEntry = uint64(le32(h[4:])), true
	espHeaderInfo(im, r, h, esp32)
	for i, s := range l.segs {
		name, exec := espSegName(s[0])
		if name == "" {
			name = fmt.Sprintf("segment %d", i)
		}
		im.Segments = append(im.Segments, fileSeg(name, uint64(s[0]), r, int64(s[1]), int64(s[2]), exec))
		im.info(fmt.Sprintf("Segment %d", i), "%s at 0x%08X, %d bytes at offset 0x%X", name, s[0], s[2], s[1])
	}
	im.espSubs = []espSub{{off: 0, esp32: esp32}}
	espCheck(im, r, l, im.espSubs[0])
	if !esp32 {
		esp8266IROM(im, r, size, l.imageEnd)
	}
	return im, nil
}

func espHeaderInfo(im *Image, r io.ReaderAt, h []byte, esp32 bool) {
	im.info("Segments", "%d", h[1])
	if int(h[2]) < len(spiModes) {
		im.info("SPI mode", "%s", spiModes[h[2]])
	} else {
		im.info("SPI mode", "%d", h[2])
	}
	im.info("Flash size / frequency", "0x%02X", h[3])
	im.info("Entry", "0x%08X", le32(h[4:]))
	if !esp32 {
		return
	}
	x := readAt(r, espHeaderLen, espExtLen)
	if x == nil {
		return
	}
	im.info("Chip ID", "%d", le16(x[4:]))
	im.info("WP pin", "0x%02X", x[0])
	im.info("Minimal chip revision", "%d (full %d.%d)", x[6], le16(x[7:])/100, le16(x[7:])%100)
	im.info("Maximal chip revision", "%d.%d", le16(x[9:])/100, le16(x[9:])%100)
	im.info("Hash appended", "%v", x[15] == 1)
}

// espSub is an ESP image inside the file: a plain image at 0, or the
// bootloader and the app of a flash image
type espSub struct {
	off   int64  // in the file
	esp32 bool   // with the extended header
	key   string // before the keys of the info ("Bootloader ")
	issue string // before the issues ("bootloader: ")
}

// espCheck checks the checksum and the hash of an ESP image read from r
// (the file from sub.off on)
func espCheck(im *Image, r io.ReaderAt, l *espLayout, sub espSub) {
	sum := espSum(r, l)
	got := readAt(r, l.csumOff, 1)
	if got == nil {
		im.issue("%sno checksum byte", sub.issue)
		return
	}
	if got[0] != sum {
		im.issue("%schecksum 0x%02X at 0x%X, should be 0x%02X", sub.issue, got[0], sub.off+l.csumOff, sum)
	} else {
		im.info(sub.key+"Checksum", "0x%02X (OK)", sum)
	}
	if !l.hashed {
		return
	}
	hash := readAt(r, l.hashOff, sha256.Size)
	if hash == nil {
		im.issue("%sno SHA-256 after the checksum", sub.issue)
		return
	}
	want := espSHA(r, l)
	if !bytes.Equal(hash, want) {
		im.issue("%sSHA-256 at 0x%X does not match the image (should be %x)", sub.issue, sub.off+l.hashOff, want)
	} else {
		im.info(sub.key+"SHA-256", "%x (OK)", hash)
	}
}

// espVerify checks the images of an ESP file again
func espVerify(r io.ReaderAt, size int64, subs []espSub) []string {
	e := &Image{}
	for _, sub := range subs {
		if sub.off < 0 || sub.off >= size {
			continue
		}
		sr := io.NewSectionReader(r, sub.off, size-sub.off)
		if l, err := espParse(sr, size-sub.off, sub.esp32); err == nil {
			espCheck(e, sr, l, sub)
		} else {
			e.issue("%s%v", sub.issue, err)
		}
	}
	return e.Issues
}

// openESP8266V2 reads the ESP8266 "v2" image: an 0xEA header, the IROM
// segment stored as is, then an 0xE9 v1 image with the IRAM segments
func openESP8266V2(r io.ReaderAt, size int64, h []byte) (*Image, error) {
	im := &Image{Format: "ESP8266 image (v2)", kind: kindESP8266, Arch: "xtensa", Family: "ESP8266"}
	im.Entry, im.HasEntry = uint64(le32(h[4:])), true
	sh := readAt(r, 8, 8)
	if sh == nil {
		return nil, ErrFormat
	}
	addr, ln := le32(sh), int64(le32(sh[4:]))
	if ln > size-16 {
		return nil, fmt.Errorf("%w: the IROM segment goes beyond the file", ErrFormat)
	}
	im.info("Entry", "0x%08X", le32(h[4:]))
	im.Segments = append(im.Segments, fileSeg("irom", uint64(addr), r, 16, ln, true))
	// The v1 image after it, aligned to 16 bytes
	off := (16 + ln + 15) &^ 15
	if off < size && size-off > 8 {
		sub := io.NewSectionReader(r, off, size-off)
		l, err := espParse(sub, size-off, false)
		if err == nil {
			for i, s := range l.segs {
				name, exec := espSegName(s[0])
				if name == "" {
					name = fmt.Sprintf("segment %d", i+1)
				}
				im.Segments = append(im.Segments, fileSeg(name, uint64(s[0]), r, off+int64(s[1]), int64(s[2]), exec))
			}
			e := &Image{}
			im.espSubs = []espSub{{off: off}}
			espCheck(e, sub, l, im.espSubs[0])
			im.Issues = append(im.Issues, e.Issues...)
			im.Info = append(im.Info, e.Info...)
			if b := readAt(sub, 4, 4); b != nil {
				im.Entry = uint64(le32(b))
			}
		} else {
			im.issue("no v1 image after the IROM segment: %v", err)
		}
	}
	return im, nil
}

// The ESP8266 maps the flash at 0x40200000 (1 MB): the code that runs from
// the flash (irom0) is stored after the image at 0, at the flash offset of
// its address (0x9000 for MicroPython, 0x10000 or 0x40000 for the SDKs)
const (
	esp8266FlashMap  = 0x40200000
	esp8266FlashSize = 1 << 20
)

// esp8266IROM adds the irom0 segment of an ESP8266 v1 image: the data
// after the image at 0 (and its padding), from the 4 KB boundary nearest
// to its first byte that is not padding
func esp8266IROM(im *Image, r io.ReaderAt, size, end int64) {
	buf := make([]byte, 4096)
	start := int64(-1)
	for o := end; o < size && o < esp8266FlashSize && start < 0; o += int64(len(buf)) {
		n, _ := r.ReadAt(buf, o)
		if n <= 0 {
			break
		}
		for i, c := range buf[:n] {
			if c != 0xFF && c != 0 {
				start = o + int64(i)
				break
			}
		}
	}
	if start < 0 {
		return
	}
	// To the nearest 4 KB block: MicroPython writes the length of the
	// image just before it, the code may start with zeros
	start = (start + 0x800) &^ 0xFFF
	if start < end {
		start = end
	}
	n := size - start
	if start+n > esp8266FlashSize {
		n = esp8266FlashSize - start
	}
	if n < 16 {
		return
	}
	addr := uint64(esp8266FlashMap + start)
	im.Segments = append(im.Segments, fileSeg("irom0", addr, r, start, n, true))
	im.info("irom0", "0x%08X, %d bytes at offset 0x%X (the flash mapped at 0x%08X)", addr, n, start, esp8266FlashMap)
}

// FixESP32 recomputes the checksum byte of an ESP32 (or ESP8266 v1) image
// and the SHA-256 when the header says it is appended. The image is
// returned changed in a copy; a hash that is missing is added at the end.
// In a full flash image, the bootloader and the app are fixed.
func FixESP32(data []byte) ([]byte, error) {
	r := bytes.NewReader(data)
	size := int64(len(data))
	if f := espFindFlash(r, size); f != nil {
		out := append([]byte(nil), data...)
		out, _ = espFix(out, f.boot, f.bootL)
		if p := f.app(r); p != nil {
			if l, err := espParse(bytes.NewReader(out[p.fileOff:]), size-p.fileOff, true); err == nil {
				out, _ = espFix(out, p.fileOff, l)
			}
		}
		return out, nil
	}
	// The hash may be missing: parse it as a longer file would be
	l, err := espParse(r, size, true)
	if err != nil {
		var err2 error
		if l, err2 = espParse(r, size, false); err2 != nil {
			return nil, err
		}
	}
	return espFix(append([]byte(nil), data...), 0, l)
}

// espFix writes the checksum and the hash of the image at off in out
func espFix(out []byte, off int64, l *espLayout) ([]byte, error) {
	img := out[off:]
	img[l.csumOff] = espSum(bytes.NewReader(img), l)
	if l.hashed {
		sum := sha256.Sum256(img[:l.csumOff+1])
		if int64(len(img)) < l.hashOff+sha256.Size {
			out = append(out[:off+l.hashOff], make([]byte, sha256.Size)...)
			img = out[off:]
		}
		copy(img[l.hashOff:], sum[:])
	}
	return out, nil
}
