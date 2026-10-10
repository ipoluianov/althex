package fs

import (
	"bytes"
	"io"
	"sort"
	"time"
)

// ext2, ext3 and ext4: the superblock at 1024, the group descriptors, the
// inodes with their block maps (extent trees or indirect blocks) or inline
// data, linear and hashed directories. The checksums (metadata_csum) are
// not verified: a hex editor wants to see damaged volumes too.

func init() { register(probeExt) }

const (
	extMagic = 0xEF53

	extCompatJournal = 0x4

	extIncompatFiletype = 0x2
	extIncompatExtents  = 0x40
	extIncompat64Bit    = 0x80
	extIncompatMetaBG   = 0x10
	extIncompatFlexBG   = 0x200
	extIncompatLargeDir = 0x4000
	extIncompatInline   = 0x8000

	extROCompatSparse = 0x1

	extFlagExtents = 0x80000
	extFlagInline  = 0x10000000

	extModeType = 0xF000
	extModeDir  = 0x4000
	extModeReg  = 0x8000
	extModeLink = 0xA000

	extRootIno = 2

	// Caps against damaged volumes
	extMaxDepth     = 5       // of an extent tree; the kernel allows 5
	extMaxMapBlocks = 1 << 20 // index or indirect blocks read for one file
	extMaxDir       = 1 << 28 // bytes of a directory
	extMaxExtents   = 1 << 22 // extents of a file
)

type extFS struct {
	r    io.ReaderAt
	size int64

	typ, label string

	bs          int64 // block size
	blocks      uint64
	firstData   uint64
	bpg, ipg    uint64 // blocks and inodes per group
	inodes      uint64
	inodeSize   int64
	descSize    int64
	groups      uint64
	incompat    uint32
	rocompat    uint32
	firstMetaBG uint64
}

// extInode is an inode as read
type extInode struct {
	ino   uint32
	raw   []byte
	mode  uint16
	size  int64
	flags uint32
	mtime time.Time
}

func (in *extInode) isDir() bool  { return in.mode&extModeType == extModeDir }
func (in *extInode) isLink() bool { return in.mode&extModeType == extModeLink }

func probeExt(r io.ReaderAt, size int64) (FS, bool, error) {
	if size < 2048 {
		return nil, false, nil
	}
	sb := make([]byte, 1024)
	if n, _ := r.ReadAt(sb, 1024); n < len(sb) || xLE16(sb[0x38:]) != extMagic {
		return nil, false, nil
	}
	e, err := newExt(r, size, sb)
	if err != nil {
		return nil, true, err
	}
	return e, true, nil
}

func newExt(r io.ReaderAt, size int64, sb []byte) (*extFS, error) {
	e := &extFS{r: r, size: size}
	logBS := xLE32(sb[0x18:])
	if logBS > 6 {
		return nil, xDamaged("ext block size 1024<<%d", logBS)
	}
	e.bs = 1024 << logBS
	e.inodes = uint64(xLE32(sb[0x0:]))
	e.blocks = uint64(xLE32(sb[0x4:]))
	e.firstData = uint64(xLE32(sb[0x14:]))
	e.bpg = uint64(xLE32(sb[0x20:]))
	e.ipg = uint64(xLE32(sb[0x28:]))
	compat := xLE32(sb[0x5C:])
	e.incompat = xLE32(sb[0x60:])
	e.rocompat = xLE32(sb[0x64:])
	e.inodeSize = 128
	if xLE32(sb[0x4C:]) >= 1 { // dynamic revision: the inode size is given
		e.inodeSize = int64(xLE16(sb[0x58:]))
	}
	e.descSize = 32
	if e.incompat&extIncompat64Bit != 0 {
		e.blocks |= uint64(xLE32(sb[0x150:])) << 32
		e.descSize = int64(xLE16(sb[0xFE:]))
	}
	e.firstMetaBG = uint64(xLE32(sb[0x104:]))
	switch {
	case e.bpg == 0 || e.ipg == 0 || e.bpg > uint64(8*e.bs):
		return nil, xDamaged("ext groups of %d blocks, %d inodes", e.bpg, e.ipg)
	case e.inodeSize < 128 || e.inodeSize > e.bs || e.inodeSize&(e.inodeSize-1) != 0:
		return nil, xDamaged("ext inode size %d", e.inodeSize)
	case e.descSize < 32 || e.descSize > e.bs || e.descSize&(e.descSize-1) != 0:
		return nil, xDamaged("ext group descriptor size %d", e.descSize)
	case e.blocks <= e.firstData || e.firstData > 1 || e.blocks > 1<<62/uint64(e.bs):
		return nil, xDamaged("ext of %d blocks from %d", e.blocks, e.firstData)
	}
	e.groups = (e.blocks - e.firstData + e.bpg - 1) / e.bpg
	if e.inodes > e.groups*e.ipg {
		e.inodes = e.groups * e.ipg
	}
	e.label = string(bytes.TrimRight(sb[0x78:0x88], "\x00"))
	const ext4ROCompat = 0x8 | 0x20 | 0x40 | 0x400 // huge_file, dir_nlink, extra_isize, metadata_csum
	switch {
	case e.incompat&(extIncompatExtents|extIncompat64Bit|extIncompatFlexBG|extIncompatInline) != 0 || e.rocompat&ext4ROCompat != 0:
		e.typ = "ext4"
	case compat&extCompatJournal != 0:
		e.typ = "ext3"
	default:
		e.typ = "ext2"
	}
	return e, nil
}

func (e *extFS) Type() string  { return e.typ }
func (e *extFS) Label() string { return e.label }

// hasSuper tells if a group has a copy of the superblock (and so of the
// descriptors): with sparse_super only 0, 1 and the powers of 3, 5 and 7
func (e *extFS) hasSuper(g uint64) bool {
	if e.rocompat&extROCompatSparse == 0 || g <= 1 {
		return true
	}
	for _, p := range []uint64{3, 5, 7} {
		x := p
		for x < g {
			x *= p
		}
		if x == g {
			return true
		}
	}
	return false
}

// inodeTable gives the first block of the inode table of a group
func (e *extFS) inodeTable(g uint64) (uint64, error) {
	if g >= e.groups {
		return 0, xDamaged("ext group %d of %d", g, e.groups)
	}
	perBlock := uint64(e.bs / e.descSize)
	var blk, idx uint64
	if e.incompat&extIncompatMetaBG != 0 && g >= e.firstMetaBG*perBlock {
		// meta_bg: the descriptors of a meta group are in its first group
		first := g / perBlock * perBlock
		blk = e.firstData + first*e.bpg
		if e.hasSuper(first) {
			blk++
		}
		idx = g % perBlock
	} else {
		blk, idx = e.firstData+1+g/perBlock, g%perBlock
	}
	d, err := xRead(e.r, e.size, int64(blk)*e.bs+int64(idx)*e.descSize, e.descSize)
	if err != nil {
		return 0, err
	}
	t := uint64(xLE32(d[0x8:]))
	if e.descSize >= 64 {
		t |= uint64(xLE32(d[0x28:])) << 32
	}
	if t == 0 || t >= e.blocks {
		return 0, xDamaged("ext inode table of group %d at block %d", g, t)
	}
	return t, nil
}

// inode reads an inode
func (e *extFS) inode(ino uint32) (*extInode, error) {
	if ino == 0 || uint64(ino) > e.inodes {
		return nil, xDamaged("ext inode %d", ino)
	}
	g, idx := uint64(ino-1)/e.ipg, uint64(ino-1)%e.ipg
	t, err := e.inodeTable(g)
	if err != nil {
		return nil, err
	}
	raw, err := xRead(e.r, e.size, int64(t)*e.bs+int64(idx)*e.inodeSize, e.inodeSize)
	if err != nil {
		return nil, err
	}
	in := &extInode{ino: ino, raw: raw, mode: xLE16(raw), flags: xLE32(raw[0x20:])}
	size := uint64(xLE32(raw[0x4:]))
	// The high half of the size of a directory was dir_acl before large_dir
	if in.mode&extModeType != extModeDir || e.incompat&extIncompatLargeDir != 0 {
		size |= uint64(xLE32(raw[0x6C:])) << 32
	}
	if size > 1<<62 {
		return nil, xDamaged("ext inode %d of %d bytes", ino, size)
	}
	in.size = int64(size)
	sec := int64(int32(xLE32(raw[0x10:])))
	var nsec int64
	if e.inodeSize > 128 && 0x80+int64(xLE16(raw[0x80:])) >= 0x8C {
		// The extra field: 2 more bits of the seconds and the nanoseconds
		x := xLE32(raw[0x88:])
		sec += int64(x&3) << 32
		nsec = int64(x >> 2)
		if nsec >= 1e9 {
			nsec = 0
		}
	}
	in.mtime = time.Unix(sec, nsec).UTC()
	return in, nil
}

// fastLink tells if a symlink keeps its target in the inode itself
func (e *extFS) fastLink(in *extInode) bool {
	if !in.isLink() || in.flags&(extFlagExtents|extFlagInline) != 0 || in.size >= 60 {
		return false
	}
	// No blocks but the one of the extended attributes, if any
	blocks := uint64(xLE32(in.raw[0x1C:]))
	if xLE32(in.raw[0x68:]) != 0 {
		blocks -= min(blocks, uint64(e.bs/512))
	}
	return blocks == 0
}

// runs maps a file to the volume; nil for one stored in the inode
func (e *extFS) runs(in *extInode) ([]Extent, error) {
	if in.flags&extFlagInline != 0 || e.fastLink(in) {
		return nil, nil
	}
	var raw []Extent
	var err error
	if in.flags&extFlagExtents != 0 {
		budget := extMaxMapBlocks
		err = e.walkExtents(in.raw[0x28:0x28+60], -1, &raw, &budget)
	} else {
		raw, err = e.indirect(in)
	}
	if err != nil {
		return nil, err
	}
	return xNormalize(raw, in.size)
}

// walkExtents adds the extents of a node of an extent tree; depth is the
// one the parent expects (-1 at the root)
func (e *extFS) walkExtents(node []byte, depth int, out *[]Extent, budget *int) error {
	if len(node) < 12 || xLE16(node) != 0xF30A {
		return xDamaged("ext extent header")
	}
	n, d := int(xLE16(node[2:])), int(xLE16(node[6:]))
	if d > extMaxDepth || depth >= 0 && d != depth {
		return xDamaged("ext extent depth %d", d)
	}
	if 12+12*n > len(node) {
		return xDamaged("ext extent node of %d entries", n)
	}
	for i := 0; i < n; i++ {
		x := node[12+12*i:]
		if d == 0 {
			lblk := uint64(xLE32(x))
			l := uint64(xLE16(x[4:]))
			uninit := l > 32768
			if uninit {
				l -= 32768
			}
			p := uint64(xLE16(x[6:]))<<32 | uint64(xLE32(x[8:]))
			if p+l > e.blocks {
				return xDamaged("ext extent at block %d past the end", p)
			}
			if len(*out) >= extMaxExtents {
				return xDamaged("ext file of too many extents")
			}
			ext := Extent{FileOff: int64(lblk) * e.bs, DiskOff: int64(p) * e.bs, Len: int64(l) * e.bs}
			if uninit {
				ext.DiskOff = -1 // allocated but not written: zeros
			}
			*out = append(*out, ext)
			continue
		}
		p := uint64(xLE16(x[8:]))<<32 | uint64(xLE32(x[4:]))
		if *budget--; *budget < 0 {
			return xDamaged("ext extent tree too large")
		}
		if p >= e.blocks {
			return xDamaged("ext extent index to block %d", p)
		}
		b, err := xRead(e.r, e.size, int64(p)*e.bs, e.bs)
		if err != nil {
			return err
		}
		if err := e.walkExtents(b, d-1, out, budget); err != nil {
			return err
		}
	}
	return nil
}

// indirect maps a file of ext2/3: 12 direct blocks, then the single,
// double and triple indirect ones
func (e *extFS) indirect(in *extInode) ([]Extent, error) {
	nblocks := uint64((in.size + e.bs - 1) / e.bs)
	per := uint64(e.bs / 4)
	var out []Extent
	var lb, mapped uint64
	budget := extMaxMapBlocks
	add := func(p uint64) error {
		if p != 0 {
			if p >= e.blocks {
				return xDamaged("ext block %d past the end", p)
			}
			// A file cannot have more blocks than the volume
			if mapped++; mapped > e.blocks {
				return xDamaged("ext file of more blocks than the volume")
			}
			if n := len(out); n > 0 && out[n-1].FileOff+out[n-1].Len == int64(lb)*e.bs && out[n-1].DiskOff+out[n-1].Len == int64(p)*e.bs {
				out[n-1].Len += e.bs
			} else {
				out = append(out, Extent{FileOff: int64(lb) * e.bs, DiskOff: int64(p) * e.bs, Len: e.bs})
			}
		}
		lb++
		return nil
	}
	var walk func(level int, p uint64) error
	walk = func(level int, p uint64) error {
		if lb >= nblocks {
			return nil
		}
		if level == 0 {
			return add(p)
		}
		span := uint64(1)
		for range level {
			span *= per
		}
		if p == 0 { // a hole of the whole subtree
			lb += span
			return nil
		}
		if p >= e.blocks {
			return xDamaged("ext indirect block %d past the end", p)
		}
		if budget--; budget < 0 {
			return xDamaged("ext block map too large")
		}
		b, err := xRead(e.r, e.size, int64(p)*e.bs, e.bs)
		if err != nil {
			return err
		}
		for i := uint64(0); i < per && lb < nblocks; i++ {
			if err := walk(level-1, uint64(xLE32(b[4*i:]))); err != nil {
				return err
			}
		}
		return nil
	}
	for i := 0; i < 15 && lb < nblocks; i++ {
		level := 0
		if i >= 12 {
			level = i - 11
		}
		if err := walk(level, uint64(xLE32(in.raw[0x28+4*i:]))); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// inlineData gives the bytes of a file kept in the inode: the 60 bytes of
// i_block and the rest in the extended attribute system.data
func (e *extFS) inlineData(in *extInode) []byte {
	b := append([]byte(nil), in.raw[0x28:0x28+60]...)
	b = append(b, e.inlineXattr(in)...)
	if int64(len(b)) > in.size {
		b = b[:in.size]
	}
	return b
}

// inlineXattr finds system.data among the attributes inside the inode
func (e *extFS) inlineXattr(in *extInode) []byte {
	if e.inodeSize <= 128 {
		return nil
	}
	start := 0x80 + int(xLE16(in.raw[0x80:]))
	if start+4 > len(in.raw) || xLE32(in.raw[start:]) != 0xEA020000 {
		return nil
	}
	base := start + 4 // value offsets are from the first entry
	for off := base; off+16 <= len(in.raw); {
		nl := int(in.raw[off])
		if nl == 0 && in.raw[off+1] == 0 && xLE16(in.raw[off+2:]) == 0 {
			break // the end: four zero bytes
		}
		idx := in.raw[off+1]
		voff, vsize := int(xLE16(in.raw[off+2:])), int(xLE32(in.raw[off+8:]))
		if off+16+nl > len(in.raw) {
			break
		}
		name := string(in.raw[off+16 : off+16+nl])
		if idx == 7 && name == "data" && xLE32(in.raw[off+4:]) == 0 {
			if base+voff+vsize <= len(in.raw) && vsize >= 0 {
				return in.raw[base+voff : base+voff+vsize]
			}
			return nil
		}
		off += (16 + nl + 3) &^ 3
	}
	return nil
}

// reader gives the contents of an inode
func (e *extFS) reader(in *extInode) (io.ReaderAt, error) {
	if in.flags&extFlagInline != 0 {
		return bytes.NewReader(e.inlineData(in)), nil
	}
	if e.fastLink(in) {
		return bytes.NewReader(in.raw[0x28 : 0x28+in.size]), nil
	}
	runs, err := e.runs(in)
	if err != nil {
		return nil, err
	}
	return &xRunsReader{r: e.r, runs: runs, size: in.size}, nil
}

// extDirent is a name in a directory
type extDirent struct {
	name string
	ino  uint32
	typ  uint8 // the file type of the entry; 0 when the volume has none
}

// dirents lists a directory; "." and ".." are left out
func (e *extFS) dirents(in *extInode) ([]extDirent, error) {
	if !in.isDir() {
		return nil, ErrNotDir
	}
	var out []extDirent
	if in.flags&extFlagInline != 0 {
		// 4 bytes of the parent, then entries in the rest of i_block and in
		// system.data, each a block of its own
		if err := e.parseDirBlock(in.raw[0x28+4:0x28+60], &out); err != nil {
			return nil, err
		}
		if x := e.inlineXattr(in); len(x) > 0 {
			if err := e.parseDirBlock(x, &out); err != nil {
				return nil, err
			}
		}
		return out, nil
	}
	r, err := e.reader(in)
	if err != nil {
		return nil, err
	}
	if in.size > int64(e.blocks)*e.bs {
		return nil, xDamaged("ext directory of %d bytes", in.size)
	}
	data, err := xReadAll(r, in.size, extMaxDir)
	if err != nil {
		return nil, err
	}
	// Hashed directories (htree) are read as linear ones: the dx_root and
	// dx_node blocks hide their index inside entries that are skipped
	for off := int64(0); off < int64(len(data)); off += e.bs {
		if err := e.parseDirBlock(data[off:min(off+e.bs, int64(len(data)))], &out); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (e *extFS) parseDirBlock(b []byte, out *[]extDirent) error {
	for off := 0; off+8 <= len(b); {
		ino := xLE32(b[off:])
		rl := int(xLE16(b[off+4:]))
		if e.bs == 65536 && (rl == 65535 || rl == 0) {
			rl = 65536 // the length of a whole 64K block does not fit
		}
		if rl < 8 || rl%4 != 0 || off+rl > len(b) {
			return xDamaged("ext directory entry of %d bytes", rl)
		}
		nl, ft := int(b[off+6]), b[off+7]
		if e.incompat&extIncompatFiletype == 0 {
			ft = 0
		}
		if ino != 0 && nl > 0 && 8+nl <= rl {
			name := string(b[off+8 : off+8+nl])
			if name != "." && name != ".." {
				*out = append(*out, extDirent{name: name, ino: ino, typ: ft})
			}
		}
		off += rl
	}
	return nil
}

// lookup finds the inode of a path; names are the names on the way.
// Symbolic links are not followed.
func (e *extFS) lookup(path string) (*extInode, []string, error) {
	names := xSplit(path)
	in, err := e.inode(extRootIno)
	if err != nil {
		return nil, nil, err
	}
	for _, name := range names {
		if !in.isDir() {
			return nil, nil, ErrNotDir
		}
		ents, err := e.dirents(in)
		if err != nil {
			return nil, nil, err
		}
		var ino uint32
		for _, d := range ents {
			if d.name == name {
				ino = d.ino
				break
			}
		}
		if ino == 0 {
			return nil, nil, ErrNotFound
		}
		if in, err = e.inode(ino); err != nil {
			return nil, nil, err
		}
	}
	return in, names, nil
}

func (e *extFS) entry(in *extInode, names []string) Entry {
	ent := Entry{Path: xJoin(names), IsDir: in.isDir(), Size: in.size, Mode: uint32(in.mode), ModTime: in.mtime, ID: uint64(in.ino)}
	if len(names) > 0 {
		ent.Name = names[len(names)-1]
	} else {
		ent.Name = "/"
	}
	if ent.IsDir {
		ent.Size = 0
	}
	return ent
}

func (e *extFS) ReadDir(path string) ([]Entry, error) {
	in, names, err := e.lookup(path)
	if err != nil {
		return nil, err
	}
	ents, err := e.dirents(in)
	if err != nil {
		return nil, err
	}
	out := make([]Entry, 0, len(ents))
	for _, d := range ents {
		sub := append(names[:len(names):len(names)], d.name)
		ci, err := e.inode(d.ino)
		if err != nil {
			// A damaged inode still shows its name
			out = append(out, Entry{Name: d.name, Path: xJoin(sub), IsDir: d.typ == 2, ID: uint64(d.ino)})
			continue
		}
		out = append(out, e.entry(ci, sub))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (e *extFS) Stat(path string) (Entry, error) {
	in, names, err := e.lookup(path)
	if err != nil {
		return Entry{}, err
	}
	return e.entry(in, names), nil
}

func (e *extFS) Extents(path string) ([]Extent, error) {
	in, _, err := e.lookup(path)
	if err != nil {
		return nil, err
	}
	return e.runs(in)
}

func (e *extFS) Open(path string) (io.ReaderAt, int64, error) {
	in, _, err := e.lookup(path)
	if err != nil {
		return nil, 0, err
	}
	if in.isDir() {
		return nil, 0, ErrIsDir
	}
	if in.flags&extFlagInline != 0 {
		b := e.inlineData(in) // shorter than i_size when damaged
		return bytes.NewReader(b), int64(len(b)), nil
	}
	r, err := e.reader(in)
	if err != nil {
		return nil, 0, err
	}
	return r, in.size, nil
}

// Readlink gives the target of a symbolic link
func (e *extFS) Readlink(path string) (string, error) {
	in, _, err := e.lookup(path)
	if err != nil {
		return "", err
	}
	if !in.isLink() {
		return "", xDamaged("not a symbolic link")
	}
	r, err := e.reader(in)
	if err != nil {
		return "", err
	}
	b, err := xReadAll(r, in.size, 1<<16)
	return string(b), err
}
