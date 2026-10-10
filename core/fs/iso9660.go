package fs

// ISO 9660 (ECMA-119) with the Joliet names of a supplementary volume
// descriptor and the Rock Ridge extensions (SUSP, RRIP 1.12): names, modes,
// times, symbolic links, relocated directories.

import (
	"encoding/binary"
	"errors"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"
)

const (
	isoSector = 2048
	// isoMaxDir caps the size of a directory that is read (64 MB)
	isoMaxDir = 64 << 20
	// isoMaxDescriptors caps the volume descriptors looked at
	isoMaxDescriptors = 64
	// isoMaxCE caps the continuation areas of one record
	isoMaxCE = 16
)

var errISODir = errors.New("damaged directory")

func init() { register(probeISO) }

type isoFS struct {
	r      io.ReaderAt
	size   int64
	bs     int64 // the logical block size
	label  string
	typ    string
	root   isoEntry
	joliet bool // the names are those of Joliet
	rr     bool // Rock Ridge: its names, modes and times
	rrSkip int  // the bytes skipped at the start of each system use area (SP)
	dirs   fatDirs[isoEntry]
}

type isoEntry struct {
	Entry
	ext    []Extent // the extents of a file; a directory has one
	raw    string   // the name as recorded, to join the parts of a file
	link   string   // the target of a symbolic link
	reloc  bool     // a directory moved by Rock Ridge (RE): not shown here
	clDir  int64    // the block of the directory a CL entry stands for; -1 none
	hidden bool
}

// probeISO looks for "CD001" at sector 16 and reads the volume descriptors
func probeISO(r io.ReaderAt, size int64) (FS, bool, error) {
	b := make([]byte, isoSector)
	if fatReadAt(r, b, 16*isoSector) != nil || string(b[1:6]) != "CD001" {
		return nil, false, nil
	}
	f := &isoFS{r: r, size: size, bs: isoSector, typ: "ISO 9660"}
	var pvd, svd []byte
	for i := 0; i < isoMaxDescriptors; i++ {
		if i > 0 && fatReadAt(r, b, int64(16+i)*isoSector) != nil {
			break
		}
		if string(b[1:6]) != "CD001" || b[0] == 255 {
			break
		}
		switch b[0] {
		case 1:
			if pvd == nil {
				pvd = slices.Clone(b)
			}
		case 2:
			// Joliet: the escape sequence of UCS-2 level 1, 2 or 3
			if e := b[88:91]; svd == nil && e[0] == '%' && e[1] == '/' && (e[2] == '@' || e[2] == 'C' || e[2] == 'E') {
				svd = slices.Clone(b)
			}
		}
	}
	if pvd == nil {
		return f, true, errors.New("no primary volume descriptor")
	}
	if bs := int64(binary.LittleEndian.Uint16(pvd[128:])); bs == 512 || bs == 1024 || bs == 2048 {
		f.bs = bs
	}
	f.label = strings.TrimRight(string(pvd[40:72]), " \x00")
	root, ok := f.rootRecord(pvd)
	if !ok {
		return f, true, errISODir
	}
	f.root = root
	f.detectRR()
	switch {
	case f.rr:
		f.typ = "ISO 9660 (Rock Ridge)"
	case svd != nil:
		if jroot, ok := f.rootRecord(svd); ok {
			f.joliet = true
			f.root = jroot
			f.typ = "ISO 9660 (Joliet)"
			if l := strings.TrimRight(isoUCS2(svd[40:72]), " \x00"); l != "" {
				f.label = l
			}
		}
	}
	return f, true, nil
}

// rootRecord parses the root directory record of a volume descriptor
func (f *isoFS) rootRecord(vd []byte) (isoEntry, bool) {
	rec := vd[156 : 156+34]
	if rec[0] < 34 {
		return isoEntry{}, false
	}
	e := isoEntry{clDir: -1}
	e.Name, e.Path, e.IsDir = "/", "/", true
	lba := int64(binary.LittleEndian.Uint32(rec[2:]))
	n := int64(binary.LittleEndian.Uint32(rec[10:]))
	e.ID = uint64(lba)
	e.ModTime = isoTime7(rec[18:25])
	e.ext = []Extent{{DiskOff: lba * f.bs, Len: n}}
	return e, true
}

// detectRR looks for the SP entry of SUSP in the "." record of the root
func (f *isoFS) detectRR() {
	b, err := f.readExtent(f.root.ext[0], isoSector)
	if err != nil || len(b) < 34 || int(b[0]) > len(b) {
		return
	}
	su := isoSystemUse(b[:b[0]], 0)
	for len(su) >= 4 {
		l := int(su[2])
		if l < 4 || l > len(su) {
			break
		}
		if string(su[:2]) == "SP" && l >= 7 && su[4] == 0xBE && su[5] == 0xEF {
			f.rr, f.rrSkip = true, int(su[6])
			return
		}
		su = su[l:]
	}
}

func (f *isoFS) Type() string  { return f.typ }
func (f *isoFS) Label() string { return f.label }

func (f *isoFS) ReadDir(p string) ([]Entry, error) {
	e, err := f.lookup(p)
	if err != nil {
		return nil, err
	}
	if !e.IsDir {
		return nil, ErrNotDir
	}
	list, err := f.readDir(e)
	out := make([]Entry, 0, len(list))
	for _, c := range list {
		if !c.hidden {
			out = append(out, c.Entry)
		}
	}
	return out, err
}

func (f *isoFS) Stat(p string) (Entry, error) {
	e, err := f.lookup(p)
	return e.Entry, err
}

func (f *isoFS) Extents(p string) ([]Extent, error) {
	e, err := f.lookup(p)
	if err != nil {
		return nil, err
	}
	return slices.Clone(e.ext), nil
}

func (f *isoFS) Open(p string) (io.ReaderAt, int64, error) {
	e, err := f.lookup(p)
	if err != nil {
		return nil, 0, err
	}
	if e.IsDir {
		return nil, 0, ErrIsDir
	}
	return &fatRunReader{r: f.r, ext: e.ext, size: e.Size, valid: e.Size}, e.Size, nil
}

func (f *isoFS) lookup(p string) (isoEntry, error) {
	names, err := fatSplit(p)
	if err != nil {
		return isoEntry{}, err
	}
	e := f.root
	for _, name := range names {
		if !e.IsDir {
			return isoEntry{}, ErrNotDir
		}
		list, err := f.readDir(e)
		if err != nil {
			return isoEntry{}, err
		}
		// Rock Ridge names are those of Unix: the case counts
		i := slices.IndexFunc(list, func(c isoEntry) bool {
			return !c.hidden && (c.Name == name || !f.rr && strings.EqualFold(c.Name, name))
		})
		if i < 0 {
			return isoEntry{}, ErrNotFound
		}
		e = list[i]
	}
	return e, nil
}

// readExtent reads up to max bytes of an extent; a directory cut by the
// end of the image is read up to there
func (f *isoFS) readExtent(x Extent, max int64) ([]byte, error) {
	if x.DiskOff < 0 || x.DiskOff >= f.size {
		return nil, io.ErrUnexpectedEOF
	}
	b := make([]byte, min(x.Len, max, f.size-x.DiskOff))
	return b, fatReadAt(f.r, b, x.DiskOff)
}

func (f *isoFS) readDir(d isoEntry) ([]isoEntry, error) {
	if list, ok := f.dirs.get(d.Path); ok {
		return list, nil
	}
	if len(d.ext) == 0 {
		return nil, errISODir
	}
	b, err := f.readExtent(d.ext[0], isoMaxDir)
	if err != nil {
		return nil, err
	}
	list := f.parseDir(b, d.Path)
	f.dirs.put(d.Path, list)
	return list, nil
}

// parseDir parses the records of a directory. A record does not cross a
// sector: a zero length goes to the next one. The parts of a file of
// several extents follow each other with the same name, all but the last
// marked with flag 0x80.
func (f *isoFS) parseDir(b []byte, dir string) []isoEntry {
	var list []isoEntry
	var part *isoEntry // a file of several extents being collected
	flush := func() {
		if part != nil {
			list = append(list, *part)
			part = nil
		}
	}
	pos, n := 0, 0
	for pos < len(b) && n < fatMaxDirEntries {
		l := int(b[pos])
		if l == 0 {
			pos = (pos/isoSector + 1) * isoSector
			continue
		}
		if l < 34 || pos+l > len(b) || 33+int(b[pos+32]) > l {
			// Damaged: try the next sector
			pos = (pos/isoSector + 1) * isoSector
			continue
		}
		rec := b[pos : pos+l]
		pos += l
		n++
		nameLen := int(rec[32])
		raw := rec[33 : 33+nameLen]
		if nameLen == 1 && (raw[0] == 0 || raw[0] == 1) {
			continue // "." and ".."
		}
		flags := rec[25]
		lba := int64(binary.LittleEndian.Uint32(rec[2:]))
		size := int64(binary.LittleEndian.Uint32(rec[10:]))
		x := Extent{DiskOff: lba * f.bs, Len: size}
		if part != nil && part.raw == string(raw) {
			x.FileOff = part.Size
			part.ext = append(part.ext, x)
			part.Size += size
			if flags&0x80 == 0 {
				flush()
			}
			continue
		}
		flush()
		e := isoEntry{raw: string(raw), clDir: -1}
		e.IsDir = flags&0x02 != 0
		e.ID = uint64(lba)
		e.ModTime = isoTime7(rec[18:25])
		e.ext = []Extent{x}
		if !e.IsDir {
			e.Size = size
		}
		switch {
		case f.joliet:
			e.Name = isoStripVersion(isoUCS2(raw))
		default:
			e.Name = isoStripVersion(string(raw))
			if !e.IsDir {
				e.Name = strings.TrimSuffix(e.Name, ".")
			}
		}
		if f.rr {
			f.applyRR(&e, rec, nameLen)
		}
		if e.Name == "" || e.Name == "." || e.Name == ".." || strings.Contains(e.Name, "/") {
			continue
		}
		e.Path = fatJoin(dir, e.Name)
		if !e.IsDir && size == 0 {
			e.ext = nil
		}
		if e.reloc {
			e.hidden = true
		}
		if flags&0x80 != 0 && !e.IsDir {
			p := e
			part = &p
			continue
		}
		list = append(list, e)
	}
	flush()
	return list
}

// applyRR takes the name, mode, time and link of the Rock Ridge entries of a
// record, following its continuation areas
func (f *isoFS) applyRR(e *isoEntry, rec []byte, nameLen int) {
	su := isoSystemUse(rec, nameLen)
	if f.rrSkip <= len(su) {
		su = su[f.rrSkip:]
	} else {
		su = nil
	}
	var name []byte
	hasName := false
	var link []string
	linkCont := false // the last component goes on in the next one
	for ce := 0; su != nil && ce <= isoMaxCE; ce++ {
		var next []byte
		for len(su) >= 4 {
			l := int(su[2])
			if l < 4 || l > len(su) {
				break
			}
			d := su[:l]
			su = su[l:]
			switch string(d[:2]) {
			case "ST":
				su = nil
			case "CE":
				if l >= 28 && next == nil {
					blk := int64(binary.LittleEndian.Uint32(d[4:]))
					off := int64(binary.LittleEndian.Uint32(d[12:]))
					n := int64(binary.LittleEndian.Uint32(d[20:]))
					if off < f.bs && n <= f.bs-off {
						b := make([]byte, n)
						if fatReadAt(f.r, b, blk*f.bs+off) == nil {
							next = b
						}
					}
				}
			case "NM":
				if l >= 5 {
					switch {
					case d[4]&2 != 0:
						name, hasName = []byte("."), true
					case d[4]&4 != 0:
						name, hasName = []byte(".."), true
					default:
						name = append(name, d[5:]...)
						hasName = true
					}
				}
			case "PX":
				if l >= 12 {
					e.Mode = binary.LittleEndian.Uint32(d[4:])
				}
			case "TF":
				if l >= 5 {
					if t, ok := isoTF(d); ok {
						e.ModTime = t
					}
				}
			case "SL":
				link, linkCont = isoSL(d, link, linkCont)
			case "CL":
				if l >= 12 {
					e.clDir = int64(binary.LittleEndian.Uint32(d[4:]))
				}
			case "RE":
				e.reloc = true
			}
		}
		su = next
	}
	if hasName {
		e.Name = string(name)
	}
	if link != nil {
		e.link = strings.Join(link, "/")
		if strings.HasPrefix(e.link, "//") {
			e.link = e.link[1:]
		}
	}
	if e.clDir >= 0 {
		// A directory relocated elsewhere: its records are at the block CL
		// gives; the length comes from its "." record
		e.IsDir = true
		e.Size = 0
		e.ID = uint64(e.clDir)
		dot := make([]byte, 34)
		if fatReadAt(f.r, dot, e.clDir*f.bs) == nil && dot[0] >= 34 {
			e.ext = []Extent{{DiskOff: e.clDir * f.bs, Len: int64(binary.LittleEndian.Uint32(dot[10:]))}}
		} else {
			e.ext = nil
		}
	}
}

// isoSystemUse is the system use area of a record: after the name and its
// padding byte
func isoSystemUse(rec []byte, nameLen int) []byte {
	s := 33 + nameLen
	if nameLen%2 == 0 {
		s++
	}
	if s >= len(rec) {
		return nil
	}
	return rec[s:]
}

// isoSL adds the components of an SL entry to a link; cont tells that the
// last one goes on
func isoSL(d []byte, link []string, cont bool) ([]string, bool) {
	if len(d) < 5 {
		return link, cont
	}
	c := d[5:]
	for len(c) >= 2 {
		fl, n := c[0], int(c[1])
		if 2+n > len(c) {
			break
		}
		var s string
		switch {
		case fl&2 != 0:
			s = "."
		case fl&4 != 0:
			s = ".."
		case fl&8 != 0:
			s = "" // the root: the joined link starts with "/"
		default:
			s = string(c[2 : 2+n])
		}
		if cont && len(link) > 0 {
			link[len(link)-1] += s
		} else {
			link = append(link, s)
		}
		cont = fl&1 != 0
		c = c[2+n:]
	}
	if link == nil {
		link = []string{}
	}
	return link, cont
}

// isoTF gives the modification time of a TF entry (or the creation time
// when there is none)
func isoTF(d []byte) (time.Time, bool) {
	fl := d[4]
	n := 7
	if fl&0x80 != 0 {
		n = 17
	}
	pos := 5
	var created time.Time
	for bit := 0; bit < 7; bit++ {
		if fl&(1<<bit) == 0 {
			continue
		}
		if pos+n > len(d) {
			break
		}
		var t time.Time
		if n == 7 {
			t = isoTime7(d[pos : pos+7])
		} else {
			t = isoTime17(d[pos : pos+17])
		}
		switch bit {
		case 0:
			created = t
		case 1:
			return t, true
		}
		pos += n
	}
	return created, !created.IsZero()
}

// isoTime7 decodes the 7-byte time of a record: the year from 1900, and
// the offset from GMT in 15 minutes
func isoTime7(b []byte) time.Time {
	if b[0] == 0 && b[1] == 0 && b[2] == 0 {
		return time.Time{}
	}
	loc := time.FixedZone("", int(int8(b[6]))*15*60)
	return time.Date(1900+int(b[0]), time.Month(b[1]), int(b[2]), int(b[3]), int(b[4]), int(b[5]), 0, loc)
}

// isoTime17 decodes "YYYYMMDDHHMMSScc" and the offset from GMT
func isoTime17(b []byte) time.Time {
	num := func(s []byte) int { v, _ := strconv.Atoi(string(s)); return v }
	y := num(b[0:4])
	if y == 0 {
		return time.Time{}
	}
	loc := time.FixedZone("", int(int8(b[16]))*15*60)
	return time.Date(y, time.Month(num(b[4:6])), num(b[6:8]), num(b[8:10]), num(b[10:12]), num(b[12:14]), num(b[14:16])*1e7, loc)
}

// isoUCS2 decodes the big-endian UCS-2 (UTF-16) of Joliet
func isoUCS2(b []byte) string {
	u := make([]uint16, len(b)/2)
	for i := range u {
		u[i] = binary.BigEndian.Uint16(b[2*i:])
	}
	return string(utf16.Decode(u))
}

// isoStripVersion removes the ";1" version of a file name
func isoStripVersion(s string) string {
	if i := strings.LastIndexByte(s, ';'); i >= 0 {
		if _, err := strconv.Atoi(s[i+1:]); err == nil || i == len(s)-1 {
			return s[:i]
		}
	}
	return s
}
