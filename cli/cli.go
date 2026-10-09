// Package cli is AltHex without a window (EXT-2): checksums, search and
// replace, strings, comparison, patches and conversion of formats, for
// scripts and for many files at once.
package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"

	"github.com/ipoluianov/althex/app"
	"github.com/ipoluianov/althex/config"
	"github.com/ipoluianov/althex/core/codec"
	"github.com/ipoluianov/althex/core/diff"
	"github.com/ipoluianov/althex/core/doc"
	checksum "github.com/ipoluianov/althex/core/hash"
	"github.com/ipoluianov/althex/core/patch"
	"github.com/ipoluianov/althex/core/search"
	"github.com/ipoluianov/althex/core/tmpl"
)

// command is a command of the command line
type command struct {
	name  string
	usage string
	run   func(ctx context.Context, args []string) error
}

var commands = []command{
	{"hash", "hash [-a crc32,sha256] [-offset N] [-length N] FILE...", runHash},
	{"find", "find (-hex \"4D 5A ??\" | -text STR [-enc ID] [-i] | -num int32:VALUE [-be]) [-max N] FILE...", runFind},
	{"replace", "replace (-hex OLD | -text OLD [-enc ID] [-i]) (-with-hex NEW | -with-text NEW) [-max N] [-o OUT] FILE", runReplace},
	{"strings", "strings [-min 4] [-utf16] [-utf8] FILE", runStrings},
	{"diff", "diff [-shifts] [-max N] FILE_A FILE_B", runDiff},
	{"patch-make", "patch-make -format ips|bps|ahp|hexdiff OLD NEW PATCH", runPatchMake},
	{"patch-apply", "patch-apply PATCH FILE OUT", runPatchApply},
	{"parse", "parse [-t TEMPLATE.bt] [-depth N] FILE", runParse},
	{"convert", "convert -from bin|ihex|srec|base64|array -to bin|ihex|srec|base64|c|go|python|rust [-addr N] IN OUT", runConvert},
}

// IsCommand tells whether the arguments ask for a command (not for files to open)
func IsCommand(args []string) bool {
	if len(args) == 0 {
		return false
	}
	switch args[0] {
	case "help", "-h", "--help", "-help", "version", "--version":
		return true
	}
	for _, c := range commands {
		if c.name == args[0] {
			return true
		}
	}
	return false
}

// Run runs the command of the arguments; returns the exit code
func Run(args []string) int {
	attachConsole()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	switch args[0] {
	case "help", "-h", "--help", "-help":
		usage(os.Stdout)
		return 0
	case "version", "--version":
		fmt.Println(app.DisplayName, app.Version)
		return 0
	}
	for _, c := range commands {
		if c.name == args[0] {
			if err := c.run(ctx, args[1:]); err != nil {
				fmt.Fprintln(os.Stderr, "althex "+c.name+":", err)
				if errors.Is(err, errNotFound) {
					return 1
				}
				return 2
			}
			return 0
		}
	}
	usage(os.Stderr)
	return 2
}

func usage(w io.Writer) {
	fmt.Fprintln(w, app.DisplayName, app.Version, "- hex editor; without a command it opens the files in a window")
	fmt.Fprintln(w, "\nCommands:")
	for _, c := range commands {
		fmt.Fprintln(w, "  althex "+c.usage)
	}
	fmt.Fprintln(w, "\nNumbers may be decimal or 0x hex. Exit code: 0 - done, 1 - nothing found, 2 - error.")
}

// errNotFound: the search found nothing (exit code 1, like grep)
var errNotFound = errors.New("not found")

// newFlags makes the flag set of a command; its errors go to stderr
func newFlags(name string) *flag.FlagSet {
	fs := flag.NewFlagSet("althex "+name, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	return fs
}

// openDoc opens a file read-only, without a journal
func openDoc(path string) (*doc.Document, error) {
	return doc.Open(path, doc.Options{ReadOnly: true})
}

// parseNum reads a number: decimal or 0x hex
func parseNum(s string) (int64, error) {
	var n int64
	_, err := fmt.Sscan(s, &n)
	if err != nil {
		if strings.HasPrefix(strings.ToLower(s), "0x") {
			_, err = fmt.Sscanf(s[2:], "%x", &n)
		}
	}
	if err != nil {
		return 0, fmt.Errorf("%q is not a number", s)
	}
	return n, nil
}

func runHash(ctx context.Context, args []string) error {
	fs := newFlags("hash")
	algs := fs.String("a", "crc32,md5,sha1,sha256", "algorithms (ids); \"list\" lists them")
	offS := fs.String("offset", "0", "the first byte")
	lenS := fs.String("length", "", "how many bytes; the rest of the file by default")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *algs == "list" {
		for _, a := range checksum.Algorithms() {
			fmt.Printf("%-16s %s\n", a.ID, a.Name)
		}
		for _, p := range checksum.CRCPresets() {
			fmt.Printf("%-16q %s\n", p.Name, p.Name)
		}
		return nil
	}
	var list []checksum.Algorithm
	for _, id := range strings.Split(*algs, ",") {
		id = strings.TrimSpace(id)
		if a, ok := checksum.ByID(id); ok {
			list = append(list, a)
		} else if p, ok := checksum.CRCPreset(id); ok {
			list = append(list, checksum.CRCAlgorithm(p))
		} else {
			return fmt.Errorf("unknown algorithm %q (see -a list)", id)
		}
	}
	off, err := parseNum(*offS)
	if err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return errors.New("no files")
	}
	for _, path := range fs.Args() {
		d, err := openDoc(path)
		if err != nil {
			return err
		}
		n := d.Size() - off
		if *lenS != "" {
			if n, err = parseNum(*lenS); err != nil {
				d.Close()
				return err
			}
		}
		sums, err := checksum.Compute(ctx, d.Snapshot(), off, n, list, nil)
		d.Close()
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		for i, a := range list {
			fmt.Printf("%s  %s  %s\n", hex.EncodeToString(sums[i]), a.ID, path)
		}
	}
	return nil
}

// patternFlags are the ways to give a pattern on the command line
type patternFlags struct {
	hex, text, num, enc *string
	fold, be            *bool
}

func addPatternFlags(fs *flag.FlagSet, prefix string) patternFlags {
	return patternFlags{
		hex:  fs.String(prefix+"hex", "", "hex bytes, ?? for any byte: \"4D 5A ?? ?? 50 45\""),
		text: fs.String(prefix+"text", "", "text"),
		num:  fs.String(prefix+"num", "", "a number: int8..int64, uint8..uint64, float, double: \"int32:1234\""),
		enc:  fs.String(prefix+"enc", "utf8", "encoding of the text (ascii, utf8, utf16le, cp1251...)"),
		fold: fs.Bool(prefix+"i", false, "ignore case"),
		be:   fs.Bool(prefix+"be", false, "numbers big-endian"),
	}
}

func (p patternFlags) pattern() (search.Pattern, error) {
	switch {
	case *p.hex != "":
		return search.ParseHex(*p.hex)
	case *p.text != "":
		enc := codec.ByID(*p.enc)
		if enc == nil {
			return search.Pattern{}, fmt.Errorf("unknown encoding %q", *p.enc)
		}
		return search.Text(*p.text, enc.Encode, *p.fold)
	case *p.num != "":
		kind, value, ok := strings.Cut(*p.num, ":")
		k, known := search.ParseNumKind(kind)
		if !ok || !known {
			return search.Pattern{}, fmt.Errorf("%q: type:value expected, e.g. int32:1234", *p.num)
		}
		return search.Number(k, value, *p.be)
	}
	return search.Pattern{}, errors.New("nothing to find: -hex, -text or -num")
}

func runFind(ctx context.Context, args []string) error {
	fs := newFlags("find")
	pf := addPatternFlags(fs, "")
	maxHits := fs.Int("max", 0, "stop after so many in a file")
	if err := fs.Parse(args); err != nil {
		return err
	}
	p, err := pf.pattern()
	if err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return errors.New("no files")
	}
	found := false
	out := bufio.NewWriter(os.Stdout)
	defer out.Flush()
	for _, path := range fs.Args() {
		d, err := openDoc(path)
		if err != nil {
			return err
		}
		snap := d.Snapshot()
		err = search.FindAll(ctx, snap, snap.Size(), p, search.Options{MaxHits: *maxHits}, func(h search.Hit) bool {
			found = true
			fmt.Fprintf(out, "%s:0x%X\n", path, h.Off)
			return true
		}, nil)
		d.Close()
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
	}
	if !found {
		return errNotFound
	}
	return nil
}

func runReplace(ctx context.Context, args []string) error {
	fs := newFlags("replace")
	pf := addPatternFlags(fs, "")
	withHex := fs.String("with-hex", "", "the new bytes in hex")
	withText := fs.String("with-text", "", "the new text (in the encoding of -enc)")
	maxHits := fs.Int("max", 0, "replace so many at most")
	out := fs.String("o", "", "write to this file; the file itself by default")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *pf.num != "" {
		return errors.New("-num cannot be replaced; use -hex")
	}
	p, err := pf.pattern()
	if err != nil {
		return err
	}
	var repl []byte
	switch {
	case *withHex != "":
		if repl, err = codec.ParseHex(*withHex); err != nil {
			return err
		}
	case *withText != "":
		enc := codec.ByID(*pf.enc)
		if enc == nil {
			return fmt.Errorf("unknown encoding %q", *pf.enc)
		}
		if repl, err = enc.Encode(*withText); err != nil {
			return err
		}
	default:
		return errors.New("the new bytes: -with-hex or -with-text")
	}
	if fs.NArg() != 1 {
		return errors.New("one file expected")
	}
	path := fs.Arg(0)
	d, err := doc.Open(path, doc.Options{})
	if err != nil {
		return err
	}
	defer d.Close()
	snap := d.Snapshot()
	var hits []search.Hit
	next := int64(0)
	err = search.FindAll(ctx, snap, snap.Size(), p, search.Options{}, func(h search.Hit) bool {
		// Overlapping matches: the first one wins
		if h.Off < next {
			return true
		}
		hits = append(hits, h)
		next = h.Off + int64(h.Len)
		return *maxHits == 0 || len(hits) < *maxHits
	}, nil)
	if err != nil {
		return err
	}
	if len(hits) == 0 {
		return errNotFound
	}
	// From the end, so the offsets of the ones before do not move
	for i := len(hits) - 1; i >= 0; i-- {
		h := hits[i]
		if len(repl) == h.Len {
			err = d.Overwrite(h.Off, repl, doc.EditOptions{})
		} else {
			if err = d.Delete(h.Off, int64(h.Len), doc.EditOptions{}); err == nil {
				err = d.Insert(h.Off, repl, doc.EditOptions{})
			}
		}
		if err != nil {
			return err
		}
	}
	if err := d.Save(doc.SaveOptions{Path: *out}); err != nil {
		return err
	}
	fmt.Printf("%d replaced\n", len(hits))
	return nil
}

func runStrings(ctx context.Context, args []string) error {
	fs := newFlags("strings")
	minLen := fs.Int("min", 4, "the shortest string")
	utf16 := fs.Bool("utf16", false, "also UTF-16 LE strings")
	utf8 := fs.Bool("utf8", false, "also UTF-8 strings (not only ASCII)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return errors.New("no files")
	}
	out := bufio.NewWriter(os.Stdout)
	defer out.Flush()
	for _, path := range fs.Args() {
		d, err := openDoc(path)
		if err != nil {
			return err
		}
		snap := d.Snapshot()
		opt := search.StringsOptions{MinLen: *minLen, ASCII: true, UTF8: *utf8, UTF16LE: *utf16}
		err = search.Strings(ctx, snap, snap.Size(), opt, func(f search.Found) bool {
			fmt.Fprintf(out, "%X\t%s\t%s\n", f.Off, f.Enc, f.Text)
			return true
		}, nil)
		d.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func runDiff(ctx context.Context, args []string) error {
	fs := newFlags("diff")
	shifts := fs.Bool("shifts", false, "find inserted and deleted bytes")
	maxRanges := fs.Int("max", 0, "stop after so many differences")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 2 {
		return errors.New("two files expected")
	}
	a, err := openDoc(fs.Arg(0))
	if err != nil {
		return err
	}
	defer a.Close()
	b, err := openDoc(fs.Arg(1))
	if err != nil {
		return err
	}
	defer b.Close()
	sa, sb := a.Snapshot(), b.Snapshot()
	rs, err := diff.Compare(ctx, sa, sa.Size(), sb, sb.Size(), diff.Options{Shifts: *shifts, MaxRanges: *maxRanges}, nil)
	if err != nil {
		return err
	}
	out := bufio.NewWriter(os.Stdout)
	defer out.Flush()
	if err := diff.WriteReport(out, rs, sa, sb, 16); err != nil {
		return err
	}
	if diff.Summarize(rs).Ranges == 0 {
		return nil
	}
	return nil
}

func formatByName(name string) (patch.Format, error) {
	for _, f := range patch.Formats() {
		if strings.EqualFold(f.String(), name) || strings.EqualFold(strings.TrimPrefix(f.Ext(), "."), name) {
			return f, nil
		}
	}
	return 0, fmt.Errorf("unknown patch format %q", name)
}

func runPatchMake(ctx context.Context, args []string) error {
	fs := newFlags("patch-make")
	format := fs.String("format", "bps", "ips, bps, ahp or hexdiff")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 3 {
		return errors.New("OLD NEW PATCH expected")
	}
	f, err := formatByName(*format)
	if err != nil {
		return err
	}
	src, err := openDoc(fs.Arg(0))
	if err != nil {
		return err
	}
	defer src.Close()
	dst, err := openDoc(fs.Arg(1))
	if err != nil {
		return err
	}
	defer dst.Close()
	var buf bytes.Buffer
	ss, ds := src.Snapshot(), dst.Snapshot()
	if err := patch.Make(ctx, f, &buf, ss, ss.Size(), ds, ds.Size(), nil); err != nil {
		return err
	}
	return os.WriteFile(fs.Arg(2), buf.Bytes(), 0644)
}

func runPatchApply(ctx context.Context, args []string) error {
	fs := newFlags("patch-apply")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 3 {
		return errors.New("PATCH FILE OUT expected")
	}
	pf, err := os.Open(fs.Arg(0))
	if err != nil {
		return err
	}
	defer pf.Close()
	head := make([]byte, 64)
	n, _ := io.ReadFull(pf, head)
	f, ok := patch.Detect(head[:n])
	if !ok {
		return errors.New("not a patch of a known format")
	}
	if _, err := pf.Seek(0, io.SeekStart); err != nil {
		return err
	}
	src, err := openDoc(fs.Arg(1))
	if err != nil {
		return err
	}
	defer src.Close()
	out, err := os.Create(fs.Arg(2))
	if err != nil {
		return err
	}
	w := bufio.NewWriterSize(out, 1<<20)
	ss := src.Snapshot()
	err = patch.Apply(ctx, f, bufio.NewReader(pf), w, ss, ss.Size())
	if err == nil {
		err = w.Flush()
	}
	if closeErr := out.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		os.Remove(fs.Arg(2))
	}
	return err
}

// runParse applies a template (the one of the format by default) and
// prints the tree of the fields: path, offset, size, value
func runParse(ctx context.Context, args []string) error {
	fs := newFlags("parse")
	tmplPath := fs.String("t", "", "a template file; the one of the format by default")
	depth := fs.Int("depth", 0, "print so many levels; 0 - all")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("one file expected")
	}
	d, err := openDoc(fs.Arg(0))
	if err != nil {
		return err
	}
	defer d.Close()
	var t *tmpl.Template
	if *tmplPath != "" {
		src, err := os.ReadFile(*tmplPath)
		if err != nil {
			return err
		}
		p, err := tmpl.Compile(*tmplPath, string(src))
		if err != nil {
			return err
		}
		return printTree(ctx, p, d, *depth)
	}
	all := append(tmpl.Library(), tmpl.LoadDir(config.TemplatesDirectory())...)
	if t = tmpl.Detect(all, d, fs.Arg(0)); t == nil {
		return errors.New("no template fits the file; give one with -t")
	}
	p, err := t.Program()
	if err != nil {
		return err
	}
	return printTree(ctx, p, d, *depth)
}

func printTree(ctx context.Context, p *tmpl.Program, d *doc.Document, depth int) error {
	res := p.Run(ctx, tmpl.Env{Data: d.Snapshot()})
	out := bufio.NewWriter(os.Stdout)
	defer out.Flush()
	for _, line := range res.Output {
		fmt.Fprintln(out, "#", line)
	}
	var walk func(n *tmpl.Node, level int)
	walk = func(n *tmpl.Node, level int) {
		if depth > 0 && level >= depth {
			return
		}
		count := n.ChildCount()
		for i := int64(0); i < count; i++ {
			ch := n.Child(i)
			if ch == nil {
				continue
			}
			fmt.Fprintf(out, "%s%s\t0x%X\t%d\t%s\n", strings.Repeat("  ", level), ch.Name, ch.Off, ch.Size, ch.Value())
			// The elements of arrays of simple types are not listed: there may be millions
			if ch.Kind != tmpl.NodeArray {
				walk(ch, level+1)
			}
		}
	}
	walk(res.Root, 0)
	if res.Err != nil {
		return fmt.Errorf("%s: %w", p.Meta().Name, res.Err)
	}
	return nil
}

func runConvert(ctx context.Context, args []string) error {
	fs := newFlags("convert")
	from := fs.String("from", "bin", "bin, ihex, srec, base64 or array")
	to := fs.String("to", "bin", "bin, ihex, srec, base64, c, go, python, rust, java, js, pascal")
	addrS := fs.String("addr", "0", "the address of the first byte (ihex, srec)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 2 {
		return errors.New("IN OUT expected")
	}
	data, err := os.ReadFile(fs.Arg(0))
	if err != nil {
		return err
	}
	addr, err := parseNum(*addrS)
	if err != nil {
		return err
	}
	var segs []codec.Segment
	switch *from {
	case "bin":
		segs = []codec.Segment{{Addr: uint64(addr), Data: data}}
	case "ihex":
		segs, err = codec.ParseIntelHex(bytes.NewReader(data))
	case "srec":
		segs, err = codec.ParseSRecord(bytes.NewReader(data))
	case "base64":
		var b []byte
		b, err = codec.ParseBase64(string(data))
		segs = []codec.Segment{{Addr: uint64(addr), Data: b}}
	case "array":
		var b []byte
		b, err = codec.ParseArray(string(data))
		segs = []codec.Segment{{Addr: uint64(addr), Data: b}}
	default:
		return fmt.Errorf("unknown input format %q", *from)
	}
	if err != nil {
		return err
	}
	var out bytes.Buffer
	flat := func() []byte {
		_, b := codec.Flatten(segs, 0xFF)
		return b
	}
	switch *to {
	case "bin":
		out.Write(flat())
	case "ihex":
		err = codec.WriteIntelHex(&out, segs, 16)
	case "srec":
		err = codec.WriteSRecord(&out, segs, 16, "")
	case "base64":
		out.WriteString(codec.FormatBase64(flat(), 76) + "\n")
	default:
		found := false
		for _, lang := range codec.Langs() {
			if strings.EqualFold(lang.String(), *to) {
				out.WriteString(codec.FormatArray(flat(), lang, "data", 16) + "\n")
				found = true
			}
		}
		if !found {
			return fmt.Errorf("unknown output format %q", *to)
		}
	}
	if err != nil {
		return err
	}
	return os.WriteFile(fs.Arg(1), out.Bytes(), 0644)
}
