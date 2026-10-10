// Package fs reads the file systems of partitions and disk images
// (BOOT-5): lists their directories, gives the contents of files and where
// their bytes are on the volume, so the hex view can go to them. It only
// reads.
package fs

import (
	"errors"
	"io"
	"time"
)

// Entry is a file or a directory
type Entry struct {
	Name    string
	Path    string // from the root, "/" separated: "/boot/grub/grub.cfg"
	IsDir   bool
	Size    int64
	Mode    uint32 // Unix permission bits and type (0 when the file system has none)
	ModTime time.Time
	// ID is the number of the file in the file system: an inode, an MFT
	// record, the first cluster
	ID uint64
}

// Extent is a run of the bytes of a file on the volume
type Extent struct {
	FileOff int64 // where the run is in the file
	DiskOff int64 // where it is on the volume; -1 - not stored (a hole: zeros)
	Len     int64
}

// FS is an open file system
type FS interface {
	// Type is the name of the file system: "FAT32", "NTFS", "ext4"...
	Type() string
	// Label is the name of the volume; "" if it has none
	Label() string
	// ReadDir lists a directory; "/" is the root
	ReadDir(path string) ([]Entry, error)
	// Stat returns the entry of a path
	Stat(path string) (Entry, error)
	// Extents returns where the bytes of a file are on the volume, in the
	// order of the file; nil for a file whose bytes are not stored as runs
	// (e.g. inside an NTFS record, compressed): Open reads those
	Extents(path string) ([]Extent, error)
	// Open returns the contents of a file
	Open(path string) (io.ReaderAt, int64, error)
}

var (
	ErrNotFound = errors.New("no such file or directory")
	ErrNotDir   = errors.New("not a directory")
	ErrIsDir    = errors.New("a directory")
	ErrUnknown  = errors.New("no known file system")
)

// prober opens a file system if the volume has it; ok is false when it is
// not this one (err then is nil)
type prober func(r io.ReaderAt, size int64) (fsys FS, ok bool, err error)

var probers []prober

// register adds a file system to Open; called from the init of its file
func register(p prober) { probers = append(probers, p) }

// registerFirst adds a file system that Open tries before the others: UDF,
// which a hybrid disc has besides ISO 9660
func registerFirst(p prober) { probers = append([]prober{p}, probers...) }

// Open finds the file system of a volume of the size. A volume may have
// several (UDF and ISO 9660 of a hybrid disc): the first one that opens
// without an error is given, else the first one found with its error.
func Open(r io.ReaderAt, size int64) (FS, error) {
	var damaged FS
	var damagedErr error
	for _, p := range probers {
		fsys, ok, err := p(r, size)
		if !ok {
			continue
		}
		if err == nil {
			return fsys, nil
		}
		if damagedErr == nil {
			damaged, damagedErr = fsys, err
		}
	}
	if damagedErr != nil {
		return damaged, damagedErr
	}
	return nil, ErrUnknown
}

// OpenAll gives every file system of a volume that opens without an error,
// in the order Open prefers them: of a hybrid disc UDF, then ISO 9660
func OpenAll(r io.ReaderAt, size int64) []FS {
	var out []FS
	for _, p := range probers {
		if fsys, ok, err := p(r, size); ok && err == nil {
			out = append(out, fsys)
		}
	}
	return out
}
