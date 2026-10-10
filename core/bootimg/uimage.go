package bootimg

import (
	"fmt"
	"hash/crc32"
	"io"
	"strings"
	"time"
)

// U-Boot legacy image (include/image.h): a header of 64 bytes, big-endian,
// with a CRC-32 of itself and of the data

const uimageMagic = 0x27051956

var (
	uimageOS = []string{"invalid", "OpenBSD", "NetBSD", "FreeBSD", "4.4BSD", "Linux",
		"SVR4", "Esix", "Solaris", "Irix", "SCO", "Dell", "NCR", "LynxOS", "VxWorks",
		"pSOS", "QNX", "U-Boot", "RTEMS", "ARTOS", "Unity", "INTEGRITY", "OSE", "Plan 9",
		"OpenRTOS", "ARM Trusted Firmware", "Trusted Execution Environment", "OpenSBI",
		"EFI firmware", "ELF image"}
	uimageArch = []string{"invalid", "Alpha", "ARM", "x86", "IA-64", "MIPS", "MIPS64",
		"PowerPC", "S390", "SuperH", "SPARC", "SPARC64", "M68K", "Nios-32", "MicroBlaze",
		"Nios-II", "Blackfin", "AVR32", "ST200", "Sandbox", "NDS32", "OpenRISC", "ARM64",
		"ARC", "x86_64", "Xtensa", "RISC-V"}
	uimageType = []string{"invalid", "standalone program", "kernel", "ramdisk",
		"multi-file", "firmware", "script", "filesystem", "flat device tree",
		"Kirkwood boot", "Freescale i.MX boot", "kernel (no load)", "PBL", "MXS boot",
		"GP header", "Atmel ROM", "Freescale i.MX boot (v2)", "Rockchip boot",
		"Rockchip SPI boot", "Rockchip SD boot", "Zynq boot", "ZynqMP boot", "FPGA",
		"Vybrid boot", "TI Keystone boot", "Davinci AIS", "TI firmware", "U-Boot loadable",
		"UBL", "Zynq bif", "Zynq bootgen", "Xilinx PMU firmware", "STM32", "Socfpga",
		"TEE", "firmware (IVT)", "PMU microcode", "FIT loader script", "Copro", "Sunxi eGON",
		"Sunxi TOC0"}
	uimageComp = []string{"none", "gzip", "bzip2", "lzma", "lzo", "lz4", "zstd"}
)

func detectUImage(d *reader, head []byte) *Image {
	if len(head) < 64 || be32(head, 0) != uimageMagic {
		return nil
	}
	img := &Image{Format: "U-Boot legacy image"}
	h := append([]byte(nil), head[:64]...)
	hcrc := be32(h, 4)
	h[4], h[5], h[6], h[7] = 0, 0, 0, 0
	img.info("image name", "%s", strings.TrimSpace(cstr(h[32:64])))
	img.info("header CRC", "%s", okBad(crc32.ChecksumIEEE(h) == hcrc))
	size := int64(be32(h, 12))
	ok := d.in(64, size)
	if ok {
		crc := crc32.NewIEEE()
		io.Copy(crc, io.NewSectionReader(d.r, 64, size))
		img.info("data CRC", "%s", okBad(crc.Sum32() == be32(h, 24)))
	} else {
		img.info("data CRC", "the data is cut")
	}
	typ, comp := int(h[30]), int(h[31])
	img.info("created", "%s", time.Unix(int64(be32(h, 8)), 0).UTC().Format("2006-01-02 15:04:05 UTC"))
	img.info("OS", "%s", name(uimageOS, int(h[28])))
	img.info("architecture", "%s", name(uimageArch, int(h[29])))
	img.info("type", "%s", name(uimageType, typ))
	img.info("compression", "%s", name(uimageComp, comp))
	img.info("data size", "%d", size)
	img.info("load address", "0x%08X", be32(h, 16))
	img.info("entry point", "0x%08X", be32(h, 20))

	kind := uimageKind(typ)
	if typ != 4 {
		if ok {
			n := kind
			if typ == 6 {
				n = "script"
			}
			addCompressed(img, d, n, kind, 64, size, comp)
		}
		return img
	}
	// Multi-file: a table of sizes ended by a zero, then the images, each
	// padded to 4 bytes
	var sizes []int64
	off := int64(64)
	for len(sizes) < 1024 {
		b := d.bytes(off, 4)
		if b == nil {
			return img
		}
		off += 4
		s := int64(be32(b, 0))
		if s == 0 {
			break
		}
		sizes = append(sizes, s)
	}
	img.info("files", "%d", len(sizes))
	for i, s := range sizes {
		if !d.in(off, s) {
			break
		}
		// By the convention of U-Boot the first is the kernel, the second
		// the ramdisk, the third the device tree
		n, k := fmt.Sprintf("file %d", i), "data"
		switch i {
		case 0:
			n, k = "kernel", "kernel"
		case 1:
			n, k = "ramdisk", "ramdisk"
		case 2:
			n, k = "dtb", "dtb"
		}
		addCompressed(img, d, n, k, off, s, comp)
		off += alignUp(s, 4)
	}
	return img
}

// addCompressed adds a part; when its magic tells nothing, the
// compression the header names is taken
func addCompressed(img *Image, d *reader, n, kind string, off, size int64, comp int) {
	if !img.add(d, n, kind, off, size) {
		return
	}
	p := &img.Parts[len(img.Parts)-1]
	if p.Compression == "" && comp > 0 && comp < len(uimageComp) && kind != "ramdisk" && kind != "dtb" {
		p.Compression = uimageComp[comp]
	}
}

func uimageKind(typ int) string {
	switch typ {
	case 2, 11:
		return "kernel"
	case 3:
		return "ramdisk"
	case 8:
		return "dtb"
	case 6:
		return "config"
	}
	return "data"
}

func okBad(ok bool) string {
	if ok {
		return "OK"
	}
	return "bad"
}
