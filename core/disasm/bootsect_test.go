package disasm

import (
	"bytes"
	"testing"
)

func TestBootSectorVBR(t *testing.T) {
	s := make([]byte, 512)
	copy(s, []byte{0xEB, 0x3C, 0x90})
	copy(s[3:], "MSDOS5.0")
	// A BPB that is not code: these bytes decode as anything
	for i := 11; i < 0x3E; i++ {
		s[i] = byte(i * 37)
	}
	// At 0x3E: mov ah, 0x0E; int 0x10; jmp $
	copy(s[0x3E:], []byte{0xB4, 0x0E, 0xCD, 0x10, 0xEB, 0xFE})
	s[510], s[511] = 0x55, 0xAA
	insts := BootSector(bytes.NewReader(s), 0)
	if insts[0].Text() != "jmp 0x7c3e" || insts[0].Addr != 0x7C00 {
		t.Fatalf("first: %+v", insts[0])
	}
	if insts[1].Text() != "nop" {
		t.Fatalf("second: %+v", insts[1])
	}
	// The BPB is data: db rows from 0x7C03 to 0x7C3E
	var at uint64 = 0x7C03
	i := 2
	for ; insts[i].Mnemonic == "db"; i++ {
		if insts[i].Addr != at {
			t.Fatalf("db at %X, want %X", insts[i].Addr, at)
		}
		at += uint64(insts[i].Len)
	}
	if at != 0x7C3E || insts[2].Comment != `BIOS parameter block (OEM "MSDOS5.0")` {
		t.Fatalf("data to %X, %q", at, insts[2].Comment)
	}
	if insts[i].Text() != "mov ah, 0xe" || insts[i+1].Comment != "teletype output" || insts[i+2].Text() != "jmp 0x7c42" {
		t.Fatalf("code: %v %v %v", insts[i], insts[i+1], insts[i+2])
	}
	last := insts[len(insts)-1]
	if last.Addr != 0x7C00+510 || last.Comment != "boot signature" {
		t.Fatalf("last: %+v", last)
	}
	// Every byte is listed once, in order
	at = 0x7C00
	for _, in := range insts {
		if in.Addr != at {
			t.Fatalf("%X, want %X", in.Addr, at)
		}
		at += uint64(in.Len)
	}
	if at != 0x7E00 {
		t.Fatalf("ends at %X", at)
	}
}

func TestBootSectorNearJump(t *testing.T) {
	s := make([]byte, 512)
	copy(s, []byte{0xE9, 0x50, 0x00}) // to 0x53
	copy(s[0x53:], []byte{0xCD, 0x19})
	insts := BootSector(bytes.NewReader(s), 0)
	if insts[0].Text() != "jmp 0x7c53" || insts[1].Mnemonic != "db" || insts[1].Addr != 0x7C03 {
		t.Fatalf("%v %+v", insts[0], insts[1])
	}
	for _, in := range insts {
		if in.Addr == 0x7C53 {
			if in.Comment != "reboot (bootstrap loader)" {
				t.Fatalf("%+v", in)
			}
			return
		}
	}
	t.Fatal("no instruction at the target")
}

func TestBootSectorMBR(t *testing.T) {
	s := make([]byte, 512)
	copy(s, []byte{0xFA, 0x31, 0xC0, 0x8E, 0xD0}) // cli; xor ax, ax; mov ss, ax
	s[0x1BE], s[0x1BE+4] = 0x80, 0x83
	s[510], s[511] = 0x55, 0xAA
	insts := BootSector(bytes.NewReader(s), 0)
	if insts[0].Text() != "cli" {
		t.Fatalf("%+v", insts[0])
	}
	var sig, entry bool
	for _, in := range insts {
		if in.Addr >= 0x7C00+0x1B8 && in.Addr < 0x7C00+510 && in.Mnemonic != "db" {
			t.Fatalf("code in the table: %+v", in)
		}
		sig = sig || in.Comment == "disk signature" && in.Addr == 0x7DB8
		entry = entry || in.Comment == "partition entry 1" && in.Addr == 0x7DBE
	}
	if !sig || !entry {
		t.Fatal("no table data")
	}
	if BootSector(bytes.NewReader(nil), 0) != nil {
		t.Error("an empty sector")
	}
}
