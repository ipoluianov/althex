package fw

import (
	"fmt"
	"hash/crc32"
	"io"
	"strings"
)

// DfuSe (ST's extension of the DFU file, UM0391): a prefix, targets (one per
// alternate setting of the DFU interface) with elements at addresses, and
// the DFU suffix with a CRC-32 of the file.
const (
	dfusePrefixLen = 11
	dfuseTargetLen = 274
	dfuSuffixLen   = 16
	dfuseMaxCount  = 1 << 16
)

func openDfuSe(r io.ReaderAt, size int64) (*Image, error) {
	pre := readAt(r, 0, dfusePrefixLen)
	if pre == nil || size < dfusePrefixLen+dfuSuffixLen {
		return nil, fmt.Errorf("%w: DfuSe: the file is too short", ErrFormat)
	}
	im := &Image{Format: "DfuSe", kind: kindDfuSe}
	imageSize := int64(le32(pre[6:]))
	targets := int(pre[10])
	im.info("Version", "%d", pre[5])
	im.info("Targets", "%d", targets)
	if pre[5] != 1 {
		im.issue("DfuSe version %d (1 is known)", pre[5])
	}
	if imageSize != size-dfuSuffixLen {
		im.issue("the prefix says %d bytes, the file without the suffix has %d", imageSize, size-dfuSuffixLen)
	}
	end := size - dfuSuffixLen
	off := int64(dfusePrefixLen)
	for t := 0; t < targets; t++ {
		th := readAt(r, off, dfuseTargetLen)
		if th == nil || off+dfuseTargetLen > end || string(th[:6]) != "Target" {
			im.issue("target %d at %d: no \"Target\" signature", t, off)
			break
		}
		alt := th[6]
		named := le32(th[7:]) != 0
		name := ""
		if named {
			name = cString(th[11:266])
		}
		tsize := int64(le32(th[266:]))
		nel := le32(th[270:])
		label := fmt.Sprintf("target %d", alt)
		if name != "" {
			label += " (" + name + ")"
		}
		im.info(fmt.Sprintf("Target %d", t), "alternate setting %d, %q, %d bytes, %d elements", alt, name, tsize, nel)
		off += dfuseTargetLen
		tend := off + tsize
		if tend > end {
			im.issue("%s: %d bytes go beyond the end of the file", label, tsize)
			tend = end
		}
		if nel > dfuseMaxCount {
			im.issue("%s: %d elements", label, nel)
			break
		}
		for e := uint32(0); e < nel; e++ {
			eh := readAt(r, off, 8)
			if eh == nil || off+8 > tend {
				im.issue("%s: element %d is cut", label, e)
				break
			}
			addr, esize := le32(eh), int64(le32(eh[4:]))
			off += 8
			if off+esize > tend {
				im.issue("%s: element %d of %d bytes goes beyond the target", label, e, esize)
				esize = tend - off
			}
			sname := label
			if nel > 1 {
				sname = fmt.Sprintf("%s, element %d", label, e)
			}
			im.Segments = append(im.Segments, fileSeg(sname, uint64(addr), r, off, esize, alt == 0))
			off += esize
		}
		off = tend
	}
	// The suffix
	suf := readAt(r, size-dfuSuffixLen, dfuSuffixLen)
	if string(suf[8:11]) != "UFD" || suf[11] != dfuSuffixLen {
		im.issue("no DFU suffix (\"UFD\", length 16)")
		return im, nil
	}
	im.info("Device (bcdDevice)", "0x%04X", le16(suf))
	im.info("USB ID", "%04X:%04X", le16(suf[4:]), le16(suf[2:]))
	im.info("DFU version", "0x%04X", le16(suf[6:]))
	if le16(suf[6:]) != 0x011A {
		im.issue("DFU version 0x%04X in the suffix (DfuSe is 0x011A)", le16(suf[6:]))
	}
	want, err := dfuCRC(r, size)
	if err != nil {
		return nil, err
	}
	if got := le32(suf[12:]); got != want {
		im.issue("DFU suffix CRC 0x%08X, should be 0x%08X", got, want)
	} else {
		im.info("CRC", "0x%08X (OK)", got)
	}
	return im, nil
}

// dfuCRC computes the CRC of a DFU file: the CRC-32 of all but its last 4
// bytes without the final inversion
func dfuCRC(r io.ReaderAt, size int64) (uint32, error) {
	h := crc32.NewIEEE()
	if _, err := io.Copy(h, io.NewSectionReader(r, 0, size-4)); err != nil {
		return 0, err
	}
	return ^h.Sum32(), nil
}

func cString(b []byte) string {
	if i := strings.IndexByte(string(b), 0); i >= 0 {
		b = b[:i]
	}
	return string(b)
}
