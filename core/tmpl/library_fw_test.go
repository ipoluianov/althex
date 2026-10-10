package tmpl

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"hash/crc32"
	"strings"
	"testing"
	"time"
)

// Firmware images (FW-1): uf2.bt, dfuse.bt, esp32-image.bt

func fwUF2Block(flags, addr, no, count, fam uint32, data []byte) []byte {
	b := make([]byte, 512)
	le := binary.LittleEndian
	le.PutUint32(b, 0x0A324655)
	le.PutUint32(b[4:], 0x9E5D5157)
	le.PutUint32(b[8:], flags)
	le.PutUint32(b[12:], addr)
	le.PutUint32(b[16:], uint32(len(data)))
	le.PutUint32(b[20:], no)
	le.PutUint32(b[24:], count)
	le.PutUint32(b[28:], fam)
	copy(b[32:], data)
	le.PutUint32(b[508:], 0x0AB16F30)
	return b
}

func TestFwUF2(t *testing.T) {
	var data []byte
	data = append(data, fwUF2Block(0x2000, 0x10000000, 0, 2, 0xE48BFF56, bytes.Repeat([]byte{1}, 256))...)
	data = append(data, fwUF2Block(0x2000, 0x10000100, 1, 2, 0xE48BFF56, bytes.Repeat([]byte{2}, 256))...)
	data = append(data, fwUF2Block(0x2000, 0x00002000, 0, 1, 0x68ED2B88, []byte{3, 4, 5})...)
	res := libraryRun(t, data, "x.uf2", "uf2.bt")
	if v := value(t, res, "block[1]"); v != "2/2: 256 bytes at 0x10000100, RP2040" {
		t.Fatalf("block 1: %s", v)
	}
	if v := value(t, res, "block[2].familyID"); !strings.Contains(v, "SAMD21") {
		t.Fatalf("family %s", v)
	}
	if n := res.Root.Find("block[2].padding"); n == nil || n.Size != 473 {
		t.Fatalf("padding %v", n)
	}
	if v := res.Root.Find("block[0].flags").Comment; v != "family ID present" {
		t.Fatalf("flags %q", v)
	}
	if res.Root.Find("block[3]") != nil {
		t.Fatal("a fourth block")
	}
}

func fwDfuSe() []byte {
	le := binary.LittleEndian
	target := func(alt byte, name string, elems ...[]byte) []byte {
		var el []byte
		for i, e := range elems {
			h := make([]byte, 8)
			le.PutUint32(h, 0x08000000+uint32(i)*0x4000+uint32(alt)*0x10000000)
			le.PutUint32(h[4:], uint32(len(e)))
			el = append(append(el, h...), e...)
		}
		h := make([]byte, 274)
		copy(h, "Target")
		h[6] = alt
		le.PutUint32(h[7:], 1)
		copy(h[11:], name)
		le.PutUint32(h[266:], uint32(len(el)))
		le.PutUint32(h[270:], uint32(len(elems)))
		return append(h, el...)
	}
	body := append(target(0, "Internal Flash", []byte("first element"), []byte("second")), target(1, "Option Bytes", []byte{0xAA, 0x55})...)
	f := []byte("DfuSe\x01")
	f = le.AppendUint32(f, uint32(11+len(body)))
	f = append(f, 2)
	f = append(f, body...)
	f = append(f, 0xFF, 0xFF, 0x11, 0xDF, 0x83, 0x04, 0x1A, 0x01, 'U', 'F', 'D', 16)
	return le.AppendUint32(f, ^crc32.ChecksumIEEE(f))
}

func TestFwDfuSe(t *testing.T) {
	res := libraryRun(t, fwDfuSe(), "fw.dfu", "dfuse.bt")
	if v := value(t, res, "prefix.bTargets"); v != "2" {
		t.Fatalf("targets %s", v)
	}
	if v := value(t, res, "target[0]"); v != `alternate setting 0, "Internal Flash", 2 elements` {
		t.Fatalf("target 0: %s", v)
	}
	if v := value(t, res, "target[0].element[1]"); v != "6 bytes at 0x08004000" {
		t.Fatalf("element: %s", v)
	}
	if v := value(t, res, "target[1].element.dwElementAddress"); v != "0x18000000" {
		t.Fatalf("address: %s", v)
	}
	if v := value(t, res, "suffix.idVendor"); v != "0x0483" {
		t.Fatalf("vendor %s", v)
	}
	if c := res.Root.Find("suffix.dwCRC").Comment; c != "OK" {
		t.Fatalf("crc %q", c)
	}
	bad := fwDfuSe()
	bad[20] ^= 1
	res = libraryRun(t, bad, "fw.dfu", "dfuse.bt")
	if c := res.Root.Find("suffix.dwCRC").Comment; !strings.HasPrefix(c, "should be") {
		t.Fatalf("crc %q", c)
	}
}

// fwESP32 is an ESP32-C3 image: 3 segments, the checksum and the SHA-256
func fwESP32(segData int) []byte {
	le := binary.LittleEndian
	b := []byte{0xE9, 3, 2, 0x20}
	b = le.AppendUint32(b, 0x40380400)
	ext := make([]byte, 16)
	ext[0] = 0xEE
	le.PutUint16(ext[4:], 5)
	ext[15] = 1
	b = append(b, ext...)
	sum := byte(0xEF)
	for i, a := range []uint32{0x3C000020, 0x3FC88000, 0x40380000} {
		d := bytes.Repeat([]byte{byte(i + 1), 0x13}, segData/2+i)
		b = le.AppendUint32(le.AppendUint32(b, a), uint32(len(d)))
		b = append(b, d...)
		for _, c := range d {
			sum ^= c
		}
	}
	for len(b)%16 != 15 {
		b = append(b, 0)
	}
	b = append(b, sum)
	h := sha256.Sum256(b)
	return append(b, h[:]...)
}

func TestFwESP32(t *testing.T) {
	data := fwESP32(100)
	res := libraryRun(t, data, "app.bin", "esp32-image.bt")
	if v := value(t, res, "extended_header.chip_id"); !strings.HasPrefix(v, "ESP32_C3") {
		t.Fatalf("chip %s", v)
	}
	if v := value(t, res, "segment[2]"); v != "104 bytes at 0x40380000" {
		t.Fatalf("segment %s", v)
	}
	if c := res.Root.Find("checksum").Comment; c != "OK" {
		t.Fatalf("checksum %q", c)
	}
	if c := res.Root.Find("sha256").Comment; c != "OK" {
		t.Fatalf("sha256 %q", c)
	}
	if res.Root.Find("rest") != nil {
		t.Fatal("data after the hash")
	}
	// A byte of a segment changed: both are wrong
	data[30] ^= 0xFF
	res = libraryRun(t, data, "app.bin", "esp32-image.bt")
	if c := res.Root.Find("checksum").Comment; !strings.HasPrefix(c, "should be") {
		t.Fatalf("checksum %q", c)
	}
	if c := res.Root.Find("sha256").Comment; !strings.HasPrefix(c, "should be") {
		t.Fatalf("sha256 %q", c)
	}
	// An ESP8266 image: no extended header
	e := []byte{0xE9, 1, 0, 0x20, 0, 0x10, 0x10, 0x40, 0, 0, 0x10, 0x40, 4, 0, 0, 0, 1, 2, 3, 4}
	for len(e)%16 != 15 {
		e = append(e, 0)
	}
	e = append(e, 0xEF^1^2^3^4)
	res = libraryRun(t, e, "esp8266.bin", "esp32-image.bt")
	if res.Root.Find("extended_header") != nil || value(t, res, "segment.load_addr") != "0x40100000" ||
		res.Root.Find("checksum").Comment != "OK" {
		t.Fatal("ESP8266 image")
	}
}

// A large image: the checksum loop must stay in time
func TestFwESP32Large(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	start := time.Now()
	res := libraryRun(t, fwESP32(1<<20), "app.bin", "esp32-image.bt")
	if c := res.Root.Find("checksum").Comment; c != "OK" {
		t.Fatalf("checksum %q", c)
	}
	t.Logf("3 MiB of segments in %v", time.Since(start))
}
