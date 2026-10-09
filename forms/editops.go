package forms

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/ipoluianov/althex/core/codec"
	"github.com/ipoluianov/althex/core/doc"
	"github.com/ipoluianov/althex/core/ops"
	"github.com/ipoluianov/althex/forms/hexview"
	"github.com/ipoluianov/nui/ui"
)

// target returns the range an operation works on: the selection, or the
// whole document when nothing is selected
func target(v *hexview.View) (off, n int64) {
	if off, n = v.Selection(); n > 0 {
		return off, n
	}
	return 0, v.Document().Size()
}

// runOp runs a long edit of the current document in the background as one
// edit of the history, with the progress in the status bar
func (c *MainForm) runOp(kind string, total int64, work func(ctx context.Context, d *doc.Document, progress func(done int64)) error) {
	t := c.tab()
	if t == nil || c.saving {
		return
	}
	d := t.doc
	if d.ReadOnly() {
		ui.ShowToast(c, T().ReadOnly, ui.ToastError)
		return
	}
	form := c.Form()
	c.saving = true // nothing else runs meanwhile
	for _, v := range t.views {
		v.SetBusy(true)
	}
	d.BeginGroup(kind)
	var lastShown atomic.Int64
	progress := func(done int64) {
		now := time.Now().UnixMilli()
		if now-lastShown.Load() < 100 || total == 0 {
			return
		}
		lastShown.Store(now)
		pct := int(done * 100 / total)
		form.Invoke(func() { c.status.SetInfo(T().Working(pct)) })
	}
	go func() {
		err := work(context.Background(), d, progress)
		form.Invoke(func() {
			d.EndGroup()
			c.saving = false
			for _, v := range t.views {
				v.SetBusy(false)
			}
			if err != nil {
				ui.ShowMessageBox(c, T().Error, err.Error())
			}
			c.updateTabs()
			c.updateStatus()
		})
	}()
}

// askBytes asks for bytes as hex digits ("FF 00") or "text" in quotes
func (c *MainForm) askBytes(title string, then func(b []byte)) {
	c.ShowDialog(NewValueDialog(title, T().BytesLabel, "FF", func(s string) error {
		b, err := parseBytes(s)
		if err != nil {
			return err
		}
		then(b)
		return nil
	}))
}

// parseBytes reads hex digits, or a text in quotes as its UTF-8
func parseBytes(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && (s[0] == '"' || s[0] == '\'') && s[len(s)-1] == s[0] {
		return []byte(s[1 : len(s)-1]), nil
	}
	b, err := codec.ParseHex(s)
	if err != nil || len(b) == 0 {
		return nil, errors.New(T().BytesBad)
	}
	return b, nil
}

func (c *MainForm) fillDialog() {
	v := c.view()
	if v == nil {
		return
	}
	off, n := target(v)
	c.ShowDialog(NewFillDialog(func(f ops.Fill) {
		c.runOp(doc.KindFill, n, func(ctx context.Context, d *doc.Document, progress func(int64)) error {
			return ops.DoFill(ctx, d, off, n, f, progress)
		})
	}))
}

// transform applies the operation to the selection (or the whole document)
func (c *MainForm) transform(t ops.Transform) {
	v := c.view()
	if v == nil {
		return
	}
	off, n := target(v)
	c.runOp("transform", n, func(ctx context.Context, d *doc.Document, progress func(int64)) error {
		return ops.DoTransform(ctx, d, off, n, t, progress)
	})
}

// transformWith asks for the operand, then applies the operation
func (c *MainForm) transformWith(op ops.Op, title func() string) func() {
	return func() {
		c.askBytes(title(), func(b []byte) { c.transform(ops.Transform{Op: op, Operand: b}) })
	}
}

// transformBits asks for the number of bits, then applies the operation
func (c *MainForm) transformBits(op ops.Op, title func() string) func() {
	return func() {
		c.ShowDialog(NewValueDialog(title(), T().BitsLabel, "1", func(s string) error {
			k, err := strconv.Atoi(strings.TrimSpace(s))
			if err != nil || k < 0 || k > 7 {
				return errors.New(T().BitsBad)
			}
			c.transform(ops.Transform{Op: op, Bits: k})
			return nil
		}))
	}
}

func (c *MainForm) registerEditOps() {
	t := T
	c.cmd("edit.fill", func() string { return t().MenuFill }, "Ctrl+Shift+F", c.fillDialog)
	c.cmd("ops.and", func() string { return "AND..." }, "", c.transformWith(ops.OpAnd, fixed("AND")))
	c.cmd("ops.or", func() string { return "OR..." }, "", c.transformWith(ops.OpOr, fixed("OR")))
	c.cmd("ops.xor", func() string { return "XOR..." }, "", c.transformWith(ops.OpXor, fixed("XOR")))
	c.cmd("ops.not", func() string { return "NOT" }, "", func() { c.transform(ops.Transform{Op: ops.OpNot}) })
	c.cmd("ops.add", func() string { return t().OpAdd }, "", c.transformWith(ops.OpAdd, func() string { return t().OpAdd }))
	c.cmd("ops.sub", func() string { return t().OpSub }, "", c.transformWith(ops.OpSub, func() string { return t().OpSub }))
	c.cmd("ops.shl", func() string { return t().OpShl }, "", c.transformBits(ops.OpShl, func() string { return t().OpShl }))
	c.cmd("ops.shr", func() string { return t().OpShr }, "", c.transformBits(ops.OpShr, func() string { return t().OpShr }))
	c.cmd("ops.rol", func() string { return t().OpRol }, "", c.transformBits(ops.OpRol, func() string { return t().OpRol }))
	c.cmd("ops.ror", func() string { return t().OpRor }, "", c.transformBits(ops.OpRor, func() string { return t().OpRor }))
	for _, w := range []int{2, 4, 8} {
		c.cmd(fmt.Sprintf("ops.swap%d", w), func() string { return t().OpSwap(w) }, "", func() {
			c.transform(ops.Transform{Op: ops.OpSwap, Width: w})
		})
	}
	c.cmd("ops.reverse", func() string { return t().OpReverse }, "", func() { c.transform(ops.Transform{Op: ops.OpReverse}) })

	// Copy as (EDIT-7)
	c.cmd("copy.hex", func() string { return t().CopyHex }, "Ctrl+Shift+C", c.copyAs(func(b []byte) string { return codec.FormatHex(b, " ") }))
	c.cmd("copy.hexPlain", func() string { return t().CopyHexPlain }, "", c.copyAs(func(b []byte) string { return codec.FormatHex(b, "") }))
	c.cmd("copy.base64", func() string { return "Base64" }, "", c.copyAs(func(b []byte) string { return codec.FormatBase64(b, 76) }))
	c.cmd("copy.escaped", func() string { return t().CopyEscaped }, "", c.copyAs(func(b []byte) string { return `"` + codec.FormatEscaped(b) + `"` }))
	for _, lang := range codec.Langs() {
		c.cmd("copy.lang."+lang.String(), fixed(lang.String()), "", c.copyAs(func(b []byte) string {
			return codec.FormatArray(b, lang, "data", 16)
		}))
	}

	// Import and export (SRC-8)
	c.cmd("import.ihex", func() string { return "Intel HEX..." }, "", func() { c.importFile(importIntelHex) })
	c.cmd("import.srec", func() string { return "Motorola S-record..." }, "", func() { c.importFile(importSRecord) })
	c.cmd("import.base64", func() string { return "Base64..." }, "", func() { c.importFile(importBase64) })
	c.cmd("import.array", func() string { return t().ImportArray }, "", func() { c.importFile(importArray) })
	c.cmd("export.ihex", func() string { return "Intel HEX..." }, "", func() { c.exportFile(".hex", exportIntelHex) })
	c.cmd("export.srec", func() string { return "Motorola S-record..." }, "", func() { c.exportFile(".s19", exportSRecord) })
	c.cmd("export.base64", func() string { return "Base64..." }, "", func() {
		c.exportFile(".b64", func(b []byte, base uint64) ([]byte, error) { return []byte(codec.FormatBase64(b, 76) + "\n"), nil })
	})
	for _, lang := range codec.Langs() {
		c.cmd("export.lang."+lang.String(), fixed(lang.String()+"..."), "", func() {
			c.exportFile(langExt(lang), func(b []byte, base uint64) ([]byte, error) {
				return []byte(codec.FormatArray(b, lang, "data", 16) + "\n"), nil
			})
		})
	}
}

func (c *MainForm) buildEditOpsMenu(edit *ui.ContextMenu) {
	t := T
	var langs, exportLangs []string
	for _, lang := range codec.Langs() {
		langs = append(langs, "copy.lang."+lang.String())
		exportLangs = append(exportLangs, "export.lang."+lang.String())
	}
	copyAs := append([]string{"copy.hex", "copy.hexPlain", "copy.base64", "copy.escaped", "-"}, langs...)
	c.subMenu(edit, func() string { return t().MenuCopyAs }, copyAs...)
	c.addItems(edit, "-", "edit.fill")
	c.subMenu(edit, func() string { return t().MenuBitOps },
		"ops.and", "ops.or", "ops.xor", "ops.not", "-", "ops.add", "ops.sub", "-",
		"ops.shl", "ops.shr", "ops.rol", "ops.ror", "-", "ops.swap2", "ops.swap4", "ops.swap8", "ops.reverse")
	_ = exportLangs
}

func (c *MainForm) buildImportExportMenu(file *ui.ContextMenu) {
	t := T
	var exportLangs []string
	for _, lang := range codec.Langs() {
		exportLangs = append(exportLangs, "export.lang."+lang.String())
	}
	c.subMenu(file, func() string { return t().MenuImport }, "import.ihex", "import.srec", "import.base64", "import.array")
	c.subMenu(file, func() string { return t().MenuExport },
		append([]string{"export.ihex", "export.srec", "export.base64", "-"}, exportLangs...)...)
}

// copyAs copies the selection made into text by format
func (c *MainForm) copyAs(format func(b []byte) string) func() {
	return func() {
		v := c.view()
		if v == nil {
			return
		}
		off, n := v.Selection()
		if n == 0 {
			return
		}
		if n > hexview.MaxClipboard {
			ui.ShowToast(c, hexview.ErrTooLarge.Error(), ui.ToastError)
			return
		}
		b := make([]byte, n)
		v.Document().ReadAt(b, off)
		ui.ClipboardSetText(format(b))
	}
}

// maxImport is the most bytes an imported file of address records may span
const maxImport = 1 << 30

func importIntelHex(data []byte) ([]byte, uint64, error) {
	segs, err := codec.ParseIntelHex(bytes.NewReader(data))
	return flatten(segs, err)
}

func importSRecord(data []byte) ([]byte, uint64, error) {
	segs, err := codec.ParseSRecord(bytes.NewReader(data))
	return flatten(segs, err)
}

func flatten(segs []codec.Segment, err error) ([]byte, uint64, error) {
	if err != nil {
		return nil, 0, err
	}
	lo, hi := codec.Bounds(segs)
	if hi-lo > maxImport {
		return nil, 0, fmt.Errorf("the records span %d bytes", hi-lo)
	}
	base, b := codec.Flatten(segs, 0xFF)
	return b, base, nil
}

func importBase64(data []byte) ([]byte, uint64, error) {
	b, err := codec.ParseBase64(string(data))
	return b, 0, err
}

func importArray(data []byte) ([]byte, uint64, error) {
	b, err := codec.ParseArray(string(data))
	return b, 0, err
}

// importFile reads a file in a text format into a new document; the address
// of the first byte is told (the gaps are filled with FF)
func (c *MainForm) importFile(parse func(data []byte) ([]byte, uint64, error)) {
	c.Form().ShowOpenFileDialog(ui.OpenFileDialogOptions{Title: T().MenuImport},
		func(paths []string, err error) {
			if err != nil || len(paths) == 0 {
				return
			}
			data, err := os.ReadFile(paths[0])
			if err != nil {
				ui.ShowMessageBox(c, T().Error, err.Error())
				return
			}
			b, base, err := parse(data)
			if err != nil {
				ui.ShowMessageBox(c, T().Error, err.Error())
				return
			}
			d, err := doc.FromBytes(b, docOptions())
			if err != nil {
				ui.ShowMessageBox(c, T().Error, err.Error())
				return
			}
			c.addTab(d)
			if base != 0 {
				ui.ShowToast(c, T().ImportedAt(base), ui.ToastInfo)
			}
		})
}

func exportIntelHex(b []byte, base uint64) ([]byte, error) {
	var out bytes.Buffer
	err := codec.WriteIntelHex(&out, []codec.Segment{{Addr: base, Data: b}}, 16)
	return out.Bytes(), err
}

func exportSRecord(b []byte, base uint64) ([]byte, error) {
	var out bytes.Buffer
	err := codec.WriteSRecord(&out, []codec.Segment{{Addr: base, Data: b}}, 16, "")
	return out.Bytes(), err
}

func langExt(l codec.Lang) string {
	switch l {
	case codec.LangGo:
		return ".go"
	case codec.LangPython:
		return ".py"
	case codec.LangRust:
		return ".rs"
	case codec.LangJava:
		return ".java"
	case codec.LangJS:
		return ".js"
	case codec.LangPascal:
		return ".pas"
	}
	return ".c"
}

// exportFile writes the selection (or the whole document) to a file in a
// format; the formats of addresses ask for the address of the first byte
func (c *MainForm) exportFile(ext string, format func(b []byte, base uint64) ([]byte, error)) {
	v := c.view()
	if v == nil {
		return
	}
	off, n := target(v)
	if n > maxImport {
		ui.ShowMessageBox(c, T().Error, hexview.ErrTooLarge.Error())
		return
	}
	write := func(base uint64) {
		name := strings.TrimSuffix(c.tab().name(), filepath.Ext(c.tab().name())) + ext
		c.Form().ShowSaveFileDialog(ui.SaveFileDialogOptions{Title: T().MenuExport, DefaultFileName: name},
			func(path string, err error) {
				if err != nil || path == "" {
					return
				}
				b := make([]byte, n)
				v.Document().ReadAt(b, off)
				out, err := format(b, base)
				if err == nil {
					err = os.WriteFile(path, out, 0644)
				}
				if err != nil {
					ui.ShowMessageBox(c, T().Error, err.Error())
				}
			})
	}
	if ext != ".hex" && ext != ".s19" {
		write(0)
		return
	}
	c.ShowDialog(NewValueDialog(T().MenuExport, T().AddressLabel, "0x"+strconv.FormatInt(off, 16), func(s string) error {
		base, ok := ParseOffset(s)
		if !ok {
			return errors.New(T().BytesBad)
		}
		write(uint64(base))
		return nil
	}))
}
