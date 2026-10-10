package disasm

import (
	"fmt"
	"strconv"
	"strings"
)

// The BIOS services of boot code by interrupt and AH, for the notes of
// int instructions (BOOT-4)
var biosCalls = map[int]map[int]string{
	0x10: {0x00: "set video mode", 0x01: "set cursor shape", 0x02: "set cursor position", 0x03: "get cursor position",
		0x06: "scroll up", 0x07: "scroll down", 0x08: "read character", 0x09: "write character and attribute",
		0x0A: "write character", 0x0C: "write pixel", 0x0E: "teletype output", 0x0F: "get video mode", 0x13: "write string",
		0x4F: "VESA BIOS extensions"},
	0x11: {-1: "get equipment list"},
	0x12: {-1: "get memory size"},
	0x13: {0x00: "reset disk", 0x01: "get disk status", 0x02: "read sectors (CHS)", 0x03: "write sectors (CHS)",
		0x04: "verify sectors", 0x08: "get drive parameters", 0x15: "get disk type", 0x41: "check extensions present",
		0x42: "extended read (LBA)", 0x43: "extended write (LBA)", 0x48: "extended get drive parameters"},
	0x14: {-1: "serial port services"},
	0x15: {0x24: "A20 gate", 0x86: "wait", 0x87: "move extended memory", 0x88: "get extended memory size",
		0xC0: "get system configuration", 0xE8: "memory map (E820/E801)"},
	0x16: {0x00: "read key", 0x01: "check for a key", 0x02: "get shift flags", 0x10: "read extended key"},
	0x17: {-1: "printer services"},
	0x18: {-1: "start ROM BASIC / boot failed"},
	0x19: {-1: "reboot (bootstrap loader)"},
	0x1A: {0x00: "read the tick count", 0x02: "read the RTC time", 0x04: "read the RTC date", 0xBB: "TPM services"},
	0x21: {0x09: "DOS: print string", 0x4C: "DOS: exit", 0x02: "DOS: print character", 0x3D: "DOS: open file", 0x3F: "DOS: read file"},
}

// annotate notes what int instructions call: the AH of the last "mov ah,
// imm" or "mov ax, imm" before them in the listing
func annotate(insts []Inst) {
	ah := -1
	for i := range insts {
		in := &insts[i]
		if in.Mnemonic == "mov" {
			dst, src, ok := strings.Cut(in.Operands, ", ")
			if ok {
				if v, err := strconv.ParseInt(strings.TrimPrefix(src, "0x"), 16, 64); err == nil && strings.HasPrefix(src, "0x") {
					switch dst {
					case "ah":
						ah = int(v & 0xFF)
					case "ax":
						ah = int(v>>8) & 0xFF
					}
				}
			}
		}
		if in.Mnemonic == "int" {
			n, err := strconv.ParseInt(strings.TrimPrefix(in.Operands, "0x"), 16, 64)
			if err != nil {
				continue
			}
			calls := biosCalls[int(n)]
			if name, ok := calls[-1]; ok {
				in.Comment = name
			} else if name, ok := calls[ah]; ok && ah >= 0 {
				in.Comment = name
			} else if calls != nil && ah >= 0 {
				in.Comment = fmt.Sprintf("BIOS 0x%02X, AH=0x%02X", n, ah)
			}
		}
		// A jump or a call ends what is known of AH
		if in.Kind == Jump || in.Kind == Call || in.Kind == Return {
			ah = -1
		}
	}
}
