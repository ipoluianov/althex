package periph

import (
	"testing"

	"github.com/ipoluianov/althex/internal/fuzzguard"
)

// FuzzSearch: any query the user types finds devices that load
func FuzzSearch(f *testing.F) {
	for _, q := range []string{"stm32f103c8", "atmega328", "esp32", "STM32F4??", "rp2040", "?", ""} {
		f.Add(q)
	}
	f.Fuzz(func(t *testing.T, q string) {
		defer fuzzguard.Start(t.Name())()
		res := Search(q)
		if len(res) > 0 {
			if _, err := Load(res[0].Name); err != nil {
				t.Fatalf("%q: %s does not load: %v", q, res[0].Name, err)
			}
		}
	})
}
