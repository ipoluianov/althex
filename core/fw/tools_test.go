package fw

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ipoluianov/althex/core/disasm"
)

// Real firmwares made by GNU binutils, when they are there: ALTHEX_BINUTILS
// or the PATH
func binutils(t *testing.T, tool string) string {
	t.Helper()
	dirs := []string{os.Getenv("ALTHEX_BINUTILS")}
	for _, d := range dirs {
		if d == "" {
			continue
		}
		p := filepath.Join(d, tool)
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	if p, err := exec.LookPath(tool); err == nil {
		return p
	}
	t.Skipf("no %s", tool)
	return ""
}

func run(t *testing.T, dir, tool string, args ...string) {
	t.Helper()
	cmd := exec.Command(tool, args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %v: %v\n%s", filepath.Base(tool), args, err, out)
	}
}

// build assembles src, links it with the script and makes .bin, .hex and
// .srec from the ELF; it returns the directory
func build(t *testing.T, prefix, src, ldScript string, asArgs, ldArgs []string) string {
	t.Helper()
	as, ld, objcopy := binutils(t, prefix+"as"), binutils(t, prefix+"ld"), binutils(t, prefix+"objcopy")
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "fw.s"), []byte(src), 0o644)
	os.WriteFile(filepath.Join(dir, "fw.ld"), []byte(ldScript), 0o644)
	run(t, dir, as, append(asArgs, "-o", "fw.o", "fw.s")...)
	run(t, dir, ld, append(ldArgs, "-T", "fw.ld", "-o", "fw.elf", "fw.o")...)
	run(t, dir, objcopy, "-O", "binary", "fw.elf", "fw.bin")
	run(t, dir, objcopy, "-O", "ihex", "fw.elf", "fw.hex")
	run(t, dir, objcopy, "-O", "srec", "fw.elf", "fw.srec")
	return dir
}

func openFile(t *testing.T, path string) *Image {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return open(t, data, path)
}

// armBody is Thumb code: functions that load, store, compare and call
func armBody(n int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, ".thumb_func\nf%d:\n push {r4, r5, lr}\n ldr r4, =0x%08X\n ldr r0, [r4, #%d]\n movs r1, #%d\n"+
			" cmp r0, r1\n beq 1f\n str r1, [r4, #4]\n adds r0, r0, #1\n1:\n bl f%d\n subs r5, r0, #%d\n lsls r0, r5, #2\n pop {r4, r5, pc}\n .ltorg\n",
			i, 0x40020000+i*0x400, (i%8)*4, i%200, (i+1)%n, i%7)
	}
	return b.String()
}

const cortexLD = `
MEMORY { FLASH (rx) : ORIGIN = 0x08000000, LENGTH = 64K
         RAM (rwx) : ORIGIN = 0x20000000, LENGTH = 20K }
SECTIONS {
  .isr_vector : { KEEP(*(.isr_vector)) } > FLASH
  .text : { *(.text*) *(.rodata*) } > FLASH
  .data : { *(.data*) } > RAM AT > FLASH
  .bss : { *(.bss*) } > RAM
}
`

func TestToolsCortexM(t *testing.T) {
	var vec strings.Builder
	vec.WriteString(".word 0x20005000\n.word reset\n.word nmi\n.word hardfault\n")
	for i := 4; i < 16; i++ {
		switch i {
		case 7, 8, 9, 10, 13:
			vec.WriteString(".word 0\n")
		default:
			vec.WriteString(".word other\n")
		}
	}
	for i := 0; i < 43; i++ {
		vec.WriteString(".word other\n")
	}
	src := ".syntax unified\n.cpu cortex-m3\n.thumb\n.section .isr_vector,\"a\"\n" + vec.String() +
		".text\n.thumb_func\nreset:\n bl f0\n b reset\n.thumb_func\nnmi:\n b nmi\n.thumb_func\nhardfault:\n b hardfault\n.thumb_func\nother:\n b other\n" +
		armBody(60) + ".data\n.word 0x12345678\n"
	dir := build(t, "arm-none-eabi-", src, cortexLD, []string{"-mthumb"}, nil)

	for _, f := range []string{"fw.elf", "fw.bin", "fw.hex", "fw.srec"} {
		im := openFile(t, filepath.Join(dir, f))
		g := best(t, im)
		if g.Arch != "arm" || g.Base != 0x08000000 || g.Family != "STM32" || len(g.Vectors) != 16+43 {
			t.Fatalf("%s (%s): %+v", f, im.Format, g)
		}
		if g.Vectors[1].Name != "Reset" || g.Entry != g.Vectors[1].Addr || g.Entry&1 != 0 {
			t.Fatalf("%s: %+v", f, g.Vectors[1])
		}
		if f == "fw.elf" {
			if im.Arch != "arm" || im.Entry != g.Entry {
				t.Fatalf("ELF entry 0x%X, reset 0x%X", im.Entry, g.Entry)
			}
			// .data is loaded in flash after .text: by the LMA
			_, hi := im.Bounds()
			if hi > 0x08010000 || len(im.Segments) < 2 {
				t.Fatalf("%+v", im.Segments)
			}
		}
	}
	// The code alone, by statistics
	if disasm.ByID("arm") == nil {
		t.Skip("no ARM decoder")
	}
	statsCheck(t, filepath.Join(dir, "fw.bin"), 0x200, "arm")
}

// statsCheck opens the code from off of a file as a raw binary and wants
// the statistics to choose arch
func statsCheck(t *testing.T, path string, off int, arch string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	im := open(t, data[off:], "code.bin")
	gs := Detect(im)
	for _, g := range gs {
		t.Logf("%s %.2f %s", g.Arch, g.Score, g.Reason)
	}
	if len(gs) == 0 || gs[0].Arch != arch || !strings.Contains(gs[0].Reason, "statistics") {
		t.Fatalf("statistics did not choose %s", arch)
	}
}

func TestToolsAVR(t *testing.T) {
	var b strings.Builder
	b.WriteString(".text\n.global main\nvectors:\n jmp reset\n")
	for i := 1; i < 26; i++ {
		b.WriteString(" jmp bad\n")
	}
	b.WriteString("reset:\n eor r1, r1\n out 0x3f, r1\n ldi r28, 0xff\n ldi r29, 0x08\n call f0\nbad:\n rjmp bad\n")
	for i := 0; i < 60; i++ {
		fmt.Fprintf(&b, "f%d:\n push r28\n push r29\n ldi r24, %d\n lds r25, 0x%x\n cpi r24, %d\n brne 1f\n sts 0x%x, r24\n1:\n rcall f%d\n movw r30, r24\n ld r18, Z+\n std Y+1, r18\n pop r29\n pop r28\n ret\n",
			i, i%250, 0x100+i, (i*3)%250, 0x120+i, (i+1)%60)
	}
	dir := build(t, "avr-", b.String(), "SECTIONS { .text 0 : { *(.text) } }\n", []string{"-mmcu=atmega328p"}, []string{"-m", "avr5"})
	for _, f := range []string{"fw.elf", "fw.bin", "fw.hex"} {
		im := openFile(t, filepath.Join(dir, f))
		g := best(t, im)
		if g.Arch != "avr" || g.Base != 0 || len(g.Vectors) != 26 || g.Entry != 26*4 || g.Vectors[1].Name != "INT0" {
			t.Fatalf("%s (%s): %+v", f, im.Format, g)
		}
	}
	if disasm.ByID("avr") == nil {
		t.Skip("no AVR decoder")
	}
	statsCheck(t, filepath.Join(dir, "fw.bin"), 26*4, "avr")
}

func TestToolsRISCV(t *testing.T) {
	var b strings.Builder
	b.WriteString(".section .init,\"ax\"\n j reset\n.align 2\n.word 0\n")
	for i := 0; i < 20; i++ {
		b.WriteString(".word irq\n")
	}
	b.WriteString(".text\nreset:\n lui sp, 0x20008\n call f0\n j reset\nirq:\n mret\n")
	for i := 0; i < 60; i++ {
		fmt.Fprintf(&b, "f%d:\n addi sp, sp, -16\n sw ra, 12(sp)\n li a0, %d\n lui a1, 0x40010\n lw a2, %d(a1)\n beq a0, a2, 1f\n sw a0, 4(a1)\n1:\n call f%d\n lw ra, 12(sp)\n addi sp, sp, 16\n ret\n",
			i, i*3, (i%8)*4, (i+1)%60)
	}
	ldScript := "SECTIONS { . = 0x08000000; .init : { *(.init) } .text : { *(.text) } }\n"
	dir := build(t, "riscv64-unknown-elf-", b.String(), ldScript, []string{"-march=rv32imac", "-mabi=ilp32"}, []string{"-m", "elf32lriscv"})
	im := openFile(t, filepath.Join(dir, "fw.elf"))
	if im.Arch != "riscv32" || im.Format != "ELF (RISC-V, 32-bit)" {
		t.Fatalf("%+v", im)
	}
	g := best(t, openFile(t, filepath.Join(dir, "fw.bin")))
	if g.Arch != "riscv32" || g.Base != 0x08000000 || g.Family != "GD32VF103" || g.Entry <= 0x08000000 {
		t.Fatalf("%+v", g)
	}
	if disasm.ByID("riscv32") == nil {
		t.Skip("no RISC-V decoder")
	}
	statsCheck(t, filepath.Join(dir, "fw.bin"), 0x60, "riscv32")
}

func TestToolsXtensa(t *testing.T) {
	var b strings.Builder
	b.WriteString(".text\n.literal_position\n")
	for i := 0; i < 60; i++ {
		fmt.Fprintf(&b, ".literal .Lc%d, 0x%x\n.align 4\nf%d:\n addi a1, a1, -16\n s32i a0, a1, 0\n l32r a2, .Lc%d\n l32i a3, a2, %d\n movi a4, %d\n beq a3, a4, 1f\n s32i a4, a2, 4\n1:\n call0 f%d\n l32i a0, a1, 0\n addi a1, a1, 16\n ret\n",
			i, 0x60000000+i*0x100, i, i, (i%8)*4, i%100, (i+1)%60)
	}
	ldScript := "SECTIONS { . = 0x40100000; .text : { *(.literal .text) } }\n"
	dir := build(t, "xtensa-lx106-elf-", b.String(), ldScript, nil, nil)
	im := openFile(t, filepath.Join(dir, "fw.elf"))
	if im.Arch != "xtensa" || len(im.Segments) == 0 || im.Segments[0].Addr != 0x40100000 {
		t.Fatalf("%+v", im)
	}
	if disasm.ByID("xtensa") == nil {
		t.Skip("no Xtensa decoder")
	}
	// The literals come first: the code is after them
	statsCheck(t, filepath.Join(dir, "fw.bin"), 60*4, "xtensa")
}
