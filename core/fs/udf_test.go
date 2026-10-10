package fs

import (
	"bytes"
	"encoding/binary"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/ipoluianov/althex/internal/fuzzguard"
)

// ---- A UDF image made in Go ----

type udfNodeT struct {
	name  string
	dir   bool
	data  []byte
	kids  []*udfNodeT
	embed bool // the data in the file entry
	parts int  // more than 1: extents not next to each other
	hole  bool // the second extent is not recorded: zeros
	aed   bool // the allocation descriptors go on in an allocation extent
	long  bool // long_ad
	efe   bool // an extended file entry
	ind   bool // strategy 4096: an old entry, an indirect entry to the real one
	icb   uint32
	// filled while building: where things are in the image (the tests break them)
	fidOff []int64 // dir: where the FIDs of the kids are (in the space)
	feBlk  uint32
	aedBlk uint32
}

type udfBuilder struct {
	ss     int
	rev    uint16
	mode   string // "", "meta", "vat", "spare"
	label  string
	root   *udfNodeT
	sp     [2][]byte // 0: the physical partition, 1: the metadata or virtual one
	mref   uint16    // the partition of the metadata (entries, directories)
	dref   uint16    // the partition of the data of files
	pstart int
	// what the tests change
	avdp, mainVDS, lvdSec int
}

var udfTestTime = time.Date(2024, 5, 6, 7, 8, 9, 0, time.FixedZone("", 3600))

func (m *udfBuilder) alloc(s uint16, n int) uint32 {
	blk := len(m.sp[s]) / m.ss
	m.sp[s] = append(m.sp[s], make([]byte, n*m.ss)...)
	return uint32(blk)
}

func (m *udfBuilder) put(s uint16, blk uint32, b []byte) {
	copy(m.sp[s][int(blk)*m.ss:], b)
}

// udfTagT fills the tag of a descriptor: its CRC over crcLen bytes after it
func udfTagT(b []byte, id uint16, loc uint32, crcLen int) {
	binary.LittleEndian.PutUint16(b, id)
	binary.LittleEndian.PutUint16(b[2:], 3)
	binary.LittleEndian.PutUint16(b[6:], 1)
	binary.LittleEndian.PutUint16(b[8:], udfCRC(b[16:16+crcLen]))
	binary.LittleEndian.PutUint16(b[10:], uint16(crcLen))
	binary.LittleEndian.PutUint32(b[12:], loc)
	var sum byte
	for i := 0; i < 16; i++ {
		if i != 4 {
			sum += b[i]
		}
	}
	b[4] = sum
}

// udfEnc encodes a name in OSTA compressed Unicode: 8 bits when it can
func udfEnc(s string) []byte {
	r := []rune(s)
	wide := false
	for _, c := range r {
		if c > 0xFF {
			wide = true
		}
	}
	if !wide {
		out := []byte{8}
		for _, c := range r {
			out = append(out, byte(c))
		}
		return out
	}
	out := []byte{16}
	for _, u := range utf16.Encode(r) {
		out = binary.BigEndian.AppendUint16(out, u)
	}
	return out
}

func udfDStr(b []byte, s string) {
	e := udfEnc(s)
	copy(b, e)
	b[len(b)-1] = byte(len(e))
}

func udfStamp(b []byte, t time.Time) {
	_, off := t.Zone()
	binary.LittleEndian.PutUint16(b, 1<<12|uint16(off/60)&0xFFF)
	binary.LittleEndian.PutUint16(b[2:], uint16(t.Year()))
	b[4], b[5], b[6], b[7], b[8] = byte(t.Month()), byte(t.Day()), byte(t.Hour()), byte(t.Minute()), byte(t.Second())
}

func udfRegid(b []byte, id string, suffix ...byte) {
	copy(b[1:24], id)
	copy(b[24:32], suffix)
}

// fe makes a file entry of the node at block blk
func (m *udfBuilder) fe(n *udfNodeT, blk uint32, fileType byte, size int64, adType uint16, ads []byte, strategy uint16) []byte {
	b := make([]byte, m.ss)
	hdr, mt := 176, 84
	if n.efe {
		hdr, mt = 216, 92
	}
	binary.LittleEndian.PutUint16(b[20:], strategy)
	binary.LittleEndian.PutUint16(b[24:], 1)
	b[27] = fileType
	binary.LittleEndian.PutUint16(b[34:], adType)
	binary.LittleEndian.PutUint32(b[36:], 0xFFFFFFFF)
	binary.LittleEndian.PutUint32(b[40:], 0xFFFFFFFF)
	perm := uint32(6<<10 | 4<<5 | 4)
	if fileType == 4 {
		perm = 7<<10 | 5<<5 | 5
	}
	binary.LittleEndian.PutUint32(b[44:], perm)
	binary.LittleEndian.PutUint16(b[48:], 1)
	binary.LittleEndian.PutUint64(b[56:], uint64(size))
	udfStamp(b[mt:], udfTestTime)
	if n.efe {
		binary.LittleEndian.PutUint64(b[64:], uint64(size))
	}
	binary.LittleEndian.PutUint32(b[hdr-4:], uint32(len(ads)))
	copy(b[hdr:], ads)
	id := uint16(udfTagFE)
	if n.efe {
		id = udfTagEFE
	}
	udfTagT(b, id, blk, hdr+len(ads)-16)
	return b
}

// ad makes an allocation descriptor of a run in partition ref
func (m *udfBuilder) ad(long bool, typ uint32, n int, blk uint32, ref uint16) []byte {
	if !long {
		b := make([]byte, 8)
		binary.LittleEndian.PutUint32(b, typ<<30|uint32(n))
		binary.LittleEndian.PutUint32(b[4:], blk)
		return b
	}
	b := make([]byte, 16)
	binary.LittleEndian.PutUint32(b, typ<<30|uint32(n))
	binary.LittleEndian.PutUint32(b[4:], blk)
	binary.LittleEndian.PutUint16(b[8:], ref)
	return b
}

func (m *udfBuilder) fid(chars byte, icb uint32, name string, loc uint32) []byte {
	var enc []byte
	if name != "" {
		enc = udfEnc(name)
	}
	l := (38 + len(enc) + 3) &^ 3
	b := make([]byte, l)
	binary.LittleEndian.PutUint16(b[16:], 1)
	b[18] = chars
	b[19] = byte(len(enc))
	binary.LittleEndian.PutUint32(b[20:], uint32(m.ss))
	binary.LittleEndian.PutUint32(b[24:], icb)
	binary.LittleEndian.PutUint16(b[28:], m.mref)
	copy(b[38:], enc)
	udfTagT(b, udfTagFID, loc, l-16)
	return b
}

func (m *udfBuilder) blocks(n int) int { return (n + m.ss - 1) / m.ss }

// allocICB gives a node its ICB: two blocks for strategy 4096 (the entry
// and the indirect entry after it)
func (m *udfBuilder) allocICB(n *udfNodeT) {
	if n.ind {
		n.icb = m.alloc(m.mref, 2)
	} else {
		n.icb = m.alloc(m.mref, 1)
	}
}

func (m *udfBuilder) build(n, parent *udfNodeT) {
	var fileType byte = 5
	var size int64
	var adType uint16
	var ads []byte
	long := n.long || m.mref != m.dref
	if n.dir {
		fileType = 4
		for _, k := range n.kids {
			m.allocICB(k)
		}
		for _, k := range n.kids {
			m.build(k, n)
		}
		data := m.fid(0x0A, parent.icb, "", 0)
		for _, k := range n.kids {
			var c byte
			if k.dir {
				c = 2
			}
			n.fidOff = append(n.fidOff, int64(len(data)))
			data = append(data, m.fid(c, k.icb, k.name, 0)...)
		}
		blk := m.alloc(m.mref, m.blocks(len(data)))
		// The FIDs again with their locations
		data = m.fid(0x0A, parent.icb, "", blk)
		for i, k := range n.kids {
			var c byte
			if k.dir {
				c = 2
			}
			data = append(data, m.fid(c, k.icb, k.name, blk+uint32(n.fidOff[i])/uint32(m.ss))...)
		}
		for i := range n.fidOff {
			n.fidOff[i] += int64(blk) * int64(m.ss)
		}
		m.put(m.mref, blk, data)
		size = int64(len(data))
		ads = m.ad(false, 0, len(data), blk, m.mref)
	} else {
		size = int64(len(n.data))
		switch {
		case n.embed:
			adType, ads = 3, n.data
		case size > 0:
			parts := max(n.parts, 1)
			chunk := m.blocks((len(n.data)+parts-1)/parts) * m.ss
			for i, off := 0, 0; off < len(n.data); i, off = i+1, off+chunk {
				l := min(chunk, len(n.data)-off)
				m.alloc(m.dref, 1) // a gap
				blk := m.alloc(m.dref, m.blocks(l))
				typ := uint32(0)
				if n.hole && i == 1 {
					typ = 1
					clear(n.data[off : off+l])
				} else {
					m.put(m.dref, blk, n.data[off:off+l])
				}
				ads = append(ads, m.ad(long, typ, l, blk, m.dref)...)
			}
			if long {
				adType = 1
			}
		}
	}
	if n.aed {
		blk := m.alloc(m.mref, 1)
		b := make([]byte, m.ss)
		binary.LittleEndian.PutUint32(b[20:], uint32(len(ads)))
		copy(b[24:], ads)
		udfTagT(b, udfTagAED, blk, 8+len(ads))
		m.put(m.mref, blk, b)
		n.aedBlk = blk
		ads = m.ad(long, 3, m.ss, blk, m.mref)
	}
	if n.ind {
		// The old entry: three embedded bytes; the indirect entry after it
		// leads to the real one, which has an empty block after it
		old := m.fe(&udfNodeT{}, n.icb, fileType, 3, 3, []byte("old"), 4096)
		m.put(m.mref, n.icb, old)
		blk := m.alloc(m.mref, 2)
		ie := make([]byte, m.ss)
		binary.LittleEndian.PutUint16(ie[20:], 4096)
		b := m.ad(true, 0, m.ss, blk, m.mref)
		copy(ie[36:], b)
		udfTagT(ie, udfTagIE, n.icb+1, 36)
		m.put(m.mref, n.icb+1, ie)
		m.put(m.mref, blk, m.fe(n, blk, fileType, size, adType, ads, 4096))
		n.feBlk = blk
		return
	}
	n.feBlk = n.icb
	m.put(m.mref, n.icb, m.fe(n, n.icb, fileType, size, adType, ads, 4))
}

// finish lays the image out: the recognition sequence, the descriptor
// sequences at sectors 32 and 48, the anchor at 256 and at the last sector
// (not with a VAT: the VAT is last), the partition from 257
func (m *udfBuilder) finish() []byte {
	ss := m.ss
	switch m.mode {
	case "meta", "vat":
		m.mref, m.dref = 1, 1
		if m.mode == "meta" {
			m.dref = 0
		}
	}
	m.alloc(0, 1) // block 0 of the physical partition is not used
	fsd := m.alloc(m.mref, 1)
	m.root.icb = 0
	m.allocICB(m.root)
	m.build(m.root, m.root)
	b := make([]byte, ss)
	udfStamp(b[16:], udfTestTime)
	udfDStr(b[112:240], m.label)
	udfDStr(b[304:336], "FILESET")
	copy(b[400:], m.ad(true, 0, ss, m.root.icb, m.mref))
	udfRegid(b[416:], "*OSTA UDF Compliant", byte(m.rev), byte(m.rev>>8))
	udfTagT(b, udfTagFSD, fsd, 512-16)
	m.put(m.mref, fsd, b)

	var maps []byte
	pm1 := []byte{1, 6, 1, 0, 0, 0}
	pm2 := func(id string) []byte {
		p := make([]byte, 64)
		p[0], p[1] = 2, 64
		udfRegid(p[4:], id, byte(m.rev), byte(m.rev>>8))
		binary.LittleEndian.PutUint16(p[36:], 1)
		return p
	}
	var tail []byte // after the partition: the spare packets, the sparing table
	switch m.mode {
	case "":
		maps = pm1
	case "meta":
		// The metadata file in two runs of the physical partition; its
		// mirror points to the same runs
		meta := m.sp[1]
		half := m.blocks(len(meta) / 2)
		var ads []byte
		for _, r := range [][]byte{meta[:half*ss], meta[half*ss:]} {
			m.alloc(0, 1)
			blk := m.alloc(0, m.blocks(len(r)))
			m.put(0, blk, r)
			ads = append(ads, m.ad(false, 0, len(r), blk, 0)...)
		}
		mainFE := m.alloc(0, 1)
		m.put(0, mainFE, m.fe(&udfNodeT{}, mainFE, 250, int64(len(meta)), 0, ads, 4))
		mirror := m.alloc(0, 1)
		m.put(0, mirror, m.fe(&udfNodeT{}, mirror, 251, int64(len(meta)), 0, ads, 4))
		p := pm2("*UDF Metadata Partition")
		binary.LittleEndian.PutUint32(p[40:], mainFE)
		binary.LittleEndian.PutUint32(p[44:], mirror)
		binary.LittleEndian.PutUint32(p[48:], 0xFFFFFFFF)
		binary.LittleEndian.PutUint32(p[52:], 32)
		binary.LittleEndian.PutUint16(p[56:], 1)
		maps = append(pm1, p...)
	case "vat":
		// The virtual blocks in the reverse order; the VAT (2.00) after them
		// and its entry in the last sector
		virt := m.sp[1]
		nv := len(virt) / ss
		base := m.alloc(0, nv)
		vat := make([]byte, 152)
		binary.LittleEndian.PutUint16(vat, 152)
		udfDStr(vat[4:132], m.label+" VAT")
		binary.LittleEndian.PutUint32(vat[132:], 0xFFFFFFFF)
		for v := 0; v < nv; v++ {
			p := base + uint32(nv-1-v)
			m.put(0, p, virt[v*ss:(v+1)*ss])
			vat = binary.LittleEndian.AppendUint32(vat, p)
		}
		vat = binary.LittleEndian.AppendUint32(vat, 0xFFFFFFFF)
		vblk := m.alloc(0, m.blocks(len(vat)))
		m.put(0, vblk, vat)
		fe := m.alloc(0, 1)
		m.put(0, fe, m.fe(&udfNodeT{}, fe, 248, int64(len(vat)), 0, m.ad(false, 0, len(vat), vblk, 0), 4))
		maps = append(pm1, pm2("*UDF Virtual Partition")...)
	case "spare":
		// Packets of 4 blocks; the first one (the FSD, the root) moved after
		// the partition, garbage left in its place
		for len(m.sp[0])%(4*ss) != 0 {
			m.alloc(0, 1)
		}
		plen := len(m.sp[0]) / ss
		spareSec := m.pstart + plen
		tail = append(tail, m.sp[0][:4*ss]...)
		for i := range 4 * ss {
			m.sp[0][i] = 0xDB
		}
		tblSec := spareSec + 4
		tbl := make([]byte, ss)
		udfRegid(tbl[16:], "*UDF Sparing Table", byte(m.rev), byte(m.rev>>8))
		binary.LittleEndian.PutUint16(tbl[48:], 2)
		binary.LittleEndian.PutUint32(tbl[56:], 0)
		binary.LittleEndian.PutUint32(tbl[60:], uint32(spareSec))
		binary.LittleEndian.PutUint32(tbl[64:], 0xFFFFFFF0)
		binary.LittleEndian.PutUint32(tbl[68:], uint32(spareSec+100))
		udfTagT(tbl, 0, uint32(tblSec), 56+16-16)
		tail = append(tail, tbl...)
		p := pm2("*UDF Sparable Partition")
		binary.LittleEndian.PutUint16(p[40:], 4)
		p[42] = 1
		binary.LittleEndian.PutUint32(p[44:], uint32(ss))
		binary.LittleEndian.PutUint32(p[48:], uint32(tblSec))
		maps = p
	}
	plen := len(m.sp[0]) / ss
	total := m.pstart + plen + len(tail)/ss
	if m.mode != "vat" {
		total++ // the last anchor
	}
	img := make([]byte, total*ss)
	copy(img[m.pstart*ss:], m.sp[0])
	copy(img[(m.pstart+plen)*ss:], tail)
	for i, id := range []string{"BEA01", m.nsr(), "TEA01"} {
		v := img[udfVRSOffset+i*max(2048, ss):]
		copy(v[1:], id)
		v[6] = 1
	}
	// The volume descriptor sequences: PVD, PD, LVD, TD
	vds := func(start int) {
		sec := func(i int) []byte { return img[(start+i)*ss : (start+i+1)*ss] }
		p := sec(0)
		udfDStr(p[24:56], m.label)
		udfTagT(p, udfTagPVD, uint32(start), 512-16)
		p = sec(1)
		binary.LittleEndian.PutUint32(p[16:], 1)
		binary.LittleEndian.PutUint16(p[20:], 1)
		udfRegid(p[24:], "+"+m.nsr())
		binary.LittleEndian.PutUint32(p[184:], 1)
		binary.LittleEndian.PutUint32(p[188:], uint32(m.pstart))
		binary.LittleEndian.PutUint32(p[192:], uint32(plen))
		udfTagT(p, udfTagPD, uint32(start+1), 512-16)
		p = sec(2)
		binary.LittleEndian.PutUint32(p[16:], 2)
		udfDStr(p[84:212], m.label)
		binary.LittleEndian.PutUint32(p[212:], uint32(ss))
		udfRegid(p[216:], "*OSTA UDF Compliant", byte(m.rev), byte(m.rev>>8))
		copy(p[248:], m.ad(true, 0, ss, fsd, m.mref))
		binary.LittleEndian.PutUint32(p[264:], uint32(len(maps)))
		binary.LittleEndian.PutUint32(p[268:], uint32(1+min(len(maps)/64, 1)))
		if m.mode == "spare" {
			binary.LittleEndian.PutUint32(p[268:], 1)
		}
		copy(p[440:], maps)
		udfTagT(p, udfTagLVD, uint32(start+2), 440+len(maps)-16)
		udfTagT(sec(3), udfTagTD, uint32(start+3), 512-16)
	}
	vds(32)
	vds(48)
	m.mainVDS, m.lvdSec = 32, 34
	anchor := func(sec int) {
		a := img[sec*ss:]
		binary.LittleEndian.PutUint32(a[16:], uint32(4*ss))
		binary.LittleEndian.PutUint32(a[20:], 32)
		binary.LittleEndian.PutUint32(a[24:], uint32(4*ss))
		binary.LittleEndian.PutUint32(a[28:], 48)
		udfTagT(a, udfTagAVDP, uint32(sec), 512-16)
	}
	anchor(256)
	m.avdp = 256
	if m.mode != "vat" {
		anchor(total - 1)
	}
	return img
}

func (m *udfBuilder) nsr() string {
	if m.rev < 0x200 {
		return "NSR02"
	}
	return "NSR03"
}

func udfTestTree() *udfNodeT {
	pattern := func(n int, k byte) []byte {
		b := make([]byte, n)
		for i := range b {
			b[i] = byte(i*7) ^ k
		}
		return b
	}
	return &udfNodeT{dir: true, kids: []*udfNodeT{
		{name: "hello.txt", data: []byte("hello world")},
		{name: "Sub Dir", dir: true, efe: true, kids: []*udfNodeT{
			{name: "deeper", dir: true, kids: []*udfNodeT{
				{name: "Data File.bin", data: pattern(10000, 1), parts: 3, aed: true},
			}},
			{name: "embedded.txt", data: []byte("data inside the file entry"), embed: true},
			{name: "Ünïcödé ✓ 名前.txt", data: []byte("sixteen bits")},
		}},
		{name: "holey.bin", data: pattern(9000, 2), parts: 3, hole: true},
		{name: "empty", data: nil},
		{name: "long.bin", data: pattern(5000, 3), parts: 2, long: true, efe: true},
		{name: "new.txt", data: []byte("the newest entry"), ind: true},
		{name: "Latin é.txt", data: []byte("latin-1")},
	}}
}

// udfExpect lists the files (holey.bin only listed: checked apart) and the
// directories of a tree
func udfExpect(root *udfNodeT) (map[string]string, []string) {
	files := map[string]string{}
	var dirs []string
	var walk func(n *udfNodeT, p string)
	walk = func(n *udfNodeT, p string) {
		for _, k := range n.kids {
			kp := fatJoin(p, k.name)
			if k.dir {
				dirs = append(dirs, kp)
				walk(k, kp)
			} else if k.hole {
				files[kp] = isoListed
			} else {
				files[kp] = string(k.data)
			}
		}
	}
	walk(root, "/")
	return files, dirs
}

func udfCheck(t *testing.T, m *udfBuilder, img []byte, wantType string) FS {
	t.Helper()
	fsys, err := Open(bytes.NewReader(img), int64(len(img)))
	if err != nil {
		t.Fatal(err)
	}
	if fsys.Type() != wantType || fsys.Label() != m.label {
		t.Fatalf("%q %q", fsys.Type(), fsys.Label())
	}
	files, dirs := udfExpect(m.root)
	fatCheckFS(t, fsys, img, files, dirs)

	// The file with a hole: zeros there, the extent not on the image
	var holey *udfNodeT
	for _, k := range m.root.kids {
		if k.hole {
			holey = k
		}
	}
	r, n, err := fsys.Open("/holey.bin")
	if err != nil || n != int64(len(holey.data)) {
		t.Fatalf("holey: %d %v", n, err)
	}
	got := make([]byte, n)
	if _, err := r.ReadAt(got, 0); err != nil || !bytes.Equal(got, holey.data) {
		t.Fatalf("holey contents: %v", err)
	}
	ext, _ := fsys.Extents("/holey.bin")
	holes := 0
	for _, x := range ext {
		if x.DiskOff < 0 {
			holes++
		}
	}
	if holes != 1 || ext[0].DiskOff < 0 || ext[len(ext)-1].DiskOff < 0 {
		t.Fatalf("holey extents %+v", ext)
	}
	if ext, _ := fsys.Extents("/Sub Dir/deeper/Data File.bin"); len(ext) < 3 {
		t.Fatalf("multi-extent: %+v", ext)
	}
	e, err := fsys.Stat("/hello.txt")
	if err != nil || !e.ModTime.Equal(udfTestTime) || e.Mode != 0o100644 {
		t.Fatalf("hello: %+v %v", e, err)
	}
	if e, _ := fsys.Stat("/sub dir/DEEPER"); !e.IsDir || e.Mode != 0o40755 {
		t.Fatalf("case-insensitive dir: %+v", e)
	}
	return fsys
}

func TestUDFSynthetic(t *testing.T) {
	for _, c := range []struct {
		name string
		ss   int
		rev  uint16
		mode string
		typ  string
	}{
		{"1.02", 2048, 0x0102, "", "UDF 1.02"},
		{"2.01-512", 512, 0x0201, "", "UDF 2.01"},
		{"2.01-4096", 4096, 0x0201, "", "UDF 2.01"},
		{"2.50-metadata", 2048, 0x0250, "meta", "UDF 2.50 (metadata)"},
		{"2.60-metadata-512", 512, 0x0260, "meta", "UDF 2.60 (metadata)"},
		{"2.01-VAT", 2048, 0x0201, "vat", "UDF 2.01 (VAT)"},
		{"1.50-sparable", 2048, 0x0150, "spare", "UDF 1.50 (sparable)"},
	} {
		t.Run(c.name, func(t *testing.T) {
			m := &udfBuilder{ss: c.ss, rev: c.rev, mode: c.mode, label: "Ünï Volume", root: udfTestTree(), pstart: 257}
			img := m.finish()
			if c.mode == "vat" {
				m.label += " VAT" // the label of the VAT wins
			}
			fsys := udfCheck(t, m, img, c.typ)
			// The embedded data is shown where it is: in the file entry
			ext, err := fsys.Extents("/Sub Dir/embedded.txt")
			if err != nil || len(ext) != 1 || string(img[ext[0].DiskOff:ext[0].DiskOff+ext[0].Len]) != "data inside the file entry" {
				t.Fatalf("embedded: %+v %v", ext, err)
			}
			if all := OpenAll(bytes.NewReader(img), int64(len(img))); len(all) != 1 {
				t.Fatalf("OpenAll: %d", len(all))
			}
		})
	}
}

// The main descriptor sequence or the first anchor damaged: the reserve
// sequence, the last anchor are used
func TestUDFRecovery(t *testing.T) {
	m := &udfBuilder{ss: 2048, rev: 0x0201, label: "REC", root: udfTestTree(), pstart: 257}
	img := m.finish()
	b := bytes.Clone(img)
	b[m.lvdSec*2048+100] ^= 0xFF // the CRC of the main LVD
	udfCheck(t, m, b, "UDF 2.01")
	b = bytes.Clone(img)
	b[m.avdp*2048+4] ^= 0xFF // the tag checksum of the first anchor
	udfCheck(t, m, b, "UDF 2.01")
	for i := 0; i < 4; i++ {
		b[(m.mainVDS+i)*2048+20] ^= 0xFF
	}
	udfCheck(t, m, b, "UDF 2.01")

	// Metadata: the main metadata file damaged, the mirror read
	m = &udfBuilder{ss: 2048, rev: 0x0250, mode: "meta", label: "META", root: udfTestTree(), pstart: 257}
	img = m.finish()
	fsys := udfCheck(t, m, img, "UDF 2.50 (metadata)")
	meta := fsys.(*udfFS).parts[1]
	mainFE := binary.LittleEndian.Uint32(img[m.lvdSec*2048+440+6+40:])
	img[(257+int(mainFE))*2048+4] ^= 0xFF
	if meta.kind != udfMeta {
		t.Fatal("no metadata partition")
	}
	udfCheck(t, m, img, "UDF 2.50 (metadata)")
}

// Damaged descriptors give errors, never a panic or a loop
func TestUDFDamaged(t *testing.T) {
	m := &udfBuilder{ss: 2048, rev: 0x0201, label: "BAD", root: udfTestTree(), pstart: 257}
	img := m.finish()
	open := func(b []byte) FS {
		t.Helper()
		fsys, err := Open(bytes.NewReader(b), int64(len(b)))
		if err != nil {
			t.Fatal(err)
		}
		return fsys
	}
	at := func(blk uint32) int { return (257 + int(blk)) * 2048 }
	kid := func(n *udfNodeT, name string) *udfNodeT {
		for _, k := range n.kids {
			if k.name == name {
				return k
			}
		}
		t.Fatalf("no %s", name)
		return nil
	}
	sub := kid(m.root, "Sub Dir")

	// A file identifier with a wrong CRC: the entries before it are listed
	b := bytes.Clone(img)
	b[257*2048+int(sub.fidOff[1])+40] ^= 0xFF
	list, err := open(b).ReadDir("/Sub Dir")
	if !errors.Is(err, errUDFTag) || len(list) != 1 || list[0].Name != "deeper" {
		t.Fatalf("damaged FID: %v %v", list, err)
	}

	// A file entry with a wrong checksum: listed, not readable
	b = bytes.Clone(img)
	hello := kid(m.root, "hello.txt")
	b[at(hello.feBlk)+4] ^= 0xFF
	fsys := open(b)
	list, err = fsys.ReadDir("/")
	if !errors.Is(err, errUDFTag) || len(list) != len(m.root.kids) || !strings.Contains(err.Error(), "/hello.txt") {
		t.Fatalf("damaged FE: %d %v", len(list), err)
	}
	if _, _, err := fsys.Open("/hello.txt"); !errors.Is(err, errUDFTag) {
		t.Fatalf("open: %v", err)
	}
	if _, _, err := fsys.Open("/long.bin"); err != nil {
		t.Fatalf("others: %v", err)
	}

	// A file entry written for another block (moved)
	b = bytes.Clone(img)
	copy(b[at(hello.feBlk):at(hello.feBlk)+2048], b[at(kid(m.root, "long.bin").feBlk):])
	if _, err := open(b).Stat("/hello.txt"); err != nil {
		t.Fatalf("stat: %v", err) // the directory is read; the entry is marked bad
	}
	if _, _, err := open(b).Open("/hello.txt"); err == nil || !strings.Contains(err.Error(), "written for block") {
		t.Fatalf("moved FE: %v", err)
	}

	// A directory inside itself: the FID of "deeper" points to the root
	b = bytes.Clone(img)
	off := 257*2048 + int(sub.fidOff[0])
	binary.LittleEndian.PutUint32(b[off+24:], m.root.icb)
	udfTagT(b[off:], udfTagFID, binary.LittleEndian.Uint32(b[off+12:]), int(binary.LittleEndian.Uint16(b[off+10:])))
	list, err = open(b).ReadDir("/Sub Dir")
	if err == nil || !strings.Contains(err.Error(), "inside itself") || len(list) != 2 {
		t.Fatalf("cycle: %v %v", list, err)
	}

	// An allocation extent that goes on in itself
	b = bytes.Clone(img)
	data := kid(kid(sub, "deeper"), "Data File.bin")
	aed := b[at(data.aedBlk):]
	l := binary.LittleEndian.Uint32(aed[20:])
	binary.LittleEndian.PutUint32(aed[24:], 3<<30|2048) // the first AD: a continuation to itself
	binary.LittleEndian.PutUint32(aed[28:], data.aedBlk)
	udfTagT(aed, udfTagAED, data.aedBlk, 8+int(l))
	if _, err := open(b).Extents("/Sub Dir/deeper/Data File.bin"); err == nil || !strings.Contains(err.Error(), "loop") {
		t.Fatalf("AED loop: %v", err)
	}

	// Indirect entries going round: the last good entry is used
	b = bytes.Clone(img)
	nw := kid(m.root, "new.txt")
	ie := b[at(nw.icb+1):]
	binary.LittleEndian.PutUint32(ie[40:], nw.icb)
	udfTagT(ie, udfTagIE, nw.icb+1, 36)
	if r, n, err := open(b).Open("/new.txt"); err != nil || n != 3 {
		t.Fatalf("IE loop: %d %v", n, err)
	} else if p := make([]byte, 3); true {
		r.ReadAt(p, 0)
		if string(p) != "old" {
			t.Fatalf("IE loop: %q", p)
		}
	}

	// An extent past the end of the image
	b = bytes.Clone(img)
	fe := b[at(hello.feBlk):]
	binary.LittleEndian.PutUint32(fe[176+4:], 1<<20)
	udfTagT(fe, udfTagFE, hello.feBlk, 176+8-16)
	if _, err := open(b).Extents("/hello.txt"); err == nil {
		t.Fatal("extent past the end")
	}

	// No anchor at all: an error, and ISO 9660 is not claimed
	b = bytes.Clone(img)
	b[256*2048+4] ^= 0xFF
	b[len(b)-2048+4] ^= 0xFF
	if _, err := Open(bytes.NewReader(b), int64(len(b))); err == nil || !strings.Contains(err.Error(), "anchor") {
		t.Fatalf("no anchor: %v", err)
	}

	// Cuts and flipped bytes
	for _, n := range []int{32768 + 2048, 256 * 2048, 257*2048 + 100, len(img) / 2, len(img) - 1} {
		if fsys, err := Open(bytes.NewReader(img[:n]), int64(n)); err == nil {
			budget := 1000
			fatWalk(fsys, "/", 0, &budget)
		}
	}
	for off := 32768; off < len(img); off += 509 {
		b := bytes.Clone(img)
		b[off] ^= 0xFF
		if fsys, err := Open(bytes.NewReader(b), int64(len(b))); err == nil {
			budget := 1000
			fatWalk(fsys, "/", 0, &budget)
		}
	}
}

// ---- Real images made by genisoimage / mkisofs -udf ----

func TestUDFTools(t *testing.T) {
	tool := fatTool(t, "genisoimage", "mkisofs")
	src := t.TempDir()
	big := make([]byte, 300000)
	for i := range big {
		big[i] = byte(i*13 + i>>8)
	}
	files := map[string]string{
		"/readme.txt":                           "read me",
		"/Mixed Case Dir/Nested/deep file.text": "deep",
		"/ünïcödé ✓ 名前.txt":                     "unicode",
		"/empty.dat":                            "",
		"/big.bin":                              string(big),
	}
	for p, d := range files {
		full := filepath.Join(src, p)
		os.MkdirAll(filepath.Dir(full), 0o755)
		if err := os.WriteFile(full, []byte(d), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dirs := []string{"/Mixed Case Dir", "/Mixed Case Dir/Nested"}
	img := filepath.Join(t.TempDir(), "x.iso")
	if out, err := exec.Command(tool, "-quiet", "-udf", "-R", "-J", "-joliet-long", "-V", "UDFTOOL", "-o", img, src).CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	b, err := os.ReadFile(img)
	if err != nil {
		t.Fatal(err)
	}
	fsys, err := Open(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatal(err)
	}
	if fsys.Type() != "UDF 1.02 + ISO 9660" || fsys.Label() != "UDFTOOL" {
		t.Fatalf("%q %q", fsys.Type(), fsys.Label())
	}
	fatCheckFS(t, fsys, b, files, dirs)
	if e, _ := fsys.Stat("/readme.txt"); e.ModTime.IsZero() || e.Mode&0o170000 != 0o100000 {
		t.Fatalf("readme: %+v", e)
	}
	// Both file systems of the hybrid image, their files at the same place
	all := OpenAll(bytes.NewReader(b), int64(len(b)))
	if len(all) != 2 || !strings.HasPrefix(all[1].Type(), "ISO 9660") {
		t.Fatalf("OpenAll: %d", len(all))
	}
	x1, _ := all[0].Extents("/big.bin")
	x2, _ := all[1].Extents("/big.bin")
	if len(x1) != 1 || len(x2) != 1 || x1[0] != x2[0] {
		t.Fatalf("extents %+v %+v", x1, x2)
	}
	// UDF damaged: Open gives the ISO 9660 of the image
	for _, sec := range []int64{256, int64(len(b))/2048 - 1, int64(len(b))/2048 - 257} {
		b[sec*2048+4] ^= 0xFF // the tag checksums of the anchors
	}
	fsys, err = Open(bytes.NewReader(b), int64(len(b)))
	if err != nil || !strings.HasPrefix(fsys.Type(), "ISO 9660") {
		t.Fatalf("fallback: %v", err)
	}
}

func FuzzUDF(f *testing.F) {
	tree := func() *udfNodeT {
		return &udfNodeT{dir: true, kids: []*udfNodeT{
			{name: "a.txt", data: []byte("hello")},
			{name: "multi", data: make([]byte, 3000), parts: 2, aed: true},
			{name: "d", dir: true, efe: true, kids: []*udfNodeT{
				{name: "e", data: []byte("embedded"), embed: true},
				{name: "名", data: []byte("x"), ind: true},
			}},
		}}
	}
	for _, c := range []struct {
		mode string
		rev  uint16
		ss   int
	}{{"", 0x0102, 512}, {"meta", 0x0250, 512}, {"vat", 0x0201, 512}, {"spare", 0x0150, 512}} {
		m := &udfBuilder{ss: c.ss, rev: c.rev, mode: c.mode, label: "F", root: tree(), pstart: 257}
		f.Add(m.finish())
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		defer fuzzguard.Start(t.Name())()
		fsys, err := Open(bytes.NewReader(b), int64(len(b)))
		if err != nil || fsys == nil {
			return
		}
		fsys.Type()
		fsys.Label()
		budget := 2000
		fatWalk(fsys, "/", 0, &budget)
	})
}
