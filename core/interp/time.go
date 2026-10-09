package interp

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
)

// Times are shown in UTC
const (
	layoutDateTime = "2006-01-02 15:04:05"
	layoutDate     = "2006-01-02"
	layoutTime     = "15:04:05"
)

// The seconds of the Unix epoch that can be shown: years 1 to 9999
var (
	minShown = time.Date(1, 1, 1, 0, 0, 0, 0, time.UTC).Unix()
	maxShown = time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC).Unix()
)

// Epochs of FILETIME (1601-01-01) and of OLE dates (1899-12-30) in Unix seconds
const (
	filetimeEpoch = -11644473600
	oleEpoch      = -2209161600
)

var errNotTime = errors.New("not a date and time (YYYY-MM-DD hh:mm:ss)")

// showUnix shows Unix seconds; "" outside years 1 to 9999
func showUnix(sec int64) string {
	if sec < minShown || sec > maxShown {
		return ""
	}
	return time.Unix(sec, 0).UTC().Format(layoutDateTime)
}

// parseTime reads a date and time in UTC; the time may be left out, and the
// seconds may have a fraction
func parseTime(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	for _, l := range []string{layoutDateTime, "2006-01-02T15:04:05", layoutDate} {
		if t, err := time.ParseInLocation(l, s, time.UTC); err == nil {
			return t, nil
		}
	}
	return time.Time{}, errNotTime
}

// parseSeconds reads a time that has whole seconds
func parseSeconds(s, name string) (int64, error) {
	t, err := parseTime(s)
	if err != nil {
		return 0, err
	}
	if t.Nanosecond() != 0 {
		return 0, fmt.Errorf("%s keeps whole seconds", name)
	}
	return t.Unix(), nil
}

func decUnix32(b []byte, be bool) (string, int) {
	return showUnix(signExtend(getUint(b, 4, be), 4)), 4
}

func encUnix32(s string, be bool) ([]byte, error) {
	sec, err := parseSeconds(s, "Unix time")
	if err != nil {
		return nil, err
	}
	if sec < math.MinInt32 || sec > math.MaxInt32 {
		return nil, rangeErr("Unix time (32)")
	}
	return putUint(uint64(sec), 4, be), nil
}

func decUnix64(b []byte, be bool) (string, int) {
	return showUnix(int64(getUint(b, 8, be))), 8
}

func encUnix64(s string, be bool) ([]byte, error) {
	sec, err := parseSeconds(s, "Unix time")
	if err != nil {
		return nil, err
	}
	return putUint(uint64(sec), 8, be), nil
}

// decFiletime shows the 100 ns ticks since 1601 only when there are some
func decFiletime(b []byte, be bool) (string, int) {
	ticks := getUint(b, 8, be)
	s := showUnix(int64(ticks/1e7) + filetimeEpoch)
	if s != "" && ticks%1e7 != 0 {
		s += fmt.Sprintf(".%07d", ticks%1e7)
	}
	return s, 8
}

func encFiletime(s string, be bool) ([]byte, error) {
	t, err := parseTime(s)
	if err != nil {
		return nil, err
	}
	if t.Nanosecond()%100 != 0 {
		return nil, errors.New("FILETIME keeps 100 ns")
	}
	sec := t.Unix() - filetimeEpoch
	if sec < 0 {
		return nil, rangeErr("FILETIME")
	}
	return putUint(uint64(sec)*1e7+uint64(t.Nanosecond()/100), 8, be), nil
}

// DOS (FAT) date word: day in bits 0-4, month in 5-8, year since 1980 in 9-15
func dosDate(w uint64) (time.Time, bool) {
	y, m, d := int(w>>9)+1980, time.Month(w>>5&0xf), int(w&0x1f)
	if m < 1 || m > 12 || d < 1 || d > time.Date(y, m+1, 0, 0, 0, 0, 0, time.UTC).Day() {
		return time.Time{}, false
	}
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC), true
}

// DOS (FAT) time word: seconds/2 in bits 0-4, minutes in 5-10, hours in 11-15
func dosTime(w uint64) (time.Duration, bool) {
	h, m, s := w>>11, w>>5&0x3f, (w&0x1f)*2
	if h > 23 || m > 59 || s > 59 {
		return 0, false
	}
	return time.Duration(h)*time.Hour + time.Duration(m)*time.Minute + time.Duration(s)*time.Second, true
}

func dosDateWord(t time.Time) (uint64, error) {
	if t.Year() < 1980 || t.Year() > 2107 {
		return 0, rangeErr("DOS date")
	}
	return uint64(t.Year()-1980)<<9 | uint64(t.Month())<<5 | uint64(t.Day()), nil
}

func dosTimeWord(t time.Time) (uint64, error) {
	if t.Nanosecond() != 0 || t.Second()%2 != 0 {
		return 0, errors.New("DOS time keeps even seconds")
	}
	return uint64(t.Hour())<<11 | uint64(t.Minute())<<5 | uint64(t.Second()/2), nil
}

func decDOSDate(b []byte, be bool) (string, int) {
	t, ok := dosDate(getUint(b, 2, be))
	if !ok {
		return "", 0
	}
	return t.Format(layoutDate), 2
}

func encDOSDate(s string, be bool) ([]byte, error) {
	t, err := time.ParseInLocation(layoutDate, strings.TrimSpace(s), time.UTC)
	if err != nil {
		return nil, errors.New("not a date (YYYY-MM-DD)")
	}
	w, err := dosDateWord(t)
	if err != nil {
		return nil, err
	}
	return putUint(w, 2, be), nil
}

func decDOSTime(b []byte, be bool) (string, int) {
	d, ok := dosTime(getUint(b, 2, be))
	if !ok {
		return "", 0
	}
	return time.Time{}.Add(d).Format(layoutTime), 2
}

func encDOSTime(s string, be bool) ([]byte, error) {
	t, err := time.ParseInLocation(layoutTime, strings.TrimSpace(s), time.UTC)
	if err != nil {
		return nil, errors.New("not a time (hh:mm:ss)")
	}
	w, err := dosTimeWord(t)
	if err != nil {
		return nil, err
	}
	return putUint(w, 2, be), nil
}

// The DOS date and time is read as a 32-bit integer in the byte order with
// the date in the high word: little-endian it is the time word then the date
// word, as in FAT directory entries and ZIP headers
func decDOSDateTime(b []byte, be bool) (string, int) {
	v := getUint(b, 4, be)
	d, ok1 := dosDate(v >> 16)
	tm, ok2 := dosTime(v & 0xffff)
	if !ok1 || !ok2 {
		return "", 0
	}
	return d.Add(tm).Format(layoutDateTime), 4
}

func encDOSDateTime(s string, be bool) ([]byte, error) {
	t, err := parseTime(s)
	if err != nil {
		return nil, err
	}
	dw, err := dosDateWord(t)
	if err != nil {
		return nil, err
	}
	tw, err := dosTimeWord(t)
	if err != nil {
		return nil, err
	}
	return putUint(dw<<16|tw, 4, be), nil
}

// The OLE automation date is days since 1899-12-30 with the time of day as
// the fraction. Before the epoch the fraction still counts forward from
// midnight: -1.25 is 1899-12-29 06:00. The valid range is years 100 to 9999.
const (
	oleMin = -657434 // 0100-01-01
	oleMax = 2958466 // 10000-01-01
)

func decOLE(b []byte, be bool) (string, int) {
	v := math.Float64frombits(getUint(b, 8, be))
	if math.IsNaN(v) || v <= oleMin-1 || v >= oleMax {
		return "", 0
	}
	days := math.Trunc(v)
	sec := int64(days)*86400 + int64(math.Round(math.Abs(v-days)*86400))
	return showUnix(sec + oleEpoch), 8
}

func encOLE(s string, be bool) ([]byte, error) {
	sec, err := parseSeconds(s, "OLE date")
	if err != nil {
		return nil, err
	}
	sec -= oleEpoch
	days := sec / 86400
	if sec%86400 < 0 {
		days--
	}
	if days < oleMin || days >= oleMax {
		return nil, rangeErr("OLE date")
	}
	frac := float64(sec-days*86400) / 86400
	v := float64(days) + frac
	if days < 0 {
		v = float64(days) - frac
	}
	return putUint(math.Float64bits(v), 8, be), nil
}
