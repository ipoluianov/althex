package fw

import (
	"strings"
	"testing"
)

type cfgWant struct {
	name  string
	value uint64
	sub   string // in the meaning
}

func checkConfig(t *testing.T, kind string, data []byte, wants []cfgWant) {
	t.Helper()
	fs, err := DecodeConfig(kind, data)
	if err != nil {
		t.Fatalf("%s: %v", kind, err)
	}
	for _, w := range wants {
		found := false
		for _, f := range fs {
			if f.Name == w.name {
				found = true
				if f.Value != w.value || !strings.Contains(f.Meaning, w.sub) {
					t.Errorf("%s %s: %+v, want 0x%X with %q", kind, w.name, f, w.value, w.sub)
				}
			}
		}
		if !found {
			t.Errorf("%s: no %s", kind, w.name)
		}
	}
}

func w32(vs ...uint32) []byte {
	b := make([]byte, 4*len(vs))
	for i, v := range vs {
		put32(b[4*i:], v)
	}
	return b
}

func TestConfigMoreKinds(t *testing.T) {
	seen := map[string]bool{}
	for _, k := range ConfigKinds() {
		if seen[k] || ConfigTitle(k) == "" {
			t.Fatalf("kind %q repeated or without a title", k)
		}
		seen[k] = true
		// short and empty data never panic
		for n := 0; n < 40; n++ {
			DecodeConfig(k, make([]byte, n))
		}
	}
	for _, k := range []string{"avr-fuses", "stm32f0-option-bytes", "stm32l4-option-bytes", "xmega-fuses", "esp32c6-efuse"} {
		if !seen[k] {
			t.Fatalf("no %s", k)
		}
	}
}

func TestConfigSTM32More(t *testing.T) {
	// F0 factory: RDP 0xAA, nRDP 0x55, USER 0xFF
	checkConfig(t, "stm32f0-option-bytes",
		[]byte{0xAA, 0x55, 0xFF, 0x00, 0xFF, 0x00, 0xFF, 0x00, 0xFF, 0x00, 0xFE, 0x01, 0xFF, 0x00, 0xFF, 0x00},
		[]cfgWant{
			{"RDP", 0xAA, "level 0"}, {"nRDP", 0x55, "complement OK"},
			{"WDG_SW", 1, "software"}, {"nBOOT1", 1, "system memory"},
			{"RAM_PARITY_CHECK", 1, "disabled"}, {"WRP0", 0xFF, "no write protection"},
			{"WRP1", 0xFE, "sector 8"}, {"nWRP1", 0x01, "OK"},
		})
	checkConfig(t, "stm32f0-option-bytes", []byte{0xBB, 0x55, 0x00, 0xFF},
		[]cfgWant{{"RDP", 0xBB, "level 1"}, {"nRDP", 0x55, "mismatch"}, {"WDG_SW", 0, "hardware"}})

	// F7 OPTCR reset 0xC0FFAAFD, OPTCR1 reset 0x00400080
	checkConfig(t, "stm32f7-option-bytes", w32(0xC0FFAAFD, 0x00400080), []cfgWant{
		{"BOR_LEV", 3, "off"}, {"RDP", 0xAA, "level 0"}, {"IWDG_SW", 1, "software"},
		{"nWRP", 0xFF, "sector 8, 9, 10, 11"}, {"IWDG_STOP", 1, "runs"},
		{"BOOT_ADD0", 0x80, "0x00200000"}, {"BOOT_ADD1", 0x40, "0x00100000"},
	})

	// L0 OPTR 0x807000AA in its half words with complements
	checkConfig(t, "stm32l0-option-bytes", w32(0xFF5500AA, 0x7F8F8070, 0xFFFE0001), []cfgWant{
		{"RDP", 0xAA, "level 0"}, {"nOPTR_LO", 0xFF55, "OK"}, {"BOR_LEV", 0, "off"},
		{"WDG_SW", 1, "software"}, {"nBOOT1", 1, "system memory"}, {"nOPTR_HI", 0x7F8F, "OK"},
		{"WRP1", 1, "sector 0 "}, {"nWRP1", 0xFFFE, "OK"},
	})
	checkConfig(t, "stm32l0-option-bytes", w32(0x0000CCCC, 0xFFF70008), []cfgWant{
		{"RDP", 0xCC, "level 2"}, {"nOPTR_LO", 0, "mismatch"}, {"BOR_LEV", 8, "level 1"},
	})

	// L4 OPTR reset 0xFFEFF8AA, its complement, WRP areas off
	l4 := make([]byte, 0x28)
	copy(l4, w32(0xFFEFF8AA, ^uint32(0xFFEFF8AA)))
	put32(l4[0x18:], 0xFF)
	put32(l4[0x20:], 0x00100002)
	checkConfig(t, "stm32l4-option-bytes", l4, []cfgWant{
		{"RDP", 0xAA, "level 0"}, {"BOR_LEV", 0, "level 0"}, {"nRST_STOP", 1, "no reset"},
		{"IWDG_SW", 1, "software"}, {"IWDG_STOP", 1, "runs"}, {"WWDG_SW", 1, "software"},
		{"BFB2", 0, "bank 1"}, {"DUALBANK", 1, "dual"}, {"nBOOT1", 1, "system memory"},
		{"SRAM2_PE", 1, "disabled"}, {"SRAM2_RST", 1, "not erased"},
		{"nSWBOOT0", 1, "pin"}, {"nBOOT0", 1, "BOOT0 = 0"},
		{"nOPTR", 0x00100755, "OK"},
		{"WRP1A_END", 0, "no write protection"}, {"WRP1B_END", 0x10, "pages 2 to 16"},
	})

	// G0 OPTR reset 0xFFFFFEAA
	checkConfig(t, "stm32g0-option-bytes", w32(0xFFFFFEAA), []cfgWant{
		{"RDP", 0xAA, "level 0"}, {"BOR_EN", 0, "off"}, {"BORF_LEV", 3, "2.8 V"}, {"BORR_LEV", 3, "2.9 V"},
		{"nRST_SHDW", 1, "no reset"}, {"RAM_PARITY_CHECK", 1, "disabled"},
		{"nBOOT_SEL", 1, "nBOOT0 option"}, {"nBOOT0", 1, "BOOT0 = 0"},
		{"NRST_MODE", 3, "bidirectional"}, {"IRHEN", 1, "low until"},
	})

	// G4: the L4-like value with NRST_MODE 3, IRHEN
	checkConfig(t, "stm32g4-option-bytes", w32(0xFFEFF8AA), []cfgWant{
		{"BOR_LEV", 0, "1.7 V"}, {"DBANK", 1, "dual bank"}, {"SRAM_PE", 1, "disabled"},
		{"CCMSRAM_RST", 1, "not erased"}, {"NRST_MODE", 3, "bidirectional"}, {"IRHEN", 1, "low until"},
	})

	// WB: ESE set, BOR level 4, nBOOT0 = 0 (BOOT0 = 1)
	checkConfig(t, "stm32wb-option-bytes", w32(0x37FFF9AA), []cfgWant{
		{"ESE", 1, "CPU2"}, {"BOR_LEV", 4, "2.8 V"}, {"nBOOT0", 0, "BOOT0 = 1"},
		{"nSWBOOT0", 1, "pin"}, {"SRAM2_RST", 1, "not erased"},
	})

	// H7: RDP 0xAA, BOR off, IWDG1 software, security, banks swapped
	checkConfig(t, "stm32h7-option-bytes", w32(0x8023AAD0), []cfgWant{
		{"RDP", 0xAA, "level 0"}, {"BOR_LEV", 0, "off"}, {"IWDG1_SW", 1, "software"},
		{"NRST_STOP_D1", 1, "no reset"}, {"NRST_STBY_D1", 1, "no reset"},
		{"FZ_IWDG_STOP", 1, "runs"}, {"FZ_IWDG_SDBY", 1, "runs"},
		{"SECURITY", 1, "ENABLED"}, {"IO_HSLV", 0, "off"}, {"SWAP_BANK_OPT", 1, "swapped"},
	})
}

func TestConfigAVRMore(t *testing.T) {
	// ATtiny85 defaults
	checkConfig(t, "attiny-fuses", []byte{0x62, 0xDF, 0xFF}, []cfgWant{
		{"CKSEL", 2, "8 MHz"}, {"SUT", 2, "65 ms"}, {"CKDIV8", 0, "divided by 8"}, {"CKOUT", 1, "no clock"},
		{"BODLEVEL", 7, "disabled"}, {"SPIEN", 0, "programmed: serial"}, {"RSTDISBL", 1, "unprogrammed"},
		{"DWEN", 1, "debugWIRE off"}, {"SELFPRGEN", 1, "disabled"},
	})
	checkConfig(t, "attiny-fuses", []byte{0xE1, 0x5C, 0xFE}, []cfgWant{
		{"CKSEL", 1, "PLL"}, {"BODLEVEL", 4, "4.3 V"}, {"RSTDISBL", 0, "I/O pin"}, {"SELFPRGEN", 0, "enabled"},
	})
	// ATtiny13A defaults L=0x6A H=0xFF: 9.6 MHz / 8
	checkConfig(t, "attiny13-fuses", []byte{0x6A, 0xFF}, []cfgWant{
		{"CKSEL", 2, "9.6 MHz"}, {"SUT", 2, "64 ms"}, {"CKDIV8", 0, "divided by 8"},
		{"SPIEN", 0, "programmed"}, {"EESAVE", 1, "clears"}, {"BODLEVEL", 3, "disabled"},
		{"RSTDISBL", 1, "resets"}, {"SELFPRGEN", 1, "disabled"},
	})
	// ATmega32U4 defaults
	checkConfig(t, "atmega32u4-fuses", []byte{0x5E, 0x99, 0xF3}, []cfgWant{
		{"CKSEL", 0xE, "8-16 MHz"}, {"CKDIV8", 0, "divided"}, {"JTAGEN", 0, "JTAG enabled"},
		{"OCDEN", 1, "off"}, {"SPIEN", 0, "programmed"}, {"BOOTSZ", 0, "2048 words"},
		{"BOOTRST", 1, "0x0000"}, {"HWBE", 0, "HWB pin starts"}, {"BODLEVEL", 3, "2.6 V"},
	})
	// ATmega2560 defaults
	checkConfig(t, "atmega2560-fuses", []byte{0x62, 0x99, 0xFF}, []cfgWant{
		{"CKSEL", 2, "8 MHz"}, {"JTAGEN", 0, "JTAG enabled"}, {"BOOTSZ", 0, "4096 words"},
		{"BOOTSZ", 0, "0x1F000"}, {"BODLEVEL", 7, "disabled"},
	})
	// ATtiny1614 defaults: 20 MHz, UPDI, no CRC, 64 ms
	checkConfig(t, "avr-tiny01-fuses", []byte{0x00, 0x00, 0x02, 0xFF, 0x00, 0xF6, 0x07, 0x00, 0x00}, []cfgWant{
		{"WDT_PERIOD", 0, "off"}, {"BOD_ACTIVE", 0, "disabled"}, {"FREQSEL", 2, "20 MHz"},
		{"OSCLOCK", 0, "may be changed"}, {"TCD0CFG", 0, "no TCD0"}, {"RSTPINCFG", 1, "UPDI"},
		{"CRCSRC", 3, "no CRC"}, {"EESAVE", 0, "clears"}, {"SUT", 7, "64 ms"},
		{"APPEND", 0, "end of the flash"}, {"BOOTEND", 0, "end of the flash"},
	})
	checkConfig(t, "avr-tiny01-fuses", []byte{0x5B, 0xE6, 0x81, 0xFF, 0x31, 0x09, 0x00, 0x40, 0x02}, []cfgWant{
		{"WDT_PERIOD", 0xB, "8K cycles"}, {"WDT_WINDOW", 5, "128 cycles"},
		{"BOD_SLEEP", 2, "sampled"}, {"BOD_ACTIVE", 1, "enabled"}, {"BOD_LVL", 7, "4.3 V"},
		{"FREQSEL", 1, "16 MHz"}, {"OSCLOCK", 1, "locked"}, {"TCD0CFG", 0x31, "WOA=1, WOB=0"},
		{"RSTPINCFG", 2, "RESET"}, {"EESAVE", 1, "kept"}, {"BOOTEND", 2, "0x200"}, {"APPEND", 0x40, "0x4000"},
	})
	// AVR DA: CRC32 of the boot section, reset pin, OSC32K, BOD 2.7 V
	checkConfig(t, "avr-dx-fuses", []byte{0x00, 0x44, 0x01, 0, 0, 0x69, 0x03, 0x00, 0x04}, []cfgWant{
		{"BOD_ACTIVE", 1, "enabled"}, {"BOD_LVL", 2, "2.7 V"}, {"CLKSEL", 1, "OSC32K"},
		{"EESAVE", 1, "kept"}, {"RSTPINCFG", 1, "RESET"}, {"CRCSEL", 1, "CRC32"}, {"CRCSRC", 1, "boot section"},
		{"SUT", 3, "4 ms"}, {"BOOTSIZE", 4, "0x800 bytes"}, {"CODESIZE", 0, "end of the flash"},
	})
	// XMEGA A1U factory: FF 00 FF FF FE FF
	checkConfig(t, "xmega-fuses", []byte{0xFF, 0x00, 0xFF, 0xFF, 0xFE, 0xFF}, []cfgWant{
		{"JTAGUSERID", 0xFF, "user ID"}, {"WDPER", 0, "8 ms"}, {"BOOTRST", 1, "application"},
		{"BODPD", 3, "disabled"}, {"JTAGEN", 0, "JTAG enabled"}, {"WDLOCK", 1, "not locked"},
		{"STARTUPTIME", 3, "0 ms"}, {"RSTDISBL", 1, "resets"},
		{"BODLEVEL", 7, "1.6 V"}, {"EESAVE", 1, "clears"}, {"BODACT", 3, "disabled"},
	})
	checkConfig(t, "xmega-fuses", []byte{0x12, 0xA9, 0xBE, 0xFF, 0xE1, 0xD0}, []cfgWant{
		{"WDPER", 9, "4000 ms"}, {"WDWPER", 0xA, "8000 ms"}, {"BOOTRST", 0, "boot loader"},
		{"BODPD", 2, "continuously"}, {"JTAGEN", 1, "disabled"}, {"WDLOCK", 0, "locked"},
		{"STARTUPTIME", 0, "64 ms"}, {"BODLEVEL", 0, "3.0 V"}, {"EESAVE", 0, "kept"}, {"BODACT", 1, "sampled"},
	})
}

func TestConfigESPMore(t *testing.T) {
	// all zero: a fresh chip
	checkConfig(t, "esp32c3-efuse", make([]byte, 24), []cfgWant{
		{"SPI_BOOT_CRYPT_CNT", 0, "disabled"}, {"SECURE_BOOT_EN", 0, "off"},
		{"KEY_PURPOSE_0", 0, "USER"}, {"DIS_DOWNLOAD_MODE", 0, "allowed"},
		{"UART_PRINT_CONTROL", 0, "always on"}, {"DIS_USB_JTAG", 0, "enabled"},
	})
	// C3: flash encryption (CNT 1), key 0 XTS_AES_128, key 1 SB digest 0,
	// secure boot, USB JTAG off, pad JTAG off, prints off, TPUW 3
	c3 := w32(0x00800000, 1<<9|1<<19|0x2, 1<<18|4<<24|9<<28, 1<<20|3<<28|7<<4, 3<<6|1<<5|1<<1, 0)
	for _, k := range []string{"esp32c3-efuse", "esp32c6-efuse"} {
		checkConfig(t, k, c3, []cfgWant{
			{"WR_DIS", 0x00800000, "write disable"}, {"RD_DIS", 2, "read disable"},
			{"SPI_BOOT_CRYPT_CNT", 1, "ENABLED"}, {"KEY_PURPOSE_0", 4, "XTS_AES_128_KEY"},
			{"KEY_PURPOSE_1", 9, "SECURE_BOOT_DIGEST0"}, {"KEY_PURPOSE_3", 7, "DIGITAL_SIGNATURE"},
			{"SECURE_BOOT_EN", 1, "ENABLED"}, {"FLASH_TPUW", 3, "3 ms"},
			{"DIS_USB_JTAG", 1, "DISABLED"}, {"DIS_PAD_JTAG", 1, "DISABLED"},
			{"UART_PRINT_CONTROL", 3, "always off"}, {"ENABLE_SECURITY_DOWNLOAD", 1, "secure download"},
			{"DIS_DIRECT_BOOT", 1, "disabled"},
		})
	}
	if f, _ := DecodeConfig("esp32c3-efuse", c3); f[0].Off != 0 {
		t.Fatal(f[0])
	}
	// S3: DIS_USB_JTAG is in RD_REPEAT_DATA2
	s3 := w32(0, 1<<15, 7<<18, 1<<22|1<<20, 1|1<<31, 0)
	checkConfig(t, "esp32s3-efuse", s3, []cfgWant{
		{"DIS_APP_CPU", 1, "APP CPU"}, {"SPI_BOOT_CRYPT_CNT", 7, "ENABLED"}, {"DIS_USB_JTAG", 1, "DISABLED"},
		{"SECURE_BOOT_EN", 1, "ENABLED"}, {"DIS_DOWNLOAD_MODE", 1, "DISABLED"}, {"DIS_USB_OTG_DOWNLOAD_MODE", 1, "disabled"},
	})
	// S2: hard JTAG disable, 2 bits of the crypt counter (even: off)
	checkConfig(t, "esp32s2-efuse", w32(0, 1<<18, 3<<18, 0, 1<<6, 0), []cfgWant{
		{"HARD_DIS_JTAG", 1, "for ever"}, {"SPI_BOOT_CRYPT_CNT", 3, "disabled"},
		{"UART_PRINT_CONTROL", 1, "GPIO46 is low"},
	})
	if _, err := DecodeConfig("esp32s3-efuse", make([]byte, 20)); err == nil {
		t.Fatal("short block 0 accepted")
	}
}

func TestConfigKindFor(t *testing.T) {
	for _, c := range []struct{ name, family, kind string }{
		{"STM32L476RG", "", "stm32l4-option-bytes"},
		{"STM32L475/476/486", "STM32L4", "stm32l4-option-bytes"},
		{"STM32F030F4", "", "stm32f0-option-bytes"},
		{"STM32F303", "", "stm32f0-option-bytes"},
		{"STM32F103C8", "", "stm32f1-option-bytes"},
		{"STM32F205/215", "", "stm32f4-option-bytes"},
		{"STM32F411CE", "", "stm32f4-option-bytes"},
		{"STM32F746NG", "", "stm32f7-option-bytes"},
		{"STM32L0x1", "", "stm32l0-option-bytes"},
		{"STM32L152", "", "stm32l0-option-bytes"},
		{"STM32G071", "", "stm32g0-option-bytes"},
		{"STM32G474", "", "stm32g4-option-bytes"},
		{"STM32WB55 (CM4)", "", "stm32wb-option-bytes"},
		{"STM32WBA5x", "", ""},
		{"STM32H742/743", "", "stm32h7-option-bytes"},
		{"STM32U575/585", "", ""},
		{"", "STM32G4", "stm32g4-option-bytes"},
		{"my board", "STM32H7", "stm32h7-option-bytes"},
		{"ATmega328P", "AVR", "avr-fuses"},
		{"ATmega168PA", "", "avr-fuses"},
		{"ATtiny85", "AVR", "attiny-fuses"},
		{"ATtiny84A", "", "attiny-fuses"},
		{"ATtiny861A", "", "attiny-fuses"},
		{"ATtiny13A", "", "attiny13-fuses"},
		{"ATmega32U4", "", "atmega32u4-fuses"},
		{"ATmega16U2", "", ""},
		{"ATmega2560", "", "atmega2560-fuses"},
		{"ATmega1284P", "", "atmega2560-fuses"},
		{"ATmega4809", "", "avr-tiny01-fuses"},
		{"ATtiny1614", "", "avr-tiny01-fuses"},
		{"ATtiny202", "", "avr-tiny01-fuses"},
		{"ATtiny3217", "", "avr-tiny01-fuses"},
		{"ATtiny1627", "", "avr-tiny01-fuses"},
		{"ATtiny1634", "", ""},
		{"ATtiny2313A", "", ""},
		{"ATtiny841", "", ""},
		{"AVR128DA48", "", "avr-dx-fuses"},
		{"AVR64DD32", "", "avr-dx-fuses"},
		{"AVR", "AVR", ""},
		{"ATxmega128A1U", "", "xmega-fuses"},
		{"ESP32", "ESP32", "esp32-efuse"},
		{"ESP32-D0WD-V3", "", "esp32-efuse"},
		{"ESP32-S2", "", "esp32s2-efuse"},
		{"ESP32-S3", "", "esp32s3-efuse"},
		{"esp32s3", "", "esp32s3-efuse"},
		{"ESP32C3", "", "esp32c3-efuse"},
		{"ESP32-C6", "", "esp32c6-efuse"},
		{"ESP32-H2", "ESP32-H2", ""},
		{"ESP32-C2", "", ""},
		{"nRF52840", "nRF52", ""},
		{"", "", ""},
	} {
		if k := ConfigKindFor(c.name, c.family); k != c.kind {
			t.Errorf("ConfigKindFor(%q, %q) = %q, want %q", c.name, c.family, k, c.kind)
		}
		if c.kind != "" && ConfigTitle(c.kind) == "" {
			t.Errorf("%s is not a kind", c.kind)
		}
	}
}
