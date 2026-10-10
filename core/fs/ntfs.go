package fs

import (
	"bytes"
	"errors"
	"io"
	"sort"
	"strings"
	"sync"
	"time"
)

// NTFS: the boot sector, the MFT read through its own data runs, MFT
// records with the update sequence applied and their attributes (also those
// moved to other records by $ATTRIBUTE_LIST), directories from the $I30
// index (INDEX_ROOT and INDX blocks of INDEX_ALLOCATION) and files from the
// unnamed $DATA: resident, non-resident (data runs), sparse and LZNT1
// compressed. Alternate data streams are not shown; encrypted files read as
// they are stored.

func init() { register(probeNTFS) }

const (
	ntfsAttrStdInfo  = 0x10
	ntfsAttrList     = 0x20
	ntfsAttrFileName = 0x30
	ntfsAttrVolName  = 0x60
	ntfsAttrData     = 0x80
	ntfsAttrIdxRoot  = 0x90
	ntfsAttrIdxAlloc = 0xA0
	ntfsAttrEnd      = 0xFFFFFFFF

	ntfsRecInUse = 0x1
	ntfsRecDir   = 0x2

	ntfsFlagCompressed = 0x1

	ntfsRefMask = 1<<48 - 1 // the record number of a file reference
	ntfsRootRec = 5
	ntfsVolRec  = 3

	ntfsNameDOS = 2 // the namespace of an 8.3 name that has a long one too

	// Caps against damaged volumes
	ntfsMaxAttrs     = 1024    // attributes of a record
	ntfsMaxRuns      = 1 << 20 // data runs of an attribute
	ntfsMaxListRecs  = 4096    // records of an attribute list
	ntfsMaxListBytes = 1 << 22
	ntfsMaxIdxDepth  = 32      // of an index B+ tree
	ntfsMaxIdxBlocks = 1 << 20 // INDX blocks read for one directory
	ntfsMaxUnit      = 1 << 20 // bytes of a compression unit
)

type ntfsFS struct {
	r    io.ReaderAt
	size int64

	cs       int64 // cluster size
	clusters int64
	recSize  int64
	mft      []Extent // where the MFT is: the runs of its $DATA
	mftSize  int64
	label    string
}

// ntfsAttr is an attribute of a file
type ntfsAttr struct {
	typ      uint32
	name     string
	flags    uint16
	resident bool
	value    []byte // resident

	// Non-resident: the runs from startVCN, in bytes; the sizes are only
	// in the piece that starts at VCN 0
	startVCN                      uint64
	runs                          []Extent
	allocSize, dataSize, initSize int64
	cu                            uint8 // log2 of the clusters of a compression unit
}

// ntfsFile is a file: its base record and the attributes of all its records
type ntfsFile struct {
	no    uint64
	flags uint16
	attrs []ntfsAttr
}

func (f *ntfsFile) isDir() bool { return f.flags&ntfsRecDir != 0 }

func probeNTFS(r io.ReaderAt, size int64) (FS, bool, error) {
	if size < 512 {
		return nil, false, nil
	}
	b := make([]byte, 512)
	if n, _ := r.ReadAt(b, 0); n < len(b) || string(b[3:11]) != "NTFS    " {
		return nil, false, nil
	}
	n, err := newNTFS(r, size, b)
	if err != nil {
		return nil, true, err
	}
	return n, true, nil
}

// ntfsSize decodes the size of an MFT record or an index block: clusters,
// or when negative, 2 to the power of minus it in bytes
func ntfsSize(v byte, cs int64) int64 {
	if int8(v) < 0 {
		// In int: -int8(0x80) is -128 again
		e := -int(int8(v))
		if e > 20 {
			return 0
		}
		return 1 << e
	}
	return int64(v) * cs
}

func newNTFS(r io.ReaderAt, size int64, boot []byte) (*ntfsFS, error) {
	bps := int64(xLE16(boot[0x0B:]))
	spc := int64(boot[0x0D])
	if spc > 0x80 { // large clusters: 2 to the power of minus it
		// 1<<63 would be negative, and pass the checks below by overflow
		if e := 256 - spc; e <= 21 {
			spc = 1 << e
		} else {
			spc = 0
		}
	}
	if bps < 256 || bps > 4096 || bps&(bps-1) != 0 || spc <= 0 || spc&(spc-1) != 0 || bps*spc > 2<<20 {
		return nil, xDamaged("NTFS sectors of %d bytes, clusters of %d sectors", bps, spc)
	}
	n := &ntfsFS{r: r, size: size, cs: bps * spc}
	total := xLE64(boot[0x28:])
	if total > 1<<50 { // so that the offsets fit in int64
		return nil, xDamaged("NTFS of %d sectors", total)
	}
	n.clusters = int64(total) * bps / n.cs
	n.recSize = ntfsSize(boot[0x40], n.cs)
	if n.recSize < 512 || n.recSize > 64<<10 || n.recSize&(n.recSize-1) != 0 {
		return nil, xDamaged("NTFS records of %d bytes", n.recSize)
	}
	mftLCN := xLE64(boot[0x30:])
	if mftLCN >= uint64(n.clusters) {
		return nil, xDamaged("NTFS MFT at cluster %d", mftLCN)
	}
	// The record of $MFT tells where the rest of the MFT is
	rec, err := xRead(r, size, int64(mftLCN)*n.cs, n.recSize)
	if err != nil {
		return nil, err
	}
	if err := ntfsFixup(rec, "FILE"); err != nil {
		return nil, err
	}
	attrs, err := n.parseAttrs(rec)
	if err != nil {
		return nil, err
	}
	for _, a := range attrs {
		if a.typ == ntfsAttrData && a.name == "" && !a.resident && a.startVCN == 0 {
			n.mftSize = a.dataSize
			if n.mft, err = xNormalize(a.runs, a.dataSize); err != nil {
				return nil, err
			}
		}
	}
	if n.mft == nil {
		return nil, xDamaged("NTFS $MFT has no data")
	}
	// A fragmented MFT may have its runs in several records: the first
	// runs are enough to read those
	mft, err := n.file(0)
	if err != nil {
		return nil, err
	}
	if a, err := mft.stream(ntfsAttrData, ""); err == nil && a != nil && !a.resident {
		if runs, err := xNormalize(a.runs, a.dataSize); err == nil {
			n.mft, n.mftSize = runs, a.dataSize
		}
	}
	if _, err := n.file(ntfsRootRec); err != nil {
		return nil, err
	}
	if vol, err := n.file(ntfsVolRec); err == nil {
		for _, a := range vol.attrs {
			if a.typ == ntfsAttrVolName && a.resident {
				n.label = xUTF16(a.value)
			}
		}
	}
	return n, nil
}

func (n *ntfsFS) Type() string  { return "NTFS" }
func (n *ntfsFS) Label() string { return n.label }

// ntfsFixup checks the magic of a record and applies its update sequence:
// the last two bytes of every 512 were replaced by the sequence number when
// it was written, the real ones are kept in the array
func ntfsFixup(b []byte, magic string) error {
	if len(b) < 8 || string(b[:4]) != magic {
		return xDamaged("NTFS %s record magic", magic)
	}
	uo, uc := int(xLE16(b[4:])), int(xLE16(b[6:]))
	if uc == 0 || uo < 8 || uo+2*uc > len(b) || (uc-1)*512 > len(b) {
		return xDamaged("NTFS update sequence of %d at %d", uc, uo)
	}
	for i := 1; i < uc; i++ {
		p := i*512 - 2
		if b[p] != b[uo] || b[p+1] != b[uo+1] {
			return xDamaged("NTFS %s record torn at %d", magic, p)
		}
		b[p], b[p+1] = b[uo+2*i], b[uo+2*i+1]
	}
	return nil
}

// readRecord reads an MFT record with the update sequence applied
func (n *ntfsFS) readRecord(no uint64) ([]byte, error) {
	if no >= uint64(n.mftSize/n.recSize) {
		return nil, xDamaged("NTFS record %d past the MFT", no)
	}
	b := make([]byte, n.recSize)
	rr := &xRunsReader{r: n.r, runs: n.mft, size: n.mftSize}
	if k, err := rr.ReadAt(b, int64(no)*n.recSize); k < len(b) {
		return nil, err
	}
	if err := ntfsFixup(b, "FILE"); err != nil {
		return nil, err
	}
	return b, nil
}

// parseAttrs reads the attributes of a record
func (n *ntfsFS) parseAttrs(rec []byte) ([]ntfsAttr, error) {
	used := min(int(xLE32(rec[0x18:])), len(rec))
	var out []ntfsAttr
	off := int(xLE16(rec[0x14:]))
	for range ntfsMaxAttrs {
		if off+8 > used {
			return out, nil // no end marker: take what there is
		}
		typ := xLE32(rec[off:])
		if typ == ntfsAttrEnd {
			return out, nil
		}
		l := int(xLE32(rec[off+4:]))
		if l < 0x18 || l > used-off {
			return nil, xDamaged("NTFS attribute 0x%X of %d bytes", typ, l)
		}
		a, err := n.parseAttr(rec[off : off+l])
		if err != nil {
			return nil, err
		}
		out = append(out, a)
		off += l
	}
	return nil, xDamaged("NTFS record of too many attributes")
}

func (n *ntfsFS) parseAttr(b []byte) (ntfsAttr, error) {
	a := ntfsAttr{typ: xLE32(b), resident: b[8] == 0, flags: xLE16(b[0xC:])}
	nl, no := int(b[9]), int(xLE16(b[0xA:]))
	if nl > 0 {
		if no+2*nl > len(b) {
			return a, xDamaged("NTFS attribute name")
		}
		a.name = xUTF16(b[no : no+2*nl])
	}
	if a.resident {
		vl, vo := int(xLE32(b[0x10:])), int(xLE16(b[0x14:]))
		if vo > len(b) || vl > len(b)-vo {
			return a, xDamaged("NTFS resident value of %d bytes", vl)
		}
		a.value = b[vo : vo+vl]
		return a, nil
	}
	if len(b) < 0x40 {
		return a, xDamaged("NTFS non-resident attribute of %d bytes", len(b))
	}
	a.startVCN = xLE64(b[0x10:])
	ro := int(xLE16(b[0x20:]))
	a.cu = b[0x22]
	a.allocSize = int64(xLE64(b[0x28:]))
	a.dataSize = int64(xLE64(b[0x30:]))
	a.initSize = int64(xLE64(b[0x38:]))
	if a.allocSize < 0 || a.dataSize < 0 || a.initSize < 0 || a.startVCN > 1<<48 || a.dataSize > n.clusters*n.cs+(1<<40) {
		return a, xDamaged("NTFS attribute sizes")
	}
	if ro > len(b) {
		return a, xDamaged("NTFS data runs at %d", ro)
	}
	var err error
	a.runs, err = n.decodeRuns(b[ro:], a.startVCN)
	return a, err
}

// decodeRuns decodes data runs: a header byte with the sizes of the
// length and of the offset, the length in clusters, the offset from the
// previous run (signed); no offset is a sparse run
func (n *ntfsFS) decodeRuns(b []byte, vcn uint64) ([]Extent, error) {
	var out []Extent
	var lcn int64
	for i := 0; i < len(b) && b[i] != 0; {
		ls, os := int(b[i]&0xF), int(b[i]>>4)
		i++
		if ls == 0 || ls > 8 || os > 8 || i+ls+os > len(b) {
			return nil, xDamaged("NTFS data run header 0x%X", b[i-1])
		}
		var length uint64
		for k := ls - 1; k >= 0; k-- {
			length = length<<8 | uint64(b[i+k])
		}
		i += ls
		if length == 0 || length > 1<<40 || vcn+length > uint64(1<<62/n.cs) {
			return nil, xDamaged("NTFS data run of %d clusters", length)
		}
		e := Extent{FileOff: int64(vcn) * n.cs, DiskOff: -1, Len: int64(length) * n.cs}
		if os > 0 {
			var d int64
			for k := os - 1; k >= 0; k-- {
				d = d<<8 | int64(b[i+k])
			}
			if b[i+os-1]&0x80 != 0 { // negative: extend the sign
				d -= 1 << (8 * os)
			}
			i += os
			lcn += d
			if lcn < 0 || lcn+int64(length) > n.clusters {
				return nil, xDamaged("NTFS data run at cluster %d", lcn)
			}
			e.DiskOff = lcn * n.cs
		}
		if len(out) >= ntfsMaxRuns {
			return nil, xDamaged("NTFS too many data runs")
		}
		out = append(out, e)
		vcn += length
	}
	return out, nil
}

// file reads a file: its base record and the records of its attribute list
func (n *ntfsFS) file(no uint64) (*ntfsFile, error) {
	rec, err := n.readRecord(no)
	if err != nil {
		return nil, err
	}
	f := &ntfsFile{no: no, flags: xLE16(rec[0x16:])}
	if f.flags&ntfsRecInUse == 0 {
		return nil, xDamaged("NTFS record %d is not in use", no)
	}
	if xLE64(rec[0x20:])&ntfsRefMask != 0 {
		return nil, xDamaged("NTFS record %d is an extension of another", no)
	}
	if f.attrs, err = n.parseAttrs(rec); err != nil {
		return nil, err
	}
	var list *ntfsAttr
	for i := range f.attrs {
		if f.attrs[i].typ == ntfsAttrList {
			list = &f.attrs[i]
		}
	}
	if list == nil {
		return f, nil
	}
	v := list.value
	if !list.resident {
		runs, err := xNormalize(list.runs, list.dataSize)
		if err != nil {
			return nil, err
		}
		if v, err = xReadAll(&xRunsReader{r: n.r, runs: runs, size: list.dataSize}, list.dataSize, ntfsMaxListBytes); err != nil {
			return nil, err
		}
	}
	// The other records named in the list, each once
	seen := map[uint64]bool{no: true}
	var recs []uint64
	for off := 0; off+0x1A <= len(v); {
		l := int(xLE16(v[off+4:]))
		if l < 0x1A || l > len(v)-off {
			return nil, xDamaged("NTFS attribute list entry of %d bytes", l)
		}
		if r := xLE64(v[off+0x10:]) & ntfsRefMask; !seen[r] {
			seen[r] = true
			recs = append(recs, r)
			if len(recs) > ntfsMaxListRecs {
				return nil, xDamaged("NTFS attribute list of too many records")
			}
		}
		off += l
	}
	for _, r := range recs {
		rec, err := n.readRecord(r)
		if err != nil {
			return nil, err
		}
		if xLE64(rec[0x20:])&ntfsRefMask != no {
			return nil, xDamaged("NTFS record %d is not of file %d", r, no)
		}
		attrs, err := n.parseAttrs(rec)
		if err != nil {
			return nil, err
		}
		f.attrs = append(f.attrs, attrs...)
	}
	return f, nil
}

// stream joins the pieces of an attribute into one: the runs of all the
// pieces, the sizes of the first; nil when the file has none
func (f *ntfsFile) stream(typ uint32, name string) (*ntfsAttr, error) {
	var pieces []ntfsAttr
	for _, a := range f.attrs {
		if a.typ == typ && a.name == name {
			if a.resident {
				return &a, nil
			}
			pieces = append(pieces, a)
		}
	}
	if len(pieces) == 0 {
		return nil, nil
	}
	sort.Slice(pieces, func(i, j int) bool { return pieces[i].startVCN < pieces[j].startVCN })
	if pieces[0].startVCN != 0 {
		return nil, xDamaged("NTFS attribute 0x%X has no first piece", typ)
	}
	a := pieces[0]
	a.runs = nil
	for _, p := range pieces {
		a.runs = append(a.runs, p.runs...)
	}
	return &a, nil
}

// streamRuns gives the runs of a non-resident stream over its data size;
// past the initialized size it reads as zeros, so those parts are holes
func (n *ntfsFS) streamRuns(a *ntfsAttr) ([]Extent, error) {
	runs, err := xNormalize(append([]Extent(nil), a.runs...), a.dataSize)
	if err != nil || a.initSize >= a.dataSize {
		return runs, err
	}
	var out []Extent
	for _, e := range runs {
		switch {
		case e.DiskOff < 0 || e.FileOff+e.Len <= a.initSize:
			out = append(out, e)
		case e.FileOff >= a.initSize:
			out = append(out, Extent{FileOff: e.FileOff, DiskOff: -1, Len: e.Len})
		default:
			k := a.initSize - e.FileOff
			out = append(out, Extent{FileOff: e.FileOff, DiskOff: e.DiskOff, Len: k},
				Extent{FileOff: a.initSize, DiskOff: -1, Len: e.Len - k})
		}
	}
	return xNormalize(out, a.dataSize)
}

func (a *ntfsAttr) compressed() bool {
	return !a.resident && a.flags&ntfsFlagCompressed != 0 && a.cu != 0
}

// ntfsName is a $FILE_NAME: the key of a directory index
type ntfsName struct {
	parent uint64
	name   string
	space  byte
	mtime  time.Time
	size   int64
	dir    bool
}

func parseNTFSName(b []byte) (ntfsName, bool) {
	if len(b) < 0x42 || 0x42+2*int(b[0x40]) > len(b) {
		return ntfsName{}, false
	}
	return ntfsName{
		parent: xLE64(b) & ntfsRefMask,
		mtime:  xFileTime(xLE64(b[0x10:])),
		size:   int64(xLE64(b[0x30:]) &^ (1 << 63)),
		dir:    xLE32(b[0x38:])&0x10000000 != 0,
		space:  b[0x41],
		name:   xUTF16(b[0x42 : 0x42+2*int(b[0x40])]),
	}, true
}

var errNTFSStop = errors.New("stop")

// walkIndex goes through the $I30 index of a directory in order: the
// entries of a child node before the entry that points to it
func (n *ntfsFS) walkIndex(f *ntfsFile, visit func(ref uint64, key []byte) error) error {
	root, err := f.stream(ntfsAttrIdxRoot, "$I30")
	if err != nil {
		return err
	}
	if root == nil || !root.resident {
		return ErrNotDir
	}
	v := root.value
	if len(v) < 0x20 {
		return xDamaged("NTFS index root of %d bytes", len(v))
	}
	blockSize := int64(xLE32(v[8:]))
	entries, err := ntfsIndexEntries(v, 0x10)
	if err != nil {
		return err
	}
	var alloc io.ReaderAt
	var allocSize int64
	if a, err := f.stream(ntfsAttrIdxAlloc, "$I30"); err != nil {
		return err
	} else if a != nil && !a.resident {
		runs, err := n.streamRuns(a)
		if err != nil {
			return err
		}
		alloc, allocSize = &xRunsReader{r: n.r, runs: runs, size: a.dataSize}, a.dataSize
	}
	// The VCNs of the index count clusters, or 512 bytes when the blocks
	// are smaller than a cluster
	unit := n.cs
	if blockSize < n.cs {
		unit = 512
	}
	seen := map[uint64]bool{}
	var walk func(entries []byte, depth int) error
	walk = func(entries []byte, depth int) error {
		for off := 0; off < len(entries); {
			if off+16 > len(entries) {
				return xDamaged("NTFS index entry cut")
			}
			e := entries[off:]
			el, kl, fl := int(xLE16(e[8:])), int(xLE16(e[10:])), xLE16(e[12:])
			if el < 16 || el > len(e) || el%8 != 0 {
				return xDamaged("NTFS index entry of %d bytes", el)
			}
			if fl&1 != 0 { // a child node first
				if el < 24 {
					return xDamaged("NTFS index entry of %d bytes", el)
				}
				vcn := xLE64(e[el-8:])
				if depth >= ntfsMaxIdxDepth || seen[vcn] || len(seen) >= ntfsMaxIdxBlocks {
					return xDamaged("NTFS index loops at VCN %d", vcn)
				}
				seen[vcn] = true
				if alloc == nil || blockSize < 512 || blockSize > 64<<10 || vcn > uint64(allocSize/unit) {
					return xDamaged("NTFS index block %d", vcn)
				}
				blk := make([]byte, blockSize)
				if k, err := alloc.ReadAt(blk, int64(vcn)*unit); k < len(blk) {
					if err == nil {
						err = io.ErrUnexpectedEOF
					}
					return err
				}
				if err := ntfsFixup(blk, "INDX"); err != nil {
					return err
				}
				sub, err := ntfsIndexEntries(blk, 0x18)
				if err != nil {
					return err
				}
				if err := walk(sub, depth+1); err != nil {
					return err
				}
			}
			if fl&2 != 0 { // the last entry has no key
				return nil
			}
			if 16+kl > el {
				return xDamaged("NTFS index key of %d bytes", kl)
			}
			if err := visit(xLE64(e)&ntfsRefMask, e[16:16+kl]); err != nil {
				return err
			}
			off += el
		}
		return nil
	}
	return walk(entries, 0)
}

// ntfsIndexEntries gives the entries of an index node from its header at h
func ntfsIndexEntries(b []byte, h int) ([]byte, error) {
	if h+16 > len(b) {
		return nil, xDamaged("NTFS index header")
	}
	start, end := h+int(xLE32(b[h:])), h+int(xLE32(b[h+4:]))
	if start < h+16 || end < start || end > len(b) {
		return nil, xDamaged("NTFS index entries %d..%d", start, end)
	}
	return b[start:end], nil
}

// ntfsChild is a name of a directory
type ntfsChild struct {
	ref uint64
	fn  ntfsName
}

// children lists a directory: the DOS names of files that have long ones
// and the directory itself (".") are left out
func (n *ntfsFS) children(f *ntfsFile) ([]ntfsChild, error) {
	var out []ntfsChild
	err := n.walkIndex(f, func(ref uint64, key []byte) error {
		fn, ok := parseNTFSName(key)
		if ok && fn.space != ntfsNameDOS && ref != f.no {
			out = append(out, ntfsChild{ref, fn})
		}
		return nil
	})
	return out, err
}

// lookup finds the file of a path, ignoring the case; names are the names
// as stored
func (n *ntfsFS) lookup(path string) (*ntfsFile, []string, error) {
	f, err := n.file(ntfsRootRec)
	if err != nil {
		return nil, nil, err
	}
	var names []string
	for _, want := range xSplit(path) {
		if !f.isDir() {
			return nil, nil, ErrNotDir
		}
		var found *ntfsChild
		err := n.walkIndex(f, func(ref uint64, key []byte) error {
			fn, ok := parseNTFSName(key)
			if ok && ref != f.no && strings.EqualFold(fn.name, want) {
				found = &ntfsChild{ref, fn}
				return errNTFSStop
			}
			return nil
		})
		if err != nil && err != errNTFSStop {
			return nil, nil, err
		}
		if found == nil {
			return nil, nil, ErrNotFound
		}
		if f, err = n.file(found.ref); err != nil {
			return nil, nil, err
		}
		names = append(names, found.fn.name)
	}
	return f, names, nil
}

// entry makes the entry of a file
func (n *ntfsFS) entry(f *ntfsFile, names []string) Entry {
	e := Entry{Name: "/", Path: xJoin(names), IsDir: f.isDir(), ID: f.no}
	if len(names) > 0 {
		e.Name = names[len(names)-1]
	}
	for _, a := range f.attrs {
		if a.typ == ntfsAttrStdInfo && a.resident && len(a.value) >= 0x10 {
			e.ModTime = xFileTime(xLE64(a.value[8:]))
		}
	}
	if !e.IsDir {
		if a, err := f.stream(ntfsAttrData, ""); err == nil && a != nil {
			e.Size = a.dataSize
			if a.resident {
				e.Size = int64(len(a.value))
			}
		}
	}
	return e
}

func (n *ntfsFS) ReadDir(path string) ([]Entry, error) {
	f, names, err := n.lookup(path)
	if err != nil {
		return nil, err
	}
	if !f.isDir() {
		return nil, ErrNotDir
	}
	kids, err := n.children(f)
	if err != nil {
		return nil, err
	}
	out := make([]Entry, 0, len(kids))
	for _, k := range kids {
		sub := append(names[:len(names):len(names)], k.fn.name)
		cf, err := n.file(k.ref)
		if err != nil {
			// A damaged record still shows what its index entry knows
			out = append(out, Entry{Name: k.fn.name, Path: xJoin(sub), IsDir: k.fn.dir, Size: k.fn.size, ModTime: k.fn.mtime, ID: k.ref})
			continue
		}
		out = append(out, n.entry(cf, sub))
	}
	return out, nil
}

func (n *ntfsFS) Stat(path string) (Entry, error) {
	f, names, err := n.lookup(path)
	if err != nil {
		return Entry{}, err
	}
	return n.entry(f, names), nil
}

// Extents of a file: nil when it is resident or compressed. For a
// directory they are those of its index blocks.
func (n *ntfsFS) Extents(path string) ([]Extent, error) {
	f, _, err := n.lookup(path)
	if err != nil {
		return nil, err
	}
	typ, name := uint32(ntfsAttrData), ""
	if f.isDir() {
		typ, name = ntfsAttrIdxAlloc, "$I30"
	}
	a, err := f.stream(typ, name)
	if err != nil || a == nil || a.resident || a.compressed() {
		return nil, err
	}
	return n.streamRuns(a)
}

func (n *ntfsFS) Open(path string) (io.ReaderAt, int64, error) {
	f, _, err := n.lookup(path)
	if err != nil {
		return nil, 0, err
	}
	if f.isDir() {
		return nil, 0, ErrIsDir
	}
	a, err := f.stream(ntfsAttrData, "")
	switch {
	case err != nil:
		return nil, 0, err
	case a == nil:
		return bytes.NewReader(nil), 0, nil
	case a.resident:
		return bytes.NewReader(a.value), int64(len(a.value)), nil
	case a.compressed():
		r, err := n.compressedReader(a)
		return r, a.dataSize, err
	}
	runs, err := n.streamRuns(a)
	if err != nil {
		return nil, 0, err
	}
	return &xRunsReader{r: n.r, runs: runs, size: a.dataSize}, a.dataSize, nil
}

// ntfsCompReader reads a compressed file one compression unit at a time:
// a unit of all its clusters is stored as it is, one that ends with sparse
// clusters keeps LZNT1 data in the clusters before them, an all sparse one
// is zeros
type ntfsCompReader struct {
	raw        *xRunsReader // the stored clusters
	size, init int64
	unit       int64

	mu     sync.Mutex
	cached int64 // the unit in buf; -1 none
	buf    []byte
}

func (n *ntfsFS) compressedReader(a *ntfsAttr) (*ntfsCompReader, error) {
	unit := n.cs << a.cu
	if a.cu > 16 || unit > ntfsMaxUnit {
		return nil, xDamaged("NTFS compression unit of %d clusters", 1<<a.cu)
	}
	units := (a.dataSize + unit - 1) / unit * unit
	runs, err := xNormalize(append([]Extent(nil), a.runs...), units)
	if err != nil {
		return nil, err
	}
	return &ntfsCompReader{
		raw:  &xRunsReader{r: n.r, runs: runs, size: units},
		size: a.dataSize, init: min(a.initSize, a.dataSize), unit: unit, cached: -1,
	}, nil
}

func (c *ntfsCompReader) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 {
		return 0, errors.New("negative offset")
	}
	if off >= c.size {
		return 0, io.EOF
	}
	var eof error
	if int64(len(p)) > c.size-off {
		p, eof = p[:c.size-off], io.EOF
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for n := 0; n < len(p); {
		at := off + int64(n)
		u := at / c.unit
		if err := c.load(u); err != nil {
			return n, err
		}
		n += copy(p[n:], c.buf[at-u*c.unit:])
	}
	return len(p), eof
}

// load makes the bytes of a unit
func (c *ntfsCompReader) load(u int64) error {
	if c.cached == u {
		return nil
	}
	c.cached = -1
	if c.buf == nil {
		c.buf = make([]byte, c.unit)
	}
	clear(c.buf)
	start := u * c.unit
	// The stored bytes of the unit: up to its first sparse run
	var stored int64
	for _, e := range c.raw.runs {
		if e.FileOff+e.Len <= start+stored || e.FileOff >= start+c.unit {
			continue
		}
		if e.DiskOff < 0 {
			break
		}
		stored = min(e.FileOff+e.Len, start+c.unit) - start
	}
	switch {
	case stored == c.unit:
		if k, err := c.raw.ReadAt(c.buf, start); k < len(c.buf) {
			return err
		}
	case stored > 0:
		src := make([]byte, stored)
		if k, err := c.raw.ReadAt(src, start); k < len(src) {
			return err
		}
		if _, err := lznt1Decompress(c.buf, src); err != nil {
			return err
		}
	}
	if c.init < start+c.unit {
		clear(c.buf[max(c.init-start, 0):])
	}
	c.cached = u
	return nil
}
