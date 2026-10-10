package fw

import (
	"bufio"
	"debug/elf"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Symbol is a name of an address from a symbol table or a map file
type Symbol struct {
	Name  string
	Addr  uint64
	Size  uint64
	Func  bool // code: an entry for the analysis
	Thumb bool // ARM code in Thumb mode
}

// elfSymbols reads the symbol table of an ELF file: functions, objects and
// the global labels of assembler code; the ARM mapping symbols ($t, $d)
// and local labels are left out
func elfSymbols(f *elf.File) []Symbol {
	syms, err := f.Symbols()
	if err != nil {
		return nil
	}
	var out []Symbol
	for _, s := range syms {
		if s.Name == "" || s.Section == elf.SHN_UNDEF || s.Section == elf.SHN_ABS && s.Value == 0 {
			continue
		}
		if strings.HasPrefix(s.Name, "$") || strings.HasPrefix(s.Name, ".L") {
			continue
		}
		typ, bind := elf.ST_TYPE(s.Info), elf.ST_BIND(s.Info)
		var isFunc bool
		switch typ {
		case elf.STT_FUNC:
			isFunc = true
		case elf.STT_OBJECT:
		case elf.STT_NOTYPE:
			if bind != elf.STB_GLOBAL && bind != elf.STB_WEAK {
				continue
			}
			isFunc = sectionExec(f, s.Section)
		default:
			continue
		}
		sym := Symbol{Name: s.Name, Addr: s.Value, Size: s.Size, Func: isFunc}
		if f.Machine == elf.EM_ARM && isFunc && sym.Addr&1 != 0 {
			sym.Addr &^= 1
			sym.Thumb = true
		}
		out = append(out, sym)
	}
	return sortSymbols(out)
}

func sectionExec(f *elf.File, idx elf.SectionIndex) bool {
	if int(idx) <= 0 || int(idx) >= len(f.Sections) {
		return false
	}
	return f.Sections[idx].Flags&elf.SHF_EXECINSTR != 0
}

// sortSymbols orders the symbols by address, one per address: a function
// wins over a data name, a global name over a later one
func sortSymbols(syms []Symbol) []Symbol {
	sort.SliceStable(syms, func(i, j int) bool { return syms[i].Addr < syms[j].Addr })
	out := syms[:0]
	for _, s := range syms {
		if n := len(out); n > 0 && out[n-1].Addr == s.Addr {
			if s.Func && !out[n-1].Func {
				out[n-1] = s
			}
			continue
		}
		out = append(out, s)
	}
	return out
}

// mapLine is a symbol line of a GNU ld map file: "    0x08000100    Reset_Handler"
var mapLine = regexp.MustCompile(`^\s+0x([0-9a-fA-F]+)\s+([A-Za-z_.$][\w.$@]*)\s*$`)

// mapSection is a section line: " .text  0x08000100  0x1a4 main.o"
var mapSection = regexp.MustCompile(`^\s*(\.[\w.]+)?\s+0x[0-9a-fA-F]+\s+0x[0-9a-fA-F]+\s`)

// ParseMap reads the symbols of a GNU ld map file (FW-12). It does not know
// which names are code: those in .text sections are taken as functions.
func ParseMap(r io.Reader) []Symbol {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	var out []Symbol
	inText, started := false, false
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "Linker script and memory map") {
			started = true
			continue
		}
		if !started {
			continue
		}
		if m := mapSection.FindStringSubmatch(line); m != nil {
			if m[1] != "" {
				inText = strings.HasPrefix(m[1], ".text") || strings.HasPrefix(m[1], ".isr_vector") || strings.HasPrefix(m[1], ".init") || strings.HasPrefix(m[1], ".vectors")
			}
			continue
		}
		if strings.HasPrefix(line, ".") || strings.HasPrefix(line, " .") {
			f := strings.Fields(line)
			inText = strings.HasPrefix(f[0], ".text") || strings.HasPrefix(f[0], ".isr_vector") || strings.HasPrefix(f[0], ".vectors")
		}
		m := mapLine.FindStringSubmatch(line)
		if m == nil || strings.Contains(m[2], "=") {
			continue
		}
		addr, err := strconv.ParseUint(m[1], 16, 64)
		if err != nil || addr == 0 {
			continue
		}
		out = append(out, Symbol{Name: m[2], Addr: addr, Func: inText})
	}
	return sortSymbols(out)
}
