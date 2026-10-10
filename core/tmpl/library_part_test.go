package tmpl

import (
	"encoding/binary"
	"hash/crc32"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"
)

// Partition tables (BOOT-10): mbr.bt, gpt.bt, apm.bt, bsdlabel.bt, sunvtoc.bt

// putMBRSector writes entries (status, type, lba, sectors) and 55 AA into
// the sector at lba
func putMBRSector(img []byte, lba int, entries ...[4]uint32) {
	s := img[lba*512:]
	for i, e := range entries {
		p := s[446+i*16:]
		p[0], p[4] = byte(e[0]), byte(e[1])
		binary.LittleEndian.PutUint32(p[8:], e[2])
		binary.LittleEndian.PutUint32(p[12:], e[3])
	}
	s[510], s[511] = 0x55, 0xAA
}

// partMBR: two primaries, an extended one with three logicals
func partMBR() []byte {
	img := make([]byte, 512*512)
	binary.LittleEndian.PutUint32(img[440:], 0xCAFEBABE)
	putMBRSector(img, 0, [4]uint32{0x80, 0x83, 8, 32}, [4]uint32{0, 0x07, 40, 32}, [4]uint32{0, 0x0F, 100, 400})
	putMBRSector(img, 100, [4]uint32{0, 0x83, 4, 50}, [4]uint32{0, 0x05, 100, 100})
	putMBRSector(img, 200, [4]uint32{0, 0x82, 4, 50}, [4]uint32{0, 0x05, 200, 100})
	putMBRSector(img, 300, [4]uint32{0, 0x0C, 4, 50})
	return img
}

func TestPartMBR(t *testing.T) {
	res := libraryRun(t, partMBR(), "disk.img", "mbr.bt")
	if v := value(t, res, "mbr.diskSignature"); !strings.Contains(strings.ToUpper(v), "CAFEBABE") {
		t.Fatalf("disk signature %s", v)
	}
	if v := value(t, res, "mbr.partition[0].type"); !strings.Contains(v, "LINUX") {
		t.Fatalf("type %s", v)
	}
	if v := value(t, res, "mbr.partition[0]"); v != "* LINUX, LBA 8, 32 sectors (16 KiB)" {
		t.Fatalf("entry %s", v)
	}
	if v := value(t, res, "mbr.partition[2].lba"); v != "100" {
		t.Fatalf("lba %s", v)
	}
	for i, want := range []string{"LINUX", "LINUX_SWAP", "FAT32_LBA"} {
		n := res.Root.Find("ebr[" + string(rune('0'+i)) + "]")
		if n == nil || n.Off != int64(100+i*100)*512 {
			t.Fatalf("ebr %d: %v", i, n)
		}
		if v := value(t, res, "ebr["+string(rune('0'+i))+"].logical.type"); !strings.HasPrefix(v, want) {
			t.Fatalf("ebr %d type %s", i, v)
		}
	}
	if res.Root.Find("ebr[3]") != nil {
		t.Fatal("a fourth EBR")
	}

	// A loop in the chain stops
	img := partMBR()
	putMBRSector(img, 300, [4]uint32{0, 0x0C, 4, 50}, [4]uint32{0, 0x05, 100, 100})
	res = libraryRun(t, img, "disk.img", "mbr.bt")
	if res.Root.Find("ebr[2]") == nil || res.Root.Find("ebr[3]") != nil {
		t.Fatal("the loop is not stopped")
	}
	if !strings.Contains(strings.Join(res.Output, "\n"), "goes back") {
		t.Fatalf("output %q", res.Output)
	}

	// A cut file
	res = libraryRun(t, partMBR()[:150*512], "disk.img", "mbr.bt")
	// One EBR: a field declared once, not a list (Find("ebr[1]") would
	// give its second member)
	if n := res.Root.Find("ebr"); n == nil || n.Kind == NodeList {
		t.Fatal("an EBR beyond the end")
	}
}

// partGPT: a disk of 256 sectors with two partitions
func partGPT() []byte {
	const n = 256
	le := binary.LittleEndian
	img := make([]byte, n*512)
	putMBRSector(img, 0, [4]uint32{0, 0xEE, 1, n - 1})
	ents := make([]byte, 128*128)
	guid := func(b []byte, d1 uint32, d2, d3 uint16, d4 string) {
		le.PutUint32(b, d1)
		le.PutUint16(b[4:], d2)
		le.PutUint16(b[6:], d3)
		copy(b[8:], d4)
	}
	guid(ents, 0xC12A7328, 0xF81F, 0x11D2, "\xBA\x4B\x00\xA0\xC9\x3E\xC9\x3B")
	guid(ents[16:], 1, 2, 3, "ABCDEFGH")
	le.PutUint64(ents[32:], 34)
	le.PutUint64(ents[40:], 99)
	for i, c := range utf16.Encode([]rune("EFI")) {
		le.PutUint16(ents[56+i*2:], c)
	}
	guid(ents[128:], 0x0FC63DAF, 0x8483, 0x4772, "\x8E\x79\x3D\x69\xD8\x47\x7D\xE4")
	le.PutUint64(ents[128+32:], 100)
	le.PutUint64(ents[128+40:], 200)
	for i, c := range utf16.Encode([]rune("root")) {
		le.PutUint16(ents[128+56+i*2:], c)
	}
	put := func(at, alt, entLBA uint64) {
		copy(img[entLBA*512:], ents)
		h := img[at*512:]
		copy(h, "EFI PART")
		le.PutUint32(h[8:], 0x00010000)
		le.PutUint32(h[12:], 92)
		le.PutUint64(h[24:], at)
		le.PutUint64(h[32:], alt)
		le.PutUint64(h[40:], 34)
		le.PutUint64(h[48:], n-34)
		le.PutUint64(h[72:], entLBA)
		le.PutUint32(h[80:], 128)
		le.PutUint32(h[84:], 128)
		le.PutUint32(h[88:], crc32.ChecksumIEEE(ents))
		le.PutUint32(h[16:], crc32.ChecksumIEEE(h[:92]))
	}
	put(1, n-1, 2)
	put(n-1, 1, n-33)
	return img
}

func TestPartGPT(t *testing.T) {
	img := partGPT()
	res := libraryRun(t, img, "disk.img", "gpt.bt")
	if v := value(t, res, "header.headerCRC32"); !strings.HasPrefix(strings.ToUpper(v), "0X") && v == "" {
		t.Fatalf("crc %s", v)
	}
	if c := res.Root.Find("header.headerCRC32").Comment; c != "OK" {
		t.Fatalf("header CRC comment %q", c)
	}
	if c := res.Root.Find("header.partitionEntryArrayCRC32").Comment; c != "OK" {
		t.Fatalf("entries CRC comment %q", c)
	}
	if v := value(t, res, "entries.entry[0]"); !strings.Contains(v, "EFI System") || !strings.Contains(v, "\"EFI\"") {
		t.Fatalf("entry 0: %s", v)
	}
	if v := value(t, res, "entries.entry[1]"); !strings.Contains(v, "Linux filesystem") || !strings.Contains(v, "LBA 100-200") {
		t.Fatalf("entry 1: %s", v)
	}
	if v := value(t, res, "entries.entry[2]"); v != "unused" {
		t.Fatalf("entry 2: %s", v)
	}
	if v := value(t, res, "entries.entry[1].typeGUID"); v != "0FC63DAF-8483-4772-8E79-3D69D8477DE4" {
		t.Fatalf("guid %s", v)
	}
	if n := res.Root.Find("backupHeader"); n == nil || n.Off != 255*512 {
		t.Fatal("backup header")
	}
	if n := res.Root.Find("backupEntries"); n == nil || n.Off != (256-33)*512 {
		t.Fatal("backup entries")
	}

	// Damaged CRCs are told in the comments
	img[512+16] ^= 1
	img[1024+56] = 'X'
	res = libraryRun(t, img, "disk.img", "gpt.bt")
	if c := res.Root.Find("header.headerCRC32").Comment; !strings.HasPrefix(c, "wrong") {
		t.Fatalf("header CRC comment %q", c)
	}
	if c := res.Root.Find("header.partitionEntryArrayCRC32").Comment; !strings.HasPrefix(c, "wrong") {
		t.Fatalf("entries CRC comment %q", c)
	}

	// A cut image: no backup, fewer entries
	res = libraryRun(t, partGPT()[:20*512], "disk.img", "gpt.bt")
	if res.Root.Find("backupHeader") != nil || res.Root.Find("entries.entry[100]") != nil || res.Root.Find("entries.entry[1]") == nil {
		t.Fatal("cut image")
	}
}

func TestPartAPM(t *testing.T) {
	be := binary.BigEndian
	img := make([]byte, 128*512)
	copy(img, "ER")
	be.PutUint16(img[2:], 512)
	be.PutUint32(img[4:], 128)
	for i, e := range []struct {
		start, n  uint32
		name, typ string
	}{{1, 3, "Apple", "Apple_partition_map"}, {4, 60, "Mac", "Apple_HFS"}, {64, 64, "", "Apple_Free"}} {
		p := img[(i+1)*512:]
		copy(p, "PM")
		be.PutUint32(p[4:], 3)
		be.PutUint32(p[8:], e.start)
		be.PutUint32(p[12:], e.n)
		copy(p[16:], e.name)
		copy(p[48:], e.typ)
	}
	res := libraryRun(t, img, "disk.img", "apm.bt")
	if v := value(t, res, "entry[1]"); !strings.Contains(v, "Apple_HFS") || !strings.Contains(v, "block 4") {
		t.Fatalf("entry 1: %s", v)
	}
	if res.Root.Find("entry[2]") == nil || res.Root.Find("entry[3]") != nil {
		t.Fatal("entries")
	}
}

func TestPartBSD(t *testing.T) {
	le := binary.LittleEndian
	img := make([]byte, 64*512)
	l := img[512:]
	le.PutUint32(l, 0x82564557)
	le.PutUint32(l[132:], 0x82564557)
	le.PutUint32(l[40:], 512)
	le.PutUint16(l[138:], 3)
	le.PutUint32(l[148:], 40)    // a: size
	le.PutUint32(l[148+4:], 16)  // a: offset
	l[148+12] = 7                // 4.2BSD
	le.PutUint32(l[148+32:], 64) // c: the whole disk
	var x uint16
	for i := 0; i < 148+48; i += 2 {
		x ^= le.Uint16(l[i:])
	}
	le.PutUint16(l[136:], x)
	putMBRSector(img, 0) // the boot1 of FreeBSD ends with 55 AA too
	res := libraryRun(t, img, "disk.img", "bsdlabel.bt")
	if v := value(t, res, "label.partition[0]"); !strings.Contains(v, "FS_BSDFFS") || !strings.Contains(v, "sector 16") {
		t.Fatalf("a: %s", v)
	}
	if c := res.Root.Find("label.checksum").Comment; c != "OK" {
		t.Fatalf("checksum %q", c)
	}
}

func TestPartVTOC(t *testing.T) {
	be := binary.BigEndian
	img := make([]byte, 128*512)
	copy(img, "test disk")
	be.PutUint32(img[128:], 1)
	be.PutUint16(img[140:], 8)
	be.PutUint32(img[188:], 0x600DDEEE)
	be.PutUint16(img[436:], 2)
	be.PutUint16(img[438:], 8)
	be.PutUint16(img[142:], 2)
	be.PutUint32(img[444:], 1)
	be.PutUint32(img[448:], 32)
	be.PutUint16(img[508:], 0xDABE)
	var x uint16
	for i := 0; i < 510; i += 2 {
		x ^= be.Uint16(img[i:])
	}
	be.PutUint16(img[510:], x)
	res := libraryRun(t, img, "disk.img", "sunvtoc.bt")
	if v := value(t, res, "label.slice[0]"); v != "sector 16, 32 sectors" {
		t.Fatalf("slice 0: %s", v)
	}
	if v := value(t, res, "label.vtoc.part[0].tag"); !strings.Contains(v, "V_ROOT") {
		t.Fatalf("tag %s", v)
	}
	if c := res.Root.Find("label.checksum").Comment; c != "OK" {
		t.Fatalf("checksum %q", c)
	}
}

// Damaged tables do not stop the templates
func TestPartTemplatesDamaged(t *testing.T) {
	for _, name := range []string{"mbr.bt", "gpt.bt", "apm.bt", "bsdlabel.bt", "sunvtoc.bt"} {
		var tm *Template
		for _, x := range Library() {
			if x.File == name {
				tm = x
			}
		}
		p, err := tm.Program()
		if err != nil {
			t.Fatal(err)
		}
		for _, img := range [][]byte{partMBR(), partGPT(), partGPT()[:1100], make([]byte, 600), make([]byte, 10)} {
			for i := 0; i < len(img) && i < 4096; i += 7 {
				img[i] ^= byte(i)
			}
			res := p.Run(t.Context(), Env{Data: &mem{b: img}})
			if res.Err != nil && !strings.Contains(res.Err.Error(), "beyond") && !strings.Contains(res.Err.Error(), "end of") {
				t.Errorf("%s: %v", name, res.Err)
			}
		}
	}
}

// Tables made by sfdisk of util-linux, when it is installed
func TestPartRealImages(t *testing.T) {
	sfdisk := ""
	for _, p := range []string{"sfdisk", "/sbin/sfdisk", "/usr/sbin/sfdisk"} {
		if q, err := exec.LookPath(p); err == nil {
			sfdisk = q
			break
		}
	}
	if sfdisk == "" {
		t.Skip("sfdisk is not installed")
	}
	mkImage := func(layout string) []byte {
		img := filepath.Join(t.TempDir(), "disk.img")
		if err := os.WriteFile(img, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Truncate(img, 16<<20); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(sfdisk, "-q", img)
		cmd.Stdin = strings.NewReader(layout)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("sfdisk: %v %s", err, out)
		}
		b, err := os.ReadFile(img)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}

	res := libraryRun(t, mkImage("label: gpt\nsize=4M, type=U, name=boot\ntype=L\n"), "disk.img", "gpt.bt")
	for _, f := range []string{"header.headerCRC32", "header.partitionEntryArrayCRC32", "backupHeader.headerCRC32", "backupHeader.partitionEntryArrayCRC32"} {
		if n := res.Root.Find(f); n == nil || n.Comment != "OK" {
			t.Fatalf("%s: %v", f, n)
		}
	}
	if v := value(t, res, "entries.entry[0]"); !strings.Contains(v, "\"boot\" EFI System, LBA 2048-10239") {
		t.Fatalf("entry 0: %s", v)
	}

	res = libraryRun(t, mkImage("label: dos\nsize=4M, type=83\ntype=5\nsize=2M, type=82\ntype=7\n"), "disk.img", "mbr.bt")
	if v := value(t, res, "ebr[1].logical.type"); !strings.HasPrefix(v, "NTFS_EXFAT") {
		t.Fatalf("logical 6: %s", v)
	}
}
