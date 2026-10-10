package expr

import (
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/ipoluianov/althex/internal/fuzzguard"
)

var vars = map[string]int64{"cursor": 100, "Size": 4096, "sel": 16}

func TestEval(t *testing.T) {
	tests := []struct {
		s    string
		want int64
	}{
		{"0", 0},
		{"42", 42},
		{"010", 10},
		{"0x400 + 3*512", 2560},
		{"0X1f", 31},
		{"0o17", 15},
		{"0b1010", 10},
		{"1A2Bh", 0x1a2b},
		{"0FFh", 255},
		{"0b1h", 0xb1},
		{"$1A2B", 0x1a2b},
		{"$ff", 255},
		{"1_000_000", 1000000},
		{"0x_FF_FF", 0xffff},
		{"  7  ", 7},
		{"-5", -5},
		{"+5", 5},
		{"--5", 5},
		{"-+-5", 5},
		{"~0", -1},
		{"~0x0F & 0xFF", 0xf0},
		{"(1+2)*3", 9},
		{"((2))", 2},
		{"1+2*3", 7},
		{"10-4-3", 3},
		{"100/10/5", 2},
		{"7%4", 3},
		{"-7/2", -3},
		{"-7%2", -1},
		{"1+2<<3", 24},
		{"1<<2+3", 32},
		{"6&3+1", 4},
		{"1|2^3", 1},
		{"1^3&2", 3},
		{"5|1&2", 5},
		{"1<<4>>2", 4},
		{"0xF0 >> 4", 15},
		{"-16 >> 2", -4},
		{"1 >> 100", 0},
		{"-1 >> 100", -1},
		{"-2*-3", 6},
		{"2*-3", -6},
		{"cursor + 0x10", 116},
		{"CURSOR+size+Sel", 4212},
		{"size/sel", 256},
		{"9223372036854775807", math.MaxInt64},
		{"-9223372036854775808", math.MinInt64},
		{"0x7FFFFFFFFFFFFFFF", math.MaxInt64},
		{"1<<62", 1 << 62},
		{"-1<<63", math.MinInt64},
		{"-4611686018427387904*2", math.MinInt64},
		{"0<<100", 0},
		{"-9223372036854775808 % -1", 0},
	}
	for _, tt := range tests {
		got, err := Eval(tt.s, vars)
		if err != nil || got != tt.want {
			t.Errorf("%q: got %d, %v; want %d", tt.s, got, err, tt.want)
		}
	}
}

func TestEvalErrors(t *testing.T) {
	tests := []struct {
		s   string
		msg string
		pos int
	}{
		{"", "unexpected end", 0},
		{"   ", "unexpected end", 3},
		{"1+", "unexpected end", 2},
		{"1 2", `unexpected "2"`, 2},
		{"(1+2", "unclosed '('", 0},
		{"(1+2]", `unexpected "]"`, 4},
		{"1)", `unexpected ")"`, 1},
		{"*3", `unexpected "*"`, 0},
		{"1 ? 2", `unexpected "?"`, 2},
		{"1 + П", `unexpected "П"`, 4},
		{"foo + 1", `unknown name "foo"`, 0},
		{"10 / 0", "division by zero", 3},
		{"10 % (5-5)", "division by zero", 3},
		{"0x", `bad number "0x"`, 0},
		{"$", `bad number "$"`, 0},
		{"12ab", `bad number "12ab"`, 0},
		{"0b102", `bad number "0b102"`, 0},
		{"0o8", `bad number "0o8"`, 0},
		{"FFh", `unknown name "FFh"`, 0},
		{"9223372036854775808", "number too big", 0},
		{"0x10000000000000000", "number too big", 0},
		{"9223372036854775807 + 1", "overflow", 20},
		{"-9223372036854775808 - 1", "overflow", 21},
		{"0 - -9223372036854775808", "overflow", 2},
		{"-(-9223372036854775808)", "overflow", 0},
		{"4611686018427387904*2", "overflow", 19},
		{"-1 * -9223372036854775808", "overflow", 3},
		{"-9223372036854775808 / -1", "overflow", 21},
		{"1<<63", "overflow", 1},
		{"3<<62", "overflow", 1},
		{"1<<64", "overflow", 1},
		{"1 << -1", "negative shift", 2},
	}
	for _, tt := range tests {
		v, err := Eval(tt.s, vars)
		var e *Error
		if !errors.As(err, &e) || e.Msg != tt.msg || e.Pos != tt.pos {
			t.Errorf("%q: got %d, %#v; want %q at %d", tt.s, v, err, tt.msg, tt.pos)
		}
	}
	_, err := Eval("2 +", nil)
	if err == nil || err.Error() != "unexpected end at position 4" {
		t.Errorf("error text %v", err)
	}
}

func TestOffset(t *testing.T) {
	tests := []struct {
		s    string
		base int64
		want int64
		ok   bool
	}{
		{"0x400 + 3*512", 50, 2560, true},
		{"+0x10", 100, 116, true},
		{" +0x10", 100, 116, true},
		{"-20", 100, 80, true},
		{"-0x10+4", 100, 88, true},
		{"-(4+6)", 100, 90, true},
		{"10", 100, 10, true},
		{"(-20)", 100, 0, false},
		{"-101", 100, 0, false},
		{"-100", 100, 0, true},
		{"+1", math.MaxInt64, 0, false},
		{"cursor - 0x10", 0, 84, true},
		{"size", 0, 4096, true},
		{"", 0, 0, false},
		{"+", 0, 0, false},
		{"+cursor", 10, 110, true},
	}
	for _, tt := range tests {
		got, err := Offset(tt.s, tt.base, vars)
		if (err == nil) != tt.ok || got != tt.want {
			t.Errorf("%q base %d: got %d, %v; want %d ok=%v", tt.s, tt.base, got, err, tt.want, tt.ok)
		}
	}
}

func FuzzEval(f *testing.F) {
	for _, s := range []string{"0x400 + 3*512", "-(1<<63)", "$ff_h", "((((", "1A2Bh%0", "cursor>>65", "~-9223372036854775808"} {
		f.Add(s, int64(0))
	}
	f.Fuzz(func(t *testing.T, s string, base int64) {
		defer fuzzguard.Start(t.Name())()
		v, err := Eval(s, vars)
		if err != nil {
			var e *Error
			if !errors.As(err, &e) || e.Pos < 0 || e.Pos > len(s) {
				t.Fatalf("%q: error %v", s, err)
			}
		} else if v2, _ := Eval("("+s+")", vars); v2 != v {
			t.Fatalf("%q: %d, in parentheses %d", s, v, v2)
		}
		if o, err := Offset(s, base, vars); err == nil && o < 0 {
			t.Fatalf("%q: offset %d", s, o)
		}
	})
}

// Deep nesting is an error, not a stack overflow
func TestNestingLimit(t *testing.T) {
	for _, s := range []string{strings.Repeat("(", 1_000_000) + "1", strings.Repeat("-", 1_000_000) + "1"} {
		if _, err := Eval(s, nil); err == nil || !strings.Contains(err.Error(), "nested too deep") {
			t.Errorf("%.10s...: %v", s, err)
		}
	}
	if v, err := Eval(strings.Repeat("(", 500)+"1"+strings.Repeat(")", 500), nil); err != nil || v != 1 {
		t.Fatal(v, err)
	}
}
