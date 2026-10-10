package tmpl

import (
	"bytes"
	"compress/gzip"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	bt "github.com/ipoluianov/althex/core/bootimg/bootimgtest"
)

// Tests of the boot image templates (BOOT-10): android-boot.bt,
// android-vendor-boot.bt, uimage.bt, fit.bt, cpio.bt, bzimage.bt,
// eltorito.bt, grub.bt, bootsect.bt. The samples come from bootimgtest.

func bootGz(data []byte) []byte {
	var b bytes.Buffer
	w := gzip.NewWriter(&b)
	w.Write(data)
	w.Close()
	return b.Bytes()
}

// bootWant checks the value of a field
func bootWant(t *testing.T, res *Result, path, want string) {
	t.Helper()
	if v := value(t, res, path); v != want {
		t.Errorf("%s = %s, want %s", path, v, want)
	}
}

// bootAt checks the offset and the size of a field
func bootAt(t *testing.T, res *Result, path string, off, size int64) {
	t.Helper()
	n := res.Root.Find(path)
	if n == nil {
		t.Fatalf("no field %s", path)
	}
	if n.Off != off || n.Size != size {
		t.Errorf("%s at 0x%X size %d, want 0x%X size %d", path, n.Off, n.Size, off, size)
	}
}

// bootRunFile runs a template chosen by its file name (not detected)
func bootRunFile(t *testing.T, file string, data []byte) *Result {
	t.Helper()
	for _, tm := range Library() {
		if tm.File == file {
			p, err := tm.Program()
			if err != nil {
				t.Fatal(err)
			}
			res := p.Run(context.Background(), Env{Data: &mem{b: data}})
			if res.Err != nil {
				t.Fatalf("%s: %v", file, res.Err)
			}
			return res
		}
	}
	t.Fatalf("no template %s", file)
	return nil
}

// bootCuts runs the template over cuts of the data: no panics, no errors
func bootCuts(t *testing.T, file string, data []byte) {
	t.Helper()
	var p *Program
	for _, tm := range Library() {
		if tm.File == file {
			p, _ = tm.Program()
		}
	}
	if p == nil {
		t.Fatalf("no template %s", file)
	}
	for n := 0; n <= len(data); n += 1 + n/40 {
		res := p.Run(context.Background(), Env{Data: &mem{b: append([]byte(nil), data[:n]...)}, MaxSteps: 5_000_000})
		if res.Err != nil {
			t.Errorf("%s cut at %d: %v", file, n, res.Err)
			return
		}
	}
}

func TestBootAndroidV0Template(t *testing.T) {
	kernel := bootGz(bytes.Repeat([]byte("kernel"), 500))
	data := bt.AndroidBoot(bt.Android{Version: 0, PageSize: 2048, Cmdline: "console=ttyS0",
		OSVersion: bt.AndroidOS(9, 0, 0, 2019, 3), Kernel: kernel, Ramdisk: []byte("rd"), Second: []byte("2nd")})
	res := libraryRun(t, data, "boot.img", "android-boot.bt")
	bootWant(t, res, "header.page_size", "2048")
	bootWant(t, res, "header.cmdline", `"console=ttyS0"`)
	bootWant(t, res, "header.os_version", "9.0.0, patch 2019-03")
	bootAt(t, res, "kernel", 2048, int64(len(kernel)))
	rd := 2048 + int64((len(kernel)+2047)/2048*2048)
	bootAt(t, res, "ramdisk", rd, 2)
	bootAt(t, res, "second", rd+2048, 3)
	if res.Root.Find("dtb") != nil || res.Root.Find("header.dtb_size") != nil {
		t.Error("dtb in v0")
	}
	bootCuts(t, "android-boot.bt", data)
}

func TestBootAndroidV2Template(t *testing.T) {
	data := bt.AndroidBoot(bt.Android{Version: 2, PageSize: 4096, Kernel: make([]byte, 5000),
		Ramdisk: []byte("rd"), RecoveryDTBO: []byte("dtbo"), DTB: []byte("dtb!")})
	res := libraryRun(t, data, "boot.img", "android-boot.bt")
	bootWant(t, res, "header.header_version", "2")
	bootWant(t, res, "header.header_size", "1660")
	bootAt(t, res, "kernel", 4096, 5000)
	bootAt(t, res, "ramdisk", 3*4096, 2)
	bootAt(t, res, "recovery_dtbo", 4*4096, 4)
	bootAt(t, res, "dtb", 5*4096, 4)
}

func TestBootAndroidV4Template(t *testing.T) {
	data := bt.AndroidBoot(bt.Android{Version: 4, Cmdline: "x=1", OSVersion: bt.AndroidOS(13, 0, 0, 2023, 5),
		Kernel: make([]byte, 10000), Ramdisk: []byte("ramdisk"), Signature: make([]byte, 4096)})
	res := libraryRun(t, data, "boot.img", "android-boot.bt")
	bootWant(t, res, "header.header_version", "4")
	bootWant(t, res, "header.signature_size", "4096")
	bootWant(t, res, "header.os_version", "13.0.0, patch 2023-05")
	bootAt(t, res, "kernel", 4096, 10000)
	bootAt(t, res, "ramdisk", 4*4096, 7)
	bootAt(t, res, "boot_signature", 5*4096, 4096)
	bootCuts(t, "android-boot.bt", data)
}

func TestBootVendorTemplate(t *testing.T) {
	rds := []bt.VendorRamdisk{{Type: 1, Data: []byte("platform")}, {Name: "dlkm", Type: 3, Data: make([]byte, 3000)}}
	data := bt.VendorBootV4(4096, "vcmd", rds, []byte{0xD0, 0x0D, 0xFE, 0xED}, []byte("a=b\n"))
	res := libraryRun(t, data, "vendor_boot.img", "android-vendor-boot.bt")
	bootWant(t, res, "header.header_version", "4")
	bootWant(t, res, "header.cmdline", `"vcmd"`)
	bootAt(t, res, "vendor_ramdisks", 4096, 3008)
	bootAt(t, res, "dtb", 2*4096, 4)
	bootWant(t, res, "vendor_ramdisk_table[1].ramdisk_type", "VENDOR_RAMDISK_TYPE_DLKM (3)")
	bootWant(t, res, "vendor_ramdisk_table[1].ramdisk_offset", "0x00000008")
	bootWant(t, res, "vendor_ramdisk_table[1].ramdisk_name", `"dlkm"`)
	bootAt(t, res, "bootconfig", 4*4096, 4)
	bootCuts(t, "android-vendor-boot.bt", data)
}

func TestBootUImageTemplate(t *testing.T) {
	kernel := bootGz([]byte("kernel"))
	data := bt.UImage("Linux-6.1", 2, 1, kernel)
	res := libraryRun(t, data, "uImage", "uimage.bt")
	bootWant(t, res, "header.ih_os", "OS_LINUX (5)")
	bootWant(t, res, "header.ih_arch", "ARCH_ARM (2)")
	bootWant(t, res, "header.ih_comp", "COMP_GZIP (1)")
	bootWant(t, res, "header.ih_name", `"Linux-6.1"`)
	bootWant(t, res, "header.ih_load", "0x80008000")
	if c := res.Root.Find("header.ih_hcrc").Comment; c != "OK" {
		t.Errorf("header CRC: %q", c)
	}
	if c := res.Root.Find("header.ih_dcrc").Comment; c != "OK" {
		t.Errorf("data CRC: %q", c)
	}
	bootAt(t, res, "data", 64, int64(len(kernel)))

	data[40] ^= 1
	data[70] ^= 1
	res = libraryRun(t, data, "uImage", "uimage.bt")
	if c := res.Root.Find("header.ih_hcrc").Comment; !strings.HasPrefix(c, "bad") {
		t.Errorf("header CRC: %q", c)
	}
	if c := res.Root.Find("header.ih_dcrc").Comment; !strings.HasPrefix(c, "bad") {
		t.Errorf("data CRC: %q", c)
	}

	data = bt.UImageMulti("multi", make([]byte, 10), []byte("rd"), []byte("dtb12"))
	res = libraryRun(t, data, "uImage", "uimage.bt")
	bootWant(t, res, "header.ih_type", "TYPE_MULTI (4)")
	bootWant(t, res, "sizes[2]", "5")
	bootAt(t, res, "file[0]", 80, 10)
	bootAt(t, res, "file[1]", 92, 2)
	bootAt(t, res, "file[2]", 96, 5)
	bootCuts(t, "uimage.bt", data)
}

func bootFIT() []byte {
	root := &bt.Node{Props: []bt.Prop{bt.P("description", bt.Str("test FIT"))},
		Children: []*bt.Node{
			{Name: "images", Children: []*bt.Node{
				{Name: "kernel-1", Props: []bt.Prop{bt.P("data", bootGz([]byte("k"))), bt.P("type", bt.Str("kernel"))}},
				{Name: "ramdisk-1", Props: []bt.Prop{bt.P("data-size", bt.U32(3)), bt.P("data-offset", bt.U32(0)), bt.P("type", bt.Str("ramdisk"))}},
			}},
			{Name: "configurations", Props: []bt.Prop{bt.P("default", bt.Str("conf-1"))}, Children: []*bt.Node{
				{Name: "conf-1", Props: []bt.Prop{bt.P("kernel", bt.Str("kernel-1"))}},
			}},
		}}
	return bt.FDT(root, []byte("RD!"))
}

func TestBootFITTemplate(t *testing.T) {
	data := bootFIT()
	res := libraryRun(t, data, "x.itb", "fit.bt")
	bootWant(t, res, "header.version", "17")
	if v := value(t, res, "root.prop[0]"); v != "description = test FIT" {
		t.Errorf("root.prop[0] = %s", v)
	}
	bootWant(t, res, "root.node[0].name", `"images"`)
	bootWant(t, res, "root.node[0].node[0]", "kernel-1")
	kernel := bootGz([]byte("k"))
	bootAt(t, res, "root.node[0].node[0].prop[0].value", int64(bytes.Index(data, kernel)), int64(len(kernel)))
	if v := value(t, res, "root.node[0].node[1].prop[2]"); v != "type = ramdisk" {
		t.Errorf("type: %s", v)
	}
	bootWant(t, res, "root.node[1].node[0].prop[0].value", `"kernel-1"`)
	bootAt(t, res, "external_data", int64(len(data)-3), 3)
	if res.Root.Find("end") == nil {
		t.Error("no FDT_END")
	}
	bootCuts(t, "fit.bt", data)
}

func TestBootCpioTemplate(t *testing.T) {
	first := bt.Cpio([]bt.CpioEntry{{Name: "kernel/x86/microcode/GenuineIntel.bin", Mode: 0o100644, Data: make([]byte, 100)}})
	first = append(first, make([]byte, 512-len(first)%512)...)
	second := bt.Cpio([]bt.CpioEntry{
		{Name: "bin", Mode: 0o040755},
		{Name: "bin/sh", Mode: 0o100755, Data: []byte("#!shell")},
		{Name: "init", Mode: 0o120777, Data: []byte("bin/sh")},
	})
	tail := bootGz([]byte("rest"))
	data := append(append(append([]byte(nil), first...), second...), tail...)
	res := libraryRun(t, data, "initrd.img", "cpio.bt")
	bootWant(t, res, "entry[0]", "kernel/x86/microcode/GenuineIntel.bin")
	bootWant(t, res, "entry[0].c_filesize", "100 (0x64)")
	bootWant(t, res, "entry[2]", "bin")
	bootWant(t, res, "entry[2].c_mode", "dir 755")
	bootWant(t, res, "entry[4].c_mode", "symlink 777")
	bootWant(t, res, "entry[5]", "TRAILER!!!")
	sh := int64(len(first)) + int64(bytes.Index(second, []byte("#!shell")))
	bootAt(t, res, "entry[3].data", sh, 7)
	bootAt(t, res, "compressed_rest", int64(len(first)+len(second)), int64(len(tail)))
	bootCuts(t, "cpio.bt", data)
}

func TestBootBzImageTemplate(t *testing.T) {
	payload := append([]byte{0x28, 0xB5, 0x2F, 0xFD}, make([]byte, 100)...)
	data := bt.BzImage(30, "6.1.0-test #1", payload)
	res := libraryRun(t, data, "vmlinuz", "bzimage.bt")
	bootWant(t, res, "setup_header.version", "2.15")
	bootWant(t, res, "setup_header.setup_sects", "30")
	if c := res.Root.Find("setup_header.kernel_version").Comment; c != "6.1.0-test #1" {
		t.Errorf("kernel version %q", c)
	}
	bootWant(t, res, "setup_header.cmdline_size", "2047")
	bootAt(t, res, "setup_header", 0x1F1, 0x26C-0x1F1)
	bootAt(t, res, "payload", 31*512+0x2CC, 104)
	bootCuts(t, "bzimage.bt", data)
}

func TestBootElToritoTemplate(t *testing.T) {
	data := bt.ElTorito([]bt.BootEntry{{Platform: 0, Sectors: 4, Data: make([]byte, 3000)},
		{Platform: 0xEF, Sectors: 1, Data: bt.FAT12(2880)}})
	// Chosen by hand: an ISO is the ISO template's by default
	res := bootRunFile(t, "eltorito.bt", data)
	bootWant(t, res, "boot_record.catalog_sector", "19")
	bootAt(t, res, "boot_catalog", 19*2048, 64+64)
	if c := res.Root.Find("boot_catalog.validation_entry.checksum").Comment; !strings.HasPrefix(c, "OK") {
		t.Errorf("checksum: %q", c)
	}
	bootWant(t, res, "boot_catalog.default_entry.boot_indicator", "BOOTABLE (136)")
	bootAt(t, res, "boot_catalog.default_entry.image", 20*2048, 2048)
	bootWant(t, res, "boot_catalog.section.header.platform", "UEFI (239)")
	bootAt(t, res, "boot_catalog.section.entry.image", 22*2048, 2880*512)
	bootCuts(t, "eltorito.bt", data[:20*2048])

	m := &mem{b: data}
	if tm := Detect(Library(), m, "x.iso"); tm != nil && tm.File == "eltorito.bt" && tm.Meta.Priority >= 0 {
		t.Error("eltorito.bt must not win over the ISO template")
	}
}

func TestBootGrubTemplate(t *testing.T) {
	// mbr.bt (of 55 AA) wins by priority; grub.bt is chosen by hand
	res := bootRunFile(t, "grub.bt", bt.GrubBoot(1, 0xFF))
	bootWant(t, res, "boot_img.kernel_sector", "1")
	bootAt(t, res, "boot_img.kernel_sector", 0x5C, 8)
	bootAt(t, res, "boot_img.boot_drive", 0x64, 1)
	bootWant(t, res, "boot_img.signature", "0xAA55")

	res = bootRunFile(t, "grub.bt", bt.GrubCore(2, 60, 0x820, make([]byte, 1000)))
	bootWant(t, res, "diskboot.firstlist.start", "2")
	bootWant(t, res, "diskboot.firstlist.len", "60")
	bootAt(t, res, "diskboot.firstlist", 0x1F4, 12)
	bootAt(t, res, "decompressor_and_kernel", 512, 1000)
	bootCuts(t, "grub.bt", bt.GrubBoot(1, 0xFF))
	bootCuts(t, "grub.bt", bt.GrubCore(2, 60, 0x820, nil))
}

func TestBootSectTemplate(t *testing.T) {
	// ntfs.bt (priority 1) wins on an NTFS boot sector; bootsect.bt is chosen by hand
	res := bootRunFile(t, "bootsect.bt", bt.NTFSBoot())
	bootWant(t, res, "oem_name", `"NTFS    "`)
	bootWant(t, res, "bpb.volume_serial", "0x1234ABCD5678EF90")
	bootWant(t, res, "bpb.total_sectors", "1048576")
	bootAt(t, res, "signature", 510, 2)
	bootAt(t, res, "boot_code_more", 512, 15*512)

	res = bootRunFile(t, "bootsect.bt", bt.FAT12(2880))
	bootWant(t, res, "bpb.total_sectors16", "2880")
	bootWant(t, res, "ebpb.fs_type", `"FAT12   "`)
	bootAt(t, res, "signature", 510, 2)
	bootCuts(t, "bootsect.bt", bt.NTFSBoot())
}

// The templates over the real files of /boot, when they can be read
func TestBootRealFilesTemplate(t *testing.T) {
	if testing.Short() {
		t.Skip("short")
	}
	found := false
	for _, c := range []struct{ pat, file string }{{"/boot/vmlinuz*", "bzimage.bt"}, {"/boot/initrd.img*", "cpio.bt"}} {
		files, _ := filepath.Glob(c.pat)
		if len(files) == 0 {
			continue
		}
		data, err := os.ReadFile(files[len(files)-1])
		if err != nil {
			continue
		}
		found = true
		res := libraryRun(t, data, filepath.Base(files[len(files)-1]), c.file)
		for _, w := range res.Output {
			t.Errorf("%s: %s", files[len(files)-1], w)
		}
		if c.file == "bzimage.bt" && res.Root.Find("payload") == nil {
			t.Errorf("%s: no payload", files[len(files)-1])
		}
		if c.file == "cpio.bt" && res.Root.Find("entry") == nil {
			t.Errorf("%s: no entries", files[len(files)-1])
		}
	}
	if !found {
		t.Skip("no readable /boot files")
	}
}

// cpio archives made by the cpio tool: newc, odc, crc
func TestBootCpioToolTemplate(t *testing.T) {
	cpio, err := exec.LookPath("cpio")
	if err != nil {
		t.Skip("cpio is not installed")
	}
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "etc"), 0o755)
	os.WriteFile(filepath.Join(dir, "etc/a.txt"), []byte("hello"), 0o644)
	os.Symlink("etc/a.txt", filepath.Join(dir, "link"))
	for _, format := range []string{"newc", "odc", "crc"} {
		cmd := exec.Command(cpio, "-o", "-H", format)
		cmd.Dir = dir
		cmd.Stdin = strings.NewReader("etc\netc/a.txt\nlink\n")
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("cpio -H %s: %v", format, err)
		}
		res := libraryRun(t, out, "x.cpio", "cpio.bt")
		bootWant(t, res, "entry[1]", "etc/a.txt")
		bootAt(t, res, "entry[1].data", int64(bytes.Index(out, []byte("hello"))), 5)
		bootWant(t, res, "entry[3]", "TRAILER!!!")
	}
}
