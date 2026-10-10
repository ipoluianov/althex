package bootimg

import (
	"bufio"
	"bytes"
	"compress/bzip2"
	"compress/gzip"
	"errors"
	"fmt"
	"io"

	"github.com/klauspost/compress/zstd"
)

// Decompression of the parts (BOOT-7): a compressed kernel or ramdisk is
// opened decompressed, and the cpio inside gets its file list. gzip,
// zstd, LZ4 (legacy and frame), bzip2, xz (LZMA2) and lzma are decoded;
// lzo is recognized only.
//
// The data is untrusted: the output is limited (a few bytes may claim
// gigabytes), and the windows the decoders keep are limited too.

// MaxDecompressed is the default limit of the decompressed size
const MaxDecompressed int64 = 4 << 30

// ErrTooLarge - the decompressed data goes past the limit
var ErrTooLarge = errors.New("the decompressed data is larger than the limit")

// ErrUnsupported - the compression is recognized but not decoded
var ErrUnsupported = errors.New("the compression is not supported")

// Decompressible tells whether data compressed so can be decompressed
func Decompressible(compression string) bool {
	switch compression {
	case "gzip", "zstd", "lz4", "bzip2", "xz", "lzma":
		return true
	}
	return false
}

// NewDecompressor returns a reader of the data of r decompressed; the
// compression is a Part.Compression. There is no limit of the size here:
// see OpenPart.
func NewDecompressor(compression string, r io.Reader) (io.ReadCloser, error) {
	br := bufio.NewReaderSize(r, 64<<10)
	var rd io.Reader
	var err error
	switch compression {
	case "gzip":
		rd, err = newGzipReader(br)
	case "zstd":
		var d *zstd.Decoder
		d, err = zstd.NewReader(br, zstd.WithDecoderConcurrency(1), zstd.WithDecoderLowmem(true),
			zstd.WithDecoderMaxWindow(1<<27), zstd.WithDecoderMaxMemory(uint64(MaxDecompressed)))
		if err == nil {
			return d.IOReadCloser(), nil
		}
	case "lz4":
		rd, err = newLZ4Reader(br)
	case "bzip2":
		rd = bzip2.NewReader(br)
	case "xz":
		rd, err = newXZReader(br)
	case "lzma":
		rd, err = newLZMAReader(br)
	case "":
		rd = br
	default:
		return nil, fmt.Errorf("%s: %w", compression, ErrUnsupported)
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w", compression, err)
	}
	return io.NopCloser(rd), nil
}

// gzipReader reads the members one after another, and stops at what is
// not a member (the padding after a ramdisk)
type gzipReader struct {
	br *bufio.Reader
	z  *gzip.Reader
}

func newGzipReader(br *bufio.Reader) (*gzipReader, error) {
	z, err := gzip.NewReader(br)
	if err != nil {
		return nil, err
	}
	z.Multistream(false)
	return &gzipReader{br: br, z: z}, nil
}

func (g *gzipReader) Read(p []byte) (int, error) {
	for {
		n, err := g.z.Read(p)
		if err != io.EOF {
			return n, err
		}
		if n > 0 {
			return n, nil
		}
		if b, err := g.br.Peek(2); err != nil || b[0] != 0x1F || b[1] != 0x8B {
			return 0, io.EOF
		}
		if err := g.z.Reset(g.br); err != nil {
			return 0, err
		}
		g.z.Multistream(false)
	}
}

// limitReader fails with ErrTooLarge past n bytes
type limitReader struct {
	r io.ReadCloser
	n int64
}

func (l *limitReader) Read(p []byte) (int, error) {
	if l.n <= 0 {
		// Exactly the limit is fine; one byte more is not
		var b [1]byte
		if n, _ := l.r.Read(b[:]); n > 0 {
			return 0, ErrTooLarge
		}
		return 0, io.EOF
	}
	if int64(len(p)) > l.n {
		p = p[:l.n]
	}
	n, err := l.r.Read(p)
	l.n -= int64(n)
	return n, err
}

func (l *limitReader) Close() error { return l.r.Close() }

// OpenPart returns a reader of the bytes of a part of the image r,
// decompressed when the part is compressed. Past limit bytes (0 -
// MaxDecompressed) it fails with ErrTooLarge.
func OpenPart(r io.ReaderAt, p Part, limit int64) (io.ReadCloser, error) {
	if limit <= 0 {
		limit = MaxDecompressed
	}
	if p.Off < 0 || p.Size < 0 {
		return nil, errors.New("bad part")
	}
	d, err := NewDecompressor(p.Compression, io.NewSectionReader(r, p.Off, p.Size))
	if err != nil {
		return nil, err
	}
	return &limitReader{r: d, n: limit}, nil
}

// DecompressPart reads a part decompressed into memory, at most limit
// bytes (0 - MaxDecompressed)
func DecompressPart(r io.ReaderAt, p Part, limit int64) ([]byte, error) {
	rd, err := OpenPart(r, p, limit)
	if err != nil {
		return nil, err
	}
	defer rd.Close()
	var b bytes.Buffer
	if _, err := b.ReadFrom(rd); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// OpenDecompressed returns the bytes of a part decompressed, as a reader
// with its size
func OpenDecompressed(r io.ReaderAt, p Part, limit int64) (io.ReaderAt, int64, error) {
	b, err := DecompressPart(r, p, limit)
	if err != nil {
		return nil, 0, err
	}
	return bytes.NewReader(b), int64(len(b)), nil
}

// DetectPart recognizes what is inside a part, decompressed first: the
// cpio of a compressed ramdisk gives its file list. It returns the image
// and the decompressed bytes its parts are in.
func DetectPart(r io.ReaderAt, p Part, limit int64) (*Image, []byte, error) {
	b, err := DecompressPart(r, p, limit)
	if err != nil {
		return nil, nil, err
	}
	img, err := Detect(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		return nil, b, err
	}
	return img, b, nil
}
