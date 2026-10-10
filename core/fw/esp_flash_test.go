package fw

import (
	"bytes"
	"crypto/md5"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type ptEntry struct {
	typ, sub  byte
	off, size uint32
	label     string
}

// partTable builds a partition table with its MD5 entry
func partTable(es ...ptEntry) []byte {
	var b bytes.Buffer
	for _, e := range es {
		b.Write([]byte{0xAA, 0x50, e.typ, e.sub})
		binary.Write(&b, binary.LittleEndian, e.off)
		binary.Write(&b, binary.LittleEndian, e.size)
		l := make([]byte, 16)
		copy(l, e.label)
		b.Write(l)
		b.Write(make([]byte, 4))
	}
	sum := md5.Sum(b.Bytes())
	b.Write([]byte{0xEB, 0xEB})
	b.Write(bytes.Repeat([]byte{0xFF}, 14))
	b.Write(sum[:])
	for b.Len() < 0xC00 {
		b.WriteByte(0xFF)
	}
	return b.Bytes()
}

// flashImage lays out a full flash image from flash address base
func flashImage(base int, boot int, bootImg, app []byte) []byte {
	out := bytes.Repeat([]byte{0xFF}, 0x10000-base+len(app))
	copy(out[boot-base:], bootImg)
	copy(out[0x8000-base:], partTable(
		ptEntry{1, 2, 0x9000, 0x6000, "nvs"},
		ptEntry{1, 1, 0xF000, 0x1000, "phy_init"},
		ptEntry{0, 0, 0x10000, 0x1F0000, "factory"},
		ptEntry{1, 0x81, 0x200000, 0x200000, "vfs"}))
	copy(out[0x10000-base:], app)
	return out
}

func segNames(im *Image) string {
	var s []string
	for _, x := range im.Segments {
		s = append(s, x.Name)
	}
	return strings.Join(s, ",")
}

func TestESP32FlashImage(t *testing.T) {
	boot := espImage(0, 0x400805B0, true,
		espSeg{0x3FFF0030, []byte("boot dram")},
		espSeg{0x40078000, bytes.Repeat([]byte{0x36, 0x41, 0}, 10)},
		espSeg{0x40080400, []byte{1, 2, 3, 4}})
	app := espImage(0, 0x40081000, true,
		espSeg{0x3F400020, []byte("rodata of the app")},
		espSeg{0x40080000, bytes.Repeat([]byte{0x36, 0x41, 0}, 0x500)},
		espSeg{0x400D0020, bytes.Repeat([]byte{0x36, 0x81, 0}, 40)})
	// From the bootloader at flash 0x1000, as MicroPython ships it
	data := flashImage(0x1000, 0x1000, boot, app)
	im := open(t, data, "esp32.bin")
	if im.Format != "ESP32 flash image" || im.Arch != "xtensa" || im.Entry != 0x40081000 {
		t.Fatalf("%s %s 0x%X", im.Format, im.Arch, im.Entry)
	}
	// The bootloader's IRAM at 0x40080400 is the app's: left out
	if n := segNames(im); n != "app drom,boot dram,boot iram,app iram,app irom" {
		t.Fatal(n)
	}
	noIssues(t, im)
	if !hasInfo(im, "Partition 2", "factory: app/factory at 0x10000") || !hasInfo(im, "Partition table", "MD5 OK") ||
		!hasInfo(im, "Bootloader SHA-256", "(OK)") || !hasInfo(im, "App SHA-256", "(OK)") ||
		!hasInfo(im, "Bootloader Segment 2", "not mapped") {
		t.Fatalf("%+v", im.Info)
	}
	// The app at flash 0x10000 is at 0xF000 in the file
	off, ok := im.AddrToOff(0x3F400020)
	if !ok || string(data[off:off+6]) != "rodata" || off < 0xF000 {
		t.Fatal(off, ok)
	}
	if a, ok := im.OffToAddr(off + 1); !ok || a != 0x3F400021 {
		t.Fatal(a, ok)
	}
	if g := best(t, im); g.Arch != "xtensa" || g.Entry != 0x40081000 {
		t.Fatalf("%+v", g)
	}

	// Broken app and bootloader: both found, both fixed
	bad := append([]byte(nil), data...)
	bad[off] ^= 1
	bad[0x22] ^= 1
	v := open(t, bad, "x").Verify()
	if len(v) != 4 || !strings.HasPrefix(v[0], "app: checksum") || !strings.HasPrefix(v[2], "bootloader: checksum") {
		t.Fatalf("%q", v)
	}
	fixed, err := FixESP32(bad)
	if err != nil || len(fixed) != len(bad) {
		t.Fatal(err, len(fixed), len(bad))
	}
	noIssues(t, open(t, fixed, "x"))

	// A dump from flash 0: the bootloader at 0x1000
	dump := append(bytes.Repeat([]byte{0xFF}, 0x1000), data...)
	im = open(t, dump, "dump.bin")
	if im.Format != "ESP32 flash image" || im.Entry != 0x40081000 || len(im.Segments) != 5 {
		t.Fatalf("%s %s", im.Format, segNames(im))
	}
	noIssues(t, im)
	if off, _ := im.AddrToOff(0x3F400020); string(dump[off:off+6]) != "rodata" {
		t.Fatal(off)
	}
}

func TestESP32C3FlashImage(t *testing.T) {
	boot := espImage(5, 0x403CC710, true,
		espSeg{0x3FCD5820, []byte("boot data")},
		espSeg{0x403CC710, bytes.Repeat([]byte{0x13, 0, 0, 0}, 16)})
	app := espImage(5, 0x4038025C, true,
		espSeg{0x3C150020, []byte("app rodata")},
		espSeg{0x3FC96600, []byte("app data")},
		espSeg{0x40380000, bytes.Repeat([]byte{0x13, 0, 0, 0}, 0x100)},
		espSeg{0x42000020, bytes.Repeat([]byte{0x13, 0, 0, 0}, 0x40)})
	data := flashImage(0, 0, boot, app)
	im := open(t, data, "c3.bin")
	if im.Format != "ESP32-C3 flash image" || im.Arch != "riscv32" || im.Entry != 0x4038025C {
		t.Fatalf("%s %s 0x%X", im.Format, im.Arch, im.Entry)
	}
	if n := segNames(im); n != "app drom,app dram,boot dram,app iram,boot iram,app irom" {
		t.Fatal(n)
	}
	noIssues(t, im)
	off, ok := im.AddrToOff(0x3C150020)
	if !ok || off < 0x10000 || string(data[off:off+10]) != "app rodata" {
		t.Fatal(off, ok)
	}
	if off, ok := im.AddrToOff(0x42000020); !ok || off < 0x10000 || data[off] != 0x13 {
		t.Fatal(off, ok)
	}
	// A plain app image larger than 0x8000 is not a flash image
	big := espImage(5, 0x40380000, true, espSeg{0x40380000, bytes.Repeat([]byte{0x13, 0, 0, 0}, 0x3000)})
	if im := open(t, big, "app.bin"); im.Format != "ESP32-C3 app image" {
		t.Fatal(im.Format)
	}
}

func TestESP8266Combined(t *testing.T) {
	first := esp8266Image()
	data := bytes.Repeat([]byte{0xFF}, 0x9000+0x300)
	copy(data, first)
	for i := 0x9000; i < len(data); i++ {
		data[i] = byte(i)
	}
	data[0x9000] = 0 // a zero at the start does not move it
	im := open(t, data, "esp8266.bin")
	if n := segNames(im); n != "dram,iram,irom0" {
		t.Fatal(n)
	}
	s := im.Segments[2]
	if s.Addr != 0x40209000 || s.Off != 0x9000 || s.Size != 0x300 || !s.Exec {
		t.Fatalf("%+v", s)
	}
	noIssues(t, im)
	if off, ok := im.AddrToOff(0x40209010); !ok || off != 0x9010 {
		t.Fatal(off, ok)
	}
}

// The MicroPython firmwares, when they are there
func TestESPMicroPython(t *testing.T) {
	dir := os.Getenv("ALTHEX_FW_SAMPLES")
	if dir == "" {
		t.Skip("ALTHEX_FW_SAMPLES is not set")
	}
	for _, c := range []struct {
		file string
		want uint64 // an address of the code in the flash
	}{
		{"ESP32_GENERIC-20241129-v1.24.1.bin", 0x400D0020},
		{"ESP32_GENERIC_C3-20241129-v1.24.1.bin", 0x42000020},
		{"ESP8266_GENERIC-20241129-v1.24.1.bin", 0x40209000},
	} {
		data, err := os.ReadFile(filepath.Join(dir, c.file))
		if err != nil {
			t.Skip(err)
		}
		im := open(t, data, c.file)
		for _, s := range im.Segments {
			t.Logf("%s: %-10s 0x%08X..0x%08X off 0x%X exec %v", c.file, s.Name, s.Addr, s.End(), s.Off, s.Exec)
		}
		i := im.find(im.Entry)
		if i < 0 || !im.Segments[i].Exec {
			t.Errorf("%s: entry 0x%X not in code", c.file, im.Entry)
		}
		if i := im.find(c.want); i < 0 || !im.Segments[i].Exec {
			t.Errorf("%s: no code at 0x%X", c.file, c.want)
		}
		if v := im.Verify(); len(v) > 0 {
			t.Errorf("%s: %q", c.file, v)
		}
		for j := 1; j < len(im.Segments); j++ {
			if im.Segments[j].Addr < im.Segments[j-1].End() {
				t.Errorf("%s: overlap at 0x%X", c.file, im.Segments[j].Addr)
			}
		}
	}
}
