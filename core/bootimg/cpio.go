package bootimg

import (
	"encoding/binary"
	"fmt"
	"strconv"
	"strings"
)

// cpio archives: newc ("070701", "070702" with a CRC), the portable odc
// ("070707", octal text) and the old binary format. The initramfs of
// Linux is often several archives one after another (the CPU microcode
// first, uncompressed, then the compressed rest), with zeros between: the
// kernel goes on after a trailer, and so does this.

const cpioTrailer = "TRAILER!!!"

// cpioEntry is a header read
type cpioEntry struct {
	name           string
	mode           uint32
	dataOff, size  int64
	next           int64 // where the next header is
	ok             bool
	format, reason string
}

func detectCpio(d *reader, head []byte) *Image {
	f := cpioFormat(head)
	if f == "" {
		return nil
	}
	// Two bytes of magic are weak: the first entry must be right too
	if strings.HasPrefix(f, "old") && !cpioRead(d, 0).ok {
		return nil
	}
	img := &Image{Format: "cpio (" + f + ")"}
	off := int64(0)
	archives := 0
	for off < d.size && len(img.Parts) < maxParts {
		h := d.bytes(off, min(110, d.size-off))
		if cpioFormat(h) == "" {
			break
		}
		archives++
		img.info(fmt.Sprintf("archive %d", archives), "%s at 0x%X", cpioFormat(h), off)
		off = cpioArchive(img, d, off)
		if off < 0 {
			return img
		}
		// Zeros pad the archives; the next starts at a multiple of 4
		off = skipZeros(d, off)
		if off >= d.size {
			break
		}
		if off%4 != 0 {
			img.info("error", "data after the trailer at 0x%X is not aligned", off)
			return img
		}
		if b := d.bytes(off, 6); b != nil && cpioFormat(b) != "" {
			continue
		}
		// What follows is a compressed archive, as the kernel takes it
		rest := d.size - off
		if c := d.compression(off, rest); c != "" {
			img.add(d, "compressed initramfs", "ramdisk", off, rest)
			img.info("compressed", "%s at 0x%X", c, off)
		} else {
			img.add(d, "data after the archive", "data", off, rest)
		}
		break
	}
	if archives > 1 {
		img.info("archives", "%d", archives)
	}
	return img
}

// cpioArchive adds the entries of one archive at off; it returns the
// offset after its trailer, -1 when the archive is broken
func cpioArchive(img *Image, d *reader, off int64) int64 {
	for len(img.Parts) < maxParts {
		e := cpioRead(d, off)
		if !e.ok {
			img.info("error", "%s at 0x%X", e.reason, off)
			return -1
		}
		if e.name == cpioTrailer {
			return e.next
		}
		kind := "data"
		switch e.mode & 0o170000 {
		case 0o100000:
			kind = "file"
		case 0o040000:
			kind = "dir"
		case 0o120000:
			kind = "symlink"
		}
		img.add(d, e.name, kind, e.dataOff, e.size)
		off = e.next
	}
	img.info("error", "more than %d entries", maxParts)
	return -1
}

// skipZeros returns the offset of the first byte that is not zero at or
// after off, d.size when there is none
func skipZeros(d *reader, off int64) int64 {
	for off < d.size {
		b := d.bytes(off, min(64<<10, d.size-off))
		if b == nil {
			return d.size
		}
		for i, c := range b {
			if c != 0 {
				return off + int64(i)
			}
		}
		off += int64(len(b))
	}
	return off
}

// cpioFormat tells the format by the magic of a header
func cpioFormat(b []byte) string {
	if len(b) >= 6 {
		switch string(b[:6]) {
		case "070701":
			return "newc"
		case "070702":
			return "newc, CRC"
		case "070707":
			return "odc"
		}
	}
	if len(b) >= 2 {
		switch {
		case b[0] == 0xC7 && b[1] == 0x71:
			return "old binary, little-endian"
		case b[0] == 0x71 && b[1] == 0xC7:
			return "old binary, big-endian"
		}
	}
	return ""
}

func cpioRead(d *reader, off int64) (e cpioEntry) {
	h := d.bytes(off, min(110, d.size-off))
	e.format = cpioFormat(h)
	var hdrSize, nameSize, align int64
	switch e.format {
	case "newc", "newc, CRC":
		if len(h) < 110 {
			e.reason = "a header is cut"
			return
		}
		f := func(i int) (int64, bool) {
			v, err := strconv.ParseUint(string(h[6+8*i:14+8*i]), 16, 32)
			return int64(v), err == nil
		}
		mode, ok1 := f(1)
		size, ok2 := f(6)
		ns, ok3 := f(11)
		if !ok1 || !ok2 || !ok3 {
			e.reason = "a header has a field that is not hex"
			return
		}
		e.mode, e.size, nameSize, hdrSize, align = uint32(mode), size, ns, 110, 4
	case "odc":
		if len(h) < 76 {
			e.reason = "a header is cut"
			return
		}
		f := func(at, n int) (int64, bool) {
			v, err := strconv.ParseUint(string(h[at:at+n]), 8, 64)
			return int64(v), err == nil
		}
		mode, ok1 := f(18, 6)
		ns, ok2 := f(59, 6)
		size, ok3 := f(65, 11)
		if !ok1 || !ok2 || !ok3 {
			e.reason = "a header has a field that is not octal"
			return
		}
		e.mode, e.size, nameSize, hdrSize, align = uint32(mode), size, ns, 76, 1
	case "old binary, little-endian", "old binary, big-endian":
		if len(h) < 26 {
			e.reason = "a header is cut"
			return
		}
		var bo binary.ByteOrder = binary.LittleEndian
		if h[0] == 0x71 {
			bo = binary.BigEndian
		}
		u := func(at int) int64 { return int64(bo.Uint16(h[at:])) }
		// The 32-bit size is two 16-bit words, the high one first
		e.mode, nameSize = uint32(u(6)), u(20)
		e.size = u(22)<<16 | u(24)
		hdrSize, align = 26, 2
	default:
		e.reason = "no cpio header"
		return
	}
	if nameSize < 1 || nameSize > 4096 {
		e.reason = "a name size is not valid"
		return
	}
	nb := d.bytes(off+hdrSize, nameSize)
	if nb == nil {
		e.reason = "a name is cut"
		return
	}
	e.name = cstr(nb)
	e.dataOff = alignUp(off+hdrSize+nameSize, align)
	if !d.in(e.dataOff, e.size) {
		// The trailer may end the file without its padding
		if e.name == cpioTrailer && e.size == 0 {
			e.ok, e.next = true, d.size
			return
		}
		e.reason = "the data of " + strconv.Quote(e.name) + " is cut"
		return
	}
	e.next = alignUp(e.dataOff+e.size, align)
	e.ok = true
	return
}
