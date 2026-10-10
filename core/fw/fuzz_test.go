package fw

import (
	"bytes"
	"testing"

	"github.com/ipoluianov/althex/internal/fuzzguard"
)

// Open, Detect and Verify must not fail on any file
func FuzzOpen(f *testing.F) {
	// The decoders are slow under the coverage of the fuzzer
	defer func(n int) { statsMax = n }(statsMax)
	statsMax = 256
	for _, s := range [][]byte{
		cortexImage(0x08000000, 0, 0x20005000, 4, 256), rp2040Image()[:512], avrImage()[:128],
		esp32C3Image(), esp8266Image(), uf2TwoFamilies()[:1024], dfuseTwoTargets(), armELF(),
		ihexFile(), srecFile(), x86Boot(),
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		defer fuzzguard.Start(t.Name())()
		for _, name := range []string{"x.bin", "x.hex", "x.s19"} {
			im, err := Open(bytes.NewReader(data), int64(len(data)), name)
			if err != nil {
				continue
			}
			if name == "x.bin" {
				Detect(im)
			}
			im.Verify()
			lo, hi := im.Bounds()
			b := make([]byte, 16)
			im.ReadAddr(b, lo)
			im.ReadAddr(b, hi-1)
			if off, ok := im.AddrToOff(lo); ok {
				if a, ok := im.OffToAddr(off); !ok || !im.Contains(a) {
					t.Fatalf("offset 0x%X of 0x%X has no address", off, lo)
				}
			}
		}
		FixESP32(data)
		for _, k := range ConfigKinds() {
			DecodeConfig(k, data)
		}
	})
}
