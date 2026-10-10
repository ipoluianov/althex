package fw

import (
	"fmt"
	"io"
	"sort"
	"strings"
)

// UF2 (Microsoft's USB flashing format): 512-byte blocks, each with the
// address and up to 476 bytes of payload
const (
	uf2Magic0   = 0x0A324655 // "UF2\n"
	uf2Magic1   = 0x9E5D5157
	uf2MagicEnd = 0x0AB16F30

	uf2NotMainFlash   = 0x00000001
	uf2FileContainer  = 0x00001000
	uf2FamilyPresent  = 0x00002000
	uf2MD5Present     = 0x00004000
	uf2ExtensionTags  = 0x00008000
	uf2MaxBlocks      = 1 << 22 // 2 GiB of blocks
	uf2MaxPayload     = 476
	uf2BlockSize      = 512
	uf2PayloadOffset  = 32
	uf2DefaultPayload = 256
)

// UF2Family is a family ID of UF2
type UF2Family struct {
	ID   uint32
	Name string
	Arch string // the disasm ID
}

// UF2Families are the family IDs of uf2families.json
var UF2Families = []UF2Family{
	{0x16573617, "ATmega32", "avr"},
	{0x1851780a, "SAML21", "arm"},
	{0x1b57745f, "nRF52", "arm"},
	{0x1c5f21b0, "ESP32", "xtensa"},
	{0x1e1f432d, "STM32L1", "arm"},
	{0x202e3a91, "STM32L0", "arm"},
	{0x21460ff0, "STM32WL", "arm"},
	{0x2abc77ec, "LPC55", "arm"},
	{0x300f5633, "STM32G0", "arm"},
	{0x31d228c6, "GD32F350", "arm"},
	{0x04240bdf, "STM32L5", "arm"},
	{0x4c71240a, "STM32G4", "arm"},
	{0x4fb2d5bd, "i.MX RT10xx", "arm"},
	{0x53b80f00, "STM32F7", "arm"},
	{0x55114460, "SAMD51", "arm"},
	{0x57755a57, "STM32F4", "arm"},
	{0x5a18069b, "Cypress FX2", ""},
	{0x5d1a0a2e, "STM32F2", "arm"},
	{0x5ee21072, "STM32F1", "arm"},
	{0x621e937a, "nRF52833", "arm"},
	{0x647824b6, "STM32F0", "arm"},
	{0x68ed2b88, "SAMD21", "arm"},
	{0x6b846188, "STM32F3", "arm"},
	{0x6d0922fa, "STM32F407", "arm"},
	{0x6db66082, "STM32H7", "arm"},
	{0x70d16653, "STM32WB", "arm"},
	{0x7eab61ed, "ESP8266", "xtensa"},
	{0x7f83e793, "KL32L2", "arm"},
	{0x8fb060fe, "STM32F407VG", "arm"},
	{0xada52840, "nRF52840", "arm"},
	{0xbfdd4eee, "ESP32-S2", "xtensa"},
	{0xc47e5767, "ESP32-S3", "xtensa"},
	{0xd42ba06c, "ESP32-C3", "riscv32"},
	{0x2b88d29c, "ESP32-C2", "riscv32"},
	{0x332726f6, "ESP32-H2", "riscv32"},
	{0x540ddf62, "ESP32-C6", "riscv32"},
	{0x3d308e94, "ESP32-P4", "riscv32"},
	{0xe48bff56, "RP2040", "arm"},
	{0xe48bff57, "RP2XXX absolute", ""},
	{0xe48bff58, "RP2XXX data", ""},
	{0xe48bff59, "RP2350 Arm Secure", "arm"},
	{0xe48bff5a, "RP2350 RISC-V", "riscv32"},
	{0xe48bff5b, "RP2350 Arm Non-Secure", "arm"},
	{0x00ff6919, "STM32L4", "arm"},
	{0x9af03e33, "GD32VF103", "riscv32"},
	{0x4f6ace52, "CSK4", ""},
	{0x6e7348a8, "CSK6", ""},
	{0x11de784a, "M0SENSE", "arm"},
}

// UF2FamilyName returns the name of a family ID; its hex if it is not known
func UF2FamilyName(id uint32) string {
	if f, ok := uf2Family(id); ok {
		return f.Name
	}
	return fmt.Sprintf("0x%08X", id)
}

func uf2Family(id uint32) (UF2Family, bool) {
	for _, f := range UF2Families {
		if f.ID == id {
			return f, true
		}
	}
	return UF2Family{}, false
}

type uf2Chunk struct {
	family uint32
	addr   uint64
	off    int64 // of the payload in the file
	data   []byte
}

func openUF2(r io.ReaderAt, size int64) (*Image, error) {
	im := &Image{kind: kindUF2}
	nblocks := size / uf2BlockSize
	if nblocks > uf2MaxBlocks {
		nblocks = uf2MaxBlocks
	}
	if size%uf2BlockSize != 0 {
		im.issue("the size %d is not a multiple of 512: %d bytes at the end are not a block", size, size%uf2BlockSize)
	}
	var chunks []uf2Chunk
	var families []uint32
	famSeen := map[uint32]bool{}
	type fileKey struct{ fam, total uint32 }
	seenNo := map[fileKey]map[uint32]bool{}
	skipped, bad := 0, 0
	b := make([]byte, uf2BlockSize)
	for i := int64(0); i < nblocks; i++ {
		off := i * uf2BlockSize
		if n, _ := r.ReadAt(b, off); n != uf2BlockSize {
			break
		}
		if le32(b) != uf2Magic0 || le32(b[4:]) != uf2Magic1 || le32(b[508:]) != uf2MagicEnd {
			bad++
			continue
		}
		flags, addr, psize := le32(b[8:]), le32(b[12:]), le32(b[16:])
		blockNo, numBlocks, fam := le32(b[20:]), le32(b[24:]), le32(b[28:])
		if psize > uf2MaxPayload {
			im.issue("block %d: payload of %d bytes (at most 476)", i, psize)
			bad++
			continue
		}
		if flags&uf2NotMainFlash != 0 || flags&uf2FileContainer != 0 {
			skipped++
			continue
		}
		if flags&uf2FamilyPresent == 0 {
			fam = 0
		}
		if !famSeen[fam] {
			famSeen[fam] = true
			families = append(families, fam)
		}
		k := fileKey{fam, numBlocks}
		if seenNo[k] == nil {
			seenNo[k] = map[uint32]bool{}
		}
		if blockNo >= numBlocks {
			im.issue("block %d: number %d of %d", i, blockNo, numBlocks)
		} else if seenNo[k][blockNo] {
			im.issue("block %d: number %d repeats", i, blockNo)
		}
		seenNo[k][blockNo] = true
		chunks = append(chunks, uf2Chunk{fam, uint64(addr), off + uf2PayloadOffset,
			append([]byte(nil), b[uf2PayloadOffset:uf2PayloadOffset+psize]...)})
	}
	if len(chunks) == 0 && skipped == 0 {
		return nil, fmt.Errorf("%w: UF2 without blocks", ErrFormat)
	}
	for k, got := range seenNo {
		if uint32(len(got)) != k.total && k.total < uf2MaxBlocks {
			im.issue("%s: %d blocks of %d", famLabel(k.fam), len(got), k.total)
		}
	}
	if bad > 0 {
		im.issue("%d blocks with wrong magic numbers", bad)
	}

	var names []string
	for _, f := range families {
		names = append(names, famLabel(f))
	}
	switch len(families) {
	case 0:
		im.Format = "UF2"
	case 1:
		im.Format = "UF2 (family " + names[0] + ")"
	default:
		im.Format = "UF2 (families " + strings.Join(names, ", ") + ")"
	}
	// The architecture when all families agree on it
	for i, f := range families {
		uf, _ := uf2Family(f)
		if i == 0 {
			im.Arch, im.Family = uf.Arch, uf.Name
		} else if uf.Arch != im.Arch {
			im.Arch = ""
		}
		if uf.Name != im.Family {
			im.Family = ""
		}
	}
	im.info("Blocks", "%d", len(chunks)+skipped)
	if skipped > 0 {
		im.info("Not main flash / file container blocks", "%d (skipped)", skipped)
	}

	sort.SliceStable(chunks, func(i, j int) bool {
		if chunks[i].family != chunks[j].family {
			return famIndex(families, chunks[i].family) < famIndex(families, chunks[j].family)
		}
		return chunks[i].addr < chunks[j].addr
	})
	for i := 0; i < len(chunks); {
		c := chunks[i]
		data := append([]byte(nil), c.data...)
		parts := []part{{c.addr, c.off, int64(len(c.data))}}
		j := i + 1
		for ; j < len(chunks) && chunks[j].family == c.family; j++ {
			end := c.addr + uint64(len(data))
			d := chunks[j]
			if d.addr < end {
				im.issue("%s: the block at 0x%X overlaps the data before it", famLabel(c.family), d.addr)
				continue
			}
			if d.addr != end {
				break
			}
			data = append(data, d.data...)
			parts = append(parts, part{d.addr, d.off, int64(len(d.data))})
		}
		name := "flash"
		if len(families) > 1 {
			name = famLabel(c.family)
		}
		s := memSeg(name, c.addr, data, true)
		s.parts = parts
		im.Segments = append(im.Segments, s)
		i = j
	}
	return im, nil
}

func famIndex(fams []uint32, f uint32) int {
	for i, x := range fams {
		if x == f {
			return i
		}
	}
	return -1
}

func famLabel(f uint32) string {
	if f == 0 {
		return "no family"
	}
	return UF2FamilyName(f)
}
