package bootimg

import (
	"encoding/binary"
	"fmt"
	"strings"
)

// Boot sectors: GRUB, Windows (NTFS, FAT), SYSLINUX

// bpb is the BIOS parameter block of a FAT or NTFS boot sector
type bpb struct {
	bytesPerSector, sectorsPerCluster, reserved, fats int64
	totalSectors                                      int64
	ntfs                                              bool
}

// readBPB reads the BPB of a boot sector; ok is false when it does not look
// like one
func readBPB(s []byte) (b bpb, ok bool) {
	if len(s) < 512 || s[510] != 0x55 || s[511] != 0xAA {
		return b, false
	}
	b.bytesPerSector = int64(le16(s, 0x0B))
	b.sectorsPerCluster = int64(s[0x0D])
	b.reserved = int64(le16(s, 0x0E))
	b.fats = int64(s[0x10])
	spc := b.sectorsPerCluster
	switch b.bytesPerSector {
	case 512, 1024, 2048, 4096:
	default:
		return b, false
	}
	b.ntfs = string(s[3:11]) == "NTFS    "
	if b.ntfs {
		// NTFS keeps a negative power of two for large clusters
		if spc > 0x80 || spc == 0 {
			spc = 1
		}
		b.totalSectors = int64(binary.LittleEndian.Uint64(s[0x28:]))
		return b, spc&(spc-1) == 0
	}
	if spc == 0 || spc&(spc-1) != 0 || b.reserved == 0 || b.fats < 1 || b.fats > 4 {
		return b, false
	}
	b.totalSectors = int64(le16(s, 0x13))
	if b.totalSectors == 0 {
		b.totalSectors = int64(le32(s, 0x20))
	}
	return b, b.totalSectors > 0
}

func (b bpb) size() int64 { return b.bytesPerSector * b.totalSectors }

// detectBootSector is a sector that ends with 55 AA: GRUB boot.img and
// core.img, Windows NTFS / FAT boot sectors, SYSLINUX
func detectBootSector(d *reader, head []byte) *Image {
	if len(head) < 512 {
		return nil
	}
	s := head[:512]
	text := string(s)
	// GRUB core.img starts with diskboot.img, which has no 55 AA
	if strings.Contains(text, "loading\x00") && strings.Contains(text, "Geom\x00Read\x00 Error") {
		return grubCore(d, s)
	}
	if s[510] != 0x55 || s[511] != 0xAA {
		return nil
	}
	if strings.Contains(text, "GRUB \x00Geom\x00Hard Disk\x00Read\x00 Error") {
		img := &Image{Format: "GRUB boot.img"}
		img.info("kernel sector", "%d", le64(s, 0x5C))
		drive := s[0x64]
		if drive == 0xFF {
			img.info("boot drive", "0xFF (the drive the BIOS booted from)")
		} else {
			img.info("boot drive", "0x%02X", drive)
		}
		mbrPartitions(img, d, s)
		return img
	}
	for _, sl := range []string{"SYSLINUX", "EXTLINUX", "ISOLINUX"} {
		if strings.Contains(text, sl) {
			img := &Image{Format: sl + " boot sector"}
			bootSectorInfo(img, s)
			return img
		}
	}
	b, ok := readBPB(s)
	if !ok {
		return nil
	}
	loader := ""
	for _, l := range [][2]string{{"BOOTMGR", "BOOTMGR"}, {"NTLDR", "NTLDR"}, {"IO      SYS", "IO.SYS"}, {"KERNEL  SYS", "KERNEL.SYS"}} {
		if strings.Contains(text, l[0]) {
			loader = l[1]
			break
		}
	}
	// NTFS keeps the loader name in the sectors after the first
	if b.ntfs && loader == "" {
		if more := d.bytes(512, min(8192, d.size-512)); more != nil {
			for _, l := range []string{"BOOTMGR", "NTLDR"} {
				if strings.Contains(string(more), l) || strings.Contains(string(more), utf16(l)) {
					loader = l
					break
				}
			}
		}
	}
	fs := "FAT"
	switch {
	case b.ntfs:
		fs = "NTFS"
	case string(s[0x52:0x5A]) == "FAT32   ":
		fs = "FAT32"
	case string(s[0x36:0x3B]) == "FAT16":
		fs = "FAT16"
	case string(s[0x36:0x3B]) == "FAT12":
		fs = "FAT12"
	}
	img := &Image{Format: fs + " boot sector"}
	if loader != "" {
		img.Format += " (" + loader + ")"
		img.info("loader", "%s", loader)
	}
	bootSectorInfo(img, s)
	return img
}

func utf16(s string) string {
	var b strings.Builder
	for _, c := range s {
		b.WriteRune(c)
		b.WriteByte(0)
	}
	return b.String()
}

// bootSectorInfo shows the BPB fields when there is one
func bootSectorInfo(img *Image, s []byte) {
	img.info("OEM name", "%s", strings.TrimSpace(strings.ToValidUTF8(cstr(s[3:11]), "?")))
	b, ok := readBPB(s)
	if !ok {
		return
	}
	img.info("bytes per sector", "%d", b.bytesPerSector)
	img.info("sectors per cluster", "%d", b.sectorsPerCluster)
	img.info("total sectors", "%d", b.totalSectors)
	if b.ntfs {
		img.info("volume serial", "%016X", le64(s, 0x48))
	} else {
		at := 0x27
		if string(s[0x52:0x5A]) == "FAT32   " {
			at = 0x43
		}
		img.info("volume serial", "%04X-%04X", le16(s, at+2), le16(s, at))
	}
}

// grubCore is core.img: diskboot.img, then lzma_decompress.img and the
// compressed kernel. Blocklists of 12 bytes go down from the end of the
// first sector: the sectors of the rest of core.img on the disk.
func grubCore(d *reader, s []byte) *Image {
	img := &Image{Format: "GRUB core.img"}
	for i := 0; i < 20; i++ {
		at := 0x1F4 - 12*i
		start, n, seg := le64(s, at), le16(s, at+8), le16(s, at+10)
		if n == 0 {
			break
		}
		img.info(fmt.Sprintf("blocklist %d", i), "%d sectors at LBA %d to segment 0x%04X", n, start, seg)
	}
	if d.size > 512 {
		img.add(d, "decompressor and kernel", "kernel", 512, d.size-512)
	}
	return img
}

// mbrPartitions lists the partition table of an MBR
func mbrPartitions(img *Image, d *reader, s []byte) {
	for i := 0; i < 4; i++ {
		e := s[0x1BE+16*i:]
		typ, lba, n := e[4], int64(le32(e, 8)), int64(le32(e, 12))
		if typ == 0 || n == 0 {
			continue
		}
		img.info(fmt.Sprintf("partition %d", i+1), "type 0x%02X, LBA %d, %d sectors", typ, lba, n)
		if d.in(lba*512, n*512) {
			img.add(d, fmt.Sprintf("partition %d", i+1), "data", lba*512, n*512)
		}
	}
}
