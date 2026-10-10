// Package diskimg reads the disk inside a disk image container (BOOT-1):
// VHD, VHDX, VMDK, QCOW2, VDI and Android sparse images. The disk is shown
// as its flat address space, read-only; what the container does not store
// reads as zeros.
//
// Differencing images (a VHD or VHDX with a parent, a QCOW2 with a backing
// file, a VMDK with a parent) keep only the changes, the rest is in another
// file; they are not supported and Open returns a *ParentError naming it.
package diskimg

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"sync"

	"github.com/ipoluianov/althex/core/source"
)

// Disk is the disk inside a container: read-only, safe for concurrent ReadAt
type Disk interface {
	io.ReaderAt
	Size() int64 // the virtual size of the disk
	Close() error
	// Format is the container: "VHD (dynamic)", "VHDX", "VMDK (sparse)",
	// "QCOW2", "VDI", "Android sparse"...
	Format() string
}

// ErrNotImage is returned for data that is not a container Detect knows
var ErrNotImage = errors.New("not a disk image of a known format")

// ParentError is returned for a differencing image: its file has only the
// changes, the rest of the disk is in the parent, which is not supported
type ParentError struct {
	Format string
	Parent string // the name of the parent as the image gives it
}

func (e *ParentError) Error() string {
	return fmt.Sprintf("%s: a differencing image of the parent %q is not supported", e.Format, e.Parent)
}

// errCorrupt makes the error of a damaged container
func errCorrupt(format, what string, args ...any) error {
	return fmt.Errorf("%s: damaged: %s", format, fmt.Sprintf(what, args...))
}

// The kinds of containers
const (
	kindNone = iota
	kindVHDFixed
	kindVHD // dynamic or differencing: the footer copy is at the start
	kindVHDX
	kindVMDKSparse
	kindVMDKDescriptor
	kindQCOW2
	kindVDI
	kindSparse
)

// Detect tells the container format from the first bytes and the size. A
// fixed VHD is only a footer at the end, so it is read too.
func Detect(r io.ReaderAt, size int64) (format string, ok bool) {
	kind, format := detect(r, size)
	return format, kind != kindNone
}

func detect(r io.ReaderAt, size int64) (int, string) {
	var b [512]byte
	n, _ := r.ReadAt(b[:min(int64(len(b)), max(size, 0))], 0)
	h := b[:n]
	has := func(off int, s string) bool { return len(h) >= off+len(s) && string(h[off:off+len(s)]) == s }
	switch {
	case has(0, "conectix") && len(h) >= 64:
		switch be32(h[60:]) {
		case vhdTypeFixed:
			// A fixed disk whose first sector looks like a footer: the real
			// footer is at the end
			if size >= 1024 && vhdFixedAtEnd(r, size) {
				return kindVHDFixed, "VHD (fixed)"
			}
		case vhdTypeDynamic:
			return kindVHD, "VHD (dynamic)"
		case vhdTypeDifferencing:
			return kindVHD, "VHD (differencing)"
		}
	case has(0, "vhdxfile"):
		return kindVHDX, "VHDX"
	case has(0, "KDMV") && len(h) >= 12:
		if le32(h[8:])&vmdkFlagCompressed != 0 {
			return kindVMDKSparse, "VMDK (stream-optimized)"
		}
		return kindVMDKSparse, "VMDK (sparse)"
	case isVMDKDescriptor(h):
		return kindVMDKDescriptor, "VMDK (descriptor)"
	case has(0, "QFI\xfb") && len(h) >= 8:
		if v := be32(h[4:]); v == 2 || v == 3 {
			return kindQCOW2, "QCOW2"
		}
	case len(h) >= 0x48 && le32(h[0x40:]) == vdiSignature:
		return kindVDI, "VDI"
	case len(h) >= 4 && le32(h) == sparseMagic:
		return kindSparse, "Android sparse"
	}
	if size >= 512 && vhdFixedAtEnd(r, size) {
		return kindVHDFixed, "VHD (fixed)"
	}
	return kindNone, ""
}

// Open opens the container file and returns its disk. The files of the
// extents of a VMDK descriptor are found next to it.
func Open(path string) (Disk, error) {
	f, err := source.OpenFile(path)
	if err != nil {
		return nil, err
	}
	o := &opener{dir: filepath.Dir(path)}
	d, err := open(f, f.Size(), o)
	if err != nil {
		o.close()
		f.Close()
		return nil, err
	}
	d.closers = append(o.files, f)
	return d, nil
}

// OpenReader is Open over a reader (for tests and nested documents). It
// cannot open a VMDK descriptor whose extents are in other files.
func OpenReader(r io.ReaderAt, size int64) (Disk, error) {
	return open(r, size, &opener{})
}

func open(r io.ReaderAt, size int64, o *opener) (*disk, error) {
	if size < 0 {
		return nil, errors.New("negative size")
	}
	kind, format := detect(r, size)
	switch kind {
	case kindVHDFixed:
		return &disk{m: flatMapper{r: r}, size: size - 512, format: format}, nil
	case kindVHD:
		return openVHD(r, size)
	case kindVHDX:
		return openVHDX(r, size)
	case kindVMDKSparse:
		return openVMDKSparse(r, size)
	case kindVMDKDescriptor:
		return openVMDKDescriptor(r, size, o)
	case kindQCOW2:
		return openQCOW2(r, size)
	case kindVDI:
		return openVDI(r, size)
	case kindSparse:
		return openSparse(r, size)
	}
	return nil, ErrNotImage
}

// opener opens the other files of an image (the extents of a VMDK) and
// keeps them to close
type opener struct {
	dir   string // "" - opened from a reader: there are no other files
	files []io.Closer
}

func (o *opener) open(name string) (io.ReaderAt, int64, error) {
	if o.dir == "" {
		return nil, 0, fmt.Errorf("the image needs the file %q: open it by its path", name)
	}
	if !filepath.IsAbs(name) {
		name = filepath.Join(o.dir, filepath.FromSlash(name))
	}
	f, err := source.OpenFile(name)
	if err != nil {
		return nil, 0, err
	}
	o.files = append(o.files, f)
	return f, f.Size(), nil
}

func (o *opener) close() {
	for _, f := range o.files {
		f.Close()
	}
	o.files = nil
}

// run is where the bytes of the disk from an offset are: a range of a file,
// decoded bytes, a repeated pattern or zeros
type run struct {
	n    int64       // the length of the run
	r    io.ReaderAt // the file; nil - not in a file
	src  int         // tells files apart: runs of the same file can be read at once
	off  int64       // the offset in r
	data []byte      // decoded bytes when r is nil; nil and no fill - zeros
	fill []byte      // a pattern repeated from its first byte
}

// mapper tells where the disk is stored; off is within the size of the disk
type mapper interface {
	mapAt(off int64) (run, error)
}

// flatMapper is a disk stored as is, from an offset of a file
type flatMapper struct {
	r    io.ReaderAt
	base int64
}

func (f flatMapper) mapAt(off int64) (run, error) {
	return run{n: 1 << 62, r: f.r, off: f.base + off}, nil
}

type zeroMapper struct{}

func (zeroMapper) mapAt(off int64) (run, error) { return run{n: 1 << 62}, nil }

// concatMapper is a disk made of parts one after the other (the extents of
// a VMDK descriptor)
type concatMapper struct {
	starts []int64 // the offset where each part begins
	ends   []int64
	parts  []mapper
}

func (c *concatMapper) mapAt(off int64) (run, error) {
	i := sort.Search(len(c.ends), func(i int) bool { return c.ends[i] > off })
	if i == len(c.ends) {
		return run{n: 1 << 62}, nil
	}
	r, err := c.parts[i].mapAt(off - c.starts[i])
	if err != nil {
		return r, err
	}
	r.n = min(r.n, c.ends[i]-off)
	r.src = i
	return r, nil
}

// disk is a Disk over a mapper
type disk struct {
	m       mapper
	size    int64
	format  string
	closers []io.Closer
	once    sync.Once
}

func (d *disk) Size() int64    { return d.size }
func (d *disk) Format() string { return d.format }

func (d *disk) Close() error {
	var err error
	d.once.Do(func() {
		for _, c := range d.closers {
			if e := c.Close(); e != nil && err == nil {
				err = e
			}
		}
	})
	return err
}

// ReadAt reads the disk; the runs that follow each other in a file are read
// with one call
func (d *disk) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 {
		return 0, errors.New("negative offset")
	}
	if off >= d.size {
		return 0, io.EOF
	}
	var eof error
	if int64(len(p)) > d.size-off {
		p = p[:d.size-off]
		eof = io.EOF
	}
	// The file read waiting to be done
	var (
		pr     io.ReaderAt
		pSrc   int
		pOff   int64
		pStart int
		pLen   int
	)
	flush := func() error {
		if pr == nil {
			return nil
		}
		err := readFull(pr, p[pStart:pStart+pLen], pOff)
		pr = nil
		if err != nil {
			return fmt.Errorf("%s: %w", d.format, err)
		}
		return nil
	}
	done := 0
	for done < len(p) {
		r, err := d.m.mapAt(off + int64(done))
		if err == nil && r.n <= 0 {
			err = errCorrupt(d.format, "an empty run at %d", off+int64(done))
		}
		if err != nil {
			if pr != nil {
				done = pStart
			}
			return done, err
		}
		n := int(min(r.n, int64(len(p)-done)))
		if r.r != nil {
			if pr != nil && pSrc == r.src && pOff+int64(pLen) == r.off {
				pLen += n
			} else {
				if err := flush(); err != nil {
					return pStart, err
				}
				pr, pSrc, pOff, pStart, pLen = r.r, r.src, r.off, done, n
			}
			done += n
			continue
		}
		if err := flush(); err != nil {
			return pStart, err
		}
		dst := p[done : done+n]
		switch {
		case r.data != nil:
			copy(dst, r.data[:n])
		case len(r.fill) > 0:
			k := copy(dst, r.fill)
			for k < n {
				k += copy(dst[k:], dst[:k])
			}
		default:
			clear(dst)
		}
		done += n
	}
	if err := flush(); err != nil {
		return pStart, err
	}
	return len(p), eof
}

// readFull reads all of b or says the container is cut short
func readFull(r io.ReaderAt, b []byte, off int64) error {
	n, err := r.ReadAt(b, off)
	if n == len(b) {
		return nil
	}
	if err == nil || err == io.EOF {
		return fmt.Errorf("the container is truncated at %d: %w", off+int64(n), io.ErrUnexpectedEOF)
	}
	return err
}

// readAtMost reads up to len(b) bytes: the end of the file may come first
func readAtMost(r io.ReaderAt, b []byte, off int64) (int, error) {
	n, err := r.ReadAt(b, off)
	if err == io.EOF {
		err = nil
	}
	return n, err
}
