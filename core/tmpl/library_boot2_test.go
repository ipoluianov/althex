package tmpl

import (
	"bytes"
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	bt "github.com/ipoluianov/althex/core/bootimg/bootimgtest"
)

// More tests of the boot templates: syslinux.bt, arm-zimage.bt, the
// Android boot image v1 and v3 and vendor_boot v3, FAT16 (fat.bt) and the
// "modern" MBR with the disk timestamp (mbr.bt)

const ldlinuxMagic = 0x3EB202FE

func b2Comment(t *testing.T, res *Result, path, prefix string) {
	t.Helper()
	n := res.Root.Find(path)
	if n == nil {
		t.Fatalf("no field %s", path)
	}
	if !strings.HasPrefix(n.Comment, prefix) {
		t.Errorf("%s comment %q, want %q...", path, n.Comment, prefix)
	}
}

func b2None(t *testing.T, res *Result, paths ...string) {
	t.Helper()
	for _, p := range paths {
		if res.Root.Find(p) != nil {
			t.Errorf("unexpected field %s", p)
		}
	}
}

// ---- SYSLINUX ----

// syslinuxBootSector is ldlinux.bss as the installer leaves it: the BPB
// of FAT32 shape, LDLINUX_MAGIC at 0x1F8, the link and 55 AA
func syslinuxBootSector() []byte {
	b := make([]byte, 512)
	copy(b, []byte{0xEB, 0x58, 0x90})
	copy(b[3:], "SYSLINUX")
	for i := 0x5A; i < 0x1F8; i++ {
		b[i] = 0x90
	}
	binary.LittleEndian.PutUint32(b[0x1F8:], ldlinuxMagic)
	binary.LittleEndian.PutUint16(b[0x1FC:], 0x7C00+0x150)
	b[510], b[511] = 0x55, 0xAA
	return b
}

// ldlinuxSys is ldlinux.sys of 4 sectors with the patch area filled in;
// the extended patch area says that the boot sector has the LBA of sector
// 1 at 0x60 and 0x66 and the RAID patch at 0x70
func ldlinuxSys() []byte {
	le := binary.LittleEndian
	b := make([]byte, 4*512)
	copy(b, "\r\nSYSLINUX 6.04 \x00\r\n\x1a")
	pa := b[0x18:]
	le.PutUint32(pa[0:], ldlinuxMagic)
	le.PutUint32(pa[4:], ldlinuxMagic^0x5E0A1B2C)
	le.PutUint16(pa[8:], 2)
	le.PutUint16(pa[10:], 2)
	le.PutUint32(pa[12:], uint32(len(b)/4))
	le.PutUint16(pa[20:], 127)
	le.PutUint16(pa[22:], 0x30)
	epa := b[0x30:]
	for i, v := range []uint16{0x100, 0x110, 64, 0x150, 32, 0x180, 8, 0x60, 0x66, 0x70} {
		le.PutUint16(epa[2*i:], v)
	}
	le.PutUint64(b[0x100:], 1000)
	le.PutUint64(b[0x108:], 1001)
	copy(b[0x110:], "/boot/syslinux/")
	le.PutUint64(b[0x180:], 100)
	le.PutUint16(b[0x188:], 1)
	le.PutUint64(b[0x18A:], 200)
	le.PutUint16(b[0x192:], 2)
	for i := 0x200; i < len(b); i++ {
		b[i] = byte(i * 7)
	}
	// The checksum: all the dwords sum to LDLINUX_MAGIC
	var s uint32
	for i := 0; i < len(b); i += 4 {
		s += le.Uint32(b[i:])
	}
	le.PutUint32(pa[16:], ldlinuxMagic-s)
	return b
}

func TestBootSyslinuxTemplate(t *testing.T) {
	bs := syslinuxBootSector()
	binary.LittleEndian.PutUint32(bs[0x60:], 99)
	binary.LittleEndian.PutUint32(bs[0x66:], 0)
	bs[0x70], bs[0x71] = 0xCD, 0x18
	sys := ldlinuxSys()
	data := append(append([]byte(nil), bs...), sys...) // ldlinux.bin
	res := libraryRun(t, data, "ldlinux.bin", "syslinux.bt")
	bootWant(t, res, "boot_sector.oem_name", `"SYSLINUX"`)
	bootWant(t, res, "boot_sector.bs_magic", "0x3EB202FE")
	b2Comment(t, res, "boot_sector.bs_magic", "LDLINUX_MAGIC")
	bootAt(t, res, "boot_sector.bs_magic", 0x1F8, 4)
	bootAt(t, res, "boot_sector.signature", 0x1FE, 2)
	if res.Root.Find("boot_sector.ebpb32") == nil {
		t.Error("no FAT32-shaped EBPB")
	}
	bootAt(t, res, "banner", 512, 0x18)
	bootAt(t, res, "patch_area", 512+0x18, 24)
	bootWant(t, res, "patch_area.magic", "0x3EB202FE")
	bootWant(t, res, "patch_area.data_sectors", "2")
	bootWant(t, res, "patch_area.adv_sectors", "2")
	bootWant(t, res, "patch_area.dwords", "512")
	bootWant(t, res, "patch_area.maxtransfer", "127")
	b2Comment(t, res, "patch_area.checksum", "OK")
	b2Comment(t, res, "patch_area.instance", "LDLINUX_MAGIC ^ 0x5E0A1B2C")
	bootAt(t, res, "ext_patch_area", 512+0x30, 20)
	bootWant(t, res, "ext_patch_area.secptrcnt", "8")
	bootWant(t, res, "adv_lba[1]", "1001")
	bootWant(t, res, "current_dir", `"/boot/syslinux/"`)
	bootAt(t, res, "subvolume", 512+0x150, 32)
	bootWant(t, res, "sectors[0]", "LBA 100, 1 sectors")
	bootWant(t, res, "sectors[1]", "LBA 200, 2 sectors")
	bootAt(t, res, "sectors[1]", 512+0x18A, 10)
	b2None(t, res, "sectors[2]")
	bootWant(t, res, "sect1_lba_low", "99")
	bootAt(t, res, "sect1_lba_low", 0x60, 4)
	bootWant(t, res, "sect1_lba_high", "0")
	bootWant(t, res, "raid_patch", "0x18CD")
	bootCuts(t, "syslinux.bt", data)

	// A changed byte breaks the checksum
	data[512+0x300] ^= 0xFF
	res = libraryRun(t, data, "ldlinux.bin", "syslinux.bt")
	b2Comment(t, res, "patch_area.checksum", "bad")

	// ldlinux.sys alone: by its banner
	res = libraryRun(t, sys, "ldlinux.sys", "syslinux.bt")
	b2None(t, res, "boot_sector", "sect1_lba_low", "raid_patch")
	bootAt(t, res, "banner", 0, 0x18)
	bootAt(t, res, "patch_area", 0x18, 24)
	b2Comment(t, res, "patch_area.checksum", "OK")
	bootWant(t, res, "sectors[0].lba", "100")
	bootCuts(t, "syslinux.bt", sys)

	// Not installed yet: no checksum
	fresh := ldlinuxSys()
	binary.LittleEndian.PutUint32(fresh[0x18+12:], 0)
	res = libraryRun(t, fresh, "ldlinux.sys", "syslinux.bt")
	b2Comment(t, res, "patch_area.checksum", "not set")

	// A volume: the boot sector at 0, ldlinux.sys at sector 40
	vol := make([]byte, 48*512)
	copy(vol, bs)
	copy(vol[40*512:], sys)
	res = libraryRun(t, vol, "part.img", "syslinux.bt")
	bootAt(t, res, "patch_area", 40*512+0x18, 24)
	b2Comment(t, res, "patch_area.checksum", "OK")
	bootAt(t, res, "current_dir", 40*512+0x110, 64)
	bootWant(t, res, "raid_patch", "0x18CD")

	// The boot sector alone (ldlinux.bss)
	res = libraryRun(t, syslinuxBootSector(), "ldlinux.bss", "syslinux.bt")
	b2None(t, res, "patch_area")
	if len(res.Warnings) != 0 {
		t.Errorf("warnings %v", res.Warnings)
	}
	bootCuts(t, "syslinux.bt", syslinuxBootSector())
}

// isolinuxBin is isolinux.bin with the boot info table filled in by
// -boot-info-table, or the defaults of the build (0xDEADBEEF)
func isolinuxBin(filled bool) []byte {
	le := binary.LittleEndian
	b := make([]byte, 4096)
	copy(b, []byte{0xFA, 0xEA, 0x40, 0x7C, 0x00, 0x00, 0x90, 0x90})
	for i := 64; i < len(b); i++ {
		b[i] = byte(i*13 + 5)
	}
	le.PutUint32(b[8:], 16)
	for i := 16; i < 64; i += 4 {
		le.PutUint32(b[i:], 0xDEADBEEF)
	}
	if filled {
		le.PutUint32(b[12:], 30)
		le.PutUint32(b[16:], uint32(len(b)))
		var s uint32
		for i := 64; i < len(b); i += 4 {
			s += le.Uint32(b[i:])
		}
		le.PutUint32(b[20:], s)
	}
	return b
}

func TestBootIsolinuxTemplate(t *testing.T) {
	data := isolinuxBin(true)
	res := libraryRun(t, data, "isolinux.bin", "syslinux.bt")
	bootAt(t, res, "jump", 0, 8)
	bootAt(t, res, "boot_info_table", 8, 56)
	bootWant(t, res, "boot_info_table.bi_pvd", "16")
	bootWant(t, res, "boot_info_table.bi_file", "30")
	bootWant(t, res, "boot_info_table.bi_length", "4096")
	b2Comment(t, res, "boot_info_table.bi_csum", "OK")
	bootAt(t, res, "code", 64, 4096-64)
	b2None(t, res, "patch_area", "boot_sector")
	bootCuts(t, "syslinux.bt", data)

	data[100] ^= 1
	res = libraryRun(t, data, "isolinux.bin", "syslinux.bt")
	b2Comment(t, res, "boot_info_table.bi_csum", "bad")

	res = libraryRun(t, isolinuxBin(false), "isolinux.bin", "syslinux.bt")
	b2Comment(t, res, "boot_info_table.bi_csum", "not filled in")
}

// ---- ARM zImage ----

// armZImage makes a zImage of 0x1000 bytes: the header, a gzip payload at
// 0x200 that ends with the size word at 0xEFC, the KLSZ table at 0xF00;
// then dtb is appended
func armZImage(efi bool, dtb []byte) []byte {
	le := binary.LittleEndian
	b := make([]byte, 0x1000)
	for i := 0; i < 8; i++ {
		le.PutUint32(b[4*i:], 0xE1A00000)
	}
	if efi {
		copy(b, "MZ")
	}
	le.PutUint32(b[0x20:], 0xEA00000F)
	le.PutUint32(b[0x24:], 0x016F2818)
	le.PutUint32(b[0x28:], 0)
	le.PutUint32(b[0x2C:], 0x1000)
	le.PutUint32(b[0x30:], 0x04030201)
	le.PutUint32(b[0x34:], 0x45454545)
	le.PutUint32(b[0x38:], 0xF00)
	gzip := bootGz(bytes.Repeat([]byte("vmlinux "), 100))
	copy(b[0x200:], gzip)
	le.PutUint32(b[0xEFC:], 123456)
	for i, v := range []uint32{6, 0x5A534C4B, 0xEFC, 0x20000, 0x8000, 0x10000, 0} {
		le.PutUint32(b[0xF00+4*i:], v)
	}
	return append(b, dtb...)
}

func TestBootARMZImageTemplate(t *testing.T) {
	dtb := bt.FDT(&bt.Node{Props: []bt.Prop{bt.P("model", bt.Str("test board"))}}, nil)
	data := armZImage(false, dtb)
	res := libraryRun(t, data, "zImage", "arm-zimage.bt")
	bootWant(t, res, "header.magic", "0x016F2818")
	bootAt(t, res, "header.magic", 0x24, 4)
	bootWant(t, res, "header.start", "0x00000000")
	bootWant(t, res, "header.end", "0x00001000")
	b2Comment(t, res, "header.end", "the image has 4096 bytes")
	b2Comment(t, res, "header.endian", "little-endian")
	bootWant(t, res, "header.table_offset", "0x00000F00")
	bootAt(t, res, "tags", 0xF00, 24)
	bootWant(t, res, "tags", "KLSZ, 6 words")
	bootWant(t, res, "tags.size_ptr", "0x00000EFC")
	bootWant(t, res, "tags.bss_size", "131072")
	bootWant(t, res, "tags.malloc_size", "65536")
	bootAt(t, res, "table_end", 0xF18, 4)
	bootWant(t, res, "decompressed_size", "123456")
	bootAt(t, res, "compressed_kernel", 0x200, 0xF00-0x200)
	bootAt(t, res, "appended_dtb", 0x1000, 40)
	bootWant(t, res, "appended_dtb.magic", "0xD00DFEED")
	bootWant(t, res, "appended_dtb.totalsize", strconv.Itoa(len(dtb)))
	bootWant(t, res, "appended_dtb.version", "17")
	bootAt(t, res, "appended_dtb_data", 0x1040-0x18, int64(len(dtb)-40))
	b2None(t, res, "after_dtb", "after_image")
	bootCuts(t, "arm-zimage.bt", data)

	// With the EFI stub ("MZ") the zImage template wins over PE
	res = libraryRun(t, armZImage(true, nil), "zImage.efi", "arm-zimage.bt")
	b2None(t, res, "appended_dtb", "after_image")

	// An old kernel: no endianness flag, no table; data after the image
	old := armZImage(false, []byte("trailing"))
	binary.LittleEndian.PutUint32(old[0x30:], 0)
	binary.LittleEndian.PutUint32(old[0x34:], 0)
	res = libraryRun(t, old, "zImage", "arm-zimage.bt")
	b2Comment(t, res, "header.endian", "no flag")
	b2None(t, res, "header.table_offset", "tags", "decompressed_size", "appended_dtb")
	bootAt(t, res, "after_image", 0x1000, 8)

	// A big-endian (BE8) kernel: the flag in its order
	be := armZImage(false, nil)
	binary.BigEndian.PutUint32(be[0x30:], 0x04030201)
	res = libraryRun(t, be, "zImage", "arm-zimage.bt")
	b2Comment(t, res, "header.endian", "big-endian")
}

// ---- Android boot v1, v3, vendor_boot v3 ----

func TestBootAndroidV1Template(t *testing.T) {
	kernel := make([]byte, 3000)
	data := bt.AndroidBoot(bt.Android{Version: 1, PageSize: 2048, Cmdline: "console=ttyMSM0",
		OSVersion: bt.AndroidOS(10, 0, 0, 2020, 6), Kernel: kernel, Ramdisk: make([]byte, 100),
		RecoveryDTBO: make([]byte, 500)})
	res := libraryRun(t, data, "boot.img", "android-boot.bt")
	bootWant(t, res, "header.header_version", "1")
	bootWant(t, res, "header.page_size", "2048")
	bootWant(t, res, "header.header_size", "1648")
	bootWant(t, res, "header.recovery_dtbo_size", "500")
	bootWant(t, res, "header.recovery_dtbo_offset", "0x0000000000002000")
	bootWant(t, res, "header.os_version", "10.0.0, patch 2020-06")
	bootWant(t, res, "header.cmdline", `"console=ttyMSM0"`)
	bootAt(t, res, "header", 0, 1648)
	b2None(t, res, "header.dtb_size", "header.dtb_addr", "dtb", "second")
	bootAt(t, res, "kernel", 2048, 3000)
	bootAt(t, res, "ramdisk", 3*2048, 100)
	bootAt(t, res, "recovery_dtbo", 4*2048, 500)
	bootCuts(t, "android-boot.bt", data)
}

func TestBootAndroidV3Template(t *testing.T) {
	data := bt.AndroidBoot(bt.Android{Version: 3, Cmdline: "androidboot.v=3",
		OSVersion: bt.AndroidOS(12, 1, 0, 2022, 1), Kernel: make([]byte, 5000), Ramdisk: []byte("ramdisk3")})
	res := libraryRun(t, data, "boot.img", "android-boot.bt")
	bootWant(t, res, "header.header_version", "3")
	bootWant(t, res, "header.header_size", "1580")
	bootWant(t, res, "header.cmdline", `"androidboot.v=3"`)
	bootWant(t, res, "header.os_version", "12.1.0, patch 2022-01")
	bootAt(t, res, "header", 0, 1580)
	b2None(t, res, "header.signature_size", "header.page_size", "boot_signature", "second")
	bootAt(t, res, "kernel", 4096, 5000)
	bootAt(t, res, "ramdisk", 3*4096, 8)
	bootCuts(t, "android-boot.bt", data)
}

// vendorBootV3 makes vendor_boot.img v3: the header of 2112 bytes, the
// vendor ramdisk, the DTB, each from a page boundary
func tmplVendorBootV3(page int, cmdline string, ramdisk, dtb []byte) []byte {
	le := binary.LittleEndian
	h := make([]byte, (2112+page-1)/page*page)
	copy(h, "VNDRBOOT")
	le.PutUint32(h[8:], 3)
	le.PutUint32(h[12:], uint32(page))
	le.PutUint32(h[16:], 0x10008000)
	le.PutUint32(h[20:], 0x11000000)
	le.PutUint32(h[24:], uint32(len(ramdisk)))
	copy(h[28:2076], cmdline)
	le.PutUint32(h[2076:], 0x10000100)
	copy(h[2080:2096], "vendor3")
	le.PutUint32(h[2096:], 2112)
	le.PutUint32(h[2100:], uint32(len(dtb)))
	le.PutUint64(h[2104:], 0x11F00000)
	out := append([]byte(nil), h...)
	for _, s := range [][]byte{ramdisk, dtb} {
		out = append(out, s...)
		for len(out)%page != 0 {
			out = append(out, 0)
		}
	}
	return out
}

func TestBootVendorV3Template(t *testing.T) {
	dtb := []byte{0xD0, 0x0D, 0xFE, 0xED, 0, 0, 0, 0x40}
	data := tmplVendorBootV3(2048, "vcmd3", make([]byte, 3000), dtb)
	res := libraryRun(t, data, "vendor_boot.img", "android-vendor-boot.bt")
	bootWant(t, res, "header.header_version", "3")
	bootWant(t, res, "header.page_size", "2048")
	bootWant(t, res, "header.header_size", "2112")
	bootWant(t, res, "header.cmdline", `"vcmd3"`)
	bootWant(t, res, "header.name", `"vendor3"`)
	bootWant(t, res, "header.dtb_size", "8")
	bootAt(t, res, "header", 0, 2112)
	b2None(t, res, "header.vendor_ramdisk_table_size", "header.bootconfig_size", "vendor_ramdisk_table", "bootconfig")
	bootAt(t, res, "vendor_ramdisks", 4096, 3000)
	bootAt(t, res, "dtb", 4096+4096, 8)
	bootCuts(t, "android-vendor-boot.bt", data)
}

// ---- FAT16 ----

// makeFAT16 makes the start of a FAT16 volume of 20000 sectors, clusters
// of 4 sectors (4981 clusters: FAT16 by the count): the boot sector, two
// FATs of 20 sectors, the fixed root of 512 entries, cluster 2
func makeFAT16() []byte {
	le := binary.LittleEndian
	const rsvd, fatSecs, rootSecs = 4, 20, 32
	b := make([]byte, (rsvd+2*fatSecs+rootSecs+4)*512)
	copy(b, []byte{0xEB, 0x3C, 0x90})
	copy(b[3:], "MSDOS5.0")
	le.PutUint16(b[11:], 512)
	b[13] = 4
	le.PutUint16(b[14:], rsvd)
	b[16] = 2
	le.PutUint16(b[17:], 512)
	le.PutUint16(b[19:], 20000)
	b[21] = 0xF8
	le.PutUint16(b[22:], fatSecs)
	le.PutUint16(b[24:], 32)
	le.PutUint16(b[26:], 64)
	b[36] = 0x80
	b[38] = 0x29
	le.PutUint32(b[39:], 0x1234ABCD)
	copy(b[43:], "SIXTEEN    FAT16   ")
	b[510], b[511] = 0x55, 0xAA
	for _, f := range []int{rsvd, rsvd + fatSecs} {
		fat := b[f*512:]
		le.PutUint16(fat, 0xFFF8)
		le.PutUint16(fat[2:], 0xFFFF)
		le.PutUint16(fat[4:], 0xFFFF) // cluster 2: the end
	}
	root := b[(rsvd+2*fatSecs)*512:]
	copy(root, "SIXTEEN    ")
	root[11] = 0x08
	d := root[32:64]
	copy(d, "README  TXT")
	d[11] = 0x20
	le.PutUint16(d[26:], 2)
	le.PutUint32(d[28:], 6)
	copy(b[(rsvd+2*fatSecs+rootSecs)*512:], "readme")
	return b
}

func TestFAT16Template(t *testing.T) {
	data := makeFAT16()
	res := libraryRun(t, data, "disk.img", "fat.bt")
	if len(res.Output) == 0 || !strings.HasPrefix(res.Output[0], "FAT16: 4981 clusters") {
		t.Errorf("output %q", res.Output)
	}
	bootWant(t, res, "bootSector.bpb.fatSz16", "20")
	bootWant(t, res, "bootSector.ebpb.filSysType", `"FAT16   "`)
	bootWant(t, res, "bootSector.ebpb.volID", "0x1234ABCD")
	b2None(t, res, "fsInfo", "backupBootSector", "bootSector.ebpb.rootClus")
	// 16-bit entries
	bootAt(t, res, "fat", 4*512, 20*512)
	bootWant(t, res, "fat[0]", "0xFFF8")
	bootWant(t, res, "fat[2]", "0xFFFF")
	bootAt(t, res, "rootDirectory", 44*512, 3*32)
	bootWant(t, res, "rootDirectory.entry[0]", "label SIXTEEN")
	bootWant(t, res, "rootDirectory.entry[1]", "README.TXT 6 bytes")
	bootWant(t, res, "rootDirectory.entry[2]", "free (the end)")
	checkTruncated(t, "fat.bt", data)
}

// A FAT16 volume made by mkfs.fat, when it is installed
func TestFAT16TemplateMkfs(t *testing.T) {
	mkfs, err := exec.LookPath("mkfs.fat")
	if err != nil {
		t.Skip("mkfs.fat is not installed (PATH=$PATH:/sbin)")
	}
	img := filepath.Join(t.TempDir(), "fat16.img")
	if out, err := exec.Command(mkfs, "-F", "16", "-n", "MKFS16", "-C", img, "16384").CombinedOutput(); err != nil {
		t.Fatalf("mkfs.fat: %v\n%s", err, out)
	}
	data, err := os.ReadFile(img)
	if err != nil {
		t.Fatal(err)
	}
	res := libraryRun(t, data, "fat16.img", "fat.bt")
	if len(res.Output) == 0 || !strings.HasPrefix(res.Output[0], "FAT16: ") {
		t.Errorf("output %q", res.Output)
	}
	if len(res.Warnings) != 0 {
		t.Errorf("warnings %q", res.Warnings)
	}
	bootWant(t, res, "bootSector.ebpb.volLab", `"MKFS16     "`)
	le := binary.LittleEndian
	rsvd, fatSz := int64(le.Uint16(data[14:])), int64(le.Uint16(data[22:]))
	bootAt(t, res, "fat", rsvd*512, fatSz*512)
	if v := value(t, res, "fat[0]"); !strings.HasPrefix(v, "0xFFF") {
		t.Errorf("fat[0] = %s", v)
	}
	bootWant(t, res, "rootDirectory.entry[0]", "label MKFS16")
}

// ---- The modern MBR ----

// modernMBR has the disk timestamp of Windows 95B-ME at 0xDA
func modernMBR(drive byte) []byte {
	le := binary.LittleEndian
	b := make([]byte, 4*512)
	for i := 0; i < 0xDA; i++ {
		b[i] = 0xFA
	}
	b[0xDC], b[0xDD], b[0xDE], b[0xDF] = drive, 30, 45, 12
	for i := 0xE0; i < 440; i++ {
		b[i] = 0xCC
	}
	le.PutUint32(b[440:], 0xCAFEF00D)
	e := b[446:]
	e[0] = 0x80
	e[4] = 0x0C
	le.PutUint32(e[8:], 1)
	le.PutUint32(e[12:], 3)
	b[510], b[511] = 0x55, 0xAA
	return b
}

func TestPartMBRModern(t *testing.T) {
	data := modernMBR(0x80)
	res := libraryRun(t, data, "disk.img", "mbr.bt")
	bootAt(t, res, "mbr.bootCode", 0, 218)
	bootAt(t, res, "mbr.timestamp", 0xDA, 6)
	bootWant(t, res, "mbr.timestamp.zero", "0")
	bootWant(t, res, "mbr.timestamp.drive", "0x80")
	// a uchar shows its character too: "45 '-'"
	for path, want := range map[string]string{"seconds": "30", "minutes": "45", "hours": "12"} {
		if v := value(t, res, "mbr.timestamp."+path); !strings.HasPrefix(v, want) {
			t.Errorf("timestamp.%s = %s, want %s", path, v, want)
		}
	}
	bootAt(t, res, "mbr.bootCode2", 0xE0, 216)
	bootAt(t, res, "mbr.diskSignature", 440, 4)
	bootWant(t, res, "mbr.diskSignature", "0xCAFEF00D")
	bootWant(t, res, "mbr.partition[0]", "* FAT32_LBA, LBA 1, 3 sectors (1 KiB)")
	checkTruncated(t, "mbr.bt", data)

	// Not a timestamp: the drive below 0x80, or the hours past 23
	for _, b := range [][]byte{modernMBR(0x00), func() []byte { b := modernMBR(0x80); b[0xDF] = 24; return b }()} {
		res = libraryRun(t, b, "disk.img", "mbr.bt")
		bootAt(t, res, "mbr.bootCode", 0, 440)
		b2None(t, res, "mbr.timestamp", "mbr.bootCode2")
		bootWant(t, res, "mbr.diskSignature", "0xCAFEF00D")
	}
}
