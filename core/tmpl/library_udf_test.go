package tmpl

import (
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"
)

// The template of UDF (udf.bt). The image is made here: the recognition
// sequence, the anchor, the volume descriptor sequences, the integrity
// descriptor, the file set and a root directory with a file and a
// directory of a 16-bit name; with meta, the file set and the root are in
// a metadata partition (UDF 2.50).

const udfSS = 2048

// udfTag fills the tag of a descriptor at the start of b
func udfTag(b []byte, id uint16, loc uint32, crcLen int) {
	le := binary.LittleEndian
	le.PutUint16(b, id)
	le.PutUint16(b[2:], 3)
	var crc uint16
	for _, v := range b[16 : 16+crcLen] {
		crc ^= uint16(v) << 8
		for i := 0; i < 8; i++ {
			if crc&0x8000 != 0 {
				crc = crc<<1 ^ 0x1021
			} else {
				crc <<= 1
			}
		}
	}
	le.PutUint16(b[8:], crc)
	le.PutUint16(b[10:], uint16(crcLen))
	le.PutUint32(b[12:], loc)
	var sum byte
	for i := 0; i < 16; i++ {
		if i != 4 {
			sum += b[i]
		}
	}
	b[4] = sum
}

func udfName(s string) []byte {
	for _, r := range s {
		if r > 0xFF {
			out := []byte{16}
			for _, u := range utf16.Encode([]rune(s)) {
				out = binary.BigEndian.AppendUint16(out, u)
			}
			return out
		}
	}
	return append([]byte{8}, s...)
}

func udfDstring(b []byte, s string) {
	n := copy(b, udfName(s))
	b[len(b)-1] = byte(n)
}

func udfFID(chars byte, icb uint32, name string, loc uint32) []byte {
	var enc []byte
	if name != "" {
		enc = udfName(name)
	}
	l := (38 + len(enc) + 3) &^ 3
	b := make([]byte, l)
	b[18], b[19] = chars, byte(len(enc))
	binary.LittleEndian.PutUint32(b[20:], udfSS)
	binary.LittleEndian.PutUint32(b[24:], icb)
	copy(b[38:], enc)
	udfTag(b, 257, loc, l-16)
	return b
}

// udfFE makes a file entry: a directory with a short_ad, or a file with
// its data embedded
func udfFE(loc uint32, dir bool, size int, ads []byte) []byte {
	le := binary.LittleEndian
	b := make([]byte, udfSS)
	le.PutUint16(b[20:], 4)
	le.PutUint16(b[24:], 1)
	if dir {
		b[27] = 4
	} else {
		b[27] = 5
		le.PutUint16(b[34:], 3)
	}
	le.PutUint32(b[44:], 0x14A5)
	le.PutUint16(b[48:], 1)
	le.PutUint64(b[56:], uint64(size))
	le.PutUint16(b[84:], 1<<12|60)
	le.PutUint16(b[86:], 2024)
	copy(b[88:], []byte{5, 6, 7, 8, 9})
	le.PutUint32(b[172:], uint32(len(ads)))
	copy(b[176:], ads)
	udfTag(b, 261, loc, 176+len(ads)-16)
	return b
}

func makeUDF(meta bool) []byte {
	le := binary.LittleEndian
	const pstart, plen = 300, 60
	b := make([]byte, (pstart+plen)*udfSS)
	for i, id := range []string{"BEA01", "NSR03", "TEA01"} {
		copy(b[32768+i*udfSS+1:], id+"\x01")
	}
	sec := func(n int) []byte { return b[n*udfSS : (n+1)*udfSS] }
	// The partition: block 0 FSD, 1 the root, 2 its data, 3 a file, 4 a
	// directory, 5 its data; in a metadata partition at blocks 20...
	base := uint32(0)
	if meta {
		base = 20
	}
	blk := func(n uint32) []byte { return sec(pstart + int(base+n)) }
	f := blk(0)
	copy(f[112:], []byte{8, 'L', 'V'})
	f[239] = 3
	udfDstring(f[304:336], "FILESET")
	le.PutUint32(f[400:], udfSS)
	le.PutUint32(f[404:], 1)
	copy(f[417:], "*OSTA UDF Compliant")
	le.PutUint16(f[440:], 0x0250)
	udfTag(f, 256, 0, 512-16)
	var dir []byte
	dir = append(dir, udfFID(0x0A, 1, "", 2)...)
	dir = append(dir, udfFID(0, 3, "hello.txt", 2)...)
	dir = append(dir, udfFID(2, 4, "Ünï 名", 2)...)
	copy(blk(2), dir)
	ad := make([]byte, 8)
	le.PutUint32(ad, uint32(len(dir)))
	le.PutUint32(ad[4:], 2)
	copy(blk(1), udfFE(1, true, len(dir), ad))
	copy(blk(3), udfFE(3, false, 5, []byte("hello")))
	sub := udfFID(0x0A, 1, "", 5)
	copy(blk(5), sub)
	le.PutUint32(ad, uint32(len(sub)))
	le.PutUint32(ad[4:], 5)
	copy(blk(4), udfFE(4, true, len(sub), ad))
	ref := uint16(0)
	maps := []byte{1, 6, 1, 0, 0, 0}
	if meta {
		// The metadata file: 10 blocks at block 20 of the physical partition
		le.PutUint32(ad, 10*udfSS)
		le.PutUint32(ad[4:], 20)
		mf := udfFE(40, false, 10*udfSS, ad)
		mf[27] = 250
		le.PutUint16(mf[34:], 0)
		udfTag(mf, 261, 40, 176+8-16)
		copy(sec(pstart+40), mf)
		m := make([]byte, 64)
		m[0], m[1] = 2, 64
		copy(m[5:], "*UDF Metadata Partition")
		le.PutUint16(m[28:], 0x0250)
		le.PutUint16(m[36:], 1)
		le.PutUint32(m[40:], 40)
		le.PutUint32(m[44:], 40)
		le.PutUint32(m[48:], 0xFFFFFFFF)
		le.PutUint32(m[52:], 32)
		le.PutUint16(m[56:], 1)
		maps = append(maps, m...)
		ref = 1
		le.PutUint16(f[408:], ref)
		udfTag(f, 256, 0, 512-16)
		// The ICBs of the FIDs are in the metadata partition
		for _, at := range []struct {
			n   uint32
			off int
		}{{2, 0}, {2, 40}, {2, 40 + len(udfFID(0, 3, "hello.txt", 2))}, {5, 0}} {
			p := blk(at.n)[at.off:]
			le.PutUint16(p[28:], 1)
			udfTag(p, 257, at.n, int(le.Uint16(p[10:])))
		}
	}
	vds := func(start int) {
		p := sec(start)
		le.PutUint32(p[16:], 1)
		udfDstring(p[24:56], "TESTVOL")
		copy(p[345:], "*AltHex test")
		le.PutUint16(p[376:], 1<<12|60)
		le.PutUint16(p[378:], 2024)
		copy(p[380:], []byte{5, 6, 7, 8, 9})
		udfTag(p, 1, uint32(start), 512-16)
		p = sec(start + 1)
		copy(p[21:], "*UDF LV Info")
		udfDstring(p[116:244], "TESTVOL")
		udfTag(p, 4, uint32(start+1), 512-16)
		p = sec(start + 2)
		le.PutUint16(p[20:], 1)
		copy(p[25:], "+NSR03")
		le.PutUint32(p[184:], 1)
		le.PutUint32(p[188:], pstart)
		le.PutUint32(p[192:], plen)
		udfTag(p, 5, uint32(start+2), 512-16)
		p = sec(start + 3)
		udfDstring(p[84:212], "TESTVOL")
		le.PutUint32(p[212:], udfSS)
		copy(p[217:], "*OSTA UDF Compliant")
		le.PutUint16(p[240:], 0x0250)
		le.PutUint32(p[248:], udfSS)
		le.PutUint16(p[256:], ref)
		le.PutUint32(p[264:], uint32(len(maps)))
		le.PutUint32(p[268:], uint32(1+len(maps)/64))
		le.PutUint32(p[432:], 2*udfSS)
		le.PutUint32(p[436:], 64)
		copy(p[440:], maps)
		udfTag(p, 6, uint32(start+3), 440+len(maps)-16)
		p = sec(start + 4)
		le.PutUint32(p[20:], 1)
		le.PutUint32(p[24:], 2*udfSS)
		le.PutUint32(p[28:], 400)
		udfTag(p, 7, uint32(start+4), 32-16)
		udfTag(sec(start+5), 8, uint32(start+5), 512-16)
	}
	vds(32)
	vds(48)
	p := sec(64)
	le.PutUint32(p[28:], 1)
	le.PutUint32(p[72:], 1)
	le.PutUint32(p[76:], 46)
	le.PutUint32(p[80:], 10)
	le.PutUint32(p[84:], plen)
	copy(p[89:], "*AltHex test")
	le.PutUint32(p[120:], 1)
	le.PutUint32(p[124:], 2)
	le.PutUint16(p[128:], 0x0250)
	udfTag(p, 9, 64, 134-16)
	udfTag(sec(65), 8, 65, 512-16)
	p = sec(256)
	le.PutUint32(p[16:], 6*udfSS)
	le.PutUint32(p[20:], 32)
	le.PutUint32(p[24:], 6*udfSS)
	le.PutUint32(p[28:], 48)
	udfTag(p, 2, 256, 512-16)
	return b
}

func udfCheckTemplate(t *testing.T, res *Result, meta bool) {
	t.Helper()
	bootWant(t, res, "vrs[1]", "NSR03")
	bootAt(t, res, "vrs[1]", 32768+2048, 7)
	bootWant(t, res, "anchor.mainVolumeDescriptorSequence", "12288 bytes at sector 32")
	bootAt(t, res, "anchor", 256*2048, 512)
	if c := res.Root.Find("anchor.tag.checksum").Comment; c != "OK" {
		t.Errorf("checksum: %q", c)
	}
	if c := res.Root.Find("anchor.tag.crc").Comment; c != "OK" {
		t.Errorf("crc: %q", c)
	}
	bootWant(t, res, "mainSequence.descriptor[0]", `PRIMARY_VOLUME "TESTVOL"`)
	bootWant(t, res, "mainSequence.descriptor[0].recordingDateAndTime", "2024-05-06 07:08:09 +01:00")
	bootWant(t, res, "mainSequence.descriptor[1].logicalVolumeIdentifier", `"TESTVOL"`)
	bootWant(t, res, "mainSequence.descriptor[2]", "PARTITION 0: sectors 300-359")
	bootWant(t, res, "mainSequence.descriptor[3]", `LOGICAL_VOLUME "TESTVOL"`)
	bootWant(t, res, "mainSequence.descriptor[3].domainIdentifier", "*OSTA UDF Compliant (UDF 2.50)")
	bootWant(t, res, "mainSequence.descriptor[3].partitionMap[0]", "physical, partition 0")
	bootWant(t, res, "mainSequence.descriptor[4].allocationDescriptors[0]", "4096 bytes at sector 400")
	bootWant(t, res, "mainSequence.descriptor[5]", "TERMINATING")
	bootWant(t, res, "reserveSequence.descriptor[5]", "TERMINATING")
	bootAt(t, res, "reserveSequence.descriptor[0]", 48*2048, 2048)
	bootWant(t, res, "integrity.numberOfDirectories", "2")
	bootWant(t, res, "integrity.sizeTable[0]", "60")
	if c := res.Root.Find("integrity.tag.crc").Comment; c != "OK" {
		t.Errorf("LVID crc: %q", c)
	}
	off := int64(300 * 2048)
	if meta {
		bootWant(t, res, "mainSequence.descriptor[3].partitionMap[1]", "metadata, partition 0")
		bootWant(t, res, "mainSequence.descriptor[3].partitionMap[1].metadataFileLocation", "40")
		bootWant(t, res, "metadataFile.icbTag.fileType", "METADATA_FILE (250)")
		off += 20 * 2048
	}
	bootAt(t, res, "fileSet", off, 2048)
	bootWant(t, res, "fileSet.fileSetIdentifier", `"FILESET"`)
	bootWant(t, res, "fileSet.rootDirectoryICB", "2048 bytes at block 1 of partition "+map[bool]string{false: "0", true: "1"}[meta])
	bootAt(t, res, "rootEntry", off+2048, 2048)
	bootWant(t, res, "rootEntry.icbTag.fileType", "DIRECTORY (4)")
	bootWant(t, res, "rootEntry.allocationDescriptor", "140 bytes at block 2")
	bootWant(t, res, "rootDirectory.fid[0]", "(parent)")
	bootWant(t, res, "rootDirectory.fid[1]", "hello.txt")
	bootWant(t, res, "rootDirectory.fid[2]", "Ünï 名/")
	bootAt(t, res, "rootDirectory.fid[1]", off+2*2048+40, 48)
}

func TestUDFTemplate(t *testing.T) {
	for _, meta := range []bool{false, true} {
		data := makeUDF(meta)
		res := libraryRun(t, data, "x.img", "udf.bt")
		udfCheckTemplate(t, res, meta)
		checkTruncated(t, "udf.bt", data)
	}
	// A damaged descriptor: its CRC is shown wrong
	data := makeUDF(false)
	data[35*2048+100] ^= 0xFF
	res := libraryRun(t, data, "x.img", "udf.bt")
	if c := res.Root.Find("mainSequence.descriptor[3].tag.crc").Comment; !strings.HasPrefix(c, "wrong") {
		t.Errorf("crc of a damaged LVD: %q", c)
	}
	data[35*2048+4] ^= 0xFF
	res = libraryRun(t, data, "x.img", "udf.bt")
	if c := res.Root.Find("mainSequence.descriptor[3].tag.checksum"); c != nil {
		t.Errorf("a descriptor of a wrong checksum is read: %v", c.Comment)
	}
}

// A hybrid image of genisoimage: the ISO template by default, this one by
// hand
func TestUDFTemplateTool(t *testing.T) {
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
	if out, err := exec.Command(tool, "-quiet", "-udf", "-V", "HYBRID", "-o", img, src).CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	data, err := os.ReadFile(img)
	if err != nil {
		t.Fatal(err)
	}
	libraryRun(t, data, "x.iso", "iso9660.bt")
	res := bootRunFile(t, "udf.bt", data)
	bootWant(t, res, "vrs[2]", "BEA01")
	bootWant(t, res, "vrs[3]", "NSR02")
	bootWant(t, res, "mainSequence.descriptor[0]", `PRIMARY_VOLUME "HYBRID"`)
	if c := res.Root.Find("mainSequence.descriptor[0].tag.crc").Comment; c != "OK" {
		t.Errorf("crc: %q", c)
	}
	names := map[string]bool{}
	for i := 0; i < 4; i++ {
		if n := res.Root.Find("rootDirectory.fid[" + string(rune('0'+i)) + "]"); n != nil {
			names[n.Value()] = true
		}
	}
	if !names["readme.txt"] || !names["Some Dir/"] {
		t.Fatalf("root: %v", names)
	}
}
