package main

import (
	"strconv"
	"strings"
)

// The hand-written part of the database: which source files are used, the
// device naming, the part-number patterns, and the memory maps (SVD files do
// not describe Flash/RAM). Sizes are the largest member of the group unless a
// per-part entry says otherwise; per-part sizes follow the datasheets.

const (
	modm   = "https://raw.githubusercontent.com/modm-io/cmsis-svd-stm32/main/"
	cmsis  = "https://raw.githubusercontent.com/cmsis-svd/cmsis-svd-data/main/data/"
	ch32   = "https://raw.githubusercontent.com/ch32-rs/ch32-rs/main/svd/fixed/"
	lpc55  = "https://raw.githubusercontent.com/lpc55/lpc55-pac/main/svd/"
	mchp   = "https://packs.download.microchip.com/"
	megaP  = mchp + "Microchip.ATmega_DFP.3.6.299.atpack#atdf/"
	tinyP  = mchp + "Microchip.ATtiny_DFP.3.4.278.atpack#atdf/"
	dxP    = mchp + "Microchip.AVR-Dx_DFP.2.8.343.atpack#atdf/"
	kb     = 1024
	mb     = 1024 * 1024
	stFl   = 0x08000000
	stSRAM = 0x20000000
)

type spec struct {
	kind     string // "svd" or "atdf"
	src      string
	dev      device // the hand-written fields
	partsDef string // "code:flashK/ramK ..." expanded per pattern as pattern+"?"+code
}

func R(name string, start, size uint64, kind string) region {
	return region{Name: name, Start: start, Size: size, Kind: kind}
}

func pats(s string) []string { return strings.Fields(s) }

// st describes an STM32 group: Flash at 0x08000000, SRAM at 0x20000000.
func st(file, name, family, patterns string, flashK, ramK uint64, parts string, extra ...region) spec {
	mem := []region{R("Flash", stFl, flashK*kb, "flash"), R("SRAM", stSRAM, ramK*kb, "ram")}
	mem = append(mem, extra...)
	return spec{kind: "svd", src: modm + file, partsDef: parts, dev: device{
		Name: name, Vendor: "STMicroelectronics", Family: family, Arch: "arm",
		Patterns: pats(patterns), Memory: mem, LoadAddr: stFl,
	}}
}

var (
	f1sys  = []region{R("System memory", 0x1FFFF000, 2*kb, "rom"), R("Option bytes", 0x1FFFF800, 16, "fuses")}
	f0ob   = []region{R("Option bytes", 0x1FFFF800, 16, "fuses")}
	f3sys  = []region{R("System memory", 0x1FFFD800, 8*kb, "rom"), R("Option bytes", 0x1FFFF800, 16, "fuses")}
	f4sys  = []region{R("System memory", 0x1FFF0000, 30*kb, "rom"), R("OTP", 0x1FFF7800, 528, "rom"), R("Option bytes", 0x1FFFC000, 16, "fuses")}
	f4ccm  = []region{R("CCM RAM", 0x10000000, 64*kb, "ram"), R("Backup SRAM", 0x40024000, 4*kb, "ram")}
	f7sys  = []region{R("ITCM RAM", 0x00000000, 16*kb, "ram"), R("Flash (ITCM)", 0x00200000, 2*mb, "flash"), R("System memory", 0x1FF00000, 60*kb, "rom"), R("Option bytes", 0x1FFF0000, 32, "fuses"), R("Backup SRAM", 0x40024000, 4*kb, "ram")}
	g0sys  = []region{R("System memory", 0x1FFF0000, 28*kb, "rom"), R("Option bytes", 0x1FFF7800, 128, "fuses")}
	g4sys  = []region{R("System memory", 0x1FFF0000, 28*kb, "rom"), R("OTP", 0x1FFF7000, 1*kb, "rom"), R("Option bytes", 0x1FFF7800, 48, "fuses")}
	l0sys  = []region{R("Data EEPROM", 0x08080000, 6*kb, "eeprom"), R("System memory", 0x1FF00000, 8*kb, "rom"), R("Option bytes", 0x1FF80000, 32, "fuses")}
	l1sys  = []region{R("Data EEPROM", 0x08080000, 16*kb, "eeprom"), R("System memory", 0x1FF00000, 8*kb, "rom"), R("Option bytes", 0x1FF80000, 128, "fuses")}
	l4sys  = []region{R("System memory", 0x1FFF0000, 28*kb, "rom"), R("OTP", 0x1FFF7000, 1*kb, "rom"), R("Option bytes", 0x1FFF7800, 40, "fuses")}
	wbsys  = []region{R("System memory", 0x1FFF0000, 28*kb, "rom"), R("OTP", 0x1FFF7000, 1*kb, "rom"), R("Option bytes", 0x1FFF8000, 128, "fuses")}
	h7axi  = []region{R("ITCM RAM", 0x00000000, 64*kb, "ram"), R("AXI SRAM", 0x24000000, 512*kb, "ram"), R("SRAM1-3 (D2)", 0x30000000, 288*kb, "ram"), R("SRAM4 (D3)", 0x38000000, 64*kb, "ram"), R("Backup SRAM", 0x38800000, 4*kb, "ram"), R("System memory", 0x1FF00000, 128*kb, "rom")}
	h72axi = []region{R("ITCM RAM", 0x00000000, 64*kb, "ram"), R("AXI SRAM", 0x24000000, 320*kb, "ram"), R("SRAM1-2 (D2)", 0x30000000, 32*kb, "ram"), R("SRAM4 (D3)", 0x38000000, 16*kb, "ram"), R("Backup SRAM", 0x38800000, 4*kb, "ram"), R("System memory", 0x1FF00000, 128*kb, "rom")}
	h7aaxi = []region{R("ITCM RAM", 0x00000000, 64*kb, "ram"), R("AXI SRAM", 0x24000000, 1024*kb, "ram"), R("AHB SRAM", 0x30000000, 128*kb, "ram"), R("SRD SRAM", 0x38000000, 32*kb, "ram"), R("Backup SRAM", 0x38800000, 4*kb, "ram"), R("System memory", 0x1FF00000, 128*kb, "rom")}
)

func cat(rs ...[]region) []region {
	var out []region
	for _, r := range rs {
		out = append(out, r...)
	}
	return out
}

func generic(kind, src, name, vendor, family, arch, core, patterns string, load uint64, mem []region, parts string) spec {
	return spec{kind: kind, src: src, partsDef: parts, dev: device{
		Name: name, Vendor: vendor, Family: family, Arch: arch, Core: core,
		Patterns: pats(patterns), Memory: mem, LoadAddr: load,
	}}
}

func avr(src, name string, extra string) spec {
	p := strings.ToUpper(name)
	if extra != "" {
		p += " " + extra
	}
	return spec{kind: "atdf", src: src + name + ".atdf", dev: device{
		Name: name, Vendor: "Microchip", Family: "AVR", Arch: "avr", Patterns: pats(p),
	}}
}

func esp(file, name, core, arch string, mem ...region) spec {
	return generic("svd", cmsis+"Espressif/"+file, name, "Espressif", name, arch, core,
		strings.ReplaceAll(strings.ToUpper(name), "-", "")+" "+strings.ToUpper(name), 0, mem, "")
}

var specs = []spec{
	// ---- STM32 (modm-io/cmsis-svd-stm32, ST SVDs, Apache-2.0) ----
	st("stm32f0/STM32F0x0.svd", "STM32F0x0", "STM32F0", "STM32F030 STM32F070", 256, 32, "4:16/4 6:32/4 8:64/8 B:128/16 C:256/32", f0ob...),
	st("stm32f0/STM32F0x1.svd", "STM32F0x1", "STM32F0", "STM32F031 STM32F051 STM32F071 STM32F091", 256, 32, "4:16/4 6:32/4 8:64/8 B:128/16 C:256/32", f0ob...),
	st("stm32f0/STM32F0x2.svd", "STM32F0x2", "STM32F0", "STM32F042 STM32F072", 128, 16, "4:16/6 6:32/6 8:64/16 B:128/16", f0ob...),
	st("stm32f1/STM32F100.svd", "STM32F100xx", "STM32F1", "STM32F100", 512, 32, "4:16/4 6:32/4 8:64/8 B:128/8 C:256/24 D:384/32 E:512/32", f1sys...),
	st("stm32f1/STM32F101.svd", "STM32F101xx", "STM32F1", "STM32F101", 1024, 80, "4:16/4 6:32/6 8:64/10 B:128/16 C:256/32 D:384/48 E:512/48 F:768/80 G:1024/80", f1sys...),
	st("stm32f1/STM32F103.svd", "STM32F103xx", "STM32F1", "STM32F103", 1024, 96, "4:16/6 6:32/10 8:64/20 B:128/20 C:256/48 D:384/64 E:512/64 F:768/96 G:1024/96", f1sys...),
	st("stm32f1/STM32F107.svd", "STM32F105/107", "STM32F1", "STM32F105 STM32F107", 256, 64, "8:64/64 B:128/64 C:256/64",
		R("System memory", 0x1FFFB000, 18*kb, "rom"), R("Option bytes", 0x1FFFF800, 16, "fuses")),
	st("stm32f2/STM32F215.svd", "STM32F205/215", "STM32F2", "STM32F205 STM32F215", 1024, 128, "B:128/64 C:256/96 E:512/128 F:768/128 G:1024/128", cat(f4sys, []region{R("Backup SRAM", 0x40024000, 4*kb, "ram")})...),
	st("stm32f2/STM32F217.svd", "STM32F207/217", "STM32F2", "STM32F207 STM32F217", 1024, 128, "C:256/128 E:512/128 F:768/128 G:1024/128", cat(f4sys, []region{R("Backup SRAM", 0x40024000, 4*kb, "ram")})...),
	st("stm32f3/STM32F301.svd", "STM32F301", "STM32F3", "STM32F301 STM32F318", 64, 16, "6:32/16 8:64/16", f3sys...),
	st("stm32f3/STM32F302.svd", "STM32F302", "STM32F3", "STM32F302", 512, 64, "6:32/16 8:64/16 B:128/32 C:256/40 D:384/64 E:512/64", f3sys...),
	st("stm32f3/STM32F303.svd", "STM32F303", "STM32F3", "STM32F303 STM32F358 STM32F398", 512, 64, "6:32/12 8:64/12 B:128/32 C:256/40 D:384/64 E:512/64",
		cat([]region{R("CCM SRAM", 0x10000000, 16*kb, "ram")}, f3sys)...),
	st("stm32f3/STM32F3x4.svd", "STM32F3x4", "STM32F3", "STM32F334 STM32F303?4 STM32F303?6 STM32F303?8", 64, 12, "4:16/12 6:32/12 8:64/12",
		cat([]region{R("CCM SRAM", 0x10000000, 4*kb, "ram")}, f3sys)...),
	st("stm32f3/STM32F373.svd", "STM32F373", "STM32F3", "STM32F373 STM32F378", 256, 32, "8:64/16 B:128/24 C:256/32", f3sys...),
	st("stm32f4/STM32F401.svd", "STM32F401", "STM32F4", "STM32F401", 512, 96, "B:128/64 C:256/64 D:384/96 E:512/96", f4sys...),
	st("stm32f4/STM32F405.svd", "STM32F405", "STM32F4", "STM32F405 STM32F415", 1024, 128, "E:512/128 G:1024/128", cat(f4ccm, f4sys)...),
	st("stm32f4/STM32F407.svd", "STM32F407", "STM32F4", "STM32F407 STM32F417", 1024, 128, "E:512/128 G:1024/128", cat(f4ccm, f4sys)...),
	st("stm32f4/STM32F410.svd", "STM32F410", "STM32F4", "STM32F410", 128, 32, "8:64/32 B:128/32", f4sys...),
	st("stm32f4/STM32F411.svd", "STM32F411", "STM32F4", "STM32F411", 512, 128, "C:256/128 E:512/128", f4sys...),
	st("stm32f4/STM32F412.svd", "STM32F412", "STM32F4", "STM32F412", 1024, 256, "E:512/256 G:1024/256", f4sys...),
	st("stm32f4/STM32F413.svd", "STM32F413", "STM32F4", "STM32F413 STM32F423", 1536, 320, "G:1024/320 H:1536/320", f4sys...),
	st("stm32f4/STM32F427.svd", "STM32F427", "STM32F4", "STM32F427 STM32F437", 2048, 192, "G:1024/192 I:2048/192", cat(f4ccm, f4sys)...),
	st("stm32f4/STM32F429.svd", "STM32F429", "STM32F4", "STM32F429 STM32F439", 2048, 192, "E:512/192 G:1024/192 I:2048/192", cat(f4ccm, f4sys)...),
	st("stm32f4/STM32F446.svd", "STM32F446", "STM32F4", "STM32F446", 512, 128, "C:256/128 E:512/128", f4sys...),
	st("stm32f4/STM32F469.svd", "STM32F469", "STM32F4", "STM32F469 STM32F479", 2048, 320, "E:512/320 G:1024/320 I:2048/320", cat(f4ccm, f4sys)...),
	st("stm32f7/STM32F722.svd", "STM32F72x/73x", "STM32F7", "STM32F722 STM32F723 STM32F732 STM32F733 STM32F730", 512, 256, "8:64/256 C:256/256 E:512/256", f7sys...),
	st("stm32f7/STM32F746.svd", "STM32F745/746", "STM32F7", "STM32F745 STM32F746 STM32F756 STM32F750", 1024, 320, "8:64/320 E:512/320 G:1024/320", f7sys...),
	st("stm32f7/STM32F767.svd", "STM32F765/767", "STM32F7", "STM32F765 STM32F767 STM32F777", 2048, 512, "G:1024/512 I:2048/512", f7sys...),
	st("stm32f7/STM32F769.svd", "STM32F769", "STM32F7", "STM32F768 STM32F769 STM32F778 STM32F779", 2048, 512, "G:1024/512 I:2048/512", f7sys...),
	st("stm32g0/STM32G030.svd", "STM32G030", "STM32G0", "STM32G030", 64, 8, "6:32/8 8:64/8", g0sys...),
	st("stm32g0/STM32G031.svd", "STM32G031", "STM32G0", "STM32G031 STM32G041", 64, 8, "4:16/8 6:32/8 8:64/8", g0sys...),
	st("stm32g0/STM32G070.svd", "STM32G070", "STM32G0", "STM32G070", 128, 36, "B:128/36", g0sys...),
	st("stm32g0/STM32G071.svd", "STM32G071", "STM32G0", "STM32G071 STM32G081", 128, 36, "6:32/36 8:64/36 B:128/36", g0sys...),
	st("stm32g0/STM32G0B1.svd", "STM32G0B1", "STM32G0", "STM32G0B1 STM32G0C1 STM32G0B0", 512, 144, "B:128/144 C:256/144 E:512/144", g0sys...),
	st("stm32g4/STM32G431.svd", "STM32G431", "STM32G4", "STM32G431 STM32G441", 128, 22, "6:32/22 8:64/22 B:128/22",
		cat([]region{R("CCM SRAM", 0x10000000, 10*kb, "ram")}, g4sys)...),
	st("stm32g4/STM32G474.svd", "STM32G474", "STM32G4", "STM32G474 STM32G484 STM32G473 STM32G483 STM32G471", 512, 96, "B:128/96 C:256/96 E:512/96",
		cat([]region{R("CCM SRAM", 0x10000000, 32*kb, "ram")}, g4sys)...),
	st("stm32g4/STM32G491.svd", "STM32G491", "STM32G4", "STM32G491 STM32G4A1", 512, 96, "C:256/96 E:512/96",
		cat([]region{R("CCM SRAM", 0x10000000, 16*kb, "ram")}, g4sys)...),
	st("stm32h7/STM32H723.svd", "STM32H723/733", "STM32H7", "STM32H723 STM32H733 STM32H725 STM32H735 STM32H730", 1024, 128, "B:128/0 E:512/0 G:1024/0", h72axi...),
	st("stm32h7/STM32H743.svd", "STM32H742/743", "STM32H7", "STM32H743 STM32H742 STM32H753", 2048, 128, "G:1024/0 I:2048/0", h7axi...),
	st("stm32h7/STM32H750.svd", "STM32H750", "STM32H7", "STM32H750", 128, 128, "B:128/0", h7axi...),
	st("stm32h7/STM32H745_CM7.svd", "STM32H745/755 (CM7)", "STM32H7", "STM32H745 STM32H755", 2048, 128, "G:1024/0 I:2048/0", h7axi...),
	st("stm32h7/STM32H747_CM7.svd", "STM32H747/757 (CM7)", "STM32H7", "STM32H747 STM32H757", 2048, 128, "G:1024/0 I:2048/0", h7axi...),
	st("stm32h7/STM32H7A3.svd", "STM32H7A3/7B3", "STM32H7", "STM32H7A3 STM32H7B3 STM32H7B0", 2048, 128, "B:128/0 G:1024/0 I:2048/0", h7aaxi...),
	st("stm32l0/STM32L0x0.svd", "STM32L0x0", "STM32L0", "STM32L010", 128, 20, "4:16/2 6:32/8 8:64/8 B:128/20", l0sys...),
	st("stm32l0/STM32L0x1.svd", "STM32L0x1", "STM32L0", "STM32L011 STM32L021 STM32L031 STM32L041 STM32L051 STM32L071 STM32L081", 192, 20, "3:8/2 4:16/2 6:32/8 8:64/8 B:128/20 Z:192/20", l0sys...),
	st("stm32l0/STM32L0x2.svd", "STM32L0x2", "STM32L0", "STM32L052 STM32L062 STM32L072 STM32L082", 192, 20, "6:32/8 8:64/8 B:128/20 Z:192/20", l0sys...),
	st("stm32l0/STM32L0x3.svd", "STM32L0x3", "STM32L0", "STM32L053 STM32L063 STM32L073 STM32L083", 192, 20, "6:32/8 8:64/8 B:128/20 Z:192/20", l0sys...),
	st("stm32l1/STM32L100.svd", "STM32L100", "STM32L1", "STM32L100", 256, 32, "6:32/4 8:64/8 B:128/10 C:256/16", l1sys...),
	st("stm32l1/STM32L151.svd", "STM32L151", "STM32L1", "STM32L151", 512, 80, "6:32/10 8:64/10 B:128/16 C:256/32 D:384/48 E:512/80", l1sys...),
	st("stm32l1/STM32L152.svd", "STM32L152", "STM32L1", "STM32L152 STM32L162", 512, 80, "6:32/10 8:64/10 B:128/16 C:256/32 D:384/48 E:512/80", l1sys...),
	st("stm32l4/STM32L412.svd", "STM32L412/422", "STM32L4", "STM32L412 STM32L422", 128, 32, "8:64/32 B:128/32",
		cat([]region{R("SRAM2", 0x10000000, 8*kb, "ram")}, l4sys)...),
	st("stm32l4/STM32L432.svd", "STM32L432/442", "STM32L4", "STM32L432 STM32L442", 256, 48, "B:128/48 C:256/48",
		cat([]region{R("SRAM2", 0x10000000, 16*kb, "ram")}, l4sys)...),
	st("stm32l4/STM32L433.svd", "STM32L433/443", "STM32L4", "STM32L433 STM32L443", 256, 48, "B:128/48 C:256/48",
		cat([]region{R("SRAM2", 0x10000000, 16*kb, "ram")}, l4sys)...),
	st("stm32l4/STM32L452.svd", "STM32L452/462", "STM32L4", "STM32L451 STM32L452 STM32L462", 512, 128, "C:256/128 E:512/128",
		cat([]region{R("SRAM2", 0x10000000, 32*kb, "ram")}, l4sys)...),
	st("stm32l4/STM32L476.svd", "STM32L475/476/486", "STM32L4", "STM32L476 STM32L475 STM32L486 STM32L471", 1024, 96, "C:256/96 E:512/96 G:1024/96",
		cat([]region{R("SRAM2", 0x10000000, 32*kb, "ram")}, l4sys)...),
	st("stm32l4/STM32L496.svd", "STM32L496/4A6", "STM32L4", "STM32L496 STM32L4A6", 1024, 256, "E:512/256 G:1024/256",
		cat([]region{R("SRAM2", 0x10000000, 64*kb, "ram")}, l4sys)...),
	st("stm32l4+/STM32L4R5.svd", "STM32L4R5/R7/S5", "STM32L4", "STM32L4R5 STM32L4R7 STM32L4S5 STM32L4S7", 2048, 640, "G:1024/640 I:2048/640",
		cat([]region{R("SRAM2", 0x10000000, 64*kb, "ram")}, l4sys)...),
	st("stm32l5/STM32L552.svd", "STM32L552", "STM32L5", "STM32L552 STM32L562", 512, 256, "C:256/256 E:512/256",
		R("System memory", 0x0BF90000, 32*kb, "rom")),
	st("stm32u5/STM32U575.svd", "STM32U575/585", "STM32U5", "STM32U575 STM32U585", 2048, 768, "G:1024/768 I:2048/768",
		R("SRAM4", 0x28000000, 16*kb, "ram"), R("System memory", 0x0BF90000, 32*kb, "rom")),
	st("stm32c0/STM32C031.svd", "STM32C031", "STM32C0", "STM32C031 STM32C011", 32, 12, "4:16/12 6:32/12",
		R("System memory", 0x1FFF0000, 6*kb, "rom"), R("Option bytes", 0x1FFF7800, 128, "fuses")),
	st("stm32c0/STM32C071.svd", "STM32C071", "STM32C0", "STM32C071", 128, 24, "8:64/24 B:128/24",
		R("System memory", 0x1FFF0000, 6*kb, "rom"), R("Option bytes", 0x1FFF7800, 128, "fuses")),
	st("stm32u0/STM32U083.svd", "STM32U073/083", "STM32U0", "STM32U083 STM32U073", 256, 40, "8:64/40 B:128/40 C:256/40",
		R("System memory", 0x1FFF0000, 28*kb, "rom")),
	st("stm32h5/STM32H503.svd", "STM32H503", "STM32H5", "STM32H503", 128, 32, "B:128/32", R("System memory", 0x0BF80000, 32*kb, "rom")),
	st("stm32h5/STM32H563.svd", "STM32H562/563/573", "STM32H5", "STM32H562 STM32H563 STM32H573", 2048, 640, "G:1024/640 I:2048/640",
		R("System memory", 0x0BF80000, 32*kb, "rom"), R("Backup SRAM", 0x40036400, 4*kb, "ram")),
	st("stm32wba5/STM32WBA55.svd", "STM32WBA5x", "STM32WBA", "STM32WBA55 STM32WBA54 STM32WBA52", 1024, 128, "C:256/64 E:512/96 G:1024/128",
		R("System memory", 0x0BF88000, 32*kb, "rom")),
	st("stm32wb/STM32WB55_CM4.svd", "STM32WB55 (CM4)", "STM32WB", "STM32WB55 STM32WB5M STM32WB50", 1024, 256, "C:256/128 E:512/256 G:1024/256", wbsys...),
	st("stm32wb/STM32WB35_CM4.svd", "STM32WB35 (CM4)", "STM32WB", "STM32WB35 STM32WB30", 512, 96, "C:256/96 E:512/96", wbsys...),
	st("stm32wb/STM32WB15_CM4.svd", "STM32WB15 (CM4)", "STM32WB", "STM32WB15 STM32WB10", 320, 48, "C:320/48", wbsys...),
	st("stm32wl/STM32WLE5_CM4.svd", "STM32WLE5", "STM32WL", "STM32WLE5 STM32WLE4", 256, 64, "8:64/20 B:128/48 C:256/64", wbsys...),
	st("stm32wl/STM32WL5x_CM4.svd", "STM32WL5x (CM4)", "STM32WL", "STM32WL55 STM32WL54", 256, 64, "B:128/48 C:256/64", wbsys...),

	// ---- Nordic (cmsis-svd-data/Nordic, Nordic 5-clause BSD-style licence) ----
	generic("svd", cmsis+"Nordic/nrf51.svd", "nRF51", "Nordic Semiconductor", "nRF51", "arm", "Cortex-M0", "NRF51822 NRF51422 NRF51824 NRF51", 0,
		[]region{R("Flash", 0, 256*kb, "flash"), R("RAM", 0x20000000, 32*kb, "ram"), R("FICR", 0x10000000, 1*kb, "rom"), R("UICR", 0x10001000, 1*kb, "fuses")}, ""),
	generic("svd", cmsis+"Nordic/nrf52.svd", "nRF52832", "Nordic Semiconductor", "nRF52", "arm", "Cortex-M4F", "NRF52832 NRF52", 0,
		[]region{R("Flash", 0, 512*kb, "flash"), R("RAM", 0x20000000, 64*kb, "ram"), R("FICR", 0x10000000, 4*kb, "rom"), R("UICR", 0x10001000, 4*kb, "fuses")}, ""),
	generic("svd", cmsis+"Nordic/nrf52833.svd", "nRF52833", "Nordic Semiconductor", "nRF52", "arm", "Cortex-M4F", "NRF52833", 0,
		[]region{R("Flash", 0, 512*kb, "flash"), R("RAM", 0x20000000, 128*kb, "ram"), R("FICR", 0x10000000, 4*kb, "rom"), R("UICR", 0x10001000, 4*kb, "fuses")}, ""),
	generic("svd", cmsis+"Nordic/nrf52840.svd", "nRF52840", "Nordic Semiconductor", "nRF52", "arm", "Cortex-M4F", "NRF52840", 0,
		[]region{R("Flash", 0, 1024*kb, "flash"), R("RAM", 0x20000000, 256*kb, "ram"), R("FICR", 0x10000000, 4*kb, "rom"), R("UICR", 0x10001000, 4*kb, "fuses")}, ""),
	generic("svd", cmsis+"Nordic/nrf5340_application.svd", "nRF5340 (application)", "Nordic Semiconductor", "nRF53", "arm", "Cortex-M33", "NRF5340", 0,
		[]region{R("Flash", 0, 1024*kb, "flash"), R("RAM", 0x20000000, 512*kb, "ram"), R("FICR", 0x00FF0000, 4*kb, "rom"), R("UICR", 0x00FF8000, 4*kb, "fuses")}, ""),
	generic("svd", cmsis+"Nordic/nrf5340_network.svd", "nRF5340 (network)", "Nordic Semiconductor", "nRF53", "arm", "Cortex-M33", "NRF5340NET", 0x01000000,
		[]region{R("Flash", 0x01000000, 256*kb, "flash"), R("RAM", 0x21000000, 64*kb, "ram"), R("FICR", 0x01FF0000, 4*kb, "rom"), R("UICR", 0x01FF8000, 4*kb, "fuses")}, ""),
	generic("svd", cmsis+"Nordic/nrf9160.svd", "nRF9160", "Nordic Semiconductor", "nRF91", "arm", "Cortex-M33", "NRF9160", 0,
		[]region{R("Flash", 0, 1024*kb, "flash"), R("RAM", 0x20000000, 256*kb, "ram"), R("FICR", 0x00FF0000, 4*kb, "rom"), R("UICR", 0x00FF8000, 4*kb, "fuses")}, ""),

	// ---- Raspberry Pi (cmsis-svd-data/RaspberryPi, BSD-3-Clause) ----
	generic("svd", cmsis+"RaspberryPi/rp2040.svd", "RP2040", "Raspberry Pi", "RP2040", "arm", "Cortex-M0+", "RP2040", 0x10000000,
		[]region{R("Boot ROM", 0, 16*kb, "rom"), R("XIP Flash", 0x10000000, 16*mb, "flash"), R("SRAM", 0x20000000, 264*kb, "ram"), R("USB DPRAM", 0x50100000, 4*kb, "ram")}, ""),
	generic("svd", cmsis+"RaspberryPi/rp2350.svd", "RP2350", "Raspberry Pi", "RP2350", "arm", "Cortex-M33", "RP2350 RP2354", 0x10000000,
		[]region{R("Boot ROM", 0, 32*kb, "rom"), R("XIP Flash", 0x10000000, 16*mb, "flash"), R("SRAM", 0x20000000, 520*kb, "ram"), R("USB DPRAM", 0x50100000, 4*kb, "ram")}, ""),

	// ---- Microchip SAM (cmsis-svd-data/Atmel, Apache-2.0) ----
	generic("svd", cmsis+"Atmel/ATSAMD21J18A.svd", "ATSAMD21", "Microchip", "SAMD21", "arm", "Cortex-M0+", "ATSAMD21 SAMD21", 0,
		[]region{R("Flash", 0, 256*kb, "flash"), R("SRAM", 0x20000000, 32*kb, "ram"), R("NVM user row", 0x00804000, 256, "fuses"), R("NVM software calibration", 0x00806020, 8, "rom")},
		"15:32/4 16:64/8 17:128/16 18:256/32"),
	generic("svd", cmsis+"Atmel/ATSAMD51P20A.svd", "ATSAMD51", "Microchip", "SAMD51", "arm", "Cortex-M4F", "ATSAMD51 SAMD51", 0,
		[]region{R("Flash", 0, 1024*kb, "flash"), R("SRAM", 0x20000000, 256*kb, "ram"), R("Backup RAM", 0x47000000, 8*kb, "ram"), R("NVM user page", 0x00804000, 512, "fuses")},
		"18:256/128 19:512/192 20:1024/256"),
	generic("svd", cmsis+"Atmel/ATSAM3X8E.svd", "ATSAM3X8E", "Microchip", "SAM3X", "arm", "Cortex-M3", "ATSAM3X8 SAM3X8 ATSAM3X SAM3X ATSAM3A", 0x00080000,
		[]region{R("Flash", 0x00080000, 512*kb, "flash"), R("Boot ROM", 0x00100000, 16*kb, "rom"), R("SRAM0", 0x20000000, 64*kb, "ram"), R("SRAM1", 0x20080000, 32*kb, "ram"), R("NFC SRAM", 0x20100000, 4*kb, "ram")}, ""),

	// ---- NXP ----
	generic("svd", cmsis+"NXP/LPC176x5x_v0.2.svd", "LPC176x/5x", "NXP", "LPC17xx", "arm", "Cortex-M3", "LPC176 LPC175", 0,
		[]region{R("Flash", 0, 512*kb, "flash"), R("Local SRAM", 0x10000000, 32*kb, "ram"), R("Boot ROM", 0x1FFF0000, 8*kb, "rom"), R("AHB SRAM", 0x2007C000, 32*kb, "ram")}, ""),
	generic("svd", lpc55+"pack-13.0.0-LPC55S69_cm33_core0.xml", "LPC55S69", "NXP", "LPC55xx", "arm", "Cortex-M33", "LPC55S69 LPC55S66 LPC5569 LPC5566", 0,
		[]region{R("Flash", 0, 640*kb, "flash"), R("Boot ROM", 0x03000000, 128*kb, "rom"), R("SRAM-X", 0x04000000, 32*kb, "ram"), R("SRAM", 0x20000000, 320*kb, "ram"), R("USB SRAM", 0x40100000, 16*kb, "ram")}, ""),
	generic("svd", cmsis+"NXP/MIMXRT1062.svd", "MIMXRT1062", "NXP", "i.MX RT1060", "arm", "Cortex-M7", "MIMXRT1062 MIMXRT1061 IMXRT1062", 0x60000000,
		[]region{R("ITCM", 0, 512*kb, "ram"), R("Boot ROM", 0x00200000, 128*kb, "rom"), R("DTCM", 0x20000000, 512*kb, "ram"), R("OCRAM2", 0x20200000, 512*kb, "ram"), R("OCRAM (FlexRAM)", 0x20280000, 512*kb, "ram"), R("FlexSPI flash", 0x60000000, 16*mb, "flash")}, ""),

	generic("svd", cmsis+"NXP/LPC11Uxx_v7.svd", "LPC11Uxx", "NXP", "LPC11xx", "arm", "Cortex-M0", "LPC11U", 0,
		[]region{R("Flash", 0, 128*kb, "flash"), R("SRAM", 0x10000000, 10*kb, "ram"), R("Boot ROM", 0x1FFF0000, 16*kb, "rom"), R("USB SRAM", 0x20004000, 2*kb, "ram")}, ""),
	generic("svd", cmsis+"NXP/LPC800_v0.3.svd", "LPC81x", "NXP", "LPC800", "arm", "Cortex-M0+", "LPC810 LPC811 LPC812 LPC81", 0,
		[]region{R("Flash", 0, 16*kb, "flash"), R("SRAM", 0x10000000, 4*kb, "ram"), R("Boot ROM", 0x1FFF0000, 8*kb, "rom")}, ""),
	generic("svd", cmsis+"Freescale/MK20D7.svd", "MK20D7", "NXP", "Kinetis K", "arm", "Cortex-M4", "MK20DX256 MK20DX128 MK20DN MK20D7", 0,
		[]region{R("Flash", 0, 256*kb, "flash"), R("FlexNVM", 0x10000000, 32*kb, "flash"), R("FlexRAM", 0x14000000, 2*kb, "ram"), R("SRAM_L", 0x1FFF8000, 32*kb, "ram"), R("SRAM_U", 0x20000000, 32*kb, "ram")}, ""),
	generic("svd", cmsis+"Freescale/MK64F12.svd", "MK64F12", "NXP", "Kinetis K", "arm", "Cortex-M4F", "MK64FN1M0 MK64FX512 MK64F", 0,
		[]region{R("Flash", 0, 1024*kb, "flash"), R("SRAM_L", 0x1FFF0000, 64*kb, "ram"), R("SRAM_U", 0x20000000, 192*kb, "ram"), R("FlexRAM", 0x14000000, 4*kb, "ram")}, ""),
	generic("svd", cmsis+"Freescale/MK66F18.svd", "MK66F18", "NXP", "Kinetis K", "arm", "Cortex-M4F", "MK66FX1M0 MK66FN2M0 MK66F", 0,
		[]region{R("Flash", 0, 2048*kb, "flash"), R("SRAM_L", 0x1FFF0000, 64*kb, "ram"), R("SRAM_U", 0x20000000, 192*kb, "ram"), R("FlexRAM", 0x14000000, 4*kb, "ram")}, ""),
	generic("svd", cmsis+"Freescale/MKL25Z4.svd", "MKL25Z4", "NXP", "Kinetis L", "arm", "Cortex-M0+", "MKL25Z", 0,
		[]region{R("Flash", 0, 128*kb, "flash"), R("SRAM_L", 0x1FFFF000, 4*kb, "ram"), R("SRAM_U", 0x20000000, 12*kb, "ram")}, ""),
	generic("svd", cmsis+"Freescale/MKL26Z4.svd", "MKL26Z4", "NXP", "Kinetis L", "arm", "Cortex-M0+", "MKL26Z", 0,
		[]region{R("Flash", 0, 256*kb, "flash"), R("SRAM_L", 0x1FFFE000, 8*kb, "ram"), R("SRAM_U", 0x20000000, 24*kb, "ram")}, ""),
	generic("svd", cmsis+"TexasInstruments/MSP432P401R.svd", "MSP432P401R", "Texas Instruments", "MSP432P4", "arm", "Cortex-M4F", "MSP432P401", 0,
		[]region{R("Flash", 0, 256*kb, "flash"), R("Information flash", 0x00200000, 16*kb, "flash"), R("SRAM (code alias)", 0x01000000, 64*kb, "ram"), R("ROM", 0x02000000, 32*kb, "rom"), R("SRAM", 0x20000000, 64*kb, "ram")}, ""),
	generic("svd", cmsis+"Infineon/XMC1100.svd", "XMC1100", "Infineon", "XMC1000", "arm", "Cortex-M0", "XMC1100", 0x10001000,
		[]region{R("ROM", 0, 8*kb, "rom"), R("Flash", 0x10001000, 64*kb, "flash"), R("SRAM", 0x20000000, 16*kb, "ram")}, ""),
	generic("svd", cmsis+"Infineon/XMC4700.svd", "XMC4700/4800", "Infineon", "XMC4000", "arm", "Cortex-M4F", "XMC4700 XMC4800", 0x08000000,
		[]region{R("ROM", 0, 16*kb, "rom"), R("Flash (cached)", 0x08000000, 2048*kb, "flash"), R("Flash (uncached)", 0x0C000000, 2048*kb, "flash"), R("PSRAM", 0x1FFE8000, 96*kb, "ram"), R("DSRAM1", 0x20000000, 128*kb, "ram"), R("DSRAM2", 0x20040000, 128*kb, "ram")}, ""),
	generic("svd", cmsis+"Atmel/ATSAME54P20A.svd", "ATSAME5x", "Microchip", "SAME5x", "arm", "Cortex-M4F", "ATSAME54 ATSAME53 ATSAME51 SAME54 SAME53 SAME51", 0,
		[]region{R("Flash", 0, 1024*kb, "flash"), R("SRAM", 0x20000000, 256*kb, "ram"), R("Backup RAM", 0x47000000, 8*kb, "ram"), R("NVM user page", 0x00804000, 512, "fuses")},
		"18:256/128 19:512/192 20:1024/256"),
	generic("svd", cmsis+"Atmel/ATSAME70Q21B.svd", "ATSAME70", "Microchip", "SAME70", "arm", "Cortex-M7", "ATSAME70 SAME70 ATSAMS70 ATSAMV71 ATSAMV70", 0x00400000,
		[]region{R("ITCM", 0, 256*kb, "ram"), R("Flash", 0x00400000, 2048*kb, "flash"), R("ROM", 0x00800000, 4*mb, "rom"), R("DTCM", 0x20000000, 256*kb, "ram"), R("SRAM", 0x20400000, 384*kb, "ram")},
		"19:512/256 20:1024/384 21:2048/384"),
	generic("svd", cmsis+"Atmel/ATSAMR21G18A.svd", "ATSAMR21", "Microchip", "SAMR21", "arm", "Cortex-M0+", "ATSAMR21 SAMR21", 0,
		[]region{R("Flash", 0, 256*kb, "flash"), R("SRAM", 0x20000000, 32*kb, "ram"), R("NVM user row", 0x00804000, 256, "fuses")},
		"16:64/8 17:128/16 18:256/32 19:256/32"),

	// ---- Espressif (cmsis-svd-data/Espressif from espressif/svd, Apache-2.0) ----
	esp("esp32.svd", "ESP32", "Xtensa LX6", "xtensa",
		R("Internal ROM 0", 0x40000000, 384*kb, "rom"), R("Internal ROM 1", 0x3FF90000, 64*kb, "rom"),
		R("SRAM0 (IRAM)", 0x40070000, 192*kb, "ram"), R("SRAM1 (DRAM)", 0x3FFE0000, 128*kb, "ram"), R("SRAM2 (DRAM)", 0x3FFAE000, 200*kb, "ram"),
		R("RTC FAST", 0x3FF80000, 8*kb, "ram"), R("RTC SLOW", 0x50000000, 8*kb, "ram"),
		R("Flash (IROM)", 0x400C2000, 0xB3E000, "flash"), R("Flash (DROM)", 0x3F400000, 4*mb, "flash"), R("External RAM", 0x3F800000, 4*mb, "ram")),
	esp("esp32s2.svd", "ESP32-S2", "Xtensa LX7", "xtensa",
		R("Internal ROM", 0x40000000, 128*kb, "rom"), R("Internal ROM (data)", 0x3FFA0000, 64*kb, "rom"),
		R("SRAM (IRAM)", 0x40020000, 320*kb, "ram"), R("SRAM (DRAM)", 0x3FFB0000, 320*kb, "ram"),
		R("RTC FAST", 0x3FF9E000, 8*kb, "ram"), R("RTC SLOW", 0x50000000, 8*kb, "ram"),
		R("Flash (IROM)", 0x40080000, 0x780000, "flash"), R("Flash (DROM)", 0x3F000000, 4*mb, "flash")),
	esp("esp32s3.svd", "ESP32-S3", "Xtensa LX7", "xtensa",
		R("Internal ROM", 0x40000000, 384*kb, "rom"), R("SRAM (IRAM)", 0x40370000, 448*kb, "ram"), R("SRAM (DRAM)", 0x3FC88000, 480*kb, "ram"),
		R("RTC FAST", 0x600FE000, 8*kb, "ram"), R("RTC SLOW", 0x50000000, 8*kb, "ram"),
		R("Flash (IROM)", 0x42000000, 32*mb, "flash"), R("Flash (DROM)", 0x3C000000, 32*mb, "flash")),
	esp("esp32c2.svd", "ESP32-C2", "RV32IMC", "riscv32",
		R("Internal ROM", 0x40000000, 576*kb, "rom"), R("SRAM (IRAM)", 0x4037C000, 272*kb, "ram"), R("SRAM (DRAM)", 0x3FCA0000, 256*kb, "ram"),
		R("Flash (IROM)", 0x42000000, 4*mb, "flash"), R("Flash (DROM)", 0x3C000000, 4*mb, "flash")),
	esp("esp32c3.svd", "ESP32-C3", "RV32IMC", "riscv32",
		R("Internal ROM", 0x40000000, 384*kb, "rom"), R("SRAM (IRAM)", 0x4037C000, 400*kb, "ram"), R("SRAM (DRAM)", 0x3FC80000, 400*kb, "ram"),
		R("RTC FAST", 0x50000000, 8*kb, "ram"),
		R("Flash (IROM)", 0x42000000, 8*mb, "flash"), R("Flash (DROM)", 0x3C000000, 8*mb, "flash")),
	esp("esp32c6.svd", "ESP32-C6", "RV32IMAC", "riscv32",
		R("Internal ROM", 0x40000000, 320*kb, "rom"), R("HP SRAM", 0x40800000, 512*kb, "ram"), R("LP SRAM", 0x50000000, 16*kb, "ram"),
		R("Flash", 0x42000000, 16*mb, "flash")),
	esp("esp32h2.svd", "ESP32-H2", "RV32IMAC", "riscv32",
		R("Internal ROM", 0x40000000, 128*kb, "rom"), R("HP SRAM", 0x40800000, 320*kb, "ram"), R("LP SRAM", 0x50000000, 4*kb, "ram"),
		R("Flash", 0x42000000, 4*mb, "flash")),

	// ---- RISC-V ----
	generic("svd", cmsis+"GigaDevice/GD32VF103.svd", "GD32VF103", "GigaDevice", "GD32VF1", "riscv32", "Bumblebee N205 (RV32IMAC)", "GD32VF103", 0x08000000,
		[]region{R("Flash", 0x08000000, 128*kb, "flash"), R("SRAM", 0x20000000, 32*kb, "ram"), R("Boot loader", 0x1FFFB000, 18*kb, "rom"), R("Option bytes", 0x1FFFF800, 16, "fuses")},
		"4:16/6 6:32/10 8:64/20 B:128/32"),
	generic("svd", ch32+"ch32v003.svd", "CH32V003", "WCH", "CH32V0", "riscv32", "QingKe V2A (RV32EC)", "CH32V003 CH32V00", 0,
		[]region{R("Flash (alias)", 0, 16*kb, "flash"), R("Flash", 0x08000000, 16*kb, "flash"), R("SRAM", 0x20000000, 2*kb, "ram"), R("System memory", 0x1FFFF000, 1920, "rom"), R("Option bytes", 0x1FFFF800, 64, "fuses")}, ""),
	generic("svd", ch32+"ch32v20x.svd", "CH32V20x", "WCH", "CH32V2", "riscv32", "QingKe V4B (RV32IMAC)", "CH32V203 CH32V208 CH32V20", 0,
		[]region{R("Flash (alias)", 0, 128*kb, "flash"), R("Flash", 0x08000000, 128*kb, "flash"), R("SRAM", 0x20000000, 64*kb, "ram"), R("System memory", 0x1FFF8000, 28*kb, "rom"), R("Option bytes", 0x1FFFF800, 16, "fuses")}, ""),
	generic("svd", ch32+"ch32v30x.svd", "CH32V30x", "WCH", "CH32V3", "riscv32", "QingKe V4F (RV32IMAFC)", "CH32V307 CH32V305 CH32V303 CH32V30", 0,
		[]region{R("Flash (alias)", 0, 288*kb, "flash"), R("Flash", 0x08000000, 288*kb, "flash"), R("SRAM", 0x20000000, 64*kb, "ram"), R("System memory", 0x1FFF8000, 28*kb, "rom"), R("Option bytes", 0x1FFFF800, 16, "fuses")}, ""),
	generic("svd", cmsis+"SiFive-Community/e310x.svd", "FE310", "SiFive", "FE310", "riscv32", "SiFive E31 (RV32IMAC)", "FE310 E310", 0x20010000,
		[]region{R("Mask ROM", 0x00010000, 8*kb, "rom"), R("QSPI flash", 0x20000000, 512*mb, "flash"), R("DTIM", 0x80000000, 16*kb, "ram")}, ""),

	// ---- AVR (Microchip device family packs, Apache-2.0) ----
	avr(megaP, "ATmega328P", ""), avr(megaP, "ATmega328PB", ""), avr(megaP, "ATmega328", ""),
	avr(megaP, "ATmega168PA", "ATMEGA168P"), avr(megaP, "ATmega168", ""), avr(megaP, "ATmega88PA", "ATMEGA88P ATMEGA88"), avr(megaP, "ATmega48PA", "ATMEGA48P ATMEGA48"),
	avr(megaP, "ATmega8A", "ATMEGA8"), avr(megaP, "ATmega16A", "ATMEGA16"), avr(megaP, "ATmega32A", "ATMEGA32"),
	avr(megaP, "ATmega2560", "ATMEGA2561"), avr(megaP, "ATmega1280", "ATMEGA1281"), avr(megaP, "ATmega1284P", "ATMEGA1284"),
	avr(megaP, "ATmega644PA", "ATMEGA644P ATMEGA644"), avr(megaP, "ATmega324PA", "ATMEGA324P"), avr(megaP, "ATmega128A", "ATMEGA128"),
	avr(megaP, "ATmega32U4", "ATMEGA16U4"), avr(megaP, "ATmega16U2", "ATMEGA8U2 ATMEGA32U2"), avr(megaP, "AT90USB1286", "AT90USB1287"),
	avr(megaP, "ATmega4809", "ATMEGA3209 ATMEGA1609 ATMEGA809"), avr(megaP, "ATmega4808", "ATMEGA3208 ATMEGA1608 ATMEGA808"),
	avr(tinyP, "ATtiny85", "ATTINY45 ATTINY25"), avr(tinyP, "ATtiny84A", "ATTINY84 ATTINY44 ATTINY24"), avr(tinyP, "ATtiny13A", "ATTINY13"),
	avr(tinyP, "ATtiny2313A", "ATTINY2313 ATTINY4313"), avr(tinyP, "ATtiny10", "ATTINY9 ATTINY5 ATTINY4"), avr(tinyP, "ATtiny88", "ATTINY48"),
	avr(tinyP, "ATtiny841", "ATTINY441"), avr(tinyP, "ATtiny1634", ""), avr(tinyP, "ATtiny167", "ATTINY87"), avr(tinyP, "ATtiny861A", "ATTINY861 ATTINY461 ATTINY261"),
	avr(tinyP, "ATtiny202", "ATTINY402"), avr(tinyP, "ATtiny412", "ATTINY212"), avr(tinyP, "ATtiny1614", "ATTINY814 ATTINY414 ATTINY214"),
	avr(tinyP, "ATtiny1616", "ATTINY816 ATTINY416"), avr(tinyP, "ATtiny3216", ""), avr(tinyP, "ATtiny3217", "ATTINY1617 ATTINY817 ATTINY417"),
	avr(tinyP, "ATtiny1627", "ATTINY1626 ATTINY1624 ATTINY3226 ATTINY3227 ATTINY3224 ATTINY827 ATTINY826 ATTINY824"),
	avr(dxP, "AVR128DA48", "AVR64DA48 AVR32DA48"), avr(dxP, "AVR128DA28", "AVR64DA28 AVR32DA28"), avr(dxP, "AVR128DA64", "AVR64DA64"),
	avr(dxP, "AVR128DB48", "AVR64DB48 AVR32DB48"), avr(dxP, "AVR64DD32", "AVR32DD32 AVR16DD32"), avr(dxP, "AVR64DU32", "AVR32DU32 AVR16DU32"),
}

// expandParts turns "code:flashK/ramK ..." into part patterns: pattern+"?"+code
// (the STM32 / GD32 flash-size letter follows the pin-count letter).
func expandParts(patterns []string, def string) []part {
	var out []part
	for _, item := range strings.Fields(def) {
		code, sizes, ok := strings.Cut(item, ":")
		if !ok {
			continue
		}
		fs, rs, _ := strings.Cut(sizes, "/")
		f, _ := strconv.ParseUint(fs, 10, 64)
		r, _ := strconv.ParseUint(rs, 10, 64)
		for _, p := range patterns {
			if strings.Contains(p, "?") {
				continue
			}
			out = append(out, part{Pattern: p + "?" + code, Flash: f * kb, RAM: r * kb})
		}
	}
	return out
}
