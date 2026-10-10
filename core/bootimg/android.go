package bootimg

import (
	"fmt"
	"strings"
)

// Android boot.img and vendor_boot.img, after mkbootimg and
// system/tools/mkbootimg/include/bootimg/bootimg.h. Every section starts
// at a page boundary, in a fixed order; v3 and later have pages of 4096.

const (
	androidMagic = "ANDROID!"
	vendorMagic  = "VNDRBOOT"
)

func detectAndroid(d *reader, head []byte) *Image {
	if len(head) < 1660 || string(head[:8]) != androidMagic {
		return nil
	}
	ver := le32(head, 40)
	if ver >= 3 && ver < 16 {
		return androidV3(d, head, ver)
	}
	// v0 used the field for dt_size: a large number is not a version
	if ver > 2 {
		ver = 0
	}
	img := &Image{Format: fmt.Sprintf("Android boot (v%d)", ver)}
	page := int64(le32(head, 36))
	if page < 512 || page > 1<<17 || page&(page-1) != 0 {
		img.info("page size", "%d (not valid)", page)
		return img
	}
	img.info("header version", "%d", ver)
	img.info("page size", "%d", page)
	if s := strings.TrimSpace(cstr(head[48:64])); s != "" {
		img.info("name", "%s", s)
	}
	cmd := cstr(head[64:576]) + cstr(head[608:1632])
	img.info("cmdline", "%s", cmd)
	androidOSVersion(img, le32(head, 44))
	img.info("kernel address", "0x%08X", le32(head, 12))
	img.info("ramdisk address", "0x%08X", le32(head, 20))
	img.info("second address", "0x%08X", le32(head, 28))
	img.info("tags address", "0x%08X", le32(head, 32))
	if ver >= 2 {
		img.info("dtb address", "0x%X", le64(head, 1652))
	}
	img.info("id", "%x", head[576:608])

	off := page
	section := func(name, kind string, size int64) {
		if size > 0 && d.in(off, size) {
			img.add(d, name, kind, off, size)
		}
		off += alignUp(size, page)
	}
	section("kernel", "kernel", int64(le32(head, 8)))
	section("ramdisk", "ramdisk", int64(le32(head, 16)))
	section("second", "data", int64(le32(head, 24)))
	if ver >= 1 {
		size := int64(le32(head, 1632))
		at := int64(le64(head, 1636))
		img.info("header size", "%d", le32(head, 1644))
		if size > 0 {
			// The offset is in the header; it is where mkbootimg puts it
			if at > 0 && at < d.size {
				off = at
			}
			section("recovery_dtbo", "dtb", size)
		}
	}
	if ver >= 2 {
		section("dtb", "dtb", int64(le32(head, 1648)))
	}
	return img
}

// androidV3 is boot.img v3 and v4: no addresses, pages of 4096
func androidV3(d *reader, head []byte, ver uint32) *Image {
	img := &Image{Format: fmt.Sprintf("Android boot (v%d)", ver)}
	const page = 4096
	img.info("header version", "%d", ver)
	img.info("header size", "%d", le32(head, 20))
	img.info("cmdline", "%s", cstr(head[44:1580]))
	androidOSVersion(img, le32(head, 16))
	off := int64(page)
	section := func(name, kind string, size int64) {
		if size > 0 && d.in(off, size) {
			img.add(d, name, kind, off, size)
		}
		off += alignUp(size, page)
	}
	section("kernel", "kernel", int64(le32(head, 8)))
	section("ramdisk", "ramdisk", int64(le32(head, 12)))
	if ver >= 4 {
		section("boot signature", "signature", int64(le32(head, 1580)))
	}
	return img
}

func detectVendorBoot(d *reader, head []byte) *Image {
	if len(head) < 2128 || string(head[:8]) != vendorMagic {
		return nil
	}
	ver := le32(head, 8)
	img := &Image{Format: fmt.Sprintf("Android vendor_boot (v%d)", ver)}
	page := int64(le32(head, 12))
	img.info("header version", "%d", ver)
	img.info("page size", "%d", page)
	if page < 512 || page > 1<<17 || page&(page-1) != 0 {
		return img
	}
	if s := strings.TrimSpace(cstr(head[2080:2096])); s != "" {
		img.info("name", "%s", s)
	}
	img.info("cmdline", "%s", cstr(head[28:2076]))
	img.info("kernel address", "0x%08X", le32(head, 16))
	img.info("ramdisk address", "0x%08X", le32(head, 20))
	img.info("tags address", "0x%08X", le32(head, 2076))
	img.info("dtb address", "0x%X", le64(head, 2104))
	hdrSize := int64(le32(head, 2096))
	img.info("header size", "%d", hdrSize)

	ramdiskSize := int64(le32(head, 24))
	dtbSize := int64(le32(head, 2100))
	var tableSize, entries, entrySize, bootconfig int64
	if ver >= 4 {
		tableSize = int64(le32(head, 2112))
		entries = int64(le32(head, 2116))
		entrySize = int64(le32(head, 2120))
		bootconfig = int64(le32(head, 2124))
	}
	rdOff := alignUp(max(hdrSize, 2112), page)
	dtbOff := rdOff + alignUp(ramdiskSize, page)
	tableOff := dtbOff + alignUp(dtbSize, page)
	bcOff := tableOff + alignUp(tableSize, page)

	// The table splits the vendor ramdisk section into ramdisks; without
	// it the section is one
	split := false
	if ver >= 4 && entries > 0 && entrySize >= 108 && entries <= 4096 && d.in(tableOff, entries*entrySize) {
		for i := int64(0); i < entries; i++ {
			e := d.bytes(tableOff+i*entrySize, 108)
			if e == nil {
				break
			}
			size, at := int64(le32(e, 0)), int64(le32(e, 4))
			typ := le32(e, 8)
			label := "vendor_ramdisk: " + name([]string{"none", "platform", "recovery", "dlkm"}, int(typ))
			if n := cstr(e[12:44]); n != "" {
				label += " (" + n + ")"
			}
			if at+size <= ramdiskSize && d.in(rdOff+at, size) {
				img.add(d, label, "ramdisk", rdOff+at, size)
				split = true
			}
		}
	}
	if !split && ramdiskSize > 0 && d.in(rdOff, ramdiskSize) {
		img.add(d, "vendor_ramdisk", "ramdisk", rdOff, ramdiskSize)
	}
	if dtbSize > 0 && d.in(dtbOff, dtbSize) {
		img.add(d, "dtb", "dtb", dtbOff, dtbSize)
	}
	if tableSize > 0 && d.in(tableOff, tableSize) {
		img.add(d, "vendor ramdisk table", "data", tableOff, tableSize)
	}
	if bootconfig > 0 && d.in(bcOff, bootconfig) {
		img.add(d, "bootconfig", "config", bcOff, bootconfig)
		if b := d.bytes(bcOff, min(bootconfig, 4096)); b != nil {
			img.info("bootconfig", "%s", strings.TrimSpace(strings.ReplaceAll(cstr(b), "\n", "; ")))
		}
	}
	return img
}

// androidOSVersion decodes os_version: A.B.C in the high 21 bits, the
// year and month of the security patch in the low 11
func androidOSVersion(img *Image, v uint32) {
	if v == 0 {
		return
	}
	os := v >> 11
	img.info("OS version", "%d.%d.%d", os>>14, os>>7&0x7F, os&0x7F)
	pl := v & 0x7FF
	img.info("OS patch level", "%04d-%02d", 2000+pl>>4, pl&0xF)
}
