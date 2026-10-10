package fs

// FAT12, FAT16 and FAT32 (Microsoft FAT32 File System Specification 1.03)
// with VFAT long names. The helpers named fat... are shared with exFAT and
// ISO 9660.

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf16"
)

const (
	// fatMaxDirEntries caps the entries of a directory: damaged volumes may
	// claim endless ones
	fatMaxDirEntries = 1 << 20
	// fatBlock is the unit of the cache of the FAT
	fatBlock = 4096
	// fatCacheBlocks caps the cache of the FAT (16 MB)
	fatCacheBlocks = 4096
	// fatDirCache caps the directories kept parsed
	fatDirCache = 256
)

var (
	errFATChain = errors.New("damaged cluster chain")
	errFATDir   = errors.New("damaged directory")
)

func init() { register(probeFAT) }

type fatFS struct {
	r        io.ReaderAt
	size     int64
	bits     int // 12, 16 or 32
	clusSz   int64
	rootOff  int64 // the fixed root directory of FAT12/16
	rootLen  int64
	rootClus uint32 // the first cluster of the root of FAT32
	dataOff  int64
	nClus    uint32 // the clusters of the data are 2..nClus+1
	label    string
	fat      *fatTable
	dirs     fatDirs[fatEntry]
}

// fatEntry is an entry with where its data starts
type fatEntry struct {
	Entry
	clus uint32
}

// probeFAT checks the jump, the BPB and its sizes; the type comes from the
// count of clusters, as the specification says, not from the text at 0x36
func probeFAT(r io.ReaderAt, size int64) (FS, bool, error) {
	b := make([]byte, 512)
	if fatReadAt(r, b, 0) != nil {
		return nil, false, nil
	}
	if b[0] != 0xEB && b[0] != 0xE9 {
		return nil, false, nil
	}
	if oem := string(b[3:11]); oem == "NTFS    " || oem == "EXFAT   " {
		return nil, false, nil
	}
	le16 := func(o int) int64 { return int64(binary.LittleEndian.Uint16(b[o:])) }
	le32 := func(o int) int64 { return int64(binary.LittleEndian.Uint32(b[o:])) }
	bps := le16(11)
	spc := int64(b[13])
	rsvd := le16(14)
	nFATs := int64(b[16])
	rootEnt := le16(17)
	tot := le16(19)
	media := b[21]
	fatSz := le16(22)
	if bps != 512 && bps != 1024 && bps != 2048 && bps != 4096 {
		return nil, false, nil
	}
	if spc == 0 || spc&(spc-1) != 0 || rsvd == 0 || nFATs == 0 || nFATs > 4 {
		return nil, false, nil
	}
	if media != 0xF0 && media < 0xF8 {
		return nil, false, nil
	}
	if tot == 0 {
		tot = le32(32)
	}
	if fatSz == 0 {
		fatSz = le32(36)
	}
	if tot == 0 || fatSz == 0 {
		return nil, false, nil
	}
	rootSecs := (rootEnt*32 + bps - 1) / bps
	meta := rsvd + nFATs*fatSz + rootSecs
	if meta >= tot {
		return nil, false, nil
	}
	n := (tot - meta) / spc
	f := &fatFS{r: r, size: size, clusSz: bps * spc, fat: newFATTable(r)}
	switch {
	case n < 4085:
		f.bits = 12
	case n < 65525:
		f.bits = 16
	default:
		f.bits = 32
	}
	// The FAT holds 2 more entries than there are clusters; a count that
	// does not fit is cut to what the FAT can say
	if max := fatSz*bps*8/int64(f.bits) - 2; n > max {
		n = max
	}
	if n < 1 {
		return nil, false, nil
	}
	f.nClus = uint32(min(n, 0x0FFFFFF5))
	active := int64(0)
	if f.bits == 32 {
		if rootEnt != 0 || le16(22) != 0 {
			return nil, false, nil
		}
		f.rootClus = uint32(le32(44))
		// Bit 7 of ExtFlags: only the FAT of bits 0-3 is used
		if ext := le16(40); ext&0x80 != 0 && ext&0x0F < nFATs {
			active = ext & 0x0F
		}
		if b[66] == 0x29 {
			f.label = fatLabel(b[71:82])
		}
	} else {
		if rootEnt == 0 {
			return nil, false, nil
		}
		f.rootOff = (rsvd + nFATs*fatSz) * bps
		f.rootLen = rootEnt * 32
		if b[38] == 0x29 {
			f.label = fatLabel(b[43:54])
		}
	}
	f.fat.off = (rsvd + active*fatSz) * bps
	f.fat.len = fatSz * bps
	f.dataOff = meta * bps
	// The label of the root directory wins over the copy in the BPB
	if _, label, err := f.loadDir(f.root()); err == nil && label != "" {
		f.label = label
	}
	return f, true, nil
}

// fatLabel trims a label padded with spaces; "NO NAME" is none
func fatLabel(b []byte) string {
	s := strings.TrimRight(fatOEM(b), " ")
	if s == "NO NAME" {
		return ""
	}
	return s
}

func (f *fatFS) Type() string  { return fmt.Sprintf("FAT%d", f.bits) }
func (f *fatFS) Label() string { return f.label }

func (f *fatFS) root() fatEntry {
	return fatEntry{Entry: Entry{Name: "/", Path: "/", IsDir: true, ID: uint64(f.rootClus)}, clus: f.rootClus}
}

func (f *fatFS) ReadDir(p string) ([]Entry, error) {
	e, err := f.lookup(p)
	if err != nil {
		return nil, err
	}
	if !e.IsDir {
		return nil, ErrNotDir
	}
	list, err := f.readDir(e)
	return fatEntries(list), err
}

func (f *fatFS) Stat(p string) (Entry, error) {
	e, err := f.lookup(p)
	return e.Entry, err
}

func (f *fatFS) Extents(p string) ([]Extent, error) {
	e, err := f.lookup(p)
	if err != nil {
		return nil, err
	}
	return f.extents(e)
}

func (f *fatFS) Open(p string) (io.ReaderAt, int64, error) {
	e, err := f.lookup(p)
	if err != nil {
		return nil, 0, err
	}
	if e.IsDir {
		return nil, 0, ErrIsDir
	}
	ext, err := f.extents(e)
	if err != nil {
		return nil, 0, err
	}
	return &fatRunReader{r: f.r, ext: ext, size: e.Size, valid: e.Size}, e.Size, nil
}

// extents of a file, or of a directory: its clusters
func (f *fatFS) extents(e fatEntry) ([]Extent, error) {
	if e.IsDir && e.clus == 0 {
		if f.bits == 32 || e.Path != "/" {
			return nil, errFATDir
		}
		return []Extent{{FileOff: 0, DiskOff: f.rootOff, Len: f.rootLen}}, nil
	}
	if !e.IsDir && e.Size == 0 {
		return nil, nil
	}
	max := f.dirClusters()
	if !e.IsDir {
		max = int((e.Size + f.clusSz - 1) / f.clusSz)
	}
	chain, err := f.chain(e.clus, max, !e.IsDir)
	if err != nil {
		return nil, err
	}
	size := e.Size
	if e.IsDir {
		size = int64(len(chain)) * f.clusSz
	}
	return fatChainExtents(chain, f.dataOff, f.clusSz, size), nil
}

// dirClusters is the most clusters a directory may have
func (f *fatFS) dirClusters() int {
	return int(min(int64(fatMaxDirEntries)*32/f.clusSz+1, int64(f.nClus)))
}

// chain follows the FAT from first; a file needs exactly max clusters
// (more are ignored), a directory ends at the end mark or at max
func (f *fatFS) chain(first uint32, max int, exact bool) ([]uint32, error) {
	var eoc uint32
	switch f.bits {
	case 12:
		eoc = 0xFF8
	case 16:
		eoc = 0xFFF8
	default:
		eoc = 0x0FFFFFF8
	}
	next := func(c uint32) (uint32, bool, error) {
		var v uint32
		switch f.bits {
		case 12:
			off := int64(c) + int64(c)/2
			w, err := f.fat.u16(off)
			if err != nil {
				return 0, false, err
			}
			if c&1 != 0 {
				v = uint32(w >> 4)
			} else {
				v = uint32(w & 0xFFF)
			}
		case 16:
			w, err := f.fat.u16(int64(c) * 2)
			if err != nil {
				return 0, false, err
			}
			v = uint32(w)
		default:
			w, err := f.fat.u32(int64(c) * 4)
			if err != nil {
				return 0, false, err
			}
			v = w & 0x0FFFFFFF
		}
		return v, v >= eoc, nil
	}
	chain, err := fatFollow(first, max, f.nClus, next)
	if err != nil {
		return nil, err
	}
	if exact && len(chain) < max {
		return nil, fmt.Errorf("%w: %d clusters of %d", errFATChain, len(chain), max)
	}
	return chain, nil
}

// lookup finds the entry of a path, from the root
func (f *fatFS) lookup(p string) (fatEntry, error) {
	names, err := fatSplit(p)
	if err != nil {
		return fatEntry{}, err
	}
	e := f.root()
	for _, name := range names {
		if !e.IsDir {
			return fatEntry{}, ErrNotDir
		}
		list, err := f.readDir(e)
		if err != nil {
			return fatEntry{}, err
		}
		i := slices.IndexFunc(list, func(c fatEntry) bool { return strings.EqualFold(c.Name, name) })
		if i < 0 {
			return fatEntry{}, ErrNotFound
		}
		e = list[i]
	}
	return e, nil
}

// readDir reads and parses a directory, keeping it for the next time
func (f *fatFS) readDir(d fatEntry) ([]fatEntry, error) {
	if list, ok := f.dirs.get(d.Path); ok {
		return list, nil
	}
	list, _, err := f.loadDir(d)
	if err == nil {
		f.dirs.put(d.Path, list)
	}
	return list, err
}

// loadDir reads and parses a directory; label is that of its volume label
// entry
func (f *fatFS) loadDir(d fatEntry) ([]fatEntry, string, error) {
	ext, err := f.extents(d)
	if err != nil {
		return nil, "", err
	}
	var size int64
	for _, x := range ext {
		size += x.Len
	}
	if err := fatInVolume(ext, f.size); err != nil {
		return nil, "", err
	}
	b := make([]byte, size)
	if _, err := (&fatRunReader{r: f.r, ext: ext, size: size, valid: size}).ReadAt(b, 0); err != nil && err != io.EOF {
		return nil, "", err
	}
	list, label := fatParseDir(b, d.Path, f.bits == 32)
	return list, label, nil
}

// fatParseDir parses the 32-byte entries of a directory; label is that of
// a volume label entry. Deleted entries and broken long names are skipped
// (the 8.3 name is used then).
func fatParseDir(b []byte, dir string, fat32 bool) (list []fatEntry, label string) {
	var lfn []uint16 // the long name being collected
	var lfnSum byte
	need := -1 // the ordinal of the long name entry expected next; 0 - complete
	for i := 0; i+32 <= len(b) && i/32 < fatMaxDirEntries; i += 32 {
		d := b[i : i+32]
		if d[0] == 0 {
			break
		}
		if d[0] == 0xE5 {
			need = -1
			continue
		}
		attr := d[11]
		if attr&0x3F == 0x0F {
			ord := int(d[0] & 0x1F)
			if d[0]&0x40 != 0 {
				if ord == 0 || ord > 20 {
					need = -1
					continue
				}
				lfn = make([]uint16, ord*13)
				lfnSum = d[13]
				need = ord
			}
			if need <= 0 || ord != need || d[13] != lfnSum {
				need = -1
				continue
			}
			p := lfn[(ord-1)*13:]
			for k, o := range [13]int{1, 3, 5, 7, 9, 14, 16, 18, 20, 22, 24, 28, 30} {
				p[k] = binary.LittleEndian.Uint16(d[o:])
			}
			need--
			continue
		}
		long := need == 0 && fatChecksum(d[:11]) == lfnSum
		need = -1
		if attr&0x08 != 0 {
			if attr&0x10 == 0 && label == "" {
				label = fatLabel(d[:11])
			}
			continue
		}
		name := fatShortName(d)
		if long {
			if s := fatLongName(lfn); s != "" {
				name = s
			}
		}
		if name == "." || name == ".." || name == "" {
			continue
		}
		clus := uint32(binary.LittleEndian.Uint16(d[26:]))
		if fat32 {
			clus |= uint32(binary.LittleEndian.Uint16(d[20:])) << 16
		}
		e := fatEntry{clus: clus}
		e.Name = name
		e.Path = fatJoin(dir, name)
		e.IsDir = attr&0x10 != 0
		if !e.IsDir {
			e.Size = int64(binary.LittleEndian.Uint32(d[28:]))
		}
		e.ModTime = fatTime(binary.LittleEndian.Uint16(d[24:]), binary.LittleEndian.Uint16(d[22:]))
		e.ID = uint64(clus)
		list = append(list, e)
	}
	return list, label
}

// fatChecksum is the checksum of an 8.3 name that its long name entries keep
func fatChecksum(name []byte) byte {
	var s byte
	for _, c := range name {
		s = (s&1)<<7 + s>>1 + c
	}
	return s
}

// fatLongName decodes UTF-16 up to the zero that ends a long name
func fatLongName(u []uint16) string {
	if i := slices.Index(u, 0); i >= 0 {
		u = u[:i]
	}
	for len(u) > 0 && u[len(u)-1] == 0xFFFF {
		u = u[:len(u)-1]
	}
	return string(utf16.Decode(u))
}

// fatShortName makes "NAME.EXT" of an 8.3 entry with the lowercase flags
// of Windows NT (byte 12)
func fatShortName(d []byte) string {
	b := append([]byte(nil), d[:11]...)
	if b[0] == 0x05 {
		b[0] = 0xE5 // a name that starts with E5 is kept as 05
	}
	base := strings.TrimRight(fatOEM(b[:8]), " ")
	ext := strings.TrimRight(fatOEM(b[8:11]), " ")
	if d[12]&0x08 != 0 {
		base = strings.ToLower(base)
	}
	if d[12]&0x10 != 0 {
		ext = strings.ToLower(ext)
	}
	if ext == "" {
		return base
	}
	return base + "." + ext
}

// cp437 is the upper half of the code page of the 8.3 names of DOS
const cp437 = "ÇüéâäàåçêëèïîìÄÅÉæÆôöòûùÿÖÜ¢£¥₧ƒáíóúñÑªº¿⌐¬½¼¡«»░▒▓│┤╡╢╖╕╣║╗╝╜╛┐└┴┬├─┼╞╟╚╔╩╦╠═╬╧╨╤╥╙╘╒╓╫╪┘┌█▄▌▐▀αßΓπΣσµτΦΘΩδ∞φε∩≡±≥≤⌠⌡÷≈°∙·√ⁿ²■ "

var cp437Runes = []rune(cp437)

// fatOEM decodes bytes of code page 437
func fatOEM(b []byte) string {
	var sb strings.Builder
	for _, c := range b {
		if c < 0x80 {
			sb.WriteByte(c)
		} else {
			sb.WriteRune(cp437Runes[c-0x80])
		}
	}
	return sb.String()
}

// fatTime decodes a DOS date and time. FAT keeps local time with no zone:
// it is given as UTC so the clock shown is the one the volume has.
func fatTime(date, tm uint16) time.Time {
	if date == 0 {
		return time.Time{}
	}
	return time.Date(1980+int(date>>9), time.Month(date>>5&15), int(date&31),
		int(tm>>11), int(tm>>5&63), int(tm&31)*2, 0, time.UTC)
}

func fatEntries(list []fatEntry) []Entry {
	out := make([]Entry, len(list))
	for i, e := range list {
		out[i] = e.Entry
	}
	return out
}

// ---- Shared by FAT, exFAT and ISO 9660 ----

// fatReadAt reads all of b; a short read is an error
func fatReadAt(r io.ReaderAt, b []byte, off int64) error {
	if off < 0 {
		return io.ErrUnexpectedEOF
	}
	n, err := r.ReadAt(b, off)
	if n == len(b) {
		return nil
	}
	if err == nil || err == io.EOF {
		err = io.ErrUnexpectedEOF
	}
	return err
}

// fatInVolume checks that extents are inside the volume before their bytes
// are allocated: damaged sizes must not cost memory
func fatInVolume(ext []Extent, size int64) error {
	for _, x := range ext {
		if x.DiskOff < 0 || x.Len < 0 || x.DiskOff > size || x.Len > size-x.DiskOff {
			return fmt.Errorf("%w: data past the end of the volume", io.ErrUnexpectedEOF)
		}
	}
	return nil
}

// fatSplit splits a "/" separated path into names; "." and empty parts
// are dropped
func fatSplit(p string) ([]string, error) {
	var out []string
	for _, s := range strings.Split(p, "/") {
		switch s {
		case "", ".":
		case "..":
			return nil, ErrNotFound
		default:
			out = append(out, s)
		}
	}
	return out, nil
}

func fatJoin(dir, name string) string {
	if dir == "/" {
		return "/" + name
	}
	return dir + "/" + name
}

// fatTable reads the entries of a FAT through a cache of blocks
type fatTable struct {
	r     io.ReaderAt
	off   int64 // where the FAT is on the volume
	len   int64
	mu    sync.Mutex
	cache map[int64][]byte
}

func newFATTable(r io.ReaderAt) *fatTable {
	return &fatTable{r: r, cache: map[int64][]byte{}}
}

// block returns the cached block k of the FAT
func (t *fatTable) block(k int64) ([]byte, error) {
	if b, ok := t.cache[k]; ok {
		return b, nil
	}
	n := min(fatBlock, t.len-k*fatBlock)
	if n <= 0 {
		return nil, errFATChain
	}
	b := make([]byte, n)
	if err := fatReadAt(t.r, b, t.off+k*fatBlock); err != nil {
		return nil, err
	}
	if len(t.cache) >= fatCacheBlocks {
		clear(t.cache)
	}
	t.cache[k] = b
	return b, nil
}

// bytes reads n bytes of the FAT at off; they may cross blocks (FAT12)
func (t *fatTable) bytes(off int64, n int) ([]byte, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]byte, 0, n)
	for len(out) < n {
		o := off + int64(len(out))
		b, err := t.block(o / fatBlock)
		if err != nil {
			return nil, err
		}
		out = append(out, b[o%fatBlock:][:min(n-len(out), len(b)-int(o%fatBlock))]...)
	}
	return out, nil
}

func (t *fatTable) u16(off int64) (uint16, error) {
	b, err := t.bytes(off, 2)
	if err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint16(b), nil
}

func (t *fatTable) u32(off int64) (uint32, error) {
	b, err := t.bytes(off, 4)
	if err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint32(b), nil
}

// fatFollow follows a chain of clusters 2..nClus+1 from first, at most max
// of them; next gives the cluster after c and whether c is the last. A
// cluster seen twice (a loop) is an error.
func fatFollow(first uint32, max int, nClus uint32, next func(c uint32) (uint32, bool, error)) ([]uint32, error) {
	var chain []uint32
	c := first
	for len(chain) < max {
		if c < 2 || c > nClus+1 {
			return nil, fmt.Errorf("%w: cluster %d", errFATChain, c)
		}
		chain = append(chain, c)
		n, last, err := next(c)
		if err != nil {
			return nil, err
		}
		if last {
			break
		}
		c = n
	}
	// A loop repeats a cluster; sorting a copy finds it without a bitmap
	// of the whole volume
	s := slices.Clone(chain)
	slices.Sort(s)
	for i := 1; i < len(s); i++ {
		if s[i] == s[i-1] {
			return nil, fmt.Errorf("%w: a loop at cluster %d", errFATChain, s[i])
		}
	}
	return chain, nil
}

// fatChainExtents makes runs of the clusters of a chain, contiguous ones
// merged; size cuts the last
func fatChainExtents(chain []uint32, dataOff, clusSz, size int64) []Extent {
	var out []Extent
	var pos int64
	for i := 0; i < len(chain) && pos < size; {
		j := i + 1
		for j < len(chain) && chain[j] == chain[j-1]+1 {
			j++
		}
		n := min(int64(j-i)*clusSz, size-pos)
		out = append(out, Extent{FileOff: pos, DiskOff: dataOff + int64(chain[i]-2)*clusSz, Len: n})
		pos += n
		i = j
	}
	return out
}

// fatRunReader reads a file from its extents; past valid it reads zeros
// (the valid data length of exFAT)
type fatRunReader struct {
	r     io.ReaderAt
	ext   []Extent
	size  int64
	valid int64
}

func (x *fatRunReader) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 {
		return 0, errors.New("negative offset")
	}
	n := 0
	for n < len(p) && off < x.size {
		q := p[n:min(int64(len(p)), int64(n)+x.size-off)]
		i := sort.Search(len(x.ext), func(i int) bool { return x.ext[i].FileOff+x.ext[i].Len > off })
		var k int
		switch {
		case off >= x.valid:
			k = len(q)
			clear(q)
		case i == len(x.ext) || x.ext[i].FileOff > off || x.ext[i].DiskOff < 0:
			// A hole: zeros up to the next run
			k = len(q)
			if i < len(x.ext) && x.ext[i].FileOff > off {
				k = int(min(int64(k), x.ext[i].FileOff-off))
			} else if i < len(x.ext) {
				// A run marked as a hole: zeros to its end
				k = int(min(int64(k), x.ext[i].FileOff+x.ext[i].Len-off))
			}
			k = int(min(int64(k), x.valid-off))
			clear(q[:k])
		default:
			e := x.ext[i]
			k = int(min(int64(len(q)), e.FileOff+e.Len-off, x.valid-off))
			if err := fatReadAt(x.r, q[:k], e.DiskOff+off-e.FileOff); err != nil {
				return n, err
			}
		}
		n += k
		off += int64(k)
	}
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

// fatDirs keeps parsed directories by path, up to fatDirCache of them
type fatDirs[E any] struct {
	mu sync.Mutex
	m  map[string][]E
}

func (c *fatDirs[E]) get(p string) ([]E, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	l, ok := c.m[p]
	return l, ok
}

func (c *fatDirs[E]) put(p string, l []E) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil || len(c.m) >= fatDirCache {
		c.m = map[string][]E{}
	}
	c.m[p] = l
}
