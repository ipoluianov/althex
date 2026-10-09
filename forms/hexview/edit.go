package hexview

import (
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/ipoluianov/althex/core/doc"
	"github.com/ipoluianov/nui/ui"
)

// MaxClipboard is the most bytes copied to the clipboard at once
const MaxClipboard = 16 << 20

// ErrTooLarge: the selection is too large for the clipboard
var ErrTooLarge = errors.New("the selection is too large to copy")

var typing = doc.EditOptions{Kind: doc.KindTyping, Typing: true}

// canEdit tells whether edits are allowed now
func (v *View) canEdit() bool {
	return !v.busy && !v.doc.ReadOnly()
}

func (v *View) report(err error) {
	if err != nil && v.OnError != nil {
		v.OnError(err)
	}
}

// byteAt returns the byte at the offset; 0 past the end
func (v *View) byteAt(off int64) byte {
	var b [1]byte
	if n, _ := v.doc.ReadAt(b[:], off); n == 1 {
		return b[0]
	}
	return 0
}

// dropSelection removes the selected bytes when typing over them in
// insert mode, or puts the caret at their start
func (v *View) dropSelection() error {
	start, n := v.Selection()
	if n == 0 {
		return nil
	}
	v.caret, v.anchor = start, start
	if v.insert {
		return v.doc.Delete(start, n, doc.EditOptions{})
	}
	return nil
}

// typeDigit types a digit of the mode at the caret: it replaces that
// digit of the byte; after the last digit the caret goes to the next byte.
// A value over 255 (e.g. 3xx in decimal) is not typed.
func (v *View) typeDigit(d int) {
	if !v.canEdit() {
		return
	}
	if err := v.dropSelection(); err != nil {
		v.report(err)
		return
	}
	digits, base := v.opts.Mode.digits(), v.opts.Mode.base()
	off := v.caret
	add := v.digit == 0 && (v.insert || off == v.doc.Size())
	old := 0
	if !add {
		old = int(v.byteAt(off))
	}
	// Replace the digit: place is its weight
	place := 1
	for k := digits - 1; k > v.digit; k-- {
		place *= base
	}
	val := old - (old/place%base)*place + d*place
	if val > 255 {
		return
	}
	var err error
	if add {
		err = v.doc.Insert(off, []byte{byte(val)}, typing)
	} else {
		err = v.doc.Overwrite(off, []byte{byte(val)}, typing)
	}
	if err != nil {
		v.report(err)
		return
	}
	v.digit++
	if v.digit == digits {
		v.digit = 0
		v.caret = off + 1
	}
	v.anchor = v.caret
	v.ensureCaretVisible()
	v.resetBlink()
	v.changed()
}

// typeRune types a character in the text column in its encoding (UTF-8
// without one); a character the encoding lacks is not typed
func (v *View) typeRune(r rune) {
	if !v.canEdit() {
		return
	}
	data := utf8.AppendRune(nil, r)
	if enc := v.opts.Encoding; enc != nil {
		var err error
		if data, err = enc.Encode(string(r)); err != nil || len(data) == 0 {
			return
		}
	}
	if err := v.dropSelection(); err != nil {
		v.report(err)
		return
	}
	off := v.caret
	var err error
	if v.insert {
		err = v.doc.Insert(off, data, typing)
	} else {
		err = v.doc.Overwrite(off, data, typing)
	}
	if err == nil {
		v.caret = off + int64(len(data))
	}
	v.report(err)
	v.anchor = v.caret
	v.digit = 0
	v.ensureCaretVisible()
	v.resetBlink()
	v.changed()
}

// DeleteForward removes the selection or the byte at the caret
func (v *View) DeleteForward() {
	if !v.canEdit() {
		return
	}
	start, n := v.Selection()
	if n == 0 {
		start, n = v.caret, 1
	}
	if start >= v.doc.Size() {
		return
	}
	v.report(v.doc.Delete(start, n, doc.EditOptions{}))
	v.caret, v.anchor, v.digit = start, start, 0
	v.moved(false)
}

// DeleteBackward removes the selection or the byte before the caret
func (v *View) DeleteBackward() {
	if !v.canEdit() {
		return
	}
	start, n := v.Selection()
	if n == 0 {
		if v.digit > 0 {
			// Typed in part: the byte is removed whole
			start, n = v.caret, 1
		} else if v.caret > 0 {
			start, n = v.caret-1, 1
		} else {
			return
		}
	}
	v.report(v.doc.Delete(start, n, doc.EditOptions{}))
	v.caret, v.anchor, v.digit = start, start, 0
	v.moved(false)
}

// Undo undoes the last edit and puts the caret where it was
func (v *View) Undo() {
	if v.busy {
		return
	}
	n, err := v.doc.Undo()
	if err != nil {
		return
	}
	v.caret, v.anchor, v.digit = n.Off, n.Off, 0
	v.moved(false)
}

// Redo makes the edit undone last again
func (v *View) Redo() {
	if v.busy {
		return
	}
	n, err := v.doc.Redo()
	if err != nil {
		return
	}
	v.caret, v.anchor, v.digit = n.Off+n.Len, n.Off+n.Len, 0
	v.moved(false)
}

// SelectAll selects the whole document
func (v *View) SelectAll() {
	v.anchor, v.caret, v.digit = 0, v.doc.Size(), 0
	v.changed()
}

// selectedBytes returns the bytes of the selection
func (v *View) selectedBytes() ([]byte, error) {
	start, n := v.Selection()
	if n > MaxClipboard {
		return nil, ErrTooLarge
	}
	buf := make([]byte, n)
	if _, err := v.doc.ReadAt(buf, start); err != nil && n > 0 {
		return buf, err
	}
	return buf, nil
}

// Copy copies the selection: as hex digits from the hex column, as text
// from the text column
func (v *View) Copy() {
	data, err := v.selectedBytes()
	if err != nil {
		v.report(err)
		return
	}
	if len(data) == 0 {
		return
	}
	if v.col == colNum {
		ui.ClipboardSetText(FormatHex(data))
	} else {
		ui.ClipboardSetText(v.bytesToText(data))
	}
}

// Cut copies and removes the selection
func (v *View) Cut() {
	if !v.canEdit() {
		return
	}
	v.Copy()
	if _, n := v.Selection(); n > 0 {
		v.DeleteForward()
	}
}

// Paste puts the clipboard at the caret: hex digits as bytes in the hex
// column, text as its bytes otherwise. In overwrite mode it overwrites.
func (v *View) Paste() {
	if !v.canEdit() {
		return
	}
	text, err := ui.ClipboardGetText()
	if err != nil || text == "" {
		return
	}
	var data []byte
	if v.col == colNum {
		if b, ok := ParseHex(text); ok {
			data = b
		}
	}
	if data == nil {
		data = []byte(text)
		if enc := v.opts.Encoding; enc != nil {
			if data, err = enc.Encode(text); err != nil {
				v.report(err)
				return
			}
		}
	}
	if err := v.dropSelection(); err != nil {
		v.report(err)
		return
	}
	off := v.caret
	opt := doc.EditOptions{Kind: doc.KindPaste}
	if v.insert {
		err = v.doc.Insert(off, data, opt)
	} else {
		err = v.doc.Overwrite(off, data, opt)
	}
	if err == nil {
		v.caret = off + int64(len(data))
	}
	v.report(err)
	v.digit = 0
	v.moved(false)
}

// FormatHex returns the bytes as hex digits separated by spaces: "4D 5A 90"
func FormatHex(data []byte) string {
	var sb strings.Builder
	sb.Grow(len(data) * 3)
	for i, b := range data {
		if i > 0 {
			sb.WriteByte(' ')
		}
		sb.WriteByte(digitChars[b>>4])
		sb.WriteByte(digitChars[b&15])
	}
	return sb.String()
}

// ParseHex reads hex digits, spaces and the "0x" prefixes between them;
// ok is false for anything else or an odd number of digits
func ParseHex(s string) ([]byte, bool) {
	var out []byte
	half, have := byte(0), false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case ' ', '\t', '\r', '\n', ',', '-', ':':
			if have {
				return nil, false // a byte is split
			}
			continue
		case '0':
			if !have && i+1 < len(s) && (s[i+1] == 'x' || s[i+1] == 'X') {
				i++
				continue
			}
		}
		d := digitValue(rune(c))
		if d < 0 {
			return nil, false
		}
		if have {
			out = append(out, half<<4|byte(d))
			have = false
		} else {
			half, have = byte(d), true
		}
	}
	if have || len(out) == 0 {
		return nil, false
	}
	return out, true
}

// bytesToText makes text of the bytes in the encoding of the text column;
// without one UTF-8 as it is, otherwise each byte a character (Latin-1)
func (v *View) bytesToText(data []byte) string {
	if enc := v.opts.Encoding; enc != nil {
		var sb strings.Builder
		for i := 0; i < len(data); {
			r, size, ok := enc.Decode(data, i)
			if size < 1 {
				size = 1
			}
			if ok {
				sb.WriteRune(r)
			} else {
				sb.WriteByte('.')
			}
			i += size
		}
		return sb.String()
	}
	if utf8.Valid(data) {
		return string(data)
	}
	rs := make([]rune, len(data))
	for i, b := range data {
		rs[i] = rune(b)
	}
	return string(rs)
}
