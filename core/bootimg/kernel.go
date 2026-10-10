package bootimg

// Linux kernel images: x86 zImage/bzImage (Documentation/arch/x86/boot.rst,
// the setup header at 0x1F1), ARM zImage and ARM64 Image

func detectBzImage(d *reader, head []byte) *Image {
	if len(head) < 0x270 || string(head[0x202:0x206]) != "HdrS" {
		return nil
	}
	proto := le16(head, 0x206)
	loadflags := head[0x211]
	img := &Image{Format: "Linux zImage"}
	if loadflags&1 != 0 {
		img.Format = "Linux bzImage" // LOADED_HIGH: the kernel goes to 1 MB
	}
	img.info("boot protocol", "%d.%02d", proto>>8, proto&0xFF)
	setupSects := int64(head[0x1F1])
	if setupSects == 0 {
		setupSects = 4 // what the old kernels meant
	}
	img.info("setup sectors", "%d", setupSects)
	if v := le16(head, 0x20E); v != 0 {
		// kernel_version points to the text, 0x200 before the header
		if b := d.bytes(int64(v)+0x200, min(256, d.size-int64(v)-0x200)); b != nil {
			img.info("kernel version", "%s", cstr(b))
		}
	}
	img.info("boot flag", "0x%04X", le16(head, 0x1FE))
	img.info("loadflags", "0x%02X", loadflags)
	img.info("code32_start", "0x%08X", le32(head, 0x214))
	img.info("syssize", "%d bytes", int64(le32(head, 0x1F4))*16)
	if proto >= 0x0203 {
		img.info("initrd_addr_max", "0x%08X", le32(head, 0x22C))
	}
	if proto >= 0x0205 {
		img.info("kernel_alignment", "0x%X", le32(head, 0x230))
		img.info("relocatable_kernel", "%d", head[0x234])
	}
	if proto >= 0x0206 {
		img.info("cmdline_size", "%d", le32(head, 0x238))
	}
	if proto >= 0x020C {
		img.info("xloadflags", "0x%04X", le16(head, 0x236))
	}
	if proto >= 0x020A {
		img.info("pref_address", "0x%X", le64(head, 0x258))
		img.info("init_size", "%d", le32(head, 0x260))
	}
	if proto >= 0x020B {
		img.info("handover_offset", "0x%X", le32(head, 0x264))
	}

	setupSize := (setupSects + 1) * 512
	if !d.in(0, setupSize) {
		return img
	}
	img.add(d, "setup", "data", 0, setupSize)
	pm := d.size - setupSize
	img.add(d, "kernel", "kernel", setupSize, pm)
	if proto >= 0x0208 {
		off, size := int64(le32(head, 0x248)), int64(le32(head, 0x24C))
		img.info("payload_offset", "0x%X", off)
		img.info("payload_length", "%d", size)
		if size > 0 && d.in(setupSize+off, size) {
			img.add(d, "kernel (compressed payload)", "kernel", setupSize+off, size)
		}
	}
	return img
}

// detectARMKernel is the 32-bit ARM zImage (its magic at 0x24) and the
// ARM64, RISC-V Image headers
func detectARMKernel(d *reader, head []byte) *Image {
	if len(head) >= 0x30 && le32(head, 0x24) == 0x016F2818 {
		img := &Image{Format: "Linux ARM zImage"}
		start, end := int64(le32(head, 0x28)), int64(le32(head, 0x2C))
		img.info("start", "0x%08X", start)
		img.info("end", "0x%08X", end)
		size := end - start
		if size <= 0 || size > d.size {
			size = d.size
		}
		img.add(d, "kernel", "kernel", 0, size)
		// A device tree appended (CONFIG_ARM_APPENDED_DTB)
		if b := d.bytes(size, 8); b != nil && be32(b, 0) == 0xD00DFEED {
			n := int64(be32(b, 4))
			if n < 40 || !d.in(size, n) {
				n = d.size - size
			}
			img.info("appended dtb", "at 0x%X, %d bytes", size, n)
			img.add(d, "dtb", "dtb", size, n)
		}
		return img
	}
	if len(head) >= 0x40 {
		magic := string(head[0x38:0x3C])
		if magic == "ARM\x64" || magic == "RSC\x05" {
			img := &Image{Format: "Linux ARM64 Image"}
			if magic != "ARM\x64" {
				img.Format = "Linux RISC-V Image"
			}
			img.info("text_offset", "0x%X", le64(head, 8))
			img.info("image_size", "%d", le64(head, 16))
			img.info("flags", "0x%X", le64(head, 24))
			img.add(d, "kernel", "kernel", 0, d.size)
			return img
		}
	}
	return nil
}
