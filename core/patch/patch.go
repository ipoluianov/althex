// Package patch exports the changes between two files as a patch and
// applies a patch to another file (EXT-3). The formats are IPS and BPS of
// the ROM hacking scene, AltHex's own format and a text hex diff.
//
// The files are read through io.ReaderAt and the result is written to an
// io.Writer as a stream. A patch is checked while it is applied, so on an
// error part of the result may be written already: write it to a temporary
// file and keep it only when Apply returns nil.
package patch

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"hash"
	"io"
)

// Format of a patch
type Format int

const (
	IPS     Format = iota // International Patching System: overwrites below 16 MB
	BPS                   // beat patch: copies from the source and the target, CRC-32
	AltHex                // AltHex's own format: copies and literals, SHA-256
	HexDiff               // text in the spirit of the unified diff
)

var (
	ErrFormat         = errors.New("patch: unknown format")
	ErrCorrupt        = errors.New("patch: corrupt patch")
	ErrChecksum       = errors.New("patch: checksum mismatch")
	ErrSourceMismatch = errors.New("patch: the patch is for another source")
	ErrTooLarge       = errors.New("patch: the change is beyond what the format can hold")
)

// Formats lists the formats
func Formats() []Format { return []Format{IPS, BPS, AltHex, HexDiff} }

func (f Format) String() string {
	switch f {
	case IPS:
		return "IPS"
	case BPS:
		return "BPS"
	case AltHex:
		return "AltHex"
	case HexDiff:
		return "HexDiff"
	}
	return fmt.Sprintf("Format(%d)", int(f))
}

// Ext is the file name extension of the format, with the dot
func (f Format) Ext() string {
	switch f {
	case IPS:
		return ".ips"
	case BPS:
		return ".bps"
	case AltHex:
		return ".ahp"
	case HexDiff:
		return ".hexdiff"
	}
	return ""
}

// Detect tells the format by the first bytes of a patch (16 are enough)
func Detect(head []byte) (Format, bool) {
	switch {
	case bytes.HasPrefix(head, []byte(ahpMagic)):
		return AltHex, true
	case bytes.HasPrefix(head, []byte(bpsMagic)):
		return BPS, true
	case bytes.HasPrefix(head, []byte(ipsMagic)):
		return IPS, true
	case bytes.HasPrefix(head, []byte("--- ")):
		return HexDiff, true
	}
	return 0, false
}

// Make writes a patch that turns src into dst. Progress, if not nil, gets
// the bytes of src compared.
func Make(ctx context.Context, f Format, w io.Writer, src io.ReaderAt, srcSize int64, dst io.ReaderAt, dstSize int64, progress func(done int64)) error {
	switch f {
	case IPS:
		return makeIPS(ctx, w, src, srcSize, dst, dstSize, progress)
	case BPS:
		return makeBPS(ctx, w, src, srcSize, dst, dstSize, progress)
	case AltHex:
		return makeAHP(ctx, w, src, srcSize, dst, dstSize, progress)
	case HexDiff:
		return MakeHexDiff(ctx, w, "source", src, srcSize, "target", dst, dstSize, progress)
	}
	return ErrFormat
}

// Apply reads a patch and writes the result of applying it to src
func Apply(ctx context.Context, f Format, patch io.Reader, w io.Writer, src io.ReaderAt, srcSize int64) error {
	switch f {
	case IPS:
		return applyIPS(ctx, patch, w, src, srcSize)
	case BPS:
		return applyBPS(ctx, patch, w, src, srcSize)
	case AltHex:
		return applyAHP(ctx, patch, w, src, srcSize)
	case HexDiff:
		return applyHexDiff(ctx, patch, w, src, srcSize)
	}
	return ErrFormat
}

const ioBuf = 1 << 20

// readAt fills buf from off; a short read is an error
func readAt(r io.ReaderAt, buf []byte, off int64) error {
	n, err := r.ReadAt(buf, off)
	if n == len(buf) {
		return nil
	}
	if err == nil || err == io.EOF {
		err = io.ErrUnexpectedEOF
	}
	return err
}

// copyFrom writes n bytes of r from off to w, checking ctx between pieces
func copyFrom(ctx context.Context, w io.Writer, r io.ReaderAt, off, n int64, buf []byte) error {
	for n > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		p := buf[:min(int64(len(buf)), n)]
		if err := readAt(r, p, off); err != nil {
			return err
		}
		if _, err := w.Write(p); err != nil {
			return err
		}
		off += int64(len(p))
		n -= int64(len(p))
	}
	return nil
}

// hashOf feeds n bytes of r to h; it runs in its own goroutine while the
// comparison goes, so the result comes through a channel
func hashOf(ctx context.Context, r io.ReaderAt, n int64, h hash.Hash) <-chan error {
	done := make(chan error, 1)
	go func() {
		buf := make([]byte, min(int64(ioBuf), max(n, 1)))
		done <- copyFrom(ctx, h, r, 0, n, buf)
	}()
	return done
}

// reader reads a patch, counting the bytes taken; errors at the end of the
// patch are ErrCorrupt, since a patch that ends early is broken
type reader struct {
	br  *bufio.Reader
	n   int64
	sum hash.Hash32 // of the bytes taken; nil - none
}

func newReader(r io.Reader) *reader {
	return &reader{br: bufio.NewReaderSize(r, 64<<10)}
}

func eofCorrupt(err error) error {
	if err == io.EOF || err == io.ErrUnexpectedEOF {
		return ErrCorrupt
	}
	return err
}

func (r *reader) ReadByte() (byte, error) {
	c, err := r.br.ReadByte()
	if err != nil {
		return 0, eofCorrupt(err)
	}
	r.n++
	if r.sum != nil {
		r.sum.Write([]byte{c})
	}
	return c, nil
}

func (r *reader) full(p []byte) error {
	n, err := io.ReadFull(r.br, p)
	r.n += int64(n)
	if r.sum != nil {
		r.sum.Write(p[:n])
	}
	return eofCorrupt(err)
}

// copyN writes the next n bytes of the patch to w
func (r *reader) copyN(ctx context.Context, w io.Writer, n int64, buf []byte) error {
	for n > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		p := buf[:min(int64(len(buf)), n)]
		if err := r.full(p); err != nil {
			return err
		}
		if _, err := w.Write(p); err != nil {
			return err
		}
		n -= int64(len(p))
	}
	return nil
}
