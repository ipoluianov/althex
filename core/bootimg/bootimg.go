// Package bootimg recognizes boot images and finds their parts (BOOT-7):
// the kernel, the ramdisk, the device tree and the rest, so they can be
// shown and extracted. Detect reads only the headers and tables: the
// compression of a part is told by its first bytes; decompress.go opens a
// compressed part decompressed (gzip, zstd, LZ4, bzip2, xz, lzma).
//
// Formats: Android boot.img (v0-v4) and vendor_boot (v3, v4), U-Boot
// legacy uImage and FIT, a plain device tree, cpio (newc, odc, old binary;
// concatenated initramfs), the Linux x86 zImage/bzImage, ARM zImage and
// ARM64 Image headers, El Torito in an ISO 9660 image, and the boot
// sectors of GRUB (boot.img, core.img), Windows (NTFS, FAT) and SYSLINUX.
//
// The data is untrusted: every offset and size is checked against the
// image, and the number of parts is limited.
package bootimg

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// ErrUnknown - the data is not a boot image this package knows
var ErrUnknown = errors.New("not a known boot image")

// Part is a component of a boot image that can be extracted
type Part struct {
	// "kernel", "ramdisk", "second", "dtb", "vendor_ramdisk: platform",
	// "fdt-1", the path of a cpio entry...
	Name string
	// "kernel", "ramdisk", "dtb", "config", "signature", "file", "dir",
	// "symlink", "boot catalog entry", "data"
	Kind      string
	Off, Size int64 // where its bytes are in the image
	// "gzip", "lz4", "lzma", "xz", "bzip2", "zstd", "lzo", "" - by the
	// magic of its first bytes
	Compression string
}

// KV is a header field worth showing
type KV struct{ Key, Value string }

// Image is a recognized boot image
type Image struct {
	Format string // "Android boot (v4)", "U-Boot legacy image", "cpio (newc)"...
	Info   []KV
	Parts  []Part
}

// Limits against garbage that claims too much
const (
	maxParts = 200_000
	maxRead  = 1 << 20 // the largest header or table read at once
)

// Detect recognizes a boot image at the start of r; size is the size of
// the data. ErrUnknown - none is recognized.
func Detect(r io.ReaderAt, size int64) (*Image, error) {
	if r == nil || size <= 0 {
		return nil, ErrUnknown
	}
	d := &reader{r: r, size: size}
	head := d.bytes(0, min(size, 4096))
	if head == nil {
		return nil, ErrUnknown
	}
	// The magics at the start first; the boot sectors, which only end with
	// 55 AA, last
	for _, f := range []func(*reader, []byte) *Image{
		detectAndroid,
		detectVendorBoot,
		detectUImage,
		detectFDT,
		detectCpio,
		detectBzImage,
		detectARMKernel,
		detectElTorito,
		detectBootSector,
	} {
		if img := f(d, head); img != nil {
			return img, nil
		}
	}
	return nil, ErrUnknown
}

// reader reads the image with bounds checks
type reader struct {
	r    io.ReaderAt
	size int64
}

// bytes reads n bytes at off; nil when they are not all in the image
func (d *reader) bytes(off, n int64) []byte {
	if off < 0 || n < 0 || n > maxRead || off > d.size || n > d.size-off {
		return nil
	}
	b := make([]byte, n)
	m, err := d.r.ReadAt(b, off)
	if m != len(b) && err != nil {
		return nil
	}
	if m != len(b) {
		return nil
	}
	return b
}

// in tells whether [off, off+n) is in the image
func (d *reader) in(off, n int64) bool {
	return off >= 0 && n >= 0 && off <= d.size && n <= d.size-off
}

// compression tells the compression of the data at off by its magic
func (d *reader) compression(off, size int64) string {
	n := min(size, 16, d.size-off)
	if n < 2 || off < 0 {
		return ""
	}
	return compressionOf(d.bytes(off, n))
}

func compressionOf(b []byte) string {
	switch {
	case bytes.HasPrefix(b, []byte{0x1F, 0x8B}), bytes.HasPrefix(b, []byte{0x1F, 0x9E}):
		return "gzip"
	case bytes.HasPrefix(b, []byte{0x02, 0x21, 0x4C, 0x18}), bytes.HasPrefix(b, []byte{0x04, 0x22, 0x4D, 0x18}):
		return "lz4" // legacy (the kernel's) and frame
	case bytes.HasPrefix(b, []byte{0xFD, '7', 'z', 'X', 'Z', 0}):
		return "xz"
	case bytes.HasPrefix(b, []byte("BZh")) && len(b) > 3 && b[3] >= '1' && b[3] <= '9':
		return "bzip2"
	case bytes.HasPrefix(b, []byte{0x28, 0xB5, 0x2F, 0xFD}):
		return "zstd"
	case bytes.HasPrefix(b, []byte{0x89, 'L', 'Z', 'O', 0, 0x0D, 0x0A, 0x1A, 0x0A}):
		return "lzo"
	case bytes.HasPrefix(b, []byte{0x5D, 0, 0}):
		return "lzma" // lc=3 lp=0 pb=2, what every lzma tool writes
	}
	return ""
}

// add appends a part, its compression detected; false when the parts are
// too many
func (img *Image) add(d *reader, name, kind string, off, size int64) bool {
	if len(img.Parts) >= maxParts {
		return false
	}
	img.Parts = append(img.Parts, Part{Name: name, Kind: kind, Off: off, Size: size, Compression: d.compression(off, size)})
	return true
}

func (img *Image) info(key, format string, args ...any) {
	img.Info = append(img.Info, KV{key, fmt.Sprintf(format, args...)})
}

// cstr is the text of b up to its first zero
func cstr(b []byte) string {
	if i := bytes.IndexByte(b, 0); i >= 0 {
		b = b[:i]
	}
	return string(b)
}

func le16(b []byte, off int) uint32 { return uint32(binary.LittleEndian.Uint16(b[off:])) }
func le32(b []byte, off int) uint32 { return binary.LittleEndian.Uint32(b[off:]) }
func le64(b []byte, off int) uint64 { return binary.LittleEndian.Uint64(b[off:]) }
func be32(b []byte, off int) uint32 { return binary.BigEndian.Uint32(b[off:]) }

// alignUp rounds n up to a multiple of a (a > 0)
func alignUp(n, a int64) int64 { return (n + a - 1) / a * a }

// name returns names[i] or "N" with the number
func name(names []string, i int) string {
	if i >= 0 && i < len(names) && names[i] != "" {
		return names[i]
	}
	return fmt.Sprint(i)
}
