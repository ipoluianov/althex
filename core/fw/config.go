package fw

import (
	"fmt"
	"math/bits"
	"strings"
)

// ConfigField is a field of a configuration area: option bytes, fuses,
// eFuses (FW-11)
type ConfigField struct {
	Name    string
	Off     int64 // of the byte (or the word) the field is in
	Bit     int   // the lowest bit of the field in it
	Bits    int
	Value   uint64
	Meaning string
}

type configDecoder struct {
	kind, title string
	minLen      int
	decode      func([]byte) []ConfigField
}

var configDecoders = []configDecoder{
	{"stm32f1-option-bytes", "STM32F1 option bytes (16 bytes at 0x1FFFF800)", 16, decodeSTM32F1},
	{"stm32f4-option-bytes", "STM32F2/F4 option bytes (16 bytes at 0x1FFFC000)", 2, decodeSTM32F4},
	{"avr-fuses", "AVR fuses of ATmega328P-like parts: low, high, extended", 1, decodeAVRFuses},
	{"avr-lock", "AVR lock bits (1 byte)", 1, decodeAVRLock},
	{"esp32-efuse", "ESP32 eFuse block 0 (7 words, EFUSE_BLK0_RDATA0..6)", 28, decodeESP32Efuse},
	{"stm32f0-option-bytes", "STM32F0/F3 option bytes (16 bytes at 0x1FFFF800)", 4, decodeSTM32F0},
	{"stm32f7-option-bytes", "STM32F7 FLASH_OPTCR, then FLASH_OPTCR1 (32-bit words)", 4, decodeSTM32F7},
	{"stm32l0-option-bytes", "STM32L0/L1 option bytes (words with complements at 0x1FF80000)", 8, decodeSTM32L0},
	{"stm32l4-option-bytes", "STM32L4 FLASH_OPTR (option bytes at 0x1FFF7800)", 4, decodeSTM32L4},
	{"stm32g0-option-bytes", "STM32G0 FLASH_OPTR (option bytes at 0x1FFF7800)", 4, decodeSTM32G0},
	{"stm32g4-option-bytes", "STM32G4 FLASH_OPTR (option bytes at 0x1FFF7800)", 4, decodeSTM32G4},
	{"stm32wb-option-bytes", "STM32WB FLASH_OPTR (option bytes at 0x1FFF8000)", 4, decodeSTM32WB},
	{"stm32h7-option-bytes", "STM32H7 FLASH_OPTSR_CUR (32-bit word)", 4, decodeSTM32H7},
	{"attiny-fuses", "ATtiny25/45/85, 24/44/84, 261/461/861 fuses: low, high, extended", 1, decodeATtinyFuses},
	{"attiny13-fuses", "ATtiny13/13A fuses: low, high", 1, decodeATtiny13Fuses},
	{"atmega32u4-fuses", "ATmega16U4/32U4 fuses: low, high, extended", 1, decodeATmega32U4Fuses},
	{"atmega2560-fuses", "ATmega640/1280/2560, 644/1284 fuses: low, high, extended", 1, decodeATmega2560Fuses},
	{"avr-tiny01-fuses", "tinyAVR 0/1/2, megaAVR 0 fuses (FUSE.WDTCFG..BOOTEND, 9 bytes)", 1, decodeTinyAVR01Fuses},
	{"avr-dx-fuses", "AVR DA/DB/DD/DU fuses (FUSE.WDTCFG..BOOTSIZE, 9 bytes)", 1, decodeAVRDxFuses},
	{"xmega-fuses", "XMEGA fuses: FUSEBYTE0..5", 1, decodeXMEGAFuses},
	{"esp32s2-efuse", "ESP32-S2 eFuse block 0 (6 words: RD_WR_DIS, RD_REPEAT_DATA0..4)", 24, decodeESP32S2Efuse},
	{"esp32s3-efuse", "ESP32-S3 eFuse block 0 (6 words: RD_WR_DIS, RD_REPEAT_DATA0..4)", 24, decodeESP32S3Efuse},
	{"esp32c3-efuse", "ESP32-C3 eFuse block 0 (6 words: RD_WR_DIS, RD_REPEAT_DATA0..4)", 24, decodeESP32C3Efuse},
	{"esp32c6-efuse", "ESP32-C6 eFuse block 0 (6 words: RD_WR_DIS, RD_REPEAT_DATA0..4)", 24, decodeESP32C6Efuse},
}

// ConfigKinds returns the kinds DecodeConfig knows
func ConfigKinds() []string {
	out := make([]string, len(configDecoders))
	for i, d := range configDecoders {
		out[i] = d.kind
	}
	return out
}

// ConfigTitle returns what a kind of configuration area is; "" if unknown
func ConfigTitle(kind string) string {
	for _, d := range configDecoders {
		if d.kind == kind {
			return d.title
		}
	}
	return ""
}

// DecodeConfig decodes a configuration area of a kind of ConfigKinds
func DecodeConfig(kind string, data []byte) ([]ConfigField, error) {
	for _, d := range configDecoders {
		if d.kind == kind {
			if len(data) < d.minLen {
				return nil, fmt.Errorf("%s: %d bytes, at least %d are needed", kind, len(data), d.minLen)
			}
			return d.decode(data), nil
		}
	}
	return nil, fmt.Errorf("unknown configuration area %q", kind)
}

func field(name string, off int64, b uint64, bit, n int, meaning string) ConfigField {
	return ConfigField{Name: name, Off: off, Bit: bit, Bits: n, Value: b >> uint(bit) & (1<<uint(n) - 1), Meaning: meaning}
}

func pick(v uint64, bit, n int) uint64 { return v >> uint(bit) & (1<<uint(n) - 1) }

// ---- STM32F1 ----

func decodeSTM32F1(d []byte) []ConfigField {
	var out []ConfigField
	comp := func(name string, i int) {
		v, nv := d[i], d[i+1]
		m := "complement OK"
		if v^nv != 0xFF {
			m = fmt.Sprintf("complement mismatch (0x%02X and 0x%02X): the option bytes are invalid, the defaults apply and OPTERR is set", v, nv)
		}
		out = append(out, ConfigField{Name: "n" + name, Off: int64(i + 1), Bits: 8, Value: uint64(nv), Meaning: m})
	}
	rdp := uint64(d[0])
	m := "read protection on (any value but 0xA5)"
	if rdp == 0xA5 {
		m = "no read protection (0xA5)"
	}
	out = append(out, ConfigField{Name: "RDP", Off: 0, Bits: 8, Value: rdp, Meaning: m})
	comp("RDP", 0)
	u := uint64(d[2])
	out = append(out,
		field("WDG_SW", 2, u, 0, 1, choose(u&1 != 0, "software watchdog", "hardware watchdog (started at reset)")),
		field("nRST_STOP", 2, u, 1, 1, choose(u&2 != 0, "no reset when entering Stop mode", "reset when entering Stop mode")),
		field("nRST_STDBY", 2, u, 2, 1, choose(u&4 != 0, "no reset when entering Standby mode", "reset when entering Standby mode")))
	comp("USER", 2)
	out = append(out, ConfigField{Name: "Data0", Off: 4, Bits: 8, Value: uint64(d[4]), Meaning: "user data byte"})
	comp("Data0", 4)
	out = append(out, ConfigField{Name: "Data1", Off: 6, Bits: 8, Value: uint64(d[6]), Meaning: "user data byte"})
	comp("Data1", 6)
	for i := 0; i < 4; i++ {
		off := 8 + 2*i
		v := uint64(d[off])
		out = append(out, ConfigField{Name: fmt.Sprintf("WRP%d", i), Off: int64(off), Bits: 8, Value: v,
			Meaning: wrpMeaning(v, 8, 8*i, "page group", "each bit protects 4 pages (2 KiB pages of high-density parts: 2 pages); bit 7 of WRP3 protects the rest of the flash")})
		comp(fmt.Sprintf("WRP%d", i), off)
	}
	return out
}

// wrpMeaning lists the protected units of active-low write protection bits
func wrpMeaning(v uint64, n, first int, unit, note string) string {
	var prot []string
	for i := 0; i < n; i++ {
		if v&(1<<uint(i)) == 0 {
			prot = append(prot, fmt.Sprint(first+i))
		}
	}
	s := "no write protection"
	if len(prot) > 0 {
		s = "write protected: " + unit + " " + strings.Join(prot, ", ")
	}
	return s + " (bits are active-low: 0 - protected; " + note + ")"
}

func choose(c bool, yes, no string) string {
	if c {
		return yes
	}
	return no
}

// ---- STM32F4 ----

func decodeSTM32F4(d []byte) []ConfigField {
	var out []ConfigField
	u := uint64(d[0])
	bor := []string{"BOR level 3 (2.70-3.60 V)", "BOR level 2 (2.40-2.70 V)", "BOR level 1 (2.10-2.40 V)", "BOR off (POR/PDR only)"}
	out = append(out,
		field("BOR_LEV", 0, u, 2, 2, bor[pick(u, 2, 2)]),
		field("WDG_SW", 0, u, 5, 1, choose(u&0x20 != 0, "software watchdog", "hardware watchdog (started at reset)")),
		field("nRST_STOP", 0, u, 6, 1, choose(u&0x40 != 0, "no reset when entering Stop mode", "reset when entering Stop mode")),
		field("nRST_STDBY", 0, u, 7, 1, choose(u&0x80 != 0, "no reset when entering Standby mode", "reset when entering Standby mode")))
	rdp := uint64(d[1])
	m := "level 1: read protection on (any value but 0xAA and 0xCC)"
	switch rdp {
	case 0xAA:
		m = "level 0: no read protection (0xAA)"
	case 0xCC:
		m = "level 2: the chip is protected for ever, debug disabled (0xCC)"
	}
	out = append(out, ConfigField{Name: "RDP", Off: 1, Bits: 8, Value: rdp, Meaning: m})
	if len(d) >= 10 {
		w := uint64(le16(d[8:]))
		sprmod := w&0x8000 != 0
		note := "sectors 0-11 of bank 1"
		if sprmod {
			out = append(out, field("nWRP", 8, w, 0, 12, wrpMeaningPCROP(w&0xFFF)))
		} else {
			out = append(out, field("nWRP", 8, w, 0, 12, wrpMeaning(w&0xFFF, 12, 0, "sector", note)))
		}
		out = append(out, field("SPRMOD", 8, w, 15, 1, choose(sprmod,
			"nWRP bits select PCROP (read protection of sectors, active-high)", "nWRP bits are write protection")))
	}
	return out
}

func wrpMeaningPCROP(v uint64) string {
	var prot []string
	for i := 0; i < 12; i++ {
		if v&(1<<uint(i)) != 0 {
			prot = append(prot, fmt.Sprint(i))
		}
	}
	if len(prot) == 0 {
		return "no PCROP sectors"
	}
	return "PCROP read protected sectors " + strings.Join(prot, ", ")
}

// ---- AVR ----

const avrActiveLow = " (fuse bits are active-low: 0 - programmed)"

func prog(v uint64, bit int) bool { return v&(1<<uint(bit)) == 0 }

func decodeAVRFuses(d []byte) []ConfigField {
	var out []ConfigField
	lo := uint64(d[0])
	out = append(out,
		field("CKSEL", 0, lo, 0, 4, ckselMeaning(pick(lo, 0, 4))+avrActiveLow),
		field("SUT", 0, lo, 4, 2, sutMeaning(pick(lo, 4, 2), pick(lo, 0, 4))),
		field("CKOUT", 0, lo, 6, 1, choose(prog(lo, 6), "programmed: the clock is output on PB0 (CLKO)", "unprogrammed: no clock output")),
		field("CKDIV8", 0, lo, 7, 1, choose(prog(lo, 7), "programmed: the clock is divided by 8 at start", "unprogrammed: no division")))
	if len(d) >= 2 {
		hi := uint64(d[1])
		bootsz := []string{"2048 words, boot section from 0x3800 (byte 0x7000)", "1024 words, from 0x3C00 (byte 0x7800)",
			"512 words, from 0x3E00 (byte 0x7C00)", "256 words, from 0x3F00 (byte 0x7E00)"}
		out = append(out,
			field("BOOTRST", 1, hi, 0, 1, choose(prog(hi, 0), "programmed: reset jumps to the boot loader section", "unprogrammed: reset jumps to 0x0000")),
			field("BOOTSZ", 1, hi, 1, 2, "boot section size of the ATmega328P: "+bootsz[pick(hi, 1, 2)]+avrActiveLow),
			field("EESAVE", 1, hi, 3, 1, choose(prog(hi, 3), "programmed: the EEPROM is kept through a chip erase", "unprogrammed: chip erase clears the EEPROM")),
			field("WDTON", 1, hi, 4, 1, choose(prog(hi, 4), "programmed: the watchdog is always on", "unprogrammed: the watchdog is enabled by software")),
			field("SPIEN", 1, hi, 5, 1, choose(prog(hi, 5), "programmed: serial (SPI) programming enabled", "unprogrammed: serial programming DISABLED")),
			field("DWEN", 1, hi, 6, 1, choose(prog(hi, 6), "programmed: debugWIRE enabled (the reset pin is debugWIRE)", "unprogrammed: debugWIRE off")),
			field("RSTDISBL", 1, hi, 7, 1, choose(prog(hi, 7), "programmed: the reset pin is an I/O pin; ISP is no longer possible", "unprogrammed: the reset pin resets")))
	}
	if len(d) >= 3 {
		ex := uint64(d[2])
		bod := map[uint64]string{7: "brown-out detection disabled", 6: "BOD at 1.8 V", 5: "BOD at 2.7 V", 4: "BOD at 4.3 V"}
		m, ok := bod[pick(ex, 0, 3)]
		if !ok {
			m = "reserved value"
		}
		out = append(out, field("BODLEVEL", 2, ex, 0, 3, m+avrActiveLow))
	}
	for i := range out {
		if out[i].Bits == 1 {
			out[i].Meaning += avrActiveLow
		}
	}
	return out
}

func ckselMeaning(v uint64) string {
	switch {
	case v == 0:
		return "external clock"
	case v == 1:
		return "reserved"
	case v == 2:
		return "calibrated internal RC oscillator, 8 MHz"
	case v == 3:
		return "internal 128 kHz RC oscillator"
	case v <= 5:
		return "low frequency crystal oscillator"
	case v <= 7:
		return "full swing crystal oscillator"
	}
	r := []string{"0.4-0.9 MHz", "0.9-3 MHz", "3-8 MHz", "8-16 MHz"}[(v-8)/2]
	return "low power crystal oscillator, " + r
}

func sutMeaning(sut, cksel uint64) string {
	m := "start-up time"
	if cksel == 2 || cksel == 3 || cksel == 0 {
		m = []string{"start-up 6 CK, +14 CK (BOD enabled)", "start-up 6 CK, +14 CK + 4.1 ms (fast rising power)",
			"start-up 6 CK, +14 CK + 65 ms (slowly rising power)", "reserved"}[sut]
	}
	return m + avrActiveLow
}

func decodeAVRLock(d []byte) []ConfigField {
	v := uint64(d[0])
	lb := map[uint64]string{3: "no memory lock", 2: "further programming of the flash and the EEPROM is disabled",
		1: "reserved", 0: "programming and verification of the flash and the EEPROM are disabled"}
	blb0 := map[uint64]string{3: "no restriction for SPM or LPM on the application section",
		2: "SPM may not write the application section",
		0: "SPM may not write the application section; LPM from the boot section may not read it, interrupts in the boot section are off when the vectors are in the application",
		1: "LPM from the boot section may not read the application section; interrupts in the boot section are off when the vectors are in the application"}
	blb1 := map[uint64]string{3: "no restriction for SPM or LPM on the boot section",
		2: "SPM may not write the boot section",
		0: "SPM may not write the boot section; LPM from the application section may not read it, interrupts in the application section are off when the vectors are in the boot section",
		1: "LPM from the application section may not read the boot section; interrupts in the application section are off when the vectors are in the boot section"}
	note := " (lock bits are active-low: 0 - programmed)"
	return []ConfigField{
		field("LB", 0, v, 0, 2, lb[pick(v, 0, 2)]+note),
		field("BLB0", 0, v, 2, 2, blb0[pick(v, 2, 2)]+note),
		field("BLB1", 0, v, 4, 2, blb1[pick(v, 4, 2)]+note),
	}
}

// ---- ESP32 ----

func decodeESP32Efuse(d []byte) []ConfigField {
	w := func(i int) uint64 { return uint64(le32(d[4*i:])) }
	f := func(name string, word, bit, n int, meaning string) ConfigField {
		return field(name, int64(4*word), w(word), bit, n, meaning)
	}
	var out []ConfigField
	w0, w1, w2, w3, w5, w6 := w(0), w(1), w(2), w(3), w(5), w(6)
	cnt := pick(w0, 20, 7)
	enc := bits.OnesCount64(cnt)%2 == 1
	out = append(out,
		f("WR_DIS", 0, 0, 16, "write disable bits of eFuse fields"),
		f("RD_DIS", 0, 16, 4, "read disable of blocks 1-3 (keys)"),
		f("FLASH_CRYPT_CNT", 0, 20, 7, choose(enc, "flash encryption ENABLED (an odd number of bits)", "flash encryption disabled (an even number of bits)")),
		f("UART_DOWNLOAD_DIS", 0, 27, 1, choose(w0>>27&1 != 0, "UART download mode disabled", "UART download mode allowed")))
	mac := []byte{byte(w2 >> 8), byte(w2), byte(w1 >> 24), byte(w1 >> 16), byte(w1 >> 8), byte(w1)}
	out = append(out, ConfigField{Name: "MAC", Off: 4, Bits: 48, Value: uint64(w2&0xFFFF)<<32 | w1,
		Meaning: fmt.Sprintf("factory MAC %02x:%02x:%02x:%02x:%02x:%02x", mac[0], mac[1], mac[2], mac[3], mac[4], mac[5])})
	crc := esp32MacCRC(mac)
	out = append(out, f("MAC_CRC", 2, 16, 8, choose(uint64(crc) == pick(w2, 16, 8), "CRC-8 of the MAC OK", fmt.Sprintf("CRC-8 of the MAC should be 0x%02X", crc))))
	rev := 0
	if w3>>15&1 != 0 {
		rev = 1
		if w5>>20&1 != 0 {
			rev = 2 // or 3, told by APB_CTRL_DATE
		}
	}
	pkg := pick(w3, 9, 3) | pick(w3, 2, 1)<<3
	pkgs := map[uint64]string{0: "ESP32-D0WDQ6", 1: "ESP32-D0WDQ5", 2: "ESP32-D2WDQ5", 4: "ESP32-U4WDH", 5: "ESP32-PICO-D4 / PICO-V3", 6: "ESP32-PICO-V3-02", 7: "ESP32-D0WDR2-V3"}
	pk, ok := pkgs[pkg]
	if !ok {
		pk = "unknown package"
	}
	out = append(out,
		f("CHIP_VER_DIS_APP_CPU", 3, 0, 1, choose(w3&1 != 0, "the APP CPU is disabled (single core)", "dual core")),
		f("CHIP_VER_DIS_BT", 3, 1, 1, choose(w3&2 != 0, "Bluetooth disabled", "Bluetooth present")),
		f("CHIP_PACKAGE", 3, 9, 3, pk),
		f("CHIP_CPU_FREQ_RATED", 3, 13, 1, choose(w3>>13&1 != 0, "the CPU frequency is rated: see CHIP_CPU_FREQ_LOW", "not rated")),
		f("CHIP_CPU_FREQ_LOW", 3, 12, 1, choose(w3>>12&1 != 0, "160 MHz", "240 MHz")),
		f("CHIP_VER_REV1", 3, 15, 1, fmt.Sprintf("chip revision v%d (with CHIP_VER_REV2; v3 is told by APB_CTRL_DATE)", rev)),
		f("CHIP_VER_REV2", 5, 20, 1, "second revision bit"),
		f("FLASH_CRYPT_CONFIG", 5, 28, 4, "which bits of the flash encryption key are tweaked (0xF - all)"),
		f("CODING_SCHEME", 6, 0, 2, []string{"none", "3/4 coding", "repeat (reserved)", "none (reserved)"}[pick(w6, 0, 2)]),
		f("CONSOLE_DEBUG_DISABLE", 6, 2, 1, choose(w6&4 != 0, "the ROM BASIC console is disabled", "the ROM BASIC console is allowed")),
		f("DISABLE_SDIO_HOST", 6, 3, 1, ""),
		f("ABS_DONE_0", 6, 4, 1, choose(w6>>4&1 != 0, "secure boot v1 ENABLED", "secure boot v1 off")),
		f("ABS_DONE_1", 6, 5, 1, choose(w6>>5&1 != 0, "secure boot v2 ENABLED", "secure boot v2 off")),
		f("JTAG_DISABLE", 6, 6, 1, choose(w6>>6&1 != 0, "JTAG is DISABLED", "JTAG enabled")),
		f("DISABLE_DL_ENCRYPT", 6, 7, 1, choose(w6>>7&1 != 0, "flash encryption in download mode disabled", "")),
		f("DISABLE_DL_DECRYPT", 6, 8, 1, choose(w6>>8&1 != 0, "flash decryption in download mode disabled", "")),
		f("DISABLE_DL_CACHE", 6, 9, 1, choose(w6>>9&1 != 0, "flash cache in download mode disabled", "")),
		f("KEY_STATUS", 6, 10, 1, choose(w6>>10&1 != 0, "block 3 holds a key", "block 3 is free for user data")),
	)
	return out
}

// esp32MacCRC is the CRC-8 of the factory MAC (polynomial 0x8C reflected, as
// espefuse computes it)
func esp32MacCRC(mac []byte) byte {
	crc := byte(0)
	for _, b := range []byte{mac[5], mac[4], mac[3], mac[2], mac[1], mac[0]} {
		crc ^= b
		for i := 0; i < 8; i++ {
			if crc&1 != 0 {
				crc = crc>>1 ^ 0x8C
			} else {
				crc >>= 1
			}
		}
	}
	return crc
}
