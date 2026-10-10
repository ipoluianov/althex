package fs

// UDF: the Universal Disk Format of DVDs, Blu-ray discs and packet-written
// media (ECMA-167 3rd edition, OSTA UDF 1.02-2.60). The volume recognition
// sequence (BEA01, NSR02/NSR03) at 32 KB, the anchor at sector 256 (or 256
// sectors before the end, or the last sector), the main volume descriptor
// sequence (the reserve one when the main is damaged): the logical volume
// with its partition maps - type 1 (physical), and type 2: virtual (the VAT
// of CD-R/DVD-R), sparable (CD-RW/DVD-RW), metadata (UDF 2.50+, BD) - the
// file set descriptor, file entries and extended file entries (with the
// indirect entries of strategy 4096), short, long and extended allocation
// descriptors with their continuations, data embedded in the entry, file
// identifiers with the OSTA compressed Unicode names.
//
// Every descriptor's tag is checked: its checksum, the CRC of the bytes it
// covers and the location it was written for; a damaged one is an error.
// Chains of indirect entries and of allocation extents, directory cycles,
// the numbers of descriptors, entries and blocks are capped: images may be
// hostile (6.3).

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode/utf16"
)

const (
	// udfVRSOffset is where the volume recognition sequence starts
	udfVRSOffset = 32768
	// udfMaxVRS caps the descriptors of the recognition sequence looked at
	udfMaxVRS = 64
	// udfMaxVDS caps the descriptors of a volume descriptor sequence read
	udfMaxVDS = 256
	// udfMaxVDP caps the volume descriptor pointers followed
	udfMaxVDP = 8
	// udfMaxMaps caps the partition maps of a logical volume
	udfMaxMaps = 64
	// udfMaxICBChain caps the indirect entries followed to a file entry
	udfMaxICBChain = 64
	// udfMaxAEDs caps the allocation extents (continuations) of a file
	udfMaxAEDs = 4096
	// udfMaxADs caps the allocation descriptors of a file
	udfMaxADs = 1 << 20
	// udfMaxPieces caps the steps of mapping the blocks of a file to the
	// image (VAT, sparing, metadata partitions map them one by one)
	udfMaxPieces = 1 << 22
	// udfMaxDir caps the size of a directory that is read (64 MB)
	udfMaxDir = 64 << 20
	// udfMaxDepth caps the depth of directories
	udfMaxDepth = 256
	// udfMaxVAT caps the size of the virtual allocation table (64 MB)
	udfMaxVAT = 64 << 20
	// udfVATSearch is how many sectors from the end are searched for the
	// file entry of the VAT
	udfVATSearch = 256
)

// Tag identifiers
const (
	udfTagPVD  = 1
	udfTagAVDP = 2
	udfTagVDP  = 3
	udfTagPD   = 5
	udfTagLVD  = 6
	udfTagTD   = 8
	udfTagFSD  = 256
	udfTagFID  = 257
	udfTagAED  = 258
	udfTagIE   = 259
	udfTagFE   = 261
	udfTagEFE  = 266
)

// Kinds of partitions
const (
	udfPhys = iota
	udfVirtual
	udfSparable
	udfMeta
	udfUnknown
)

var (
	errUDFTag = errors.New("damaged UDF descriptor")
	errUDF    = errors.New("damaged UDF volume")
)

func init() { registerFirst(probeUDF) }

// udfLBA is the address of a logical block: the block in a partition and the
// partition's reference number (its index among the partition maps)
type udfLBA struct {
	block uint32
	part  uint16
}

func (a udfLBA) key() uint64 { return uint64(a.part)<<32 | uint64(a.block) }

type udfPart struct {
	kind   int
	num    uint16 // the partition number of its descriptor
	start  int64  // the first sector (physical, sparable)
	length int64  // in blocks (physical, sparable)
	under  *udfPart
	vat    []uint32          // virtual: the physical block of each virtual one
	meta   []Extent          // metadata: the runs of the metadata file on the image
	packet int64             // sparable: blocks in a packet
	spare  map[uint32]uint32 // sparable: packets moved (the first block -> the sector)
}

type udfFS struct {
	r     io.ReaderAt
	size  int64
	ss    int64 // the sector size
	bs    int64 // the logical block size
	label string
	typ   string
	parts []*udfPart
	root  udfEntry
	dirs  fatDirs[udfDir]
}

// udfDir is a directory read: damaged ones are kept with their error, so
// that their entries are not read again for each lookup
type udfDir struct {
	list []udfEntry
	err  error
}

type udfEntry struct {
	Entry
	icb udfLBA
	anc []uint64 // the ICBs of a directory and of the ones it is in: cycles
	bad error    // its file entry could not be read
}

// udfFE is what is kept of a file entry
type udfFE struct {
	loc      udfLBA
	diskOff  int64 // where its block is on the image
	fileType byte
	flags    uint16 // of the ICB tag: the type of allocation descriptors...
	size     int64
	mtime    time.Time
	perm     uint32
	adOff    int // where the allocation descriptors are in the block
	ad       []byte
}

// probeUDF looks for the recognition sequence and reads the volume
func probeUDF(r io.ReaderAt, size int64) (FS, bool, error) {
	if !udfVRS(r) {
		return nil, false, nil
	}
	f := &udfFS{r: r, size: size, ss: isoSector, bs: isoSector, typ: "UDF"}
	f.root = udfEntry{Entry: Entry{Name: "/", Path: "/", IsDir: true}}
	return f, true, f.mount()
}

// udfVRS tells whether the volume recognition sequence has an NSR
// descriptor; its descriptors take 2048 bytes, or a sector of 4096
func udfVRS(r io.ReaderAt) bool {
	b := make([]byte, 7)
	for _, step := range []int64{2048, 4096} {
		for i := int64(0); i < udfMaxVRS; i++ {
			if fatReadAt(r, b, udfVRSOffset+i*step) != nil {
				break
			}
			switch string(b[1:6]) {
			case "NSR02", "NSR03":
				return true
			case "BEA01", "TEA01", "CD001", "CDW02", "BOOT2":
				continue
			}
			break
		}
	}
	return false
}

// udfCheckTag checks the tag of the descriptor at the start of b: its
// checksum, its version, the CRC of the bytes it covers and, when loc is
// not negative, the location it was written for. It returns the tag
// identifier.
func udfCheckTag(b []byte, loc int64) (uint16, error) {
	if len(b) < 16 {
		return 0, fmt.Errorf("%w: cut", errUDFTag)
	}
	var sum byte
	for i := 0; i < 16; i++ {
		if i != 4 {
			sum += b[i]
		}
	}
	if sum != b[4] {
		return 0, fmt.Errorf("%w: tag checksum 0x%02X, the tag sums to 0x%02X", errUDFTag, b[4], sum)
	}
	id := binary.LittleEndian.Uint16(b)
	if v := binary.LittleEndian.Uint16(b[2:]); v != 2 && v != 3 {
		return id, fmt.Errorf("%w: descriptor version %d", errUDFTag, v)
	}
	// A CRC over more than there is can not be checked (Linux skips it too)
	if n := int(binary.LittleEndian.Uint16(b[10:])); 16+n <= len(b) {
		if c := udfCRC(b[16 : 16+n]); c != binary.LittleEndian.Uint16(b[8:]) {
			return id, fmt.Errorf("%w: tag %d: CRC 0x%04X, the data has 0x%04X", errUDFTag, id, binary.LittleEndian.Uint16(b[8:]), c)
		}
	}
	if at := binary.LittleEndian.Uint32(b[12:]); loc >= 0 && int64(at) != loc {
		return id, fmt.Errorf("%w: tag %d written for block %d, found at %d", errUDFTag, id, at, loc)
	}
	return id, nil
}

// udfCRC is the CRC-ITU-T of the descriptors (polynomial 0x1021, from 0)
func udfCRC(b []byte) uint16 {
	var c uint16
	for _, v := range b {
		c = c<<8 ^ udfCRCTable[byte(c>>8)^v]
	}
	return c
}

var udfCRCTable = func() (t [256]uint16) {
	for i := range t {
		c := uint16(i) << 8
		for range 8 {
			if c&0x8000 != 0 {
				c = c<<1 ^ 0x1021
			} else {
				c <<= 1
			}
		}
		t[i] = c
	}
	return t
}()

// udfChars decodes OSTA compressed Unicode: the first byte is 8 (a byte a
// character) or 16 (big-endian UTF-16); 254 and 255 are the same
func udfChars(b []byte) string {
	if len(b) < 1 {
		return ""
	}
	switch b[0] {
	case 16, 255:
		u := make([]uint16, (len(b)-1)/2)
		for i := range u {
			u[i] = binary.BigEndian.Uint16(b[1+2*i:])
		}
		return string(utf16.Decode(u))
	default:
		r := make([]rune, len(b)-1)
		for i, c := range b[1:] {
			r[i] = rune(c)
		}
		return string(r)
	}
}

// udfDString decodes a dstring: compressed Unicode whose used length is in
// its last byte
func udfDString(b []byte) string {
	if len(b) < 2 {
		return ""
	}
	n := min(int(b[len(b)-1]), len(b)-1)
	return strings.TrimRight(udfChars(b[:n]), "\x00 ")
}

// udfTime decodes a timestamp; the time zone is in minutes (-2047: none)
func udfTime(b []byte) time.Time {
	tz := binary.LittleEndian.Uint16(b)
	year := int(int16(binary.LittleEndian.Uint16(b[2:])))
	if year == 0 && b[4] == 0 {
		return time.Time{}
	}
	loc := time.UTC
	if mins := int(int16(tz<<4) >> 4); tz>>12 == 1 && mins != -2047 && mins >= -1440 && mins <= 1440 {
		loc = time.FixedZone("", mins*60)
	}
	ns := int(b[9])*10_000_000 + int(b[10])*100_000 + int(b[11])*1000
	return time.Date(year, time.Month(b[4]), int(b[5]), int(b[6]), int(b[7]), int(b[8]), ns, loc)
}

// ---- Mounting ----

func (f *udfFS) mount() error {
	avdp, err := f.anchor()
	if err != nil {
		return err
	}
	// The main sequence, the reserve one when the main is damaged
	for i, at := range []int{16, 24} {
		n := int64(binary.LittleEndian.Uint32(avdp[at:]))
		loc := int64(binary.LittleEndian.Uint32(avdp[at+4:]))
		lvd, pds, e := f.readVDS(loc, n)
		if e == nil {
			f.parts = nil
			if e = f.readLVD(lvd, pds); e == nil {
				return nil
			}
		}
		if i == 0 {
			err = e
		}
	}
	return err
}

// anchor finds the anchor volume descriptor pointer and so the sector size
func (f *udfFS) anchor() ([]byte, error) {
	b := make([]byte, 512)
	sizes := []int64{2048, 512, 4096, 1024}
	try := func(ss, loc int64) bool {
		if loc < 256 || (loc+1)*ss > f.size || fatReadAt(f.r, b, loc*ss) != nil {
			return false
		}
		if id, err := udfCheckTag(b, loc); err != nil || id != udfTagAVDP {
			return false
		}
		f.ss, f.bs = ss, ss
		return true
	}
	for _, ss := range sizes {
		if try(ss, 256) {
			return b, nil
		}
	}
	for _, ss := range sizes {
		n := f.size / ss
		if try(ss, n-1) || try(ss, n-257) || try(ss, n-256) {
			return b, nil
		}
	}
	return nil, fmt.Errorf("%w: no anchor volume descriptor pointer", errUDF)
}

// readVDS reads a volume descriptor sequence of n bytes at sector loc: the
// logical volume descriptor and the partition descriptors by number (of
// each the one with the highest sequence number)
func (f *udfFS) readVDS(loc, n int64) (lvd []byte, pds map[uint16][]byte, err error) {
	pds = map[uint16][]byte{}
	seq := map[uint16]uint32{}
	var lvdSeq uint32
	var firstErr error
	count, vdp := int64(n/f.ss), 0
	if count < 1 {
		count = 1
	}
	for i, total := int64(0), 0; i < count && total < udfMaxVDS; i, total = i+1, total+1 {
		sec := loc + i
		b := make([]byte, f.ss)
		if fatReadAt(f.r, b, sec*f.ss) != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("%w: volume descriptor sequence at sector %d past the end", errUDF, sec)
			}
			break
		}
		id, e := udfCheckTag(b, sec)
		if e != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("volume descriptor at sector %d: %w", sec, e)
			}
			continue
		}
		vdsn := binary.LittleEndian.Uint32(b[16:])
		switch id {
		case udfTagVDP:
			if vdp++; vdp > udfMaxVDP {
				return nil, nil, fmt.Errorf("%w: too many volume descriptor pointers", errUDF)
			}
			l := int64(binary.LittleEndian.Uint32(b[20:]))
			loc, count, i = int64(binary.LittleEndian.Uint32(b[24:])), max(l/f.ss, 1), -1
		case udfTagPD:
			num := binary.LittleEndian.Uint16(b[22:])
			if old, ok := seq[num]; !ok || vdsn >= old {
				pds[num], seq[num] = b, vdsn
			}
		case udfTagLVD:
			if lvd == nil || vdsn >= lvdSeq {
				lvd, lvdSeq = b, vdsn
			}
		case udfTagTD:
			i = count
		}
	}
	if lvd == nil || len(pds) == 0 {
		if firstErr != nil {
			return nil, nil, firstErr
		}
		return nil, nil, fmt.Errorf("%w: no logical volume or partition descriptor", errUDF)
	}
	return lvd, pds, nil
}

// readLVD reads the logical volume: its partition maps, the file set
// descriptor and the root directory
func (f *udfFS) readLVD(lvd []byte, pds map[uint16][]byte) error {
	if bs := int64(binary.LittleEndian.Uint32(lvd[212:])); bs >= 512 && bs <= 65536 && bs&(bs-1) == 0 {
		f.bs = bs
	} else {
		return fmt.Errorf("%w: logical block size %d", errUDF, bs)
	}
	f.label = udfDString(lvd[84:212])
	rev := binary.LittleEndian.Uint16(lvd[216+24:])
	mtl := int64(binary.LittleEndian.Uint32(lvd[264:]))
	npm := int64(binary.LittleEndian.Uint32(lvd[268:]))
	if mtl > int64(len(lvd))-440 {
		return fmt.Errorf("%w: partition maps of %d bytes past the descriptor", errUDF, mtl)
	}
	if npm > udfMaxMaps {
		return fmt.Errorf("%w: %d partition maps", errUDF, npm)
	}
	maps := lvd[440 : 440+mtl]
	type pending struct {
		p *udfPart
		m []byte
	}
	var later []pending
	phys := func(num uint16) (*udfPart, error) {
		pd, ok := pds[num]
		if !ok {
			return nil, fmt.Errorf("%w: no descriptor of partition %d", errUDF, num)
		}
		return &udfPart{kind: udfPhys, num: num,
			start:  int64(binary.LittleEndian.Uint32(pd[188:])),
			length: int64(binary.LittleEndian.Uint32(pd[192:]))}, nil
	}
	for i := int64(0); i < npm; i++ {
		if len(maps) < 2 || maps[1] < 2 || int(maps[1]) > len(maps) {
			return fmt.Errorf("%w: partition map %d", errUDF, i)
		}
		m := maps[:maps[1]]
		maps = maps[maps[1]:]
		switch {
		case m[0] == 1 && len(m) >= 6:
			p, err := phys(binary.LittleEndian.Uint16(m[4:]))
			if err != nil {
				return err
			}
			f.parts = append(f.parts, p)
		case m[0] == 2 && len(m) >= 64:
			id := strings.TrimRight(string(m[5:28]), "\x00 ")
			p, err := phys(binary.LittleEndian.Uint16(m[38:]))
			if err != nil {
				return err
			}
			switch id {
			case "*UDF Virtual Partition":
				p.kind = udfVirtual
			case "*UDF Sparable Partition":
				p.kind = udfSparable
			case "*UDF Metadata Partition":
				p.kind = udfMeta
			default:
				// Unknown: its references fail, the others work
				p.kind = udfUnknown
			}
			f.parts = append(f.parts, p)
			if p.kind != udfUnknown {
				later = append(later, pending{p, m})
			}
		default:
			return fmt.Errorf("%w: partition map %d of type %d", errUDF, i, m[0])
		}
	}
	// Virtual and metadata partitions lie in a physical one of their number
	under := func(p *udfPart) uint16 {
		for i, q := range f.parts {
			if (q.kind == udfPhys || q.kind == udfSparable) && q.num == p.num {
				return uint16(i)
			}
		}
		q := *p
		q.kind = udfPhys
		f.parts = append(f.parts, &q)
		return uint16(len(f.parts) - 1)
	}
	var kinds []string
	for _, x := range later {
		p, m := x.p, x.m
		switch p.kind {
		case udfSparable:
			p.packet = int64(binary.LittleEndian.Uint16(m[40:]))
			if p.packet == 0 {
				p.packet = 32
			}
			f.readSparing(p, m)
			kinds = append(kinds, "sparable")
		case udfVirtual:
			u := under(p)
			p.under = f.parts[u]
			if err := f.readVAT(p, u); err != nil {
				return err
			}
			kinds = append(kinds, "VAT")
		case udfMeta:
			u := under(p)
			p.under = f.parts[u]
			var err error
			for _, at := range []int{40, 44} {
				icb := udfLBA{binary.LittleEndian.Uint32(m[at:]), u}
				var fe *udfFE
				if fe, err = f.readICB(icb); err == nil {
					if p.meta, err = f.extents(fe); err == nil {
						break
					}
				}
			}
			if err != nil {
				return fmt.Errorf("metadata file: %w", err)
			}
			kinds = append(kinds, "metadata")
		}
	}

	// The file set descriptor and the root directory
	fsd := udfLBA{binary.LittleEndian.Uint32(lvd[252:]), binary.LittleEndian.Uint16(lvd[256:])}
	b, _, err := f.readLB(fsd)
	if err != nil {
		return fmt.Errorf("file set descriptor: %w", err)
	}
	if id, err := udfCheckTag(b, int64(fsd.block)); err != nil || id != udfTagFSD {
		if err == nil {
			err = fmt.Errorf("%w: tag %d instead of a file set descriptor", errUDFTag, id)
		}
		return fmt.Errorf("file set descriptor: %w", err)
	}
	if f.label == "" {
		f.label = udfDString(b[112:240])
	}
	if rev == 0 {
		rev = binary.LittleEndian.Uint16(b[416+24:])
	}
	f.typ = "UDF"
	if rev != 0 {
		f.typ = fmt.Sprintf("UDF %x.%02x", rev>>8, rev&0xFF)
	}
	if len(kinds) > 0 {
		f.typ += " (" + strings.Join(kinds, ", ") + ")"
	}
	cd := make([]byte, 6)
	if fatReadAt(f.r, cd, 16*isoSector) == nil && string(cd[1:6]) == "CD001" {
		f.typ += " + ISO 9660"
	}
	root := udfLBA{binary.LittleEndian.Uint32(b[404:]), binary.LittleEndian.Uint16(b[408:])}
	fe, err := f.readICB(root)
	if err != nil {
		return fmt.Errorf("root directory: %w", err)
	}
	if fe.fileType != 4 {
		return fmt.Errorf("%w: the root is not a directory (file type %d)", errUDF, fe.fileType)
	}
	f.root.icb = root
	f.root.anc = []uint64{root.key()}
	f.fill(&f.root.Entry, fe)
	return nil
}

// readSparing reads the first good sparing table of a sparable partition
func (f *udfFS) readSparing(p *udfPart, m []byte) {
	n := min(int(m[42]), 4)
	size := int64(binary.LittleEndian.Uint32(m[44:]))
	size = min(max(size, 56), 56+8*65535)
	for i := 0; i < n; i++ {
		sec := int64(binary.LittleEndian.Uint32(m[48+4*i:]))
		b := make([]byte, (size+f.ss-1)/f.ss*f.ss)
		if fatReadAt(f.r, b, sec*f.ss) != nil {
			continue
		}
		if _, err := udfCheckTag(b, sec); err != nil || !strings.HasPrefix(string(b[17:40]), "*UDF Sparing Table") {
			continue
		}
		cnt := int(binary.LittleEndian.Uint16(b[48:]))
		p.spare = map[uint32]uint32{}
		for k := 0; k < cnt && 56+8*k+8 <= len(b); k++ {
			orig := binary.LittleEndian.Uint32(b[56+8*k:])
			if orig < 0xFFFFFFF0 {
				p.spare[orig] = binary.LittleEndian.Uint32(b[60+8*k:])
			}
		}
		return
	}
}

// readVAT finds the virtual allocation table: its file entry is in one of
// the last sectors (the last written one) of the physical partition u
func (f *udfFS) readVAT(p *udfPart, u uint16) error {
	ph := f.parts[u]
	last := f.size/f.ss - 1
	for s := last; s > last-udfVATSearch && s >= ph.start; s-- {
		fe, err := f.readICB(udfLBA{uint32(s - ph.start), u})
		if err != nil || fe.fileType != 248 && fe.fileType != 0 || fe.size > udfMaxVAT {
			continue
		}
		data, err := f.readAll(fe, udfMaxVAT)
		if err != nil {
			continue
		}
		var tbl []byte
		switch {
		case fe.fileType == 248 && len(data) >= 152:
			// UDF 2.00+: a header, its length first
			h := int(binary.LittleEndian.Uint16(data))
			if h < 152 || h > len(data) {
				continue
			}
			if l := udfDString(data[4:132]); l != "" {
				f.label = l
			}
			tbl = data[h:]
		case fe.fileType == 0 && len(data) >= 36 && string(data[len(data)-35:len(data)-13]) == "*UDF Virtual Alloc Tbl":
			// UDF 1.50: the table, then the identifier and the previous VAT
			tbl = data[:len(data)-36]
		default:
			continue
		}
		p.vat = make([]uint32, len(tbl)/4)
		for i := range p.vat {
			p.vat[i] = binary.LittleEndian.Uint32(tbl[4*i:])
		}
		return nil
	}
	return fmt.Errorf("%w: no virtual allocation table in the last %d sectors", errUDF, udfVATSearch)
}

// ---- Blocks ----

// mapOff gives where byte off of partition p is on the image and how many
// bytes from there go on contiguously in the partition
func (f *udfFS) mapOff(p *udfPart, off int64, depth int) (int64, int64, error) {
	if depth > 2 {
		return 0, 0, fmt.Errorf("%w: partitions inside partitions", errUDF)
	}
	if off < 0 {
		return 0, 0, fmt.Errorf("%w: offset %d", errUDF, off)
	}
	b, in := off/f.bs, off%f.bs
	switch p.kind {
	case udfUnknown:
		return 0, 0, fmt.Errorf("%w: a partition of an unknown type", errUDF)
	case udfVirtual:
		if b >= int64(len(p.vat)) || p.vat[b] == 0xFFFFFFFF {
			return 0, 0, fmt.Errorf("%w: virtual block %d not in the VAT", errUDF, b)
		}
		d, _, err := f.mapOff(p.under, int64(p.vat[b])*f.bs+in, depth+1)
		return d, f.bs - in, err
	case udfMeta:
		i := sort.Search(len(p.meta), func(i int) bool { return p.meta[i].FileOff+p.meta[i].Len > off })
		if i == len(p.meta) || p.meta[i].FileOff > off || p.meta[i].DiskOff < 0 {
			return 0, 0, fmt.Errorf("%w: block %d not in the metadata file", errUDF, b)
		}
		x := p.meta[i]
		return x.DiskOff + off - x.FileOff, x.FileOff + x.Len - off, nil
	}
	if b >= p.length {
		return 0, 0, fmt.Errorf("%w: block %d past partition %d (%d blocks)", errUDF, b, p.num, p.length)
	}
	n := p.length*f.bs - off
	d := p.start*f.ss + off
	if p.kind == udfSparable {
		first := b / p.packet * p.packet
		n = min(n, (first+p.packet)*f.bs-off)
		if m, ok := p.spare[uint32(first)]; ok {
			d = int64(m)*f.ss + off - first*f.bs
		}
	}
	return d, n, nil
}

// mapRun gives the pieces of the image that n bytes from a block of a
// partition are in, for the bytes of a file from fileOff; budget caps the
// steps
func (f *udfFS) mapRun(a udfLBA, fileOff, n int64, out []Extent, budget *int) ([]Extent, error) {
	if int(a.part) >= len(f.parts) {
		return out, fmt.Errorf("%w: partition reference %d of %d", errUDF, a.part, len(f.parts))
	}
	p := f.parts[a.part]
	off := int64(a.block) * f.bs
	for n > 0 {
		if *budget--; *budget < 0 {
			return out, fmt.Errorf("%w: too many pieces", errUDF)
		}
		d, c, err := f.mapOff(p, off, 0)
		if err != nil {
			return out, err
		}
		k := min(n, c)
		if k <= 0 || d < 0 || d > f.size || k > f.size-d {
			return out, fmt.Errorf("%w: data at 0x%X past the end of the image", io.ErrUnexpectedEOF, d)
		}
		if l := len(out) - 1; l >= 0 && out[l].DiskOff >= 0 && out[l].DiskOff+out[l].Len == d && out[l].FileOff+out[l].Len == fileOff {
			out[l].Len += k
		} else {
			out = append(out, Extent{FileOff: fileOff, DiskOff: d, Len: k})
		}
		n -= k
		fileOff += k
		off += k
	}
	return out, nil
}

// readLB reads a logical block; it returns its bytes and where they are
func (f *udfFS) readLB(a udfLBA) ([]byte, int64, error) {
	budget := 4
	ext, err := f.mapRun(a, 0, f.bs, nil, &budget)
	if err != nil {
		return nil, 0, err
	}
	b := make([]byte, f.bs)
	if err := fatReadAt(&fatRunReader{r: f.r, ext: ext, size: f.bs, valid: f.bs}, b, 0); err != nil {
		return nil, 0, err
	}
	return b, ext[0].DiskOff, nil
}

// ---- File entries ----

// readICB reads the file entry of an ICB, following indirect entries and
// the newer entries of strategy 4096; a chain going round is an error
func (f *udfFS) readICB(a udfLBA) (*udfFE, error) {
	seen := map[uint64]bool{}
	var best *udfFE
	for range udfMaxICBChain {
		if seen[a.key()] {
			break
		}
		seen[a.key()] = true
		b, off, err := f.readLB(a)
		if err != nil {
			if best != nil {
				return best, nil
			}
			return nil, err
		}
		id, err := udfCheckTag(b, int64(a.block))
		if err != nil {
			if best != nil {
				return best, nil
			}
			return nil, fmt.Errorf("file entry at block %d: %w", a.block, err)
		}
		switch id {
		case udfTagIE:
			next, ok := udfLongAD(b[36:])
			if !ok {
				if best != nil {
					return best, nil
				}
				return nil, fmt.Errorf("%w: an empty indirect entry at block %d", errUDF, a.block)
			}
			a = next
		case udfTagFE, udfTagEFE:
			fe, err := parseFE(b, off, a, id == udfTagEFE)
			if err != nil {
				if best != nil {
					return best, nil
				}
				return nil, err
			}
			best = fe
			if binary.LittleEndian.Uint16(b[20:]) != 4096 {
				return best, nil
			}
			// Strategy 4096: an indirect entry after it leads to a newer one
			nb, _, err := f.readLB(udfLBA{a.block + 1, a.part})
			if err != nil {
				return best, nil
			}
			if id, err := udfCheckTag(nb, int64(a.block)+1); err != nil || id != udfTagIE {
				return best, nil
			}
			next, ok := udfLongAD(nb[36:])
			if !ok {
				return best, nil
			}
			a = next
		default:
			if best != nil {
				return best, nil
			}
			return nil, fmt.Errorf("%w: tag %d at block %d instead of a file entry", errUDFTag, id, a.block)
		}
	}
	if best != nil {
		return best, nil
	}
	return nil, fmt.Errorf("%w: a loop of indirect entries at block %d", errUDF, a.block)
}

// udfLongAD gives the address of a long_ad; ok is false for an empty one
func udfLongAD(b []byte) (udfLBA, bool) {
	l := binary.LittleEndian.Uint32(b) & 0x3FFFFFFF
	return udfLBA{binary.LittleEndian.Uint32(b[4:]), binary.LittleEndian.Uint16(b[8:])}, l != 0
}

// parseFE parses a file entry or an extended file entry
func parseFE(b []byte, off int64, a udfLBA, efe bool) (*udfFE, error) {
	hdr, mt := 176, 84
	if efe {
		hdr, mt = 216, 92
	}
	if len(b) < hdr {
		return nil, fmt.Errorf("%w: file entry cut", errUDF)
	}
	lea := uint64(binary.LittleEndian.Uint32(b[hdr-8:]))
	lad := uint64(binary.LittleEndian.Uint32(b[hdr-4:]))
	if uint64(hdr)+lea+lad > uint64(len(b)) {
		return nil, fmt.Errorf("%w: file entry at block %d: %d bytes of attributes and %d of allocation descriptors past the block", errUDF, a.block, lea, lad)
	}
	size := binary.LittleEndian.Uint64(b[56:])
	if size > 1<<62 {
		return nil, fmt.Errorf("%w: file entry at block %d: size %d", errUDF, a.block, size)
	}
	adOff := hdr + int(lea)
	return &udfFE{
		loc:      a,
		diskOff:  off,
		fileType: b[27],
		flags:    binary.LittleEndian.Uint16(b[34:]),
		size:     int64(size),
		mtime:    udfTime(b[mt:]),
		perm:     binary.LittleEndian.Uint32(b[44:]),
		adOff:    adOff,
		ad:       slices.Clone(b[adOff : adOff+int(lad)]),
	}, nil
}

// fill takes the size, the time and the mode of a file entry
func (f *udfFS) fill(e *Entry, fe *udfFE) {
	e.IsDir = fe.fileType == 4
	e.Size = fe.size
	if e.IsDir {
		e.Size = 0
	}
	e.ModTime = fe.mtime
	p := fe.perm
	e.Mode = (p>>10&7)<<6 | (p>>5&7)<<3 | p&7
	if fe.flags&0x40 != 0 {
		e.Mode |= 0o4000
	}
	if fe.flags&0x80 != 0 {
		e.Mode |= 0o2000
	}
	if fe.flags&0x100 != 0 {
		e.Mode |= 0o1000
	}
	switch fe.fileType {
	case 4:
		e.Mode |= 0o40000
	case 12:
		e.Mode |= 0o120000
	case 6:
		e.Mode |= 0o60000
	case 7:
		e.Mode |= 0o20000
	case 9:
		e.Mode |= 0o10000
	case 10:
		e.Mode |= 0o140000
	default:
		e.Mode |= 0o100000
	}
	e.ID = fe.loc.key()
}

// extents gives the runs of a file on the image from its allocation
// descriptors, following their continuations; holes have DiskOff -1
func (f *udfFS) extents(fe *udfFE) ([]Extent, error) {
	adType := fe.flags & 7
	if adType == 3 {
		// The data is in the entry, after the extended attributes
		if fe.size > int64(len(fe.ad)) {
			return nil, fmt.Errorf("%w: %d bytes embedded in an entry with room for %d", errUDF, fe.size, len(fe.ad))
		}
		if fe.size == 0 {
			return nil, nil
		}
		return []Extent{{DiskOff: fe.diskOff + int64(fe.adOff), Len: fe.size}}, nil
	}
	var adLen int
	switch adType {
	case 0:
		adLen = 8
	case 1:
		adLen = 16
	case 2:
		adLen = 20
	default:
		return nil, fmt.Errorf("%w: allocation descriptors of type %d", errUDF, adType)
	}
	var out []Extent
	budget := udfMaxPieces
	seen := map[uint64]bool{}
	ads := fe.ad
	pos, count, recorded := int64(0), 0, int64(0)
	for len(ads) >= adLen && pos < fe.size {
		raw := binary.LittleEndian.Uint32(ads)
		typ, n := raw>>30, int64(raw&0x3FFFFFFF)
		a := udfLBA{binary.LittleEndian.Uint32(ads[4:]), fe.loc.part}
		switch adType {
		case 1:
			a.part = binary.LittleEndian.Uint16(ads[8:])
		case 2:
			a = udfLBA{binary.LittleEndian.Uint32(ads[12:]), binary.LittleEndian.Uint16(ads[16:])}
		}
		ads = ads[adLen:]
		if n == 0 {
			break
		}
		if count++; count > udfMaxADs {
			return out, fmt.Errorf("%w: more than %d allocation descriptors", errUDF, udfMaxADs)
		}
		if typ == 3 {
			// The descriptors go on in an allocation extent descriptor
			if seen[a.key()] || len(seen) >= udfMaxAEDs {
				return out, fmt.Errorf("%w: a loop of allocation extents at block %d", errUDF, a.block)
			}
			seen[a.key()] = true
			b, _, err := f.readLB(a)
			if err != nil {
				return out, err
			}
			if id, err := udfCheckTag(b, int64(a.block)); err != nil || id != udfTagAED {
				if err == nil {
					err = fmt.Errorf("%w: tag %d instead of an allocation extent", errUDFTag, id)
				}
				return out, fmt.Errorf("allocation extent at block %d: %w", a.block, err)
			}
			l := int64(binary.LittleEndian.Uint32(b[20:]))
			if l > int64(len(b))-24 {
				return out, fmt.Errorf("%w: allocation extent at block %d of %d bytes", errUDF, a.block, l)
			}
			ads = b[24 : 24+l]
			continue
		}
		n = min(n, fe.size-pos)
		if typ == 0 {
			// The blocks of a file are its own: more of them than the
			// image has is a damaged (or hostile) volume
			if recorded += n; recorded > f.size {
				return out, fmt.Errorf("%w: a file of more recorded bytes than the image", errUDF)
			}
			var err error
			if out, err = f.mapRun(a, pos, n, out, &budget); err != nil {
				return out, err
			}
		} else {
			// Allocated or not, not recorded: zeros
			out = append(out, Extent{FileOff: pos, DiskOff: -1, Len: n})
		}
		pos += n
	}
	return out, nil
}

// readAll reads a file up to max bytes
func (f *udfFS) readAll(fe *udfFE, max int64) ([]byte, error) {
	ext, err := f.extents(fe)
	if err != nil {
		return nil, err
	}
	n := min(fe.size, max)
	if err := fatInVolume(ext, f.size); err != nil {
		return nil, err
	}
	b := make([]byte, n)
	if err := fatReadAt(&fatRunReader{r: f.r, ext: ext, size: fe.size, valid: fe.size}, b, 0); err != nil {
		return nil, err
	}
	return b, nil
}

// ---- FS ----

func (f *udfFS) Type() string  { return f.typ }
func (f *udfFS) Label() string { return f.label }

func (f *udfFS) ReadDir(p string) ([]Entry, error) {
	e, err := f.lookup(p)
	if err != nil {
		return nil, err
	}
	if !e.IsDir {
		return nil, ErrNotDir
	}
	list, err := f.readDir(e)
	out := make([]Entry, len(list))
	for i, c := range list {
		out[i] = c.Entry
	}
	return out, err
}

func (f *udfFS) Stat(p string) (Entry, error) {
	e, err := f.lookup(p)
	return e.Entry, err
}

// entry reads the file entry of a file found
func (f *udfFS) entry(p string) (udfEntry, *udfFE, error) {
	e, err := f.lookup(p)
	if err != nil {
		return e, nil, err
	}
	if e.bad != nil {
		return e, nil, e.bad
	}
	if f.root.anc == nil {
		return e, nil, errUDF
	}
	fe, err := f.readICB(e.icb)
	return e, fe, err
}

func (f *udfFS) Extents(p string) ([]Extent, error) {
	_, fe, err := f.entry(p)
	if err != nil {
		return nil, err
	}
	return f.extents(fe)
}

func (f *udfFS) Open(p string) (io.ReaderAt, int64, error) {
	e, fe, err := f.entry(p)
	if err != nil {
		return nil, 0, err
	}
	if e.IsDir {
		return nil, 0, ErrIsDir
	}
	ext, err := f.extents(fe)
	if err != nil {
		return nil, 0, err
	}
	return &fatRunReader{r: f.r, ext: ext, size: fe.size, valid: fe.size}, fe.size, nil
}

func (f *udfFS) lookup(p string) (udfEntry, error) {
	names, err := fatSplit(p)
	if err != nil {
		return udfEntry{}, err
	}
	e := f.root
	for _, name := range names {
		if !e.IsDir {
			return udfEntry{}, ErrNotDir
		}
		list, err := f.readDir(e)
		if err != nil && list == nil {
			return udfEntry{}, err
		}
		i := slices.IndexFunc(list, func(c udfEntry) bool { return c.Name == name })
		if i < 0 {
			i = slices.IndexFunc(list, func(c udfEntry) bool { return strings.EqualFold(c.Name, name) })
		}
		if i < 0 {
			return udfEntry{}, ErrNotFound
		}
		e = list[i]
	}
	return e, nil
}

// readDir lists a directory: its file identifier descriptors, the file
// entry of each read for its size and time. A damaged descriptor ends the
// list with an error; the entries before it are kept.
func (f *udfFS) readDir(d udfEntry) ([]udfEntry, error) {
	if c, ok := f.dirs.get(d.Path); ok {
		return c[0].list, c[0].err
	}
	list, err := f.parseDir(d)
	f.dirs.put(d.Path, []udfDir{{list, err}})
	return list, err
}

func (f *udfFS) parseDir(d udfEntry) ([]udfEntry, error) {
	if f.root.anc == nil {
		return nil, errUDF
	}
	fe, err := f.readICB(d.icb)
	if err != nil {
		return nil, err
	}
	if fe.size > udfMaxDir || fe.size > f.size {
		return nil, fmt.Errorf("%w: a directory of %d bytes", errUDF, fe.size)
	}
	b, err := f.readAll(fe, udfMaxDir)
	if err != nil {
		return nil, err
	}
	var list []udfEntry
	var dirErr error
	bad := 0
	for pos := 0; pos+38 <= len(b) && len(list) < fatMaxDirEntries; {
		lfi := int(b[pos+19])
		liu := int(binary.LittleEndian.Uint16(b[pos+36:]))
		end := pos + 38 + liu + lfi
		if binary.LittleEndian.Uint16(b[pos:]) == 0 && b[pos+4] == 0 {
			break // zeros: the end
		}
		if end > len(b) {
			dirErr = fmt.Errorf("%w: file identifier at %d of directory %s past its end", errUDF, pos, d.Path)
			break
		}
		// The CRC covers the padding to 4 bytes too
		if id, err := udfCheckTag(b[pos:min((end+3)&^3, len(b))], -1); err != nil || id != udfTagFID {
			if err == nil {
				err = fmt.Errorf("%w: tag %d instead of a file identifier", errUDFTag, id)
			}
			dirErr = fmt.Errorf("directory %s at %d: %w", d.Path, pos, err)
			break
		}
		chars := b[pos+18]
		icb, _ := udfLongAD(b[pos+20:])
		name := udfChars(b[pos+38+liu : end])
		pos = (end + 3) &^ 3
		if chars&0x0C != 0 || name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\x00") {
			continue // deleted, the parent, or a name that can not be a path
		}
		e := udfEntry{Entry: Entry{Name: name, Path: fatJoin(d.Path, name), IsDir: chars&2 != 0, ID: icb.key()}, icb: icb}
		cfe, err := f.readICB(icb)
		if err != nil {
			e.bad = err
			bad++
			if dirErr == nil {
				dirErr = fmt.Errorf("%s: %w", e.Path, err)
			}
		} else {
			f.fill(&e.Entry, cfe)
		}
		if e.IsDir {
			if slices.Contains(d.anc, icb.key()) || len(d.anc) >= udfMaxDepth {
				if dirErr == nil {
					dirErr = fmt.Errorf("%w: directory %s is inside itself", errUDF, e.Path)
				}
				continue
			}
			e.anc = append(slices.Clone(d.anc), icb.key())
		}
		list = append(list, e)
	}
	if bad > 1 && dirErr != nil {
		dirErr = fmt.Errorf("%w (and %d more damaged entries)", dirErr, bad-1)
	}
	return list, dirErr
}
