package disasm

import (
	"fmt"
	"io"
	"strings"
)

// BootSectorAddr is where the BIOS loads a boot sector
const BootSectorAddr = 0x7C00

// BootSector lists the x86 real mode code of a boot sector (an MBR or a
// VBR) at off in r, loaded at 0x7C00 (BOOT-4). The listing follows the
// first jump of a VBR (EB xx 90, E9 xx xx): the bytes it jumps over are
// the BIOS parameter block and are shown as data, not as code. The
// partition table of an MBR, the disk signature and the 55 AA at the end
// are data too.
func BootSector(r io.ReaderAt, off int64) []Inst {
	sector := make([]byte, 512)
	n, _ := r.ReadAt(sector, off)
	sector = sector[:n]
	if n == 0 {
		return nil
	}
	a := ByID("x86-16")
	end := n // where the code ends
	signature := n == 512 && sector[510] == 0x55 && sector[511] == 0xAA
	if signature {
		end = 510
	}

	var out []Inst
	code := func(from, to int) {
		if from >= to {
			return
		}
		insts := Linear(a, readerAt(sector[:to]), int64(from), BootSectorAddr+uint64(from), to-from)
		out = append(out, insts...)
	}
	data := func(from, to int, comment string) {
		for i := from; i < to; i += 8 {
			j := min(i+8, to)
			var ops []string
			for _, c := range sector[i:j] {
				ops = append(ops, fmt.Sprintf("0x%02X", c))
			}
			out = append(out, Inst{Addr: BootSectorAddr + uint64(i), Len: j - i, Bytes: append([]byte(nil), sector[i:j]...),
				Mnemonic: "db", Operands: strings.Join(ops, ", "), Kind: Invalid, Comment: comment})
			comment = ""
		}
	}

	// The jump over the BPB of a VBR
	start, bpbEnd := 0, 0
	switch {
	case n >= 3 && sector[0] == 0xEB:
		target := 2 + int(int8(sector[1]))
		if target > 2 && target < end {
			start, bpbEnd = 2, target
			if sector[2] == 0x90 && target > 3 {
				start = 3
			}
		}
	case n >= 3 && sector[0] == 0xE9:
		target := 3 + int(int16(uint16(sector[1])|uint16(sector[2])<<8))
		if target > 3 && target < end {
			start, bpbEnd = 3, target
		}
	}
	if bpbEnd > 0 {
		code(0, start) // the jmp and its nop
		data(start, bpbEnd, bpbComment(sector))
		start = bpbEnd
	} else if signature && mbrTable(sector) {
		// An MBR: the code ends at the disk signature
		end = 0x1B8
	}
	code(start, end)
	if end == 0x1B8 {
		data(0x1B8, 0x1BC, "disk signature")
		data(0x1BC, 0x1BE, "")
		for i := range 4 {
			data(0x1BE+16*i, 0x1CE+16*i, fmt.Sprintf("partition entry %d", i+1))
		}
	}
	if signature {
		out = append(out, Inst{Addr: BootSectorAddr + 510, Len: 2, Bytes: []byte{0x55, 0xAA},
			Mnemonic: "dw", Operands: "0xAA55", Kind: Invalid, Comment: "boot signature"})
	}
	annotate(out)
	return out
}

// bpbComment names what the jump skips
func bpbComment(s []byte) string {
	if len(s) >= 11 {
		oem := strings.TrimRight(string(s[3:11]), " \x00")
		printable := oem != ""
		for _, c := range oem {
			if c < 0x20 || c > 0x7E {
				printable = false
			}
		}
		if printable {
			return "BIOS parameter block (OEM \"" + oem + "\")"
		}
	}
	return "BIOS parameter block (data)"
}

// mbrTable tells whether the sector has a partition table: the status of
// every entry is 0x00 or 0x80, and one at least is used
func mbrTable(s []byte) bool {
	used := false
	for i := range 4 {
		e := s[0x1BE+16*i:]
		if e[0] != 0 && e[0] != 0x80 {
			return false
		}
		if e[4] != 0 {
			used = true
		}
	}
	return used
}

type readerAt []byte

func (b readerAt) ReadAt(p []byte, off int64) (int, error) {
	if off >= int64(len(b)) {
		return 0, io.EOF
	}
	n := copy(p, b[off:])
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}
