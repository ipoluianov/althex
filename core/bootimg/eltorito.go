package bootimg

import (
	"fmt"
	"strings"
)

// El Torito, the bootable CD: the boot record volume descriptor at sector
// 17 of an ISO 9660 image points to the boot catalog, whose entries point
// to the boot images

const isoSector = 2048

var (
	etPlatforms = map[byte]string{0: "x86", 1: "PowerPC", 2: "Mac", 0xEF: "UEFI"}
	etMedia     = []string{"no emulation", "1.2 MB floppy", "1.44 MB floppy", "2.88 MB floppy", "hard disk"}
)

func detectElTorito(d *reader, _ []byte) *Image {
	br := d.bytes(17*isoSector, 0x4B)
	if br == nil || br[0] != 0 || string(br[1:6]) != "CD001" ||
		!strings.HasPrefix(string(br[7:39]), "EL TORITO SPECIFICATION") {
		return nil
	}
	img := &Image{Format: "El Torito"}
	if pvd := d.bytes(16*isoSector, 72); pvd != nil && pvd[0] == 1 && string(pvd[1:6]) == "CD001" {
		img.info("volume", "%s", strings.TrimSpace(string(pvd[40:72])))
	}
	lba := int64(le32(br, 0x47))
	img.info("boot catalog", "sector %d", lba)
	cat := d.bytes(lba*isoSector, isoSector)
	if cat == nil {
		img.info("error", "the boot catalog is out of the image")
		return img
	}
	img.add(d, "boot catalog", "data", lba*isoSector, isoSector)

	// The validation entry: its 16 words sum to zero
	if cat[0] != 1 || cat[30] != 0x55 || cat[31] != 0xAA {
		img.info("validation entry", "not valid")
		return img
	}
	var sum uint16
	for i := 0; i < 32; i += 2 {
		sum += uint16(le16(cat, i))
	}
	img.info("validation entry checksum", "%s", okBad(sum == 0))
	platform := cat[1]
	img.info("platform", "%s", etPlatform(platform))
	if id := strings.TrimSpace(cstr(cat[4:28])); id != "" {
		img.info("ID", "%s", id)
	}
	etEntry(img, d, cat[32:64], "default entry", platform)

	// Sections: a header, its entries, extension entries (0x44) between
	off := 64
	for sec := 1; off+32 <= isoSector; sec++ {
		h := cat[off:]
		if h[0] != 0x90 && h[0] != 0x91 {
			break
		}
		last := h[0] == 0x91
		plat := h[1]
		n := int(le16(h, 2))
		if id := strings.TrimSpace(cstr(h[4:32])); id != "" {
			img.info(fmt.Sprintf("section %d", sec), "%s, %s", etPlatform(plat), id)
		} else {
			img.info(fmt.Sprintf("section %d", sec), "%s", etPlatform(plat))
		}
		off += 32
		for i := 1; i <= n && off+32 <= isoSector; i++ {
			etEntry(img, d, cat[off:off+32], fmt.Sprintf("section %d entry %d", sec, i), plat)
			off += 32
			for off+32 <= isoSector && cat[off] == 0x44 {
				off += 32
			}
		}
		if last {
			break
		}
	}
	return img
}

// etEntry adds the boot image of an initial or section entry
func etEntry(img *Image, d *reader, e []byte, label string, platform byte) {
	if e[0] != 0x88 && e[0] != 0 {
		return
	}
	media := int(e[1] & 0x0F)
	count := int64(le16(e, 6))
	rba := int64(le32(e, 8))
	boot := "bootable"
	if e[0] == 0 {
		boot = "not bootable"
	}
	img.info(label, "%s, %s, %s, load segment 0x%04X, %d sectors at sector %d",
		etPlatform(platform), name(etMedia, media), boot, le16(e, 2), count, rba)
	if rba == 0 {
		return
	}
	off := rba * isoSector
	size := count * 512
	switch media {
	case 1:
		size = 1228800
	case 2:
		size = 1474560
	case 3:
		size = 2949120
	}
	// UEFI images are FAT file systems whose sector count is often 0 or 1
	// (or cut at 65535): their BPB knows the size
	if s := d.bytes(off, 512); s != nil {
		if b, ok := readBPB(s); ok && !b.ntfs && b.size() > size {
			size = b.size()
		}
	}
	if !d.in(off, size) {
		size = max(0, d.size-off)
	}
	if !d.in(off, size) {
		return
	}
	img.add(d, label+" ("+etPlatform(platform)+", "+name(etMedia, media)+")", "boot catalog entry", off, size)
}

func etPlatform(p byte) string {
	if s, ok := etPlatforms[p]; ok {
		return s
	}
	return fmt.Sprintf("platform 0x%02X", p)
}
