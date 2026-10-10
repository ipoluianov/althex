package part

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"strconv"
)

// ---- Apple Partition Map ----

// The Driver Descriptor Map is block 0 ("ER"); the map entries ("PM") are
// blocks 1, 2... each with the number of entries in the map. Starts and
// sizes are in blocks of the size the DDM gives.
const maxAPM = 1024

func readAPM(d *disk, ss int) *Table {
	b0 := d.readFull(0, 512)
	if b0 == nil {
		return nil
	}
	be := binary.BigEndian
	bs := 512
	var blocks uint32
	if string(b0[:2]) == "ER" {
		blocks = be.Uint32(b0[4:])
		if v := int(be.Uint16(b0[2:])); v == 512 || v == 1024 || v == 2048 || v == 4096 {
			bs = v
		}
	}
	first := d.readFull(int64(bs), 512)
	if first == nil || string(first[:2]) != "PM" {
		if bs == 512 {
			return nil
		}
		// Some images give a block size the map does not follow
		bs = 512
		if first = d.readFull(512, 512); first == nil || string(first[:2]) != "PM" {
			return nil
		}
	}
	count := be.Uint32(first[4:])
	if count == 0 {
		return nil
	}
	t := &Table{Scheme: "APM", SectorSize: bs}
	if blocks != 0 && bytesOf(uint64(blocks), bs) > d.size {
		t.issue(Warning, 4, 4, "the disk has %d blocks of %d bytes, more than the image: the image may be truncated", blocks, bs)
	}
	if count > maxAPM {
		t.issue(Error, int64(bs)+4, 4, "the partition map has %d entries, more than %d are not read", count, maxAPM)
		count = maxAPM
	}
	for i := 1; i <= int(count); i++ {
		off := int64(i) * int64(bs)
		e := d.readFull(off, 512)
		if e == nil {
			t.issue(Error, off, int64(bs), "the partition map entry %d is beyond the end of the disk", i)
			break
		}
		if string(e[:2]) != "PM" {
			t.issue(Error, off, 2, "the partition map entry %d has no signature \"PM\"", i)
			break
		}
		if n := be.Uint32(e[4:]); n != be.Uint32(first[4:]) {
			t.issue(Warning, off+4, 4, "the partition map entry %d says the map has %d entries, the first says %d", i, n, be.Uint32(first[4:]))
		}
		typ := cString(e[48:80])
		if typ == "Apple_Free" {
			continue // free space, not a partition
		}
		status := be.Uint32(e[88:])
		t.Partitions = append(t.Partitions, Partition{
			Index: i, Scheme: "APM",
			Start: bytesOf(uint64(be.Uint32(e[8:])), bs), Size: bytesOf(uint64(be.Uint32(e[12:])), bs),
			TypeID: typ, Type: TypeName("APM", typ), Name: cString(e[16:48]),
			Bootable: status&0x08 != 0, Attrs: uint64(status),
			EntryOff: off, EntryLen: 512,
		})
	}
	return t
}

// cString is text up to its first zero byte
func cString(b []byte) string {
	if i := bytes.IndexByte(b, 0); i >= 0 {
		b = b[:i]
	}
	return string(b)
}

// ---- BSD disklabel ----

const (
	bsdMagic   = 0x82564557
	bsdHeader  = 148 // the partitions follow the header
	bsdMaxPart = 22  // what fits in a sector after the header
)

// The label is in sector 1 (i386, amd64) or at 64 in sector 0 (some other
// ports), little- or big-endian as the machine was
func readBSD(d *disk, ss int) *Table {
	for _, off := range []int64{512, 64} {
		b := d.read(off, 512)
		if len(b) < bsdHeader {
			continue
		}
		for _, bo := range []binary.ByteOrder{binary.LittleEndian, binary.BigEndian} {
			if bo.Uint32(b) == bsdMagic && bo.Uint32(b[132:]) == bsdMagic {
				return bsdTable(d, b, off, bo, ss)
			}
		}
	}
	return nil
}

func bsdTable(d *disk, b []byte, off int64, bo binary.ByteOrder, ss int) *Table {
	secsize := int(bo.Uint32(b[40:]))
	t := &Table{Scheme: "BSD", SectorSize: ss, whole: map[int]bool{}}
	if secsize >= 512 && secsize <= 65536 && secsize&(secsize-1) == 0 {
		t.SectorSize = secsize
	} else {
		t.issue(Warning, off+40, 4, "the disklabel sector size is %d; %d is used", secsize, ss)
	}
	n := int(bo.Uint16(b[138:]))
	maxN := min(bsdMaxPart, (len(b)-bsdHeader)/16)
	if n > maxN {
		t.issue(Error, off+138, 2, "the disklabel has %d partitions, more than %d", n, maxN)
		n = maxN
	} else {
		// The checksum: the XOR of the 16-bit words of the label is 0
		var x uint16
		for i := 0; i+1 < bsdHeader+16*n; i += 2 {
			x ^= bo.Uint16(b[i:])
		}
		if x != 0 {
			stored := bo.Uint16(b[136:])
			t.issue(Error, off+136, 2, "the disklabel checksum is 0x%04X, should be 0x%04X", stored, stored^x)
		}
	}
	total := uint64(bo.Uint32(b[60:]))
	if total != 0 && bytesOf(total, t.SectorSize) > d.size {
		t.issue(Warning, off+60, 4, "the disklabel says the disk has %d sectors, more than the image: the image may be truncated", total)
	}
	// Offsets are from the start of the disk; in a slice opened alone the
	// raw partition 'c' starts at the slice, so they are made relative to it
	var base uint64
	if n > 2 {
		base = uint64(bo.Uint32(b[bsdHeader+2*16+4:]))
	}
	for i := 0; i < n; i++ {
		e := b[bsdHeader+i*16:]
		size, start := uint64(bo.Uint32(e)), uint64(bo.Uint32(e[4:]))
		if size == 0 {
			continue
		}
		if start >= base {
			start -= base
		}
		fs := strconv.Itoa(int(e[12]))
		if i == 2 || (start == 0 && total != 0 && size >= total) {
			t.whole[len(t.Partitions)] = true
		}
		name := string(rune('a' + i))
		typ := TypeName("BSD", fs)
		if t.whole[len(t.Partitions)] && e[12] == 0 {
			typ = "Whole disk"
		}
		t.Partitions = append(t.Partitions, Partition{
			Index: i + 1, Scheme: "BSD", Name: name,
			Start: bytesOf(start, t.SectorSize), Size: bytesOf(size, t.SectorSize),
			TypeID: fs, Type: typ,
			EntryOff: off + bsdHeader + int64(i)*16, EntryLen: 16,
		})
	}
	return t
}

// ---- Sun VTOC (the SPARC disk label) ----

const (
	vtocMagic  = 0xDABE
	vtocSanity = 0x600DDEEE
)

// The label is sector 0, big-endian: the text, the VTOC with the tags of
// the eight slices at 142, the geometry at 436, the slices (starting
// cylinder, sectors) at 444, the magic at 508, the checksum at 510
func readVTOC(d *disk, ss int) *Table {
	b := d.readFull(0, 512)
	if b == nil {
		return nil
	}
	be := binary.BigEndian
	if be.Uint16(b[508:]) != vtocMagic {
		return nil
	}
	t := &Table{Scheme: "VTOC", SectorSize: 512, whole: map[int]bool{}}
	var x uint16
	for i := 0; i < 512; i += 2 {
		x ^= be.Uint16(b[i:])
	}
	if x != 0 {
		stored := be.Uint16(b[510:])
		t.issue(Error, 510, 2, "the Sun label checksum is 0x%04X, should be 0x%04X", stored, stored^x)
	}
	hasVTOC := be.Uint32(b[128:]) == 1 && be.Uint32(b[188:]) == vtocSanity
	if !hasVTOC {
		t.issue(Warning, 188, 4, "the Sun label has no valid VTOC (version 1, sanity 0x600DDEEE): the slice tags are unknown")
	}
	heads, sects := uint64(be.Uint16(b[436:])), uint64(be.Uint16(b[438:]))
	spc := heads * sects
	for i := 0; i < 8; i++ {
		e := b[444+i*8:]
		cyl, n := uint64(be.Uint32(e)), uint64(be.Uint32(e[4:]))
		if n == 0 {
			continue
		}
		if spc == 0 {
			t.issue(Error, 436, 4, "the Sun label has no geometry (heads %d, sectors %d): the slices cannot be placed", heads, sects)
			break
		}
		var tag uint16
		id := ""
		if hasVTOC {
			tag = be.Uint16(b[142+i*4:])
			id = fmt.Sprintf("0x%02X", tag)
		}
		if tag == 5 || (!hasVTOC && i == 2) {
			t.whole[len(t.Partitions)] = true
		}
		typ := TypeName("VTOC", id)
		if !hasVTOC && i == 2 {
			typ = "Whole disk"
		}
		t.Partitions = append(t.Partitions, Partition{
			Index: i + 1, Scheme: "VTOC", Name: fmt.Sprintf("slice %d", i),
			Start: bytesOf(mulSat(cyl, spc), 512), Size: bytesOf(n, 512),
			TypeID: id, Type: typ,
			EntryOff: int64(444 + i*8), EntryLen: 8,
		})
	}
	return t
}
