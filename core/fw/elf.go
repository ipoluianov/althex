package fw

import (
	"debug/elf"
	"fmt"
	"io"
	"strings"
)

// elfArch maps the machine of an ELF file to the disasm ID and a name
func elfArch(f *elf.File) (id, name string) {
	switch f.Machine {
	case elf.EM_ARM:
		return "arm", "ARM"
	case elf.EM_AARCH64:
		return "arm64", "AArch64"
	case elf.EM_AVR:
		return "avr", "AVR"
	case elf.EM_RISCV:
		if f.Class == elf.ELFCLASS64 {
			return "riscv64", "RISC-V"
		}
		return "riscv32", "RISC-V"
	case elf.EM_XTENSA:
		return "xtensa", "Xtensa"
	case elf.EM_386:
		return "x86-32", "x86"
	case elf.EM_X86_64:
		return "x86-64", "x86-64"
	}
	if f.Machine == 0x18AD {
		return "", "AVR32" // not in debug/elf
	}
	name = f.Machine.String()
	if strings.Contains(name, "+") { // debug/elf does not know it: "ALPHA_STD+6276"
		return "", fmt.Sprintf("machine %d", uint16(f.Machine))
	}
	return "", strings.TrimPrefix(name, "EM_")
}

// avrSpace names the memory spaces of AVR ELF files (avr-libc's addresses)
func avrSpace(addr uint64) string {
	switch {
	case addr >= 0x840000 && addr < 0x850000:
		return "signature"
	case addr >= 0x830000:
		return "lock"
	case addr >= 0x820000:
		return "fuse"
	case addr >= 0x810000:
		return "eeprom"
	}
	return ""
}

func openELF(r io.ReaderAt, size int64) (*Image, error) {
	f, err := elf.NewFile(io.NewSectionReader(r, 0, size))
	if err != nil {
		return nil, fmt.Errorf("%w: ELF: %v", ErrFormat, err)
	}
	id, mname := elfArch(f)
	bits := 32
	if f.Class == elf.ELFCLASS64 {
		bits = 64
	}
	im := &Image{Format: fmt.Sprintf("ELF (%s, %d-bit)", mname, bits), Arch: id, kind: kindELF}
	im.info("Type", "%s", f.Type)
	im.info("Machine", "%s", f.Machine)
	im.Entry, im.HasEntry = f.Entry, f.Type == elf.ET_EXEC || f.Entry != 0
	if f.Machine == elf.EM_ARM && f.Entry&1 != 0 {
		im.Entry &^= 1
		im.info("Entry", "0x%X (Thumb)", im.Entry)
	} else {
		im.info("Entry", "0x%X", im.Entry)
	}

	// Sections give the segments their names
	secName := func(off, n uint64) string {
		var names []string
		for _, s := range f.Sections {
			if s.Type == elf.SHT_NOBITS || s.Size == 0 || s.Flags&elf.SHF_ALLOC == 0 {
				continue
			}
			if s.Offset >= off && s.Offset < off+n {
				names = append(names, s.Name)
			}
		}
		if len(names) > 3 {
			names = append(names[:3], "...")
		}
		return strings.Join(names, " ")
	}

	for i, p := range f.Progs {
		if p.Type != elf.PT_LOAD || p.Filesz == 0 {
			continue
		}
		if p.Off > uint64(size) || p.Filesz > uint64(size)-p.Off {
			im.issue("segment %d: %d bytes at offset 0x%X go beyond the file", i, p.Filesz, p.Off)
			continue
		}
		name := secName(p.Off, p.Filesz)
		if f.Machine == elf.EM_AVR {
			if sp := avrSpace(p.Paddr); sp != "" {
				name = sp
			}
		}
		if name == "" {
			name = fmt.Sprintf("segment %d", i)
		}
		s := fileSeg(name, p.Paddr, r, int64(p.Off), int64(p.Filesz), p.Flags&elf.PF_X != 0)
		im.Segments = append(im.Segments, s)
		note := ""
		if p.Vaddr != p.Paddr {
			note = fmt.Sprintf(", runs at 0x%X (VMA)", p.Vaddr)
		}
		if p.Memsz > p.Filesz {
			note += fmt.Sprintf(", +%d bytes of zeros", p.Memsz-p.Filesz)
		}
		im.info(fmt.Sprintf("Segment %d", i), "%s: LMA 0x%X, %d bytes, %s%s", name, p.Paddr, p.Filesz, p.Flags, note)
	}
	if len(im.Segments) == 0 {
		// An object file: its allocated sections at their addresses
		for _, s := range f.Sections {
			if s.Type != elf.SHT_PROGBITS || s.Flags&elf.SHF_ALLOC == 0 || s.Size == 0 {
				continue
			}
			if s.Offset > uint64(size) || s.Size > uint64(size)-s.Offset {
				im.issue("section %s goes beyond the file", s.Name)
				continue
			}
			im.Segments = append(im.Segments, fileSeg(s.Name, s.Addr, r, int64(s.Offset), int64(s.Size), s.Flags&elf.SHF_EXECINSTR != 0))
		}
	}
	im.Symbols = elfSymbols(f)
	if len(im.Symbols) > 0 {
		im.info("Symbols", "%d", len(im.Symbols))
	}
	return im, nil
}
