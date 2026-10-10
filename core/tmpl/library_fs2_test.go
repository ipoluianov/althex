package tmpl

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/binary"
	"io"
	"math/rand"
	"os"
	"strings"
	"testing"
	"unicode/utf16"
)

// The templates of file systems (BOOT-10): NTFS, ext2/3/4, HFS+, APFS. The
// samples are made here; the images of the tests of core/fs are run too
// when they are there.

// fs2NTFS makes the boot sector and the $MFT record of an NTFS volume:
// clusters of 4096 bytes, the MFT at cluster 1, records of 1024 bytes
func fs2NTFS() []byte {
	b := make([]byte, 3*4096)
	le := binary.LittleEndian
	copy(b, []byte{0xEB, 0x52, 0x90})
	copy(b[3:], "NTFS    ")
	le.PutUint16(b[0x0B:], 512)
	b[0x0D] = 8
	b[0x15] = 0xF8
	le.PutUint64(b[0x28:], 24-1)
	le.PutUint64(b[0x30:], 1)
	le.PutUint64(b[0x38:], 2)
	b[0x40] = 0xF6 // 2^10
	b[0x44] = 1
	le.PutUint16(b[0x1FE:], 0xAA55)

	r := b[4096 : 4096+1024]
	copy(r, "FILE")
	le.PutUint16(r[4:], 0x30)
	le.PutUint16(r[6:], 3)
	le.PutUint16(r[0x10:], 1)
	le.PutUint16(r[0x12:], 1)
	le.PutUint16(r[0x14:], 0x38)
	le.PutUint16(r[0x16:], 1)
	le.PutUint32(r[0x1C:], 1024)
	off := 0x38
	resident := func(typ uint32, val []byte) {
		l := (0x18 + len(val) + 7) &^ 7
		le.PutUint32(r[off:], typ)
		le.PutUint32(r[off+4:], uint32(l))
		le.PutUint32(r[off+0x10:], uint32(len(val)))
		le.PutUint16(r[off+0x14:], 0x18)
		copy(r[off+0x18:], val)
		off += l
	}
	si := make([]byte, 72)
	le.PutUint64(si[8:], 133000000000000000) // 2022-06-18 04:26:40
	resident(0x10, si)
	name := utf16.Encode([]rune("$MFT"))
	fn := make([]byte, 0x42+2*len(name))
	le.PutUint64(fn, 5)
	fn[0x40] = byte(len(name))
	fn[0x41] = 3
	for i, c := range name {
		le.PutUint16(fn[0x42+2*i:], c)
	}
	resident(0x30, fn)
	// $DATA: 4 clusters at 1, then 2 sparse, then 3 at 1 - 1 = 0... at 5
	d := r[off:]
	le.PutUint32(d, 0x80)
	le.PutUint32(d[4:], 0x50)
	d[8] = 1
	le.PutUint64(d[0x18:], 8)
	le.PutUint16(d[0x20:], 0x40)
	le.PutUint64(d[0x28:], 9*4096)
	le.PutUint64(d[0x30:], 9*4096)
	le.PutUint64(d[0x38:], 9*4096)
	copy(d[0x40:], []byte{0x11, 0x04, 0x01, 0x01, 0x02, 0x11, 0x03, 0x04, 0x00})
	off += 0x50
	le.PutUint32(r[off:], 0xFFFFFFFF)
	le.PutUint32(r[0x18:], uint32(off+8))
	return b
}

func TestFS2NTFS(t *testing.T) {
	data := fs2NTFS()
	res := libraryRun(t, data, "disk.img", "ntfs.bt")
	for path, want := range map[string]string{
		"boot.bytesPerSector":                    "512",
		"boot.mftLcn":                            "1",
		"mftRecord.header.flags":                 "0x0001",
		"mftRecord.attribute[1].value":           "$MFT",
		"mftRecord.attribute[1].value.nameSpace": "WIN32_AND_DOS (3)",
		"mftRecord.attribute[2].dataSize":        "36864",
		"mftRecord.attribute[2].runs.run[0]":     "4 clusters, cluster offset +1",
		"mftRecord.attribute[2].runs.run[1]":     "2 clusters, sparse",
		"mftRecord.attribute[2].runs.run[2]":     "3 clusters, cluster offset +4",
		"mftRecord.endMarker":                    "0xFFFFFFFF",
	} {
		if got := value(t, res, path); got != want {
			t.Errorf("%s = %q, want %q", path, got, want)
		}
	}
	if got := value(t, res, "mftRecord.attribute[0].value.modificationTime"); !strings.HasPrefix(got, "2022-06-18 04:26:40") {
		t.Errorf("time %q", got)
	}
	fs2Damaged(t, data, "ntfs.bt", 4096)
}

// fs2Ext makes an ext4 superblock of 64bit with 3 groups and their
// descriptors of 64 bytes
func fs2Ext() []byte {
	b := make([]byte, 4*4096)
	le := binary.LittleEndian
	sb := b[1024:]
	le.PutUint32(sb[0x0:], 3*8192)
	le.PutUint32(sb[0x4:], 3*32768-100)
	le.PutUint32(sb[0x18:], 2) // 4096
	le.PutUint32(sb[0x20:], 32768)
	le.PutUint32(sb[0x28:], 8192)
	le.PutUint32(sb[0x2C:], 1700000000)
	le.PutUint16(sb[0x38:], 0xEF53)
	le.PutUint16(sb[0x3A:], 1)
	le.PutUint32(sb[0x4C:], 1)
	le.PutUint16(sb[0x58:], 256)
	le.PutUint32(sb[0x5C:], 0x3C)
	le.PutUint32(sb[0x60:], 0x2C2)
	le.PutUint32(sb[0x64:], 0x46B)
	copy(sb[0x68:], []byte{0x12, 0x34, 0x56, 0x78, 0x9A, 0xBC, 0xDE, 0xF0, 1, 2, 3, 4, 5, 6, 7, 8})
	copy(sb[0x78:], "rootfs")
	le.PutUint16(sb[0xFE:], 64)
	for g := range 3 {
		d := b[4096+64*g:]
		le.PutUint32(d[0x8:], uint32(100+g*512))
		le.PutUint16(d[0xC:], uint16(1000+g))
	}
	return b
}

func TestFS2Ext(t *testing.T) {
	data := fs2Ext()
	res := libraryRun(t, data, "root.img", "ext4.bt")
	for path, want := range map[string]string{
		"superblock.s_magic":                               "0xEF53",
		"superblock.s_volume_name":                         `"rootfs"`,
		"superblock.s_uuid":                                "12345678-9abc-def0-0102-030405060708",
		"superblock.s_mtime":                               "2023-11-14 22:13:20 UTC",
		"superblock.s_checksum":                            "0x00000000",
		"groupDescriptors.desc[2].bg_inode_table_lo":       "1124",
		"groupDescriptors.desc[1].bg_free_blocks_count_lo": "1001",
		"groupDescriptors.desc[0].bg_reserved":             "0",
	} {
		if got := value(t, res, path); got != want {
			t.Errorf("%s = %q, want %q", path, got, want)
		}
	}
	if n := res.Root.Find("groupDescriptors.desc"); n == nil || n.ChildCount() != 3 {
		t.Fatal("not 3 descriptors")
	}
	if !strings.Contains(res.Root.Find("superblock.s_feature_incompat").Comment, "64bit") {
		t.Error("no 64bit in the comment")
	}
	fs2Damaged(t, data, "ext4.bt", 1024)

	// The images of core/fs, made by mke2fs
	for _, p := range []string{"../fs/testdata/ext4/ext4.img.gz", "../fs/testdata/ext4/ext2.img.gz"} {
		img := fs2Gunzip(t, p)
		if img == nil {
			continue
		}
		res := libraryRun(t, img, "x.img", "ext4.bt")
		if v := value(t, res, "superblock.s_log_block_size"); v != "0" {
			t.Errorf("%s: block size %s", p, v)
		}
		if res.Root.Find("groupDescriptors.desc[0].bg_inode_table_lo") == nil {
			t.Errorf("%s: no group descriptor", p)
		}
	}
	if img := fs2Gunzip(t, "../fs/testdata/ntfs/small.img.gz"); img != nil {
		res := libraryRun(t, img, "x.img", "ntfs.bt")
		if v := value(t, res, "mftRecord.attribute[1].value"); v != "$MFT" {
			t.Errorf("ntfs image: %q", v)
		}
	}
}

func fs2HFS() []byte {
	b := make([]byte, 4096)
	be := binary.BigEndian
	h := b[1024:]
	copy(h, "H+")
	be.PutUint16(h[2:], 4)
	be.PutUint32(h[4:], 0x2100)
	copy(h[8:], "HFSJ")
	be.PutUint32(h[0x14:], 3786912000) // 2024-01-01 in seconds since 1904
	be.PutUint32(h[0x28:], 4096)
	be.PutUint32(h[0x2C:], 1000)
	// The catalog file: 3 forks of 80 bytes after 0x70
	cat := h[0x70+2*80:]
	be.PutUint64(cat, 8*4096)
	be.PutUint32(cat[0xC:], 8)
	be.PutUint32(cat[0x10:], 50)
	be.PutUint32(cat[0x14:], 8)
	return b
}

func TestFS2HFSPlus(t *testing.T) {
	data := fs2HFS()
	res := libraryRun(t, data, "mac.img", "hfsplus.bt")
	for path, want := range map[string]string{
		"volumeHeader.signature":                         `"H+"`,
		"volumeHeader.blockSize":                         "4096",
		"volumeHeader.modifyDate":                        "2024-01-01 00:00:00",
		"volumeHeader.catalogFile":                       "32768 bytes, 8 blocks",
		"volumeHeader.catalogFile.extents[0]":            "8 blocks at 50",
		"volumeHeader.startupFile.extents[7].blockCount": "0",
	} {
		if got := value(t, res, path); got != want {
			t.Errorf("%s = %q, want %q", path, got, want)
		}
	}
	// HFSX
	copy(data[1024:], "HX")
	libraryRun(t, data, "mac.img", "hfsplus.bt")
	fs2Damaged(t, data, "hfsplus.bt", 1024)
}

func fs2APFS() []byte {
	b := make([]byte, 4096)
	le := binary.LittleEndian
	le.PutUint64(b[8:], 1)
	le.PutUint64(b[16:], 42)
	le.PutUint32(b[24:], 0x80000001)
	copy(b[0x20:], "NXSB")
	le.PutUint32(b[0x24:], 4096)
	le.PutUint64(b[0x28:], 25600)
	le.PutUint64(b[0x40:], 2)
	le.PutUint64(b[0xA0:], 1026)
	le.PutUint32(b[0xB4:], 100)
	le.PutUint64(b[0xB8:], 1026)
	le.PutUint64(b[0xC0:], 1027)
	le.PutUint64(b[0x568:], 0x7777)
	return b
}

func TestFS2APFS(t *testing.T) {
	data := fs2APFS()
	res := libraryRun(t, data, "mac.img", "apfs.bt")
	for path, want := range map[string]string{
		"superblock.nx_magic":                  `"NXSB"`,
		"superblock.nx_block_size":             "4096",
		"superblock.nx_block_count":            "25600",
		"superblock.nx_o.o_xid":                "42",
		"superblock.nx_omap_oid":               "1026",
		"superblock.nx_fs_oid[1]":              "1027",
		"superblock.nx_newest_mounted_version": "30583",
	} {
		if got := value(t, res, path); got != want {
			t.Errorf("%s = %q, want %q", path, got, want)
		}
	}
	if !strings.Contains(strings.Join(res.Output, ""), "2 volumes") {
		t.Errorf("output %q", res.Output)
	}
	if c := res.Root.Find("superblock.nx_o.o_type").Comment; !strings.Contains(c, "NX_SUPERBLOCK") {
		t.Errorf("type comment %q", c)
	}
	fs2Damaged(t, data, "apfs.bt", 0x20)
}

// fs2Damaged runs a template over cut and garbled copies of a sample: it
// must end without an error. keep is the length of the start that is left
// alone so the template is still detected.
func fs2Damaged(t *testing.T, data []byte, file string, keep int) {
	t.Helper()
	p := execTemplate(t, file)
	rng := rand.New(rand.NewSource(1))
	for i := range 300 {
		d := append([]byte(nil), data...)
		if i%3 == 0 {
			d = d[:keep+rng.Intn(len(d)-keep)]
		}
		for range 1 + rng.Intn(40) {
			d[rng.Intn(len(d))] = byte(rng.Intn(256))
		}
		res := p.Run(context.Background(), Env{Data: &mem{b: d}, MaxSteps: execMaxSteps})
		if res.Err != nil {
			t.Fatalf("%s, sample %d: %v", file, i, res.Err)
		}
	}
}

// fs2Gunzip reads a gzipped image; nil when it is not there
func fs2Gunzip(t *testing.T, path string) []byte {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	z, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(z)
	if err != nil {
		t.Fatal(err)
	}
	return bytes.Clone(b)
}
