package fw

import (
	"bytes"
	"crypto/md5"
	"fmt"
	"io"
	"strings"
)

// A full flash image of an ESP32-family chip (what esptool writes with
// merge_bin, or reads with read_flash): the second-stage bootloader at
// flash 0x1000 (ESP32, ESP32-S2) or 0 (the others), the partition table at
// flash 0x8000, the app in its partition (factory or ota_0, usually at
// 0x10000). The file may start at flash 0 or at the bootloader.
const (
	espPTFlash    = 0x8000 // flash address of the partition table
	espPTLen      = 0xC00  // its largest size
	espPTEntryLen = 32
	espPTMagic    = 0x50AA
	espPTMD5Magic = 0xEBEB
)

// espPart is an entry of the partition table
type espPart struct {
	typ, sub  byte
	off, size uint32 // on the flash
	label     string
	flags     uint32
	fileOff   int64 // in the file; -1 when out of it
}

// espFlash is the layout of a full flash image
type espFlash struct {
	base  int64 // the flash address of the start of the file
	boot  int64 // the file offset of the bootloader image
	bootL *espLayout
	pt    int64 // the file offset of the partition table
	parts []espPart
	md5   string // "OK", "bad", "" (none)
}

// espFindFlash finds the bootloader and the partition table of a full
// flash image; nil when the file is not one
func espFindFlash(r io.ReaderAt, size int64) *espFlash {
	type try struct{ boot, pt, base int64 }
	for _, t := range []try{
		{0, 0x8000, 0},      // from flash 0, the bootloader at 0 (C3, S3, C6...)
		{0, 0x7000, 0x1000}, // from the bootloader at flash 0x1000 (ESP32, S2)
		{0x1000, 0x8000, 0}, // from flash 0, the bootloader at 0x1000
	} {
		if size < t.pt+espPTEntryLen {
			continue
		}
		if b := readAt(r, t.boot, 1); b == nil || b[0] != espMagic {
			continue
		}
		sr := io.NewSectionReader(r, t.boot, size-t.boot)
		l, err := espParse(sr, size-t.boot, true)
		if err != nil || t.boot+l.imageEnd > t.pt {
			continue
		}
		f := &espFlash{base: t.base, boot: t.boot, bootL: l, pt: t.pt}
		if !f.readPT(r, size) {
			continue
		}
		return f
	}
	return nil
}

// readPT reads the partition table; false when there is none
func (f *espFlash) readPT(r io.ReaderAt, size int64) bool {
	n := int64(espPTLen)
	if size-f.pt < n {
		n = (size - f.pt) / espPTEntryLen * espPTEntryLen
	}
	tab := readAt(r, f.pt, int(n))
	if tab == nil {
		return false
	}
	for o := 0; o+espPTEntryLen <= len(tab); o += espPTEntryLen {
		e := tab[o : o+espPTEntryLen]
		switch le16(e) {
		case espPTMagic:
			p := espPart{typ: e[2], sub: e[3], off: le32(e[4:]), size: le32(e[8:]), flags: le32(e[28:]), fileOff: -1}
			p.label = strings.TrimRight(string(e[12:28]), "\x00")
			if fo := int64(p.off) - f.base; fo >= 0 && fo < size {
				p.fileOff = fo
			}
			f.parts = append(f.parts, p)
			continue
		case espPTMD5Magic:
			sum := md5.Sum(tab[:o])
			if bytes.Equal(sum[:], e[16:32]) {
				f.md5 = "OK"
			} else {
				f.md5 = "bad"
			}
		}
		break
	}
	return len(f.parts) > 0
}

// espPartTypes names the types and the subtypes of the partitions
func espPartType(p espPart) string {
	switch p.typ {
	case 0:
		switch {
		case p.sub == 0:
			return "app/factory"
		case p.sub >= 0x10 && p.sub < 0x20:
			return fmt.Sprintf("app/ota_%d", p.sub-0x10)
		case p.sub == 0x20:
			return "app/test"
		}
		return fmt.Sprintf("app/0x%02X", p.sub)
	case 1:
		subs := map[byte]string{0: "ota", 1: "phy", 2: "nvs", 3: "coredump", 4: "nvs_keys", 5: "efuse",
			6: "undefined", 0x80: "esphttpd", 0x81: "fat", 0x82: "spiffs", 0x83: "littlefs"}
		if s, ok := subs[p.sub]; ok {
			return "data/" + s
		}
		return fmt.Sprintf("data/0x%02X", p.sub)
	case 2:
		return fmt.Sprintf("bootloader/0x%02X", p.sub)
	case 3:
		return fmt.Sprintf("partition_table/0x%02X", p.sub)
	}
	return fmt.Sprintf("0x%02X/0x%02X", p.typ, p.sub)
}

// app is the partition whose app runs: factory, else ota_0, else the first
// app partition there is in the file
func (f *espFlash) app(r io.ReaderAt) *espPart {
	rank := func(p *espPart) int {
		switch {
		case p.typ != 0:
			return -1
		case p.sub == 0:
			return 3
		case p.sub == 0x10:
			return 2
		}
		return 1
	}
	var best *espPart
	for i := range f.parts {
		p := &f.parts[i]
		if rank(p) < 0 || p.fileOff < 0 {
			continue
		}
		if b := readAt(r, p.fileOff, 1); b == nil || b[0] != espMagic {
			continue
		}
		if best == nil || rank(p) > rank(best) {
			best = p
		}
	}
	return best
}

// espSegs adds the segments of an image at a file offset, named with a
// prefix; the ones that overlap the segments already there are left out
func espSegs(im *Image, r io.ReaderAt, l *espLayout, off int64, prefix, key string) {
	for i, s := range l.segs {
		name, exec := espSegName(s[0])
		if name == "" {
			name = fmt.Sprintf("segment %d", i)
		}
		name = prefix + name
		addr, ln := uint64(s[0]), uint64(s[2])
		desc := fmt.Sprintf("%s at 0x%08X, %d bytes at offset 0x%X", name, s[0], s[2], off+int64(s[1]))
		over := ""
		for _, o := range im.Segments {
			if addr < o.End() && o.Addr < addr+ln {
				over = o.Name
				break
			}
		}
		if over != "" {
			// The bootloader of the ESP32 loads into IRAM that the app
			// takes over later
			im.info(fmt.Sprintf("%sSegment %d", key, i), "%s (overlaps %s: not mapped)", desc, over)
			continue
		}
		im.Segments = append(im.Segments, fileSeg(name, addr, r, off+int64(s[1]), int64(s[2]), exec))
		im.info(fmt.Sprintf("%sSegment %d", key, i), "%s", desc)
	}
}

// openESPFlash reads a full flash image: the segments of the app and of
// the bootloader, the partitions in the info
func openESPFlash(r io.ReaderAt, size int64, f *espFlash) *Image {
	chip := f.bootL.chip
	im := &Image{Format: chip.Name + " flash image", kind: kindESP32, Arch: chip.Arch, Family: chip.Name}
	bh := readAt(r, f.boot, espHeaderLen)
	bootEntry := le32(bh[4:])
	im.Entry, im.HasEntry = uint64(bootEntry), true
	im.info("Flash start", "the file starts at flash 0x%X", f.base)

	// The partitions
	md5 := ""
	switch f.md5 {
	case "OK":
		md5 = ", MD5 OK"
	case "bad":
		md5 = ", MD5 does not match"
		im.issue("partition table at 0x%X: the MD5 does not match the entries", f.pt)
	}
	im.info("Partition table", "at flash 0x%X (file 0x%X), %d entries%s", f.pt+f.base, f.pt, len(f.parts), md5)
	for i, p := range f.parts {
		where := fmt.Sprintf("file 0x%X", p.fileOff)
		if p.fileOff < 0 {
			where = "not in the file"
		} else if p.fileOff+int64(p.size) > size {
			where += ", cut by the end of the file"
		}
		enc := ""
		if p.flags&1 != 0 {
			enc = ", encrypted"
		}
		im.info(fmt.Sprintf("Partition %d", i), "%s: %s at 0x%X, 0x%X bytes (%s%s)", p.label, espPartType(p), p.off, p.size, where, enc)
	}

	// The app
	if p := f.app(r); p != nil {
		sr := io.NewSectionReader(r, p.fileOff, size-p.fileOff)
		l, err := espParse(sr, size-p.fileOff, true)
		if err == nil && l.chip.ID != chip.ID {
			err = fmt.Errorf("an app for %s", l.chip.Name)
		}
		if err != nil {
			im.issue("app in partition %s at 0x%X: %v", p.label, p.off, err)
		} else {
			ah := readAt(sr, 0, espHeaderLen)
			im.Entry = uint64(le32(ah[4:]))
			im.info("App", "partition %s (%s) at flash 0x%X (file 0x%X), %d bytes", p.label, espPartType(*p), p.off, p.fileOff, l.imageEnd)
			espHeaderInfo(im, sr, ah, true)
			espSegs(im, r, l, p.fileOff, "app ", "App ")
			sub := espSub{off: p.fileOff, esp32: true, key: "App ", issue: "app: "}
			im.espSubs = append(im.espSubs, sub)
			espCheck(im, sr, l, sub)
		}
	} else {
		im.issue("no app partition with an image in the file")
	}

	// The bootloader
	im.info("Bootloader", "at flash 0x%X (file 0x%X), %d segments, entry 0x%08X", f.boot+f.base, f.boot, len(f.bootL.segs), bootEntry)
	espSegs(im, r, f.bootL, f.boot, "boot ", "Bootloader ")
	sub := espSub{off: f.boot, esp32: true, key: "Bootloader ", issue: "bootloader: "}
	im.espSubs = append(im.espSubs, sub)
	espCheck(im, io.NewSectionReader(r, f.boot, size-f.boot), f.bootL, sub)
	return im
}
