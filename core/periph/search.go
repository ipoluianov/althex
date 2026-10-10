package periph

import (
	"sort"
	"strings"
)

// normalize upper-cases s and keeps only letters, digits and '?'.
func normalize(s string) string {
	var b strings.Builder
	for _, c := range strings.ToUpper(s) {
		if (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '?' {
			b.WriteRune(c)
		}
	}
	return b.String()
}

// globPrefix reports whether q starts with pattern p ('?' matches any character).
func globPrefix(p, q string) bool {
	if len(q) < len(p) {
		return false
	}
	for i := 0; i < len(p); i++ {
		if p[i] != '?' && p[i] != q[i] {
			return false
		}
	}
	return true
}

// patternScore rates how well the normalised query q (a part number) matches
// the device's patterns: 0 = no match, otherwise 1000 + the length of the
// longest matching pattern.
func patternScore(e *indexEntry, q string) int {
	best := 0
	for _, p := range e.npats {
		if globPrefix(p, q) {
			best = max(best, 1000+len(p))
		}
	}
	if globPrefix(e.norm, q) {
		best = max(best, 1000+len(e.norm))
	}
	return best
}

// Search finds devices by a part number or a fragment of it:
// "stm32f103c8" finds STM32F103xx, "atmega328" finds ATmega328 (and the
// ATmega328P/PB entries), "esp32" lists the ESP32 family. Devices whose part
// patterns match the query come first (most specific first), then devices
// whose name or patterns contain the query.
func Search(query string) []DeviceInfo {
	if loadIndex() != nil {
		return nil
	}
	q := normalize(query)
	if q == "" {
		return nil
	}
	type hit struct {
		e     *indexEntry
		score int
	}
	var hits []hit
	for _, e := range entries {
		s := patternScore(e, q)
		if s == 0 {
			if strings.Contains(e.norm, q) {
				s = 500 - len(e.norm)
			} else {
				for _, p := range e.npats {
					if strings.Contains(p, q) {
						s = 400 - len(p)
						break
					}
				}
			}
		}
		if s == 0 && strings.Contains(normalize(e.info.Family), q) {
			s = 100
		}
		if s > 0 {
			hits = append(hits, hit{e, s})
		}
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].score > hits[j].score })
	out := make([]DeviceInfo, len(hits))
	for i, h := range hits {
		out[i] = h.e.info
	}
	return out
}
