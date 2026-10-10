package part

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"hash/crc32"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/ipoluianov/althex/internal/fuzzguard"
)

var le = binary.LittleEndian

func read(t *testing.T, img []byte, ss int) *Table {
	t.Helper()
	tb, err := Read(bytes.NewReader(img), int64(len(img)), ss)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	return tb
}

// noIssues fails on any issue
func noIssues(t *testing.T, tb *Table) {
	t.Helper()
	for _, is := range tb.Issues {
		t.Errorf("unexpected issue at %d+%d: %s", is.Off, is.Len, is.Message)
	}
}

// findIssue returns the issue at off with the text in its message
func findIssue(t *testing.T, tb *Table, sev Severity, off int64, text string) Issue {
	t.Helper()
	for _, is := range tb.Issues {
		if is.Severity == sev && is.Off == off && strings.Contains(is.Message, text) {
			return is
		}
	}
	for _, is := range tb.Issues {
		t.Logf("issue %v at %d+%d: %s", is.Severity, is.Off, is.Len, is.Message)
	}
	t.Fatalf("no %v issue at %d with %q", sev, off, text)
	return Issue{}
}

// ---- MBR images ----

type mbrPart struct {
	status, typ  byte
	start, count uint32
}

func putEntry(b []byte, p mbrPart) {
	b[0] = p.status
	b[4] = p.typ
	le.PutUint32(b[8:], p.start)
	le.PutUint32(b[12:], p.count)
}

func putMBR(img []byte, sector uint32, parts ...mbrPart) {
	s := img[int64(sector)*512:]
	for i, p := range parts {
		putEntry(s[446+i*16:], p)
	}
	s[510], s[511] = 0x55, 0xAA
}

// makeMBR: 2 primaries, an extended with 3 logicals; 8 MiB
func makeMBR() []byte {
	img := make([]byte, 8<<20)
	le.PutUint32(img[440:], 0x1234ABCD)
	putMBR(img, 0,
		mbrPart{0x80, 0x83, 2048, 2048},
		mbrPart{0, 0x07, 4096, 2048},
		mbrPart{0, 0x0F, 6144, 8192})
	// EBRs at 6144, 8192, 10240; each logical starts 2048 after its EBR
	putMBR(img, 6144, mbrPart{0, 0x83, 1024, 1024}, mbrPart{0, 0x05, 2048, 2048})
	putMBR(img, 8192, mbrPart{0, 0x82, 1024, 1024}, mbrPart{0, 0x05, 4096, 2048})
	putMBR(img, 10240, mbrPart{0, 0x0C, 1024, 1024})
	return img
}

func TestMBR(t *testing.T) {
	tb := read(t, makeMBR(), 0)
	noIssues(t, tb)
	if tb.Scheme != "MBR" || tb.DiskGUID != "0x1234ABCD" {
		t.Fatalf("scheme %q id %q", tb.Scheme, tb.DiskGUID)
	}
	want := []struct {
		idx   int
		start int64
		typ   string
		ext   bool
		log   bool
		eoff  int64
	}{
		{1, 2048 * 512, "Linux", false, false, 446},
		{2, 4096 * 512, "NTFS/exFAT", false, false, 462},
		{3, 6144 * 512, "Extended (LBA)", true, false, 478},
		{5, 7168 * 512, "Linux", false, true, 6144*512 + 446},
		{6, 9216 * 512, "Linux swap / Solaris", false, true, 8192*512 + 446},
		{7, 11264 * 512, "FAT32 (LBA)", false, true, 10240*512 + 446},
	}
	if len(tb.Partitions) != len(want) {
		t.Fatalf("%d partitions: %+v", len(tb.Partitions), tb.Partitions)
	}
	for i, w := range want {
		p := tb.Partitions[i]
		if p.Index != w.idx || p.Start != w.start || p.Type != w.typ || p.Extended != w.ext || p.Logical != w.log || p.EntryOff != w.eoff {
			t.Errorf("partition %d: %+v, want %+v", i, p, w)
		}
	}
	if !tb.Partitions[0].Bootable || tb.Partitions[1].Bootable {
		t.Error("bootable flags")
	}
}

func TestMBRDamaged(t *testing.T) {
	t.Run("no signature", func(t *testing.T) {
		img := makeMBR()
		img[510], img[511] = 0, 0
		tb := read(t, img, 0)
		findIssue(t, tb, Error, 510, "55 AA")
	})
	t.Run("beyond the end", func(t *testing.T) {
		img := makeMBR()
		putEntry(img[446+48:], mbrPart{0, 0x83, 16000, 4000})
		tb := read(t, img, 0)
		findIssue(t, tb, Error, 446+48, "beyond the end")
	})
	t.Run("overlap", func(t *testing.T) {
		img := makeMBR()
		putEntry(img[446+16:], mbrPart{0, 0x07, 3000, 2048}) // into partition 1
		tb := read(t, img, 0)
		findIssue(t, tb, Error, 446+16, "partitions 1 and 2 overlap")
		// Partition 2 now also overlaps the extended one at 6144? no: 3000+2048 < 6144
		for _, is := range tb.Issues {
			if strings.Contains(is.Message, "overlap") && !strings.Contains(is.Message, "1 and 2") {
				t.Errorf("extra overlap: %s", is.Message)
			}
		}
	})
	t.Run("logical overlap", func(t *testing.T) {
		img2 := makeMBR()
		putMBR(img2, 8192, mbrPart{0, 0x82, 1024, 3000}, mbrPart{0, 0x05, 4096, 2048})
		tb := read(t, img2, 0)
		findIssue(t, tb, Error, 10240*512+446, "partitions 6 and 7 overlap")
	})
	t.Run("EBR loop", func(t *testing.T) {
		img := makeMBR()
		// The third EBR links back to the second
		putMBR(img, 10240, mbrPart{0, 0x0C, 1024, 1024}, mbrPart{0, 0x05, 2048, 2048})
		tb := read(t, img, 0)
		findIssue(t, tb, Error, 8192*512+446+16, "loops")
		if n := len(tb.Partitions); n != 6 {
			t.Fatalf("%d partitions", n)
		}
	})
	t.Run("EBR without signature", func(t *testing.T) {
		img := makeMBR()
		img[8192*512+510] = 0
		tb := read(t, img, 0)
		findIssue(t, tb, Error, 8192*512+510, "55 AA")
		if n := len(tb.Partitions); n != 4 {
			t.Fatalf("%d partitions", n)
		}
	})
	t.Run("too many logicals", func(t *testing.T) {
		img := make([]byte, 2048*512)
		putMBR(img, 0, mbrPart{0, 0x05, 64, uint32(len(img)/512 - 64)})
		for i := uint32(0); i < 200; i++ {
			putMBR(img, 64+i*8, mbrPart{0, 0x83, 1, 7}, mbrPart{0, 0x05, (i + 1) * 8, 8})
		}
		tb := read(t, img, 0)
		if n := len(tb.Partitions); n != 1+maxLogical {
			t.Fatalf("%d partitions", n)
		}
		findIssue(t, tb, Warning, int64(64+maxLogical*8)*512, "more than 128")
	})
	t.Run("FAT boot sector", func(t *testing.T) {
		img := make([]byte, 1<<20)
		img[0], img[1], img[2] = 0xEB, 0x3C, 0x90
		copy(img[3:], "MSDOS5.0")
		le.PutUint16(img[11:], 512)
		img[13] = 4
		copy(img[446:], "Remove disks or other media.\xFF")
		img[510], img[511] = 0x55, 0xAA
		if _, err := Read(bytes.NewReader(img), int64(len(img)), 0); !errors.Is(err, ErrNoTable) {
			t.Fatalf("err %v", err)
		}
	})
	t.Run("nothing", func(t *testing.T) {
		img := bytes.Repeat([]byte{0x41}, 4096)
		if _, err := Read(bytes.NewReader(img), int64(len(img)), 0); !errors.Is(err, ErrNoTable) {
			t.Fatalf("err %v", err)
		}
		if _, err := Read(bytes.NewReader(nil), 0, 0); !errors.Is(err, ErrNoTable) {
			t.Fatalf("err %v", err)
		}
	})
}

// ---- GPT images ----

type gptPart struct {
	typ, guid   string
	first, last uint64
	attrs       uint64
	name        string
}

func guidBytes(s string) []byte {
	h := strings.ReplaceAll(s, "-", "")
	var raw [16]byte
	for i := range raw {
		v, _ := strconv.ParseUint(h[i*2:i*2+2], 16, 8)
		raw[i] = byte(v)
	}
	b := make([]byte, 16)
	b[0], b[1], b[2], b[3] = raw[3], raw[2], raw[1], raw[0]
	b[4], b[5] = raw[5], raw[4]
	b[6], b[7] = raw[7], raw[6]
	copy(b[8:], raw[8:])
	return b
}

const testDiskGUID = "11223344-5566-7788-99AA-BBCCDDEEFF00"

var testGPT = []gptPart{
	{"C12A7328-F81F-11D2-BA4B-00A0C93EC93B", "A0000000-0000-0000-0000-000000000001", 40, 99, 1, "EFI system partition"},
	{"EBD0A0A2-B9E5-4433-87C0-68B6B72699C7", "A0000000-0000-0000-0000-000000000002", 100, 199, 0, "Basic data"},
	{"0FC63DAF-8483-4772-8E79-3D69D8477DE4", "A0000000-0000-0000-0000-000000000003", 200, 299, 4, "root"},
	{"0657FD6D-A4AB-43C4-84E5-0933C84B4F4F", "A0000000-0000-0000-0000-000000000004", 300, 349, 0, "swap"},
}

// makeGPT makes a disk of n sectors with a protective MBR, both GPTs and
// 128 entries of 128 bytes
func makeGPT(ss int, n uint64, parts []gptPart) []byte {
	img := make([]byte, int(n)*ss)
	putMBR(img, 0, mbrPart{0, 0xEE, 1, uint32(min(n-1, 0xFFFFFFFF))})
	entSectors := uint64(128 * 128 / ss)
	ents := make([]byte, 128*128)
	for i, p := range parts {
		e := ents[i*128:]
		copy(e, guidBytes(p.typ))
		copy(e[16:], guidBytes(p.guid))
		le.PutUint64(e[32:], p.first)
		le.PutUint64(e[40:], p.last)
		le.PutUint64(e[48:], p.attrs)
		for j, c := range utf16.Encode([]rune(p.name)) {
			le.PutUint16(e[56+j*2:], c)
		}
	}
	first, last := 2+entSectors, n-2-entSectors
	put := func(at, alt, entLBA uint64) {
		copy(img[entLBA*uint64(ss):], ents)
		h := img[at*uint64(ss):]
		copy(h, gptSig)
		le.PutUint32(h[8:], gptRevision)
		le.PutUint32(h[12:], 92)
		le.PutUint64(h[24:], at)
		le.PutUint64(h[32:], alt)
		le.PutUint64(h[40:], first)
		le.PutUint64(h[48:], last)
		copy(h[56:], guidBytes(testDiskGUID))
		le.PutUint64(h[72:], entLBA)
		le.PutUint32(h[80:], 128)
		le.PutUint32(h[84:], 128)
		le.PutUint32(h[88:], crc32.ChecksumIEEE(ents))
		le.PutUint32(h[16:], crc32.ChecksumIEEE(h[:92]))
	}
	put(1, n-1, 2)
	put(n-1, 1, n-1-entSectors)
	return img
}

// fixCRC recomputes the CRCs of the header at lba (its entries first)
func fixCRC(img []byte, ss int, lba uint64) {
	h := img[lba*uint64(ss):]
	eo := le.Uint64(h[72:]) * uint64(ss)
	le.PutUint32(h[88:], crc32.ChecksumIEEE(img[eo:eo+128*128]))
	le.PutUint32(h[16:], 0)
	le.PutUint32(h[16:], crc32.ChecksumIEEE(h[:92]))
}

const gptSectors = 1024

func TestGPT(t *testing.T) {
	for _, ss := range []int{512, 4096} {
		img := makeGPT(ss, gptSectors, testGPT)
		for _, arg := range []int{0, ss} {
			tb := read(t, img, arg)
			noIssues(t, tb)
			if tb.Scheme != "GPT" || tb.SectorSize != ss || tb.DiskGUID != testDiskGUID {
				t.Fatalf("%s %d %s", tb.Scheme, tb.SectorSize, tb.DiskGUID)
			}
			if len(tb.Partitions) != 4 {
				t.Fatalf("%d partitions", len(tb.Partitions))
			}
			names := []string{"EFI System", "Microsoft basic data", "Linux filesystem", "Linux swap"}
			for i, p := range tb.Partitions {
				w := testGPT[i]
				if p.Index != i+1 || p.TypeID != w.typ || p.GUID != w.guid || p.Name != w.name || p.Type != names[i] ||
					p.Start != int64(w.first)*int64(ss) || p.Size != int64(w.last-w.first+1)*int64(ss) ||
					p.Attrs != w.attrs || p.EntryOff != int64(2*ss+i*128) || p.EntryLen != 128 {
					t.Errorf("partition %d: %+v", i, p)
				}
			}
			if !tb.Partitions[2].Bootable || tb.Partitions[0].Bootable {
				t.Error("legacy BIOS bootable")
			}
		}
	}
}

func TestGPTDamaged(t *testing.T) {
	const ss = 512
	backup := int64(gptSectors-1) * ss
	backupEnts := int64(gptSectors-1-32) * ss

	t.Run("header CRC", func(t *testing.T) {
		img := makeGPT(ss, gptSectors, testGPT)
		img[512+16] ^= 1
		tb := read(t, img, 0)
		is := findIssue(t, tb, Error, 512+16, "primary GPT header CRC32")
		if is.Len != 4 {
			t.Fatal(is)
		}
		findIssue(t, tb, Warning, backup, "read from the backup")
		if len(tb.Partitions) != 4 || tb.Partitions[0].EntryOff != backupEnts {
			t.Fatalf("%+v", tb.Partitions)
		}
	})
	t.Run("entries CRC", func(t *testing.T) {
		img := makeGPT(ss, gptSectors, testGPT)
		img[1024+56] = 'X' // the name of partition 1
		tb := read(t, img, 0)
		findIssue(t, tb, Error, 512+88, "primary GPT entries CRC32")
		findIssue(t, tb, Error, backupEnts, "backup GPT entry 1 differs")
		if tb.Partitions[0].Name[0] != 'X' {
			t.Fatal("the primary should be used")
		}
	})
	t.Run("backup differs", func(t *testing.T) {
		img := makeGPT(ss, gptSectors, testGPT)
		le.PutUint64(img[backup+48:], gptSectors-100)
		fixCRC(img, ss, gptSectors-1)
		tb := read(t, img, 0)
		findIssue(t, tb, Error, backup+48, "last usable LBA")
	})
	t.Run("backup entries differ", func(t *testing.T) {
		img := makeGPT(ss, gptSectors, testGPT)
		le.PutUint64(img[backupEnts+128+40:], 150)
		fixCRC(img, ss, gptSectors-1)
		tb := read(t, img, 0)
		findIssue(t, tb, Error, backup+88, "entries CRC32")
		findIssue(t, tb, Error, backupEnts+128, "backup GPT entry 2 differs")
	})
	t.Run("backup missing", func(t *testing.T) {
		img := makeGPT(ss, gptSectors, testGPT)
		clear(img[backup:])
		tb := read(t, img, 0)
		findIssue(t, tb, Error, backup, "backup GPT header is missing")
	})
	t.Run("primary missing", func(t *testing.T) {
		img := makeGPT(ss, gptSectors, testGPT)
		clear(img[512:1024])
		tb := read(t, img, 0)
		findIssue(t, tb, Error, 512, "primary GPT header")
		if len(tb.Partitions) != 4 {
			t.Fatal(tb.Partitions)
		}
	})
	t.Run("overlap", func(t *testing.T) {
		img := makeGPT(ss, gptSectors, testGPT)
		le.PutUint64(img[1024+3*128+32:], 250) // partition 4 starts inside 3
		fixCRC(img, ss, 1)
		copy(img[backupEnts:], img[1024:1024+128*128])
		fixCRC(img, ss, gptSectors-1)
		tb := read(t, img, 0)
		findIssue(t, tb, Error, 1024+3*128, "partitions 3 and 4 overlap")
		if len(tb.Issues) != 1 {
			t.Fatal(tb.Issues)
		}
	})
	t.Run("outside usable", func(t *testing.T) {
		img := makeGPT(ss, gptSectors, testGPT)
		le.PutUint64(img[1024+3*128+40:], gptSectors+10)
		fixCRC(img, ss, 1)
		copy(img[backupEnts:], img[1024:1024+128*128])
		fixCRC(img, ss, gptSectors-1)
		tb := read(t, img, 0)
		findIssue(t, tb, Error, 1024+3*128+32, "outside the usable range")
		findIssue(t, tb, Error, 1024+3*128, "beyond the end of the disk")
	})
	t.Run("truncated", func(t *testing.T) {
		img := makeGPT(ss, gptSectors, testGPT)[:320*ss]
		tb := read(t, img, 0)
		findIssue(t, tb, Error, 512+32, "truncated")
		findIssue(t, tb, Error, 512+48, "truncated")
		findIssue(t, tb, Error, 1024+3*128, "beyond the end of the disk")
		if len(tb.Partitions) != 4 {
			t.Fatal(tb.Partitions)
		}
	})
	t.Run("no protective MBR signature", func(t *testing.T) {
		img := makeGPT(ss, gptSectors, testGPT)
		img[510] = 0
		tb := read(t, img, 0)
		findIssue(t, tb, Warning, 510, "55 AA")
	})
	t.Run("hybrid", func(t *testing.T) {
		img := makeGPT(ss, gptSectors, testGPT)
		putEntry(img[446+16:], mbrPart{0x80, 0x07, 100, 100}) // = GPT partition 2
		tb := read(t, img, 0)
		noIssues(t, tb)
		if tb.Scheme != "GPT (hybrid MBR)" || len(tb.Partitions) != 4 {
			t.Fatal(tb.Scheme)
		}
		putEntry(img[446+32:], mbrPart{0, 0x83, 200, 99}) // one sector short of partition 3
		tb = read(t, img, 0)
		findIssue(t, tb, Warning, 446+32, "matches no GPT partition")
	})
}

// ---- APM, BSD, VTOC ----

func makeAPM() []byte {
	img := make([]byte, 2048*512)
	be := binary.BigEndian
	copy(img, "ER")
	be.PutUint16(img[2:], 512)
	be.PutUint32(img[4:], 2048)
	ent := func(i int, start, n uint32, name, typ string) {
		e := img[i*512:]
		copy(e, "PM")
		be.PutUint32(e[4:], 4)
		be.PutUint32(e[8:], start)
		be.PutUint32(e[12:], n)
		copy(e[16:], name)
		copy(e[48:], typ)
	}
	ent(1, 1, 63, "Apple", "Apple_partition_map")
	ent(2, 64, 1000, "Macintosh HD", "Apple_HFS")
	ent(3, 1064, 900, "", "Apple_Free")
	ent(4, 1964, 84, "swap", "Linux_swap")
	return img
}

func TestAPM(t *testing.T) {
	tb := read(t, makeAPM(), 0)
	noIssues(t, tb)
	if tb.Scheme != "APM" || len(tb.Partitions) != 3 {
		t.Fatalf("%s %+v", tb.Scheme, tb.Partitions)
	}
	p := tb.Partitions[1]
	if p.Index != 2 || p.Name != "Macintosh HD" || p.Type != "HFS/HFS+" || p.Start != 64*512 || p.EntryOff != 1024 {
		t.Fatalf("%+v", p)
	}
	img := makeAPM()
	copy(img[3*512:], "XX")
	tb = read(t, img, 0)
	findIssue(t, tb, Error, 3*512, "PM")
}

func makeBSD() []byte {
	img := make([]byte, 4096*512)
	l := img[512:]
	le.PutUint32(l, bsdMagic)
	le.PutUint32(l[132:], bsdMagic)
	le.PutUint32(l[40:], 512)
	le.PutUint32(l[60:], 4096)
	le.PutUint16(l[138:], 8)
	part := func(i int, off, size uint32, fs byte) {
		e := l[148+i*16:]
		le.PutUint32(e, size)
		le.PutUint32(e[4:], off)
		e[12] = fs
	}
	part(0, 16, 2000, 7)
	part(1, 2016, 1000, 1)
	part(2, 0, 4096, 0)
	part(4, 3016, 1080, 7)
	var x uint16
	for i := 0; i < 148+16*8; i += 2 {
		x ^= le.Uint16(l[i:])
	}
	le.PutUint16(l[136:], x)
	return img
}

func TestBSD(t *testing.T) {
	tb := read(t, makeBSD(), 0)
	noIssues(t, tb)
	if tb.Scheme != "BSD" || len(tb.Partitions) != 4 {
		t.Fatalf("%s %+v", tb.Scheme, tb.Partitions)
	}
	if p := tb.Partitions[0]; p.Name != "a" || p.Type != "4.2BSD (FFS)" || p.Start != 16*512 || p.EntryOff != 512+148 {
		t.Fatalf("%+v", p)
	}
	if p := tb.Partitions[2]; p.Name != "c" || p.Type != "Whole disk" {
		t.Fatalf("%+v", p)
	}
	img := makeBSD()
	le.PutUint32(img[512+148+4*16+4:], 2900) // 'e' into 'b'
	tb = read(t, img, 0)
	findIssue(t, tb, Error, 512+136, "checksum")
	findIssue(t, tb, Error, 512+148+4*16, "partitions b and e overlap")
}

func makeVTOC() []byte {
	img := make([]byte, 4096*512)
	be := binary.BigEndian
	copy(img, "SUN test disk cyl 64 alt 2 hd 4 sec 16")
	be.PutUint32(img[128:], 1)
	be.PutUint16(img[140:], 8)
	be.PutUint32(img[188:], vtocSanity)
	be.PutUint16(img[436:], 4)
	be.PutUint16(img[438:], 16)
	slice := func(i int, tag uint16, cyl, n uint32) {
		be.PutUint16(img[142+i*4:], tag)
		be.PutUint32(img[444+i*8:], cyl)
		be.PutUint32(img[448+i*8:], n)
	}
	slice(0, 2, 0, 64*20)
	slice(1, 3, 20, 64*10)
	slice(2, 5, 0, 4096)
	be.PutUint16(img[508:], vtocMagic)
	var x uint16
	for i := 0; i < 510; i += 2 {
		x ^= be.Uint16(img[i:])
	}
	be.PutUint16(img[510:], x)
	return img
}

func TestVTOC(t *testing.T) {
	tb := read(t, makeVTOC(), 0)
	noIssues(t, tb)
	if tb.Scheme != "VTOC" || len(tb.Partitions) != 3 {
		t.Fatalf("%s %+v", tb.Scheme, tb.Partitions)
	}
	if p := tb.Partitions[1]; p.Type != "Swap" || p.Start != 20*64*512 || p.Size != 640*512 || p.EntryOff != 452 {
		t.Fatalf("%+v", p)
	}
	img := makeVTOC()
	img[0] = 'X'
	tb = read(t, img, 0)
	findIssue(t, tb, Error, 510, "checksum")
}

func TestTypeName(t *testing.T) {
	cases := [][3]string{
		{"MBR", "0x83", "Linux"}, {"EBR", "0x0f", "Extended (LBA)"}, {"MBR", "0xee", "GPT protective"},
		{"GPT", "c12a7328-f81f-11d2-ba4b-00a0c93ec93b", "EFI System"},
		{"GPT", "BC13C2FF-59E6-4262-A352-B275FD6F7172", "Linux extended boot"},
		{"APM", "Apple_Driver_Foo", "Apple driver"}, {"BSD", "7", "4.2BSD (FFS)"}, {"VTOC", "0x03", "Swap"},
		{"GPT", "nonsense", ""},
	}
	for _, c := range cases {
		if got := TypeName(c[0], c[1]); got != c[2] {
			t.Errorf("TypeName(%s, %s) = %q", c[0], c[1], got)
		}
	}
	if len(gptTypes) < 40 || len(mbrTypes) < 60 {
		t.Fatalf("%d GPT types, %d MBR types", len(gptTypes), len(mbrTypes))
	}
	if g := guidString(guidBytes(testDiskGUID)); g != testDiskGUID {
		t.Fatal(g)
	}
}

// ---- Real tables made by the tools of util-linux and parted ----

func tool(t *testing.T, name string) string {
	t.Helper()
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	for _, dir := range []string{"/sbin", "/usr/sbin"} {
		p := filepath.Join(dir, name)
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	t.Skipf("%s is not installed", name)
	return ""
}

type sfdiskDump struct {
	Table struct {
		Label      string `json:"label"`
		ID         string `json:"id"`
		SectorSize int    `json:"sectorsize"`
		Partitions []struct {
			Node     string `json:"node"`
			Start    int64  `json:"start"`
			Size     int64  `json:"size"`
			Type     string `json:"type"`
			UUID     string `json:"uuid"`
			Name     string `json:"name"`
			Bootable bool   `json:"bootable"`
		} `json:"partitions"`
	} `json:"partitiontable"`
}

func TestSfdisk(t *testing.T) {
	sfdisk := tool(t, "sfdisk")
	layouts := map[string]string{
		"dos": "label: dos\nsize=10M, type=83, bootable\nsize=10M, type=7\ntype=5\nsize=5M, type=83\nsize=5M, type=82\ntype=c\n",
		"gpt": "label: gpt\nsize=10M, type=L, name=\"root fs\"\nsize=5M, type=U\nsize=5M, type=H, attrs=LegacyBIOSBootable\ntype=S, name=swap\n",
	}
	for label, layout := range layouts {
		t.Run(label, func(t *testing.T) {
			img := filepath.Join(t.TempDir(), "disk.img")
			if err := os.WriteFile(img, nil, 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.Truncate(img, 64<<20); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(sfdisk, "-q", img)
			cmd.Stdin = strings.NewReader(layout)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Skipf("sfdisk: %v %s", err, out)
			}
			out, err := exec.Command(sfdisk, "-J", img).Output()
			if err != nil {
				t.Skipf("sfdisk -J: %v", err)
			}
			var dump sfdiskDump
			if err := json.Unmarshal(out, &dump); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(img)
			if err != nil {
				t.Fatal(err)
			}
			tb := read(t, data, 0)
			noIssues(t, tb)
			if len(tb.Partitions) != len(dump.Table.Partitions) {
				t.Fatalf("%d partitions, sfdisk has %d", len(tb.Partitions), len(dump.Table.Partitions))
			}
			if !strings.EqualFold(tb.DiskGUID, dump.Table.ID) {
				t.Errorf("disk id %s, sfdisk %s", tb.DiskGUID, dump.Table.ID)
			}
			for i, w := range dump.Table.Partitions {
				p := tb.Partitions[i]
				num := w.Node[strings.LastIndexFunc(w.Node, func(r rune) bool { return r < '0' || r > '9' })+1:]
				typ := w.Type
				if label == "dos" {
					v, _ := strconv.ParseUint(typ, 16, 8)
					typ = mbrTypeID(byte(v))
				}
				if strconv.Itoa(p.Index) != num || p.Start != w.Start*512 || p.Size != w.Size*512 ||
					p.TypeID != typ || p.GUID != w.UUID || p.Name != w.Name || (label == "dos" && p.Bootable != w.Bootable) {
					t.Errorf("partition %d: %+v, sfdisk %+v", i, p, w)
				}
				if p.Type == "" {
					t.Errorf("no name for the type %s", p.TypeID)
				}
			}
			if label == "gpt" && !tb.Partitions[2].Bootable {
				t.Error("LegacyBIOSBootable")
			}
		})
	}
}

func TestParted(t *testing.T) {
	parted := tool(t, "parted")
	img := filepath.Join(t.TempDir(), "disk.img")
	if err := os.WriteFile(img, make([]byte, 32<<20), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(parted, "-s", img, "mklabel", "gpt",
		"mkpart", "boot", "fat32", "1MiB", "5MiB", "set", "1", "esp", "on",
		"mkpart", "data", "ext4", "5MiB", "20MiB", "mkpart", "rest", "linux-swap", "20MiB", "31MiB")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("parted: %v %s", err, out)
	}
	out, err := exec.Command(parted, "-s", "-m", img, "unit", "B", "print").Output()
	if err != nil {
		t.Skipf("parted print: %v", err)
	}
	data, err := os.ReadFile(img)
	if err != nil {
		t.Fatal(err)
	}
	tb := read(t, data, 0)
	noIssues(t, tb)
	var n int
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Split(strings.TrimSuffix(line, ";"), ":")
		if len(f) < 6 || f[0] == "BYT" || strings.HasPrefix(f[0], "/") {
			continue
		}
		start, _ := strconv.ParseInt(strings.TrimSuffix(f[1], "B"), 10, 64)
		size, _ := strconv.ParseInt(strings.TrimSuffix(f[3], "B"), 10, 64)
		if n >= len(tb.Partitions) {
			t.Fatalf("parted has more partitions: %s", out)
		}
		p := tb.Partitions[n]
		if strconv.Itoa(p.Index) != f[0] || p.Start != start || p.Size != size || p.Name != f[5] {
			t.Errorf("partition %+v, parted %q", p, line)
		}
		n++
	}
	if n != 3 || tb.Partitions[0].Type != "EFI System" {
		t.Fatalf("%d partitions %+v", n, tb.Partitions)
	}
}

// ---- Fuzzing ----

func FuzzRead(f *testing.F) {
	small := make([]byte, 64*512)
	putMBR(small, 0, mbrPart{0x80, 0x83, 1, 7}, mbrPart{0, 0x0F, 8, 40})
	putMBR(small, 8, mbrPart{0, 0x83, 1, 7}, mbrPart{0, 0x05, 8, 8})
	putMBR(small, 16, mbrPart{0, 0x07, 1, 7})
	f.Add(small)
	f.Add(makeGPT(512, 80, testGPT[:1]))
	f.Add(makeAPM()[:4096])
	f.Add(makeBSD()[:2048])
	f.Add(makeVTOC()[:2048])
	f.Fuzz(func(t *testing.T, b []byte) {
		defer fuzzguard.Start(t.Name())()
		for _, ss := range []int{0, 512, 4096} {
			tb, err := Read(bytes.NewReader(b), int64(len(b)), ss)
			if err != nil {
				if !errors.Is(err, ErrNoTable) {
					t.Fatal(err)
				}
				continue
			}
			for _, is := range tb.Issues {
				if is.Message == "" || is.Len < 0 {
					t.Fatalf("bad issue %+v", is)
				}
			}
			if len(tb.Issues) > maxIssues {
				t.Fatal("too many issues")
			}
		}
	})
}
