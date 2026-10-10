package fw

import (
	"bytes"
	"crypto/sha256"
	"debug/elf"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"strings"

	"github.com/ipoluianov/althex/core/codec"
)

func put32(b []byte, v uint32) { binary.LittleEndian.PutUint32(b, v) }

// cortexImage is a raw Cortex-M image at base: the vector table with the
// system handlers and irqs IRQ handlers at tab, Thumb code after it
func cortexImage(base uint64, tab int, sp uint32, irqs int, size int) []byte {
	img := make([]byte, size)
	code := tab + (16+irqs)*4 + 0x20
	w := func(i int, v uint32) { put32(img[tab+4*i:], v) }
	w(0, sp)
	w(1, uint32(base)+uint32(code)+1)
	for _, i := range []int{2, 3, 4, 5, 6, 11, 12, 14, 15} {
		w(i, uint32(base)+uint32(code)+0x10+uint32(i)*2+1)
	}
	for i := 0; i < irqs; i++ {
		w(16+i, uint32(base)+uint32(code)+0x40+1)
	}
	// b . (Thumb) all over the code
	for o := code; o+1 < size; o += 2 {
		img[o], img[o+1] = 0xFE, 0xE7
	}
	return img
}

// rp2040Image is a raw RP2040 flash image: boot2 with its CRC, the table at 0x100
func rp2040Image() []byte {
	img := cortexImage(0x10000000, 0x100, 0x20042000, 26, 2048)
	for i := 0; i < 252; i++ {
		img[i] = byte(i * 7)
	}
	put32(img[252:], crcMPEG2(img[:252]))
	return img
}

// avrImage: 26 jmp vectors of an ATmega328P, reset at 0x68
func avrImage() []byte {
	img := make([]byte, 512)
	for i := 0; i < 26; i++ {
		t := uint16(0x7C / 2)
		if i == 0 {
			t = 0x68 / 2
		}
		binary.LittleEndian.PutUint16(img[4*i:], 0x940C)
		binary.LittleEndian.PutUint16(img[4*i+2:], t)
	}
	// eor r1, r1; then rjmp .-2 loops
	for o := 0x68; o+1 < len(img); o += 2 {
		binary.LittleEndian.PutUint16(img[o:], 0xCFFF)
	}
	binary.LittleEndian.PutUint16(img[0x68:], 0x2411)
	return img
}

type espSeg struct {
	addr uint32
	data []byte
}

// espImage builds an ESP32-family image with a correct checksum and hash
func espImage(chip uint16, entry uint32, hashed bool, segs ...espSeg) []byte {
	var b bytes.Buffer
	b.Write([]byte{0xE9, byte(len(segs)), 2, 0x20})
	binary.Write(&b, binary.LittleEndian, entry)
	ext := make([]byte, 16)
	ext[0] = 0xEE
	binary.LittleEndian.PutUint16(ext[4:], chip)
	if hashed {
		ext[15] = 1
	}
	b.Write(ext)
	sum := byte(0xEF)
	for _, s := range segs {
		binary.Write(&b, binary.LittleEndian, s.addr)
		binary.Write(&b, binary.LittleEndian, uint32(len(s.data)))
		b.Write(s.data)
		for _, c := range s.data {
			sum ^= c
		}
	}
	for b.Len()%16 != 15 {
		b.WriteByte(0)
	}
	b.WriteByte(sum)
	if hashed {
		h := sha256.Sum256(b.Bytes())
		b.Write(h[:])
	}
	return b.Bytes()
}

func esp32C3Image() []byte {
	return espImage(5, 0x40380400, true,
		espSeg{0x3C000020, bytes.Repeat([]byte{1, 2, 3}, 33)},
		espSeg{0x3FC88000, []byte("dram data")},
		espSeg{0x40380000, bytes.Repeat([]byte{0x13, 0, 0, 0}, 64)})
}

// esp8266Image is a v1 image: an 8-byte header, segments, checksum
func esp8266Image() []byte {
	var b bytes.Buffer
	b.Write([]byte{0xE9, 2, 0, 0x20, 0x00, 0x10, 0x10, 0x40})
	sum := byte(0xEF)
	for _, s := range []espSeg{{0x40100000, bytes.Repeat([]byte{0x12, 0x34}, 50)}, {0x3FFE8000, []byte("hello")}} {
		binary.Write(&b, binary.LittleEndian, s.addr)
		binary.Write(&b, binary.LittleEndian, uint32(len(s.data)))
		b.Write(s.data)
		for _, c := range s.data {
			sum ^= c
		}
	}
	for b.Len()%16 != 15 {
		b.WriteByte(0)
	}
	b.WriteByte(sum)
	return b.Bytes()
}

type uf2Block struct {
	fam  uint32
	addr uint32
	data []byte
}

func uf2File(blocks ...uf2Block) []byte {
	var out []byte
	count := map[uint32]int{}
	for _, b := range blocks {
		count[b.fam]++
	}
	no := map[uint32]int{}
	for _, bl := range blocks {
		b := make([]byte, 512)
		put32(b, uf2Magic0)
		put32(b[4:], uf2Magic1)
		put32(b[8:], uf2FamilyPresent)
		put32(b[12:], bl.addr)
		put32(b[16:], uint32(len(bl.data)))
		put32(b[20:], uint32(no[bl.fam]))
		put32(b[24:], uint32(count[bl.fam]))
		put32(b[28:], bl.fam)
		copy(b[32:], bl.data)
		put32(b[508:], uf2MagicEnd)
		no[bl.fam]++
		out = append(out, b...)
	}
	return out
}

func uf2TwoFamilies() []byte {
	rp := rp2040Image()
	return uf2File(
		uf2Block{0xe48bff56, 0x10000000, rp[:256]},
		uf2Block{0xe48bff56, 0x10000100, rp[256:512]},
		uf2Block{0xe48bff56, 0x10000200, rp[512:768]},
		uf2Block{0x68ed2b88, 0x00002000, bytes.Repeat([]byte{0xAA}, 256)},
		uf2Block{0x68ed2b88, 0x00002100, bytes.Repeat([]byte{0xBB}, 256)},
	)
}

type dfuTarget struct {
	alt   byte
	name  string
	elems []espSeg
}

func dfuseFile(targets ...dfuTarget) []byte {
	var body bytes.Buffer
	for _, t := range targets {
		var el bytes.Buffer
		for _, e := range t.elems {
			binary.Write(&el, binary.LittleEndian, e.addr)
			binary.Write(&el, binary.LittleEndian, uint32(len(e.data)))
			el.Write(e.data)
		}
		h := make([]byte, 274)
		copy(h, "Target")
		h[6] = t.alt
		put32(h[7:], 1)
		copy(h[11:], t.name)
		put32(h[266:], uint32(el.Len()))
		put32(h[270:], uint32(len(t.elems)))
		body.Write(h)
		body.Write(el.Bytes())
	}
	var f bytes.Buffer
	f.WriteString("DfuSe")
	f.WriteByte(1)
	binary.Write(&f, binary.LittleEndian, uint32(11+body.Len()))
	f.WriteByte(byte(len(targets)))
	f.Write(body.Bytes())
	f.Write([]byte{0xFF, 0xFF, 0x11, 0xDF, 0x83, 0x04, 0x1A, 0x01, 'U', 'F', 'D', 16})
	crc := ^crc32.ChecksumIEEE(f.Bytes())
	binary.Write(&f, binary.LittleEndian, crc)
	return f.Bytes()
}

func dfuseTwoTargets() []byte {
	return dfuseFile(
		dfuTarget{0, "Internal Flash", []espSeg{
			{0x08000000, cortexImage(0x08000000, 0, 0x20005000, 10, 512)},
			{0x08004000, []byte("second element")}}},
		dfuTarget{1, "Option Bytes", []espSeg{{0x1FFFF800, []byte{0xA5, 0x5A, 0x07, 0xF8, 0xFF, 0, 0xFF, 0, 0xFF, 0, 0xFF, 0, 0xFF, 0, 0xFF, 0}}}},
	)
}

// armELF builds an ELF executable: .text at 0x08000000, .data running at
// 0x20000000 but loaded at 0x08000100
func armELF() []byte {
	text := cortexImage(0x08000000, 0, 0x20005000, 4, 256)
	data := []byte("initialized data")
	const ehsize, phsize = 52, 32
	textOff := uint32(ehsize + 2*phsize)
	dataOff := textOff + uint32(len(text))
	var b bytes.Buffer
	hdr := elf.Header32{
		Type: uint16(elf.ET_EXEC), Machine: uint16(elf.EM_ARM), Version: 1,
		Entry: le32(text[4:]), Phoff: ehsize,
		Ehsize: ehsize, Phentsize: phsize, Phnum: 2, Shentsize: 40,
	}
	copy(hdr.Ident[:], "\x7FELF")
	hdr.Ident[elf.EI_CLASS] = byte(elf.ELFCLASS32)
	hdr.Ident[elf.EI_DATA] = byte(elf.ELFDATA2LSB)
	hdr.Ident[elf.EI_VERSION] = 1
	binary.Write(&b, binary.LittleEndian, hdr)
	binary.Write(&b, binary.LittleEndian, elf.Prog32{Type: uint32(elf.PT_LOAD), Off: textOff, Vaddr: 0x08000000, Paddr: 0x08000000,
		Filesz: uint32(len(text)), Memsz: uint32(len(text)), Flags: uint32(elf.PF_R | elf.PF_X), Align: 4})
	binary.Write(&b, binary.LittleEndian, elf.Prog32{Type: uint32(elf.PT_LOAD), Off: dataOff, Vaddr: 0x20000000, Paddr: 0x08000100,
		Filesz: uint32(len(data)), Memsz: uint32(len(data)) + 64, Flags: uint32(elf.PF_R | elf.PF_W), Align: 4})
	b.Write(text)
	b.Write(data)
	return b.Bytes()
}

// ihexFile: data at 0x08000000 and at 0x08010000 (extended linear
// addresses), the start linear address 0x08000101
func ihexFile() []byte {
	var b bytes.Buffer
	codec.WriteIntelHex(&b, []codec.Segment{
		{Addr: 0x08000000, Data: cortexImage(0x08000000, 0, 0x20005000, 8, 256)},
		{Addr: 0x08010000, Data: []byte("far away")},
	}, 16)
	s := b.String()
	rec := []byte{4, 0, 0, 5, 0x08, 0x00, 0x01, 0x01}
	var sum byte
	for _, c := range rec {
		sum += c
	}
	start := ":" + strings.ToUpper(fmt.Sprintf("%x", rec)) + fmt.Sprintf("%02X", byte(-int(sum))) + "\n"
	i := strings.Index(s, ":00000001FF")
	return []byte(s[:i] + start + s[i:])
}

func srecFile() []byte {
	var b bytes.Buffer
	codec.WriteSRecord(&b, []codec.Segment{{Addr: 0x1000, Data: []byte("0123456789abcdef!")}}, 16, "hdr")
	return b.Bytes()
}

// x86Boot is a boot sector: jmp short, nops, 55 AA
func x86Boot() []byte {
	b := make([]byte, 512)
	b[0], b[1] = 0xEB, 0x3C
	for i := 2; i < 510; i++ {
		b[i] = 0x90
	}
	b[510], b[511] = 0x55, 0xAA
	return b
}
