package cli

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// run runs a command as "althex ARGS" with the input, returning what it
// printed and its exit code
func run(t *testing.T, stdin string, args ...string) (string, int) {
	t.Helper()
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	inR, inW, _ := os.Pipe()
	oldOut, oldErr, oldIn := os.Stdout, os.Stderr, os.Stdin
	os.Stdout, os.Stderr, os.Stdin = outW, outW, inR
	go func() {
		io.WriteString(inW, stdin)
		inW.Close()
	}()
	done := make(chan string)
	go func() {
		b, _ := io.ReadAll(outR)
		done <- string(b)
	}()
	code := Run(args)
	os.Stdout, os.Stderr, os.Stdin = oldOut, oldErr, oldIn
	outW.Close()
	inR.Close()
	return <-done, code
}

func writeFile(t *testing.T, dir, name string, data []byte) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestInfoCommands(t *testing.T) {
	if out, code := run(t, "", "version"); code != 0 || !strings.Contains(out, "AltHex") {
		t.Errorf("version: %d %q", code, out)
	}
	if out, code := run(t, "", "help"); code != 0 || !strings.Contains(out, "althex mcp") || !strings.Contains(out, "althex hash") {
		t.Errorf("help: %d %q", code, out)
	}
	if out, code := run(t, "", "licenses"); code != 0 || !strings.Contains(out, "MIT License") || !strings.Contains(out, "Apache License") {
		t.Errorf("licenses: %d, %d bytes", code, len(out))
	}
	if !IsCommand([]string{"hash"}) || IsCommand([]string{"some.bin"}) || IsCommand(nil) {
		t.Error("IsCommand")
	}
	if _, code := run(t, "", "hash"); code != 2 {
		t.Errorf("hash without files: %d", code)
	}
}

func TestHashFind(t *testing.T) {
	dir := t.TempDir()
	f := writeFile(t, dir, "a.bin", []byte("xx123456789yy\x78\x56\x34\x12"))
	out, code := run(t, "", "hash", "-a", "crc32,sha256", "-offset", "2", "-length", "9", f)
	if code != 0 || !strings.Contains(out, "cbf43926") || !strings.Contains(out, "15e2b0d3c33891ebb0f1ef609ec419420c20e320ce94c65fbc8c3312448eb225") {
		t.Errorf("hash: %d %q", code, out)
	}
	// A CRC of one's own: CRC-16/CCITT-FALSE of "123456789" is 29B1
	out, code = run(t, "", "hash", "-a", "md5", "-offset", "2", "-length", "9", "-crc", "width=16 poly=0x1021 init=0xFFFF refin=false refout=false xorout=0", f)
	if code != 0 || !strings.Contains(out, "29b1") {
		t.Errorf("hash -crc: %d %q", code, out)
	}
	if _, code = run(t, "", "hash", "-crc", "poly=1", f); code != 2 {
		t.Errorf("bad -crc: %d", code)
	}
	if out, code = run(t, "", "hash", "-a", "list"); code != 0 || !strings.Contains(out, "blake3") {
		t.Errorf("list: %d", code)
	}
	if out, code = run(t, "", "find", "-text", "456", f); code != 0 || !strings.Contains(strings.ToLower(out), "0x5") {
		t.Errorf("find text: %d %q", code, out)
	}
	if out, code = run(t, "", "find", "-hex", "79 ?? 78", f); code != 0 || !strings.Contains(strings.ToLower(out), "0xb") {
		t.Errorf("find hex: %d %q", code, out)
	}
	if out, code = run(t, "", "find", "-num", "int32:0x12345678", f); code != 0 || !strings.Contains(strings.ToLower(out), "0xd") {
		t.Errorf("find number: %d %q", code, out)
	}
	if _, code = run(t, "", "find", "-text", "absent", f); code != 1 {
		t.Errorf("nothing found must exit 1: %d", code)
	}
}

func TestReplaceStringsDiff(t *testing.T) {
	dir := t.TempDir()
	f := writeFile(t, dir, "a.bin", []byte("\x00\x01Hello world\x00\x02Hello again\x00"))
	out := filepath.Join(dir, "b.bin")
	if o, code := run(t, "", "replace", "-text", "Hello", "-with-text", "HELLO", "-o", out, f); code != 0 {
		t.Fatalf("replace: %d %q", code, o)
	}
	b, _ := os.ReadFile(out)
	if !bytes.Equal(b, []byte("\x00\x01HELLO world\x00\x02HELLO again\x00")) {
		t.Errorf("replaced: %q", b)
	}
	if o, code := run(t, "", "strings", "-min", "5", f); code != 0 || !strings.Contains(o, "Hello world") || !strings.Contains(o, "Hello again") {
		t.Errorf("strings: %d %q", code, o)
	}
	o, code := run(t, "", "diff", f, out)
	if code == 2 || !strings.Contains(o, "0x00000003+4") || !strings.Contains(o, "Differing ranges: 2") {
		t.Errorf("diff: %d %q", code, o)
	}
	if _, code := run(t, "", "diff", f, f); code == 2 {
		t.Errorf("diff of the same file: %d", code)
	}
}

func TestPatchRoundTrip(t *testing.T) {
	dir := t.TempDir()
	old := bytes.Repeat([]byte("0123456789abcdef"), 64)
	newer := append([]byte(nil), old...)
	copy(newer[100:], "PATCHED")
	newer = append(newer, "tail"...)
	a := writeFile(t, dir, "old.bin", old)
	b := writeFile(t, dir, "new.bin", newer)
	for _, format := range []string{"ips", "bps", "ahp", "hexdiff"} {
		p := filepath.Join(dir, "p."+format)
		if o, code := run(t, "", "patch-make", "-format", format, a, b, p); code != 0 {
			t.Fatalf("%s make: %d %q", format, code, o)
		}
		out := filepath.Join(dir, "out."+format)
		if o, code := run(t, "", "patch-apply", p, a, out); code != 0 {
			t.Fatalf("%s apply: %d %q", format, code, o)
		}
		got, _ := os.ReadFile(out)
		if !bytes.Equal(got, newer) {
			t.Errorf("%s: the patched file differs", format)
		}
	}
}

func TestParseConvert(t *testing.T) {
	dir := t.TempDir()
	bmp := make([]byte, 58)
	copy(bmp, "BM")
	bmp[2], bmp[10], bmp[14], bmp[18], bmp[22], bmp[26], bmp[28] = 58, 54, 40, 1, 1, 1, 32
	f := writeFile(t, dir, "x.bmp", bmp)
	if o, code := run(t, "", "parse", "-depth", "2", f); code != 0 || !strings.Contains(o, "width") {
		t.Errorf("parse: %d %q", code, o)
	}
	bin := writeFile(t, dir, "fw.bin", []byte("firmware bytes \x00\x01\x02"))
	hex := filepath.Join(dir, "fw.hex")
	if o, code := run(t, "", "convert", "-from", "bin", "-to", "ihex", "-addr", "0x8000000", bin, hex); code != 0 {
		t.Fatalf("to ihex: %d %q", code, o)
	}
	back := filepath.Join(dir, "back.bin")
	if o, code := run(t, "", "convert", "-from", "ihex", "-to", "bin", hex, back); code != 0 {
		t.Fatalf("from ihex: %d %q", code, o)
	}
	a, _ := os.ReadFile(bin)
	b, _ := os.ReadFile(back)
	if !bytes.Equal(a, b) {
		t.Errorf("ihex round trip: % X", b)
	}
	c := filepath.Join(dir, "fw.c")
	if o, code := run(t, "", "convert", "-from", "bin", "-to", "c", bin, c); code != 0 {
		t.Fatalf("to c: %d %q", code, o)
	}
	if src, _ := os.ReadFile(c); !strings.Contains(string(src), "0x66") {
		t.Errorf("c array: %s", src)
	}
}

func TestExternalCommands(t *testing.T) {
	dir := t.TempDir()
	f := writeFile(t, dir, "a.bin", []byte("Hello, AltHex"))
	req := `{"jsonrpc":"2.0","id":1,"method":"read","params":{"offset":7,"length":6,"format":"text"}}` + "\n"
	if o, code := run(t, req, "rpc", "-headless", f); code != 0 || !strings.Contains(o, "AltHex") {
		t.Errorf("rpc: %d %q", code, o)
	}
	init := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}` + "\n"
	call := `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"list_documents","arguments":{}}}` + "\n"
	if o, code := run(t, init+call, "mcp", "-headless", f); code != 0 || !strings.Contains(o, "a.bin") {
		t.Errorf("mcp: %d %q", code, o)
	}
	if _, code := run(t, "", "mcp", "-http", "0.0.0.0:1", "-token", "0123456789abcdef"); code != 2 {
		t.Errorf("mcp -http on all addresses must be refused: %d", code)
	}
	if _, code := run(t, "", "mcp", "-http", "127.0.0.1:1", "-token", "short"); code != 2 {
		t.Errorf("mcp -http with a short token must be refused: %d", code)
	}
}
