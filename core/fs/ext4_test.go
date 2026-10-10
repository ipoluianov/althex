package fs

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/ipoluianov/althex/internal/fuzzguard"
)

// xTool finds a tool of the tests in PATH or the sbin directories, which
// are often not in the PATH of a user; "" when absent
func xTool(name string) string {
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	for _, d := range []string{"/sbin", "/usr/sbin", "/usr/local/sbin"} {
		p := filepath.Join(d, name)
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	return ""
}

func xRun(t *testing.T, name string, args ...string) {
	t.Helper()
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
	}
}

// xRandom is reproducible content
func xRandom(n int, seed int64) []byte {
	b := make([]byte, n)
	rand.New(rand.NewSource(seed)).Read(b)
	return b
}

// xOpenImage opens an image file as a file system
func xOpenImage(t *testing.T, path string) (FS, *os.File) {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	st, _ := f.Stat()
	fsys, err := Open(f, st.Size())
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	return fsys, f
}

// xCheckFile compares a file of the volume with the expected bytes, both
// through Open and through its extents read from the volume
func xCheckFile(t *testing.T, fsys FS, vol io.ReaderAt, path string, want []byte) {
	t.Helper()
	r, size, err := fsys.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	if size != int64(len(want)) {
		t.Fatalf("%s: size %d, want %d", path, size, len(want))
	}
	got := make([]byte, size)
	if n, err := r.ReadAt(got, 0); n != len(got) {
		t.Fatalf("%s: read %d: %v", path, n, err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("%s: contents differ", path)
	}
	// A read in the middle
	if size > 10 {
		part := make([]byte, size/2)
		if n, _ := r.ReadAt(part, size/3); n != len(part) || !bytes.Equal(part, want[size/3:size/3+int64(len(part))]) {
			t.Fatalf("%s: a read in the middle differs", path)
		}
	}
	exts, err := fsys.Extents(path)
	if err != nil {
		t.Fatalf("extents %s: %v", path, err)
	}
	if exts == nil {
		return
	}
	var pos int64
	for _, e := range exts {
		if e.FileOff != pos || e.Len <= 0 {
			t.Fatalf("%s: extent %+v at %d", path, e, pos)
		}
		b := make([]byte, e.Len)
		if e.DiskOff >= 0 {
			if _, err := vol.ReadAt(b, e.DiskOff); err != nil {
				t.Fatal(err)
			}
		}
		if !bytes.Equal(b, want[pos:pos+e.Len]) {
			t.Fatalf("%s: the bytes of extent %+v differ", path, e)
		}
		pos += e.Len
	}
	if pos != size {
		t.Fatalf("%s: extents cover %d of %d", path, pos, size)
	}
}

// xCompareTree compares the volume with a directory of the host
func xCompareTree(t *testing.T, fsys FS, vol io.ReaderAt, host, path string, skip func(string) bool) {
	t.Helper()
	hostEnts, err := os.ReadDir(filepath.Join(host, filepath.FromSlash(path)))
	if err != nil {
		t.Fatal(err)
	}
	ents, err := fsys.ReadDir(path)
	if err != nil {
		t.Fatalf("readdir %s: %v", path, err)
	}
	var names, want []string
	for _, e := range ents {
		if !skip(e.Name) {
			names = append(names, e.Name)
		}
	}
	for _, e := range hostEnts {
		want = append(want, e.Name())
	}
	sort.Strings(names)
	sort.Strings(want)
	if strings.Join(names, "|") != strings.Join(want, "|") {
		t.Fatalf("%s: names %q, want %q", path, names, want)
	}
	for _, e := range ents {
		if skip(e.Name) {
			continue
		}
		p := strings.TrimSuffix(path, "/") + "/" + e.Name
		if e.Path != p {
			t.Fatalf("path %q, want %q", e.Path, p)
		}
		st, err := fsys.Stat(p)
		if err != nil || st != e {
			t.Fatalf("stat %s: %+v %v, readdir %+v", p, st, err, e)
		}
		hp := filepath.Join(host, filepath.FromSlash(p))
		hi, _ := os.Lstat(hp)
		if hi.Mode()&os.ModeSymlink != 0 {
			continue
		}
		if e.IsDir != hi.IsDir() {
			t.Fatalf("%s: dir %v", p, e.IsDir)
		}
		if e.IsDir {
			xCompareTree(t, fsys, vol, host, p, skip)
			continue
		}
		data, _ := os.ReadFile(hp)
		xCheckFile(t, fsys, vol, p, data)
		if mt := hi.ModTime().Unix(); e.ModTime.Unix() != mt {
			t.Fatalf("%s: time %v, want %v", p, e.ModTime, hi.ModTime())
		}
	}
}

// makeExtTree makes the files the ext images are made from
func makeExtTree(t *testing.T) string {
	dir := t.TempDir()
	put := func(name string, data []byte) {
		p := filepath.Join(dir, filepath.FromSlash(name))
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	put("hello.txt", []byte("hello, world\n"))
	put("tiny", []byte("x"))
	put("empty", nil)
	put("a/big.bin", xRandom(300_000, 1))
	put("a/b/c/deep.txt", []byte("deep\n"))
	put("файл.txt", []byte("привет\n"))
	for i := range 300 {
		// Long names make the directory hashed (htree) in ext4
		put(fmt.Sprintf("many/file-with-a-rather-long-name-%04d.txt", i), []byte(fmt.Sprint(i)))
	}
	// A sparse file: a hole, data, a hole, data
	f, err := os.Create(filepath.Join(dir, "sparse"))
	if err != nil {
		t.Fatal(err)
	}
	f.WriteAt([]byte("start"), 0)
	f.WriteAt([]byte("middle"), 3<<20)
	f.WriteAt([]byte("end"), 5<<20)
	f.Close()
	os.Symlink("hello.txt", filepath.Join(dir, "link"))
	os.Symlink(strings.Repeat("long/", 30)+"target", filepath.Join(dir, "longlink"))
	return dir
}

func TestExtTools(t *testing.T) {
	mke2fs := xTool("mke2fs")
	if mke2fs == "" {
		t.Skip("no mke2fs")
	}
	host := makeExtTree(t)
	skip := func(n string) bool { return n == "lost+found" }
	for _, v := range []struct {
		name, typ string
		args      []string
	}{
		{"ext4", "ext4", []string{"-t", "ext4"}},
		{"ext4-1k", "ext4", []string{"-t", "ext4", "-b", "1024"}},
		{"ext4-inline", "ext4", []string{"-t", "ext4", "-O", "inline_data"}},
		{"ext4-64bit-metabg", "ext4", []string{"-t", "ext4", "-O", "64bit,meta_bg,^resize_inode"}},
		{"ext3", "ext3", []string{"-t", "ext3"}},
		{"ext2", "ext2", []string{"-t", "ext2"}},
		{"ext2-1k", "ext2", []string{"-t", "ext2", "-b", "1024"}},
	} {
		t.Run(v.name, func(t *testing.T) {
			img := filepath.Join(t.TempDir(), "img")
			args := append([]string{"-q", "-F", "-L", xLabel(v.name), "-d", host}, v.args...)
			xRun(t, mke2fs, append(args, img, "16M")...)
			// mke2fs -d makes linear directories; e2fsck -D hashes them
			if fsck := xTool("e2fsck"); fsck != "" && v.typ != "ext2" {
				exec.Command(fsck, "-f", "-y", "-D", img).Run()
			}
			fsys, f := xOpenImage(t, img)
			if fsys.Type() != v.typ || fsys.Label() != xLabel(v.name) {
				t.Fatalf("type %q label %q", fsys.Type(), fsys.Label())
			}
			xCompareTree(t, fsys, f, host, "/", skip)
			e := fsys.(*extFS)
			for _, l := range []string{"link", "longlink"} {
				want, _ := os.Readlink(filepath.Join(host, l))
				if got, err := e.Readlink("/" + l); err != nil || got != want {
					t.Fatalf("%s: %q %v", l, got, err)
				}
				st, _ := fsys.Stat("/" + l)
				if st.Mode&0xF000 != 0xA000 {
					t.Fatalf("%s: mode %o", l, st.Mode)
				}
			}
			exts, _ := fsys.Extents("/sparse")
			holes := 0
			for _, x := range exts {
				if x.DiskOff < 0 {
					holes++
				}
			}
			if holes < 2 {
				t.Fatalf("sparse: %+v", exts)
			}
			if _, err := fsys.Stat("/HELLO.TXT"); !errors.Is(err, ErrNotFound) {
				t.Fatal("ext names must be case-sensitive")
			}
			if _, err := fsys.ReadDir("/hello.txt"); !errors.Is(err, ErrNotDir) {
				t.Fatal(err)
			}
			if _, _, err := fsys.Open("/a"); !errors.Is(err, ErrIsDir) {
				t.Fatal(err)
			}
			if st, err := fsys.Stat("/a/b/../b/c/./deep.txt"); err != nil || st.Path != "/a/b/c/deep.txt" {
				t.Fatal(st, err)
			}
			if v.name == "ext4" && xTool("e2fsck") != "" {
				in, _, _ := e.lookup("/many")
				if in.flags&0x1000 == 0 {
					t.Fatal("/many is not hashed")
				}
			}
			if v.name == "ext4-inline" {
				in, _, _ := e.lookup("/a/b/c")
				if in.flags&extFlagInline == 0 {
					t.Fatal("/a/b/c is not inline")
				}

				in, _, _ = e.lookup("/tiny")
				if in.flags&extFlagInline == 0 {
					t.Fatal("tiny is not inline")
				}
				if x, _ := fsys.Extents("/tiny"); x != nil {
					t.Fatal("inline data has no extents")
				}
			}
		})
	}
}

// xLabel is a volume label of at most 16 bytes
func xLabel(s string) string { return s[:min(len(s), 16)] }

// A file of many fragments: the free space is cut into pieces by removing
// every other small file, then a large file is written over them. In
// ext4 it needs an extent tree deeper than the inode.
func TestExtFragmented(t *testing.T) {
	mke2fs, debugfs := xTool("mke2fs"), xTool("debugfs")
	if mke2fs == "" || debugfs == "" {
		t.Skip("no mke2fs or debugfs")
	}
	for _, typ := range []string{"ext4", "ext2"} {
		t.Run(typ, func(t *testing.T) {
			dir := t.TempDir()
			host := filepath.Join(dir, "host")
			os.MkdirAll(host, 0o755)
			for i := range 200 {
				os.WriteFile(filepath.Join(host, fmt.Sprintf("f%03d", i)), xRandom(4096, int64(i)), 0o644)
			}
			img := filepath.Join(dir, "img")
			xRun(t, mke2fs, "-q", "-F", "-t", typ, "-b", "1024", "-d", host, img, "4M")
			var cmds []string
			for i := 0; i < 200; i += 2 {
				cmds = append(cmds, fmt.Sprintf("rm f%03d", i))
			}
			big := xRandom(380_000, 99)
			bigPath := filepath.Join(dir, "big")
			os.WriteFile(bigPath, big, 0o644)
			cmds = append(cmds, "write "+bigPath+" big")
			script := filepath.Join(dir, "cmds")
			os.WriteFile(script, []byte(strings.Join(cmds, "\n")+"\n"), 0o644)
			xRun(t, debugfs, "-w", "-f", script, img)
			fsys, f := xOpenImage(t, img)
			exts, err := fsys.Extents("/big")
			if err != nil {
				t.Fatal(err)
			}
			if len(exts) < 10 {
				t.Fatalf("only %d extents", len(exts))
			}
			xCheckFile(t, fsys, f, "/big", big)
			if typ == "ext4" {
				in, _, _ := fsys.(*extFS).lookup("/big")
				if xLE16(in.raw[0x28+6:]) == 0 {
					t.Fatal("the extent tree has depth 0")
				}
			}
		})
	}
}

// The images of the tests made by mke2fs, kept in testdata so the tests
// run without the tools too
func TestExtTestdata(t *testing.T) {
	files, _ := filepath.Glob("testdata/ext4/*.img.gz")
	if len(files) == 0 {
		t.Skip("no images")
	}
	for _, p := range files {
		data := xGunzip(t, p)
		fsys, err := Open(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		r, size, err := fsys.Open("/hello.txt")
		if err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		b := make([]byte, size)
		r.ReadAt(b, 0)
		if string(b) != "hello, world\n" {
			t.Fatalf("%s: %q", p, b)
		}
		xWalk(t, fsys, "/", 0)
	}
}

// xWalk reads every file of a volume
func xWalk(t *testing.T, fsys FS, path string, depth int) {
	t.Helper()
	ents, err := fsys.ReadDir(path)
	if err != nil {
		t.Fatalf("readdir %s: %v", path, err)
	}
	for _, e := range ents {
		if e.IsDir {
			if depth < 8 {
				xWalk(t, fsys, e.Path, depth+1)
			}
			continue
		}
		r, size, err := fsys.Open(e.Path)
		if err != nil {
			t.Fatalf("open %s: %v", e.Path, err)
		}
		if size > 0 {
			b := make([]byte, size)
			if n, err := r.ReadAt(b, 0); n != len(b) {
				t.Fatalf("read %s: %v", e.Path, err)
			}
		}
	}
}

func xGunzip(t testing.TB, path string) []byte {
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	z, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(z)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// ---- A tiny ext2 made here ----

// makeTinyExt2 makes an ext2 of 1K blocks, one group: the root with a file,
// a fast symlink and a directory that has a file of indirect blocks
func makeTinyExt2() ([]byte, []byte) {
	const bs = 1024
	img := make([]byte, 128*bs)
	le16 := func(off int, v uint16) { binary.LittleEndian.PutUint16(img[off:], v) }
	le32 := func(off int, v uint32) { binary.LittleEndian.PutUint32(img[off:], v) }
	sb := 1024
	le32(sb+0x0, 32)    // inodes
	le32(sb+0x4, 128)   // blocks
	le32(sb+0x14, 1)    // first data block
	le32(sb+0x18, 0)    // 1K blocks
	le32(sb+0x20, 8192) // blocks per group
	le32(sb+0x28, 32)   // inodes per group
	le16(sb+0x38, extMagic)
	le32(sb+0x4C, 1) // dynamic revision
	le32(sb+0x54, 11)
	le16(sb+0x58, 128)
	le32(sb+0x60, extIncompatFiletype)
	copy(img[sb+0x78:], "tiny")
	// The group descriptor at block 2: the inode table at block 5 (4 blocks)
	le32(2*bs+0x8, 5)
	inode := func(ino int, mode uint16, size int, mtime uint32, blocks ...uint32) {
		off := 5*bs + (ino-1)*128
		le16(off, mode)
		le32(off+0x4, uint32(size))
		le32(off+0x10, mtime)
		le16(off+0x1A, 1)
		for i, b := range blocks {
			le32(off+0x28+4*i, b)
		}
	}
	dirent := func(off, ino int, name string, typ byte, last int) int {
		rl := (8 + len(name) + 3) &^ 3
		if last > 0 {
			rl = last - off
		}
		le32(off, uint32(ino))
		le16(off+4, uint16(rl))
		img[off+6] = byte(len(name))
		img[off+7] = typ
		copy(img[off+8:], name)
		return off + rl
	}
	// The root (2) at block 20: ".", "..", hello.txt (12), sub (13), ln (14)
	inode(2, 0x41ED, bs, 1000, 20)
	o := dirent(20*bs, 2, ".", 2, 0)
	o = dirent(o, 2, "..", 2, 0)
	o = dirent(o, 12, "hello.txt", 1, 0)
	o = dirent(o, 0, "", 0, 0) // a free entry
	o = dirent(o, 13, "sub", 2, 0)
	dirent(o, 14, "ln", 7, 21*bs)
	inode(12, 0x81A4, 6, 1234567890, 30)
	copy(img[30*bs:], "hello\n")
	// A fast symlink
	inode(14, 0xA1FF, 9, 1000)
	copy(img[5*bs+13*128+0x28:], "hello.txt")
	// sub (13) at block 21 with deep (15): 14 blocks, a hole at block 3,
	// the last two through the single indirect block 40
	inode(13, 0x41ED, bs, 1000, 21)
	o = dirent(21*bs, 13, ".", 2, 0)
	o = dirent(o, 2, "..", 2, 0)
	dirent(o, 15, "deep", 1, 22*bs)
	size := 13*bs + 100
	want := make([]byte, size)
	blocks := []uint32{50, 51, 52, 0, 60, 61, 62, 63, 64, 65, 66, 67, 40}
	for i := 0; i < 14; i++ {
		var p int
		switch {
		case i == 3:
			continue
		case i < 12:
			p = int(blocks[i])
		default:
			p = 41 + i - 12
			le32(40*bs+4*(i-12), uint32(p))
		}
		n := min(bs, size-i*bs)
		for j := 0; j < n; j++ {
			img[p*bs+j] = byte(i*7 + j)
			want[i*bs+j] = byte(i*7 + j)
		}
	}
	inode(15, 0x81A4, size, 1000, blocks...)
	return img, want
}

func TestExtTiny(t *testing.T) {
	const bs = 1024
	img, deep := makeTinyExt2()
	fsys, err := Open(bytes.NewReader(img), int64(len(img)))
	if err != nil {
		t.Fatal(err)
	}
	if fsys.Type() != "ext2" || fsys.Label() != "tiny" {
		t.Fatal(fsys.Type(), fsys.Label())
	}
	ents, err := fsys.ReadDir("/")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range ents {
		names = append(names, e.Name)
	}
	if strings.Join(names, ",") != "hello.txt,ln,sub" {
		t.Fatal(names)
	}
	st, err := fsys.Stat("/hello.txt")
	if err != nil || st.Size != 6 || st.ID != 12 || st.ModTime.Unix() != 1234567890 || st.Mode != 0x81A4 {
		t.Fatal(st, err)
	}
	vol := bytes.NewReader(img)
	xCheckFile(t, fsys, vol, "/hello.txt", []byte("hello\n"))
	xCheckFile(t, fsys, vol, "/sub/deep", deep)
	exts, _ := fsys.Extents("/sub/deep")
	if len(exts) != 4 || exts[1].DiskOff != -1 {
		t.Fatalf("%+v", exts)
	}
	if l, err := fsys.(*extFS).Readlink("/ln"); l != "hello.txt" || err != nil {
		t.Fatal(l, err)
	}
	xCheckFile(t, fsys, vol, "/ln", []byte("hello.txt"))
	if st, _ := fsys.Stat("/"); !st.IsDir || st.ID != 2 {
		t.Fatal(st)
	}
	// Damage: a loop in the directory, an inode past the end, a bad table
	bad := append([]byte(nil), img...)
	binary.LittleEndian.PutUint16(bad[20*bs+4:], 0)
	if f, err := Open(bytes.NewReader(bad), int64(len(bad))); err != nil {
		t.Fatal(err)
	} else if _, err := f.ReadDir("/"); err == nil {
		t.Fatal("a zero record length is not an error")
	}
	bad = append([]byte(nil), img...)
	binary.LittleEndian.PutUint32(bad[5*bs+14*128+0x28+4*12:], 1<<30)
	if f, _ := Open(bytes.NewReader(bad), int64(len(bad))); f != nil {
		if _, err := f.Extents("/sub/deep"); err == nil {
			t.Fatal("an indirect block past the end is not an error")
		}
	}
}

// Damaged images must give errors, not panics or endless loops. The
// input of the fuzzer is a list of changes to one of the images (the first
// byte chooses it; then 4 bytes of the offset and the new byte each): whole
// images would be too large for it.
func FuzzExt(f *testing.F) {
	img, _ := makeTinyExt2()
	bases := [][]byte{img}
	files, _ := filepath.Glob("testdata/ext4/*.img.gz")
	for _, p := range files {
		bases = append(bases, xGunzip(f, p))
	}
	xFuzzPatched(f, bases)
}

func xFuzzPatched(f *testing.F, bases [][]byte) {
	for i := range bases {
		f.Add([]byte{byte(i)})
		f.Add([]byte{byte(i), 0x38, 0x04, 0, 0, 0xFF})
	}
	f.Fuzz(func(t *testing.T, patch []byte) {
		defer fuzzguard.Start(t.Name())()
		if len(patch) == 0 {
			return
		}
		data := append([]byte(nil), bases[int(patch[0])%len(bases)]...)
		for p := patch[1:]; len(p) >= 5; p = p[5:] {
			data[int(xLE32(p)%uint32(len(data)))] = p[4]
		}
		fsys, err := Open(bytes.NewReader(data), int64(len(data)))
		if err != nil || fsys == nil {
			return
		}
		xFuzzWalk(fsys)
	})
}

// xFuzzWalk reads what it can of a volume, ignoring the errors
func xFuzzWalk(fsys FS) {
	count := 0
	var walk func(path string, depth int)
	walk = func(path string, depth int) {
		ents, _ := fsys.ReadDir(path)
		for _, e := range ents {
			if count++; count > 200 {
				return
			}
			fsys.Stat(e.Path)
			fsys.Extents(e.Path)
			if e.IsDir {
				if depth < 4 {
					walk(e.Path, depth+1)
				}
				continue
			}
			if r, size, err := fsys.Open(e.Path); err == nil && size > 0 {
				b := make([]byte, min(size, 1<<16))
				r.ReadAt(b, 0)
			}
		}
	}
	walk("/", 0)
}
