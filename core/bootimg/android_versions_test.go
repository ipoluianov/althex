package bootimg

import (
	"bytes"
	"encoding/binary"
	"testing"

	bt "github.com/ipoluianov/althex/core/bootimg/bootimgtest"
)

// Android boot.img v1 and v3 and vendor_boot.img v3: the versions between
// those of bootimg_test.go

// vendorBootV3 makes vendor_boot.img v3 (bootimg.h vendor_boot_img_hdr_v3):
// the header of 2112 bytes, the vendor ramdisk and the DTB, each from a
// page boundary
func vendorBootV3(page int, cmdline string, ramdisk, dtb []byte) []byte {
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
		s = append([]byte(nil), s...)
		for len(s)%page != 0 {
			s = append(s, 0)
		}
		out = append(out, s...)
	}
	return out
}

func noPart(t *testing.T, img *Image, name string) {
	t.Helper()
	for _, p := range img.Parts {
		if p.Name == name {
			t.Errorf("%s: unexpected part %q", img.Format, name)
		}
	}
}

func noInfo(t *testing.T, img *Image, key string) {
	t.Helper()
	for _, kv := range img.Info {
		if kv.Key == key {
			t.Errorf("%s: unexpected info %s = %q", img.Format, key, kv.Value)
		}
	}
}

// detectCuts runs Detect over cuts of the data: no panics, and every part
// in the data
func detectCuts(t *testing.T, data []byte) {
	t.Helper()
	for n := 0; n <= len(data); n += 1 + n/32 {
		img, err := Detect(bytes.NewReader(data[:n]), int64(n))
		if err != nil {
			continue
		}
		for _, p := range img.Parts {
			if p.Off < 0 || p.Size < 0 || p.Off+p.Size > int64(n) {
				t.Fatalf("cut at %d: %s %q at %d size %d", n, img.Format, p.Name, p.Off, p.Size)
			}
		}
	}
}

func TestAndroidV1(t *testing.T) {
	kernel := gz(t, bytes.Repeat([]byte("kernel v1 "), 400))
	dtbo := append([]byte{0xD7, 0xB7, 0xAB, 0x1E}, make([]byte, 496)...)
	data := bt.AndroidBoot(bt.Android{Version: 1, PageSize: 2048, Cmdline: "console=ttyMSM0",
		OSVersion: bt.AndroidOS(10, 0, 0, 2020, 6), Kernel: kernel, Ramdisk: []byte("rd1"),
		RecoveryDTBO: dtbo})
	img := detect(t, data)
	if img.Format != "Android boot (v1)" {
		t.Fatal(img.Format)
	}
	info(t, img, "header version", "1")
	info(t, img, "page size", "2048")
	info(t, img, "header size", "1648")
	info(t, img, "cmdline", "console=ttyMSM0")
	info(t, img, "OS version", "10.0.0")
	info(t, img, "OS patch level", "2020-06")
	info(t, img, "name", "sample")
	noInfo(t, img, "dtb address")
	if k := part(t, img, "kernel", 2048, int64(len(kernel))); k.Compression != "gzip" {
		t.Errorf("kernel compression %q", k.Compression)
	}
	rd := 2048 + alignUp(int64(len(kernel)), 2048)
	part(t, img, "ramdisk", rd, 3)
	noPart(t, img, "second")
	if p := part(t, img, "recovery_dtbo", rd+2048, 500); p.Kind != "dtb" {
		t.Errorf("recovery_dtbo kind %q", p.Kind)
	}
	noPart(t, img, "dtb")
	detectCuts(t, data)

	// Without a recovery DTBO the offset is 0: no part
	data = bt.AndroidBoot(bt.Android{Version: 1, PageSize: 4096, Kernel: make([]byte, 100), Ramdisk: []byte("r")})
	img = detect(t, data)
	if img.Format != "Android boot (v1)" {
		t.Fatal(img.Format)
	}
	part(t, img, "kernel", 4096, 100)
	part(t, img, "ramdisk", 8192, 1)
	noPart(t, img, "recovery_dtbo")
	if len(img.Parts) != 2 {
		t.Errorf("parts %+v", img.Parts)
	}
}

func TestAndroidV3(t *testing.T) {
	ramdisk := []byte{0x02, 0x21, 0x4C, 0x18, 9, 9, 9}
	data := bt.AndroidBoot(bt.Android{Version: 3, Cmdline: "androidboot.v=3",
		OSVersion: bt.AndroidOS(12, 1, 0, 2022, 1), Kernel: make([]byte, 5000), Ramdisk: ramdisk})
	img := detect(t, data)
	if img.Format != "Android boot (v3)" {
		t.Fatal(img.Format)
	}
	info(t, img, "header version", "3")
	info(t, img, "header size", "1580")
	info(t, img, "cmdline", "androidboot.v=3")
	info(t, img, "OS version", "12.1.0")
	info(t, img, "OS patch level", "2022-01")
	noInfo(t, img, "page size")
	noInfo(t, img, "kernel address")
	part(t, img, "kernel", 4096, 5000)
	if p := part(t, img, "ramdisk", 3*4096, 7); p.Compression != "lz4" {
		t.Errorf("ramdisk compression %q", p.Compression)
	}
	noPart(t, img, "boot signature")
	noPart(t, img, "second")
	if len(img.Parts) != 2 {
		t.Errorf("parts %+v", img.Parts)
	}
	detectCuts(t, data)

	// The ramdisk cut off: only the kernel
	img = detect(t, data[:3*4096+3])
	part(t, img, "kernel", 4096, 5000)
	noPart(t, img, "ramdisk")
}

func TestVendorBootV3(t *testing.T) {
	ramdisk := gz(t, bytes.Repeat([]byte("vendor ramdisk "), 300))
	dtb := append([]byte{0xD0, 0x0D, 0xFE, 0xED}, make([]byte, 60)...)
	data := vendorBootV3(2048, "androidboot.vendor=1", ramdisk, dtb)
	img := detect(t, data)
	if img.Format != "Android vendor_boot (v3)" {
		t.Fatal(img.Format)
	}
	info(t, img, "header version", "3")
	info(t, img, "page size", "2048")
	info(t, img, "header size", "2112")
	info(t, img, "name", "vendor3")
	info(t, img, "cmdline", "androidboot.vendor=1")
	info(t, img, "kernel address", "0x10008000")
	info(t, img, "tags address", "0x10000100")
	info(t, img, "dtb address", "0x11F00000")
	noInfo(t, img, "bootconfig")
	// The header of 2112 bytes takes two pages of 2048
	rdOff := int64(4096)
	if p := part(t, img, "vendor_ramdisk", rdOff, int64(len(ramdisk))); p.Compression != "gzip" || p.Kind != "ramdisk" {
		t.Errorf("vendor_ramdisk %+v", p)
	}
	part(t, img, "dtb", rdOff+alignUp(int64(len(ramdisk)), 2048), int64(len(dtb)))
	noPart(t, img, "vendor ramdisk table")
	noPart(t, img, "bootconfig")
	if len(img.Parts) != 2 {
		t.Errorf("parts %+v", img.Parts)
	}
	detectCuts(t, data)

	// Pages of 4096: the header takes one
	data = vendorBootV3(4096, "", []byte("rd"), nil)
	img = detect(t, data)
	part(t, img, "vendor_ramdisk", 4096, 2)
	noPart(t, img, "dtb")

	// v3 has no ramdisk table: whatever is where v4 has its sizes is not read
	data = vendorBootV3(4096, "", []byte("rd"), nil)
	binary.LittleEndian.PutUint32(data[2112:], 108)
	binary.LittleEndian.PutUint32(data[2116:], 1)
	binary.LittleEndian.PutUint32(data[2120:], 108)
	binary.LittleEndian.PutUint32(data[2124:], 10)
	img = detect(t, data)
	noPart(t, img, "vendor ramdisk table")
	noPart(t, img, "bootconfig")
	part(t, img, "vendor_ramdisk", 4096, 2)
}
