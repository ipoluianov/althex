package main

import (
	"regexp"
	"strings"
)

// The intermediate model shared by the SVD and ATDF parsers and the encoder.

type region struct {
	Name  string
	Start uint64
	Size  uint64
	Kind  string // flash, ram, periph, rom, eeprom, fuses, regs, ...
	Space string // "" (unified address space) or an AVR address space ("prog", "data", ...)
}

type interrupt struct {
	Number int
	Name   string
	Desc   string
}

type enumValue struct {
	Name  string
	Desc  string
	Value uint64
}

type field struct {
	Name   string
	Desc   string
	Bit    int
	Bits   int
	Mask   uint64 // non-zero only for a non-contiguous mask (ATDF)
	Values []enumValue
}

// reg is a register (or a register array: Dim > 1) relative to the peripheral base.
type reg struct {
	Name   string // may contain %s when Dim > 1
	Desc   string
	Offset uint64
	Bits   int
	Reset  uint64
	Dim    int
	Stride uint64
	DimIdx []string // empty: 0..Dim-1
	Fields []field
}

type layout struct {
	Regs []reg
	key  string // content key for deduplication (filled by the encoder)
	// placement, filled by the encoder
	chunk, index int
}

type periphInst struct {
	Name   string
	Desc   string
	Group  string
	Base   uint64
	Size   uint64
	Layout *layout
}

type part struct {
	Pattern string
	Flash   uint64
	RAM     uint64
}

type device struct {
	Name        string
	Vendor      string
	Family      string
	Arch        string
	Core        string
	Description string
	Patterns    []string
	Parts       []part
	Memory      []region
	LoadAddr    uint64
	Interrupts  []interrupt
	Periphs     []periphInst
	Source      string
}

var wsRe = regexp.MustCompile(`\s+`)

// clean normalises a description: collapses white space, limits the length.
func clean(s string) string {
	s = strings.ReplaceAll(s, `\n`, " ")
	s = strings.TrimSpace(wsRe.ReplaceAllString(s, " "))
	const max = 600
	if len(s) > max {
		cut := max
		for cut > 0 && (s[cut]&0xC0) == 0x80 { // do not cut a UTF-8 sequence
			cut--
		}
		s = s[:cut] + "..."
	}
	return s
}
