// Package bootimgtest makes small boot images for the tests of bootimg and
// of the templates: built by hand after the specifications, so the tests
// do not need mkbootimg, mkimage or xorriso.
package bootimgtest

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"strconv"
)

var (
	le = binary.LittleEndian
	be = binary.BigEndian
)

func pad(b []byte, a int) []byte {
	for len(b)%a != 0 {
		b = append(b, 0)
	}
	return b
}

// Android is the content of an Android boot image
type Android struct {
	Version      int // 0-4
	PageSize     int // v0-v2; v3, v4 use 4096
	Cmdline      string
	OSVersion    uint32
	Kernel       []byte
	Ramdisk      []byte
	Second       []byte // v0-v2
	RecoveryDTBO []byte // v1, v2
	DTB          []byte // v2
	Signature    []byte // v4
}

// AndroidOS encodes os_version: A.B.C and the patch year-month
func AndroidOS(a, b, c, year, month uint32) uint32 {
	return (a<<14|b<<7|c)<<11 | (year-2000)<<4 | month
}

// AndroidBoot makes boot.img; the sections in the order of mkbootimg
func AndroidBoot(a Android) []byte {
	page := a.PageSize
	if a.Version >= 3 {
		page = 4096
	}
	h := make([]byte, page)
	copy(h, "ANDROID!")
	var out []byte
	if a.Version >= 3 {
		le.PutUint32(h[8:], uint32(len(a.Kernel)))
		le.PutUint32(h[12:], uint32(len(a.Ramdisk)))
		le.PutUint32(h[16:], a.OSVersion)
		size := 1580
		if a.Version >= 4 {
			size = 1584
			le.PutUint32(h[1580:], uint32(len(a.Signature)))
		}
		le.PutUint32(h[20:], uint32(size))
		le.PutUint32(h[40:], uint32(a.Version))
		copy(h[44:1580], a.Cmdline)
		out = append(out, h...)
		out = append(out, pad(append([]byte(nil), a.Kernel...), page)...)
		out = append(out, pad(append([]byte(nil), a.Ramdisk...), page)...)
		if a.Version >= 4 {
			out = append(out, pad(append([]byte(nil), a.Signature...), page)...)
		}
		return out
	}
	le.PutUint32(h[8:], uint32(len(a.Kernel)))
	le.PutUint32(h[12:], 0x10008000)
	le.PutUint32(h[16:], uint32(len(a.Ramdisk)))
	le.PutUint32(h[20:], 0x11000000)
	le.PutUint32(h[24:], uint32(len(a.Second)))
	le.PutUint32(h[28:], 0x10F00000)
	le.PutUint32(h[32:], 0x10000100)
	le.PutUint32(h[36:], uint32(page))
	le.PutUint32(h[40:], uint32(a.Version))
	le.PutUint32(h[44:], a.OSVersion)
	copy(h[48:64], "sample")
	copy(h[64:576], a.Cmdline)
	off := page
	for _, s := range [][]byte{a.Kernel, a.Ramdisk, a.Second} {
		off += (len(s) + page - 1) / page * page
	}
	if a.Version >= 1 {
		le.PutUint32(h[1632:], uint32(len(a.RecoveryDTBO)))
		if len(a.RecoveryDTBO) > 0 {
			le.PutUint64(h[1636:], uint64(off))
		}
		size := 1648
		if a.Version == 2 {
			size = 1660
			le.PutUint32(h[1648:], uint32(len(a.DTB)))
			le.PutUint64(h[1652:], 0x11F00000)
		}
		le.PutUint32(h[1644:], uint32(size))
	}
	out = append(out, h...)
	for _, s := range [][]byte{a.Kernel, a.Ramdisk, a.Second, a.RecoveryDTBO, a.DTB} {
		out = append(out, pad(append([]byte(nil), s...), page)...)
	}
	return out
}

// VendorRamdisk is an entry of the vendor ramdisk table (v4)
type VendorRamdisk struct {
	Name string
	Type uint32 // 1 platform, 2 recovery, 3 dlkm
	Data []byte
}

// VendorBootV4 makes vendor_boot.img v4
func VendorBootV4(page int, cmdline string, ramdisks []VendorRamdisk, dtb, bootconfig []byte) []byte {
	h := make([]byte, (2128+page-1)/page*page)
	copy(h, "VNDRBOOT")
	le.PutUint32(h[8:], 4)
	le.PutUint32(h[12:], uint32(page))
	le.PutUint32(h[16:], 0x10008000)
	le.PutUint32(h[20:], 0x11000000)
	var rd, table []byte
	for _, r := range ramdisks {
		e := make([]byte, 108)
		le.PutUint32(e[0:], uint32(len(r.Data)))
		le.PutUint32(e[4:], uint32(len(rd)))
		le.PutUint32(e[8:], r.Type)
		copy(e[12:44], r.Name)
		table = append(table, e...)
		rd = append(rd, r.Data...)
	}
	le.PutUint32(h[24:], uint32(len(rd)))
	copy(h[28:2076], cmdline)
	le.PutUint32(h[2076:], 0x10000100)
	copy(h[2080:2096], "vendor")
	le.PutUint32(h[2096:], 2128)
	le.PutUint32(h[2100:], uint32(len(dtb)))
	le.PutUint64(h[2104:], 0x11F00000)
	le.PutUint32(h[2112:], uint32(len(table)))
	le.PutUint32(h[2116:], uint32(len(ramdisks)))
	le.PutUint32(h[2120:], 108)
	le.PutUint32(h[2124:], uint32(len(bootconfig)))
	out := append([]byte(nil), h...)
	for _, s := range [][]byte{rd, dtb, table, bootconfig} {
		out = append(out, pad(append([]byte(nil), s...), page)...)
	}
	return out
}

// UImage makes a legacy U-Boot image with right CRCs. os 5 Linux, arch 2
// ARM, typ 2 kernel / 3 ramdisk / 4 multi-file, comp 0 none / 1 gzip.
func UImage(name string, typ, comp byte, data []byte) []byte {
	h := make([]byte, 64)
	be.PutUint32(h[0:], 0x27051956)
	be.PutUint32(h[8:], 1700000000)
	be.PutUint32(h[12:], uint32(len(data)))
	be.PutUint32(h[16:], 0x80008000)
	be.PutUint32(h[20:], 0x80008040)
	be.PutUint32(h[24:], crc32.ChecksumIEEE(data))
	h[28], h[29], h[30], h[31] = 5, 2, typ, comp
	copy(h[32:], name)
	be.PutUint32(h[4:], crc32.ChecksumIEEE(h))
	return append(h, data...)
}

// UImageMulti makes a multi-file image: the size table and the files
func UImageMulti(name string, files ...[]byte) []byte {
	var data []byte
	for _, f := range files {
		data = be.AppendUint32(data, uint32(len(f)))
	}
	data = be.AppendUint32(data, 0)
	for _, f := range files {
		data = pad(append(data, f...), 4)
	}
	return UImage(name, 4, 0, data)
}

// Node is a node of a device tree
type Node struct {
	Name     string
	Props    []Prop
	Children []*Node
}

// Prop is a property of a node
type Prop struct {
	Name  string
	Value []byte
}

// Str is a string property value
func Str(s ...string) []byte {
	var b []byte
	for _, x := range s {
		b = append(append(b, x...), 0)
	}
	return b
}

// U32 is a property value of one cell
func U32(v uint32) []byte { return be.AppendUint32(nil, v) }

// FDT makes a flattened device tree (version 17); extra is appended after
// it, aligned to 4: the external data of a FIT
func FDT(root *Node, extra []byte) []byte {
	var st, strs []byte
	nameOff := map[string]int{}
	var walk func(n *Node)
	walk = func(n *Node) {
		st = be.AppendUint32(st, 1)
		st = pad(append(append(st, n.Name...), 0), 4)
		for _, p := range n.Props {
			o, ok := nameOff[p.Name]
			if !ok {
				o = len(strs)
				nameOff[p.Name] = o
				strs = append(append(strs, p.Name...), 0)
			}
			st = be.AppendUint32(st, 3)
			st = be.AppendUint32(st, uint32(len(p.Value)))
			st = be.AppendUint32(st, uint32(o))
			st = pad(append(st, p.Value...), 4)
		}
		for _, c := range n.Children {
			walk(c)
		}
		st = be.AppendUint32(st, 2)
	}
	walk(root)
	st = be.AppendUint32(st, 9)
	const hdr = 40
	rsv := hdr                // the memory reservation map: one empty entry
	stOff := rsv + 16         // the structure block
	strOff := stOff + len(st) // the strings
	total := strOff + len(strs)
	h := make([]byte, hdr+16)
	be.PutUint32(h[0:], 0xD00DFEED)
	be.PutUint32(h[4:], uint32(total))
	be.PutUint32(h[8:], uint32(stOff))
	be.PutUint32(h[12:], uint32(strOff))
	be.PutUint32(h[16:], uint32(rsv))
	be.PutUint32(h[20:], 17)
	be.PutUint32(h[24:], 16)
	be.PutUint32(h[32:], uint32(len(strs)))
	be.PutUint32(h[36:], uint32(len(st)))
	out := append(append(h, st...), strs...)
	if len(extra) > 0 {
		out = append(pad(out, 4), extra...)
	}
	return out
}

// CpioEntry is a file of a cpio archive
type CpioEntry struct {
	Name string
	Mode uint32 // with the type: 0o040755 a dir, 0o100644 a file, 0o120777 a symlink
	Data []byte // a symlink: its target
}

// Cpio makes a newc archive with its trailer, padded to 4
func Cpio(entries []CpioEntry) []byte {
	var out []byte
	hex := func(v int) string {
		s := strconv.FormatUint(uint64(v), 16)
		return string(bytes.Repeat([]byte("0"), 8-len(s))) + s
	}
	put := func(e CpioEntry, ino int) {
		out = append(out, "070701"...)
		for _, v := range []int{ino, int(e.Mode), 0, 0, 1, 1700000000, len(e.Data), 0, 0, 0, 0, len(e.Name) + 1, 0} {
			out = append(out, hex(v)...)
		}
		out = pad(append(append(out, e.Name...), 0), 4)
		out = pad(append(out, e.Data...), 4)
	}
	for i, e := range entries {
		put(e, i+1)
	}
	put(CpioEntry{Name: "TRAILER!!!"}, 0)
	return out
}

// BzImage makes the setup of a bzImage (protocol 2.15) and the protected
// mode code: 0x2CC bytes of code and the payload after it
func BzImage(setupSects int, version string, payload []byte) []byte {
	b := make([]byte, (setupSects+1)*512)
	b[0x1F1] = byte(setupSects)
	syssize := 0x2CC + len(payload)
	le.PutUint32(b[0x1F4:], uint32((syssize+15)/16))
	le.PutUint16(b[0x1FE:], 0xAA55)
	b[0x200], b[0x201] = 0xEB, 0x66
	copy(b[0x202:], "HdrS")
	le.PutUint16(b[0x206:], 0x020F)
	le.PutUint16(b[0x20E:], 0x400) // the version text at 0x600
	copy(b[0x600:], version+"\x00")
	b[0x211] = 1
	le.PutUint32(b[0x214:], 0x100000)
	le.PutUint32(b[0x22C:], 0x7FFFFFFF)
	le.PutUint32(b[0x230:], 0x200000)
	b[0x234] = 1
	le.PutUint32(b[0x238:], 2047)
	le.PutUint32(b[0x248:], 0x2CC)
	le.PutUint32(b[0x24C:], uint32(len(payload)))
	pm := make([]byte, 0x2CC)
	return append(append(b, pm...), payload...)
}

// BootEntry is a boot image of an El Torito catalog
type BootEntry struct {
	Platform byte // 0 x86, 0xEF UEFI
	Sectors  uint16
	Data     []byte // at a sector of its own
}

// ElTorito makes an ISO 9660 image (only its descriptors) with a boot
// catalog: the first entry is the default one, the rest are in a section
// each
func ElTorito(entries []BootEntry) []byte {
	const sec = 2048
	img := make([]byte, 20*sec)
	pvd := img[16*sec:]
	pvd[0] = 1
	copy(pvd[1:], "CD001")
	pvd[6] = 1
	copy(pvd[40:72], "SAMPLE                          ")
	br := img[17*sec:]
	copy(br[1:], "CD001")
	br[6] = 1
	copy(br[7:], "EL TORITO SPECIFICATION")
	le.PutUint32(br[0x47:], 19)
	term := img[18*sec:]
	term[0] = 255
	copy(term[1:], "CD001")
	term[6] = 1

	cat := img[19*sec:]
	cat[0] = 1
	cat[1] = entries[0].Platform
	copy(cat[4:], "SAMPLE")
	cat[30], cat[31] = 0x55, 0xAA
	var sum uint16
	for i := 0; i < 32; i += 2 {
		sum += le.Uint16(cat[i:])
	}
	le.PutUint16(cat[28:], -sum)

	lba := 20
	entry := func(e []byte, b BootEntry) {
		e[0] = 0x88
		le.PutUint16(e[6:], b.Sectors)
		le.PutUint32(e[8:], uint32(lba))
		lba += (len(b.Data) + sec - 1) / sec
	}
	entry(cat[32:], entries[0])
	off := 64
	for i, b := range entries[1:] {
		h := cat[off:]
		h[0] = 0x90
		if i == len(entries)-2 {
			h[0] = 0x91
		}
		h[1] = b.Platform
		le.PutUint16(h[2:], 1)
		entry(cat[off+32:], b)
		off += 64
	}
	for _, b := range entries {
		img = append(img, b.Data...)
		img = pad(img, sec)
	}
	return img
}

// FAT12 makes the boot sector of a FAT12 file system of n sectors of 512
// bytes, padded to its size: an EFI boot image
func FAT12(n int) []byte {
	b := make([]byte, n*512)
	b[0], b[1], b[2] = 0xEB, 0x3C, 0x90
	copy(b[3:], "mkfs.fat")
	le.PutUint16(b[0x0B:], 512)
	b[0x0D] = 4
	le.PutUint16(b[0x0E:], 1)
	b[0x10] = 2
	le.PutUint16(b[0x11:], 512)
	le.PutUint16(b[0x13:], uint16(n))
	b[0x15] = 0xF8
	copy(b[0x36:], "FAT12   ")
	b[510], b[511] = 0x55, 0xAA
	return b
}

// GrubBoot makes GRUB boot.img: the kernel sector at 0x5C, the boot drive
// at 0x64, the messages and 55 AA
func GrubBoot(kernelSector uint64, drive byte) []byte {
	b := make([]byte, 512)
	b[0], b[1] = 0xEB, 0x63
	le.PutUint64(b[0x5C:], kernelSector)
	b[0x64] = drive
	copy(b[0x180:], "GRUB \x00Geom\x00Hard Disk\x00Read\x00 Error\r\n\x00")
	b[510], b[511] = 0x55, 0xAA
	return b
}

// GrubCore makes the start of core.img: diskboot.img with one blocklist,
// then rest
func GrubCore(start uint64, sectors, segment uint16, rest []byte) []byte {
	b := make([]byte, 512)
	b[0] = 0x52
	copy(b[0x100:], "loading\x00.\x00\r\n\x00Geom\x00Read\x00 Error\x00")
	le.PutUint64(b[0x1F4:], start)
	le.PutUint16(b[0x1FC:], sectors)
	le.PutUint16(b[0x1FE:], segment)
	return append(b, rest...)
}

// NTFSBoot makes an NTFS boot sector of Windows: the BPB, code, 55 AA and
// the next sectors with the name of BOOTMGR in UTF-16
func NTFSBoot() []byte {
	b := make([]byte, 16*512)
	b[0], b[1], b[2] = 0xEB, 0x52, 0x90
	copy(b[3:], "NTFS    ")
	le.PutUint16(b[0x0B:], 512)
	b[0x0D] = 8
	b[0x15] = 0xF8
	le.PutUint64(b[0x28:], 1<<20)
	le.PutUint64(b[0x30:], 4)
	le.PutUint64(b[0x48:], 0x1234ABCD5678EF90)
	copy(b[0x54:], []byte{0xFA, 0x33, 0xC0})
	b[510], b[511] = 0x55, 0xAA
	copy(b[0x600:], "B\x00O\x00O\x00T\x00M\x00G\x00R\x00")
	return b
}

// P is a property
func P(name string, value []byte) Prop { return Prop{Name: name, Value: value} }
