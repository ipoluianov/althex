package source

import "io"

// Section is a part of another source: a partition of a disk, a file of a
// file system, a component of a boot image
type Section struct {
	r    io.ReaderAt
	off  int64
	size int64
}

// NewSection makes a source of size bytes of r from off
func NewSection(r io.ReaderAt, off, size int64) *Section {
	return &Section{r: r, off: off, size: size}
}

func (s *Section) Size() int64 { return s.size }

func (s *Section) ReadAt(p []byte, off int64) (int, error) {
	if off >= s.size {
		return 0, io.EOF
	}
	if rest := s.size - off; int64(len(p)) > rest {
		n, err := s.r.ReadAt(p[:rest], s.off+off)
		if err == nil {
			err = io.EOF
		}
		return n, err
	}
	return s.r.ReadAt(p, s.off+off)
}

// Close does nothing: the source under it belongs to someone else
func (s *Section) Close() error { return nil }
