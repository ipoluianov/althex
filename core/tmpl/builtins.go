package tmpl

import (
	"encoding/hex"
	"fmt"
	"math"
	"strings"

	checksum "github.com/ipoluianov/althex/core/hash"
	"github.com/ipoluianov/althex/core/search"
)

type builtin func(in *interp, pos Pos, args []Value) Value

var builtins map[string]builtin

func init() {
	builtins = map[string]builtin{
		"FTell":               func(in *interp, pos Pos, a []Value) Value { return Int(in.pos) },
		"FileSize":            func(in *interp, pos Pos, a []Value) Value { return Int(in.rd.size()) },
		"FEof":                func(in *interp, pos Pos, a []Value) Value { return boolValue(in.pos >= in.rd.size()) },
		"FSeek":               bFSeek,
		"FSkip":               bFSkip,
		"LittleEndian":        func(in *interp, pos Pos, a []Value) Value { in.big = false; return Value{} },
		"BigEndian":           func(in *interp, pos Pos, a []Value) Value { in.big = true; return Value{} },
		"IsLittleEndian":      func(in *interp, pos Pos, a []Value) Value { return boolValue(!in.big) },
		"IsBigEndian":         func(in *interp, pos Pos, a []Value) Value { return boolValue(in.big) },
		"BitfieldRightToLeft": func(in *interp, pos Pos, a []Value) Value { in.bitOrder = bitsRightToLeft; return Value{} },
		"BitfieldLeftToRight": func(in *interp, pos Pos, a []Value) Value { in.bitOrder = bitsLeftToRight; return Value{} },
		"BitfieldByteOrder":   func(in *interp, pos Pos, a []Value) Value { in.bitOrder = bitsByOrder; return Value{} },
		"GetSelStart":         func(in *interp, pos Pos, a []Value) Value { return Int(in.env.SelStart) },
		"GetSelSize":          func(in *interp, pos Pos, a []Value) Value { return Int(in.env.SelSize) },
		"GetCursorPos":        func(in *interp, pos Pos, a []Value) Value { return Int(in.env.Cursor) },

		"ReadByte":    readAt("char"),
		"ReadUByte":   readAt("uchar"),
		"ReadShort":   readAt("int16"),
		"ReadUShort":  readAt("uint16"),
		"ReadInt":     readAt("int32"),
		"ReadUInt":    readAt("uint32"),
		"ReadInt64":   readAt("int64"),
		"ReadUInt64":  readAt("uint64"),
		"ReadQuad":    readAt("int64"),
		"ReadUQuad":   readAt("uint64"),
		"ReadHFloat":  readAt("hfloat"),
		"ReadFloat":   readAt("float"),
		"ReadDouble":  readAt("double"),
		"ReadString":  bReadString(false),
		"ReadWString": bReadString(true),

		"WriteByte":   writeAt("char"),
		"WriteUByte":  writeAt("uchar"),
		"WriteShort":  writeAt("int16"),
		"WriteUShort": writeAt("uint16"),
		"WriteInt":    writeAt("int32"),
		"WriteUInt":   writeAt("uint32"),
		"WriteInt64":  writeAt("int64"),
		"WriteUInt64": writeAt("uint64"),
		"WriteQuad":   writeAt("int64"),
		"WriteUQuad":  writeAt("uint64"),
		"WriteHFloat": writeAt("hfloat"),
		"WriteFloat":  writeAt("float"),
		"WriteDouble": writeAt("double"),
		"WriteString": bWriteString,
		"InsertBytes": bInsertBytes,
		"DeleteBytes": bDeleteBytes,
		"Checksum":    bChecksum,
		"FindHex":     bFindHex,
		"Printf":      bPrintf,
		"Str":         bStr,
		"SPrintf":     bStr,
		"Warning":     bWarning,
		"Assert":      bAssert,
		"Exit":        bExit,
		"Strlen":      bStrlen,
		"SubStr":      bSubStr,
		"Strstr":      bStrstr,
		"Atoi":        bAtoi,
		"ToUpper":     func(in *interp, pos Pos, a []Value) Value { return String(strings.ToUpper(in.str(pos, a, 0))) },
		"ToLower":     func(in *interp, pos Pos, a []Value) Value { return String(strings.ToLower(in.str(pos, a, 0))) },
		"Abs":         bAbs,
		"Min":         func(in *interp, pos Pos, a []Value) Value { return minMax(in, pos, a, true) },
		"Max":         func(in *interp, pos Pos, a []Value) Value { return minMax(in, pos, a, false) },
		"Pow": func(in *interp, pos Pos, a []Value) Value {
			return Float(math.Pow(in.num(pos, a, 0), in.num(pos, a, 1)))
		},
		"Floor":        func(in *interp, pos Pos, a []Value) Value { return Float(math.Floor(in.num(pos, a, 0))) },
		"Ceil":         func(in *interp, pos Pos, a []Value) Value { return Float(math.Ceil(in.num(pos, a, 0))) },
		"Sqrt":         func(in *interp, pos Pos, a []Value) Value { return Float(math.Sqrt(in.num(pos, a, 0))) },
		"EnumToString": bEnumToString,
	}
}

func (in *interp) arg(pos Pos, a []Value, i int) Value {
	if i >= len(a) {
		in.fail(pos, "too few arguments")
	}
	return a[i]
}

func (in *interp) int(pos Pos, a []Value, i int) int64 { return in.toInt(pos, in.arg(pos, a, i)) }

func (in *interp) num(pos Pos, a []Value, i int) float64 {
	v := in.arg(pos, a, i).deref()
	if !v.num() {
		in.fail(pos, "a number expected, %s found", kindName(v))
	}
	return v.asFloat()
}

func (in *interp) str(pos Pos, a []Value, i int) string {
	v := in.arg(pos, a, i)
	if v.k == kNode && v.n.isText {
		return strings.Trim(v.n.Value(), "\"")
	}
	v = v.deref()
	if v.k != kString {
		in.fail(pos, "a string expected, %s found", kindName(v))
	}
	return v.s
}

func bFSeek(in *interp, pos Pos, a []Value) Value {
	p := in.int(pos, a, 0)
	if p < 0 || p > in.rd.size() {
		return Int(-1)
	}
	in.pos = p
	in.bits.active = false
	return Int(0)
}

func bFSkip(in *interp, pos Pos, a []Value) Value {
	return bFSeek(in, pos, []Value{Int(in.pos + in.int(pos, a, 0))})
}

// readAt makes the function that reads a value of the type at an offset
// (the current position without one), not moving the position
func readAt(typeName string) builtin {
	t := primTypes[typeName]
	return func(in *interp, pos Pos, a []Value) Value {
		off := in.pos
		if len(a) > 0 {
			off = in.int(pos, a, 0)
		}
		return decodePrim(t, in.rd.read(pos, in, off, t.size), in.big)
	}
}

func bReadString(wide bool) builtin {
	return func(in *interp, pos Pos, a []Value) Value {
		off := in.pos
		if len(a) > 0 {
			off = in.int(pos, a, 0)
		}
		if off < 0 || off > in.rd.size() {
			in.fail(pos, "reading at 0x%X past the end", off)
		}
		limit := 1 << 20
		maxLen := int64(-1)
		if len(a) > 1 {
			if maxLen = in.int(pos, a, 1); maxLen >= 0 {
				// A character is at most 4 bytes in UTF-8, 2 in UTF-16
				limit = int(min(maxLen*2+2, 1<<20))
			}
		}
		s, _ := in.rd.readZStringMax(off, wide, in.big, limit)
		// Cut by bytes (by characters for UTF-16): the text need not be UTF-8
		if maxLen >= 0 {
			if wide && maxLen < int64(len([]rune(s))) {
				s = string([]rune(s)[:maxLen])
			} else if !wide && maxLen < int64(len(s)) {
				s = s[:maxLen]
			}
		}
		return String(s)
	}
}

func writeAt(typeName string) builtin {
	t := primTypes[typeName]
	return func(in *interp, pos Pos, a []Value) Value {
		off := in.int(pos, a, 0)
		if off < 0 || off+int64(t.size) > in.rd.size() {
			in.fail(pos, "writing at 0x%X past the end", off)
		}
		in.write(pos, off, encodePrim(t, in.arg(pos, a, 1), in.big))
		return Value{}
	}
}

func bWriteString(in *interp, pos Pos, a []Value) Value {
	off := in.int(pos, a, 0)
	s := in.str(pos, a, 1)
	if off < 0 || off+int64(len(s)) > in.rd.size() {
		in.fail(pos, "writing at 0x%X past the end", off)
	}
	in.write(pos, off, []byte(s))
	return Value{}
}

func bInsertBytes(in *interp, pos Pos, a []Value) Value {
	off, n := in.int(pos, a, 0), in.int(pos, a, 1)
	fill := byte(0)
	if len(a) > 2 {
		fill = byte(in.int(pos, a, 2))
	}
	if in.env.Writer == nil {
		in.fail(pos, "the template writes, but the document is read-only here")
	}
	if n < 0 || n > 1<<30 || off < 0 || off > in.rd.size() {
		in.fail(pos, "inserting %d bytes at 0x%X", n, off)
	}
	b := make([]byte, n)
	for i := range b {
		b[i] = fill
	}
	if err := in.env.Writer.Insert(off, b); err != nil {
		in.fail(pos, "inserting at 0x%X: %v", off, err)
	}
	in.res.Wrote = true
	in.rd.invalidate()
	return Value{}
}

func bDeleteBytes(in *interp, pos Pos, a []Value) Value {
	off, n := in.int(pos, a, 0), in.int(pos, a, 1)
	if in.env.Writer == nil {
		in.fail(pos, "the template writes, but the document is read-only here")
	}
	if n < 0 || off < 0 || off+n > in.rd.size() {
		in.fail(pos, "deleting %d bytes at 0x%X", n, off)
	}
	if err := in.env.Writer.Delete(off, n); err != nil {
		in.fail(pos, "deleting at 0x%X: %v", off, err)
	}
	in.res.Wrote = true
	in.rd.invalidate()
	return Value{}
}

// bChecksum computes a checksum of a range: CRCs and Adler as numbers, the
// hashes as hex text
func bChecksum(in *interp, pos Pos, a []Value) Value {
	name := in.str(pos, a, 0)
	off, n := in.int(pos, a, 1), in.int(pos, a, 2)
	alg, ok := checksum.ByID(strings.ToLower(name))
	if !ok {
		p, ok2 := checksum.CRCPreset(name)
		if !ok2 {
			in.fail(pos, "unknown checksum %q", name)
		}
		alg = checksum.CRCAlgorithm(p)
	}
	if off < 0 || n < 0 || off+n > in.rd.size() {
		in.fail(pos, "the range 0x%X+%d is out of the data", off, n)
	}
	sums, err := checksum.Compute(in.ctx, in.env.Data, off, n, []checksum.Algorithm{alg}, nil)
	if err != nil {
		in.fail(pos, "%v", err)
	}
	sum := sums[0]
	if len(sum) <= 8 && (strings.HasPrefix(alg.ID, "crc") || alg.ID == "adler32" || strings.HasPrefix(alg.ID, "sum") || alg.ID == "xor8" || strings.HasPrefix(alg.Name, "CRC")) {
		var v uint64
		for _, b := range sum {
			v = v<<8 | uint64(b)
		}
		return Uint(v)
	}
	return String(hex.EncodeToString(sum))
}

// format formats like printf: %d %u %x %X %o %c %s %f %e %g %%, with flags and widths
func (in *interp) format(pos Pos, a []Value) string {
	f := in.str(pos, a, 0)
	args := a[1:]
	var sb strings.Builder
	k := 0
	for i := 0; i < len(f); i++ {
		if f[i] != '%' {
			sb.WriteByte(f[i])
			continue
		}
		j := i + 1
		for j < len(f) && strings.IndexByte("-+ #0123456789.lhL", f[j]) >= 0 {
			j++
		}
		if j >= len(f) {
			sb.WriteString(f[i:])
			break
		}
		verb := f[j]
		spec := strings.NewReplacer("l", "", "h", "", "L", "").Replace(f[i:j])
		if verb == '%' {
			sb.WriteByte('%')
			i = j
			continue
		}
		if k >= len(args) {
			in.fail(pos, "too few arguments for the format")
		}
		v := args[k]
		k++
		switch verb {
		case 'd', 'i':
			n, _ := v.AsInt()
			fmt.Fprintf(&sb, spec+"d", n)
		case 'u':
			n, _ := v.AsInt()
			fmt.Fprintf(&sb, spec+"d", uint64(n))
		case 'x', 'X', 'o', 'b':
			n, _ := v.AsInt()
			fmt.Fprintf(&sb, spec+string(verb), uint64(n))
		case 'c':
			n, _ := v.AsInt()
			sb.WriteRune(rune(n))
		case 's':
			fmt.Fprintf(&sb, spec+"s", textOf(v))
		case 'f', 'e', 'g', 'E', 'G':
			fmt.Fprintf(&sb, spec+string(verb), v.asFloat())
		default:
			sb.WriteString(f[i : j+1])
		}
		i = j
	}
	return sb.String()
}

// maxOutput is how many lines of output a run keeps
const maxOutput = 10000

func (in *interp) print(s string) {
	if len(in.res.Output) < maxOutput {
		in.res.Output = append(in.res.Output, strings.TrimSuffix(s, "\n"))
	}
}

func bPrintf(in *interp, pos Pos, a []Value) Value {
	in.print(in.format(pos, a))
	return Value{}
}

func bStr(in *interp, pos Pos, a []Value) Value { return String(in.format(pos, a)) }

func bWarning(in *interp, pos Pos, a []Value) Value {
	msg := in.format(pos, a)
	if len(in.res.Warnings) < maxOutput {
		in.res.Warnings = append(in.res.Warnings, msg)
	}
	in.print("Warning: " + msg)
	return Value{}
}

func bAssert(in *interp, pos Pos, a []Value) Value {
	if !in.arg(pos, a, 0).truthy() {
		msg := "assertion failed"
		if len(a) > 1 {
			msg = in.format(pos, a[1:])
		}
		in.fail(pos, "%s", msg)
	}
	return Value{}
}

func bExit(in *interp, pos Pos, a []Value) Value {
	code := int64(0)
	if len(a) > 0 {
		code = in.int(pos, a, 0)
	}
	panic(stopRun{code})
}

func bStrlen(in *interp, pos Pos, a []Value) Value { return Int(int64(len(in.str(pos, a, 0)))) }

func bSubStr(in *interp, pos Pos, a []Value) Value {
	s := in.str(pos, a, 0)
	start := max(min(in.int(pos, a, 1), int64(len(s))), 0)
	end := int64(len(s))
	if len(a) > 2 {
		if n := in.int(pos, a, 2); n >= 0 {
			end = min(start+n, end)
		}
	}
	return String(s[start:end])
}

func bStrstr(in *interp, pos Pos, a []Value) Value {
	return Int(int64(strings.Index(in.str(pos, a, 0), in.str(pos, a, 1))))
}

func bAtoi(in *interp, pos Pos, a []Value) Value {
	var n int64
	fmt.Sscan(strings.TrimSpace(in.str(pos, a, 0)), &n)
	return Int(n)
}

func bAbs(in *interp, pos Pos, a []Value) Value {
	v := in.arg(pos, a, 0).deref()
	if v.k == kFloat {
		return Float(math.Abs(v.f))
	}
	n := in.toInt(pos, v)
	if n < 0 {
		n = -n
	}
	return Int(n)
}

func minMax(in *interp, pos Pos, a []Value, isMin bool) Value {
	x, y := in.arg(pos, a, 0).deref(), in.arg(pos, a, 1).deref()
	less := compare(in, pos, "<", x, y).truthy()
	if less == isMin {
		return x
	}
	return y
}

func bEnumToString(in *interp, pos Pos, a []Value) Value {
	v := in.arg(pos, a, 0)
	if v.k == kNode {
		if n, ok := v.deref().AsInt(); ok {
			return String(v.n.typ.enumName(n))
		}
	}
	return String("")
}

// bFindHex finds hex bytes ("FF D8 ??", ?? for any byte) from start to
// end (the end of the data without it); returns the offset or -1
func bFindHex(in *interp, pos Pos, a []Value) Value {
	pat, err := search.ParseHex(in.str(pos, a, 0))
	if err != nil {
		in.fail(pos, "FindHex: %v", err)
	}
	from := in.int(pos, a, 1)
	to := in.rd.size()
	if len(a) > 2 {
		to = min(in.int(pos, a, 2), to)
	}
	if from < 0 || from >= to {
		return Int(-1)
	}
	found := int64(-1)
	err = search.FindAll(in.ctx, in.env.Data, in.rd.size(), pat, search.Options{From: from, To: to, MaxHits: 1, Workers: 1, ChunkSize: 1 << 20},
		func(h search.Hit) bool { found = h.Off; return false }, nil)
	if err != nil {
		in.fail(pos, "FindHex: %v", err)
	}
	return Int(found)
}
