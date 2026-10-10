package fw

import (
	"fmt"
	"math/bits"
	"strings"
)

// More configuration areas (FW-11): the option bytes of more STM32 families,
// the fuses of more AVR families, the eFuse block 0 of the newer ESP32 chips.
// The 32-bit option registers are decoded as the reference manuals describe
// the register (FLASH_OPTR, FLASH_OPTCR, FLASH_OPTSR), little-endian, the way
// they are stored in the option flash and read by the programmers.

// ---- STM32 helpers ----

// optBit is a one-bit option: one and zero are what 1 and 0 mean
func optBit(name string, off int64, v uint64, bit int, one, zero string) ConfigField {
	return field(name, off, v, bit, 1, choose(v>>uint(bit)&1 != 0, one, zero))
}

func rdpLevel(v uint64) string {
	switch v {
	case 0xAA:
		return "level 0: no read protection (0xAA)"
	case 0xCC:
		return "level 2: the chip is protected for ever, debug disabled (0xCC)"
	}
	return "level 1: read protection on (any value but 0xAA and 0xCC)"
}

// complement16 checks a word stored as a half word and its complement
func complement16(name string, off int64, w uint64) ConfigField {
	lo, hi := w&0xFFFF, w>>16&0xFFFF
	m := "complement OK"
	if lo^hi != 0xFFFF {
		m = fmt.Sprintf("complement mismatch (0x%04X and 0x%04X): the option bytes are invalid", lo, hi)
	}
	return ConfigField{Name: "n" + name, Off: off + 2, Bits: 16, Value: hi, Meaning: m}
}

// ---- STM32F0/F3 ----

// decodeSTM32F0 decodes the F0/F3 option bytes at 0x1FFFF800: RDP, USER,
// Data0, Data1, WRP0..3, each byte followed by its complement
func decodeSTM32F0(d []byte) []ConfigField {
	var out []ConfigField
	comp := func(name string, i int) {
		if i+1 >= len(d) {
			return
		}
		v, nv := d[i], d[i+1]
		m := "complement OK"
		if v^nv != 0xFF {
			m = fmt.Sprintf("complement mismatch (0x%02X and 0x%02X): the option bytes are invalid, OPTERR is set", v, nv)
		}
		out = append(out, ConfigField{Name: "n" + name, Off: int64(i + 1), Bits: 8, Value: uint64(nv), Meaning: m})
	}
	rdp := uint64(d[0])
	out = append(out, ConfigField{Name: "RDP", Off: 0, Bits: 8, Value: rdp, Meaning: rdpLevel(rdp)})
	comp("RDP", 0)
	u := uint64(d[2])
	out = append(out,
		optBit("WDG_SW", 2, u, 0, "software watchdog", "hardware watchdog (started at reset)"),
		optBit("nRST_STOP", 2, u, 1, "no reset when entering Stop mode", "reset when entering Stop mode"),
		optBit("nRST_STDBY", 2, u, 2, "no reset when entering Standby mode", "reset when entering Standby mode"),
		optBit("nBOOT0", 2, u, 3, "BOOT0 = 0 when BOOT_SEL = 0 (STM32F04x/F09x only)", "BOOT0 = 1 when BOOT_SEL = 0 (STM32F04x/F09x only)"),
		optBit("nBOOT1", 2, u, 4, "with BOOT0 = 1: boot from the system memory", "with BOOT0 = 1: boot from the embedded SRAM"),
		optBit("VDDA_MONITOR", 2, u, 5, "the VDDA power supply supervisor is on", "the VDDA power supply supervisor is off"),
		optBit("RAM_PARITY_CHECK", 2, u, 6, "RAM parity check disabled", "RAM parity check enabled"),
		optBit("BOOT_SEL", 2, u, 7, "BOOT0 is the BOOT0 pin (STM32F04x/F09x; reserved on F3)", "BOOT0 is the nBOOT0 option bit (STM32F04x/F09x)"))
	comp("USER", 2)
	for i, off := range []int{4, 6} {
		if off < len(d) {
			out = append(out, ConfigField{Name: fmt.Sprintf("Data%d", i), Off: int64(off), Bits: 8, Value: uint64(d[off]), Meaning: "user data byte"})
			comp(fmt.Sprintf("Data%d", i), off)
		}
	}
	for i := 0; i < 4; i++ {
		off := 8 + 2*i
		if off >= len(d) {
			break
		}
		v := uint64(d[off])
		out = append(out, ConfigField{Name: fmt.Sprintf("WRP%d", i), Off: int64(off), Bits: 8, Value: v,
			Meaning: wrpMeaning(v, 8, 8*i, "sector", "a sector is 4 KiB: 4 pages of 1 KiB or 2 pages of 2 KiB")})
		comp(fmt.Sprintf("WRP%d", i), off)
	}
	return out
}

// ---- STM32F7 ----

func f7BOR(v uint64) string {
	return []string{"BOR level 3 (2.70-3.60 V)", "BOR level 2 (2.40-2.70 V)", "BOR level 1 (2.10-2.40 V)", "BOR off (POR/PDR only)"}[v]
}

// decodeSTM32F7 decodes FLASH_OPTCR and, if present, FLASH_OPTCR1
func decodeSTM32F7(d []byte) []ConfigField {
	w := uint64(le32(d))
	nwrp := pick(w, 16, 12)
	out := []ConfigField{
		field("BOR_LEV", 0, w, 2, 2, f7BOR(pick(w, 2, 2))),
		optBit("WWDG_SW", 0, w, 4, "software window watchdog", "hardware window watchdog"),
		optBit("IWDG_SW", 0, w, 5, "software independent watchdog", "hardware independent watchdog (started at reset)"),
		optBit("nRST_STOP", 0, w, 6, "no reset when entering Stop mode", "reset when entering Stop mode"),
		optBit("nRST_STDBY", 0, w, 7, "no reset when entering Standby mode", "reset when entering Standby mode"),
		field("RDP", 0, w, 8, 8, rdpLevel(pick(w, 8, 8))),
		field("nWRP", 0, w, 16, 12, wrpMeaning(nwrp, 12, 0, "sector", "bits 8-11 exist on STM32F76x/F77x only")),
		optBit("nDBOOT", 0, w, 28, "dual boot off (STM32F76x/F77x)", "dual boot on (STM32F76x/F77x)"),
		optBit("nDBANK", 0, w, 29, "single bank, 256-bit reads (STM32F76x/F77x)", "dual bank, 128-bit reads (STM32F76x/F77x)"),
		optBit("IWDG_STDBY", 0, w, 30, "the independent watchdog runs in Standby mode", "the independent watchdog is frozen in Standby mode"),
		optBit("IWDG_STOP", 0, w, 31, "the independent watchdog runs in Stop mode", "the independent watchdog is frozen in Stop mode"),
	}
	if len(d) >= 8 {
		w1 := uint64(le32(d[4:]))
		out = append(out,
			field("BOOT_ADD0", 4, w1, 0, 16, fmt.Sprintf("boot address with BOOT0 = 0: 0x%08X", pick(w1, 0, 16)<<14)),
			field("BOOT_ADD1", 4, w1, 16, 16, fmt.Sprintf("boot address with BOOT0 = 1: 0x%08X", pick(w1, 16, 16)<<14)))
	}
	return out
}

// ---- STM32L0/L1 ----

func l01BOR(v uint64) string {
	switch {
	case v < 8:
		return "BOR off"
	case v <= 0xC:
		return []string{"BOR level 1 (1.69-1.80 V)", "BOR level 2 (1.94-2.10 V)", "BOR level 3 (2.30-2.49 V)",
			"BOR level 4 (2.54-2.74 V)", "BOR level 5 (2.77-3.00 V)"}[v-8]
	}
	return "reserved value"
}

// decodeSTM32L0 decodes the option bytes at 0x1FF80000: words whose low
// half word is the option and whose high half word is its complement; word
// 0 is FLASH_OPTR[15:0], word 1 FLASH_OPTR[31:16], words 2 and 3 the write
// protection of sectors 0-15 and 16-31
func decodeSTM32L0(d []byte) []ConfigField {
	w0, w1 := uint64(le32(d)), uint64(le32(d[4:]))
	out := []ConfigField{
		field("RDP", 0, w0, 0, 8, rdpLevel(pick(w0, 0, 8))),
		optBit("WPRMOD", 0, w0, 8, "WRP bits are PCROP (read protection of sectors, STM32L0) / SPRMOD (STM32L1)", "WRP bits are write protection"),
		complement16("OPTR_LO", 0, w0),
		field("BOR_LEV", 4, w1, 0, 4, l01BOR(pick(w1, 0, 4))),
		optBit("WDG_SW", 4, w1, 4, "software independent watchdog", "hardware independent watchdog (started at reset)"),
		optBit("nRST_STOP", 4, w1, 5, "no reset when entering Stop mode", "reset when entering Stop mode"),
		optBit("nRST_STDBY", 4, w1, 6, "no reset when entering Standby mode", "reset when entering Standby mode"),
		optBit("BFB2", 4, w1, 7, "boot from bank 2 when its vectors are valid (dual-bank parts)", "boot from bank 1"),
		optBit("nBOOT1", 4, w1, 15, "with BOOT0 = 1: boot from the system memory (STM32L0)", "with BOOT0 = 1: boot from the SRAM (STM32L0)"),
		complement16("OPTR_HI", 4, w1),
	}
	for i := 0; i < 2 && 8+4*i+4 <= len(d); i++ {
		off := 8 + 4*i
		w := uint64(le32(d[off:]))
		out = append(out, field(fmt.Sprintf("WRP%d", i+1), int64(off), w, 0, 16,
			wrpProt(w&0xFFFF, 16, 16*i, "a sector is 4 KiB; bits are active-high: 1 - protected")),
			complement16(fmt.Sprintf("WRP%d", i+1), int64(off), w))
	}
	return out
}

// wrpProt lists the units of active-high protection bits
func wrpProt(v uint64, n, first int, note string) string {
	var prot []string
	for i := 0; i < n; i++ {
		if v&(1<<uint(i)) != 0 {
			prot = append(prot, fmt.Sprint(first+i))
		}
	}
	s := "no write protection"
	if len(prot) > 0 {
		s = "write protected: sector " + strings.Join(prot, ", ")
	}
	return s + " (" + note + ")"
}

// ---- STM32L4/G0/G4/WB: FLASH_OPTR ----

func l4BOR(v uint64) string {
	if v > 4 {
		return "reserved value"
	}
	return []string{"BOR level 0 (~1.7 V)", "BOR level 1 (~2.0 V)", "BOR level 2 (~2.2 V)", "BOR level 3 (~2.5 V)", "BOR level 4 (~2.8 V)"}[v]
}

func nrstMode(v uint64) string {
	return []string{"reserved", "NRST is the reset input only", "NRST is a GPIO (reset is internal only)", "NRST is bidirectional: reset input and output"}[v]
}

// stm32OPTR decodes FLASH_OPTR with the family fields; the rest of the
// option flash, when given, follows as in the reference manuals: the
// complement of OPTR at 4, PCROP at 0x08 and 0x10, WRP area A at 0x18 and B
// at 0x20 (each in a 64-bit double word)
func stm32OPTR(d []byte, family func(w uint64) []ConfigField) []ConfigField {
	w := uint64(le32(d))
	out := []ConfigField{field("RDP", 0, w, 0, 8, rdpLevel(pick(w, 0, 8)))}
	out = append(out, family(w)...)
	if len(d) >= 8 {
		nw := uint64(le32(d[4:]))
		m := "complement OK"
		if nw^w != 0xFFFFFFFF {
			m = fmt.Sprintf("complement mismatch (0x%08X and 0x%08X): the option bytes are invalid, OPTVERR is set", w, nw)
		}
		out = append(out, ConfigField{Name: "nOPTR", Off: 4, Bits: 32, Value: nw, Meaning: m})
	}
	for i, off := range []int{0x18, 0x20} {
		if off+4 > len(d) {
			break
		}
		r := uint64(le32(d[off:]))
		s, e := pick(r, 0, 8), pick(r, 16, 8)
		m := "no write protection (START > END)"
		if s <= e {
			m = fmt.Sprintf("pages %d to %d are write protected (offsets from the bank start)", s, e)
		}
		name := fmt.Sprintf("WRP1%c", 'A'+i)
		out = append(out,
			field(name+"_STRT", int64(off), r, 0, 8, "first page of the area"),
			field(name+"_END", int64(off), r, 16, 8, m))
	}
	return out
}

// the fields from nRST_STOP to WWDG_SW, common to L4, G4, WB
func l4Common(w uint64) []ConfigField {
	return []ConfigField{
		optBit("nRST_STOP", 0, w, 12, "no reset when entering Stop mode", "reset when entering Stop mode"),
		optBit("nRST_STDBY", 0, w, 13, "no reset when entering Standby mode", "reset when entering Standby mode"),
		optBit("nRST_SHDW", 0, w, 14, "no reset when entering Shutdown mode", "reset when entering Shutdown mode"),
		optBit("IWDG_SW", 0, w, 16, "software independent watchdog", "hardware independent watchdog (started at reset)"),
		optBit("IWDG_STOP", 0, w, 17, "the independent watchdog runs in Stop mode", "the independent watchdog is frozen in Stop mode"),
		optBit("IWDG_STDBY", 0, w, 18, "the independent watchdog runs in Standby mode", "the independent watchdog is frozen in Standby mode"),
		optBit("WWDG_SW", 0, w, 19, "software window watchdog", "hardware window watchdog"),
	}
}

func bootBits(w uint64, sel string) []ConfigField {
	return []ConfigField{
		optBit("nBOOT1", 0, w, 23, "with BOOT0 = 1: boot from the system memory", "with BOOT0 = 1: boot from the embedded SRAM"),
		optBit(sel, 0, w, 26, "BOOT0 is the BOOT0 pin", "BOOT0 is the nBOOT0 option bit"),
		optBit("nBOOT0", 0, w, 27, "BOOT0 = 0 when it is taken from the option bit", "BOOT0 = 1 when it is taken from the option bit"),
	}
}

func decodeSTM32L4(d []byte) []ConfigField {
	return stm32OPTR(d, func(w uint64) []ConfigField {
		out := []ConfigField{field("BOR_LEV", 0, w, 8, 3, l4BOR(pick(w, 8, 3)))}
		out = append(out, l4Common(w)...)
		out = append(out,
			optBit("BFB2", 0, w, 20, "boot from bank 2 (dual-bank parts)", "boot from bank 1"),
			optBit("DUALBANK", 0, w, 21, "dual bank (on 256/512 KiB parts)", "single bank (on 256/512 KiB parts)"),
			optBit("SRAM2_PE", 0, w, 24, "SRAM2 parity check disabled", "SRAM2 parity check enabled"),
			optBit("SRAM2_RST", 0, w, 25, "SRAM2 is not erased on a system reset", "SRAM2 is erased on a system reset"))
		return append(out, bootBits(w, "nSWBOOT0")...)
	})
}

func decodeSTM32G4(d []byte) []ConfigField {
	return stm32OPTR(d, func(w uint64) []ConfigField {
		out := []ConfigField{field("BOR_LEV", 0, w, 8, 3, l4BOR(pick(w, 8, 3)))}
		out = append(out, l4Common(w)...)
		out = append(out,
			optBit("BFB2", 0, w, 20, "boot from bank 2 (dual-bank mode)", "boot from bank 1"),
			optBit("DBANK", 0, w, 22, "dual bank mode (category 3 parts)", "single bank mode (category 3 parts)"),
			optBit("SRAM_PE", 0, w, 24, "SRAM1 and CCM SRAM parity check disabled", "SRAM1 and CCM SRAM parity check enabled"),
			optBit("CCMSRAM_RST", 0, w, 25, "CCM SRAM is not erased on a system reset", "CCM SRAM is erased on a system reset"))
		out = append(out, bootBits(w, "nSWBOOT0")...)
		return append(out,
			field("NRST_MODE", 0, w, 28, 2, nrstMode(pick(w, 28, 2))),
			optBit("IRHEN", 0, w, 30, "internal resets drive NRST low until it is seen low", "internal resets are a pulse on NRST"))
	})
}

func decodeSTM32WB(d []byte) []ConfigField {
	return stm32OPTR(d, func(w uint64) []ConfigField {
		out := []ConfigField{
			optBit("ESE", 0, w, 8, "security of CPU2 (the wireless stack) is enabled", "security disabled"),
			field("BOR_LEV", 0, w, 9, 3, l4BOR(pick(w, 9, 3))),
		}
		out = append(out, l4Common(w)...)
		out = append(out,
			optBit("SRAM2_PE", 0, w, 24, "SRAM2 parity check disabled", "SRAM2 parity check enabled"),
			optBit("SRAM2_RST", 0, w, 25, "SRAM2 and PKA RAM are not erased on a system reset", "SRAM2 and PKA RAM are erased on a system reset"))
		out = append(out, bootBits(w, "nSWBOOT0")...)
		return append(out, field("AGC_TRIM", 0, w, 29, 3, "radio automatic gain control trimming"))
	})
}

func decodeSTM32G0(d []byte) []ConfigField {
	return stm32OPTR(d, func(w uint64) []ConfigField {
		borf := []string{"~2.0 V", "~2.2 V", "~2.5 V", "~2.8 V"}
		borr := []string{"~2.1 V", "~2.3 V", "~2.6 V", "~2.9 V"}
		out := []ConfigField{
			optBit("BOR_EN", 0, w, 8, "brown-out reset enabled (BORF_LEV, BORR_LEV)", "brown-out reset off (POR/PDR only)"),
			field("BORF_LEV", 0, w, 9, 2, "BOR falling threshold "+borf[pick(w, 9, 2)]),
			field("BORR_LEV", 0, w, 11, 2, "BOR rising threshold "+borr[pick(w, 11, 2)]),
			optBit("nRST_STOP", 0, w, 13, "no reset when entering Stop mode", "reset when entering Stop mode"),
			optBit("nRST_STDBY", 0, w, 14, "no reset when entering Standby mode", "reset when entering Standby mode"),
			optBit("nRST_SHDW", 0, w, 15, "no reset when entering Shutdown mode", "reset when entering Shutdown mode"),
			optBit("IWDG_SW", 0, w, 16, "software independent watchdog", "hardware independent watchdog (started at reset)"),
			optBit("IWDG_STOP", 0, w, 17, "the independent watchdog runs in Stop mode", "the independent watchdog is frozen in Stop mode"),
			optBit("IWDG_STDBY", 0, w, 18, "the independent watchdog runs in Standby mode", "the independent watchdog is frozen in Standby mode"),
			optBit("WWDG_SW", 0, w, 19, "software window watchdog", "hardware window watchdog"),
			optBit("nSWAP_BANK", 0, w, 20, "bank 1 at 0x08000000 (STM32G0B/G0C)", "bank 2 at 0x08000000 (STM32G0B/G0C)"),
			optBit("DUAL_BANK", 0, w, 21, "dual bank (STM32G0B/G0C)", "single bank (STM32G0B/G0C)"),
			optBit("RAM_PARITY_CHECK", 0, w, 22, "SRAM parity check disabled", "SRAM parity check enabled"),
			optBit("nBOOT_SEL", 0, w, 24, "BOOT0 is the nBOOT0 option bit", "BOOT0 is the PA14-BOOT0 pin (legacy)"),
			optBit("nBOOT1", 0, w, 25, "with BOOT0 = 1: boot from the system memory", "with BOOT0 = 1: boot from the embedded SRAM"),
			optBit("nBOOT0", 0, w, 26, "BOOT0 = 0 when it is taken from the option bit", "BOOT0 = 1 when it is taken from the option bit"),
			field("NRST_MODE", 0, w, 27, 2, nrstMode(pick(w, 27, 2))),
			optBit("IRHEN", 0, w, 29, "internal resets drive NRST low until it is seen low", "internal resets are a pulse on NRST"),
		}
		return out
	})
}

// ---- STM32H7: FLASH_OPTSR ----

func decodeSTM32H7(d []byte) []ConfigField {
	w := uint64(le32(d))
	bor := []string{"BOR off (VBOR0, POR/PDR only)", "BOR level 1 (~2.1 V)", "BOR level 2 (~2.4 V)", "BOR level 3 (~2.7 V)"}
	ram := []string{"no ST RAM reserved", "2 KiB of ST RAM reserved", "4 KiB of ST RAM reserved", "8 KiB of ST RAM reserved"}
	return []ConfigField{
		field("BOR_LEV", 0, w, 2, 2, bor[pick(w, 2, 2)]),
		optBit("IWDG1_SW", 0, w, 4, "software independent watchdog 1", "hardware independent watchdog 1 (started at reset)"),
		optBit("IWDG2_SW", 0, w, 5, "software independent watchdog 2 (dual-core parts)", "hardware independent watchdog 2 (dual-core parts)"),
		optBit("NRST_STOP_D1", 0, w, 6, "no reset when D1 enters Stop mode", "reset when D1 enters Stop mode"),
		optBit("NRST_STBY_D1", 0, w, 7, "no reset when D1 enters Standby mode", "reset when D1 enters Standby mode"),
		field("RDP", 0, w, 8, 8, rdpLevel(pick(w, 8, 8))),
		optBit("FZ_IWDG_STOP", 0, w, 16, "the independent watchdog runs in Stop mode", "the independent watchdog is frozen in Stop mode"),
		optBit("FZ_IWDG_SDBY", 0, w, 17, "the independent watchdog runs in Standby mode", "the independent watchdog is frozen in Standby mode"),
		field("ST_RAM_SIZE", 0, w, 18, 2, ram[pick(w, 18, 2)]+" (secure access mode)"),
		optBit("SECURITY", 0, w, 21, "secure access mode ENABLED", "secure access mode off"),
		optBit("BCM4", 0, w, 22, "the Cortex-M4 boots (dual-core parts)", "the Cortex-M4 is gated at boot (dual-core parts)"),
		optBit("BCM7", 0, w, 23, "the Cortex-M7 boots (dual-core parts)", "the Cortex-M7 is gated at boot (dual-core parts)"),
		optBit("NRST_STOP_D2", 0, w, 24, "no reset when D2 enters Stop mode (dual-core parts)", "reset when D2 enters Stop mode (dual-core parts)"),
		optBit("NRST_STBY_D2", 0, w, 25, "no reset when D2 enters Standby mode (dual-core parts)", "reset when D2 enters Standby mode (dual-core parts)"),
		optBit("IO_HSLV", 0, w, 29, "I/O high speed at low voltage on (only with VDD < 2.7 V)", "I/O high speed at low voltage off"),
		optBit("SWAP_BANK_OPT", 0, w, 31, "the banks are swapped: bank 2 at 0x08000000", "the banks are not swapped"),
	}
}

// ---- AVR helpers ----

// fuse is a one-bit AVR fuse: prog and unprog say what 0 and 1 mean
func fuse(name string, off int64, v uint64, bit int, progd, unprog string) ConfigField {
	return field(name, off, v, bit, 1, choose(prog(v, bit), "programmed: "+progd, "unprogrammed: "+unprog)+avrActiveLow)
}

func avrBOD3(v uint64, levels map[uint64]string) string {
	m, ok := levels[v]
	if !ok {
		m = "reserved value"
	}
	return m + avrActiveLow
}

// avrLow is the low fuse byte of the ATmega and of most ATtiny: CKSEL, SUT,
// CKOUT, CKDIV8
func avrLow(v uint64, cksel func(uint64) string) []ConfigField {
	return []ConfigField{
		field("CKSEL", 0, v, 0, 4, cksel(pick(v, 0, 4))+avrActiveLow),
		field("SUT", 0, v, 4, 2, sutMeaning(pick(v, 4, 2), pick(v, 0, 4))),
		fuse("CKOUT", 0, v, 6, "the clock is output on the CLKO pin", "no clock output"),
		fuse("CKDIV8", 0, v, 7, "the clock is divided by 8 at start", "no division"),
	}
}

// ---- ATtiny25/45/85, 24/44/84, 261/461/861 ----

func tiny85CKSEL(v uint64) string {
	switch v {
	case 0:
		return "external clock"
	case 1:
		return "high frequency PLL clock, 16 MHz (ATtiny25/45/85, 261/461/861)"
	case 2:
		return "calibrated internal RC oscillator, 8 MHz"
	case 3:
		return "ATtiny15 compatibility mode, 6.4 MHz (ATtiny25/45/85); reserved on the others"
	case 4:
		return "internal 128 kHz RC oscillator"
	case 6:
		return "low frequency crystal oscillator (32.768 kHz)"
	case 5, 7:
		return "reserved"
	}
	return "crystal oscillator, " + []string{"0.4-0.9 MHz", "0.9-3 MHz", "3-8 MHz", "8 MHz and more"}[(v-8)/2]
}

var tinyBOD = map[uint64]string{7: "brown-out detection disabled", 6: "BOD at 1.8 V", 5: "BOD at 2.7 V", 4: "BOD at 4.3 V"}

func decodeATtinyFuses(d []byte) []ConfigField {
	out := avrLow(uint64(d[0]), tiny85CKSEL)
	if len(d) >= 2 {
		hi := uint64(d[1])
		out = append(out,
			field("BODLEVEL", 1, hi, 0, 3, avrBOD3(pick(hi, 0, 3), tinyBOD)),
			fuse("EESAVE", 1, hi, 3, "the EEPROM is kept through a chip erase", "chip erase clears the EEPROM"),
			fuse("WDTON", 1, hi, 4, "the watchdog is always on", "the watchdog is enabled by software"),
			fuse("SPIEN", 1, hi, 5, "serial (SPI) programming enabled", "serial programming DISABLED"),
			fuse("DWEN", 1, hi, 6, "debugWIRE enabled (the reset pin is debugWIRE)", "debugWIRE off"),
			fuse("RSTDISBL", 1, hi, 7, "the reset pin is an I/O pin; ISP is no longer possible", "the reset pin resets"))
	}
	if len(d) >= 3 {
		out = append(out, fuse("SELFPRGEN", 2, uint64(d[2]), 0, "self-programming (SPM) enabled", "self-programming disabled"))
	}
	return out
}

// ---- ATtiny13/13A ----

func decodeATtiny13Fuses(d []byte) []ConfigField {
	lo := uint64(d[0])
	cksel := []string{"external clock", "calibrated internal RC oscillator, 4.8 MHz", "calibrated internal RC oscillator, 9.6 MHz", "internal 128 kHz RC oscillator"}
	sut := []string{"start-up 6 CK, +14 CK", "start-up 6 CK, +14 CK + 4 ms", "start-up 6 CK, +14 CK + 64 ms", "reserved"}
	out := []ConfigField{
		field("CKSEL", 0, lo, 0, 2, cksel[pick(lo, 0, 2)]+avrActiveLow),
		field("SUT", 0, lo, 2, 2, sut[pick(lo, 2, 2)]+avrActiveLow),
		fuse("CKDIV8", 0, lo, 4, "the clock is divided by 8 at start", "no division"),
		fuse("WDTON", 0, lo, 5, "the watchdog is always on", "the watchdog is enabled by software"),
		fuse("EESAVE", 0, lo, 6, "the EEPROM is kept through a chip erase", "chip erase clears the EEPROM"),
		fuse("SPIEN", 0, lo, 7, "serial (SPI) programming enabled", "serial programming DISABLED"),
	}
	if len(d) >= 2 {
		hi := uint64(d[1])
		bod := []string{"BOD at 4.3 V", "BOD at 2.7 V", "BOD at 1.8 V", "brown-out detection disabled"}
		out = append(out,
			fuse("RSTDISBL", 1, hi, 0, "the reset pin is an I/O pin; ISP is no longer possible", "the reset pin resets"),
			field("BODLEVEL", 1, hi, 1, 2, bod[pick(hi, 1, 2)]+avrActiveLow),
			fuse("DWEN", 1, hi, 3, "debugWIRE enabled (the reset pin is debugWIRE)", "debugWIRE off"),
			fuse("SELFPRGEN", 1, hi, 4, "self-programming (SPM) enabled", "self-programming disabled"))
	}
	return out
}

// ---- ATmega16U4/32U4 and ATmega640/1280/2560, 644/1284 ----

// the high fuse of the ATmega parts with JTAG
func avrJTAGHigh(hi uint64, bootsz []string) []ConfigField {
	return []ConfigField{
		fuse("BOOTRST", 1, hi, 0, "reset jumps to the boot loader section", "reset jumps to 0x0000"),
		field("BOOTSZ", 1, hi, 1, 2, "boot section "+bootsz[pick(hi, 1, 2)]+avrActiveLow),
		fuse("EESAVE", 1, hi, 3, "the EEPROM is kept through a chip erase", "chip erase clears the EEPROM"),
		fuse("WDTON", 1, hi, 4, "the watchdog is always on", "the watchdog is enabled by software"),
		fuse("SPIEN", 1, hi, 5, "serial (SPI) programming enabled", "serial programming DISABLED"),
		fuse("JTAGEN", 1, hi, 6, "JTAG enabled", "JTAG disabled"),
		fuse("OCDEN", 1, hi, 7, "on-chip debugging enabled (JTAGEN must be programmed too)", "on-chip debugging off"),
	}
}

func decodeATmega32U4Fuses(d []byte) []ConfigField {
	ck := func(v uint64) string {
		if v == 3 || v == 1 {
			return "reserved"
		}
		return ckselMeaning(v)
	}
	out := avrLow(uint64(d[0]), ck)
	if len(d) >= 2 {
		out = append(out, avrJTAGHigh(uint64(d[1]), []string{
			"2048 words, from word 0x3800 (byte 0x7000) of the ATmega32U4", "1024 words, from word 0x3C00 (byte 0x7800)",
			"512 words, from word 0x3E00 (byte 0x7C00)", "256 words, from word 0x3F00 (byte 0x7E00)"})...)
	}
	if len(d) >= 3 {
		ex := uint64(d[2])
		bod := map[uint64]string{7: "brown-out detection disabled", 6: "BOD at 2.0 V", 5: "BOD at 2.2 V", 4: "BOD at 2.4 V",
			3: "BOD at 2.6 V", 2: "BOD at 3.4 V", 1: "BOD at 3.5 V", 0: "BOD at 4.3 V"}
		out = append(out,
			field("BODLEVEL", 2, ex, 0, 3, avrBOD3(pick(ex, 0, 3), bod)),
			fuse("HWBE", 2, ex, 3, "the HWB pin starts the boot loader when low at reset", "the HWB pin is an I/O pin"))
	}
	return out
}

func decodeATmega2560Fuses(d []byte) []ConfigField {
	out := avrLow(uint64(d[0]), ckselMeaning)
	if len(d) >= 2 {
		out = append(out, avrJTAGHigh(uint64(d[1]), []string{
			"4096 words, from word 0x1F000 of the ATmega2560 (ATmega1280/1284: 0xF000)",
			"2048 words, from word 0x1F800 (ATmega1280/1284: 0xF800)",
			"1024 words, from word 0x1FC00 (ATmega1280/1284: 0xFC00)",
			"512 words, from word 0x1FE00 (ATmega1280/1284: 0xFE00)"})...)
	}
	if len(d) >= 3 {
		ex := uint64(d[2])
		out = append(out, field("BODLEVEL", 2, ex, 0, 3, avrBOD3(pick(ex, 0, 3), tinyBOD)))
	}
	return out
}

// ---- AVR with UPDI: tinyAVR 0/1/2, megaAVR 0, AVR Dx ----

func wdtPeriod(v uint64) string {
	if v == 0 {
		return "off"
	}
	if v > 0xB {
		return "reserved"
	}
	c := 4 << v // cycles of the 1 kHz oscillator
	if c >= 1024 {
		return fmt.Sprintf("%dK cycles (~%d s)", c/1024, c/1024)
	}
	return fmt.Sprintf("%d cycles (~%d ms)", c, c)
}

func bodMode(v uint64) string {
	return []string{"disabled", "enabled", "sampled", "enabled, wake-up waits for the BOD (ENWAKE; active mode only)"}[v]
}

// updiWDT decodes WDTCFG
func updiWDT(d []byte) []ConfigField {
	w := uint64(d[0])
	return []ConfigField{
		field("WDT_PERIOD", 0, w, 0, 4, "watchdog period at reset: "+wdtPeriod(pick(w, 0, 4))),
		field("WDT_WINDOW", 0, w, 4, 4, "watchdog window at reset: "+wdtPeriod(pick(w, 4, 4))),
	}
}

func updiBOD(d []byte, lvl []string, freq [2]string) []ConfigField {
	if len(d) < 2 {
		return nil
	}
	b := uint64(d[1])
	l := "reserved"
	if v := pick(b, 5, 3); int(v) < len(lvl) && lvl[v] != "" {
		l = lvl[v]
	}
	return []ConfigField{
		field("BOD_SLEEP", 1, b, 0, 2, "BOD in sleep: "+choose(pick(b, 0, 2) == 3, "reserved", bodMode(pick(b, 0, 2)))),
		field("BOD_ACTIVE", 1, b, 2, 2, "BOD in active mode: "+bodMode(pick(b, 2, 2))),
		field("BOD_SAMPFREQ", 1, b, 4, 1, "sampling frequency "+freq[pick(b, 4, 1)]),
		field("BOD_LVL", 1, b, 5, 3, "BOD level "+l),
	}
}

func updiSUT(d []byte) []ConfigField {
	if len(d) < 7 {
		return nil
	}
	v := uint64(d[6])
	return []ConfigField{field("SUT", 6, v, 0, 3, fmt.Sprintf("start-up time %d ms", []int{0, 1, 2, 4, 8, 16, 32, 64}[pick(v, 0, 3)]))}
}

func crcSrc(v uint64) string {
	return []string{"CRC of the whole flash at reset", "CRC of the boot section at reset", "CRC of the boot and application sections at reset", "no CRC at reset"}[v]
}

// decodeTinyAVR01Fuses decodes FUSE.WDTCFG..BOOTEND (9 bytes at 0x1280 of
// the tinyAVR 0/1/2-series, megaAVR 0-series)
func decodeTinyAVR01Fuses(d []byte) []ConfigField {
	out := updiWDT(d)
	out = append(out, updiBOD(d, []string{"1.8 V (BODLEVEL0)", "", "2.6 V (BODLEVEL2)", "", "", "", "", "4.3 V (BODLEVEL7)"}, [2]string{"1 kHz", "125 Hz"})...)
	if len(d) >= 3 {
		o := uint64(d[2])
		fs := []string{"reserved", "16 MHz", "20 MHz", "reserved"}
		out = append(out,
			field("FREQSEL", 2, o, 0, 2, "main oscillator "+fs[pick(o, 0, 2)]),
			optBit("OSCLOCK", 2, o, 7, "the oscillator calibration is locked", "the oscillator calibration may be changed"))
	}
	if len(d) >= 5 {
		t := uint64(d[4])
		var en []string
		for i := 0; i < 4; i++ {
			if t>>uint(4+i)&1 != 0 {
				en = append(en, fmt.Sprintf("WO%c=%d", 'A'+i, t>>uint(i)&1))
			}
		}
		m := "no TCD0 outputs enabled at reset"
		if len(en) > 0 {
			m = "TCD0 outputs enabled at reset with values " + strings.Join(en, ", ")
		}
		out = append(out, field("TCD0CFG", 4, t, 0, 8, m+" (tinyAVR 1-series only)"))
	}
	if len(d) >= 6 {
		s := uint64(d[5])
		rst := []string{"GPIO", "UPDI", "RESET", "reserved"}
		out = append(out,
			optBit("EESAVE", 5, s, 0, "the EEPROM is kept through a chip erase", "chip erase clears the EEPROM"),
			field("RSTPINCFG", 5, s, 2, 2, "the UPDI/reset pin is "+rst[pick(s, 2, 2)]+" (megaAVR 0: bit 3 only, 0 - GPIO, 1 - RESET)"),
			field("CRCSRC", 5, s, 6, 2, crcSrc(pick(s, 6, 2))))
	}
	out = append(out, updiSUT(d)...)
	if len(d) >= 9 {
		a, b := uint64(d[7]), uint64(d[8])
		out = append(out,
			field("APPEND", 7, a, 0, 8, sectionEnd("application code", a, 256)),
			field("BOOTEND", 8, b, 0, 8, sectionEnd("boot", b, 256)))
	}
	return out
}

func sectionEnd(name string, v uint64, block int) string {
	if v == 0 {
		return name + " section to the end of the flash"
	}
	return fmt.Sprintf("%s section ends at 0x%X (%d blocks of %d bytes)", name, v*uint64(block), v, block)
}

// decodeAVRDxFuses decodes FUSE.WDTCFG..BOOTSIZE (9 bytes at 0x1050 of the
// AVR DA/DB/DD/DU)
func decodeAVRDxFuses(d []byte) []ConfigField {
	out := updiWDT(d)
	out = append(out, updiBOD(d, []string{"1.9 V (BODLEVEL0)", "2.45 V (BODLEVEL1)", "2.7 V (BODLEVEL2)", "2.85 V (BODLEVEL3)"}, [2]string{"128 Hz", "32 Hz"})...)
	if len(d) >= 3 {
		o := uint64(d[2])
		cs := "reserved"
		switch pick(o, 0, 4) {
		case 0:
			cs = "the internal high-frequency oscillator (OSCHF)"
		case 1:
			cs = "the 32.768 kHz internal oscillator (OSC32K)"
		}
		out = append(out, field("CLKSEL", 2, o, 0, 4, "clock at reset: "+cs))
	}
	if len(d) >= 6 {
		s := uint64(d[5])
		out = append(out,
			optBit("EESAVE", 5, s, 0, "the EEPROM is kept through a chip erase", "chip erase clears the EEPROM"),
			optBit("RSTPINCFG", 5, s, 3, "the reset pin is RESET", "the reset pin is a GPIO"),
			optBit("UPDIPINCFG", 5, s, 4, "the UPDI pin is UPDI (AVR DD/DU)", "the UPDI pin is a GPIO (AVR DD/DU)"),
			optBit("CRCSEL", 5, s, 5, "CRC32", "CRC16"),
			field("CRCSRC", 5, s, 6, 2, crcSrc(pick(s, 6, 2))))
	}
	out = append(out, updiSUT(d)...)
	if len(d) >= 9 {
		a, b := uint64(d[7]), uint64(d[8])
		out = append(out,
			field("CODESIZE", 7, a, 0, 8, sectionSize("boot and application code", a)),
			field("BOOTSIZE", 8, b, 0, 8, sectionSize("boot", b)))
	}
	return out
}

func sectionSize(name string, v uint64) string {
	if v == 0 {
		return name + " sections to the end of the flash"
	}
	return fmt.Sprintf("%s section: %d pages of 512 bytes (0x%X bytes)", name, v, v*512)
}

// ---- XMEGA ----

// decodeXMEGAFuses decodes FUSEBYTE0..5 (FUSEBYTE3 is reserved)
func decodeXMEGAFuses(d []byte) []ConfigField {
	per := func(v uint64) string {
		if v > 0xA {
			return "reserved"
		}
		ms := 8 << v
		if v >= 4 {
			ms = 125 << (v - 4)
		}
		return fmt.Sprintf("%d cycles of 1 kHz (~%d ms)", 8<<v, ms)
	}
	out := []ConfigField{{Name: "JTAGUSERID", Off: 0, Bits: 8, Value: uint64(d[0]), Meaning: "JTAG user ID"}}
	if len(d) >= 2 {
		w := uint64(d[1])
		out = append(out,
			field("WDPER", 1, w, 0, 4, "watchdog period at reset: "+per(pick(w, 0, 4))),
			field("WDWPER", 1, w, 4, 4, "watchdog window period at reset: "+per(pick(w, 4, 4))))
	}
	bodm := []string{"reserved", "sampled", "enabled continuously", "disabled"}
	if len(d) >= 3 {
		b := uint64(d[2])
		out = append(out,
			field("BODPD", 2, b, 0, 2, "BOD in power-down: "+bodm[pick(b, 0, 2)]),
			optBit("TOSCSEL", 2, b, 5, "TOSC1/2 on the default pins", "TOSC1/2 on the alternate pins (where there are)"),
			optBit("BOOTRST", 2, b, 6, "reset jumps to the application section", "reset jumps to the boot loader section"))
	}
	if len(d) >= 5 {
		f := uint64(d[4])
		st := []string{"64 ms", "4 ms", "reserved", "0 ms"}
		out = append(out,
			optBit("JTAGEN", 4, f, 0, "JTAG disabled", "JTAG enabled"),
			optBit("WDLOCK", 4, f, 1, "the watchdog is not locked", "the watchdog is locked (WDT and WDTCFG cannot be changed)"),
			field("STARTUPTIME", 4, f, 2, 2, "start-up time "+st[pick(f, 2, 2)]),
			optBit("RSTDISBL", 4, f, 4, "the reset pin resets", "the reset pin is disabled"))
	}
	if len(d) >= 6 {
		f := uint64(d[5])
		lv := []string{"3.0 V", "2.8 V", "2.6 V", "2.4 V", "2.2 V", "2.0 V", "1.8 V", "1.6 V"}
		out = append(out,
			field("BODLEVEL", 5, f, 0, 3, "BOD level "+lv[pick(f, 0, 3)]),
			optBit("EESAVE", 5, f, 3, "chip erase clears the EEPROM", "the EEPROM is kept through a chip erase"),
			field("BODACT", 5, f, 4, 2, "BOD in active mode: "+bodm[pick(f, 4, 2)]))
	}
	return out
}

// ---- ESP32-S2/S3/C3/C6 ----

// espBlk0 is where the fields of eFuse block 0 of a chip are: the words are
// those of espefuse's BLOCK0 dump, EFUSE_RD_WR_DIS_REG then
// EFUSE_RD_REPEAT_DATA0..4 (word w at byte 4*w)
type espBit struct {
	name       string
	word, bit  int
	n          int
	one, other string // what a 1 (any non-zero) and a 0 mean
}

var (
	espJTAGC = []espBit{
		{"DIS_ICACHE", 1, 8, 1, "the instruction cache is disabled", ""},
		{"DIS_USB_JTAG", 1, 9, 1, "the USB-to-JTAG function is DISABLED", "USB-to-JTAG enabled"},
		{"DIS_DOWNLOAD_ICACHE", 1, 10, 1, "the cache is disabled in download mode", ""},
		{"DIS_USB_SERIAL_JTAG", 1, 11, 1, "the USB-Serial-JTAG controller is disabled", "USB-Serial-JTAG enabled"},
		{"DIS_FORCE_DOWNLOAD", 1, 12, 1, "forcing download mode by software is disabled", ""},
		{"DIS_TWAI", 1, 14, 1, "the TWAI (CAN) controller is disabled", ""},
		{"JTAG_SEL_ENABLE", 1, 15, 1, "JTAG source is chosen by a strapping pin", ""},
		{"SOFT_DIS_JTAG", 1, 16, 3, "JTAG is disabled until re-enabled by HMAC (odd number of bits)", ""},
		{"DIS_PAD_JTAG", 1, 19, 1, "JTAG on the pads is DISABLED for ever", "pad JTAG enabled"},
		{"DIS_DOWNLOAD_MANUAL_ENCRYPT", 1, 20, 1, "flash encryption in download mode is disabled", ""},
	}
	espBlk0Common = []espBit{
		{"WDT_DELAY_SEL", 2, 16, 2, "", ""},
		{"SECURE_BOOT_KEY_REVOKE0", 2, 21, 1, "secure boot key digest 0 revoked", ""},
		{"SECURE_BOOT_KEY_REVOKE1", 2, 22, 1, "secure boot key digest 1 revoked", ""},
		{"SECURE_BOOT_KEY_REVOKE2", 2, 23, 1, "secure boot key digest 2 revoked", ""},
		{"SECURE_BOOT_EN", 3, 20, 1, "secure boot v2 ENABLED", "secure boot off"},
		{"SECURE_BOOT_AGGRESSIVE_REVOKE", 3, 21, 1, "aggressive revocation of the secure boot keys", ""},
		{"DIS_DOWNLOAD_MODE", 4, 0, 1, "all download boot modes are DISABLED", "download mode allowed"},
		{"ENABLE_SECURITY_DOWNLOAD", 4, 5, 1, "only the secure download mode is allowed", ""},
	}
	espS2 = []espBit{
		{"DIS_RTC_RAM_BOOT", 1, 7, 1, "boot from the RTC RAM is disabled", ""},
		{"DIS_ICACHE", 1, 8, 1, "the instruction cache is disabled", ""},
		{"DIS_DCACHE", 1, 9, 1, "the data cache is disabled", ""},
		{"DIS_DOWNLOAD_ICACHE", 1, 10, 1, "the instruction cache is disabled in download mode", ""},
		{"DIS_DOWNLOAD_DCACHE", 1, 11, 1, "the data cache is disabled in download mode", ""},
		{"DIS_FORCE_DOWNLOAD", 1, 12, 1, "forcing download mode by software is disabled", ""},
		{"DIS_USB", 1, 13, 1, "the USB OTG is disabled", ""},
		{"DIS_TWAI", 1, 14, 1, "the TWAI (CAN) controller is disabled", ""},
		{"DIS_BOOT_REMAP", 1, 15, 1, "", ""},
		{"SOFT_DIS_JTAG", 1, 17, 1, "JTAG is disabled until re-enabled by HMAC", ""},
		{"HARD_DIS_JTAG", 1, 18, 1, "JTAG is DISABLED for ever", "JTAG enabled"},
		{"DIS_DOWNLOAD_MANUAL_ENCRYPT", 1, 19, 1, "flash encryption in download mode is disabled", ""},
		{"DIS_LEGACY_SPI_BOOT", 4, 1, 1, "legacy SPI boot is disabled", ""},
		{"UART_PRINT_CHANNEL", 4, 2, 1, "ROM messages on UART1", "ROM messages on UART0"},
		{"DIS_USB_DOWNLOAD_MODE", 4, 4, 1, "download over USB is disabled", ""},
		{"SECURE_VERSION", 4, 11, 16, "", ""},
	}
	espS3 = []espBit{
		{"DIS_ICACHE", 1, 8, 1, "the instruction cache is disabled", ""},
		{"DIS_DCACHE", 1, 9, 1, "the data cache is disabled", ""},
		{"DIS_DOWNLOAD_ICACHE", 1, 10, 1, "the instruction cache is disabled in download mode", ""},
		{"DIS_DOWNLOAD_DCACHE", 1, 11, 1, "the data cache is disabled in download mode", ""},
		{"DIS_FORCE_DOWNLOAD", 1, 12, 1, "forcing download mode by software is disabled", ""},
		{"DIS_USB_OTG", 1, 13, 1, "the USB OTG is disabled", ""},
		{"DIS_TWAI", 1, 14, 1, "the TWAI (CAN) controller is disabled", ""},
		{"DIS_APP_CPU", 1, 15, 1, "the APP CPU is disabled", ""},
		{"SOFT_DIS_JTAG", 1, 16, 3, "JTAG is disabled until re-enabled by HMAC (odd number of bits)", ""},
		{"DIS_PAD_JTAG", 1, 19, 1, "JTAG on the pads is DISABLED for ever", "pad JTAG enabled"},
		{"DIS_DOWNLOAD_MANUAL_ENCRYPT", 1, 20, 1, "flash encryption in download mode is disabled", ""},
		{"DIS_USB_JTAG", 3, 22, 1, "the USB-to-JTAG function is DISABLED", "USB-to-JTAG enabled"},
		{"DIS_USB_SERIAL_JTAG", 3, 23, 1, "the USB-Serial-JTAG controller is disabled", "USB-Serial-JTAG enabled"},
		{"STRAP_JTAG_SEL", 3, 24, 1, "JTAG source is chosen by GPIO3", ""},
		{"USB_PHY_SEL", 3, 25, 1, "internal PHY for USB OTG, external for USB-Serial-JTAG", "internal PHY for USB-Serial-JTAG"},
		{"DIS_DIRECT_BOOT", 4, 1, 1, "direct boot (legacy SPI boot) is disabled", ""},
		{"DIS_USB_SERIAL_JTAG_ROM_PRINT", 4, 2, 1, "no ROM messages on USB-Serial-JTAG", ""},
		{"DIS_USB_SERIAL_JTAG_DOWNLOAD_MODE", 4, 4, 1, "download over USB-Serial-JTAG is disabled", ""},
		{"FLASH_TYPE", 4, 9, 1, "8-line flash", "4-line flash"},
		{"FLASH_ECC_EN", 4, 12, 1, "flash ECC enabled", ""},
		{"SECURE_VERSION", 4, 14, 16, "", ""},
		{"DIS_USB_OTG_DOWNLOAD_MODE", 4, 31, 1, "download over USB OTG is disabled", ""},
	}
	espC3 = append(append([]espBit{{"DIS_RTC_RAM_BOOT", 1, 7, 1, "boot from the RTC RAM is disabled", ""}}, espJTAGC...),
		espBit{"DIS_DIRECT_BOOT", 4, 1, 1, "direct boot (legacy SPI boot) is disabled", ""},
		espBit{"DIS_USB_SERIAL_JTAG_ROM_PRINT", 4, 2, 1, "no ROM messages on USB-Serial-JTAG", ""},
		espBit{"DIS_USB_SERIAL_JTAG_DOWNLOAD_MODE", 4, 4, 1, "download over USB-Serial-JTAG is disabled", ""},
		espBit{"SECURE_VERSION", 4, 14, 16, "", ""})
	espC6 = append(append([]espBit{{"SWAP_UART_SDIO_EN", 1, 7, 1, "the UART and SDIO pins are swapped", ""}}, espJTAGC...),
		espBit{"DIS_DIRECT_BOOT", 4, 1, 1, "direct boot (legacy SPI boot) is disabled", ""},
		espBit{"DIS_USB_SERIAL_JTAG_ROM_PRINT", 4, 2, 1, "no ROM messages on USB-Serial-JTAG", ""},
		espBit{"DIS_USB_SERIAL_JTAG_DOWNLOAD_MODE", 4, 4, 1, "download over USB-Serial-JTAG is disabled", ""},
		espBit{"SECURE_VERSION", 4, 9, 16, "", ""})
)

var espKeyPurposes = []string{"USER", "RESERVED", "XTS_AES_256_KEY_1 (ESP32-S2/S3)", "XTS_AES_256_KEY_2 (ESP32-S2/S3)", "XTS_AES_128_KEY",
	"HMAC_DOWN_ALL", "HMAC_DOWN_JTAG", "HMAC_DOWN_DIGITAL_SIGNATURE", "HMAC_UP",
	"SECURE_BOOT_DIGEST0", "SECURE_BOOT_DIGEST1", "SECURE_BOOT_DIGEST2"}

func decodeESPEfuse(d []byte, chip []espBit, printPin string) []ConfigField {
	w := func(i int) uint64 { return uint64(le32(d[4*i:])) }
	f := func(name string, word, bit, n int, meaning string) ConfigField {
		return field(name, int64(4*word), w(word), bit, n, meaning)
	}
	cnt := pick(w(2), 18, 3)
	out := []ConfigField{
		f("WR_DIS", 0, 0, 32, "write disable bits of eFuse fields"),
		f("RD_DIS", 1, 0, 7, "read disable of the key blocks 4-10 (BLOCK_KEY0..5, BLOCK_SYS_DATA2)"),
		f("SPI_BOOT_CRYPT_CNT", 2, 18, 3, choose(bits.OnesCount64(cnt)%2 == 1,
			"flash encryption ENABLED (an odd number of bits)", "flash encryption disabled (an even number of bits)")),
	}
	for i := 0; i < 6; i++ {
		word, bit := 2, 24+4*i
		if i >= 2 {
			word, bit = 3, 4*(i-2)
		}
		p := pick(w(word), bit, 4)
		m := "reserved"
		if int(p) < len(espKeyPurposes) {
			m = espKeyPurposes[p]
		}
		out = append(out, f(fmt.Sprintf("KEY_PURPOSE_%d", i), word, bit, 4, "BLOCK_KEY"+fmt.Sprint(i)+": "+m))
	}
	tpuw := pick(w(3), 28, 4)
	out = append(out, f("FLASH_TPUW", 3, 28, 4, fmt.Sprintf("the ROM waits %d ms after the flash power-up", tpuw)))
	upc := []string{"ROM messages always on", "ROM messages on when " + printPin + " is low at reset",
		"ROM messages on when " + printPin + " is high at reset", "ROM messages always off"}
	out = append(out, f("UART_PRINT_CONTROL", 4, 6, 2, upc[pick(w(4), 6, 2)]))
	for _, b := range append(append([]espBit{}, espBlk0Common...), chip...) {
		v := pick(w(b.word), b.bit, b.n)
		m := b.other
		if v != 0 {
			m = b.one
		}
		if b.name == "SOFT_DIS_JTAG" && b.n > 1 {
			m = choose(bits.OnesCount64(v)%2 == 1, b.one, "")
		}
		out = append(out, f(b.name, b.word, b.bit, b.n, m))
	}
	return out
}

func decodeESP32S2Efuse(d []byte) []ConfigField { return decodeESPEfuse(d, espS2, "GPIO46") }
func decodeESP32S3Efuse(d []byte) []ConfigField { return decodeESPEfuse(d, espS3, "GPIO46") }
func decodeESP32C3Efuse(d []byte) []ConfigField { return decodeESPEfuse(d, espC3, "GPIO8") }
func decodeESP32C6Efuse(d []byte) []ConfigField { return decodeESPEfuse(d, espC6, "GPIO8") }

// ---- the kind for a device ----

// ConfigKindFor returns the configuration area kind that fits a device:
// its name ("STM32L476RG", "ATtiny85", "AVR128DA48", "ESP32-S3") or, when
// the name tells nothing, its family ("STM32L4"); "" if unknown
func ConfigKindFor(deviceName, family string) string {
	if k := configKindForName(deviceName); k != "" {
		return k
	}
	return configKindForName(family)
}

func configKindForName(name string) string {
	n := strings.ToUpper(strings.TrimSpace(name))
	n = strings.NewReplacer("-", "", "_", "", " ", "").Replace(n)
	switch {
	case n == "":
		return ""
	case strings.HasPrefix(n, "ESP32"):
		r := n[5:]
		switch {
		case strings.HasPrefix(r, "S2"):
			return "esp32s2-efuse"
		case strings.HasPrefix(r, "S3"):
			return "esp32s3-efuse"
		case strings.HasPrefix(r, "C3"):
			return "esp32c3-efuse"
		case strings.HasPrefix(r, "C6"):
			return "esp32c6-efuse"
		case r == "" || strings.HasPrefix(r, "D") || strings.HasPrefix(r, "U") || strings.HasPrefix(r, "PICO") ||
			strings.HasPrefix(r, "WROOM") || strings.HasPrefix(r, "WROVER") || strings.HasPrefix(r, "SOLO"):
			return "esp32-efuse"
		}
		return ""
	case strings.HasPrefix(n, "STM32"):
		r := n[5:]
		for _, p := range []struct{ prefix, kind string }{
			{"F0", "stm32f0-option-bytes"}, {"F3", "stm32f0-option-bytes"},
			{"F1", "stm32f1-option-bytes"},
			{"F2", "stm32f4-option-bytes"}, {"F4", "stm32f4-option-bytes"},
			{"F7", "stm32f7-option-bytes"},
			{"L0", "stm32l0-option-bytes"}, {"L1", "stm32l0-option-bytes"},
			{"L4", "stm32l4-option-bytes"},
			{"G0", "stm32g0-option-bytes"},
			{"G4", "stm32g4-option-bytes"},
			{"H7", "stm32h7-option-bytes"},
		} {
			if strings.HasPrefix(r, p.prefix) {
				return p.kind
			}
		}
		if strings.HasPrefix(r, "WB") && !strings.HasPrefix(r, "WBA") {
			return "stm32wb-option-bytes"
		}
		return ""
	case strings.HasPrefix(n, "ATXMEGA"):
		return "xmega-fuses"
	case strings.HasPrefix(n, "AVR"):
		// AVR128DA48, AVR64DD32: digits then the family letters
		r := strings.TrimLeft(n[3:], "0123456789")
		if len(r) >= 2 && r[0] == 'D' && len(r) < len(n)-3 {
			return "avr-dx-fuses"
		}
		return ""
	case strings.HasPrefix(n, "ATTINY"):
		num := leadingDigits(n[6:])
		switch {
		case num == "13":
			return "attiny13-fuses"
		case num == "25" || num == "45" || num == "85" || num == "24" || num == "44" || num == "84" ||
			num == "261" || num == "461" || num == "861":
			return "attiny-fuses"
		case tinyAVR012(num):
			return "avr-tiny01-fuses"
		}
		return ""
	case strings.HasPrefix(n, "ATMEGA"):
		num := leadingDigits(n[6:])
		rest := n[6+len(num):]
		switch {
		case (num == "16" || num == "32") && strings.HasPrefix(rest, "U4"):
			return "atmega32u4-fuses"
		case num == "640" || num == "1280" || num == "1281" || num == "2560" || num == "2561" ||
			num == "644" || num == "1284":
			return "atmega2560-fuses"
		case num == "48" || num == "88" || num == "168" || num == "328":
			return "avr-fuses"
		case len(num) >= 3 && (num[len(num)-1] == '8' || num[len(num)-1] == '9') && num[len(num)-2] == '0' &&
			(num[:len(num)-2] == "8" || num[:len(num)-2] == "16" || num[:len(num)-2] == "32" || num[:len(num)-2] == "48"):
			return "avr-tiny01-fuses" // megaAVR 0-series: ATmega808..4809
		}
		return ""
	}
	return ""
}

func leadingDigits(s string) string {
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	return s[:i]
}

// tinyAVR012 tells the tinyAVR 0/1/2-series numbers: flash KiB (2, 4, 8,
// 16, 32), the series (0, 1, 2), the pins (2 - 8, 4 - 14, 6 - 20, 7 - 24)
func tinyAVR012(num string) bool {
	if len(num) < 3 {
		return false
	}
	pins, series, kb := num[len(num)-1], num[len(num)-2], num[:len(num)-2]
	return strings.ContainsRune("2467", rune(pins)) && strings.ContainsRune("012", rune(series)) &&
		(kb == "2" || kb == "4" || kb == "8" || kb == "16" || kb == "32")
}
