package tmpl

import (
	"bytes"
	"context"
	"debug/elf"
	"debug/macho"
	"debug/pe"
	"encoding/base64"
	"encoding/binary"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The templates of executables: ELF, PE, Mach-O. The samples are made here
// and checked with the standard library (debug/elf, debug/pe, debug/macho)
// before the templates are compared with it; real files of the system are
// run too when there are some.

// execTemplate returns a built-in template by its file name
func execTemplate(t *testing.T, file string) *Program {
	t.Helper()
	for _, tm := range Library() {
		if tm.File == file {
			p, err := tm.Program()
			if err != nil {
				t.Fatalf("%s: %v", file, err)
			}
			return p
		}
	}
	t.Fatalf("no template %s", file)
	return nil
}

const execMaxSteps = 50_000_000

// execRun runs the template over the data; the run must end without an error
func execRun(t *testing.T, file string, data []byte) *Result {
	t.Helper()
	p := execTemplate(t, file)
	res := p.Run(context.Background(), Env{Data: &mem{b: data}, MaxSteps: execMaxSteps})
	if res.Err != nil {
		t.Fatalf("%s: %v", file, res.Err)
	}
	return res
}

// execDetect checks the template chosen for the data
func execDetect(t *testing.T, data []byte, name, want string) {
	t.Helper()
	tm := Detect(Library(), &mem{b: data}, name)
	if tm == nil || tm.File != want {
		got := "none"
		if tm != nil {
			got = tm.File
		}
		t.Fatalf("%s: detected %s, want %s", name, got, want)
	}
}

// num returns the number of a field
func execNum(t *testing.T, res *Result, path string) uint64 {
	t.Helper()
	n := res.Root.Find(path)
	if n == nil {
		t.Fatalf("no field %s", path)
	}
	v, ok := n.val.AsInt()
	if !ok {
		t.Fatalf("%s is not a number: %s", path, n.Value())
	}
	return uint64(v)
}

// shown returns the value shown for a field (a read= function makes it)
func execShown(t *testing.T, res *Result, path string) string {
	t.Helper()
	n := res.Root.Find(path)
	if n == nil {
		t.Fatalf("no field %s", path)
	}
	return n.Value()
}

// truncated runs the template over the data cut at several points: no
// panic, an end in time; errors are allowed only before the main header
func execTruncated(t *testing.T, file string, data []byte, points int) {
	t.Helper()
	p := execTemplate(t, file)
	step := max(len(data)/points, 1)
	for n := 0; n < len(data); n += step {
		cut := append([]byte(nil), data[:n]...)
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("%s cut at %d: panic %v", file, n, r)
				}
			}()
			start := time.Now()
			res := p.Run(context.Background(), Env{Data: &mem{b: cut}, MaxSteps: execMaxSteps})
			if res.Err != nil {
				t.Errorf("%s cut at %d: %v", file, n, res.Err)
			}
			if d := time.Since(start); d > 5*time.Second {
				t.Errorf("%s cut at %d: %v", file, n, d)
			}
		}()
	}
}

// corrupted runs the template over copies of the data with bytes changed
// at many places: no panic, an end in time
func execCorrupted(t *testing.T, file string, data []byte) {
	t.Helper()
	p := execTemplate(t, file)
	for i := 0; i < len(data) && i < 4096; i++ {
		for _, v := range []byte{0xFF, 0x00, 0x80} {
			b := append([]byte(nil), data...)
			b[i] = v
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Fatalf("%s with byte %d = %X: panic %v", file, i, v, r)
					}
				}()
				res := p.Run(context.Background(), Env{Data: &mem{b: b}, MaxSteps: execMaxSteps})
				if res.Duration > 5*time.Second {
					t.Errorf("%s with byte %d = %X: %v", file, i, v, res.Duration)
				}
			}()
		}
	}
}

// ---- ELF ----

type elfSec struct {
	name    string
	typ     uint32
	flags   uint64
	data    []byte
	link    uint32
	info    uint32
	entsize uint64
}

// makeELF makes an executable with a PT_LOAD program header and the
// sections (after the null one), with .shstrtab last
func makeELF(is64 bool, bo binary.AppendByteOrder, machine uint16, entry uint64, secs []elfSec) []byte {
	ehsize, phentsize, shentsize := 52, 32, 40
	if is64 {
		ehsize, phentsize, shentsize = 64, 56, 64
	}
	// .shstrtab
	shstr := []byte{0}
	nameOff := make([]uint32, len(secs)+1)
	for i, s := range secs {
		nameOff[i] = uint32(len(shstr))
		shstr = append(append(shstr, s.name...), 0)
	}
	nameOff[len(secs)] = uint32(len(shstr))
	shstr = append(append(shstr, ".shstrtab"...), 0)
	secs = append(secs, elfSec{name: ".shstrtab", typ: uint32(elf.SHT_STRTAB), data: shstr})

	var buf bytes.Buffer
	buf.Write(make([]byte, ehsize+phentsize))
	offs := make([]uint64, len(secs))
	for i, s := range secs {
		for buf.Len()%8 != 0 {
			buf.WriteByte(0)
		}
		offs[i] = uint64(buf.Len())
		buf.Write(s.data)
	}
	for buf.Len()%8 != 0 {
		buf.WriteByte(0)
	}
	shoff := uint64(buf.Len())
	buf.Write(make([]byte, shentsize)) // the null section
	put := func(b []byte, v64 bool, v uint64) []byte {
		if v64 {
			return bo.AppendUint64(b, v)
		}
		return bo.AppendUint32(b, uint32(v))
	}
	for i, s := range secs {
		var h []byte
		h = bo.AppendUint32(h, nameOff[i])
		h = bo.AppendUint32(h, s.typ)
		h = put(h, is64, s.flags)
		h = put(h, is64, 0x400000+offs[i]) // sh_addr
		h = put(h, is64, offs[i])
		h = put(h, is64, uint64(len(s.data)))
		h = bo.AppendUint32(h, s.link)
		h = bo.AppendUint32(h, s.info)
		h = put(h, is64, 1)
		h = put(h, is64, s.entsize)
		buf.Write(h)
	}
	b := buf.Bytes()

	// The header
	copy(b, "\x7fELF")
	b[4] = 1
	if is64 {
		b[4] = 2
	}
	b[5] = 1
	if bo == binary.AppendByteOrder(binary.BigEndian) {
		b[5] = 2
	}
	b[6] = 1
	h := bo.AppendUint16(nil, uint16(elf.ET_EXEC))
	h = bo.AppendUint16(h, machine)
	h = bo.AppendUint32(h, 1)
	h = put(h, is64, entry)
	h = put(h, is64, uint64(ehsize)) // e_phoff
	h = put(h, is64, shoff)
	h = bo.AppendUint32(h, 0)
	h = bo.AppendUint16(h, uint16(ehsize))
	h = bo.AppendUint16(h, uint16(phentsize))
	h = bo.AppendUint16(h, 1)
	h = bo.AppendUint16(h, uint16(shentsize))
	h = bo.AppendUint16(h, uint16(len(secs)+1))
	h = bo.AppendUint16(h, uint16(len(secs))) // .shstrtab
	copy(b[16:], h)

	// PT_LOAD of the whole file, R-X
	var ph []byte
	ph = bo.AppendUint32(ph, uint32(elf.PT_LOAD))
	if is64 {
		ph = bo.AppendUint32(ph, 5)
		ph = bo.AppendUint64(ph, 0)
		ph = bo.AppendUint64(ph, 0x400000)
		ph = bo.AppendUint64(ph, 0x400000)
		ph = bo.AppendUint64(ph, uint64(len(b)))
		ph = bo.AppendUint64(ph, uint64(len(b)))
		ph = bo.AppendUint64(ph, 0x1000)
	} else {
		ph = bo.AppendUint32(ph, 0)
		ph = bo.AppendUint32(ph, 0x400000)
		ph = bo.AppendUint32(ph, 0x400000)
		ph = bo.AppendUint32(ph, uint32(len(b)))
		ph = bo.AppendUint32(ph, uint32(len(b)))
		ph = bo.AppendUint32(ph, 5)
		ph = bo.AppendUint32(ph, 0x1000)
	}
	copy(b[ehsize:], ph)
	return b
}

func makeELF64() []byte {
	code := []byte{0x48, 0x31, 0xff, 0xb8, 0x3c, 0, 0, 0, 0x0f, 0x05}
	return makeELF(true, binary.LittleEndian, uint16(elf.EM_X86_64), 0x401000, []elfSec{
		{name: ".text", typ: uint32(elf.SHT_PROGBITS), flags: 6, data: code},
		{name: ".data", typ: uint32(elf.SHT_PROGBITS), flags: 3, data: []byte("hello, world\n")},
	})
}

// makeELF32BE has a symbol table: .text, .symtab, .strtab (+ .shstrtab)
func makeELF32BE() []byte {
	bo := binary.BigEndian
	strtab := []byte("\x00main\x00counter\x00")
	sym := func(name, value, size uint32, info byte, shndx uint16) []byte {
		b := bo.AppendUint32(nil, name)
		b = bo.AppendUint32(b, value)
		b = bo.AppendUint32(b, size)
		b = append(b, info, 0)
		return bo.AppendUint16(b, shndx)
	}
	var symtab []byte
	symtab = append(symtab, sym(0, 0, 0, 0, 0)...)
	symtab = append(symtab, sym(1, 0x10000100, 16, byte(elf.STB_GLOBAL)<<4|byte(elf.STT_FUNC), 1)...)
	symtab = append(symtab, sym(6, 0x10000200, 4, byte(elf.STB_LOCAL)<<4|byte(elf.STT_OBJECT), 1)...)
	return makeELF(false, bo, uint16(elf.EM_PPC), 0x10000100, []elfSec{
		{name: ".text", typ: uint32(elf.SHT_PROGBITS), flags: 6, data: make([]byte, 32)},
		{name: ".symtab", typ: uint32(elf.SHT_SYMTAB), data: symtab, link: 3, info: 2, entsize: 16},
		{name: ".strtab", typ: uint32(elf.SHT_STRTAB), data: strtab},
	})
}

// elfCompare checks the template's fields against debug/elf
func elfCompare(t *testing.T, res *Result, f *elf.File) {
	t.Helper()
	if e := execNum(t, res, "ehdr.e_entry"); e != f.Entry {
		t.Errorf("e_entry 0x%X, debug/elf 0x%X", e, f.Entry)
	}
	if m := execNum(t, res, "ehdr.e_machine"); elf.Machine(m) != f.Machine {
		t.Errorf("e_machine %d, debug/elf %v", m, f.Machine)
	}
	if n := execNum(t, res, "ehdr.e_shnum"); int(n) != len(f.Sections) && n != 0 {
		t.Errorf("e_shnum %d, debug/elf %d sections", n, len(f.Sections))
	}
	if len(f.Sections) > 4096 {
		return
	}
	sh := res.Root.Find("section_headers.shdr")
	if len(f.Sections) == 0 {
		return
	}
	if sh == nil || sh.ChildCount() != int64(len(f.Sections)) {
		t.Fatalf("section_headers.shdr: %v, debug/elf %d sections", sh, len(f.Sections))
	}
	for i, s := range f.Sections {
		if i == 0 {
			continue
		}
		got := execShown(t, res, "section_headers.shdr["+strconv.Itoa(i)+"]")
		name := strings.SplitN(got, " ", 2)[0]
		if s.Name != "" && name != s.Name {
			t.Errorf("section %d: %q, debug/elf %q", i, got, s.Name)
		}
		if typ := execNum(t, res, "section_headers.shdr["+strconv.Itoa(i)+"].sh_type"); elf.SectionType(typ) != s.Type {
			t.Errorf("section %d: sh_type %d, debug/elf %v", i, typ, s.Type)
		}
	}
	if pn := res.Root.Find("program_headers.phdr"); len(f.Progs) > 1 && (pn == nil || pn.ChildCount() != int64(len(f.Progs))) {
		t.Errorf("program headers %v, debug/elf %d", pn, len(f.Progs))
	}
}

func TestExecELF64(t *testing.T) {
	data := makeELF64()
	f, err := elf.NewFile(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("debug/elf: %v", err)
	}
	if len(f.Sections) != 4 || f.Sections[1].Name != ".text" || f.Sections[3].Name != ".shstrtab" {
		t.Fatalf("debug/elf sees %v", f.Sections)
	}
	execDetect(t, data, "a.out", "elf.bt")
	res := execRun(t, "elf.bt", data)
	elfCompare(t, res, f)
	if v := execShown(t, res, "ehdr.e_ident.ei_class"); v != "ELFCLASS64 (2)" {
		t.Errorf("ei_class %s", v)
	}
	if v := execShown(t, res, "ehdr.e_machine"); !strings.HasPrefix(v, "EM_X86_64") {
		t.Errorf("e_machine %s", v)
	}
	if v := execShown(t, res, "section_headers.shdr[1]"); v != ".text 0xA bytes at 0x78" {
		t.Errorf("section 1 %q", v)
	}
	if v := execShown(t, res, "program_headers.phdr"); v != "PT_LOAD R-X 0x"+strings.ToUpper(strconv.FormatUint(uint64(len(data)), 16))+" bytes at 0x0" {
		t.Errorf("program header %q", v)
	}
	if c := res.Root.Find("section_headers.shdr[1].sh_flags").Comment; !strings.HasPrefix(c, "AX ") {
		t.Errorf("sh_flags comment %q", c)
	}
	execTruncated(t, "elf.bt", data, len(data))
	execCorrupted(t, "elf.bt", data)
}

func TestExecELF32BE(t *testing.T) {
	data := makeELF32BE()
	f, err := elf.NewFile(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("debug/elf: %v", err)
	}
	if f.Class != elf.ELFCLASS32 || f.ByteOrder != binary.BigEndian {
		t.Fatalf("debug/elf: %v %v", f.Class, f.ByteOrder)
	}
	syms, err := f.Symbols()
	if err != nil || len(syms) != 2 || syms[0].Name != "main" {
		t.Fatalf("debug/elf symbols %v %v", syms, err)
	}
	execDetect(t, data, "x", "elf.bt")
	res := execRun(t, "elf.bt", data)
	elfCompare(t, res, f)
	if v := execShown(t, res, "ehdr.e_ident.ei_data"); v != "ELFDATA2MSB (2)" {
		t.Errorf("ei_data %s", v)
	}
	if n := res.Root.Find("symtab.sym"); n == nil || n.ChildCount() != 3 {
		t.Fatalf("symtab %v", n)
	}
	// debug/elf leaves out the null symbol
	for i, s := range syms {
		p := "symtab.sym[" + strconv.Itoa(i+1) + "]"
		if v := execShown(t, res, p+".name"); v != `"`+s.Name+`"` {
			t.Errorf("%s.name %s, debug/elf %s", p, v, s.Name)
		}
		if v := execNum(t, res, p+".st_value"); v != s.Value {
			t.Errorf("%s.st_value 0x%X, debug/elf 0x%X", p, v, s.Value)
		}
		if v := execNum(t, res, p+".st_info.st_type"); elf.SymType(v) != elf.ST_TYPE(s.Info) {
			t.Errorf("%s st_type %d, debug/elf %v", p, v, elf.ST_TYPE(s.Info))
		}
		if v := execNum(t, res, p+".st_info.st_bind"); elf.SymBind(v) != elf.ST_BIND(s.Info) {
			t.Errorf("%s st_bind %d, debug/elf %v", p, v, elf.ST_BIND(s.Info))
		}
	}
	if v := execShown(t, res, "symtab.sym[1]"); v != "main 0x10000100" {
		t.Errorf("symbol 1 %q", v)
	}
	if v := execShown(t, res, "symtab.sym[1].st_info"); v != "STB_GLOBAL STT_FUNC" {
		t.Errorf("st_info %q", v)
	}
	execTruncated(t, "elf.bt", data, len(data))
	execCorrupted(t, "elf.bt", data)
}

// The ELF files of the system
func TestExecELFSystem(t *testing.T) {
	files := []string{"/bin/ls", "/bin/sh", "/usr/bin/env"}
	if exe, err := os.Executable(); err == nil {
		files = append(files, exe) // a Go binary: several MB, many symbols
	}
	more, _ := filepath.Glob("/usr/bin/*")
	files = append(files, more...)
	more, _ = filepath.Glob("/usr/lib/x86_64-linux-gnu/*.so*")
	files = append(files, more...)
	more, _ = filepath.Glob("/usr/lib/*.so*")
	files = append(files, more...)
	n := 0
	var biggest string
	var biggestSize int64
	for _, name := range files {
		if n >= 25 {
			break
		}
		st, err := os.Stat(name)
		if err != nil || !st.Mode().IsRegular() || st.Size() > 64<<20 {
			continue
		}
		f, err := elf.Open(name)
		if err != nil {
			continue
		}
		data, err := os.ReadFile(name)
		if err != nil {
			f.Close()
			continue
		}
		n++
		if st.Size() > biggestSize {
			biggest, biggestSize = name, st.Size()
		}
		t.Run(filepath.Base(name), func(t *testing.T) {
			res := execRun(t, "elf.bt", data)
			elfCompare(t, res, f)
			if res.Duration > 5*time.Second {
				t.Errorf("%s: %v", name, res.Duration)
			}
		})
		f.Close()
	}
	if n == 0 {
		t.Skip("no ELF files")
	}
	// The biggest one, cut
	data, err := os.ReadFile(biggest)
	if err == nil {
		t.Logf("%s: %d bytes", biggest, len(data))
		start := time.Now()
		execRun(t, "elf.bt", data)
		if d := time.Since(start); d > 5*time.Second {
			t.Errorf("%s: %v", biggest, d)
		}
		execTruncated(t, "elf.bt", data, 8)
	}
}

// ---- PE ----

// makePE64 makes a PE32+ console program with .text and .idata; .idata has
// the import directory of KERNEL32.dll: ExitProcess, GetStdHandle and an
// import by ordinal
func makePE64() []byte {
	le := binary.LittleEndian
	b := make([]byte, 0x600)
	copy(b, "MZ")
	le.PutUint32(b[0x3C:], 0x80)
	copy(b[0x40:], "\x0e\x1f\xba\x0e\x00\xb4\x09\xcd\x21\xb8\x01\x4c\xcd\x21This program cannot be run in DOS mode.\r\r\n$")
	nt := 0x80
	copy(b[nt:], "PE\x00\x00")
	fh := nt + 4
	le.PutUint16(b[fh:], 0x8664) // Machine
	le.PutUint16(b[fh+2:], 2)    // NumberOfSections
	le.PutUint32(b[fh+4:], 0x5F000000)
	le.PutUint16(b[fh+16:], 240)    // SizeOfOptionalHeader
	le.PutUint16(b[fh+18:], 0x0022) // EXECUTABLE_IMAGE | LARGE_ADDRESS_AWARE
	oh := fh + 20
	le.PutUint16(b[oh:], 0x20B)
	b[oh+2] = 14
	le.PutUint32(b[oh+4:], 0x200)        // SizeOfCode
	le.PutUint32(b[oh+8:], 0x200)        // SizeOfInitializedData
	le.PutUint32(b[oh+16:], 0x1000)      // AddressOfEntryPoint
	le.PutUint32(b[oh+20:], 0x1000)      // BaseOfCode
	le.PutUint64(b[oh+24:], 0x140000000) // ImageBase
	le.PutUint32(b[oh+32:], 0x1000)      // SectionAlignment
	le.PutUint32(b[oh+36:], 0x200)       // FileAlignment
	le.PutUint16(b[oh+40:], 6)           // MajorOperatingSystemVersion
	le.PutUint16(b[oh+48:], 6)           // MajorSubsystemVersion
	le.PutUint32(b[oh+56:], 0x3000)      // SizeOfImage
	le.PutUint32(b[oh+60:], 0x200)       // SizeOfHeaders
	le.PutUint16(b[oh+68:], 3)           // Subsystem: console
	le.PutUint16(b[oh+70:], 0x8160)      // DllCharacteristics
	le.PutUint64(b[oh+72:], 0x100000)    // SizeOfStackReserve
	le.PutUint64(b[oh+80:], 0x1000)
	le.PutUint64(b[oh+88:], 0x100000)
	le.PutUint64(b[oh+96:], 0x1000)
	le.PutUint32(b[oh+108:], 16) // NumberOfRvaAndSizes
	dd := oh + 112
	le.PutUint32(b[dd+8:], 0x2000) // IMPORT
	le.PutUint32(b[dd+12:], 40)
	le.PutUint32(b[dd+12*8:], 0x2040) // IAT
	le.PutUint32(b[dd+12*8+4:], 32)
	sec := oh + 240
	section := func(at int, name string, vsize, va, rawSize, raw, chars uint32) {
		copy(b[at:], name)
		le.PutUint32(b[at+8:], vsize)
		le.PutUint32(b[at+12:], va)
		le.PutUint32(b[at+16:], rawSize)
		le.PutUint32(b[at+20:], raw)
		le.PutUint32(b[at+36:], chars)
	}
	section(sec, ".text", 0x10, 0x1000, 0x200, 0x200, 0x60000020)
	section(sec+40, ".idata", 0x100, 0x2000, 0x200, 0x400, 0xC0000040)
	// .text: sub rsp,40; xor ecx,ecx; call [ExitProcess]
	copy(b[0x200:], []byte{0x48, 0x83, 0xEC, 0x28, 0x31, 0xC9, 0xFF, 0x15, 0x34, 0x10, 0, 0})
	// .idata at RVA 0x2000, file 0x400
	id := 0x400
	le.PutUint32(b[id:], 0x2028)    // OriginalFirstThunk
	le.PutUint32(b[id+12:], 0x2090) // Name
	le.PutUint32(b[id+16:], 0x2040) // FirstThunk
	for _, base := range []int{id + 0x28, id + 0x40} {
		le.PutUint64(b[base:], 0x2060)
		le.PutUint64(b[base+8:], 0x2070)
		le.PutUint64(b[base+16:], 1<<63|5) // ordinal 5
	}
	le.PutUint16(b[id+0x60:], 0x120)
	copy(b[id+0x62:], "ExitProcess\x00")
	le.PutUint16(b[id+0x70:], 0x2B0)
	copy(b[id+0x72:], "GetStdHandle\x00")
	copy(b[id+0x90:], "KERNEL32.dll\x00")
	return b
}

// peLibraries returns the imported DLLs, in order, from the imported
// symbols of debug/pe (its ImportedLibraries gives none)
func peLibraries(f *pe.File) []string {
	syms, _ := f.ImportedSymbols()
	var libs []string
	seen := map[string]bool{}
	for _, s := range syms {
		_, lib, ok := strings.Cut(s, ":")
		if ok && !seen[lib] {
			seen[lib] = true
			libs = append(libs, lib)
		}
	}
	return libs
}

// peCompare checks the template's fields against debug/pe
func peCompare(t *testing.T, res *Result, f *pe.File) {
	t.Helper()
	if m := execNum(t, res, "nt_headers.FileHeader.Machine"); uint16(m) != f.Machine {
		t.Errorf("Machine 0x%X, debug/pe 0x%X", m, f.Machine)
	}
	var entry uint32
	switch oh := f.OptionalHeader.(type) {
	case *pe.OptionalHeader32:
		entry = oh.AddressOfEntryPoint
		if v := execShown(t, res, "nt_headers.OptionalHeader.Magic"); !strings.HasPrefix(v, "IMAGE_NT_OPTIONAL_HDR32_MAGIC") {
			t.Errorf("Magic %s", v)
		}
	case *pe.OptionalHeader64:
		entry = oh.AddressOfEntryPoint
		if v := execShown(t, res, "nt_headers.OptionalHeader.Magic"); !strings.HasPrefix(v, "IMAGE_NT_OPTIONAL_HDR64_MAGIC") {
			t.Errorf("Magic %s", v)
		}
	}
	if f.OptionalHeader != nil {
		if e := execNum(t, res, "nt_headers.OptionalHeader.AddressOfEntryPoint"); uint32(e) != entry {
			t.Errorf("AddressOfEntryPoint 0x%X, debug/pe 0x%X", e, entry)
		}
	}
	sh := res.Root.Find("section_headers")
	if sh == nil || sh.ChildCount() != int64(len(f.Sections)) {
		t.Fatalf("section_headers %v, debug/pe %d sections", sh, len(f.Sections))
	}
	for i, s := range f.Sections {
		got := execShown(t, res, "section_headers["+strconv.Itoa(i)+"]")
		// debug/pe resolves the long names (/4) of the string table; the
		// template shows the 8 bytes
		if len(s.Name) <= 8 && !strings.HasPrefix(got, s.Name+" ") {
			t.Errorf("section %d: %q, debug/pe %q", i, got, s.Name)
		}
	}
	libs := peLibraries(f)
	if len(libs) == 0 {
		return
	}
	dir := res.Root.Find("import_directory.Descriptor")
	if dir == nil {
		t.Fatalf("no import directory; debug/pe %v", libs)
	}
	var got []string
	if dir.Kind == NodeList {
		for _, d := range dir.Children() {
			got = append(got, d.Value())
		}
	} else {
		got = append(got, dir.Value())
	}
	if strings.Join(got, ",") != strings.Join(libs, ",") {
		t.Errorf("imported DLLs %v, debug/pe %v", got, libs)
	}
}

func TestExecPE64(t *testing.T) {
	data := makePE64()
	f, err := pe.NewFile(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("debug/pe: %v", err)
	}
	libs := peLibraries(f)
	syms, _ := f.ImportedSymbols()
	if len(f.Sections) != 2 || len(libs) != 1 || libs[0] != "KERNEL32.dll" || len(syms) < 2 || syms[0] != "ExitProcess:KERNEL32.dll" {
		t.Fatalf("debug/pe sees %d sections, %v, %v", len(f.Sections), libs, syms)
	}
	execDetect(t, data, "a.exe", "pe.bt")
	res := execRun(t, "pe.bt", data)
	peCompare(t, res, f)
	if v := execShown(t, res, "nt_headers.Signature"); v != `"PE"` {
		t.Errorf("Signature %s", v)
	}
	if v := execShown(t, res, "nt_headers.FileHeader.Machine"); !strings.HasPrefix(v, "IMAGE_FILE_MACHINE_AMD64") {
		t.Errorf("Machine %s", v)
	}
	if v := execNum(t, res, "nt_headers.FileHeader.Characteristics.IMAGE_FILE_LARGE_ADDRESS_AWARE"); v != 1 {
		t.Errorf("LARGE_ADDRESS_AWARE %d", v)
	}
	if v := execShown(t, res, "nt_headers.OptionalHeader.DataDirectory.IMPORT"); v != "RVA 0x2000, 0x28 bytes" {
		t.Errorf("IMPORT %q", v)
	}
	if v := execShown(t, res, "section_headers[1]"); v != ".idata 0x200 bytes at 0x400" {
		t.Errorf("section 1 %q", v)
	}
	if v := execShown(t, res, "import_directory.Descriptor.DllName"); v != `"KERNEL32.dll"` {
		t.Errorf("DllName %s", v)
	}
	for i, want := range []string{"ExitProcess", "GetStdHandle", "Ordinal 5"} {
		if v := execShown(t, res, "import_directory.Descriptor.Functions.Thunk["+strconv.Itoa(i)+"]"); v != want {
			t.Errorf("function %d: %q, want %q", i, v, want)
		}
	}
	execTruncated(t, "pe.bt", data, len(data)/4)
	execCorrupted(t, "pe.bt", data[:0x500])
}

// goroot returns the root of the Go toolchain, whose testdata has samples
func execGoroot() string {
	if r := os.Getenv("GOROOT"); r != "" {
		return r
	}
	return runtime.GOROOT()
}

// execFindFiles walks the directories for files of the extensions, at most n
// and for at most a second
func execFindFiles(dirs []string, exts []string, maxSize int64, n int) []string {
	var out []string
	deadline := time.Now().Add(time.Second)
	for _, dir := range dirs {
		filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
			if err != nil || len(out) >= n || time.Now().After(deadline) {
				if d != nil && d.IsDir() && err == nil {
					return filepath.SkipDir
				}
				return nil
			}
			if d.IsDir() {
				return nil
			}
			ext := strings.ToLower(filepath.Ext(p))
			for _, e := range exts {
				if ext == e {
					if st, err := d.Info(); err == nil && st.Mode().IsRegular() && st.Size() <= maxSize {
						out = append(out, p)
					}
				}
			}
			return nil
		})
	}
	return out
}

// The PE files of the system: the Go toolchain's samples, *.exe / *.dll
func TestExecPESystem(t *testing.T) {
	files, _ := filepath.Glob(filepath.Join(execGoroot(), "src/debug/pe/testdata/*"))
	home, _ := os.UserHomeDir()
	files = append(files, execFindFiles([]string{"/usr/lib", "/usr/share", "/opt", home},
		[]string{".exe", ".dll"}, 5<<20, 6)...)
	n := 0
	for _, name := range files {
		f, err := pe.Open(name)
		if err != nil {
			continue
		}
		data, err := os.ReadFile(name)
		if err != nil || len(data) < 2 || data[0] != 'M' || data[1] != 'Z' {
			f.Close()
			continue // an object file: no MS-DOS header
		}
		n++
		t.Run(filepath.Base(name), func(t *testing.T) {
			res := execRun(t, "pe.bt", data)
			peCompare(t, res, f)
			if res.Duration > 5*time.Second {
				t.Errorf("%s: %v", name, res.Duration)
			}
			execTruncated(t, "pe.bt", data, 16)
		})
		f.Close()
	}
	if n == 0 {
		t.Skip("no PE files")
	}
}

// ---- Mach-O ----

// makeMachO makes an executable with LC_SEGMENT(_64) __TEXT with __text,
// LC_LOAD_DYLIB and LC_UUID
func makeMachO(is64 bool, bo binary.AppendByteOrder, cpu macho.Cpu) []byte {
	var cmds []byte
	u32 := func(b []byte, v uint32) []byte { return bo.AppendUint32(b, v) }
	word := func(b []byte, v uint64) []byte {
		if is64 {
			return bo.AppendUint64(b, v)
		}
		return bo.AppendUint32(b, uint32(v))
	}
	name16 := func(b []byte, s string) []byte {
		n := make([]byte, 16)
		copy(n, s)
		return append(b, n...)
	}
	hdrSize := 28
	segSize, sectSize := 56, 68
	if is64 {
		hdrSize, segSize, sectSize = 32, 72, 80
	}
	dylib := "/usr/lib/libSystem.B.dylib"
	dylibSize := (24 + len(dylib) + 1 + 7) &^ 7
	sizeofcmds := segSize + sectSize + dylibSize + 24
	textOff := uint64(hdrSize + sizeofcmds)
	code := []byte{0x31, 0xC0, 0xC3}

	segCmd := uint32(macho.LoadCmdSegment)
	if is64 {
		segCmd = uint32(macho.LoadCmdSegment64)
	}
	cmds = u32(cmds, segCmd)
	cmds = u32(cmds, uint32(segSize+sectSize))
	cmds = name16(cmds, "__TEXT")
	cmds = word(cmds, 0x1000)                    // vmaddr
	cmds = word(cmds, 0x1000)                    // vmsize
	cmds = word(cmds, 0)                         // fileoff
	cmds = word(cmds, textOff+uint64(len(code))) // filesize
	cmds = u32(cmds, 5)                          // maxprot
	cmds = u32(cmds, 5)                          // initprot
	cmds = u32(cmds, 1)                          // nsects
	cmds = u32(cmds, 0)                          // flags
	cmds = name16(cmds, "__text")
	cmds = name16(cmds, "__TEXT")
	cmds = word(cmds, 0x1000+textOff)    // addr
	cmds = word(cmds, uint64(len(code))) // size
	cmds = u32(cmds, uint32(textOff))    // offset
	cmds = u32(cmds, 0)                  // align
	cmds = u32(cmds, 0)
	cmds = u32(cmds, 0)
	cmds = u32(cmds, 0x80000400) // flags
	cmds = u32(cmds, 0)
	cmds = u32(cmds, 0)
	if is64 {
		cmds = u32(cmds, 0)
	}
	cmds = u32(cmds, uint32(macho.LoadCmdDylib))
	cmds = u32(cmds, uint32(dylibSize))
	cmds = u32(cmds, 24)
	cmds = u32(cmds, 2)
	cmds = u32(cmds, 0x050C0000)
	cmds = u32(cmds, 0x00010000)
	d := make([]byte, dylibSize-24)
	copy(d, dylib)
	cmds = append(cmds, d...)
	cmds = u32(cmds, 0x1B) // LC_UUID
	cmds = u32(cmds, 24)
	cmds = append(cmds, 0x12, 0x34, 0x56, 0x78, 0x9A, 0xBC, 0xDE, 0xF0, 1, 2, 3, 4, 5, 6, 7, 8)

	var b []byte
	magic := uint32(macho.Magic32)
	if is64 {
		magic = macho.Magic64
	}
	b = u32(b, magic)
	b = u32(b, uint32(cpu))
	b = u32(b, 3)
	b = u32(b, uint32(macho.TypeExec))
	b = u32(b, 3)
	b = u32(b, uint32(sizeofcmds))
	b = u32(b, 0x200085) // MH_NOUNDEFS | MH_DYLDLINK | MH_TWOLEVEL | MH_PIE
	if is64 {
		b = u32(b, 0)
	}
	b = append(b, cmds...)
	return append(b, code...)
}

// makeFat makes a universal file of the Mach-Os, each at a 4 KB boundary
func makeFat(archs ...[]byte) []byte {
	be := binary.BigEndian
	b := be.AppendUint32(nil, 0xCAFEBABE)
	b = be.AppendUint32(b, uint32(len(archs)))
	off := 0x1000
	for _, a := range archs {
		var cpu, sub uint32
		if a[0] == 0xFE {
			cpu, sub = be.Uint32(a[4:]), be.Uint32(a[8:])
		} else {
			cpu, sub = binary.LittleEndian.Uint32(a[4:]), binary.LittleEndian.Uint32(a[8:])
		}
		b = be.AppendUint32(b, cpu)
		b = be.AppendUint32(b, sub)
		b = be.AppendUint32(b, uint32(off))
		b = be.AppendUint32(b, uint32(len(a)))
		b = be.AppendUint32(b, 12)
		off += (len(a) + 0xFFF) &^ 0xFFF
	}
	for _, a := range archs {
		for len(b)%0x1000 != 0 {
			b = append(b, 0)
		}
		b = append(b, a...)
	}
	return b
}

// machoCompare checks a Mach-O of the template (at path) against debug/macho
func machoCompare(t *testing.T, res *Result, path string, f *macho.File) {
	t.Helper()
	if c := execNum(t, res, path+".header.cputype"); macho.Cpu(c) != f.Cpu {
		t.Errorf("%s cputype 0x%X, debug/macho %v", path, c, f.Cpu)
	}
	if v := execNum(t, res, path+".header.filetype"); macho.Type(v) != f.Type {
		t.Errorf("%s filetype %d, debug/macho %v", path, v, f.Type)
	}
	if v := execNum(t, res, path+".header.ncmds"); int(v) != len(f.Loads) {
		t.Errorf("%s ncmds %d, debug/macho %d", path, v, len(f.Loads))
	}
	cmds := res.Root.Find(path + ".load_commands.command")
	if cmds == nil {
		t.Fatalf("%s: no load commands", path)
	}
	var shownCmds []string
	if cmds.Kind == NodeList {
		for _, c := range cmds.Children() {
			shownCmds = append(shownCmds, c.Value())
		}
	} else {
		shownCmds = append(shownCmds, cmds.Value())
	}
	if len(shownCmds) != len(f.Loads) {
		t.Fatalf("%s: %d load commands, debug/macho %d", path, len(shownCmds), len(f.Loads))
	}
	for i, l := range f.Loads {
		switch l := l.(type) {
		case *macho.Segment:
			want := "LC_SEGMENT_64 " + l.Name
			if l.Cmd == macho.LoadCmdSegment {
				want = "LC_SEGMENT " + l.Name
			}
			if strings.TrimSpace(shownCmds[i]) != strings.TrimSpace(want) {
				t.Errorf("%s command %d %q, want %q", path, i, shownCmds[i], want)
			}
		case *macho.Dylib:
			if !strings.HasSuffix(shownCmds[i], " "+l.Name) {
				t.Errorf("%s command %d %q, debug/macho %q", path, i, shownCmds[i], l.Name)
			}
		}
	}
}

func TestExecMachO(t *testing.T) {
	data := makeMachO(true, binary.LittleEndian, macho.CpuAmd64)
	f, err := macho.NewFile(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("debug/macho: %v", err)
	}
	if s := f.Section("__text"); s == nil || s.Size != 3 || f.Segment("__TEXT") == nil {
		t.Fatalf("debug/macho sees %v", f.Sections)
	}
	if libs, _ := f.ImportedLibraries(); len(libs) != 1 || libs[0] != "/usr/lib/libSystem.B.dylib" {
		t.Fatalf("debug/macho libraries %v", libs)
	}
	execDetect(t, data, "a.out", "macho.bt")
	res := execRun(t, "macho.bt", data)
	machoCompare(t, res, "macho", f)
	if v := execShown(t, res, "macho.header.magic"); v != "MH_MAGIC_64 (0xFEEDFACF)" {
		t.Errorf("magic %s", v)
	}
	if v := execShown(t, res, "macho.load_commands.command[0]"); v != "LC_SEGMENT_64 __TEXT" {
		t.Errorf("command 0 %q", v)
	}
	if v := execShown(t, res, "macho.load_commands.command[0].sections"); v != "[1]" {
		t.Errorf("sections %q", v)
	}
	s := f.Section("__text")
	want := "__text 0x3 bytes at 0x" + strings.ToUpper(strconv.FormatUint(uint64(s.Offset), 16))
	if v := execShown(t, res, "macho.load_commands.command[0].sections[0]"); v != want {
		t.Errorf("section %q, want %q", v, want)
	}
	if v := execShown(t, res, "macho.load_commands.command[2]"); v != "LC_UUID 12345678-9ABC-DEF0-0102-030405060708" {
		t.Errorf("uuid %q", v)
	}
	execTruncated(t, "macho.bt", data, len(data))
	execCorrupted(t, "macho.bt", data)

	// 32 bits, big-endian
	data = makeMachO(false, binary.BigEndian, macho.CpuPpc)
	if f, err = macho.NewFile(bytes.NewReader(data)); err != nil {
		t.Fatalf("debug/macho: %v", err)
	}
	execDetect(t, data, "a.out", "macho.bt")
	res = execRun(t, "macho.bt", data)
	machoCompare(t, res, "macho", f)
	if v := execShown(t, res, "macho.load_commands.command[0]"); v != "LC_SEGMENT __TEXT" {
		t.Errorf("command 0 %q", v)
	}
	execTruncated(t, "macho.bt", data, len(data))
}

func TestExecMachOFat(t *testing.T) {
	data := makeFat(makeMachO(false, binary.LittleEndian, macho.Cpu386), makeMachO(true, binary.LittleEndian, macho.CpuAmd64))
	ff, err := macho.NewFatFile(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("debug/macho: %v", err)
	}
	if len(ff.Arches) != 2 {
		t.Fatalf("debug/macho: %d arches", len(ff.Arches))
	}
	execDetect(t, data, "a.out", "macho.bt")
	res := execRun(t, "macho.bt", data)
	if v := execNum(t, res, "fat.nfat_arch"); v != 2 {
		t.Fatalf("nfat_arch %d", v)
	}
	for i, a := range ff.Arches {
		p := "fat_arches[" + strconv.Itoa(i) + "]"
		if v := execNum(t, res, p+".offset"); uint32(v) != a.Offset {
			t.Errorf("%s.offset 0x%X, debug/macho 0x%X", p, v, a.Offset)
		}
		if v := execNum(t, res, p+".cputype"); macho.Cpu(v) != a.Cpu {
			t.Errorf("%s.cputype 0x%X, debug/macho %v", p, v, a.Cpu)
		}
		machoCompare(t, res, "arch["+strconv.Itoa(i)+"]", a.File)
	}
	if v := execShown(t, res, "arch[1]"); v != "CPU_TYPE_X86_64 MH_EXECUTE" {
		t.Errorf("arch 1 %q", v)
	}
	if v := execShown(t, res, "fat_arches[0]"); v != "CPU_TYPE_X86 0x"+strings.ToUpper(strconv.FormatUint(uint64(ff.Arches[0].Size), 16))+" bytes at 0x1000" {
		t.Errorf("fat_arch 0 %q", v)
	}
	execTruncated(t, "macho.bt", data, 300)
	execCorrupted(t, "macho.bt", data[:0x200])
}

// A CAFEBABE file of a Java class is not taken for a fat Mach-O
func TestExecMachONotJava(t *testing.T) {
	class := []byte{0xCA, 0xFE, 0xBA, 0xBE, 0, 0, 0, 0x34, 0, 0x10}
	if tm := Detect(Library(), &mem{b: class}, "A.class"); tm != nil && tm.File == "macho.bt" {
		t.Fatal("a Java class detected as Mach-O")
	}
}

// The Mach-O files of the system: the Go toolchain's samples, *.dylib
func TestExecMachOSystem(t *testing.T) {
	samples, _ := filepath.Glob(filepath.Join(execGoroot(), "src/debug/macho/testdata/*.base64"))
	type file struct {
		name string
		data []byte
	}
	var files []file
	for _, s := range samples {
		text, err := os.ReadFile(s)
		if err != nil {
			continue
		}
		data, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(text)))
		if err == nil {
			files = append(files, file{filepath.Base(s), data})
		}
	}
	home, _ := os.UserHomeDir()
	for _, p := range execFindFiles([]string{"/usr/lib", "/opt", home}, []string{".dylib"}, 32<<20, 4) {
		if data, err := os.ReadFile(p); err == nil {
			files = append(files, file{filepath.Base(p), data})
		}
	}
	n := 0
	for _, fl := range files {
		data := fl.data
		if ff, err := macho.NewFatFile(bytes.NewReader(data)); err == nil {
			n++
			t.Run(fl.name, func(t *testing.T) {
				res := execRun(t, "macho.bt", data)
				for i, a := range ff.Arches {
					if i < 64 {
						machoCompare(t, res, "arch["+strconv.Itoa(i)+"]", a.File)
					}
				}
				execTruncated(t, "macho.bt", data, 16)
			})
			continue
		}
		f, err := macho.NewFile(bytes.NewReader(data))
		if err != nil {
			continue
		}
		n++
		t.Run(fl.name, func(t *testing.T) {
			res := execRun(t, "macho.bt", data)
			machoCompare(t, res, "macho", f)
			execTruncated(t, "macho.bt", data, 16)
		})
	}
	if n == 0 {
		t.Skip("no Mach-O files")
	}
}
