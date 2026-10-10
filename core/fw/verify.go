package fw

import (
	"bytes"
	"fmt"
)

// Verify checks the checksums of the image again (FW-14): the checksum and
// the SHA-256 of ESP images, the CRC of the DfuSe suffix, the boot2 CRC of
// an RP2040; with the issues found when it was opened (overlaps, missing
// UF2 blocks...). Nothing - all is well.
func (im *Image) Verify() []string {
	issues := append([]string(nil), im.Issues...)
	have := func(s string) bool {
		for _, x := range issues {
			if x == s {
				return true
			}
		}
		return false
	}
	add := func(s []string) {
		for _, x := range s {
			if !have(x) {
				issues = append(issues, x)
			}
		}
	}
	if im.src != nil {
		switch im.kind {
		case kindESP32, kindESP8266:
			add(espVerify(im.src, im.size, im.espSubs))
		case kindDfuSe:
			if im.size >= dfuSuffixLen {
				suf := readAt(im.src, im.size-dfuSuffixLen, dfuSuffixLen)
				if suf != nil && string(suf[8:11]) == "UFD" {
					if want, err := dfuCRC(im.src, im.size); err == nil && le32(suf[12:]) != want {
						add([]string{fmt.Sprintf("DFU suffix CRC 0x%08X, should be 0x%08X", le32(suf[12:]), want)})
					}
				}
			}
		}
	}
	add(checkRP2040(im))
	return issues
}

// FixDfuSe recomputes the CRC of the suffix of a DfuSe (or DFU) file
func FixDfuSe(data []byte) ([]byte, error) {
	if len(data) < dfuSuffixLen || string(data[len(data)-8:len(data)-5]) != "UFD" {
		return nil, fmt.Errorf("%w: no DFU suffix", ErrFormat)
	}
	out := append([]byte(nil), data...)
	crc, _ := dfuCRC(bytes.NewReader(out), int64(len(out)))
	n := len(out)
	out[n-4], out[n-3], out[n-2], out[n-1] = byte(crc), byte(crc>>8), byte(crc>>16), byte(crc>>24)
	return out, nil
}
