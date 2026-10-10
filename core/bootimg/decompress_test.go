package bootimg

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/klauspost/compress/zstd"

	bt "github.com/ipoluianov/althex/core/bootimg/bootimgtest"
	"github.com/ipoluianov/althex/internal/fuzzguard"
)

// payload is what the fixtures in testdata are made of: text that
// compresses (long and short matches) and random bytes that do not
func payload() []byte {
	words := []string{"kernel ", "ramdisk ", "init ", "boot ", "the ", "a ", "/sbin/", "module ", "\n", "0x7C00 ", "dtb "}
	var b bytes.Buffer
	x := uint32(12345)
	next := func() uint32 { x = x*1103515245 + 12345; return x >> 8 }
	for b.Len() < 120_000 {
		b.WriteString(words[next()%uint32(len(words))])
	}
	for range 16_000 {
		b.WriteByte(byte(next()))
	}
	for b.Len() < 150_000 {
		b.WriteString(words[next()%uint32(len(words))])
	}
	return b.Bytes()
}

// TestMakeFixtures writes testdata/payload.* with the tools; run with
// ALTHEX_MAKE_FIXTURES=1 when payload changes
func TestMakeFixtures(t *testing.T) {
	if os.Getenv("ALTHEX_MAKE_FIXTURES") == "" {
		t.Skip("ALTHEX_MAKE_FIXTURES is not set")
	}
	p := payload()
	run := func(out string, name string, args ...string) {
		cmd := exec.Command(name, args...)
		cmd.Stdin = bytes.NewReader(p)
		b, err := cmd.Output()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join("testdata", out), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	os.MkdirAll("testdata", 0o755)
	run("payload.xz", "xz", "-c", "-6")
	run("payload-crc32.xz", "xz", "-c", "--check=crc32", "--lzma2=dict=1MiB")
	run("payload.lzma", "lzma", "-c")
	run("payload.bz2", "bzip2", "-c", "-9")
	run("payload-bcj.xz", "xz", "-c", "--x86", "--lzma2")
}

func fixture(t testing.TB, name string) []byte {
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// lz4Encode is a greedy LZ4 block encoder for the tests: it compresses
// buf[start:], matches may go back into buf[:start]
func lz4Encode(buf []byte, start int) []byte {
	var out []byte
	table := make([]int, 1<<16)
	for i := range table {
		table[i] = -1
	}
	hash := func(i int) int { return int((binary.LittleEndian.Uint32(buf[i:]) * 2654435761) >> 16) }
	for i := max(0, start-65535); i+4 <= start; i++ {
		table[hash(i)] = i
	}
	length := func(n int) {
		for ; n >= 255; n -= 255 {
			out = append(out, 255)
		}
		out = append(out, byte(n))
	}
	seq := func(lit []byte, off, ml int) {
		tok := byte(min(len(lit), 15)) << 4
		if ml > 0 {
			tok |= byte(min(ml-4, 15))
		}
		out = append(out, tok)
		if len(lit) >= 15 {
			length(len(lit) - 15)
		}
		out = append(out, lit...)
		if ml == 0 {
			return
		}
		out = append(out, byte(off), byte(off>>8))
		if ml-4 >= 15 {
			length(ml - 4 - 15)
		}
	}
	anchor, i := start, start
	for i+12 <= len(buf) {
		h := hash(i)
		c := table[h]
		table[h] = i
		if c >= 0 && i-c <= 65535 && i > c && binary.LittleEndian.Uint32(buf[c:]) == binary.LittleEndian.Uint32(buf[i:]) {
			ml := 4
			for i+ml < len(buf)-5 && buf[c+ml] == buf[i+ml] {
				ml++
			}
			seq(buf[anchor:i], i-c, ml)
			i += ml
			anchor = i
			continue
		}
		i++
	}
	seq(buf[anchor:], 0, 0)
	return out
}

// lz4Legacy writes the legacy format, with the size of the data after it
// as the kernel build appends
func lz4Legacy(data []byte, block int) []byte {
	out := binary.LittleEndian.AppendUint32(nil, lz4LegacyMagic)
	for off := 0; off < len(data); off += block {
		b := lz4Encode(data[off:min(off+block, len(data))], 0)
		out = binary.LittleEndian.AppendUint32(out, uint32(len(b)))
		out = append(out, b...)
	}
	return binary.LittleEndian.AppendUint32(out, uint32(len(data)))
}

// lz4Frame writes a frame of 64 KB blocks; dependent: the blocks refer to
// the ones before; raw: incompressible blocks are stored
func lz4Frame(data []byte, dependent, checksums bool) []byte {
	flg := byte(0x40)
	if !dependent {
		flg |= 0x20
	}
	if checksums {
		flg |= 0x10 | 0x04 | 0x08
	}
	out := binary.LittleEndian.AppendUint32(nil, lz4FrameMagic)
	out = append(out, flg, 0x40)
	if checksums {
		out = binary.LittleEndian.AppendUint64(out, uint64(len(data)))
	}
	out = append(out, 0) // the header checksum, not checked
	for off := 0; off < len(data); off += 64 << 10 {
		end := min(off+64<<10, len(data))
		var b []byte
		if dependent {
			b = lz4Encode(data[:end], off)
		} else {
			b = lz4Encode(data[off:end], 0)
		}
		size := uint32(len(b))
		if len(b) >= end-off {
			b, size = data[off:end], uint32(end-off)|0x80000000
		}
		out = binary.LittleEndian.AppendUint32(out, size)
		out = append(out, b...)
		if checksums {
			out = append(out, 1, 2, 3, 4)
		}
	}
	out = binary.LittleEndian.AppendUint32(out, 0)
	if checksums {
		out = append(out, 1, 2, 3, 4)
	}
	return out
}

func zstdCompress(t testing.TB, data []byte) []byte {
	e, err := zstd.NewWriter(nil)
	if err != nil {
		t.Fatal(err)
	}
	return e.EncodeAll(data, nil)
}

// decompress decompresses all of data as a part
func decompress(t testing.TB, compression string, data []byte, limit int64) ([]byte, error) {
	t.Helper()
	p := Part{Off: 0, Size: int64(len(data)), Compression: compression}
	return DecompressPart(bytes.NewReader(data), p, limit)
}

func TestDecompress(t *testing.T) {
	want := payload()
	cases := []struct {
		name, compression string
		data              []byte
	}{
		{"gzip", "gzip", gz(t, want)},
		{"gzip members and padding", "gzip", append(append(gz(t, want[:1000]), gz(t, want[1000:])...), 0, 0, 0, 0)},
		{"zstd", "zstd", zstdCompress(t, want)},
		{"lz4 legacy", "lz4", lz4Legacy(want, 50_000)},
		{"lz4 legacy, two streams", "lz4", func() []byte {
			a := lz4Legacy(want[:7000], 4096)
			return append(a[:len(a)-4], lz4Legacy(want[7000:], 1<<20)...)
		}()},
		{"lz4 frame", "lz4", lz4Frame(want, false, false)},
		{"lz4 frame, dependent blocks, checksums", "lz4", lz4Frame(want, true, true)},
		{"lz4 frames and a skippable one", "lz4", append(append(lz4Frame(want[:5000], true, false), 0x50, 0x2A, 0x4D, 0x18, 2, 0, 0, 0, 9, 9), lz4Frame(want[5000:], false, true)...)},
		{"xz", "xz", fixture(t, "payload.xz")},
		{"xz crc32", "xz", fixture(t, "payload-crc32.xz")},
		{"lzma", "lzma", fixture(t, "payload.lzma")},
		{"bzip2", "bzip2", fixture(t, "payload.bz2")},
		{"none", "", want},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := compressionOf(c.data[:16]); got != c.compression {
				t.Errorf("compressionOf = %q, want %q", got, c.compression)
			}
			got, err := decompress(t, c.compression, c.data, 0)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("%d bytes, want %d; differ at %d", len(got), len(want), diffAt(got, want))
			}
			// The limit: exactly the size passes, one byte less does not
			if _, err := decompress(t, c.compression, c.data, int64(len(want))); err != nil {
				t.Errorf("limit = size: %v", err)
			}
			if _, err := decompress(t, c.compression, c.data, int64(len(want))-1); !errors.Is(err, ErrTooLarge) {
				t.Errorf("limit = size-1: %v, want ErrTooLarge", err)
			}
		})
	}
}

func diffAt(a, b []byte) int {
	for i := range min(len(a), len(b)) {
		if a[i] != b[i] {
			return i
		}
	}
	return min(len(a), len(b))
}

func TestLZMAKnownSize(t *testing.T) {
	// The size in the header instead of all ones: the end marker that
	// follows is not needed, and is allowed
	want := payload()
	data := append([]byte(nil), fixture(t, "payload.lzma")...)
	binary.LittleEndian.PutUint64(data[5:], uint64(len(want)))
	got, err := decompress(t, "lzma", data, 0)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("%d bytes, %v", len(got), err)
	}
	// A size shorter than the data: the stream is cut there
	binary.LittleEndian.PutUint64(data[5:], 1000)
	if _, err := decompress(t, "lzma", data, 0); err == nil {
		t.Log("a short size is accepted") // a match may end exactly at 1000
	}
}

func TestDecompressErrors(t *testing.T) {
	want := payload()
	for _, c := range []struct {
		name, compression string
		data              []byte
	}{
		{"xz with BCJ", "xz", fixture(t, "payload-bcj.xz")},
		{"lzo", "lzo", []byte{0x89, 'L', 'Z', 'O', 0, 0x0D, 0x0A, 0x1A, 0x0A, 0, 0, 0, 0, 0, 0, 0}},
		{"gzip cut", "gzip", gz(t, want)[:1000]},
		{"xz cut", "xz", fixture(t, "payload.xz")[:5000]},
		{"xz broken", "xz", flip(fixture(t, "payload.xz"), 3000)},
		{"lzma cut", "lzma", fixture(t, "payload.lzma")[:5000]},
		{"lz4 frame cut", "lz4", lz4Frame(want, true, false)[:5000]},
		{"lz4 frame broken", "lz4", flip(lz4Frame(want, false, false), 7)},
		{"zstd broken", "zstd", flip(zstdCompress(t, want), 100)},
	} {
		t.Run(c.name, func(t *testing.T) {
			if _, err := decompress(t, c.compression, c.data, 0); err == nil {
				t.Fatal("no error")
			} else {
				t.Log(err)
			}
		})
	}
	if Decompressible("lzo") || !Decompressible("xz") {
		t.Error("Decompressible")
	}
}

func flip(b []byte, at int) []byte {
	b = append([]byte(nil), b...)
	b[at] ^= 0xFF
	return b
}

// TestBomb: a few bytes that claim much more stop at the limit, quickly
func TestBomb(t *testing.T) {
	zeros := make([]byte, 64<<20)
	for _, c := range []struct {
		compression string
		data        []byte
	}{
		{"gzip", gz(t, zeros)},
		{"zstd", zstdCompress(t, zeros)},
		{"lz4", lz4Frame(zeros, true, false)},
		{"lz4", lz4Legacy(zeros, lz4LegacyBlock)},
	} {
		_, err := decompress(t, c.compression, c.data, 1<<20)
		if !errors.Is(err, ErrTooLarge) {
			t.Errorf("%s: %v, want ErrTooLarge", c.compression, err)
		}
	}
	// An LZMA header that claims a 4 GB dictionary does not take it
	data := append([]byte(nil), fixture(t, "payload.lzma")...)
	binary.LittleEndian.PutUint32(data[1:], 0xFFFFFFFF)
	if got, err := decompress(t, "lzma", data, 0); err != nil || !bytes.Equal(got, payload()) {
		t.Errorf("a large dictionary: %d bytes, %v", len(got), err)
	}
}

// A compressed ramdisk of an Android boot image: its cpio gets its files
func TestCompressedRamdisk(t *testing.T) {
	cp := bt.Cpio(cpioSample())
	for _, c := range []struct {
		compression string
		data        []byte
	}{
		{"gzip", gz(t, cp)},
		{"zstd", zstdCompress(t, cp)},
		{"lz4", lz4Legacy(cp, lz4LegacyBlock)},
		{"lz4", lz4Frame(cp, true, true)},
	} {
		img := detect(t, bt.AndroidBoot(bt.Android{Version: 2, PageSize: 2048, Kernel: gz(t, []byte("kernel")), Ramdisk: c.data}))
		var rd Part
		for _, p := range img.Parts {
			if p.Name == "ramdisk" {
				rd = p
			}
		}
		if rd.Compression != c.compression {
			t.Fatalf("ramdisk compression %q, want %q", rd.Compression, c.compression)
		}
		snap := bt.AndroidBoot(bt.Android{Version: 2, PageSize: 2048, Kernel: gz(t, []byte("kernel")), Ramdisk: c.data})
		inner, data, err := DetectPart(bytes.NewReader(snap), rd, 0)
		if err != nil {
			t.Fatalf("%s: %v", c.compression, err)
		}
		if inner.Format != "cpio (newc)" || len(inner.Parts) == 0 {
			t.Fatalf("%s: %s %+v", c.compression, inner.Format, inner.Parts)
		}
		if !bytes.Equal(data, cp) {
			t.Fatalf("%s: the ramdisk differs", c.compression)
		}
		// The kernel opens decompressed too
		for _, p := range img.Parts {
			if p.Name == "kernel" {
				r, n, err := OpenDecompressed(bytes.NewReader(snap), p, 0)
				if err != nil || n != 6 {
					t.Fatalf("kernel: %d bytes, %v", n, err)
				}
				b := make([]byte, n)
				r.ReadAt(b, 0)
				if string(b) != "kernel" {
					t.Fatalf("kernel: %q", b)
				}
			}
		}
	}
}

// The tools write the formats as a distribution does
func TestDecompressTools(t *testing.T) {
	cp := bt.Cpio(cpioSample())
	for _, c := range []struct {
		compression, tool string
		args              []string
	}{
		{"xz", "xz", []string{"-c", "--check=crc32"}},
		{"xz", "xz", []string{"-c", "-0", "--check=none"}},
		{"xz", "xz", []string{"-c", "-9e", "--check=sha256"}},
		{"lzma", "lzma", []string{"-c", "-9"}},
		{"bzip2", "bzip2", []string{"-c"}},
		{"zstd", "zstd", []string{"-c", "-19"}},
		{"gzip", "gzip", []string{"-c", "-9"}},
		{"lz4", "lz4", []string{"-c", "-l"}},
		{"lz4", "lz4", []string{"-c", "--content-size", "-BD"}},
	} {
		path, err := exec.LookPath(c.tool)
		if err != nil {
			t.Logf("%s: not found", c.tool)
			continue
		}
		for _, data := range [][]byte{cp, payload()} {
			cmd := exec.Command(path, c.args...)
			cmd.Stdin = bytes.NewReader(data)
			out, err := cmd.Output()
			if err != nil {
				t.Fatalf("%s %v: %v", c.tool, c.args, err)
			}
			got, err := decompress(t, c.compression, out, 0)
			if err != nil || !bytes.Equal(got, data) {
				t.Errorf("%s %v: %d bytes of %d, %v", c.tool, c.args, len(got), len(data), err)
			}
		}
	}
}

func TestOpenPartStream(t *testing.T) {
	want := payload()
	data := gz(t, want)
	r, err := OpenPart(bytes.NewReader(data), Part{Size: int64(len(data)), Compression: "gzip"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	got, err := io.ReadAll(r)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("%d bytes, %v", len(got), err)
	}
	if _, err := OpenPart(bytes.NewReader(data), Part{Off: -1, Size: 1}, 0); err == nil {
		t.Error("a bad part opened")
	}
}

func fuzzDecoder(f *testing.F, compression string, seeds ...[]byte) {
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		defer fuzzguard.Start(t.Name())()
		rd, err := NewDecompressor(compression, bytes.NewReader(b))
		if err != nil {
			return
		}
		defer rd.Close()
		io.Copy(io.Discard, &limitReader{r: rd, n: 4 << 20})
	})
}

func FuzzLZ4(f *testing.F) {
	p := payload()[:20000]
	fuzzDecoder(f, "lz4", lz4Legacy(p, 4096), lz4Frame(p, true, true), lz4Frame(p, false, false))
}

func FuzzLZMA(f *testing.F) {
	data, _ := os.ReadFile("testdata/payload.lzma")
	fuzzDecoder(f, "lzma", data[:min(len(data), 4000)], []byte{0x5D, 0, 0, 1, 0, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0, 0x41, 0xFE, 0xFB, 0xFF, 0xFF, 0xA3, 0xBD, 0x85, 0, 0})
}

func FuzzXZ(f *testing.F) {
	data, _ := os.ReadFile("testdata/payload-crc32.xz")
	small, _ := os.ReadFile("testdata/payload.xz")
	fuzzDecoder(f, "xz", data, small[:min(len(small), 3000)])
}

func FuzzGzip(f *testing.F) { fuzzDecoder(f, "gzip", gz(f, payload()[:3000])) }

func FuzzZstd(f *testing.F) { fuzzDecoder(f, "zstd", zstdCompress(f, payload()[:3000])) }

func FuzzBzip2(f *testing.F) {
	data, _ := os.ReadFile("testdata/payload.bz2")
	fuzzDecoder(f, "bzip2", data)
}

// The decoder of LZ4 blocks alone: never past its limit, never a panic
func FuzzLZ4Block(f *testing.F) {
	f.Add(lz4Encode(payload()[:5000], 0))
	f.Fuzz(func(t *testing.T, b []byte) {
		out, _ := lz4Block(nil, b, 1<<16)
		if len(out) > 1<<16 {
			t.Fatalf("%d bytes", len(out))
		}
	})
}
