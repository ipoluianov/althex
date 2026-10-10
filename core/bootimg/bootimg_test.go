package bootimg

import (
	"bytes"
	"compress/gzip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	bt "github.com/ipoluianov/althex/core/bootimg/bootimgtest"
	"github.com/ipoluianov/althex/internal/fuzzguard"
)

func gz(_ testing.TB, data []byte) []byte {
	var b bytes.Buffer
	w := gzip.NewWriter(&b)
	w.Write(data)
	w.Close()
	return b.Bytes()
}

func detect(t *testing.T, data []byte) *Image {
	t.Helper()
	img, err := Detect(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	return img
}

// part checks that the image has the part at off of size and returns it
func part(t *testing.T, img *Image, name string, off, size int64) Part {
	t.Helper()
	for _, p := range img.Parts {
		if p.Name == name {
			if p.Off != off || p.Size != size {
				t.Errorf("%s: %s at 0x%X size %d, want 0x%X size %d", img.Format, name, p.Off, p.Size, off, size)
			}
			return p
		}
	}
	t.Fatalf("%s: no part %q in %+v", img.Format, name, img.Parts)
	return Part{}
}

func info(t *testing.T, img *Image, key, want string) {
	t.Helper()
	for _, kv := range img.Info {
		if kv.Key == key {
			if kv.Value != want {
				t.Errorf("%s: %s = %q, want %q", img.Format, key, kv.Value, want)
			}
			return
		}
	}
	t.Errorf("%s: no info %q in %+v", img.Format, key, img.Info)
}

func TestAndroidV0(t *testing.T) {
	kernel := gz(t, bytes.Repeat([]byte("kernel"), 500))
	ramdisk := bt.Cpio([]bt.CpioEntry{{Name: "init", Mode: 0o100755, Data: []byte("x")}})
	data := bt.AndroidBoot(bt.Android{Version: 0, PageSize: 2048, Cmdline: "console=ttyS0",
		OSVersion: bt.AndroidOS(9, 0, 0, 2019, 3), Kernel: kernel, Ramdisk: ramdisk, Second: []byte("2nd")})
	img := detect(t, data)
	if img.Format != "Android boot (v0)" {
		t.Fatal(img.Format)
	}
	k := part(t, img, "kernel", 2048, int64(len(kernel)))
	if k.Compression != "gzip" {
		t.Errorf("kernel compression %q", k.Compression)
	}
	rdOff := 2048 + alignUp(int64(len(kernel)), 2048)
	part(t, img, "ramdisk", rdOff, int64(len(ramdisk)))
	part(t, img, "second", rdOff+2048, 3)
	info(t, img, "cmdline", "console=ttyS0")
	info(t, img, "OS version", "9.0.0")
	info(t, img, "OS patch level", "2019-03")
	info(t, img, "kernel address", "0x10008000")
}

func TestAndroidV2(t *testing.T) {
	data := bt.AndroidBoot(bt.Android{Version: 2, PageSize: 4096, Kernel: make([]byte, 5000),
		Ramdisk: []byte{0x04, 0x22, 0x4D, 0x18, 1, 2}, RecoveryDTBO: []byte("dtbo"), DTB: []byte{0xD0, 0x0D, 0xFE, 0xED}})
	img := detect(t, data)
	if img.Format != "Android boot (v2)" {
		t.Fatal(img.Format)
	}
	part(t, img, "kernel", 4096, 5000)
	if p := part(t, img, "ramdisk", 4096*3, 6); p.Compression != "lz4" {
		t.Errorf("ramdisk compression %q", p.Compression)
	}
	part(t, img, "recovery_dtbo", 4096*4, 4)
	part(t, img, "dtb", 4096*5, 4)
	info(t, img, "dtb address", "0x11F00000")
}

func TestAndroidV4(t *testing.T) {
	data := bt.AndroidBoot(bt.Android{Version: 4, Cmdline: "androidboot.x=1",
		OSVersion: bt.AndroidOS(13, 0, 0, 2023, 5), Kernel: make([]byte, 10000),
		Ramdisk: gz(t, []byte("ramdisk")), Signature: make([]byte, 4096)})
	img := detect(t, data)
	if img.Format != "Android boot (v4)" {
		t.Fatal(img.Format)
	}
	part(t, img, "kernel", 4096, 10000)
	r := part(t, img, "ramdisk", 4096*4, int64(len(gz(t, []byte("ramdisk")))))
	if r.Compression != "gzip" {
		t.Error(r.Compression)
	}
	if p := part(t, img, "boot signature", 4096*5, 4096); p.Kind != "signature" {
		t.Error(p.Kind)
	}
	info(t, img, "cmdline", "androidboot.x=1")
	info(t, img, "OS version", "13.0.0")
	info(t, img, "OS patch level", "2023-05")
}

func TestVendorBootV4(t *testing.T) {
	rds := []bt.VendorRamdisk{
		{Name: "", Type: 1, Data: gz(t, []byte("platform"))},
		{Name: "dlkm", Type: 3, Data: make([]byte, 3000)},
	}
	bc := []byte("androidboot.hardware=x\nkernel.foo=1\n")
	data := bt.VendorBootV4(4096, "vcmd", rds, []byte{0xD0, 0x0D, 0xFE, 0xED, 0, 0, 0, 0}, bc)
	img := detect(t, data)
	if img.Format != "Android vendor_boot (v4)" {
		t.Fatal(img.Format)
	}
	n0 := int64(len(rds[0].Data))
	if p := part(t, img, "vendor_ramdisk: platform", 4096, n0); p.Compression != "gzip" {
		t.Error(p.Compression)
	}
	part(t, img, "vendor_ramdisk: dlkm (dlkm)", 4096+n0, 3000)
	part(t, img, "dtb", 4096*2, 8)
	part(t, img, "vendor ramdisk table", 4096*3, 216)
	if p := part(t, img, "bootconfig", 4096*4, int64(len(bc))); p.Kind != "config" {
		t.Error(p.Kind)
	}
	info(t, img, "cmdline", "vcmd")
	info(t, img, "bootconfig", "androidboot.hardware=x; kernel.foo=1;")
}

func TestUImage(t *testing.T) {
	kernel := gz(t, bytes.Repeat([]byte("k"), 1000))
	data := bt.UImage("Linux-6.1", 2, 1, kernel)
	img := detect(t, data)
	if img.Format != "U-Boot legacy image" {
		t.Fatal(img.Format)
	}
	info(t, img, "header CRC", "OK")
	info(t, img, "data CRC", "OK")
	info(t, img, "image name", "Linux-6.1")
	info(t, img, "OS", "Linux")
	info(t, img, "architecture", "ARM")
	info(t, img, "type", "kernel")
	info(t, img, "compression", "gzip")
	info(t, img, "load address", "0x80008000")
	info(t, img, "entry point", "0x80008040")
	if p := part(t, img, "kernel", 64, int64(len(kernel))); p.Compression != "gzip" {
		t.Error(p.Compression)
	}

	data[70] ^= 1
	data[40] ^= 1
	img = detect(t, data)
	info(t, img, "header CRC", "bad")
	info(t, img, "data CRC", "bad")
}

func TestUImageMulti(t *testing.T) {
	k, r, d := make([]byte, 10), gz(t, []byte("rd")), []byte{0xD0, 0x0D, 0xFE, 0xED, 1}
	data := bt.UImageMulti("multi", k, r, d)
	img := detect(t, data)
	info(t, img, "type", "multi-file")
	info(t, img, "files", "3")
	info(t, img, "header CRC", "OK")
	info(t, img, "data CRC", "OK")
	base := int64(64 + 16)
	part(t, img, "kernel", base, 10)
	if p := part(t, img, "ramdisk", base+12, int64(len(r))); p.Compression != "gzip" {
		t.Error(p.Compression)
	}
	part(t, img, "dtb", base+12+alignUp(int64(len(r)), 4), 5)
}

// fit makes a FIT with an inline kernel and an external ramdisk
func fit(t testing.TB) (data, kernel, ramdisk []byte) {
	kernel = gz(t, bytes.Repeat([]byte("kernel"), 100))
	ramdisk = []byte{0x28, 0xB5, 0x2F, 0xFD, 9, 9, 9}
	root := &bt.Node{Props: []bt.Prop{bt.P("description", bt.Str("test FIT")), bt.P("#address-cells", bt.U32(1))},
		Children: []*bt.Node{
			{Name: "images", Children: []*bt.Node{
				{Name: "kernel-1", Props: []bt.Prop{
					bt.P("description", bt.Str("Linux")), bt.P("data", kernel), bt.P("type", bt.Str("kernel")),
					bt.P("arch", bt.Str("arm64")), bt.P("os", bt.Str("linux")), bt.P("compression", bt.Str("gzip")),
					bt.P("load", bt.U32(0x80080000)), bt.P("entry", bt.U32(0x80080000))},
					Children: []*bt.Node{{Name: "hash-1", Props: []bt.Prop{bt.P("algo", bt.Str("sha256")), bt.P("value", make([]byte, 32))}}}},
				{Name: "ramdisk-1", Props: []bt.Prop{
					bt.P("data-size", bt.U32(uint32(len(ramdisk)))), bt.P("data-offset", bt.U32(4)),
					bt.P("type", bt.Str("ramdisk")), bt.P("arch", bt.Str("arm64")), bt.P("compression", bt.Str("none"))}},
			}},
			{Name: "configurations", Props: []bt.Prop{bt.P("default", bt.Str("conf-1"))}, Children: []*bt.Node{
				{Name: "conf-1", Props: []bt.Prop{bt.P("kernel", bt.Str("kernel-1")), bt.P("ramdisk", bt.Str("ramdisk-1"))}},
			}},
		}}
	data = bt.FDT(root, append([]byte("pad!"), ramdisk...))
	return data, kernel, ramdisk
}

func TestFIT(t *testing.T) {
	data, kernel, ramdisk := fit(t)
	img := detect(t, data)
	if img.Format != "U-Boot FIT" {
		t.Fatal(img.Format)
	}
	k := part(t, img, "kernel-1", int64(bytes.Index(data, kernel)), int64(len(kernel)))
	if k.Kind != "kernel" || k.Compression != "gzip" {
		t.Errorf("%+v", k)
	}
	r := part(t, img, "ramdisk-1", int64(len(data)-len(ramdisk)), int64(len(ramdisk)))
	if r.Kind != "ramdisk" || r.Compression != "zstd" {
		t.Errorf("%+v", r)
	}
	info(t, img, "description", "test FIT")
	info(t, img, "default configuration", "conf-1")
	info(t, img, "configuration conf-1", "kernel=kernel-1; ramdisk=ramdisk-1")
	info(t, img, "image kernel-1", "description=Linux; type=kernel; arch=arm64; os=linux; compression=gzip; load=0x80080000; entry=0x80080000")
}

func TestDeviceTree(t *testing.T) {
	data := bt.FDT(&bt.Node{Props: []bt.Prop{bt.P("model", bt.Str("Board X")), bt.P("compatible", bt.Str("vendor,x", "vendor,soc"))},
		Children: []*bt.Node{{Name: "cpus"}}}, nil)
	img := detect(t, data)
	if img.Format != "Device tree" || len(img.Parts) != 0 {
		t.Fatalf("%+v", img)
	}
	info(t, img, "model", "Board X")
	info(t, img, "compatible", "vendor,x, vendor,soc")
}

func cpioSample() []bt.CpioEntry {
	return []bt.CpioEntry{
		{Name: "bin", Mode: 0o040755},
		{Name: "bin/sh", Mode: 0o100755, Data: []byte("#!shell")},
		{Name: "init", Mode: 0o120777, Data: []byte("bin/sh")},
		{Name: "data.gz", Mode: 0o100644, Data: gz(nil, []byte("x"))},
	}
}

func TestCpio(t *testing.T) {
	data := bt.Cpio(cpioSample())
	img := detect(t, data)
	if img.Format != "cpio (newc)" || len(img.Parts) != 4 {
		t.Fatalf("%+v", img)
	}
	kinds := []string{"dir", "file", "symlink", "file"}
	for i, p := range img.Parts {
		if p.Kind != kinds[i] {
			t.Errorf("%s: %s", p.Name, p.Kind)
		}
	}
	part(t, img, "bin/sh", int64(bytes.Index(data, []byte("#!shell"))), 7)
	if p := img.Parts[3]; p.Compression != "gzip" {
		t.Error(p.Compression)
	}
}

func TestCpioConcatenated(t *testing.T) {
	first := bt.Cpio([]bt.CpioEntry{{Name: "kernel/x86/microcode/GenuineIntel.bin", Mode: 0o100644, Data: make([]byte, 100)}})
	first = append(first, make([]byte, 512-len(first)%512)...)
	second := bt.Cpio(cpioSample())
	tail := gz(t, bt.Cpio(cpioSample()))
	data := append(append(append(append([]byte(nil), first...), second...), make([]byte, 16)...), tail...)
	img := detect(t, data)
	if len(img.Parts) != 6 {
		t.Fatalf("%d parts: %+v", len(img.Parts), img.Parts)
	}
	info(t, img, "archives", "2")
	// "bin\0" after the header of 110 bytes, aligned to 4
	part(t, img, "bin", int64(len(first))+116, 0)
	tailOff := int64(len(first) + len(second) + 16)
	if p := part(t, img, "compressed initramfs", tailOff, int64(len(tail))); p.Compression != "gzip" {
		t.Error(p.Compression)
	}
}

func TestBzImage(t *testing.T) {
	payload := append([]byte{0x28, 0xB5, 0x2F, 0xFD}, make([]byte, 100)...)
	data := bt.BzImage(30, "6.1.0-test (x@y) #1 SMP", payload)
	img := detect(t, data)
	if img.Format != "Linux bzImage" {
		t.Fatal(img.Format)
	}
	info(t, img, "boot protocol", "2.15")
	info(t, img, "setup sectors", "30")
	info(t, img, "kernel version", "6.1.0-test (x@y) #1 SMP")
	info(t, img, "cmdline_size", "2047")
	part(t, img, "setup", 0, 31*512)
	part(t, img, "kernel", 31*512, 0x2CC+104)
	if p := part(t, img, "kernel (compressed payload)", 31*512+0x2CC, 104); p.Compression != "zstd" {
		t.Error(p.Compression)
	}
}

func TestElTorito(t *testing.T) {
	x86 := append([]byte{0xFA, 0xEB}, make([]byte, 3000)...)
	efi := bt.FAT12(2880)
	data := bt.ElTorito([]bt.BootEntry{{Platform: 0, Sectors: 4, Data: x86}, {Platform: 0xEF, Sectors: 1, Data: efi}})
	img := detect(t, data)
	if img.Format != "El Torito" {
		t.Fatal(img.Format)
	}
	info(t, img, "validation entry checksum", "OK")
	info(t, img, "boot catalog", "sector 19")
	info(t, img, "volume", "SAMPLE")
	part(t, img, "boot catalog", 19*2048, 2048)
	p := part(t, img, "default entry (x86, no emulation)", 20*2048, 4*512)
	if p.Kind != "boot catalog entry" {
		t.Error(p.Kind)
	}
	part(t, img, "section 1 entry 1 (UEFI, no emulation)", 22*2048, 2880*512)
}

func TestBootSectors(t *testing.T) {
	img := detect(t, bt.GrubBoot(1, 0xFF))
	if img.Format != "GRUB boot.img" {
		t.Fatal(img.Format)
	}
	info(t, img, "kernel sector", "1")
	img = detect(t, bt.GrubCore(2, 60, 0x820, make([]byte, 1000)))
	if img.Format != "GRUB core.img" {
		t.Fatal(img.Format)
	}
	info(t, img, "blocklist 0", "60 sectors at LBA 2 to segment 0x0820")
	part(t, img, "decompressor and kernel", 512, 1000)
	img = detect(t, bt.NTFSBoot())
	if img.Format != "NTFS boot sector (BOOTMGR)" {
		t.Fatal(img.Format)
	}
	info(t, img, "volume serial", "1234ABCD5678EF90")
}

func TestUnknown(t *testing.T) {
	for _, d := range [][]byte{nil, []byte("hello"), make([]byte, 100000)} {
		if _, err := Detect(bytes.NewReader(d), int64(len(d))); err != ErrUnknown {
			t.Errorf("%d bytes: %v", len(d), err)
		}
	}
}

// Every cut of every sample is detected without a panic
func TestTruncated(t *testing.T) {
	data, _, _ := fit(t)
	samples := [][]byte{data, bt.Cpio(cpioSample()), bt.UImageMulti("m", []byte("a"), []byte("bc")),
		bt.AndroidBoot(bt.Android{Version: 2, PageSize: 2048, Kernel: []byte("k"), DTB: []byte("d")}),
		bt.VendorBootV4(2048, "", []bt.VendorRamdisk{{Type: 1, Data: []byte("r")}}, nil, []byte("b")),
		bt.BzImage(4, "v", []byte("p")),
	}
	for _, s := range samples {
		for n := 0; n <= len(s); n += 1 + n/50 {
			Detect(bytes.NewReader(s[:n]), int64(n))
		}
	}
}

// Real images made by the tools, when they are installed

func tool(t *testing.T, name string) string {
	p, err := exec.LookPath(name)
	if err != nil {
		t.Skipf("%s is not installed", name)
	}
	return p
}

func TestCpioTool(t *testing.T) {
	cpio := tool(t, "cpio")
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "etc/sub"), 0o755)
	os.WriteFile(filepath.Join(dir, "etc/a.txt"), []byte("hello"), 0o644)
	os.Symlink("etc/a.txt", filepath.Join(dir, "link"))
	for _, format := range []string{"newc", "odc", "crc", "bin"} {
		cmd := exec.Command(cpio, "-o", "-H", format)
		cmd.Dir = dir
		cmd.Stdin = strings.NewReader("etc\netc/sub\netc/a.txt\nlink\n")
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("cpio -H %s: %v", format, err)
		}
		img := detect(t, out)
		if len(img.Parts) != 4 {
			t.Fatalf("%s: %+v", format, img)
		}
		p := part(t, img, "etc/a.txt", int64(bytes.Index(out, []byte("hello"))), 5)
		if p.Kind != "file" || img.Parts[3].Kind != "symlink" || img.Parts[1].Kind != "dir" {
			t.Errorf("%s: %+v", format, img.Parts)
		}
	}
}

func TestElToritoTool(t *testing.T) {
	mk := tool(t, "genisoimage")
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	os.MkdirAll(filepath.Join(root, "boot"), 0o755)
	os.WriteFile(filepath.Join(root, "boot/loader.bin"), bytes.Repeat([]byte{0x90}, 6000), 0o644)
	os.WriteFile(filepath.Join(root, "boot/efi.img"), bt.FAT12(2880), 0o644)
	iso := filepath.Join(dir, "x.iso")
	args := []string{"-quiet", "-o", iso, "-b", "boot/loader.bin", "-c", "boot/boot.cat",
		"-no-emul-boot", "-boot-load-size", "4",
		"-eltorito-alt-boot", "-e", "boot/efi.img", "-no-emul-boot", root}
	efi := true
	if out, err := exec.Command(mk, args...).CombinedOutput(); err != nil {
		// A genisoimage without -e (UEFI): x86 only
		efi = false
		args = []string{"-quiet", "-o", iso, "-b", "boot/loader.bin", "-c", "boot/boot.cat", "-no-emul-boot", "-boot-load-size", "4", root}
		if out2, err := exec.Command(mk, args...).CombinedOutput(); err != nil {
			t.Fatalf("%v\n%s\n%s", err, out, out2)
		}
	}
	data, err := os.ReadFile(iso)
	if err != nil {
		t.Fatal(err)
	}
	img := detect(t, data)
	if img.Format != "El Torito" {
		t.Fatal(img.Format)
	}
	info(t, img, "validation entry checksum", "OK")
	var x86, uefi *Part
	for i, p := range img.Parts {
		switch {
		case strings.HasPrefix(p.Name, "default entry"):
			x86 = &img.Parts[i]
		case strings.Contains(p.Name, "UEFI"):
			uefi = &img.Parts[i]
		}
	}
	if x86 == nil || x86.Size != 2048 || !bytes.Equal(data[x86.Off:x86.Off+16], bytes.Repeat([]byte{0x90}, 16)) {
		t.Errorf("x86 entry %+v", x86)
	}
	if efi {
		if uefi == nil || uefi.Size != 2880*512 || !bytes.Equal(data[uefi.Off:uefi.Off+512], bt.FAT12(2880)[:512]) {
			t.Errorf("UEFI entry %+v", uefi)
		}
	}
}

func TestRealBootFiles(t *testing.T) {
	found := false
	for _, pat := range []string{"/boot/vmlinuz*", "/boot/initrd.img*"} {
		files, _ := filepath.Glob(pat)
		for _, f := range files {
			fh, err := os.Open(f)
			if err != nil {
				continue // not readable
			}
			st, _ := fh.Stat()
			img, err := Detect(fh, st.Size())
			fh.Close()
			if err != nil {
				t.Errorf("%s: %v", f, err)
				continue
			}
			found = true
			if strings.Contains(pat, "vmlinuz") {
				if img.Format != "Linux bzImage" {
					t.Errorf("%s: %s", f, img.Format)
				}
				for _, p := range img.Parts {
					if p.Name == "kernel (compressed payload)" && p.Compression == "" {
						t.Errorf("%s: payload compression unknown", f)
					}
				}
			} else {
				if !strings.HasPrefix(img.Format, "cpio") || len(img.Parts) == 0 {
					t.Errorf("%s: %s, %d parts", f, img.Format, len(img.Parts))
				}
				for _, kv := range img.Info {
					if kv.Key == "error" {
						t.Errorf("%s: %s", f, kv.Value)
					}
				}
			}
			t.Logf("%s: %s, %d parts", f, img.Format, len(img.Parts))
		}
	}
	if !found {
		t.Skip("no readable /boot files")
	}
}

func FuzzDetect(f *testing.F) {
	data, _, _ := fit(f)
	f.Add(data)
	f.Add(bt.Cpio(cpioSample()))
	f.Add(bt.UImageMulti("m", []byte("a"), []byte("bc")))
	f.Add(bt.AndroidBoot(bt.Android{Version: 2, PageSize: 2048, Kernel: []byte("k"), DTB: []byte("d")}))
	f.Add(bt.AndroidBoot(bt.Android{Version: 4, Kernel: []byte("k"), Signature: []byte("s")}))
	f.Add(bt.VendorBootV4(2048, "", []bt.VendorRamdisk{{Type: 1, Data: []byte("r")}}, nil, []byte("b")))
	f.Add(bt.BzImage(4, "v", []byte("p")))
	f.Add(bt.GrubBoot(1, 0x80))
	f.Add(bt.GrubCore(1, 2, 3, nil))
	f.Add(bt.NTFSBoot())
	f.Add(bt.FAT12(64))
	f.Add(bt.UImage("k", 2, 1, []byte("kernel")))
	f.Add(bt.ElTorito([]bt.BootEntry{{Platform: 0, Sectors: 4, Data: []byte{0xFA, 0xEB}}, {Platform: 0xEF, Sectors: 1, Data: bt.FAT12(64)}}))
	zimage := make([]byte, 0x40)
	copy(zimage[0x24:], []byte{0x18, 0x28, 0x6F, 0x01, 0, 0, 0, 0, 0x40})
	f.Add(zimage)
	arm64 := make([]byte, 0x48)
	copy(arm64[0x38:], "ARM\x64")
	f.Add(arm64)
	f.Fuzz(func(t *testing.T, b []byte) {
		defer fuzzguard.Start(t.Name())()
		img, err := Detect(bytes.NewReader(b), int64(len(b)))
		if err != nil {
			return
		}
		for _, p := range img.Parts {
			if p.Off < 0 || p.Size < 0 || p.Off+p.Size > int64(len(b)) {
				t.Fatalf("%s: part %+v out of %d bytes", img.Format, p, len(b))
			}
		}
	})
}
