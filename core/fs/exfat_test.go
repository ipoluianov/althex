package fs

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/ipoluianov/althex/internal/fuzzguard"
)

// ---- An exFAT volume made in Go ----

type exfatNodeT struct {
	name    string
	dir     bool
	data    []byte
	valid   int // the valid data length; -1 - all
	kids    []*exfatNodeT
	clus    []uint32
	noChain bool // contiguous, not in the FAT
	useFAT  bool // a directory chained in the FAT even if contiguous
	deleted bool
}

// exfatBuilder writes an exFAT volume in memory. Contiguous files get the
// NoFatChain flag as Windows writes them; fragmented ones a FAT chain.
type exfatBuilder struct {
	b       []byte
	clusSz  int
	fatOff  int
	fatLen  int
	heapOff int
	nClus   int
	used    []bool
	root    *exfatNodeT
	label   string
}

func newExFATBuilder(nClus, spcShift int) *exfatBuilder {
	const bps = 512
	m := &exfatBuilder{clusSz: bps << spcShift, nClus: nClus, root: &exfatNodeT{dir: true, useFAT: true}, label: "exFAT Vol"}
	fatSecs := ((nClus+2)*4 + bps - 1) / bps
	m.fatOff = 24 * bps
	m.fatLen = fatSecs * bps
	m.heapOff = (24 + fatSecs) * bps
	m.b = make([]byte, m.heapOff+nClus*m.clusSz)
	m.used = make([]bool, nClus+2)
	m.used[0], m.used[1] = true, true
	b := m.b
	copy(b, []byte{0xEB, 0x76, 0x90})
	copy(b[3:], "EXFAT   ")
	binary.LittleEndian.PutUint64(b[0x48:], uint64(len(m.b)/bps))
	binary.LittleEndian.PutUint32(b[0x50:], 24)
	binary.LittleEndian.PutUint32(b[0x54:], uint32(fatSecs))
	binary.LittleEndian.PutUint32(b[0x58:], uint32(24+fatSecs))
	binary.LittleEndian.PutUint32(b[0x5C:], uint32(nClus))
	binary.LittleEndian.PutUint32(b[0x64:], 0xCAFEBABE)
	binary.LittleEndian.PutUint16(b[0x68:], 0x0100)
	b[0x6C] = 9
	b[0x6D] = byte(spcShift)
	b[0x6E] = 1
	b[0x6F] = 0x80
	b[0x70] = 0xFF
	b[510], b[511] = 0x55, 0xAA
	copy(b[12*bps:], b[:bps]) // the backup boot region
	m.setFAT(0, 0xFFFFFFF8)
	m.setFAT(1, 0xFFFFFFFF)
	return m
}

func (m *exfatBuilder) setFAT(c, v uint32) { binary.LittleEndian.PutUint32(m.b[m.fatOff+4*int(c):], v) }

// alloc takes the first free clusters; chain - write them in the FAT even
// when they are contiguous
func (m *exfatBuilder) alloc(n int, chain bool) (out []uint32, noChain bool) {
	for c := 2; c < len(m.used) && len(out) < n; c++ {
		if !m.used[c] {
			m.used[c] = true
			out = append(out, uint32(c))
		}
	}
	if len(out) < n {
		panic("the test volume is full")
	}
	noChain = !chain
	for i := 1; i < len(out); i++ {
		if out[i] != out[i-1]+1 {
			noChain = false
		}
	}
	if !noChain {
		for i, c := range out {
			if i+1 < len(out) {
				m.setFAT(c, out[i+1])
			} else {
				m.setFAT(c, 0xFFFFFFFF)
			}
		}
	}
	return out, noChain
}

func (m *exfatBuilder) write(chain []uint32, data []byte) {
	for i, c := range chain {
		s := data[min(i*m.clusSz, len(data)):min((i+1)*m.clusSz, len(data))]
		copy(m.b[m.heapOff+int(c-2)*m.clusSz:], s)
	}
}

func (m *exfatBuilder) file(parent *exfatNodeT, name string, data []byte, fat bool) *exfatNodeT {
	n := &exfatNodeT{name: name, data: data, valid: -1}
	if len(data) > 0 {
		n.clus, n.noChain = m.alloc((len(data)+m.clusSz-1)/m.clusSz, fat)
		m.write(n.clus, data)
	}
	parent.kids = append(parent.kids, n)
	return n
}

func (m *exfatBuilder) mkdir(parent *exfatNodeT, name string) *exfatNodeT {
	n := &exfatNodeT{name: name, dir: true}
	parent.kids = append(parent.kids, n)
	return n
}

func (m *exfatBuilder) remove(n *exfatNodeT) {
	n.deleted = true
	for _, c := range n.clus {
		m.used[c] = false
		if !n.noChain {
			m.setFAT(c, 0)
		}
	}
}

// exfatTS is 2023-03-04 10:20:30 (+1.5 s in the 10 ms byte), UTC+2
const exfatTS = (2023-1980)<<25 | 3<<21 | 4<<16 | 10<<11 | 20<<5 | 30/2

// set makes the entry set of a file
func (m *exfatBuilder) set(k *exfatNodeT) []byte {
	u := utf16.Encode([]rune(k.name))
	nNames := (len(u) + 14) / 15
	b := make([]byte, 32*(2+nNames))
	b[0] = 0x85
	b[1] = byte(1 + nNames)
	attr := uint16(0x20)
	if k.dir {
		attr = 0x10
	}
	binary.LittleEndian.PutUint16(b[4:], attr)
	for _, o := range []int{8, 12, 16} {
		binary.LittleEndian.PutUint32(b[o:], exfatTS)
	}
	b[20], b[21] = 150, 150
	b[22], b[23], b[24] = 0x88, 0x88, 0x88
	s := b[32:]
	s[0] = 0xC0
	var size int
	if k.dir {
		size = max(len(k.clus), 1) * m.clusSz
	} else {
		size = len(k.data)
	}
	if size > 0 {
		s[1] = 1
		if k.noChain {
			s[1] |= 2
		}
	}
	s[3] = byte(len(u))
	valid := size
	if k.valid >= 0 && !k.dir {
		valid = k.valid
	}
	binary.LittleEndian.PutUint64(s[8:], uint64(valid))
	if len(k.clus) > 0 {
		binary.LittleEndian.PutUint32(s[20:], k.clus[0])
	}
	binary.LittleEndian.PutUint64(s[24:], uint64(size))
	for i := 0; i < nNames; i++ {
		x := b[64+32*i:]
		x[0] = 0xC1
		for j := 0; j < 15 && i*15+j < len(u); j++ {
			binary.LittleEndian.PutUint16(x[2+2*j:], u[i*15+j])
		}
	}
	var sum uint16
	for i, c := range b {
		if i != 2 && i != 3 {
			sum = (sum<<15 | sum>>1) + uint16(c)
		}
	}
	binary.LittleEndian.PutUint16(b[2:], sum)
	if k.deleted {
		for i := 0; i < len(b); i += 32 {
			b[i] &^= 0x80
		}
	}
	return b
}

func (m *exfatBuilder) dirBytes(d *exfatNodeT, bitmapClus uint32) []byte {
	var b []byte
	if d == m.root {
		l := make([]byte, 32)
		l[0] = 0x83
		u := utf16.Encode([]rune(m.label))
		l[1] = byte(len(u))
		for i, c := range u {
			binary.LittleEndian.PutUint16(l[2+2*i:], c)
		}
		bm := make([]byte, 32)
		bm[0] = 0x81
		binary.LittleEndian.PutUint32(bm[20:], bitmapClus)
		binary.LittleEndian.PutUint64(bm[24:], uint64((m.nClus+7)/8))
		up := make([]byte, 32)
		up[0] = 0x82
		b = append(append(append(b, l...), bm...), up...)
	}
	for _, k := range d.kids {
		b = append(b, m.set(k)...)
	}
	return b
}

func (m *exfatBuilder) finish() []byte {
	bitmap, _ := m.alloc(((m.nClus+7)/8+m.clusSz-1)/m.clusSz, true)
	var alloc func(d *exfatNodeT)
	alloc = func(d *exfatNodeT) {
		n := (len(m.dirBytes(d, 0)) + m.clusSz - 1) / m.clusSz
		d.clus, d.noChain = m.alloc(max(n, 1), d.useFAT)
		for _, k := range d.kids {
			if k.dir {
				alloc(k)
			}
		}
	}
	alloc(m.root)
	binary.LittleEndian.PutUint32(m.b[0x60:], m.root.clus[0])
	var walk func(d *exfatNodeT)
	walk = func(d *exfatNodeT) {
		m.write(d.clus, m.dirBytes(d, bitmap[0]))
		for _, k := range d.kids {
			if k.dir {
				walk(k)
			}
		}
	}
	walk(m.root)
	bits := make([]byte, (m.nClus+7)/8)
	for c := 2; c < len(m.used); c++ {
		if m.used[c] {
			bits[(c-2)/8] |= 1 << ((c - 2) % 8)
		}
	}
	m.write(bitmap, bits)
	return m.b
}

func exfatTestTree(m *exfatBuilder) (files map[string]string, dirs []string) {
	files = map[string]string{}
	pat := func(n int, seed byte) []byte {
		b := make([]byte, n)
		for i := range b {
			b[i] = byte(i*11) ^ seed
		}
		return b
	}
	put := func(d *exfatNodeT, p string, data []byte, fat bool) *exfatNodeT {
		files[p] = string(data)
		return m.file(d, filepath.Base(p), data, fat)
	}
	put(m.root, "/Hello.txt", []byte("hello, exFAT"), false)
	put(m.root, "/empty", nil, false)
	put(m.root, "/A name longer than fifteen characters, Ünïcödé ✓.txt", pat(3000, 1), false)
	put(m.root, "/chained.bin", pat(2*m.clusSz+3, 2), true)
	gone := m.file(m.root, "gone.bin", pat(3*m.clusSz, 3), false)
	put(m.root, "/C.bin", pat(m.clusSz, 4), false)
	m.remove(gone)
	put(m.root, "/fragmented.bin", pat(5*m.clusSz+7, 5), false)
	d1 := m.mkdir(m.root, "Dir One")
	nested := m.mkdir(d1, "Nested")
	dirs = []string{"/Dir One", "/Dir One/Nested", "/Many"}
	put(nested, "/Dir One/Nested/deep.txt", []byte("deep"), false)
	many := m.mkdir(m.root, "Many")
	many.useFAT = true
	for i := 0; i < 30; i++ {
		put(many, fmt.Sprintf("/Many/entry %02d", i), []byte(fmt.Sprint(i)), false)
	}
	return files, dirs
}

func TestExFATBuilt(t *testing.T) {
	m := newExFATBuilder(400, 1)
	files, dirs := exfatTestTree(m)
	img := m.finish()
	fsys, err := Open(bytes.NewReader(img), int64(len(img)))
	if err != nil {
		t.Fatal(err)
	}
	if fsys.Type() != "exFAT" || fsys.Label() != "exFAT Vol" {
		t.Fatalf("%s %q", fsys.Type(), fsys.Label())
	}
	fatCheckFS(t, fsys, img, files, dirs)
	ext, err := fsys.Extents("/fragmented.bin")
	if err != nil || len(ext) != 2 {
		t.Fatalf("fragmented: %+v %v", ext, err)
	}
	if ext, _ := fsys.Extents("/Hello.txt"); len(ext) != 1 {
		t.Fatalf("contiguous: %+v", ext)
	}
	e, err := fsys.Stat("/dir one/NESTED/DEEP.TXT")
	if err != nil || e.Path != "/Dir One/Nested/deep.txt" {
		t.Fatalf("case-insensitive: %+v %v", e, err)
	}
	want := time.Date(2023, 3, 4, 10, 20, 31, 500e6, time.FixedZone("", 2*3600))
	if !e.ModTime.Equal(want) {
		t.Fatalf("time %v, want %v", e.ModTime, want)
	}
	if _, err := fsys.Stat("/a NAME longer than fifteen characters, üNÏcödé ✓.txt"); err != nil {
		t.Fatalf("folding: %v", err)
	}
	if _, err := fsys.Stat("/gone.bin"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted: %v", err)
	}
}

// A file whose valid data length is less than its size reads zeros after it
func TestExFATValidLength(t *testing.T) {
	m := newExFATBuilder(100, 0)
	part := m.file(m.root, "partial.bin", bytes.Repeat([]byte{0xAB}, 2000), false)
	part.valid = 100
	img := m.finish()
	fsys, err := Open(bytes.NewReader(img), int64(len(img)))
	if err != nil {
		t.Fatal(err)
	}
	r, n, err := fsys.Open("/partial.bin")
	if err != nil || n != 2000 {
		t.Fatal(n, err)
	}
	b := make([]byte, n)
	if _, err := r.ReadAt(b, 0); err != nil && err != io.EOF {
		t.Fatal(err)
	}
	if !bytes.Equal(b[:100], bytes.Repeat([]byte{0xAB}, 100)) || !bytes.Equal(b[100:], make([]byte, 1900)) {
		t.Fatal("partial.bin: no zeros after the valid data length")
	}
}

func TestExFATDamaged(t *testing.T) {
	m := newExFATBuilder(100, 0)
	f := m.file(m.root, "loop.bin", make([]byte, 4*512), true)
	img := m.finish()
	m.setFAT(f.clus[2], f.clus[1])
	fsys, err := Open(bytes.NewReader(img), int64(len(img)))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := fsys.Open("/loop.bin"); !errors.Is(err, errFATChain) {
		t.Fatalf("loop: %v", err)
	}
	// The root in a loop to itself
	m.setFAT(m.root.clus[0], m.root.clus[0])
	fsys, err = Open(bytes.NewReader(img), int64(len(img)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fsys.ReadDir("/"); !errors.Is(err, errFATChain) {
		t.Fatalf("root loop: %v", err)
	}
}

func TestExFATTools(t *testing.T) {
	mkfs := fatTool(t, "mkfs.exfat", "mkexfatfs")
	// Without root the files can be put only by a FUSE mount or by tools
	// that are rarely installed; the empty volume is checked
	dir := t.TempDir()
	img := filepath.Join(dir, "v.img")
	if err := os.WriteFile(img, make([]byte, 8<<20), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(mkfs, "-L", "TOOLS", img).CombinedOutput(); err != nil {
		t.Skipf("%s: %v\n%s", mkfs, err, out)
	}
	b, _ := os.ReadFile(img)
	fsys, err := Open(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatal(err)
	}
	if fsys.Type() != "exFAT" || fsys.Label() != "TOOLS" {
		t.Fatalf("%s %q", fsys.Type(), fsys.Label())
	}
	if l, err := fsys.ReadDir("/"); err != nil || len(l) != 0 {
		t.Fatalf("%v %v", l, err)
	}
}

func FuzzExFAT(f *testing.F) {
	m := newExFATBuilder(60, 0)
	m.file(m.root, "Hello.txt", []byte("hello"), false)
	d := m.mkdir(m.root, "Dir")
	m.file(d, "chained.bin", make([]byte, 1100), true)
	f.Add(m.finish())
	f.Fuzz(func(t *testing.T, b []byte) {
		defer fuzzguard.Start(t.Name())()
		fsys, err := Open(bytes.NewReader(b), int64(len(b)))
		if err != nil || fsys == nil {
			return
		}
		fsys.Label()
		budget := 2000
		fatWalk(fsys, "/", 0, &budget)
	})
}
