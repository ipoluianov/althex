package fs

import (
	"bytes"
	"encoding/binary"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/ipoluianov/althex/internal/fuzzguard"
)

// ---- An ISO 9660 image made in Go ----

type isoNodeT struct {
	name  string // the Rock Ridge and Joliet name
	dir   bool
	data  []byte
	parts int // more than 1: a file of several extents
	link  string
	ce    bool // the Rock Ridge name is in a continuation area
	kids  []*isoNodeT
	ext   [][2]uint32 // the block and length of each extent of a file
	ceLBA uint32
	lba   [2]uint32 // of a directory in the primary and the Joliet tree
	size  [2]uint32
}

// isoBuilder writes an image with a primary volume descriptor, optionally
// a Joliet one and Rock Ridge entries
type isoBuilder struct {
	b      []byte
	rr     bool
	joliet bool
	root   *isoNodeT
}

func (m *isoBuilder) alloc(n int) uint32 {
	lba := uint32(len(m.b) / isoSector)
	m.b = append(m.b, make([]byte, n*isoSector)...)
	return lba
}

func isoBoth32(b []byte, v uint32) {
	binary.LittleEndian.PutUint32(b, v)
	binary.BigEndian.PutUint32(b[4:], v)
}

func isoBoth16(b []byte, v uint16) {
	binary.LittleEndian.PutUint16(b, v)
	binary.BigEndian.PutUint16(b[2:], v)
}

// isoDate is 2024-05-06 07:08:09 UTC+1
var isoDate = []byte{124, 5, 6, 7, 8, 9, 4}

func isoRec(name []byte, lba, size uint32, flags byte, su []byte) []byte {
	n := len(name)
	l := 33 + n
	if n%2 == 0 {
		l++
	}
	l += len(su)
	l += l % 2
	r := make([]byte, l)
	r[0] = byte(l)
	isoBoth32(r[2:], lba)
	isoBoth32(r[10:], size)
	copy(r[18:], isoDate)
	r[25] = flags
	isoBoth16(r[28:], 1)
	r[32] = byte(n)
	copy(r[33:], name)
	s := 33 + n
	if n%2 == 0 {
		s++
	}
	copy(r[s:], su)
	return r
}

func isoSU(sig string, body ...byte) []byte {
	return append([]byte{sig[0], sig[1], byte(4 + len(body)), 1}, body...)
}

func isoPX(mode uint32) []byte {
	b := make([]byte, 32)
	isoBoth32(b, mode)
	isoBoth32(b[8:], 1)
	return isoSU("PX", b...)
}

// isoPlain is the ISO 9660 name of a Rock Ridge one: d-characters
func isoPlain(name string, dir bool) string {
	s := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			return r - 32
		case r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.':
			return r
		}
		return '_'
	}, name)
	if !dir {
		if !strings.Contains(s, ".") {
			s += "."
		}
		s += ";1"
	}
	return s
}

func (m *isoBuilder) su(k *isoNodeT, mode uint32) []byte {
	var su []byte
	if k.ce {
		su = append(su, isoSU("CE", make([]byte, 24)...)...)
		isoBoth32(su[4:], k.ceLBA)
		isoBoth32(su[12:], 0)
		isoBoth32(su[20:], uint32(len(m.ceArea(k))))
	} else {
		su = append(su, isoSU("NM", append([]byte{0}, k.name...)...)...)
	}
	su = append(su, isoPX(mode)...)
	su = append(su, isoSU("TF", append([]byte{0x02}, isoDate...)...)...)
	if k.link != "" {
		var comp []byte
		for i, c := range strings.Split(k.link, "/") {
			switch {
			case c == "" && i == 0:
				comp = append(comp, 8, 0)
			case c == "..":
				comp = append(comp, 4, 0)
			case c == ".":
				comp = append(comp, 2, 0)
			default:
				comp = append(append(comp, 0, byte(len(c))), c...)
			}
		}
		su = append(su, isoSU("SL", append([]byte{0}, comp...)...)...)
	}
	return su
}

// ceArea is what a continuation area holds: the name, split in two NM
// entries to test their joining
func (m *isoBuilder) ceArea(k *isoNodeT) []byte {
	h := len(k.name) / 2
	b := isoSU("NM", append([]byte{1}, k.name[:h]...)...)
	b = append(b, isoSU("NM", append([]byte{0}, k.name[h:]...)...)...)
	return append(b, isoSU("ST")...)
}

func (m *isoBuilder) mode(k *isoNodeT) uint32 {
	switch {
	case k.dir:
		return 0o40755
	case k.link != "":
		return 0o120777
	}
	return 0o100644
}

// records of a directory in tree t (0 primary, 1 Joliet)
func (m *isoBuilder) records(d, parent *isoNodeT, t int) [][]byte {
	var dotSU, dotdotSU []byte
	rr := m.rr && t == 0
	if rr {
		if d == m.root {
			dotSU = isoSU("SP", 0xBE, 0xEF, 0)
		}
		dotSU = append(dotSU, isoPX(0o40755)...)
		dotdotSU = isoPX(0o40755)
	}
	out := [][]byte{
		isoRec([]byte{0}, d.lba[t], d.size[t], 2, dotSU),
		isoRec([]byte{1}, parent.lba[t], parent.size[t], 2, dotdotSU),
	}
	for _, k := range d.kids {
		var name []byte
		if t == 1 {
			n := k.name
			if !k.dir {
				n += ";1"
			}
			for _, u := range utf16.Encode([]rune(n)) {
				name = binary.BigEndian.AppendUint16(name, u)
			}
		} else {
			name = []byte(isoPlain(k.name, k.dir))
		}
		var su []byte
		if rr {
			su = m.su(k, m.mode(k))
		}
		switch {
		case k.dir:
			out = append(out, isoRec(name, k.lba[t], k.size[t], 2, su))
		case len(k.ext) == 0:
			out = append(out, isoRec(name, 0, 0, 0, su))
		default:
			for i, x := range k.ext {
				var fl byte
				if i+1 < len(k.ext) {
					fl = 0x80
				}
				out = append(out, isoRec(name, x[0], x[1], fl, su))
			}
		}
	}
	return out
}

// layout puts records into sectors: a record does not cross one
func isoLayout(recs [][]byte) []byte {
	var b []byte
	for _, r := range recs {
		if len(b)/isoSector != (len(b)+len(r)-1)/isoSector {
			b = append(b, make([]byte, isoSector-len(b)%isoSector)...)
		}
		b = append(b, r...)
	}
	return append(b, make([]byte, (isoSector-len(b)%isoSector)%isoSector)...)
}

func (m *isoBuilder) finish(label string) []byte {
	m.b = make([]byte, 16*isoSector)
	pvd := m.alloc(1)
	var svd uint32
	if m.joliet {
		svd = m.alloc(1)
	}
	term := m.alloc(1)
	var walk func(d *isoNodeT, fn func(k, parent *isoNodeT))
	walk = func(d *isoNodeT, fn func(k, parent *isoNodeT)) {
		for _, k := range d.kids {
			fn(k, d)
			if k.dir {
				walk(k, fn)
			}
		}
	}
	// File data; the parts of a file of several extents are apart
	walk(m.root, func(k, _ *isoNodeT) {
		if k.dir || len(k.data) == 0 {
			return
		}
		parts := max(k.parts, 1)
		per := max(len(k.data)/parts/isoSector, 1) * isoSector
		for p, off := 0, 0; off < len(k.data); p++ {
			n := min(per, len(k.data)-off)
			if p == parts-1 {
				n = len(k.data) - off
			}
			lba := m.alloc((n + isoSector - 1) / isoSector)
			copy(m.b[int(lba)*isoSector:], k.data[off:off+n])
			k.ext = append(k.ext, [2]uint32{lba, uint32(n)})
			off += n
			m.alloc(1) // a gap
		}
	})
	if m.rr {
		walk(m.root, func(k, _ *isoNodeT) {
			if k.ce {
				k.ceLBA = m.alloc(1)
				copy(m.b[int(k.ceLBA)*isoSector:], m.ceArea(k))
			}
		})
	}
	trees := 1
	if m.joliet {
		trees = 2
	}
	for t := 0; t < trees; t++ {
		all := []*isoNodeT{m.root}
		parents := []*isoNodeT{m.root}
		walk(m.root, func(k, p *isoNodeT) {
			if k.dir {
				all = append(all, k)
				parents = append(parents, p)
			}
		})
		for i, d := range all {
			n := len(isoLayout(m.records(d, parents[i], t)))
			d.size[t] = uint32(n)
			d.lba[t] = m.alloc(n / isoSector)
		}
		for i, d := range all {
			copy(m.b[int(d.lba[t])*isoSector:], isoLayout(m.records(d, parents[i], t)))
		}
	}
	vd := func(lba uint32, typ byte, joliet bool) {
		b := m.b[int(lba)*isoSector:][:isoSector]
		b[0] = typ
		copy(b[1:], "CD001\x01")
		copy(b[8:40], strings.Repeat(" ", 32))
		copy(b[40:72], strings.Repeat(" ", 32))
		t := 0
		if joliet {
			t = 1
			copy(b[88:], "%/E")
			var l []byte
			for _, u := range utf16.Encode([]rune("Joliet " + label)) {
				l = binary.BigEndian.AppendUint16(l, u)
			}
			copy(b[40:72], bytes.Repeat([]byte{0, ' '}, 16))
			copy(b[40:72], l)
		} else {
			copy(b[40:], label)
		}
		isoBoth32(b[80:], uint32(len(m.b)/isoSector))
		isoBoth16(b[120:], 1)
		isoBoth16(b[124:], 1)
		isoBoth16(b[128:], isoSector)
		copy(b[156:], isoRec([]byte{0}, m.root.lba[t], m.root.size[t], 2, nil))
		b[881] = 1
	}
	vd(pvd, 1, false)
	if m.joliet {
		vd(svd, 2, true)
	}
	copy(m.b[int(term)*isoSector:], "\xFFCD001\x01")
	return m.b
}

// isoTestTree: names as Rock Ridge and Joliet keep them
func isoTestTree() *isoNodeT {
	pat := func(n int, seed byte) []byte {
		b := make([]byte, n)
		for i := range b {
			b[i] = byte(i*5) ^ seed
		}
		return b
	}
	sub := &isoNodeT{name: "Sub Dir", dir: true, kids: []*isoNodeT{
		{name: "Deeper", dir: true, kids: []*isoNodeT{{name: "data file.bin", data: pat(5000, 1)}}},
		{name: "a.txt", data: []byte("a")},
	}}
	return &isoNodeT{dir: true, kids: []*isoNodeT{
		{name: "Hello World.txt", data: []byte("hello, ISO\n")},
		{name: "big multi.bin", data: pat(3*isoSector+100, 2), parts: 3},
		{name: "empty", data: nil},
		sub,
	}}
}

// isoExpect lists the files and directories of a tree under the names a
// reader should give
func isoExpect(root *isoNodeT, name func(*isoNodeT) string) (map[string]string, []string) {
	files := map[string]string{}
	var dirs []string
	var walk func(d *isoNodeT, p string)
	walk = func(d *isoNodeT, p string) {
		for _, k := range d.kids {
			kp := fatJoin(p, name(k))
			switch {
			case k.dir:
				dirs = append(dirs, kp)
				walk(k, kp)
			case k.link != "":
				files[kp] = isoListed
			default:
				files[kp] = string(k.data)
			}
		}
	}
	walk(root, "/")
	return files, dirs
}

// isoListed marks an entry that is only listed (a link): fatCheckFS skips
// its contents
const isoListed = "\x00listed only"

func isoOpen(t *testing.T, img []byte) FS {
	t.Helper()
	fsys, err := Open(bytes.NewReader(img), int64(len(img)))
	if err != nil {
		t.Fatal(err)
	}
	return fsys
}

func TestISOPlainAndJoliet(t *testing.T) {
	m := &isoBuilder{root: isoTestTree()}
	img := m.finish("PLAIN")
	fsys := isoOpen(t, img)
	if fsys.Type() != "ISO 9660" || fsys.Label() != "PLAIN" {
		t.Fatalf("%s %q", fsys.Type(), fsys.Label())
	}
	files, dirs := isoExpect(m.root, func(k *isoNodeT) string {
		return strings.TrimSuffix(strings.TrimSuffix(isoPlain(k.name, k.dir), ";1"), ".")
	})
	fatCheckFS(t, fsys, img, files, dirs)
	if ext, _ := fsys.Extents("/BIG_MULTI.BIN"); len(ext) != 3 {
		t.Fatalf("multi-extent: %+v", ext)
	}
	if e, err := fsys.Stat("/sub_dir/deeper/Data_File.bin"); err != nil || e.Name != "DATA_FILE.BIN" {
		t.Fatalf("case-insensitive: %+v %v", e, err)
	}
	if e, _ := fsys.Stat("/HELLO_WORLD.TXT"); !e.ModTime.Equal(time.Date(2024, 5, 6, 6, 8, 9, 0, time.UTC)) {
		t.Fatalf("time %v", e.ModTime)
	}

	m = &isoBuilder{root: isoTestTree(), joliet: true}
	img = m.finish("J")
	fsys = isoOpen(t, img)
	if fsys.Type() != "ISO 9660 (Joliet)" || fsys.Label() != "Joliet J" {
		t.Fatalf("%s %q", fsys.Type(), fsys.Label())
	}
	files, dirs = isoExpect(m.root, func(k *isoNodeT) string { return k.name })
	fatCheckFS(t, fsys, img, files, dirs)
	if _, err := fsys.Stat("/SUB DIR/deeper"); err != nil {
		t.Fatalf("case-insensitive: %v", err)
	}
}

func TestISORockRidge(t *testing.T) {
	root := isoTestTree()
	root.kids = append(root.kids,
		&isoNodeT{name: "link", link: "../Sub Dir/a.txt"},
		&isoNodeT{name: "abs", link: "/etc/passwd"},
		&isoNodeT{name: "Name in a continuation area.txt", data: []byte("ce"), ce: true},
		&isoNodeT{name: "MixedCase", data: []byte("1")},
		&isoNodeT{name: "mixedcase", data: []byte("2")},
	)
	m := &isoBuilder{root: root, rr: true, joliet: true}
	img := m.finish("RR")
	fsys := isoOpen(t, img)
	if fsys.Type() != "ISO 9660 (Rock Ridge)" || fsys.Label() != "RR" {
		t.Fatalf("%s %q", fsys.Type(), fsys.Label())
	}
	files, dirs := isoExpect(m.root, func(k *isoNodeT) string { return k.name })
	fatCheckFS(t, fsys, img, files, dirs)
	if _, err := fsys.Stat("/sub dir"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Rock Ridge names are case-sensitive: %v", err)
	}
	e, err := fsys.Stat("/link")
	if err != nil || e.Mode != 0o120777 {
		t.Fatalf("link: %+v %v", e, err)
	}
	if l := fsys.(*isoFS); true {
		le, _ := l.lookup("/link")
		ae, _ := l.lookup("/abs")
		if le.link != "../Sub Dir/a.txt" || ae.link != "/etc/passwd" {
			t.Fatalf("links %q %q", le.link, ae.link)
		}
	}
	if e, _ := fsys.Stat("/Sub Dir"); e.Mode != 0o40755 || !e.IsDir {
		t.Fatalf("dir mode %o", e.Mode)
	}
}

// Damaged images: records with wrong lengths, a CE loop, extents past
// the end give errors or less, never a panic
func TestISODamaged(t *testing.T) {
	root := isoTestTree()
	root.kids = append(root.kids, &isoNodeT{name: "ce", data: []byte("x"), ce: true})
	m := &isoBuilder{root: root, rr: true}
	img := m.finish("X")
	for _, n := range []int{17 * isoSector, 18*isoSector + 100, len(img) / 2, len(img) - 1} {
		if fsys, err := Open(bytes.NewReader(img[:n]), int64(n)); err == nil {
			budget := 1000
			fatWalk(fsys, "/", 0, &budget)
		}
	}
	for off := 16 * isoSector; off < len(img); off += 97 {
		b := bytes.Clone(img)
		b[off] ^= 0xFF
		if fsys, err := Open(bytes.NewReader(b), int64(len(b))); err == nil {
			budget := 1000
			fatWalk(fsys, "/", 0, &budget)
		}
	}
}

// ---- Real images made by genisoimage / mkisofs / xorriso ----

func TestISOTools(t *testing.T) {
	tool := fatTool(t, "genisoimage", "mkisofs", "xorrisofs")
	src := t.TempDir()
	write := func(p string, data []byte) {
		full := filepath.Join(src, p)
		os.MkdirAll(filepath.Dir(full), 0o755)
		if err := os.WriteFile(full, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	big := make([]byte, 70000)
	for i := range big {
		big[i] = byte(i * 3)
	}
	files := map[string]string{
		"/readme.txt":                           "read me",
		"/Mixed Case Dir/Nested/deep file.text": "deep",
		"/ünïcödé ✓.txt":                        "unicode",
		"/empty.dat":                            "",
		"/big.bin":                              string(big),
	}
	for p, d := range files {
		write(p, []byte(d))
	}
	if err := os.Symlink("readme.txt", filepath.Join(src, "link")); err != nil {
		t.Fatal(err)
	}
	dirs := []string{"/Mixed Case Dir", "/Mixed Case Dir/Nested"}
	mk := func(args ...string) []byte {
		img := filepath.Join(t.TempDir(), "x.iso")
		args = append(append([]string{"-quiet", "-V", "TESTISO", "-o", img}, args...), src)
		if out, err := exec.Command(tool, args...).CombinedOutput(); err != nil {
			t.Fatalf("%s %v: %v\n%s", tool, args, err, out)
		}
		b, err := os.ReadFile(img)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}

	img := mk("-R", "-J", "-joliet-long")
	fsys := isoOpen(t, img)
	if fsys.Type() != "ISO 9660 (Rock Ridge)" || fsys.Label() != "TESTISO" {
		t.Fatalf("%s %q", fsys.Type(), fsys.Label())
	}
	rrFiles := map[string]string{"/link": isoListed}
	for k, v := range files {
		rrFiles[k] = v
	}
	fatCheckFS(t, fsys, img, rrFiles, dirs)
	if e, _ := fsys.Stat("/link"); e.Mode&0o170000 != 0o120000 {
		t.Fatalf("link mode %o", e.Mode)
	}
	if e, _ := fsys.Stat("/readme.txt"); e.Mode&0o777 == 0 || e.ModTime.IsZero() {
		t.Fatalf("readme: %+v", e)
	}

	// Joliet only: the link is a file or missing; check the others
	img = mk("-J", "-joliet-long")
	fsys = isoOpen(t, img)
	if fsys.Type() != "ISO 9660 (Joliet)" {
		t.Fatal(fsys.Type())
	}
	for p, d := range files {
		r, n, err := fsys.Open(strings.ToUpper(p))
		if err != nil || n != int64(len(d)) {
			t.Fatalf("%s: %v", p, err)
		}
		b := make([]byte, n)
		r.ReadAt(b, 0)
		if string(b) != d {
			t.Fatalf("%s: contents", p)
		}
	}

	// Plain ISO 9660: 8.3 names in capitals
	img = mk()
	fsys = isoOpen(t, img)
	if e, err := fsys.Stat("/readme.txt"); err != nil || e.Name != "README.TXT" {
		t.Fatalf("plain: %+v %v", e, err)
	}
}

func FuzzISO(f *testing.F) {
	root := func() *isoNodeT {
		return &isoNodeT{dir: true, kids: []*isoNodeT{
			{name: "a.txt", data: []byte("hello")},
			{name: "multi", data: make([]byte, 2*isoSector+5), parts: 2},
			{name: "d", dir: true, kids: []*isoNodeT{{name: "ce name", data: []byte("x"), ce: true}}},
			{name: "l", link: "a.txt"},
		}}
	}
	f.Add((&isoBuilder{root: root(), rr: true}).finish("F"))
	f.Add((&isoBuilder{root: root(), joliet: true}).finish("F"))
	f.Fuzz(func(t *testing.T, b []byte) {
		defer fuzzguard.Start(t.Name())()
		fsys, err := Open(bytes.NewReader(b), int64(len(b)))
		if err != nil || fsys == nil {
			return
		}
		budget := 2000
		fatWalk(fsys, "/", 0, &budget)
	})
}
