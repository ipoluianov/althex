package tmpl

import (
	"encoding/binary"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"
)

// ---- FAT ----

// makeFAT12 makes the start of a 1.44 MB floppy: the boot sector, the two
// FATs and the root directory with a label, a long name and a file
func makeFAT12() []byte {
	b := make([]byte, 33*512+2*512) // up to the first two clusters
	copy(b, []byte{0xEB, 0x3C, 0x90})
	copy(b[3:], "MSWIN4.1")
	binary.LittleEndian.PutUint16(b[11:], 512)
	b[13] = 1
	binary.LittleEndian.PutUint16(b[14:], 1)
	b[16] = 2
	binary.LittleEndian.PutUint16(b[17:], 224)
	binary.LittleEndian.PutUint16(b[19:], 2880)
	b[21] = 0xF0
	binary.LittleEndian.PutUint16(b[22:], 9)
	b[38] = 0x29
	copy(b[43:], "FLOPPY     FAT12   ")
	b[510], b[511] = 0x55, 0xAA
	for _, fat := range []int{512, 10 * 512} {
		copy(b[fat:], []byte{0xF0, 0xFF, 0xFF, 0xFF, 0x0F}) // cluster 2: the end
	}
	root := b[19*512:]
	copy(root, "FLOPPY     ")
	root[11] = 0x08
	// "Hello World.txt": one long name entry and the 8.3 alias
	short := []byte("HELLOW~1TXT")
	var sum byte
	for _, c := range short {
		sum = (sum&1)<<7 + sum>>1 + c
	}
	lfn := root[32:64]
	lfn[0] = 0x41
	lfn[11] = 0x0F
	lfn[13] = sum
	u := utf16.Encode([]rune("Hello World.t"))
	for k, o := range []int{1, 3, 5, 7, 9, 14, 16, 18, 20, 22, 24, 28, 30} {
		binary.LittleEndian.PutUint16(lfn[o:], u[k])
	}
	d := root[64:96]
	copy(d, short)
	d[11] = 0x20
	binary.LittleEndian.PutUint16(d[22:], 12<<11|34<<5|28)
	binary.LittleEndian.PutUint16(d[24:], 44<<9|7<<5|15)
	binary.LittleEndian.PutUint16(d[26:], 2)
	binary.LittleEndian.PutUint32(d[28:], 5)
	copy(b[33*512:], "hello")
	return b
}

// makeFAT32 makes the reserved sectors of a FAT32 volume with FSInfo and
// the backup boot sector, a part of the FAT and the root cluster
func makeFAT32() []byte {
	const rsvd, fatSecs = 32, 520
	b := make([]byte, (rsvd+2*fatSecs+1)*512)
	copy(b, []byte{0xEB, 0x58, 0x90})
	copy(b[3:], "mkfs.fat")
	binary.LittleEndian.PutUint16(b[11:], 512)
	b[13] = 1
	binary.LittleEndian.PutUint16(b[14:], rsvd)
	b[16] = 2
	b[21] = 0xF8
	binary.LittleEndian.PutUint32(b[32:], 66600)
	binary.LittleEndian.PutUint32(b[36:], fatSecs)
	binary.LittleEndian.PutUint32(b[44:], 2)
	binary.LittleEndian.PutUint16(b[48:], 1)
	binary.LittleEndian.PutUint16(b[50:], 6)
	b[66] = 0x29
	copy(b[71:], "BIG VOLUME FAT32   ")
	b[510], b[511] = 0x55, 0xAA
	fi := b[512:]
	binary.LittleEndian.PutUint32(fi, 0x41615252)
	binary.LittleEndian.PutUint32(fi[484:], 0x61417272)
	binary.LittleEndian.PutUint32(fi[488:], 65000)
	binary.LittleEndian.PutUint32(fi[492:], 3)
	binary.LittleEndian.PutUint32(fi[508:], 0xAA550000)
	copy(b[6*512:], b[:1024])
	fat := b[rsvd*512:]
	binary.LittleEndian.PutUint32(fat, 0x0FFFFFF8)
	binary.LittleEndian.PutUint32(fat[4:], 0x0FFFFFFF)
	binary.LittleEndian.PutUint32(fat[8:], 0x0FFFFFFF)
	root := b[(rsvd+2*fatSecs)*512:]
	copy(root, "BIG VOLUME ")
	root[11] = 0x08
	copy(root[32:], "SUBDIR     ")
	root[32+11] = 0x10
	return b
}

func TestFATTemplate(t *testing.T) {
	data := makeFAT12()
	res := libraryRun(t, data, "floppy.img", "fat.bt")
	if v := value(t, res, "bootSector.bpb.bytesPerSec"); v != "512" {
		t.Fatalf("bytesPerSec %s", v)
	}
	if v := value(t, res, "bootSector.ebpb.volLab"); !strings.Contains(v, "FLOPPY") {
		t.Fatalf("volLab %s", v)
	}
	if v := value(t, res, "rootDirectory.entry[1]"); !strings.Contains(v, "Hello World.t") {
		t.Fatalf("long name entry %q", v)
	}
	if v := value(t, res, "rootDirectory.entry[2]"); !strings.Contains(v, "HELLOW~1.TXT 5 bytes") {
		t.Fatalf("8.3 entry %q", v)
	}
	if v := value(t, res, "rootDirectory.entry[2].fstClusLO"); v != "2" {
		t.Fatalf("cluster %s", v)
	}
	if n := res.Root.Find("rootDirectory.entry[3]"); n == nil {
		t.Fatal("the zero entry that ends the directory is not shown")
	}
	if n := res.Root.Find("rootDirectory.entry[4]"); n != nil {
		t.Fatal("entries after the end")
	}
	checkTruncated(t, "fat.bt", data)

	data = makeFAT32()
	res = libraryRun(t, data, "disk.img", "fat.bt")
	if v := value(t, res, "bootSector.ebpb.rootClus"); v != "2" {
		t.Fatalf("rootClus %s", v)
	}
	if v := value(t, res, "fsInfo.freeCount"); v != "65000" {
		t.Fatalf("freeCount %s", v)
	}
	if v := value(t, res, "backupBootSector.ebpb.fsInfo"); v != "1" {
		t.Fatalf("backup %s", v)
	}
	if v := value(t, res, "rootDirectory.entry[1]"); v != "SUBDIR/" {
		t.Fatalf("root %q", v)
	}
	if n := res.Root.Find("fat"); n == nil || n.Size != 520*512 {
		t.Fatal("fat")
	}
	checkTruncated(t, "fat.bt", data)
}

// ---- exFAT ----

// makeExFAT makes a small exFAT volume: 512-byte sectors and clusters, the
// boot region with its checksum, the FAT and a root directory with the
// bitmap, the up-case table, a label and one file
func makeExFAT() []byte {
	const bps, fatOff, fatLen, heap, clusters = 512, 24, 1, 32, 16
	b := make([]byte, (heap+clusters)*bps)
	copy(b, []byte{0xEB, 0x76, 0x90})
	copy(b[3:], "EXFAT   ")
	binary.LittleEndian.PutUint64(b[0x48:], uint64(len(b)/bps))
	binary.LittleEndian.PutUint32(b[0x50:], fatOff)
	binary.LittleEndian.PutUint32(b[0x54:], fatLen)
	binary.LittleEndian.PutUint32(b[0x58:], heap)
	binary.LittleEndian.PutUint32(b[0x5C:], clusters)
	binary.LittleEndian.PutUint32(b[0x60:], 4)
	binary.LittleEndian.PutUint16(b[0x68:], 0x0100)
	b[0x6C], b[0x6D], b[0x6E], b[0x70] = 9, 0, 1, 0xFF
	b[510], b[511] = 0x55, 0xAA
	var sum uint32
	for i := 0; i < 11*bps; i++ {
		if i != 106 && i != 107 && i != 112 {
			sum = (sum<<31 | sum>>1) + uint32(b[i])
		}
	}
	for i := 11 * bps; i < 12*bps; i += 4 {
		binary.LittleEndian.PutUint32(b[i:], sum)
	}
	copy(b[12*bps:], b[:12*bps])
	fat := b[fatOff*bps:]
	for c, v := range []uint32{0xFFFFFFF8, 0xFFFFFFFF, 0xFFFFFFFF, 0xFFFFFFFF, 0xFFFFFFFF} {
		binary.LittleEndian.PutUint32(fat[4*c:], v)
	}
	root := b[(heap+2)*bps:]
	root[0] = 0x83
	label := utf16.Encode([]rune("Volume"))
	root[1] = byte(len(label))
	for i, u := range label {
		binary.LittleEndian.PutUint16(root[2+2*i:], u)
	}
	root[32] = 0x81
	binary.LittleEndian.PutUint32(root[32+20:], 2)
	binary.LittleEndian.PutUint64(root[32+24:], 2)
	root[64] = 0x82
	binary.LittleEndian.PutUint32(root[64+20:], 3)
	binary.LittleEndian.PutUint64(root[64+24:], 10)
	f := root[96:]
	f[0], f[1] = 0x85, 2
	binary.LittleEndian.PutUint16(f[4:], 0x20)
	binary.LittleEndian.PutUint32(f[12:], (2024-1980)<<25|7<<21|15<<16|12<<11)
	f[23] = 0x88
	s := f[32:]
	s[0], s[1], s[3] = 0xC0, 3, 8
	binary.LittleEndian.PutUint64(s[8:], 5)
	binary.LittleEndian.PutUint32(s[20:], 5)
	binary.LittleEndian.PutUint64(s[24:], 5)
	n := f[64:]
	n[0] = 0xC1
	for i, u := range utf16.Encode([]rune("file.txt")) {
		binary.LittleEndian.PutUint16(n[2+2*i:], u)
	}
	copy(b[(heap+3)*bps:], "hello")
	return b
}

func TestExFATTemplate(t *testing.T) {
	data := makeExFAT()
	res := libraryRun(t, data, "x.img", "exfat.bt")
	if v := value(t, res, "bootSector.clusterCount"); v != "16" {
		t.Fatalf("clusterCount %s", v)
	}
	if len(res.Warnings) > 0 {
		t.Fatalf("warnings %v", res.Warnings)
	}
	want := []string{"volume label: Volume", "allocation bitmap: cluster 2, 2 bytes", "up-case table: cluster 3, 10 bytes",
		"file: 2 secondary entries, 2024-07-15 12:00:00", "stream: cluster 5, 5 bytes, contiguous", "name: file.txt", "unused (the end)"}
	for i, w := range want {
		if v := value(t, res, "rootDirectory.entry["+string(rune('0'+i))+"]"); v != w {
			t.Errorf("entry %d: %q, want %q", i, v, w)
		}
	}
	if v := value(t, res, "rootDirectory.entry[3].lastModifiedUtcOffset"); !strings.Contains(res.Root.Find("rootDirectory.entry[3].lastModifiedUtcOffset").Comment, "UTC+02:00") {
		t.Errorf("utc offset %s", v)
	}
	checkTruncated(t, "exfat.bt", data)
}

// ---- ISO 9660 ----

func both32(b []byte, v uint32) {
	binary.LittleEndian.PutUint32(b, v)
	binary.BigEndian.PutUint32(b[4:], v)
}

// isoRecord makes a directory record with a system use area
func isoRecord(name string, lba, size uint32, flags byte, su []byte) []byte {
	n := len(name)
	l := 33 + n + (1 - n%2) + len(su)
	l += l % 2
	r := make([]byte, l)
	r[0] = byte(l)
	both32(r[2:], lba)
	both32(r[10:], size)
	copy(r[18:], []byte{124, 5, 6, 7, 8, 9, 4})
	r[25] = flags
	r[28], r[31] = 1, 1
	r[32] = byte(n)
	copy(r[33:], name)
	copy(r[33+n+(1-n%2):], su)
	return r
}

// makeISO makes an image with a boot record of El Torito, a primary and a
// Joliet descriptor, the terminator, the boot catalog and a root directory
// with Rock Ridge entries
func makeISO() []byte {
	const S = 2048
	b := make([]byte, 24*S)
	vd := func(n int, typ byte) []byte {
		v := b[n*S : (n+1)*S]
		v[0] = typ
		copy(v[1:], "CD001\x01")
		return v
	}
	br := vd(16, 0)
	copy(br[7:], "EL TORITO SPECIFICATION")
	binary.LittleEndian.PutUint32(br[0x47:], 20)
	for i, typ := range []byte{1, 2} {
		p := vd(17+i, typ)
		copy(p[8:], strings.Repeat(" ", 64))
		copy(p[40:], "TEST_VOLUME")
		both32(p[80:], 24)
		if typ == 2 {
			copy(p[88:], "%/E")
		}
		binary.LittleEndian.PutUint16(p[128:], S)
		binary.BigEndian.PutUint16(p[130:], S)
		copy(p[156:], isoRecord("\x00", 21, S, 2, nil))
		copy(p[813:], "2024050607080900")
		p[813+16] = 4
		p[881] = 1
	}
	vd(19, 255)
	cat := b[20*S:]
	cat[0] = 1
	copy(cat[4:], "TEST")
	cat[30], cat[31] = 0x55, 0xAA
	cat[32] = 0x88
	binary.LittleEndian.PutUint16(cat[32+6:], 4)
	binary.LittleEndian.PutUint32(cat[32+8:], 22)
	su := []byte{'S', 'P', 7, 1, 0xBE, 0xEF, 0}
	px := make([]byte, 36)
	copy(px, "PX")
	px[2], px[3] = 36, 1
	both32(px[4:], 0o40755)
	root := isoRecord("\x00", 21, S, 2, append(su, px...))
	root = append(root, isoRecord("\x01", 21, S, 2, nil)...)
	nm := append([]byte{'N', 'M', 5 + 8, 1, 0}, "file.txt"...)
	root = append(root, isoRecord("FILE.TXT;1", 23, 5, 0, append(nm, 'S', 'T', 4, 1))...)
	copy(b[21*S:], root)
	copy(b[23*S:], "hello")
	return b
}

func TestISO9660Template(t *testing.T) {
	data := makeISO()
	res := libraryRun(t, data, "x.iso", "iso9660.bt")
	if v := value(t, res, "volumeDescriptor[0]"); v != "BOOT_RECORD" {
		t.Fatalf("vd 0: %s", v)
	}
	if v := value(t, res, "volumeDescriptor[0].bootCatalog"); v != "20" {
		t.Fatalf("catalog %s", v)
	}
	if v := value(t, res, "volumeDescriptor[1].volumeId"); !strings.HasPrefix(v, `"TEST_VOLUME `) {
		t.Fatalf("volumeId %q", v)
	}
	if v := value(t, res, "volumeDescriptor[1].logicalBlockSize"); v != "2048" {
		t.Fatalf("block size %s", v)
	}
	if v := value(t, res, "volumeDescriptor[1].rootDirectoryRecord"); v != ". at block 21, 2048 bytes" {
		t.Fatalf("root record %q", v)
	}
	if v := value(t, res, "volumeDescriptor[1].creationDate"); v != "2024-05-06 07:08:09 +01:00" {
		t.Fatalf("date %q", v)
	}
	if v := value(t, res, "volumeDescriptor[2].escapeSequences"); !strings.Contains(res.Root.Find("volumeDescriptor[2].escapeSequences").Comment, "Joliet") {
		t.Fatalf("Joliet %q", v)
	}
	if v := value(t, res, "volumeDescriptor[3]"); v != "TERMINATOR" {
		t.Fatalf("vd 3: %s", v)
	}
	if v := value(t, res, "bootCatalog.initialEntry.loadRBA"); v != "22" {
		t.Fatalf("loadRBA %s", v)
	}
	if v := value(t, res, "rootDirectory.record[2]"); v != "FILE.TXT;1 at block 23, 5 bytes" {
		t.Fatalf("record %q", v)
	}
	if v := value(t, res, "rootDirectory.record[2].systemUse[0]"); v != "NM file.txt" {
		t.Fatalf("NM %q", v)
	}
	if v := value(t, res, "rootDirectory.record[0].systemUse[1]"); v != "PX mode 40755" {
		t.Fatalf("PX %q", v)
	}
	checkTruncated(t, "iso9660.bt", data)
}

// The template over an image of genisoimage, when it is installed
func TestISO9660TemplateTool(t *testing.T) {
	tool, err := exec.LookPath("genisoimage")
	if err != nil {
		if tool, err = exec.LookPath("mkisofs"); err != nil {
			t.Skip("genisoimage not installed")
		}
	}
	src := t.TempDir()
	os.WriteFile(filepath.Join(src, "readme.txt"), []byte("read me"), 0o644)
	os.Mkdir(filepath.Join(src, "Some Dir"), 0o755)
	img := filepath.Join(t.TempDir(), "x.iso")
	if out, err := exec.Command(tool, "-quiet", "-R", "-J", "-V", "TOOLS", "-o", img, src).CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	data, err := os.ReadFile(img)
	if err != nil {
		t.Fatal(err)
	}
	res := libraryRun(t, data, "x.iso", "iso9660.bt")
	if v := value(t, res, "volumeDescriptor[0].volumeId"); !strings.HasPrefix(v, `"TOOLS`) {
		t.Fatalf("volumeId %s", v)
	}
	found := false
	for i := 0; i < 8; i++ {
		n := res.Root.Find(fmt.Sprintf("rootDirectory.record[%d]", i))
		if n != nil && strings.HasPrefix(n.Value(), "README.TXT;1") {
			found = true
		}
	}
	if !found {
		t.Fatal("no README.TXT;1 in the root records")
	}
}
