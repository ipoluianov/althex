package fs

// exFAT (the Microsoft exFAT File System Specification): the boot sector,
// the FAT, the allocation bitmap, and the directory entry sets of files.

import (
	"encoding/binary"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"
	"unicode/utf16"
)

func init() { register(probeExFAT) }

type exfatFS struct {
	r        io.ReaderAt
	size     int64
	clusSz   int64
	heapOff  int64
	nClus    uint32
	rootClus uint32
	label    string
	fat      *fatTable
	dirs     fatDirs[exfatEntry]
	// The allocation bitmap: where it is, its size. It is kept for the
	// templates and checks; reading files does not need it.
	bitmapClus uint32
	bitmapLen  int64
}

type exfatEntry struct {
	Entry
	clus    uint32
	noChain bool  // the clusters are contiguous; the FAT has nothing for them
	valid   int64 // the valid data length: past it the file reads zeros
}

// probeExFAT checks the name, the signature, the zero BPB of FAT and the
// sizes of the boot sector
func probeExFAT(r io.ReaderAt, size int64) (FS, bool, error) {
	b := make([]byte, 512)
	if fatReadAt(r, b, 0) != nil || string(b[3:11]) != "EXFAT   " || b[510] != 0x55 || b[511] != 0xAA {
		return nil, false, nil
	}
	for _, c := range b[11:64] {
		if c != 0 {
			return nil, false, nil
		}
	}
	le32 := func(o int) int64 { return int64(binary.LittleEndian.Uint32(b[o:])) }
	bpsShift, spcShift := int(b[108]), int(b[109])
	if bpsShift < 9 || bpsShift > 12 || spcShift > 25-bpsShift || b[110] == 0 || b[110] > 2 {
		return nil, false, nil
	}
	bps := int64(1) << bpsShift
	f := &exfatFS{r: r, size: size, clusSz: bps << spcShift, fat: newFATTable(r)}
	fatOff, fatLen := le32(80), le32(84)
	heap, n := le32(88), le32(92)
	f.rootClus = uint32(le32(96))
	if fatOff < 24 || fatLen == 0 || heap < fatOff+fatLen || n == 0 || n > 0xFFFFFFF5 {
		return nil, false, nil
	}
	// The second FAT is used when bit 0 of VolumeFlags (ActiveFat) says so
	if b[110] == 2 && b[106]&1 != 0 {
		fatOff += fatLen
	}
	f.fat.off, f.fat.len = fatOff*bps, fatLen*bps
	f.heapOff = heap * bps
	if max := f.fat.len/4 - 2; n > max {
		n = max
	}
	if n < 1 {
		return nil, false, nil
	}
	f.nClus = uint32(n)
	if _, label, bm, err := f.loadDir(f.root()); err == nil {
		f.label = label
		f.bitmapClus, f.bitmapLen = bm.clus, bm.len
	}
	return f, true, nil
}

func (f *exfatFS) Type() string  { return "exFAT" }
func (f *exfatFS) Label() string { return f.label }

func (f *exfatFS) root() exfatEntry {
	return exfatEntry{Entry: Entry{Name: "/", Path: "/", IsDir: true, ID: uint64(f.rootClus)}, clus: f.rootClus}
}

func (f *exfatFS) ReadDir(p string) ([]Entry, error) {
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

func (f *exfatFS) Stat(p string) (Entry, error) {
	e, err := f.lookup(p)
	return e.Entry, err
}

func (f *exfatFS) Extents(p string) ([]Extent, error) {
	e, err := f.lookup(p)
	if err != nil {
		return nil, err
	}
	return f.extents(e)
}

func (f *exfatFS) Open(p string) (io.ReaderAt, int64, error) {
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
	return &fatRunReader{r: f.r, ext: ext, size: e.Size, valid: min(e.valid, e.Size)}, e.Size, nil
}

// extents of a file or a directory. The root has no stream extension: its
// size is that of its chain.
func (f *exfatFS) extents(e exfatEntry) ([]Extent, error) {
	size := e.Size
	if e.Path == "/" {
		size = int64(fatMaxDirEntries) * 32
	} else if e.IsDir {
		size = min(size, int64(fatMaxDirEntries)*32)
	}
	if size == 0 {
		return nil, nil
	}
	need := (size + f.clusSz - 1) / f.clusSz
	if e.noChain {
		if e.clus < 2 || int64(e.clus)-2+need > int64(f.nClus) {
			return nil, fmt.Errorf("%w: clusters %d+%d", errFATChain, e.clus, need)
		}
		return []Extent{{DiskOff: f.heapOff + int64(e.clus-2)*f.clusSz, Len: size}}, nil
	}
	need = min(need, int64(f.nClus))
	next := func(c uint32) (uint32, bool, error) {
		v, err := f.fat.u32(int64(c) * 4)
		return v, v == 0xFFFFFFFF, err
	}
	chain, err := fatFollow(e.clus, int(need), f.nClus, next)
	if err != nil {
		return nil, err
	}
	if e.Path == "/" {
		size = int64(len(chain)) * f.clusSz
	} else if int64(len(chain)) < need {
		return nil, fmt.Errorf("%w: %d clusters of %d", errFATChain, len(chain), need)
	}
	return fatChainExtents(chain, f.heapOff, f.clusSz, size), nil
}

// lookup finds the entry of a path. Names are compared with simple case
// folding, not the up-case table of the volume: they differ only for rare
// letters.
func (f *exfatFS) lookup(p string) (exfatEntry, error) {
	names, err := fatSplit(p)
	if err != nil {
		return exfatEntry{}, err
	}
	e := f.root()
	for _, name := range names {
		if !e.IsDir {
			return exfatEntry{}, ErrNotDir
		}
		list, err := f.readDir(e)
		if err != nil {
			return exfatEntry{}, err
		}
		i := slices.IndexFunc(list, func(c exfatEntry) bool { return strings.EqualFold(c.Name, name) })
		if i < 0 {
			return exfatEntry{}, ErrNotFound
		}
		e = list[i]
	}
	return e, nil
}

func (f *exfatFS) readDir(d exfatEntry) ([]exfatEntry, error) {
	if list, ok := f.dirs.get(d.Path); ok {
		return list, nil
	}
	list, _, _, err := f.loadDir(d)
	if err == nil {
		f.dirs.put(d.Path, list)
	}
	return list, err
}

// loadDir reads and parses a directory; the label and the bitmap are those
// of its entries (in the root)
func (f *exfatFS) loadDir(d exfatEntry) ([]exfatEntry, string, exfatBitmap, error) {
	var bm exfatBitmap
	ext, err := f.extents(d)
	if err != nil {
		return nil, "", bm, err
	}
	var size int64
	for _, x := range ext {
		size += x.Len
	}
	if err := fatInVolume(ext, f.size); err != nil {
		return nil, "", bm, err
	}
	b := make([]byte, size)
	if _, err := (&fatRunReader{r: f.r, ext: ext, size: size, valid: size}).ReadAt(b, 0); err != nil && err != io.EOF {
		return nil, "", bm, err
	}
	list, label, bm := exfatParseDir(b, d.Path)
	return list, label, bm, nil
}

type exfatBitmap struct {
	clus uint32
	len  int64
}

// exfatParseDir parses the entries of a directory: the sets of a file
// entry (0x85), a stream extension (0xC0) and file name entries (0xC1);
// the volume label (0x83) and the allocation bitmap (0x81). Sets that are
// not whole are skipped.
func exfatParseDir(b []byte, dir string) (list []exfatEntry, label string, bm exfatBitmap) {
	n := min(len(b)/32, fatMaxDirEntries)
	ent := func(i int) []byte { return b[i*32 : i*32+32] }
	for i := 0; i < n; i++ {
		d := ent(i)
		switch d[0] {
		case 0x00:
			return
		case 0x83:
			if k := int(d[1]); k <= 11 && label == "" {
				label = exfatUTF16(d[2 : 2+2*k])
			}
		case 0x81:
			if bm.clus == 0 && d[1]&1 == 0 {
				bm = exfatBitmap{binary.LittleEndian.Uint32(d[20:]), int64(binary.LittleEndian.Uint64(d[24:]))}
			}
		case 0x85:
			sc := int(d[1])
			if sc < 2 || sc > 18 || i+sc >= n {
				continue
			}
			s := ent(i + 1)
			if s[0] != 0xC0 {
				continue
			}
			nameLen := int(s[3])
			var name []byte
			ok := true
			for k := 2; k <= sc; k++ {
				x := ent(i + k)
				if x[0] != 0xC1 {
					// Other secondary entries (vendor ones) may follow
					// the names; an unused one breaks the set
					if x[0]&0x80 == 0 {
						ok = false
					}
					continue
				}
				name = append(name, x[2:32]...)
			}
			if !ok || nameLen == 0 || nameLen*2 > len(name) {
				continue
			}
			e := exfatEntry{
				clus:    binary.LittleEndian.Uint32(s[20:]),
				noChain: s[1]&2 != 0,
				valid:   int64(binary.LittleEndian.Uint64(s[8:])),
			}
			e.Name = exfatUTF16(name[:nameLen*2])
			e.Path = fatJoin(dir, e.Name)
			e.IsDir = binary.LittleEndian.Uint16(d[4:])&0x10 != 0
			e.Size = int64(binary.LittleEndian.Uint64(s[24:]))
			if e.Size < 0 || e.valid < 0 {
				continue
			}
			if s[1]&1 == 0 {
				// No allocation: an empty file
				e.clus, e.Size, e.valid = 0, 0, 0
			}
			e.ModTime = exfatTime(binary.LittleEndian.Uint32(d[12:]), d[21], d[23])
			e.ID = uint64(e.clus)
			list = append(list, e)
			i += sc
		}
	}
	return
}

func exfatUTF16(b []byte) string {
	u := make([]uint16, len(b)/2)
	for i := range u {
		u[i] = binary.LittleEndian.Uint16(b[2*i:])
	}
	if i := slices.Index(u, 0); i >= 0 {
		u = u[:i]
	}
	return string(utf16.Decode(u))
}

// exfatTime decodes a timestamp (a DOS date and time), its 10 ms part and
// its offset from UTC in 15 minutes (bit 7 - the offset is valid)
func exfatTime(ts uint32, ms10, utc byte) time.Time {
	if ts == 0 {
		return time.Time{}
	}
	loc := time.UTC
	if utc&0x80 != 0 {
		off := int(int8(utc<<1)>>1) * 15 * 60
		loc = time.FixedZone("", off)
	}
	ms := int(min(ms10, 199)) * 10
	return time.Date(1980+int(ts>>25), time.Month(ts>>21&15), int(ts>>16&31),
		int(ts>>11&31), int(ts>>5&63), int(ts&31)*2+ms/1000, (ms%1000)*1e6, loc)
}
