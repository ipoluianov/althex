package part

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"unicode/utf16"
)

// The GPT header (UEFI 2.10, 5.3.2): at LBA 1, the backup at the last LBA
const (
	gptSig       = "EFI PART"
	gptRevision  = 0x00010000
	gptMinHeader = 92
	// The most entries read; the specification asks for room for 128, a
	// count of millions is a damaged header
	gptMaxEntries = 65536
)

// Offsets of the fields of the header
const (
	ghRevision   = 8
	ghSize       = 12
	ghCRC        = 16
	ghReserved   = 20
	ghMyLBA      = 24
	ghAltLBA     = 32
	ghFirst      = 40
	ghLast       = 48
	ghDiskGUID   = 56
	ghEntriesLBA = 72
	ghCount      = 80
	ghEntrySize  = 84
	ghEntriesCRC = 88
)

// gptHeader is a header with where it is and what was found in it
type gptHeader struct {
	off                int64 // bytes
	revision, size     uint32
	crc, crcCalc       uint32
	reserved           uint32
	myLBA, altLBA      uint64
	first, last        uint64
	diskGUID           []byte
	entriesLBA         uint64
	count, entrySize   uint32
	entriesCRC         uint32
	entries            []byte // what could be read of the array
	entriesFull        bool   // all of the array was read
	entriesCalc        uint32 // the CRC32 of the array, when entriesFull
	sizeOK, crcOK, eOK bool
}

// readGPTHeader reads a header at lba; nil - no signature there
func readGPTHeader(d *disk, lba uint64, ss int) *gptHeader {
	off := bytesOf(lba, ss)
	b := d.readFull(off, int64(ss))
	if b == nil || string(b[:8]) != gptSig {
		return nil
	}
	le := binary.LittleEndian
	h := &gptHeader{
		off:        off,
		revision:   le.Uint32(b[ghRevision:]),
		size:       le.Uint32(b[ghSize:]),
		crc:        le.Uint32(b[ghCRC:]),
		reserved:   le.Uint32(b[ghReserved:]),
		myLBA:      le.Uint64(b[ghMyLBA:]),
		altLBA:     le.Uint64(b[ghAltLBA:]),
		first:      le.Uint64(b[ghFirst:]),
		last:       le.Uint64(b[ghLast:]),
		diskGUID:   b[ghDiskGUID : ghDiskGUID+16],
		entriesLBA: le.Uint64(b[ghEntriesLBA:]),
		count:      le.Uint32(b[ghCount:]),
		entrySize:  le.Uint32(b[ghEntrySize:]),
		entriesCRC: le.Uint32(b[ghEntriesCRC:]),
	}
	h.sizeOK = h.size >= gptMinHeader && int(h.size) <= ss
	if h.sizeOK {
		// The CRC is of the header with its own field taken as zero
		c := append([]byte(nil), b[:h.size]...)
		clear(c[ghCRC : ghCRC+4])
		h.crcCalc = crc32.ChecksumIEEE(c)
		h.crcOK = h.crcCalc == h.crc
	}
	if h.entrySize >= 128 && h.entrySize%128 == 0 && h.entrySize <= 4096 && h.count <= gptMaxEntries {
		n := int64(h.count) * int64(h.entrySize)
		eo := bytesOf(h.entriesLBA, ss)
		h.entries = d.read(eo, n)
		h.entriesFull = int64(len(h.entries)) == n
		h.entries = h.entries[:int64(len(h.entries))/int64(h.entrySize)*int64(h.entrySize)]
		if h.entriesFull {
			h.entriesCalc = crc32.ChecksumIEEE(h.entries)
			h.eOK = h.entriesCalc == h.entriesCRC
		}
	}
	return h
}

func (h *gptHeader) valid() bool { return h.crcOK && h.eOK }

// checkHeader reports what is wrong in one header; name is "primary" or "backup"
func checkHeader(t *Table, h *gptHeader, name string, ss int, wantLBA uint64, lbas uint64) {
	o := h.off
	if h.revision != gptRevision {
		t.issue(Warning, o+ghRevision, 4, "%s GPT header revision is 0x%08X, should be 0x00010000", name, h.revision)
	}
	if !h.sizeOK {
		t.issue(Error, o+ghSize, 4, "%s GPT header size is %d, should be from 92 to %d", name, h.size, ss)
	} else if !h.crcOK {
		t.issue(Error, o+ghCRC, 4, "%s GPT header CRC32 is 0x%08X, should be 0x%08X", name, h.crc, h.crcCalc)
	}
	if h.reserved != 0 {
		t.issue(Warning, o+ghReserved, 4, "%s GPT header reserved field is 0x%08X, should be 0", name, h.reserved)
	}
	if h.myLBA != wantLBA {
		t.issue(Error, o+ghMyLBA, 8, "%s GPT header says it is at LBA %d, it is at LBA %d", name, h.myLBA, wantLBA)
	}
	if h.first > h.last {
		t.issue(Error, o+ghFirst, 16, "%s GPT first usable LBA %d is after the last usable LBA %d", name, h.first, h.last)
	} else if h.last >= lbas {
		t.issue(Error, o+ghLast, 8, "%s GPT last usable LBA %d is beyond the end of the disk (%d sectors): the image may be truncated",
			name, h.last, lbas)
	}
	if h.entrySize < 128 || h.entrySize%128 != 0 || h.entrySize > 4096 {
		t.issue(Error, o+ghEntrySize, 4, "%s GPT entry size is %d, should be 128 or a multiple of it", name, h.entrySize)
		return
	}
	if h.count > gptMaxEntries {
		t.issue(Error, o+ghCount, 4, "%s GPT has %d entries, more than %d", name, h.count, gptMaxEntries)
		return
	}
	n := uint64(h.count) * uint64(h.entrySize)
	sectors := (n + uint64(ss) - 1) / uint64(ss)
	// The entries are outside the usable range: after the header in the
	// primary GPT, before it in the backup
	if h.entriesLBA < h.first && h.entriesLBA+sectors > h.first && wantLBA == 1 {
		t.issue(Error, o+ghEntriesLBA, 8, "%s GPT entries (LBA %d-%d) overlap the usable range from LBA %d",
			name, h.entriesLBA, h.entriesLBA+sectors-1, h.first)
	} else if h.entriesLBA >= h.first && h.entriesLBA <= h.last {
		t.issue(Error, o+ghEntriesLBA, 8, "%s GPT entries at LBA %d are inside the usable range %d-%d", name, h.entriesLBA, h.first, h.last)
	} else if h.entriesLBA <= wantLBA && h.entriesLBA+sectors > wantLBA {
		t.issue(Error, o+ghEntriesLBA, 8, "%s GPT entries at LBA %d overlap the header", name, h.entriesLBA)
	}
	if !h.entriesFull {
		t.issue(Error, o+ghEntriesLBA, 8, "%s GPT entries (LBA %d, %d bytes) go beyond the end of the disk", name, h.entriesLBA, n)
	} else if !h.eOK {
		t.issue(Error, o+ghEntriesCRC, 4, "%s GPT entries CRC32 is 0x%08X, should be 0x%08X", name, h.entriesCRC, h.entriesCalc)
	}
}

// readGPT reads a GPT with sectors of ss bytes. fromBackup: the primary
// header is not there, look for the backup at the last LBA. nil - no GPT.
func readGPT(d *disk, ss int, fromBackup bool) *Table {
	lbas := uint64(d.size / int64(ss))
	if lbas < 3 {
		return nil
	}
	lastLBA := lbas - 1
	t := &Table{Scheme: "GPT", SectorSize: ss}

	var prim, back *gptHeader
	if fromBackup {
		back = readGPTHeader(d, lastLBA, ss)
		if back == nil {
			return nil
		}
		t.issue(Error, int64(ss), int64(ss), "the primary GPT header at LBA 1 is missing; the backup is used")
		checkHeader(t, back, "backup", ss, lastLBA, lbas)
	} else {
		prim = readGPTHeader(d, 1, ss)
		if prim == nil {
			return nil
		}
		checkHeader(t, prim, "primary", ss, 1, lbas)
		back = findBackup(d, t, prim, ss, lbas)
		if back != nil {
			checkHeader(t, back, "backup", ss, bytesToLBA(back.off, ss), lbas)
			compareHeaders(t, prim, back)
		}
	}
	checkProtective(d, t, ss, lbas)

	// The partitions come from the primary unless it is damaged and the
	// backup is not
	use := prim
	if use == nil || (!use.crcOK && back != nil && back.valid()) {
		if prim != nil {
			t.issue(Warning, back.off, int64(ss), "the primary GPT header is damaged; the partitions are read from the backup")
		}
		use = back
	}
	t.DiskGUID = guidString(use.diskGUID)
	readEntries(t, use, ss)
	checkHybrid(d, t, ss)
	return t
}

func bytesToLBA(off int64, ss int) uint64 { return uint64(off / int64(ss)) }

// findBackup reads the backup header where the primary says it is, else
// at the last LBA
func findBackup(d *disk, t *Table, prim *gptHeader, ss int, lbas uint64) *gptHeader {
	last := lbas - 1
	po := prim.off
	if prim.altLBA >= lbas {
		t.issue(Error, po+ghAltLBA, 8, "the backup GPT header is at LBA %d, beyond the end of the disk (%d sectors): the image may be truncated",
			prim.altLBA, lbas)
	} else if prim.altLBA > 1 {
		if h := readGPTHeader(d, prim.altLBA, ss); h != nil {
			if prim.altLBA != last {
				t.issue(Warning, po+ghAltLBA, 8, "the backup GPT header is at LBA %d, not at the last LBA %d (was the disk enlarged?)",
					prim.altLBA, last)
			}
			return h
		}
	}
	if prim.altLBA != last {
		if h := readGPTHeader(d, last, ss); h != nil {
			t.issue(Error, po+ghAltLBA, 8, "the primary GPT header says the backup is at LBA %d, it is at LBA %d", prim.altLBA, last)
			return h
		}
	}
	at := bytesOf(min(prim.altLBA, last), ss)
	t.issue(Error, at, int64(ss), "the backup GPT header is missing (no \"EFI PART\" at LBA %d)", min(prim.altLBA, last))
	return nil
}

// compareHeaders reports where the backup differs from the primary
func compareHeaders(t *Table, p, b *gptHeader) {
	bo := b.off
	diff := func(field int64, n int64, what string, pv, bv any) {
		t.issue(Error, bo+field, n, "backup GPT %s is %v, the primary has %v", what, bv, pv)
	}
	if b.altLBA != 1 {
		diff(ghAltLBA, 8, "alternate LBA", 1, b.altLBA)
	}
	if b.first != p.first {
		diff(ghFirst, 8, "first usable LBA", p.first, b.first)
	}
	if b.last != p.last {
		diff(ghLast, 8, "last usable LBA", p.last, b.last)
	}
	if !bytes.Equal(b.diskGUID, p.diskGUID) {
		diff(ghDiskGUID, 16, "disk GUID", guidString(p.diskGUID), guidString(b.diskGUID))
	}
	if b.count != p.count {
		diff(ghCount, 4, "number of entries", p.count, b.count)
	}
	if b.entrySize != p.entrySize {
		diff(ghEntrySize, 4, "entry size", p.entrySize, b.entrySize)
	}
	if b.entriesCRC != p.entriesCRC {
		diff(ghEntriesCRC, 4, "entries CRC32", fmt.Sprintf("0x%08X", p.entriesCRC), fmt.Sprintf("0x%08X", b.entriesCRC))
	}
	// The entries themselves: the first one that differs
	if b.entrySize == p.entrySize && b.entrySize > 0 {
		es := int(b.entrySize)
		n := min(len(p.entries), len(b.entries))
		for i := 0; i+es <= n; i += es {
			if !bytes.Equal(p.entries[i:i+es], b.entries[i:i+es]) {
				eo := bytesOf(b.entriesLBA, t.SectorSize) + int64(i)
				t.issue(Error, eo, int64(es), "backup GPT entry %d differs from the primary", i/es+1)
				break
			}
		}
	}
}

// checkProtective checks the protective MBR of a GPT disk; the hybrid one
// is checked after the entries are read
func checkProtective(d *disk, t *Table, ss int, lbas uint64) {
	b := d.readFull(0, 512)
	if b == nil {
		return
	}
	if !hasSig(b) {
		t.issue(Warning, mbrSig, 2, "the protective MBR has no signature 55 AA")
	}
	found := false
	for i := 0; i < 4; i++ {
		e := parseEntry(b[mbrTable+i*16:])
		if e.typ != 0xEE {
			continue
		}
		found = true
		off := int64(mbrTable + i*16)
		if e.start != 1 {
			t.issue(Warning, off+8, 4, "the protective MBR partition starts at LBA %d, should be 1", e.start)
		}
		want := min(lbas-1, 0xFFFFFFFF)
		if uint64(e.count) != want && uint64(e.count) != 0xFFFFFFFF {
			t.issue(Warning, off+12, 4, "the protective MBR partition has %d sectors, should be %d", e.count, want)
		}
	}
	if !found && hasSig(b) {
		t.issue(Warning, mbrTable, 64, "the MBR has no protective partition of type 0xEE")
	}
}

// checkHybrid: primary MBR entries other than 0xEE make a hybrid MBR;
// each of them should be the same as a GPT partition
func checkHybrid(d *disk, t *Table, ss int) {
	b := d.readFull(0, 512)
	if b == nil || !hasSig(b) {
		return
	}
	hasEE, others := false, 0
	for i := 0; i < 4; i++ {
		switch b[mbrTable+i*16+4] {
		case 0:
		case 0xEE:
			hasEE = true
		default:
			others++
		}
	}
	if !hasEE || others == 0 {
		return
	}
	t.Scheme = "GPT (hybrid MBR)"
	for i := 0; i < 4; i++ {
		e := parseEntry(b[mbrTable+i*16:])
		if e.typ == 0 || e.typ == 0xEE {
			continue
		}
		start, size := bytesOf(uint64(e.start), ss), bytesOf(uint64(e.count), ss)
		match := false
		for _, p := range t.Partitions {
			if p.Start == start && p.Size == size {
				match = true
				break
			}
		}
		if !match {
			t.issue(Warning, int64(mbrTable+i*16), 16, "hybrid MBR partition %d (LBA %d, %d sectors) matches no GPT partition",
				i+1, e.start, e.count)
		}
	}
}

// readEntries makes the partitions of the entries of a header
func readEntries(t *Table, h *gptHeader, ss int) {
	if h.entrySize == 0 || len(h.entries) == 0 {
		return
	}
	es := int(h.entrySize)
	le := binary.LittleEndian
	base := bytesOf(h.entriesLBA, ss)
	for i := 0; i+es <= len(h.entries); i += es {
		e := h.entries[i : i+es]
		if allZero(e[:16]) {
			continue // an unused entry
		}
		n := i/es + 1
		off := base + int64(i)
		first, last := le.Uint64(e[32:]), le.Uint64(e[40:])
		attrs := le.Uint64(e[48:])
		typeID := guidString(e[:16])
		p := Partition{
			Index: n, Scheme: "GPT",
			Start: bytesOf(first, ss), TypeID: typeID, Type: TypeName("GPT", typeID),
			Name: utf16Name(e[56:128]), GUID: guidString(e[16:32]),
			Bootable: attrs&4 != 0, Attrs: attrs,
			EntryOff: off, EntryLen: int64(es),
		}
		if last < first {
			t.issue(Error, off+32, 16, "GPT partition %d ends (LBA %d) before it starts (LBA %d)", n, last, first)
		} else {
			p.Size = bytesOf(last-first+1, ss)
			if first < h.first || last > h.last {
				t.issue(Error, off+32, 16, "GPT partition %d (LBA %d-%d) is outside the usable range %d-%d",
					n, first, last, h.first, h.last)
			}
		}
		if allZero(e[16:32]) {
			t.issue(Warning, off+16, 16, "GPT partition %d has no unique GUID", n)
		}
		t.Partitions = append(t.Partitions, p)
	}
}

func allZero(b []byte) bool {
	for _, c := range b {
		if c != 0 {
			return false
		}
	}
	return true
}

// guidString formats a GUID as Windows shows it: the first three fields
// are little-endian
func guidString(b []byte) string {
	if len(b) < 16 {
		return ""
	}
	le := binary.LittleEndian
	return fmt.Sprintf("%08X-%04X-%04X-%02X%02X-%02X%02X%02X%02X%02X%02X",
		le.Uint32(b), le.Uint16(b[4:]), le.Uint16(b[6:]), b[8], b[9], b[10], b[11], b[12], b[13], b[14], b[15])
}

// utf16Name decodes a UTF-16LE name up to its first zero
func utf16Name(b []byte) string {
	u := make([]uint16, 0, len(b)/2)
	for i := 0; i+1 < len(b); i += 2 {
		c := binary.LittleEndian.Uint16(b[i:])
		if c == 0 {
			break
		}
		u = append(u, c)
	}
	return string(utf16.Decode(u))
}
