package fs

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"unicode/utf16"
)

// ntfsProtect does to a record what writing it does: the last two bytes of
// every 512 go to the update sequence array (at 0x28 here), the sequence
// number takes their place
func ntfsProtect(b []byte, magic string, usn uint16) {
	copy(b, magic)
	const uo = 0x28
	uc := len(b)/512 + 1
	binary.LittleEndian.PutUint16(b[4:], uo)
	binary.LittleEndian.PutUint16(b[6:], uint16(uc))
	binary.LittleEndian.PutUint16(b[uo:], usn)
	for i := 1; i < uc; i++ {
		p := i*512 - 2
		copy(b[uo+2*i:], b[p:p+2])
		binary.LittleEndian.PutUint16(b[p:], usn)
	}
}

func TestNTFSFixup(t *testing.T) {
	b := make([]byte, 1024)
	for i := 0x40; i < len(b); i++ {
		b[i] = byte(i)
	}
	want := append([]byte(nil), b...)
	ntfsProtect(b, "FILE", 7)
	copy(want, b[:0x40]) // the header changed, the rest must come back
	if bytes.Equal(b, want) {
		t.Fatal("not protected")
	}
	torn := append([]byte(nil), b...)
	if err := ntfsFixup(b, "FILE"); err != nil || !bytes.Equal(b, want) {
		t.Fatal("fixup", err)
	}
	torn[1022] = 0x55 // the second sector was not written
	if err := ntfsFixup(torn, "FILE"); err == nil {
		t.Fatal("a torn record is not an error")
	}
	if err := ntfsFixup(want, "INDX"); err == nil {
		t.Fatal("a wrong magic is not an error")
	}
	binary.LittleEndian.PutUint16(torn[6:], 900)
	if err := ntfsFixup(torn, "FILE"); err == nil {
		t.Fatal("a long update sequence is not an error")
	}
}

func TestNTFSRuns(t *testing.T) {
	n := &ntfsFS{cs: 4096, clusters: 1 << 20}
	runs, err := n.decodeRuns([]byte{
		0x21, 0x18, 0x34, 0x56, // 0x18 clusters at 0x5634
		0x01, 0x05, // 5 sparse
		0x11, 0x30, 0xF0, // 0x30 at -16 from the last: 0x5624
		0x31, 0x02, 0x00, 0x01, 0x00, // 2 at +0x100: 0x5724
		0x00, 0xFF, // the end; what follows is ignored
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	want := []Extent{
		{0, 0x5634 * 4096, 0x18 * 4096},
		{0x18 * 4096, -1, 5 * 4096},
		{0x1D * 4096, 0x5624 * 4096, 0x30 * 4096},
		{0x4D * 4096, 0x5724 * 4096, 2 * 4096},
	}
	if fmt.Sprint(runs) != fmt.Sprint(want) {
		t.Fatalf("%v\nwant %v", runs, want)
	}
	for _, bad := range [][]byte{
		{0x11, 0x05, 0x80},             // a negative cluster
		{0x31, 0x05, 0xFF, 0xFF, 0x7F}, // past the end of the volume
		{0x09, 0x05},                   // a length of 9 bytes
		{0x21, 0x05},                   // cut
	} {
		if _, err := n.decodeRuns(bad, 0); err == nil {
			t.Fatalf("% X is not an error", bad)
		}
	}
	// The initialized size: past it the file reads as zeros
	a := &ntfsAttr{runs: runs[:1], dataSize: 10000, initSize: 5000}
	got, err := n.streamRuns(a)
	if err != nil || fmt.Sprint(got) != fmt.Sprint([]Extent{{0, 0x5634 * 4096, 5000}, {5000, -1, 5000}}) {
		t.Fatal(got, err)
	}
}

// ntfsKey makes a $FILE_NAME of a name in the Win32 namespace
func ntfsKey(name string, space byte) []byte {
	u := utf16.Encode([]rune(name))
	k := make([]byte, 0x42+2*len(u))
	binary.LittleEndian.PutUint64(k, ntfsRootRec)
	k[0x40] = byte(len(u))
	k[0x41] = space
	for i, c := range u {
		binary.LittleEndian.PutUint16(k[0x42+2*i:], c)
	}
	return k
}

// ntfsIdxEntry makes an index entry; sub >= 0 points to a child node, a
// nil key is the last entry
func ntfsIdxEntry(ref uint64, key []byte, sub int64) []byte {
	l := (16 + len(key) + 7) &^ 7
	var fl uint16
	if key == nil {
		fl |= 2
	}
	if sub >= 0 {
		fl |= 1
		l += 8
	}
	e := make([]byte, l)
	binary.LittleEndian.PutUint64(e, ref)
	binary.LittleEndian.PutUint16(e[8:], uint16(l))
	binary.LittleEndian.PutUint16(e[10:], uint16(len(key)))
	binary.LittleEndian.PutUint16(e[12:], fl)
	copy(e[16:], key)
	if sub >= 0 {
		binary.LittleEndian.PutUint64(e[l-8:], uint64(sub))
	}
	return e
}

// ntfsIndx makes an INDX block of 4096 bytes
func ntfsIndx(vcn uint64, entries ...[]byte) []byte {
	b := make([]byte, 4096)
	binary.LittleEndian.PutUint64(b[0x10:], vcn)
	body := bytes.Join(entries, nil)
	binary.LittleEndian.PutUint32(b[0x18:], 0x28) // entries at 0x40
	binary.LittleEndian.PutUint32(b[0x1C:], uint32(0x28+len(body)))
	binary.LittleEndian.PutUint32(b[0x20:], 4096-0x18)
	copy(b[0x40:], body)
	ntfsProtect(b, "INDX", 3)
	return b
}

// A directory of two levels: the root has "m" with the node of "a" "b"
// before it, and the last entry points to the node of "x" "z"
func TestNTFSIndex(t *testing.T) {
	vol := make([]byte, 64<<10)
	copy(vol[16<<10:], ntfsIndx(0, ntfsIdxEntry(30, ntfsKey("x", 1), -1), ntfsIdxEntry(31, ntfsKey("Z", 1), -1), ntfsIdxEntry(0, nil, -1)))
	copy(vol[20<<10:], ntfsIndx(8, ntfsIdxEntry(20, ntfsKey("a", 1), -1),
		ntfsIdxEntry(20, ntfsKey("A~1", ntfsNameDOS), -1), ntfsIdxEntry(21, ntfsKey("b", 1), -1), ntfsIdxEntry(0, nil, -1)))
	body := bytes.Join([][]byte{ntfsIdxEntry(22, ntfsKey("m", 1), 8), ntfsIdxEntry(0, nil, 0)}, nil)
	root := make([]byte, 0x20+len(body))
	binary.LittleEndian.PutUint32(root, ntfsAttrFileName)
	binary.LittleEndian.PutUint32(root[8:], 4096)
	root[0xC] = 8
	binary.LittleEndian.PutUint32(root[0x10:], 0x10)
	binary.LittleEndian.PutUint32(root[0x14:], uint32(0x10+len(body)))
	copy(root[0x20:], body)

	n := &ntfsFS{r: bytes.NewReader(vol), size: int64(len(vol)), cs: 512, clusters: 128}
	f := &ntfsFile{no: 40, flags: ntfsRecInUse | ntfsRecDir, attrs: []ntfsAttr{
		{typ: ntfsAttrIdxRoot, name: "$I30", resident: true, value: root},
		{typ: ntfsAttrIdxAlloc, name: "$I30", runs: []Extent{{0, 16 << 10, 8192}}, dataSize: 8192, allocSize: 8192, initSize: 8192},
	}}
	kids, err := n.children(f)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, k := range kids {
		names = append(names, k.fn.name+":"+strconv.Itoa(int(k.ref)))
	}
	if strings.Join(names, " ") != "a:20 b:21 m:22 x:30 Z:31" {
		t.Fatal(names)
	}
	// A child that points back to its parent's node is a loop
	copy(vol[16<<10:], ntfsIndx(0, ntfsIdxEntry(30, ntfsKey("x", 1), 0), ntfsIdxEntry(0, nil, -1)))
	if _, err := n.children(f); err == nil {
		t.Fatal("a loop is not an error")
	}
	vol[20<<10+0x40+8] = 3 // an entry of 3 bytes
	if _, err := n.children(f); err == nil {
		t.Fatal("a bad entry is not an error")
	}
}

func TestNTFSRecord(t *testing.T) {
	n := &ntfsFS{cs: 4096, clusters: 1 << 20}
	rec := make([]byte, 1024)
	binary.LittleEndian.PutUint16(rec[0x14:], 0x38) // the first attribute
	binary.LittleEndian.PutUint16(rec[0x16:], ntfsRecInUse)
	off := 0x38
	attr := func(b []byte) { copy(rec[off:], b); off += len(b) }
	// A resident $FILE_NAME, a named resident $DATA, a non-resident $DATA
	fn := ntfsKey("Some file.txt", 1)
	a := make([]byte, (0x18+len(fn)+7)&^7)
	binary.LittleEndian.PutUint32(a, ntfsAttrFileName)
	binary.LittleEndian.PutUint32(a[4:], uint32(len(a)))
	binary.LittleEndian.PutUint32(a[0x10:], uint32(len(fn)))
	binary.LittleEndian.PutUint16(a[0x14:], 0x18)
	copy(a[0x18:], fn)
	attr(a)
	a = make([]byte, 0x28)
	binary.LittleEndian.PutUint32(a, ntfsAttrData)
	binary.LittleEndian.PutUint32(a[4:], 0x28)
	a[9], a[0xA] = 2, 0x18 // the name "ab"
	copy(a[0x18:], []byte{'a', 0, 'b', 0})
	binary.LittleEndian.PutUint32(a[0x10:], 3)
	binary.LittleEndian.PutUint16(a[0x14:], 0x20)
	copy(a[0x20:], "xyz")
	attr(a)
	a = make([]byte, 0x48)
	binary.LittleEndian.PutUint32(a, ntfsAttrData)
	binary.LittleEndian.PutUint32(a[4:], 0x48)
	a[8] = 1
	binary.LittleEndian.PutUint16(a[0x20:], 0x40)
	binary.LittleEndian.PutUint64(a[0x28:], 8192)
	binary.LittleEndian.PutUint64(a[0x30:], 5000)
	binary.LittleEndian.PutUint64(a[0x38:], 5000)
	copy(a[0x40:], []byte{0x11, 0x02, 0x10, 0})
	attr(a)
	binary.LittleEndian.PutUint32(rec[off:], ntfsAttrEnd)
	binary.LittleEndian.PutUint32(rec[0x18:], uint32(off+8))
	ntfsProtect(rec, "FILE", 1)
	if err := ntfsFixup(rec, "FILE"); err != nil {
		t.Fatal(err)
	}
	attrs, err := n.parseAttrs(rec)
	if err != nil || len(attrs) != 3 {
		t.Fatal(attrs, err)
	}
	if nm, ok := parseNTFSName(attrs[0].value); !ok || nm.name != "Some file.txt" || nm.space != 1 {
		t.Fatal(nm)
	}
	if attrs[1].name != "ab" || string(attrs[1].value) != "xyz" {
		t.Fatal(attrs[1])
	}
	f := &ntfsFile{attrs: attrs}
	d, err := f.stream(ntfsAttrData, "")
	if err != nil || d.resident || d.dataSize != 5000 || fmt.Sprint(d.runs) != fmt.Sprint([]Extent{{0, 16 * 4096, 8192}}) {
		t.Fatal(d, err)
	}
	// An attribute longer than the record
	binary.LittleEndian.PutUint32(rec[0x38+4:], 5000)
	if _, err := n.parseAttrs(rec); err == nil {
		t.Fatal("a long attribute is not an error")
	}
}

func TestLZNT1(t *testing.T) {
	// Literals and a reference that overlaps what it makes
	dst := make([]byte, 12)
	src := []byte{0x05, 0xB0, 0x08, 'a', 'b', 'c', 0x06, 0x20}
	if n, err := lznt1Decompress(dst, src); err != nil || n != 12 || string(dst) != "abcabcabcabc" {
		t.Fatalf("%d %q %v", n, dst, err)
	}
	// Past 16 bytes the offset takes one more bit: 20 literals, then 10
	// bytes from 20 back (token 19<<11 | 7)
	body := []byte{0x00}
	body = append(body, "01234567"...)
	body = append(body, 0x00)
	body = append(body, "89ABCDEF"...)
	body = append(body, 0x10)
	body = append(body, "GHIJ"...)
	body = append(body, 0x07, 0x98)
	src = append([]byte{byte(len(body) - 1), 0xB0}, body...)
	dst = make([]byte, 30)
	if _, err := lznt1Decompress(dst, src); err != nil || string(dst) != "0123456789ABCDEFGHIJ0123456789" {
		t.Fatalf("%q %v", dst, err)
	}
	// A stored chunk, then a compressed one; the first short chunk is
	// followed by zeros to 4096
	raw := xRandom(4096, 5)
	src = append([]byte{0xFF, 0x3F}, raw...)
	src = append(src, 0x05, 0xB0, 0x08, 'a', 'b', 'c', 0x06, 0x20)
	src = append(src, 0x05, 0xB0, 0x08, 'x', 'y', 'z', 0x06, 0x20, 0, 0)
	dst = make([]byte, 3*4096)
	n, err := lznt1Decompress(dst, src)
	if err != nil || n != 3*4096 || !bytes.Equal(dst[:4096], raw) || string(dst[4096:4108]) != "abcabcabcabc" ||
		!bytes.Equal(dst[4108:8192], make([]byte, 4096-12)) || string(dst[8192:8204]) != "xyzxyzxyzxyz" {
		t.Fatal(n, err)
	}
	for _, bad := range [][]byte{
		{0x02, 0xB0, 0x01, 0x00, 0x10},      // a reference at the start
		{0x03, 0xB0, 0x02, 'a', 0xFF, 0xFF}, // a reference before the chunk
		{0x10, 0xB0, 0x00},                  // past the end
	} {
		if _, err := lznt1Decompress(make([]byte, 4096), bad); err == nil {
			t.Fatalf("% X is not an error", bad)
		}
	}
}

// A compressed file: unit 0 compressed in one cluster, unit 1 stored, unit
// 2 sparse
func TestNTFSCompressed(t *testing.T) {
	vol := make([]byte, 64<<10)
	comp := []byte{0x05, 0xB0, 0x08, 'a', 'b', 'c', 0x06, 0x20}
	copy(vol[1024:], comp)
	stored := xRandom(8192, 7)
	copy(vol[8192:], stored)
	n := &ntfsFS{r: bytes.NewReader(vol), size: int64(len(vol)), cs: 512, clusters: 128}
	a := &ntfsAttr{flags: ntfsFlagCompressed, cu: 4, dataSize: 20000, initSize: 20000, allocSize: 24576,
		runs: []Extent{{0, 1024, 512}, {512, -1, 7680}, {8192, 8192, 8192}, {16384, -1, 8192}}}
	r, err := n.compressedReader(a)
	if err != nil {
		t.Fatal(err)
	}
	want := make([]byte, 20000)
	copy(want, "abcabcabcabc")
	copy(want[8192:], stored)
	got := make([]byte, 20000)
	if k, _ := r.ReadAt(got, 0); k != len(got) || !bytes.Equal(got, want) {
		t.Fatal("contents differ")
	}
	part := make([]byte, 100)
	if k, _ := r.ReadAt(part, 8150); k != 100 || !bytes.Equal(part, want[8150:8250]) {
		t.Fatal("a read across units differs")
	}
}

// ---- Volumes made by mkntfs and ntfscp ----

func TestNTFSTools(t *testing.T) {
	mkntfs, ntfscp := xTool("mkntfs"), xTool("ntfscp")
	if mkntfs == "" || ntfscp == "" {
		t.Skip("no mkntfs or ntfscp")
	}
	dir := t.TempDir()
	img := filepath.Join(dir, "img")
	if err := os.WriteFile(img, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	os.Truncate(img, 8<<20)
	xRun(t, mkntfs, "-F", "-Q", "-q", "-L", "Test Volume", img)
	files := map[string][]byte{
		"hello.txt": []byte("hello, world\n"),
		"tiny":      []byte("x"),
		"empty":     nil,
		"big.bin":   xRandom(300_000, 1),
		"файл.txt":  []byte("привет\n"),
		"ads":       xRandom(200_000, 2),
	}
	for i := range 150 {
		files[fmt.Sprintf("file-with-a-rather-long-name-%04d.txt", i)] = []byte(strconv.Itoa(i))
	}
	cp := func(data []byte, args ...string) {
		src := filepath.Join(dir, "src")
		os.WriteFile(src, data, 0o644)
		xRun(t, ntfscp, append(append([]string{"-q"}, args[:len(args)-1]...), img, src, args[len(args)-1])...)
	}
	for name, data := range files {
		if name != "ads" {
			cp(data, "/"+name)
		}
	}
	// Named streams fill the record of "ads": its attributes go to other
	// records through an attribute list
	cp([]byte("x"), "/ads")
	for i := range 12 {
		cp(xRandom(300, int64(i)), "-N", fmt.Sprintf("stream%d", i), "/ads")
	}
	cp(files["ads"], "/ads")
	// A sparse file: ntfstruncate makes the rest a hole
	sparse := xRandom(3000, 3)
	cp(sparse, "/sparse")
	if tr := xTool("ntfstruncate"); tr != "" {
		fsys, _ := xOpenImage(t, img)
		st, err := fsys.Stat("/sparse")
		if err != nil {
			t.Fatal(err)
		}
		xRun(t, tr, img, strconv.FormatUint(st.ID, 10), "0x80", "2000000")
		sparse = append(sparse, make([]byte, 2000000-len(sparse))...)
	}
	files["sparse"] = sparse

	fsys, vol := xOpenImage(t, img)
	if fsys.Type() != "NTFS" || fsys.Label() != "Test Volume" {
		t.Fatalf("%q %q", fsys.Type(), fsys.Label())
	}
	ents, err := fsys.ReadDir("/")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]Entry{}
	prev := ""
	for _, e := range ents {
		got[e.Name] = e
		if strings.ToUpper(e.Name) < strings.ToUpper(prev) {
			t.Fatalf("not in order: %q after %q", e.Name, prev)
		}
		prev = e.Name
	}
	for _, sys := range []string{"$MFT", "$MFTMirr", "$LogFile", "$Volume", "$AttrDef", "$Bitmap", "$Boot", "$BadClus", "$Secure", "$UpCase", "$Extend"} {
		if _, ok := got[sys]; !ok {
			t.Fatalf("no %s in the root", sys)
		}
	}
	if len(got) != len(files)+11 {
		t.Fatalf("%d entries", len(got))
	}
	for name, data := range files {
		e, ok := got[name]
		if !ok || e.IsDir || e.Size != int64(len(data)) || e.Path != "/"+name || e.ModTime.IsZero() {
			t.Fatalf("%s: %+v", name, e)
		}
		xCheckFile(t, fsys, vol, "/"+name, data)
	}
	if x, _ := fsys.Extents("/tiny"); x != nil {
		t.Fatal("a resident file has extents")
	}
	if x, _ := fsys.Extents("/sparse"); len(x) < 2 || x[len(x)-1].DiskOff != -1 {
		t.Fatalf("sparse: %+v", x)
	}
	// The MFT is a file too, read through its own runs
	st, _ := fsys.Stat("/$mft")
	if st.Name != "$MFT" || st.ID != 0 || st.Size == 0 {
		t.Fatal(st)
	}
	n := fsys.(*ntfsFS)
	mftData := make([]byte, n.mftSize)
	(&xRunsReader{r: vol, runs: n.mft, size: n.mftSize}).ReadAt(mftData, 0)
	xCheckFile(t, fsys, vol, "/$MFT", mftData)
	f, _, err := n.lookup("/ads")
	if err != nil {
		t.Fatal(err)
	}
	hasList := false
	for _, a := range f.attrs {
		hasList = hasList || a.typ == ntfsAttrList
	}
	if !hasList {
		t.Fatal("/ads has no attribute list")
	}
	root, _ := n.file(ntfsRootRec)
	if a, _ := root.stream(ntfsAttrIdxAlloc, "$I30"); a == nil {
		t.Fatal("the root has no index blocks")
	}
	// Case-insensitive names, a directory of system files
	if st, err := fsys.Stat("/HELLO.TXT"); err != nil || st.Name != "hello.txt" {
		t.Fatal(st, err)
	}
	ext, err := fsys.ReadDir("/$Extend")
	if err != nil || len(ext) == 0 {
		t.Fatal(ext, err)
	}
	if _, err := fsys.Stat("/nope"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if _, err := fsys.ReadDir("/hello.txt"); !errors.Is(err, ErrNotDir) {
		t.Fatal(err)
	}
	if _, _, err := fsys.Open("/$Extend"); !errors.Is(err, ErrIsDir) {
		t.Fatal(err)
	}
	if st, err := fsys.Stat("/"); err != nil || !st.IsDir || st.ID != ntfsRootRec {
		t.Fatal(st, err)
	}
	// ntfsls agrees on the names
	if ls := xTool("ntfsls"); ls != "" {
		out, err := exec.Command(ls, "-a", "-s", img).Output()
		if err != nil {
			t.Fatal(err)
		}
		for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			if l != "." && l != ".." {
				if _, ok := got[l]; !ok {
					t.Fatalf("ntfsls shows %q", l)
				}
			}
		}
	}
}

func TestNTFSTestdata(t *testing.T) {
	files, _ := filepath.Glob("testdata/ntfs/*.img.gz")
	if len(files) == 0 {
		t.Skip("no images")
	}
	for _, p := range files {
		data := xGunzip(t, p)
		fsys, err := Open(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		if fsys.Type() != "NTFS" {
			t.Fatal(fsys.Type())
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

// Damaged volumes must give errors, not panics or endless loops; the input
// is changes to an image as in FuzzExt
func FuzzNTFS(f *testing.F) {
	var bases [][]byte
	files, _ := filepath.Glob("testdata/ntfs/*.img.gz")
	for _, p := range files {
		bases = append(bases, xGunzip(f, p))
	}
	if len(bases) == 0 {
		f.Skip("no images")
	}
	xFuzzPatched(f, bases)
}
