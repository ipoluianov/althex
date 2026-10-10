package part

import (
	"encoding/binary"
	"fmt"
)

// The MBR: boot code, the disk signature at 440 (the "modern" MBR of
// Windows NT and later), four entries of 16 bytes at 446, 55 AA at 510
const (
	mbrTable   = 446
	mbrSig     = 510
	mbrDiskID  = 440
	maxLogical = 128
)

// mbrEntry is an entry of the table of an MBR or an EBR
type mbrEntry struct {
	status byte
	typ    byte
	start  uint32 // LBA; the CHS fields are ignored, as by all systems now
	count  uint32
}

func parseEntry(b []byte) mbrEntry {
	return mbrEntry{
		status: b[0],
		typ:    b[4],
		start:  binary.LittleEndian.Uint32(b[8:]),
		count:  binary.LittleEndian.Uint32(b[12:]),
	}
}

func isExtended(t byte) bool { return t == 0x05 || t == 0x0F || t == 0x85 }

func mbrTypeID(t byte) string { return fmt.Sprintf("0x%02X", t) }

// hasSig tells whether a sector ends with 55 AA
func hasSig(b []byte) bool { return len(b) >= 512 && b[mbrSig] == 0x55 && b[mbrSig+1] == 0xAA }

// mbrHasType tells whether a primary entry of sector 0 has the type
func mbrHasType(d *disk, typ byte) bool {
	b := d.readFull(0, 512)
	if b == nil {
		return false
	}
	for i := 0; i < 4; i++ {
		if b[mbrTable+i*16+4] == typ {
			return true
		}
	}
	return false
}

// looksLikeVBR tells a boot sector of a file system (FAT, NTFS, exFAT):
// it ends with 55 AA too, but its bytes at 446 are code, not a table
func looksLikeVBR(b []byte) bool {
	oem := string(b[3:11])
	if oem == "NTFS    " || oem == "EXFAT   " {
		return true
	}
	if b[0] != 0xEB && b[0] != 0xE9 {
		return false
	}
	bps := binary.LittleEndian.Uint16(b[11:])
	spc := b[13]
	return (bps == 512 || bps == 1024 || bps == 2048 || bps == 4096) && spc != 0 && spc&(spc-1) == 0
}

// readMBR reads a classic or modern MBR with its EBR chain; nil - sector
// 0 is not an MBR
func readMBR(d *disk, ss int) *Table {
	b := d.readFull(0, 512)
	if b == nil {
		return nil
	}
	var e [4]mbrEntry
	valid, used := 0, 0
	for i := range e {
		e[i] = parseEntry(b[mbrTable+i*16:])
		if e[i].status == 0 || e[i].status == 0x80 {
			valid++
		}
		if e[i].typ != 0 {
			used++
		}
	}
	sig := hasSig(b)
	switch {
	case !sig && (valid < 4 || used == 0):
		return nil // no signature and no plausible table: not an MBR
	case sig && valid < 4 && looksLikeVBR(b):
		return nil
	case sig && valid == 0:
		return nil
	}

	t := &Table{Scheme: "MBR", SectorSize: ss}
	if id := binary.LittleEndian.Uint32(b[mbrDiskID:]); id != 0 {
		t.DiskGUID = fmt.Sprintf("0x%08X", id)
	}
	if !sig {
		t.issue(Error, mbrSig, 2, "the MBR signature 55 AA is missing (%02X %02X)", b[mbrSig], b[mbrSig+1])
	}
	extSeen := false
	for i, x := range e {
		off := int64(mbrTable + i*16)
		if x.status != 0 && x.status != 0x80 {
			t.issue(Warning, off, 1, "partition %d has the status 0x%02X, should be 0x00 or 0x80", i+1, x.status)
		}
		if x.typ == 0 {
			continue
		}
		p := Partition{
			Index: i + 1, Scheme: "MBR",
			Start: bytesOf(uint64(x.start), ss), Size: bytesOf(uint64(x.count), ss),
			TypeID: mbrTypeID(x.typ), Type: TypeName("MBR", mbrTypeID(x.typ)),
			Bootable: x.status == 0x80, Extended: isExtended(x.typ),
			EntryOff: off, EntryLen: 16,
		}
		if x.count == 0 {
			t.issue(Warning, off+12, 4, "partition %d has the type 0x%02X but no sectors", i+1, x.typ)
		}
		if x.start == 0 && x.count != 0 {
			t.issue(Error, off+8, 4, "partition %d starts at sector 0, over the MBR", i+1)
		}
		t.Partitions = append(t.Partitions, p)
		if p.Extended {
			if extSeen {
				t.issue(Warning, off+4, 1, "partition %d is a second extended partition; its logical partitions are not read", i+1)
				continue
			}
			extSeen = true
			readEBRs(d, t, ss, x)
		}
	}
	return t
}

// readEBRs follows the chain of EBRs of an extended partition. Each EBR
// has the logical partition (relative to the EBR) in its first entry and
// the next EBR (relative to the extended partition) in the second.
func readEBRs(d *disk, t *Table, ss int, ext mbrEntry) {
	base := uint64(ext.start)
	extEnd := base + uint64(ext.count)
	cur := base
	seen := map[uint64]bool{}
	index := 5
	for n := 0; ; n++ {
		ebrOff := bytesOf(cur, ss)
		if seen[cur] {
			t.issue(Error, ebrOff+mbrTable+16, 16, "the EBR chain loops back to sector %d", cur)
			return
		}
		seen[cur] = true
		if n >= maxLogical {
			t.issue(Warning, ebrOff, 512, "more than %d logical partitions; the rest are not read", maxLogical)
			return
		}
		b := d.readFull(ebrOff, 512)
		if b == nil {
			t.issue(Error, ebrOff, 512, "the EBR at sector %d is beyond the end of the disk", cur)
			return
		}
		if !hasSig(b) {
			t.issue(Error, ebrOff+mbrSig, 2, "the EBR at sector %d has no signature 55 AA", cur)
			return
		}
		l := parseEntry(b[mbrTable:])
		next := parseEntry(b[mbrTable+16:])
		entOff := ebrOff + mbrTable
		if l.typ != 0 {
			start := cur + uint64(l.start)
			p := Partition{
				Index: index, Scheme: "EBR",
				Start: bytesOf(start, ss), Size: bytesOf(uint64(l.count), ss),
				TypeID: mbrTypeID(l.typ), Type: TypeName("MBR", mbrTypeID(l.typ)),
				Bootable: l.status == 0x80, Logical: true,
				EntryOff: entOff, EntryLen: 16,
			}
			if isExtended(l.typ) {
				t.issue(Warning, entOff+4, 1, "logical partition %d has an extended type", index)
			}
			if start+uint64(l.count) > extEnd || l.start == 0 {
				t.issue(Error, entOff+8, 8, "logical partition %d is not inside the extended partition", index)
			}
			t.Partitions = append(t.Partitions, p)
			index++
		}
		for i := 2; i < 4; i++ {
			if b[mbrTable+i*16+4] != 0 {
				t.issue(Warning, entOff+int64(i)*16, 16, "the EBR at sector %d has an entry %d; only two are used", cur, i+1)
			}
		}
		if next.typ == 0 || next.count == 0 && next.start == 0 {
			return
		}
		if !isExtended(next.typ) {
			t.issue(Warning, entOff+16+4, 1, "the link to the next EBR has the type 0x%02X, not an extended one", next.typ)
		}
		nb := base + uint64(next.start)
		if next.start == 0 || nb >= extEnd {
			t.issue(Error, entOff+16+8, 4, "the next EBR (sector %d) is outside the extended partition", nb)
			return
		}
		cur = nb
	}
}
