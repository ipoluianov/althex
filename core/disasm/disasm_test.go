package disasm

import (
	"bytes"
	"testing"
)

func TestX86Boot(t *testing.T) {
	// A boot sector start: cli; xor ax,ax; mov ah,0x0e; mov al,'A'; int 0x10; jmp $
	code := []byte{0xFA, 0x31, 0xC0, 0xB4, 0x0E, 0xB0, 0x41, 0xCD, 0x10, 0xEB, 0xFE}
	insts := Linear(ByID("x86-16"), bytes.NewReader(code), 0, 0x7C00, 10)
	if len(insts) != 6 {
		t.Fatalf("%d instructions", len(insts))
	}
	if insts[0].Mnemonic != "cli" || insts[0].Addr != 0x7C00 {
		t.Fatalf("first %+v", insts[0])
	}
	if insts[4].Comment != "teletype output" {
		t.Fatalf("int 0x10: %q", insts[4].Comment)
	}
	jmp := insts[5]
	if jmp.Kind != Jump || !jmp.HasTarget || jmp.Target != 0x7C09 {
		t.Fatalf("jmp $: %+v", jmp)
	}
}

func TestX86Invalid(t *testing.T) {
	// 0x0F 0xFF is not an instruction: one byte of data, then on
	insts := Linear(ByID("x86-64"), bytes.NewReader([]byte{0x0F, 0xFF, 0x90}), 0, 0, 5)
	if len(insts) < 2 || insts[0].Kind != Invalid || insts[len(insts)-1].Mnemonic != "nop" {
		t.Fatalf("%+v", insts)
	}
}
