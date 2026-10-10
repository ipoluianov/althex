package analysis_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ipoluianov/althex/core/analysis"
	"github.com/ipoluianov/althex/core/disasm"
	"github.com/ipoluianov/althex/core/fw"
)

// TestSamples analyzes real firmwares: the files of the directory in
// ALTHEX_FW_SAMPLES (Arduino sketches and bootloaders, MicroPython builds
// for STM32, nRF52, RP2040/RP2350, ESP32, ESP32-C3, ESP8266 were used).
// With GNU binutils (ALTHEX_BINUTILS or the PATH) the export is assembled
// again and must give the same bytes (FW-10).
func TestSamples(t *testing.T) {
	dir := os.Getenv("ALTHEX_FW_SAMPLES")
	if dir == "" {
		t.Skip("ALTHEX_FW_SAMPLES is not set")
	}
	var files []string
	filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			switch strings.ToLower(filepath.Ext(path)) {
			case ".bin", ".hex", ".elf", ".uf2", ".dfu", ".srec", ".s19", ".axf":
				files = append(files, path)
			}
		}
		return nil
	})
	if len(files) == 0 {
		t.Skip("no firmwares in", dir)
	}
	for _, path := range files {
		t.Run(filepath.Base(path), func(t *testing.T) { sample(t, path) })
	}
}

// gnu are the GNU tools that take an architecture back; xtensa only for
// the LX106 (ESP8266): the others have their own toolchains
var gnu = map[string]struct {
	prefix string
	as, ld []string
}{
	"avr":     {"avr-", []string{"-mmcu=avr6"}, []string{"-mavr6"}},
	"arm":     {"arm-none-eabi-", nil, nil},
	"riscv32": {"riscv64-unknown-elf-", []string{"-march=rv32imac_zicsr_zifencei", "-mabi=ilp32"}, []string{"-melf32lriscv", "--no-relax"}},
	"xtensa":  {"xtensa-lx106-elf-", nil, nil},
}

func sample(t *testing.T, path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	img, err := fw.Open(bytes.NewReader(data), int64(len(data)), path)
	if err != nil {
		t.Skipf("not a firmware: %v", err)
	}
	gs := fw.Detect(img)
	if len(gs) == 0 || disasm.ByID(gs[0].Arch) == nil {
		t.Skipf("%s: no architecture to analyze", img.Format)
	}
	g := gs[0]
	chip := img.Family
	if chip == "" {
		chip = g.Family
	}
	if chip == "" && strings.Contains(img.Format, "RP2350") {
		chip = "RP2350"
	}

	// The entries as the window takes them: the entry, the vectors (the
	// jmp of an AVR vector is code), the functions of the symbol table
	var es []analysis.Entry
	if img.HasEntry {
		es = append(es, analysis.Entry{Addr: img.Entry, Mode: g.Mode})
	}
	for _, v := range g.Vectors {
		if g.Arch == "avr" && v.Addr != g.Base+uint64(v.Off) {
			es = append(es, analysis.Entry{Addr: g.Base + uint64(v.Off), Code: true})
		}
		if v.Addr == 0 || v.Index == 0 && g.Arch == "arm" {
			continue
		}
		es = append(es, analysis.Entry{Addr: v.Addr &^ 1, Mode: g.Mode})
	}
	labels := map[uint64]string{}
	for _, s := range img.Symbols {
		if s.Func {
			es = append(es, analysis.Entry{Addr: s.Addr, Mode: g.Mode, Name: s.Name})
		} else if g.Arch != "avr" || s.Addr < 0x800000 {
			labels[s.Addr] = s.Name
		}
	}
	var ranges []analysis.Range
	var total int64
	for i, s := range img.Segments {
		ranges = append(ranges, analysis.Range{Name: fmt.Sprintf("s%d", i), Addr: s.Addr, Size: uint64(s.Size)})
		total += s.Size
	}
	res := analysis.Analyze(context.Background(), img, analysis.Options{
		Arch: disasm.ByID(g.Arch), Entries: es, Labels: labels, Ranges: ranges, Chip: chip,
	})
	var code int64
	for _, c := range res.Insts {
		code += int64(c.Len)
	}
	t.Logf("%s, %s %s: %d instructions, %d functions, code %d%% of %d bytes",
		img.Format, g.Arch, chip, len(res.Insts), len(res.Funcs), 100*code/max(total, 1), total)
	if len(res.Insts) == 0 {
		t.Fatal("no code found")
	}

	tools, ok := gnu[g.Arch]
	if !ok || g.Arch == "xtensa" && !strings.Contains(strings.ToUpper(chip), "ESP8266") {
		return
	}
	header, footer, comment := analysis.AsmSyntax(g.Arch)
	var src bytes.Buffer
	res.WriteAsm(&src, ranges, analysis.AsmOptions{Header: header, Footer: footer, Comment: comment,
		Read: func(p []byte, addr uint64) { img.ReadAddr(p, addr) }})
	as, ld := tool(t, tools.prefix+"as"), tool(t, tools.prefix+"ld")
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "x.s"), src.Bytes(), 0o644)
	run(t, dir, as, append(tools.as, "-o", "x.o", "x.s")...)
	args := append([]string{"-o", "x.elf", "-e", "0"}, tools.ld...)
	for _, r := range ranges {
		args = append(args, fmt.Sprintf("--section-start=.%s=0x%X", r.Name, r.Addr))
	}
	run(t, dir, ld, append(args, "x.o")...)
	elf, _ := os.ReadFile(filepath.Join(dir, "x.elf"))
	again, err := fw.Open(bytes.NewReader(elf), int64(len(elf)), "x.elf")
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range ranges {
		a, b := make([]byte, r.Size), make([]byte, r.Size)
		img.ReadAddr(a, r.Addr)
		again.ReadAddr(b, r.Addr)
		if !bytes.Equal(a, b) {
			n := 0
			for a[n] == b[n] {
				n++
			}
			t.Fatalf("assembled again, 0x%X differs: % X instead of % X", r.Addr+uint64(n), b[n:min(n+8, len(b))], a[n:min(n+8, len(a))])
		}
	}
	t.Logf("assembled again: the same bytes")
}
