package analysis_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ipoluianov/althex/core/analysis"
	"github.com/ipoluianov/althex/core/disasm"
	"github.com/ipoluianov/althex/core/fw"
)

// tool finds a GNU binutils program: in ALTHEX_BINUTILS or on PATH
func tool(t *testing.T, name string) string {
	t.Helper()
	if d := os.Getenv("ALTHEX_BINUTILS"); d != "" {
		if p := filepath.Join(d, name); fileExists(p) {
			return p
		}
	}
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	t.Skipf("no %s", name)
	return ""
}

func fileExists(p string) bool { _, err := os.Stat(p); return err == nil }

func run(t *testing.T, dir, prog string, args ...string) {
	t.Helper()
	cmd := exec.Command(prog, args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %v: %v\n%s", filepath.Base(prog), args, err, out)
	}
}

// assemble makes a raw binary at base from an assembler source
func assemble(t *testing.T, dir, prefix, src string, base uint64, asArgs ...string) []byte {
	t.Helper()
	as, ld, objcopy := tool(t, prefix+"as"), tool(t, prefix+"ld"), tool(t, prefix+"objcopy")
	os.WriteFile(filepath.Join(dir, "in.s"), []byte(src), 0o644)
	run(t, dir, as, append(asArgs, "-o", "in.o", "in.s")...)
	// Every section of the source at base, in order
	run(t, dir, ld, "-e", fmt.Sprint(base), "-Ttext", fmt.Sprintf("0x%X", base), "-o", "in.elf", "in.o")
	run(t, dir, objcopy, "-O", "binary", "in.elf", "in.bin")
	b, err := os.ReadFile(filepath.Join(dir, "in.bin"))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// roundTrip analyzes a firmware, exports it and assembles the export again:
// the bytes must be the same (FW-10)
func roundTrip(t *testing.T, prefix, arch string, src string, minInsts int, base uint64, entries []analysis.Entry, asArgs ...string) {
	t.Helper()
	header, footer, comment := analysis.AsmSyntax(arch)
	dir := t.TempDir()
	orig := assemble(t, dir, prefix, src, base, asArgs...)
	img, err := fw.Open(bytes.NewReader(orig), int64(len(orig)), "fw.bin")
	if err != nil {
		t.Fatal(err)
	}
	mem := &rebasedMem{img: img, base: base}
	if arch == "arm" { // the vector table, as the window does
		for i := 1; i < 4; i++ {
			var w [4]byte
			mem.ReadAddr(w[:], base+uint64(i*4))
			v := uint64(w[0]) | uint64(w[1])<<8 | uint64(w[2])<<16 | uint64(w[3])<<24
			entries = append(entries, analysis.Entry{Addr: v &^ 1, Mode: disasm.ModeThumb})
		}
	}
	res := analysis.Analyze(context.Background(), mem, analysis.Options{Arch: disasm.ByID(arch), Entries: entries})
	if len(res.Insts) == 0 {
		t.Fatal("no code found")
	}
	if len(res.Insts) < minInsts {
		t.Errorf("%d instructions found, want at least %d", len(res.Insts), minInsts)
	}
	ranges := []analysis.Range{{Name: "text", Addr: base, Size: uint64(len(orig))}}
	var out bytes.Buffer
	err = res.WriteAsm(&out, ranges, analysis.AsmOptions{Header: header, Footer: footer, Comment: comment,
		Read: func(p []byte, addr uint64) { mem.ReadAddr(p, addr) }})
	if err != nil {
		t.Fatal(err)
	}
	// The export is assembled with .text renamed: one section at base
	exported := strings.Replace(out.String(), `.section .text, "ax"`, ".text", 1)
	dir2 := t.TempDir()
	again := assemble(t, dir2, prefix, exported, base, asArgs...)
	if !bytes.Equal(orig, again) {
		os.WriteFile(filepath.Join(dir2, "orig.bin"), orig, 0o644)
		n := 0
		for n < len(orig) && n < len(again) && orig[n] == again[n] {
			n++
		}
		lines := strings.Split(exported, "\n")
		t.Fatalf("%d bytes vs %d, first difference at 0x%X; export (%d instructions):\n%s",
			len(orig), len(again), base+uint64(n), len(res.Insts), strings.Join(lines[:min(len(lines), 400)], "\n"))
	}
	if testing.Verbose() {
		t.Log(exported)
	}
	t.Logf("%d bytes, %d instructions, %d functions: same bytes", len(orig), len(res.Insts), len(res.Funcs))
}

// rebasedMem shows a raw binary at its base address
type rebasedMem struct {
	img  *fw.Image
	base uint64
}

func (m *rebasedMem) ReadAddr(p []byte, addr uint64) (int, error) {
	return m.img.ReadAddr(p, addr-m.base)
}

func (m *rebasedMem) Contains(addr uint64) bool {
	return addr >= m.base && m.img.Contains(addr-m.base)
}

const thumbSrc = `
	.syntax unified
	.thumb
	.cpu cortex-m4
	.text
vectors:
	.word 0x20005000
	.word reset + 1
	.word nmi + 1
	.word fault + 1
	.thumb_func
reset:
	ldr r0, =0x40021000
	ldr r1, [r0, #24]
	orr r1, r1, #4
	str r1, [r0, #24]
	movw r2, #0x1234
	movt r2, #0x4001
	cmp r1, #0
	it eq
	addeq r1, r1, #1
	bl setup
	bl table
	ldr r3, =msg
loop:
	ldrb r4, [r3], #1
	cbz r4, done
	str r4, [r2, #4]
	b loop
done:
	wfi
	b.w done
	.thumb_func
setup:
	push {r4-r7, lr}
	movs r4, #10
1:	subs r4, r4, #1
	bne 1b
	ldr.w r5, [sp, #-8]!
	add sp, #8
	ubfx r6, r5, #3, #5
	mul r7, r6, r5
	sdiv r7, r7, r4
	pop {r4-r7, pc}
	.thumb_func
table:
	tbb [pc, r0]
	.byte 2, 3, 4, 5
	nop
	nop
	bx lr
	.thumb_func
nmi:
	b nmi
	.thumb_func
fault:
	vmov s0, r1
	vadd.f32 s1, s0, s0
	vmov r1, s1
	b fault
	.ltorg
msg:
	.asciz "hello"
	.align 2
`

func TestRoundTripThumb(t *testing.T) {
	const base = 0x08000000
	roundTrip(t, "arm-none-eabi-", "arm", thumbSrc, 34, base, []analysis.Entry{
		{Addr: base + 0x10, Mode: disasm.ModeThumb, Name: "reset"},
	}, "-mcpu=cortex-m4", "-mfpu=fpv4-sp-d16", "-mfloat-abi=hard")
}

const avrSrc = `
	.text
vectors:
	jmp reset
	jmp timer
	jmp timer
reset:
	ldi r16, 0xFF
	out 0x04, r16
	ldi r30, lo8(msg)
	ldi r31, hi8(msg)
	ldi r24, 10
	call setup
loop:
	lpm r18, Z+
	tst r18
	breq done
	sts 0x00C6, r18
	rjmp loop
done:
	sei
	sleep
	rjmp done
setup:
	push r28
	in r28, 0x3D
	movw r26, r24
	adiw r26, 1
	ldd r20, Y+3
	std Z+2, r20
	sbrs r24, 0
	rcall twice
	mul r24, r25
	pop r28
	ret
twice:
	lsl r24
	ret
timer:
	push r1
	cbi 0x05, 5
	sbi 0x05, 5
	pop r1
	reti
msg:
	.asciz "hi!"
	.align 1
`

func TestRoundTripAVR(t *testing.T) {
	roundTrip(t, "avr-", "avr", avrSrc, 35, 0, []analysis.Entry{{Addr: 0, Name: "vectors"}, {Addr: 4}, {Addr: 8}}, "-mmcu=atmega328p")
}
