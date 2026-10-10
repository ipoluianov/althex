package diskimg

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/ipoluianov/althex/core/image/diskimgtest"
	"github.com/ipoluianov/althex/internal/fuzzguard"
)

// The raw disk of the tests: 4 MB and 12 KB, so the last block of most
// formats is partial
var raw = diskimgtest.Raw(4<<20 + 12<<10)

type memReader []byte

func (m memReader) ReadAt(p []byte, off int64) (int, error) {
	if off >= int64(len(m)) {
		return 0, io.EOF
	}
	n := copy(p, m[off:])
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

func openBytes(t *testing.T, b []byte) Disk {
	t.Helper()
	d, err := OpenReader(memReader(b), int64(len(b)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

// checkDisk reads the disk back: all of it, at random offsets and lengths,
// across the end and from many goroutines
func checkDisk(t *testing.T, d Disk, want []byte, format string) {
	t.Helper()
	if d.Size() != int64(len(want)) {
		t.Fatalf("size %d, want %d", d.Size(), len(want))
	}
	if format != "" && d.Format() != format {
		t.Fatalf("format %q, want %q", d.Format(), format)
	}
	all := make([]byte, len(want))
	if n, err := d.ReadAt(all, 0); n != len(all) || (err != nil && err != io.EOF) {
		t.Fatalf("read all: %d %v", n, err)
	}
	if i := firstDiff(all, want); i >= 0 {
		t.Fatalf("differs at %d: %02x, want %02x", i, all[i], want[i])
	}
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for g := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rnd := rand.New(rand.NewSource(int64(g)))
			buf := make([]byte, 300<<10)
			for range 150 {
				off := rnd.Int63n(int64(len(want)))
				n := rnd.Intn(len(buf))
				switch rnd.Intn(4) {
				case 0:
					n = rnd.Intn(1100)
				case 1:
					// Around a boundary of 64 KB
					off = (off&^0xFFFF + int64(rnd.Intn(64)) - 32 + int64(len(want))) % int64(len(want))
					n = rnd.Intn(200)
				}
				k, err := d.ReadAt(buf[:n], off)
				end := min(off+int64(n), int64(len(want)))
				if k != int(end-off) || (err != nil && !(err == io.EOF && off+int64(n) > int64(len(want)))) {
					errs <- fmt.Errorf("read %d at %d: %d %v", n, off, k, err)
					return
				}
				if !bytes.Equal(buf[:k], want[off:end]) {
					errs <- fmt.Errorf("read %d at %d: wrong bytes at %d", n, off, int64(firstDiff(buf[:k], want[off:end]))+off)
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	// At and past the end
	var b [16]byte
	if n, err := d.ReadAt(b[:], d.Size()); n != 0 || err != io.EOF {
		t.Fatalf("read at the end: %d %v", n, err)
	}
	if n, err := d.ReadAt(b[:], d.Size()-5); n != 5 || err != io.EOF {
		t.Fatalf("read across the end: %d %v", n, err)
	}
	if _, err := d.ReadAt(b[:], -1); err == nil {
		t.Fatal("a negative offset is read")
	}
}

func firstDiff(a, b []byte) int {
	for i := range min(len(a), len(b)) {
		if a[i] != b[i] {
			return i
		}
	}
	if len(a) != len(b) {
		return min(len(a), len(b))
	}
	return -1
}

func TestRawHasZeros(t *testing.T) {
	// The tests need the zero areas the formats leave out
	if !bytes.Equal(raw[1<<20:5<<19], make([]byte, 3<<19)) || raw[0] != 0 || raw[8] == 0 {
		t.Fatal("the raw content")
	}
}

func TestVHD(t *testing.T) {
	checkDisk(t, openBytes(t, diskimgtest.VHDFixed(raw)), raw, "VHD (fixed)")
	for _, block := range []int{512 << 10, 2 << 20, 4096} {
		img := diskimgtest.VHDDynamic(raw, block)
		checkDisk(t, openBytes(t, img), raw, "VHD (dynamic)")
		// The footer at the end lost: the copy at the start is used
		cut := img[:len(img)-512]
		checkDisk(t, openBytes(t, cut), raw, "VHD (dynamic)")
	}
}

func TestVHDDifferencing(t *testing.T) {
	img := diskimgtest.VHDDynamic(raw[:1<<20], 1<<20)
	for _, f := range [][]byte{img[:512], img[len(img)-512:]} {
		f[63] = 4
		diskimgtest.VHDChecksum(f)
	}
	name := "C:\\disks\\parent.vhd"
	for i, c := range name {
		img[512+64+i*2+1] = byte(c)
	}
	var pe *ParentError
	if _, err := OpenReader(memReader(img), int64(len(img))); !errors.As(err, &pe) || pe.Parent != name {
		t.Fatalf("%v", err)
	}
}

func TestVHDX(t *testing.T) {
	img := diskimgtest.VHDX(raw, 1<<20, diskimgtest.VHDXOptions{})
	checkDisk(t, openBytes(t, img), raw, "VHDX")
	img = diskimgtest.VHDX(raw, 2<<20, diskimgtest.VHDXOptions{Log: true})
	checkDisk(t, openBytes(t, img), raw, "VHDX (log not replayed)")
	small := raw[:1<<20+8192]
	checkDisk(t, openBytes(t, diskimgtest.VHDX(small, 1<<20, diskimgtest.VHDXOptions{Compact: true})), small, "VHDX")

	// The first header damaged: the second one is used; both - an error
	img = diskimgtest.VHDX(raw, 1<<20, diskimgtest.VHDXOptions{})
	img[128<<10+100] ^= 1
	checkDisk(t, openBytes(t, img), raw, "VHDX")
	img[64<<10+100] ^= 1
	if _, err := OpenReader(memReader(img), int64(len(img))); err == nil {
		t.Fatal("no valid header is opened")
	}

	// A parent: the flag in the file parameters
	img = diskimgtest.VHDX(raw[:1<<20], 1<<20, diskimgtest.VHDXOptions{})
	img[2<<20+4096+4] |= 2
	var pe *ParentError
	if _, err := OpenReader(memReader(img), int64(len(img))); !errors.As(err, &pe) {
		t.Fatalf("%v", err)
	}
}

func TestVMDK(t *testing.T) {
	for _, g := range []struct{ grain, gtes int }{{16, 64}, {128, 512}, {1, 4}} {
		checkDisk(t, openBytes(t, diskimgtest.VMDKSparse(raw, g.grain, g.gtes)), raw, "VMDK (sparse)")
	}
	checkDisk(t, openBytes(t, diskimgtest.VMDKStream(raw)), raw, "VMDK (stream-optimized)")
}

func TestVMDKDescriptor(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, b []byte) {
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// A flat extent from sector 2 of its file, zeros, a sparse extent
	write("disk-f001.vmdk", append(make([]byte, 1024), raw[:1<<20]...))
	write("disk-s003.vmdk", diskimgtest.VMDKSparse(raw[2<<20:], 16, 128))
	rest := (len(raw) - 2<<20) / 512
	desc := diskimgtest.VMDKDescriptor("twoGbMaxExtentSparse",
		`RW 2048 FLAT "disk-f001.vmdk" 2`,
		`RW 2048 ZERO`,
		fmt.Sprintf(`RW %d SPARSE "disk-s003.vmdk"`, rest))
	write("disk.vmdk", []byte(desc))
	if f, ok := Detect(memReader(desc), int64(len(desc))); !ok || f != "VMDK (descriptor)" {
		t.Fatalf("detect %q", f)
	}
	d, err := Open(filepath.Join(dir, "disk.vmdk"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	checkDisk(t, d, raw, "VMDK (twoGbMaxExtentSparse)")
	// Over a reader the other files cannot be found
	if _, err := OpenReader(memReader(desc), int64(len(desc))); err == nil {
		t.Fatal("a descriptor is opened without its files")
	}
	// A missing extent file
	write("bad.vmdk", []byte(diskimgtest.VMDKDescriptor("monolithicFlat", `RW 2048 FLAT "nothing.vmdk" 0`)))
	if _, err := Open(filepath.Join(dir, "bad.vmdk")); err == nil {
		t.Fatal("a missing extent")
	}
	// A child of another disk
	child := strings.Replace(diskimgtest.VMDKDescriptor("monolithicSparse", `RW 2048 SPARSE "c.vmdk"`),
		"parentCID=ffffffff", "parentCID=12345678\nparentFileNameHint=\"base.vmdk\"", 1)
	write("child.vmdk", []byte(child))
	var pe *ParentError
	if _, err := Open(filepath.Join(dir, "child.vmdk")); !errors.As(err, &pe) || pe.Parent != "base.vmdk" {
		t.Fatalf("%v", err)
	}
}

func TestQCOW2(t *testing.T) {
	for _, o := range []diskimgtest.QCOW2Options{
		{Version: 3, ClusterBits: 16, Compress: true},
		{Version: 3, ClusterBits: 9, Compress: true},
		{Version: 3, ClusterBits: 12},
		{Version: 2, ClusterBits: 16, Compress: true},
		{Version: 2, ClusterBits: 21, Compress: true},
	} {
		t.Run(fmt.Sprint(o.Version, "-", o.ClusterBits), func(t *testing.T) {
			checkDisk(t, openBytes(t, diskimgtest.QCOW2(raw, o)), raw, "QCOW2")
		})
	}
	// Odd sizes
	odd := raw[:1<<20+777]
	checkDisk(t, openBytes(t, diskimgtest.QCOW2(odd, diskimgtest.QCOW2Options{Version: 3, ClusterBits: 16, Compress: true})), odd, "QCOW2")
}

func TestQCOW2Refused(t *testing.T) {
	small := raw[:256<<10]
	open := func(b []byte) error {
		_, err := OpenReader(memReader(b), int64(len(b)))
		return err
	}
	var pe *ParentError
	img := diskimgtest.QCOW2(small, diskimgtest.QCOW2Options{Version: 3, ClusterBits: 16, Backing: "base.qcow2"})
	if err := open(img); !errors.As(err, &pe) || pe.Parent != "base.qcow2" {
		t.Fatalf("backing: %v", err)
	}
	img = diskimgtest.QCOW2(small, diskimgtest.QCOW2Options{Version: 3, ClusterBits: 16})
	img[79] |= 8 // the compression type feature
	img[104] = 1 // zstd
	if err := open(img); err == nil || !strings.Contains(err.Error(), "zstd") {
		t.Fatalf("zstd: %v", err)
	}
	img = diskimgtest.QCOW2(small, diskimgtest.QCOW2Options{Version: 3, ClusterBits: 16})
	img[35] = 2 // LUKS
	if err := open(img); err == nil || !strings.Contains(err.Error(), "encrypted") {
		t.Fatalf("encryption: %v", err)
	}
	img = diskimgtest.QCOW2(small, diskimgtest.QCOW2Options{Version: 3, ClusterBits: 16})
	img[79] |= 4 // an external data file
	if err := open(img); err == nil || !strings.Contains(err.Error(), "external") {
		t.Fatalf("data file: %v", err)
	}
}

func TestVDI(t *testing.T) {
	for _, block := range []int{1 << 20, 64 << 10} {
		checkDisk(t, openBytes(t, diskimgtest.VDI(raw, block)), raw, "VDI")
	}
}

func TestAndroidSparse(t *testing.T) {
	img := diskimgtest.AndroidSparse(raw, 4096)
	checkDisk(t, openBytes(t, img), raw, "Android sparse")
	checkDisk(t, openBytes(t, diskimgtest.AndroidSparse(raw, 1024)), raw, "Android sparse")
}

func TestDetect(t *testing.T) {
	cases := map[string][]byte{
		"VHD (fixed)":             diskimgtest.VHDFixed(raw[:64<<10]),
		"VHD (dynamic)":           diskimgtest.VHDDynamic(raw[:64<<10], 4096),
		"VHDX":                    diskimgtest.VHDX(nil, 1<<20, diskimgtest.VHDXOptions{Compact: true}),
		"VMDK (sparse)":           diskimgtest.VMDKSparse(raw[:64<<10], 16, 64),
		"VMDK (stream-optimized)": diskimgtest.VMDKStream(raw[:64<<10]),
		"QCOW2":                   diskimgtest.QCOW2(raw[:64<<10], diskimgtest.QCOW2Options{Version: 3, ClusterBits: 12}),
		"VDI":                     diskimgtest.VDI(raw[:64<<10], 4096),
		"Android sparse":          diskimgtest.AndroidSparse(raw[:64<<10], 4096),
	}
	for want, b := range cases {
		if f, ok := Detect(memReader(b), int64(len(b))); !ok || f != want {
			t.Errorf("detect %q, want %q", f, want)
		}
	}
	for _, b := range [][]byte{nil, {1, 2, 3}, raw[:100<<10]} {
		if f, ok := Detect(memReader(b), int64(len(b))); ok {
			t.Errorf("detect %q in what is not an image", f)
		}
		if _, err := OpenReader(memReader(b), int64(len(b))); err != ErrNotImage {
			t.Errorf("open: %v", err)
		}
	}
}

// samples are small images of all formats, for the damage tests and fuzzing
func samples() [][]byte {
	small := raw[:64<<10]
	return [][]byte{
		diskimgtest.VHDFixed(small),
		diskimgtest.VHDDynamic(small, 4096),
		diskimgtest.VHDX(nil, 1<<20, diskimgtest.VHDXOptions{Compact: true}),
		diskimgtest.VMDKSparse(small, 8, 16),
		diskimgtest.VMDKStream(small),
		diskimgtest.QCOW2(small, diskimgtest.QCOW2Options{Version: 3, ClusterBits: 12, Compress: true}),
		diskimgtest.QCOW2(small, diskimgtest.QCOW2Options{Version: 2, ClusterBits: 9}),
		diskimgtest.VDI(small, 4096),
		diskimgtest.AndroidSparse(diskimgtest.Raw(3<<20 + 64<<10)[3<<20-8192:], 4096),
	}
}

// tryOpen opens and reads a few places; it must not panic or hang
func tryOpen(b []byte) {
	d, err := OpenReader(memReader(b), int64(len(b)))
	if err != nil {
		return
	}
	defer d.Close()
	d.Format()
	size := d.Size()
	buf := make([]byte, 70000)
	for _, off := range []int64{0, 4095, size / 3, size / 2, size - 100, size - 1} {
		if off >= 0 {
			d.ReadAt(buf, off)
		}
	}
}

// Damaged containers: errors, no panics
func TestDamaged(t *testing.T) {
	for _, img := range samples() {
		for _, n := range []int{0, 1, 8, 64, 511, 512, 513, 1024, 1536, 4096, len(img) / 3, len(img) / 2, len(img) - 513, len(img) - 1} {
			if n >= 0 && n <= len(img) {
				tryOpen(img[:n])
			}
		}
		rnd := rand.New(rand.NewSource(1))
		for range 300 {
			b := append([]byte(nil), img...)
			for range 1 + rnd.Intn(4) {
				i := rnd.Intn(min(len(b), 8192))
				if rnd.Intn(3) == 0 {
					i = rnd.Intn(len(b))
				}
				b[i] = byte(rnd.Intn(256))
				if rnd.Intn(2) == 0 {
					b[i] = 0xFF
				}
			}
			tryOpen(b)
		}
	}
}

// A truncated container: reading the cut part is an error, not zeros
func TestTruncatedRead(t *testing.T) {
	img := diskimgtest.QCOW2(raw, diskimgtest.QCOW2Options{Version: 3, ClusterBits: 16})
	img = img[:len(img)-100000]
	d := openBytes(t, img)
	buf := make([]byte, len(raw))
	if _, err := d.ReadAt(buf, 0); err == nil || err == io.EOF {
		t.Fatalf("a truncated image read: %v", err)
	}
}

func FuzzOpen(f *testing.F) {
	for _, s := range samples() {
		f.Add(s)
	}
	f.Add([]byte(diskimgtest.VMDKDescriptor("monolithicFlat", `RW 2048 FLAT "x.vmdk" 0`)))
	f.Fuzz(func(t *testing.T, b []byte) {
		defer fuzzguard.Start(t.Name())()
		Detect(memReader(b), int64(len(b)))
		tryOpen(b)
	})
}

// Images made by real tools, when they are installed

func toolImage(t *testing.T, tool string, args func(in, out string) []string) string {
	t.Helper()
	if _, err := exec.LookPath(tool); err != nil {
		t.Skip(tool + " is not installed")
	}
	dir := t.TempDir()
	in := filepath.Join(dir, "disk.raw")
	out := filepath.Join(dir, "disk.img")
	if err := os.WriteFile(in, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	a := args(in, out)
	if b, err := exec.Command(tool, a...).CombinedOutput(); err != nil {
		t.Fatalf("%s %v: %v\n%s", tool, a, err, b)
	}
	return out
}

func checkToolImage(t *testing.T, path string, want []byte) {
	t.Helper()
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	// Tools may round the size up
	if d.Size() < int64(len(want)) {
		t.Fatalf("size %d", d.Size())
	}
	got := make([]byte, d.Size())
	if _, err := d.ReadAt(got, 0); err != nil && err != io.EOF {
		t.Fatal(err)
	}
	if !bytes.Equal(got[:len(want)], want) || !bytes.Equal(got[len(want):], make([]byte, len(got)-len(want))) {
		t.Fatalf("differs at %d", firstDiff(got[:len(want)], want))
	}
	checkDisk(t, d, got, "")
}

func TestQemuImg(t *testing.T) {
	if _, err := exec.LookPath("qemu-img"); err != nil {
		t.Skip("qemu-img is not installed")
	}
	for _, f := range []struct{ name, format string }{
		{"vpc-dynamic", "-O vpc"},
		{"vpc-fixed", "-O vpc -o subformat=fixed,force_size=on"},
		{"vhdx", "-O vhdx"},
		{"vhdx-small-blocks", "-O vhdx -o block_size=1M"},
		{"vmdk", "-O vmdk"},
		{"vmdk-stream", "-O vmdk -o subformat=streamOptimized"},
		{"vmdk-flat", "-O vmdk -o subformat=monolithicFlat"},
		{"vmdk-split", "-O vmdk -o subformat=twoGbMaxExtentSparse"},
		{"qcow2", "-O qcow2"},
		{"qcow2-compressed", "-c -O qcow2"},
		{"qcow2-v2", "-O qcow2 -o compat=0.10"},
		{"qcow2-small", "-O qcow2 -o cluster_size=4096"},
		{"vdi", "-O vdi"},
	} {
		t.Run(f.name, func(t *testing.T) {
			out := toolImage(t, "qemu-img", func(in, out string) []string {
				return append(append([]string{"convert", "-f", "raw"}, strings.Fields(f.format)...), in, out)
			})
			checkToolImage(t, out, raw)
		})
	}
}

func TestVBoxManage(t *testing.T) {
	if _, err := exec.LookPath("VBoxManage"); err != nil {
		t.Skip("VBoxManage is not installed")
	}
	for _, f := range []string{"VDI", "VHD", "VMDK"} {
		t.Run(f, func(t *testing.T) {
			out := toolImage(t, "VBoxManage", func(in, out string) []string {
				return []string{"convertfromraw", in, out, "--format", f}
			})
			checkToolImage(t, out, raw)
		})
	}
}

func TestImg2simg(t *testing.T) {
	out := toolImage(t, "img2simg", func(in, out string) []string { return []string{in, out} })
	checkToolImage(t, out, raw)
}
