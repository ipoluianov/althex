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
	"slices"
	"sort"
	"strings"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/ipoluianov/althex/internal/fuzzguard"
)

// ---- Checks shared by the tests of FAT, exFAT and ISO 9660 ----

// fatCheckFS compares a file system with what it should have: files (path
// to contents) and dirs. The listings, Stat, the contents by Open and by
// the extents read from the image must agree.
func fatCheckFS(t *testing.T, fsys FS, img []byte, files map[string]string, dirs []string) {
	t.Helper()
	want := map[string][]string{"/": nil}
	for _, d := range dirs {
		want[d] = want[d]
	}
	add := func(p string) {
		dir := filepath.Dir(p)
		want[dir] = append(want[dir], filepath.Base(p))
	}
	for p := range files {
		add(p)
	}
	for _, d := range dirs {
		add(d)
	}
	for d, names := range want {
		list, err := fsys.ReadDir(d)
		if err != nil {
			t.Errorf("ReadDir %s: %v", d, err)
			continue
		}
		var got []string
		for _, e := range list {
			got = append(got, e.Name)
			if e.Path != fatJoin(d, e.Name) {
				t.Errorf("%s: path %q", e.Name, e.Path)
			}
		}
		sort.Strings(got)
		sort.Strings(names)
		if !slices.Equal(got, names) {
			t.Errorf("ReadDir %s: %q, want %q", d, got, names)
		}
	}
	for _, d := range dirs {
		if e, err := fsys.Stat(d); err != nil || !e.IsDir {
			t.Errorf("Stat %s: %+v %v", d, e, err)
		}
	}
	for p, data := range files {
		if data == isoListed {
			if _, err := fsys.Stat(p); err != nil {
				t.Errorf("Stat %s: %v", p, err)
			}
			continue
		}
		e, err := fsys.Stat(p)
		if err != nil || e.IsDir || e.Size != int64(len(data)) {
			t.Errorf("Stat %s: %+v %v", p, e, err)
			continue
		}
		r, n, err := fsys.Open(p)
		if err != nil || n != int64(len(data)) {
			t.Errorf("Open %s: %d %v", p, n, err)
			continue
		}
		b := make([]byte, n)
		if _, err := r.ReadAt(b, 0); err != nil && err != io.EOF {
			t.Errorf("read %s: %v", p, err)
		}
		if string(b) != data {
			t.Errorf("%s: contents differ", p)
		}
		ext, err := fsys.Extents(p)
		if err != nil {
			t.Errorf("Extents %s: %v", p, err)
			continue
		}
		var fromExt []byte
		for _, x := range ext {
			if x.FileOff != int64(len(fromExt)) || x.DiskOff < 0 || x.DiskOff+x.Len > int64(len(img)) {
				t.Fatalf("%s: bad extent %+v", p, x)
			}
			fromExt = append(fromExt, img[x.DiskOff:x.DiskOff+x.Len]...)
		}
		if string(fromExt) != data {
			t.Errorf("%s: the extents give other bytes (%d extents)", p, len(ext))
		}
	}
}

// fatWalk lists a file system and reads its files, with caps: for fuzzing
func fatWalk(fsys FS, dir string, depth int, budget *int) {
	list, _ := fsys.ReadDir(dir)
	for _, e := range list {
		if *budget--; *budget < 0 {
			return
		}
		if e.IsDir {
			if depth < 6 {
				fatWalk(fsys, e.Path, depth+1, budget)
			}
			continue
		}
		fsys.Extents(e.Path)
		if r, n, err := fsys.Open(e.Path); err == nil {
			b := make([]byte, min(n, 1<<16))
			r.ReadAt(b, 0)
		}
	}
}

// ---- A FAT volume made in Go ----

type fatNodeT struct {
	name    string
	dir     bool
	data    []byte
	kids    []*fatNodeT
	clus    []uint32
	deleted bool
}

// fatBuilder writes a FAT12/16/32 volume in memory, so the tests need no
// tools; files are written as they come, so deleting one and writing a
// bigger one fragments it as on a real volume
type fatBuilder struct {
	b        []byte
	bits     int
	clusSz   int
	fatOff   int
	fatSz    int // bytes of one FAT
	rootOff  int
	rootEnt  int
	dataOff  int
	nClus    int
	used     []bool
	root     *fatNodeT
	alias    int
	label    string
	rootClus uint32
}

func newFATBuilder(t testing.TB, bits, totSecs, spc int) *fatBuilder {
	const bps = 512
	m := &fatBuilder{bits: bits, clusSz: bps * spc, b: make([]byte, totSecs*bps), root: &fatNodeT{dir: true}, label: "TESTVOL"}
	rsvd, rootEnt := 1, 224
	switch bits {
	case 16:
		rootEnt = 512
	case 32:
		rsvd, rootEnt = 32, 0
	}
	rootSecs := rootEnt * 32 / bps
	fatSecs := 1
	for {
		n := (totSecs - rsvd - 2*fatSecs - rootSecs) / spc
		need := ((n+2)*bits/8 + bps) / bps
		if need <= fatSecs {
			m.nClus = n
			break
		}
		fatSecs = need
	}
	m.fatOff = rsvd * bps
	m.fatSz = fatSecs * bps
	m.rootOff = m.fatOff + 2*m.fatSz
	m.rootEnt = rootEnt
	m.dataOff = m.rootOff + rootSecs*bps
	m.used = make([]bool, m.nClus+2)
	m.used[0], m.used[1] = true, true
	got := 12
	if m.nClus >= 4085 {
		got = 16
	}
	if m.nClus >= 65525 {
		got = 32
	}
	if got != bits {
		t.Fatalf("FAT%d builder made %d clusters", bits, m.nClus)
	}
	b := m.b
	copy(b, []byte{0xEB, 0x3C, 0x90})
	copy(b[3:], "MSWIN4.1")
	binary.LittleEndian.PutUint16(b[11:], bps)
	b[13] = byte(spc)
	binary.LittleEndian.PutUint16(b[14:], uint16(rsvd))
	b[16] = 2
	binary.LittleEndian.PutUint16(b[17:], uint16(rootEnt))
	if totSecs < 65536 && bits != 32 {
		binary.LittleEndian.PutUint16(b[19:], uint16(totSecs))
	} else {
		binary.LittleEndian.PutUint32(b[32:], uint32(totSecs))
	}
	b[21] = 0xF8
	if bits == 32 {
		binary.LittleEndian.PutUint32(b[36:], uint32(fatSecs))
		m.rootClus = 2
		m.used[2] = true // the first cluster of the root
		binary.LittleEndian.PutUint32(b[44:], 2)
		binary.LittleEndian.PutUint16(b[48:], 1) // FSInfo
		binary.LittleEndian.PutUint16(b[50:], 6) // the backup boot sector
		b[66] = 0x29
		binary.LittleEndian.PutUint32(b[67:], 0x12345678)
		copy(b[71:], "BPBLABEL   ")
		copy(b[82:], "FAT32   ")
		fi := b[bps:]
		binary.LittleEndian.PutUint32(fi, 0x41615252)
		binary.LittleEndian.PutUint32(fi[484:], 0x61417272)
		binary.LittleEndian.PutUint32(fi[488:], 0xFFFFFFFF)
		binary.LittleEndian.PutUint32(fi[492:], 3)
		binary.LittleEndian.PutUint32(fi[508:], 0xAA550000)
	} else {
		binary.LittleEndian.PutUint16(b[22:], uint16(fatSecs))
		b[38] = 0x29
		binary.LittleEndian.PutUint32(b[39:], 0x12345678)
		copy(b[43:], "BPBLABEL   ")
		copy(b[54:], fmt.Sprintf("FAT%d   ", bits))
	}
	b[510], b[511] = 0x55, 0xAA
	m.setFAT(0, 0x0FFFFFF8)
	m.setFAT(1, 0x0FFFFFFF)
	if bits == 32 {
		m.setFAT(2, 0x0FFFFFFF)
		copy(b[6*bps:7*bps], b[:bps])
	}
	return m
}

func (m *fatBuilder) eoc() uint32 {
	return map[int]uint32{12: 0xFFF, 16: 0xFFFF, 32: 0x0FFFFFFF}[m.bits]
}

func (m *fatBuilder) setFAT(c, v uint32) {
	for k := 0; k < 2; k++ {
		f := m.b[m.fatOff+k*m.fatSz:]
		switch m.bits {
		case 12:
			v &= 0xFFF
			o := int(c + c/2)
			w := binary.LittleEndian.Uint16(f[o:])
			if c&1 != 0 {
				w = w&0x000F | uint16(v)<<4
			} else {
				w = w&0xF000 | uint16(v)
			}
			binary.LittleEndian.PutUint16(f[o:], w)
		case 16:
			binary.LittleEndian.PutUint16(f[2*c:], uint16(v))
		default:
			binary.LittleEndian.PutUint32(f[4*c:], v&0x0FFFFFFF)
		}
	}
}

// alloc takes the first free clusters and chains them
func (m *fatBuilder) alloc(n int) []uint32 {
	var out []uint32
	for c := 2; c < len(m.used) && len(out) < n; c++ {
		if !m.used[c] {
			m.used[c] = true
			out = append(out, uint32(c))
		}
	}
	if len(out) < n {
		panic("the test volume is full")
	}
	for i, c := range out {
		if i+1 < len(out) {
			m.setFAT(c, out[i+1])
		} else {
			m.setFAT(c, m.eoc())
		}
	}
	return out
}

func (m *fatBuilder) clusOff(c uint32) int { return m.dataOff + int(c-2)*m.clusSz }

func (m *fatBuilder) writeChain(chain []uint32, data []byte) {
	for i, c := range chain {
		s := data[min(i*m.clusSz, len(data)):min((i+1)*m.clusSz, len(data))]
		copy(m.b[m.clusOff(c):], s)
	}
}

func (m *fatBuilder) mkdir(parent *fatNodeT, name string) *fatNodeT {
	n := &fatNodeT{name: name, dir: true}
	parent.kids = append(parent.kids, n)
	return n
}

func (m *fatBuilder) file(parent *fatNodeT, name string, data []byte) *fatNodeT {
	n := &fatNodeT{name: name, data: data}
	if len(data) > 0 {
		n.clus = m.alloc((len(data) + m.clusSz - 1) / m.clusSz)
		m.writeChain(n.clus, data)
	}
	parent.kids = append(parent.kids, n)
	return n
}

// remove deletes a file as DOS does: its entries get E5, its clusters are freed
func (m *fatBuilder) remove(n *fatNodeT) {
	n.deleted = true
	for _, c := range n.clus {
		m.used[c] = false
		m.setFAT(c, 0)
	}
}

// entries makes the directory entries of a name: an 8.3 one, with long
// name entries before it when the name is not 8.3
func (m *fatBuilder) entries(name string, attr byte, clus uint32, size uint32) []byte {
	short, ntFlags, long := fatShortFor(name)
	if long {
		m.alias++
		base := strings.Map(func(r rune) rune {
			if r >= 'a' && r <= 'z' {
				return r - 32
			}
			if r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
				return r
			}
			return -1
		}, strings.TrimSuffix(name, filepath.Ext(name)))
		base = (base + "XXXXXX")[:6]
		short = fmt.Sprintf("%-8s%-3s", fmt.Sprintf("%s~%d", base[:min(6, 7-len(fmt.Sprint(m.alias)))], m.alias), "")
	}
	d := make([]byte, 32)
	copy(d, short)
	d[11] = attr
	d[12] = ntFlags
	binary.LittleEndian.PutUint16(d[20:], uint16(clus>>16))
	binary.LittleEndian.PutUint16(d[22:], 12<<11|34<<5|56/2)      // 12:34:56
	binary.LittleEndian.PutUint16(d[24:], (2024-1980)<<9|7<<5|15) // 2024-07-15
	binary.LittleEndian.PutUint16(d[26:], uint16(clus))
	binary.LittleEndian.PutUint32(d[28:], size)
	if !long {
		return d
	}
	u := utf16.Encode([]rune(name))
	u = append(u, 0)
	for len(u)%13 != 0 {
		u = append(u, 0xFFFF)
	}
	cnt := len(u) / 13
	sum := fatChecksum(d[:11])
	var out []byte
	for ord := cnt; ord >= 1; ord-- {
		e := make([]byte, 32)
		e[0] = byte(ord)
		if ord == cnt {
			e[0] |= 0x40
		}
		e[11] = 0x0F
		e[13] = sum
		for k, o := range [13]int{1, 3, 5, 7, 9, 14, 16, 18, 20, 22, 24, 28, 30} {
			binary.LittleEndian.PutUint16(e[o:], u[(ord-1)*13+k])
		}
		out = append(out, e...)
	}
	return append(out, d...)
}

// fatShortFor gives the 8.3 form of a name; long - it has none and needs
// long name entries
func fatShortFor(name string) (short string, ntFlags byte, long bool) {
	base, ext, _ := strings.Cut(name, ".")
	ok83 := len(base) >= 1 && len(base) <= 8 && len(ext) <= 3 && !strings.Contains(ext, ".")
	for _, r := range name {
		if !(r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '.' || r == '_' || r == '-') {
			ok83 = false
		}
	}
	if !ok83 {
		return "", 0, true
	}
	// One case per part, as Windows NT keeps with the flags
	for i, part := range []string{base, ext} {
		switch part {
		case strings.ToUpper(part):
		case strings.ToLower(part):
			ntFlags |= 0x08 << i
		default:
			return "", 0, true
		}
	}
	return fmt.Sprintf("%-8s%-3s", strings.ToUpper(base), strings.ToUpper(ext)), ntFlags, false
}

// dirBytes makes the entries of a directory, the subdirectories written
// first (their clusters are known after)
func (m *fatBuilder) dirBytes(d *fatNodeT, self, parent uint32) []byte {
	var b []byte
	if d != m.root {
		dot := m.entries("X", 0x10, self, 0)
		copy(dot, ".          ")
		dd := m.entries("X", 0x10, parent, 0)
		copy(dd, "..         ")
		b = append(append(b, dot...), dd...)
	} else if m.label != "" {
		v := make([]byte, 32)
		copy(v, fmt.Sprintf("%-11s", m.label))
		v[11] = 0x08
		b = append(b, v...)
	}
	for _, k := range d.kids {
		var c uint32 // a directory has none yet when its parent is measured
		if len(k.clus) > 0 {
			c = k.clus[0]
		}
		var e []byte
		if k.dir {
			e = m.entries(k.name, 0x10, c, 0)
		} else {
			e = m.entries(k.name, 0x20, c, uint32(len(k.data)))
		}
		if k.deleted {
			for i := 0; i < len(e); i += 32 {
				e[i] = 0xE5
			}
		}
		b = append(b, e...)
	}
	return b
}

// finish writes the directories
func (m *fatBuilder) finish() []byte {
	var walk func(d *fatNodeT, parent uint32)
	// Clusters for every directory first: "." and ".." need them
	var alloc func(d *fatNodeT)
	alloc = func(d *fatNodeT) {
		for _, k := range d.kids {
			if k.dir {
				n := (len(m.dirBytes(k, 0, 0)) + m.clusSz - 1) / m.clusSz
				k.clus = m.alloc(n)
				alloc(k)
			}
		}
	}
	alloc(m.root)
	walk = func(d *fatNodeT, parent uint32) {
		var self uint32
		if len(d.clus) > 0 {
			self = d.clus[0]
		}
		b := m.dirBytes(d, self, parent)
		if d == m.root {
			if m.bits == 32 {
				// The root of FAT32 is a chain from cluster 2
				n := (len(b) + m.clusSz - 1) / m.clusSz
				chain := []uint32{2}
				if n > 1 {
					more := m.alloc(n - 1)
					m.setFAT(2, more[0])
					chain = append(chain, more...)
				}
				m.writeChain(chain, b)
			} else {
				copy(m.b[m.rootOff:m.rootOff+m.rootEnt*32], b)
			}
		} else {
			m.writeChain(d.clus, b)
		}
		for _, k := range d.kids {
			if k.dir {
				p := self
				if d == m.root {
					p = 0
				}
				walk(k, p)
			}
		}
	}
	walk(m.root, 0)
	return m.b
}

// fatTestTree fills a builder: nested directories, long and non-ASCII
// names, lowercase 8.3 names, an empty file, a deleted one and a file
// fragmented around another
func fatTestTree(m *fatBuilder) (files map[string]string, dirs []string) {
	files = map[string]string{}
	pat := func(n int, seed byte) []byte {
		b := make([]byte, n)
		for i := range b {
			b[i] = byte(i*7) ^ seed
		}
		return b
	}
	put := func(dir *fatNodeT, path string, data []byte) *fatNodeT {
		files[path] = string(data)
		return m.file(dir, filepath.Base(path), data)
	}
	put(m.root, "/README.TXT", []byte("hello, FAT\n"))
	put(m.root, "/lower.txt", []byte("lowercase 8.3"))
	put(m.root, "/A long file name.text", pat(3000, 1))
	put(m.root, "/empty", nil)
	a := put(m.root, "/A.BIN", pat(2*m.clusSz, 2))
	gone := m.file(m.root, "deleted.bin", pat(3*m.clusSz, 3))
	put(m.root, "/C.BIN", pat(m.clusSz+5, 4))
	m.remove(gone)
	_ = a
	// Takes the hole of deleted.bin and goes on after C.BIN
	put(m.root, "/fragmented.dat", pat(6*m.clusSz+100, 5))
	sub := m.mkdir(m.root, "Sub Directory")
	deep := m.mkdir(sub, "deeper")
	dirs = append(dirs, "/Sub Directory", "/Sub Directory/deeper")
	put(sub, "/Sub Directory/Привет мир.txt", []byte("non-ASCII long name"))
	put(deep, "/Sub Directory/deeper/x.c", pat(100, 6))
	// Enough entries for the directory to take several clusters
	for i := 0; i < 40; i++ {
		put(deep, fmt.Sprintf("/Sub Directory/deeper/file number %02d.txt", i), []byte(fmt.Sprintf("file %d", i)))
	}
	m.mkdir(m.root, "EMPTYDIR")
	dirs = append(dirs, "/EMPTYDIR")
	return files, dirs
}

func TestFATBuilt(t *testing.T) {
	for _, c := range []struct{ bits, tot, spc int }{{12, 2880, 1}, {16, 4300, 1}, {32, 68000, 1}} {
		t.Run(fmt.Sprintf("FAT%d", c.bits), func(t *testing.T) {
			m := newFATBuilder(t, c.bits, c.tot, c.spc)
			files, dirs := fatTestTree(m)
			img := m.finish()
			fsys, err := Open(bytes.NewReader(img), int64(len(img)))
			if err != nil {
				t.Fatal(err)
			}
			if fsys.Type() != fmt.Sprintf("FAT%d", c.bits) || fsys.Label() != "TESTVOL" {
				t.Fatalf("%s %q", fsys.Type(), fsys.Label())
			}
			fatCheckFS(t, fsys, img, files, dirs)
			ext, err := fsys.Extents("/fragmented.dat")
			if err != nil || len(ext) != 2 {
				t.Fatalf("fragmented: %+v %v", ext, err)
			}
			e, err := fsys.Stat("/sub directory/DEEPER/X.C")
			if err != nil || e.Name != "x.c" || e.Path != "/Sub Directory/deeper/x.c" {
				t.Fatalf("case-insensitive: %+v %v", e, err)
			}
			if want := time.Date(2024, 7, 15, 12, 34, 56, 0, time.UTC); !e.ModTime.Equal(want) {
				t.Fatalf("time %v", e.ModTime)
			}
			if _, err := fsys.Stat("/deleted.bin"); !errors.Is(err, ErrNotFound) {
				t.Fatalf("deleted: %v", err)
			}
			if _, err := fsys.ReadDir("/README.TXT"); !errors.Is(err, ErrNotDir) {
				t.Fatalf("ReadDir of a file: %v", err)
			}
			if _, _, err := fsys.Open("/EMPTYDIR"); !errors.Is(err, ErrIsDir) {
				t.Fatalf("Open of a directory: %v", err)
			}
			// The extents of a directory: its clusters
			if ext, err := fsys.Extents("/Sub Directory/deeper"); err != nil || len(ext) == 0 {
				t.Fatalf("directory extents: %v %v", ext, err)
			}
		})
	}
}

// A FAT12 floppy with a damaged FAT: a loop, a chain that ends early, a
// free cluster in a chain
func TestFATDamaged(t *testing.T) {
	m := newFATBuilder(t, 12, 2880, 1)
	m.file(m.root, "loop.bin", make([]byte, 4*512))
	m.file(m.root, "short.bin", make([]byte, 4*512))
	m.file(m.root, "free.bin", make([]byte, 4*512))
	sub := m.mkdir(m.root, "dirloop")
	m.file(sub, "a", []byte("a"))
	img := m.finish()
	loop := m.root.kids[0].clus
	m.setFAT(loop[2], loop[0])
	short := m.root.kids[1].clus
	m.setFAT(short[1], m.eoc())
	free := m.root.kids[2].clus
	m.setFAT(free[1], 0)
	m.setFAT(sub.clus[0], sub.clus[0])
	fsys, err := Open(bytes.NewReader(img), int64(len(img)))
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"/loop.bin", "/short.bin", "/free.bin"} {
		if _, _, err := fsys.Open(p); !errors.Is(err, errFATChain) {
			t.Errorf("%s: %v", p, err)
		}
	}
	if _, err := fsys.ReadDir("/dirloop"); !errors.Is(err, errFATChain) {
		t.Errorf("a directory in a loop: %v", err)
	}
	// Truncated volumes give errors, not panics
	for _, n := range []int{512, 1024, 10000, 20000} {
		if f, err := Open(bytes.NewReader(img[:n]), int64(n)); err == nil {
			budget := 1000
			fatWalk(f, "/", 0, &budget)
		}
	}
}

// ---- Real volumes made by mkfs.fat and mtools, when they are installed ----

func fatTool(t *testing.T, names ...string) string {
	for _, n := range names {
		if p, err := exec.LookPath(n); err == nil {
			return p
		}
	}
	t.Skipf("%s not installed", names[0])
	return ""
}

func TestFATTools(t *testing.T) {
	mkfs := fatTool(t, "mkfs.fat", "mkfs.vfat", "mkdosfs")
	mcopy := fatTool(t, "mcopy")
	mmd := fatTool(t, "mmd")
	mdel := fatTool(t, "mdel")
	for _, c := range []struct {
		bits int
		kb   string
	}{{12, "1440"}, {16, "32768"}, {32, "40000"}} {
		t.Run(fmt.Sprintf("FAT%d", c.bits), func(t *testing.T) {
			dir := t.TempDir()
			img := filepath.Join(dir, "v.img")
			run := func(name string, args ...string) {
				cmd := exec.Command(name, args...)
				cmd.Env = append(os.Environ(), "MTOOLS_SKIP_CHECK=1")
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("%s %v: %v\n%s", name, args, err, out)
				}
			}
			args := []string{"-C", "-F", fmt.Sprint(c.bits), "-n", "TOOLVOL", "-s", "1"}
			run(mkfs, append(args, img, c.kb)...)
			files := map[string]string{}
			put := func(p string, data []byte) {
				src := filepath.Join(dir, "src")
				os.WriteFile(src, data, 0o644)
				run(mcopy, "-i", img, src, "::"+p)
				files[p] = string(data)
			}
			pat := func(n int, seed byte) []byte {
				b := make([]byte, n)
				for i := range b {
					b[i] = byte(i*13) ^ seed
				}
				return b
			}
			run(mmd, "-i", img, "::/Long Directory Name")
			run(mmd, "-i", img, "::/Long Directory Name/Ünïcödé")
			put("/small.txt", []byte("small"))
			put("/empty.txt", nil)
			put("/Long Directory Name/Ünïcödé/inner file.bin", pat(5000, 1))
			put("/a.bin", pat(3000, 2))
			put("/gone.bin", pat(3000, 3))
			put("/c.bin", pat(3000, 4))
			run(mdel, "-i", img, "::/gone.bin")
			put("/fragmented.bin", pat(9000, 5))
			b, err := os.ReadFile(img)
			if err != nil {
				t.Fatal(err)
			}
			fsys, err := Open(bytes.NewReader(b), int64(len(b)))
			if err != nil {
				t.Fatal(err)
			}
			if fsys.Type() != fmt.Sprintf("FAT%d", c.bits) || fsys.Label() != "TOOLVOL" {
				t.Fatalf("%s %q", fsys.Type(), fsys.Label())
			}
			fatCheckFS(t, fsys, b, files, []string{"/Long Directory Name", "/Long Directory Name/Ünïcödé"})
			if ext, _ := fsys.Extents("/fragmented.bin"); len(ext) < 2 {
				t.Errorf("not fragmented: %+v", ext)
			}
		})
	}
}

func FuzzFAT(f *testing.F) {
	m := newFATBuilder(f, 12, 64, 1)
	m.label = "FUZZ"
	m.file(m.root, "A long name.txt", []byte("hello"))
	sub := m.mkdir(m.root, "SUB")
	m.file(sub, "x.bin", make([]byte, 1500))
	f.Add(m.finish())
	m = newFATBuilder(f, 32, 66700, 1)
	m.file(m.root, "file.txt", []byte("fat32"))
	img := m.finish()
	f.Add(img[:40000]) // the boot sector, the FSInfo, a part of the FAT
	f.Fuzz(func(t *testing.T, b []byte) {
		defer fuzzguard.Start(t.Name())()
		fsys, err := Open(bytes.NewReader(b), int64(len(b)))
		if err != nil || fsys == nil {
			return
		}
		fsys.Type()
		fsys.Label()
		budget := 2000
		fatWalk(fsys, "/", 0, &budget)
	})
}

// Empty volumes made by mkfs.fat alone (without mtools to add files): the
// type and the label are read
func TestFATMkfsOnly(t *testing.T) {
	mkfs := fatTool(t, "mkfs.fat", "mkfs.vfat", "mkdosfs")
	for _, c := range []struct {
		bits int
		kb   string
		want string
	}{{12, "1440", "FAT12"}, {16, "32768", "FAT16"}, {32, "40000", "FAT32"}} {
		img := filepath.Join(t.TempDir(), "v.img")
		out, err := exec.Command(mkfs, "-C", "-F", fmt.Sprint(c.bits), "-n", "ALTHEX", img, c.kb).CombinedOutput()
		if err != nil {
			t.Logf("FAT%d: %s: %v\n%s", c.bits, mkfs, err, out)
			continue
		}
		data, _ := os.ReadFile(img)
		f, err := Open(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			t.Fatalf("FAT%d: %v", c.bits, err)
		}
		if f.Type() != c.want || f.Label() != "ALTHEX" {
			t.Fatalf("FAT%d: %s %q", c.bits, f.Type(), f.Label())
		}
		if es, err := f.ReadDir("/"); err != nil || len(es) != 0 {
			t.Fatalf("FAT%d root: %v %v", c.bits, es, err)
		}
	}
}
