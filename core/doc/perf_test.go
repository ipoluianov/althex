package doc

import (
	"math/rand/v2"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// hugeFile makes a sparse file of the size: it takes no space on the disk
func hugeFile(t testing.TB, size int64) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "huge.bin")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := f.Truncate(size); err != nil {
		t.Skipf("no sparse file of %d bytes here: %v", size, err)
	}
	return path
}

// PERF-2: a file of 100 GB opens and shows its first page within 1 s
// PERF-7: and takes no more than 150 MB of memory after reading all over it
func TestPerfOpenHuge(t *testing.T) {
	if raceEnabled {
		t.Skip("timings are not checked with the race detector")
	}
	path := hugeFile(t, 100<<30)
	start := time.Now()
	d, err := Open(path, Options{JournalDir: filepath.Join(t.TempDir(), "j")})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	page := make([]byte, 64*32)
	if _, err := d.ReadAt(page, 0); err != nil {
		t.Fatal(err)
	}
	if took := time.Since(start); took > time.Second {
		t.Errorf("opening took %v", took)
	}
	t.Logf("opened in %v", time.Since(start))

	r := rand.New(rand.NewPCG(1, 1))
	for i := 0; i < 5000; i++ {
		d.ReadAt(page, r.Int64N(d.Size()-int64(len(page))))
	}
	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	if ms.HeapInuse > 150<<20 {
		t.Errorf("memory after reading: %d MB", ms.HeapInuse>>20)
	}
	t.Logf("memory after reading: %d MB", ms.HeapInuse>>20)
}

// PERF-5: inserting 1 MB in the middle of a file of 50 GB takes up to 50 ms
func TestPerfInsertIntoHuge(t *testing.T) {
	if raceEnabled {
		t.Skip("timings are not checked with the race detector")
	}
	path := hugeFile(t, 50<<30)
	d, err := Open(path, Options{JournalDir: filepath.Join(t.TempDir(), "j")})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	data := make([]byte, 1<<20)
	start := time.Now()
	if err := d.Insert(25<<30, data, EditOptions{}); err != nil {
		t.Fatal(err)
	}
	if took := time.Since(start); took > 50*time.Millisecond {
		t.Errorf("inserting took %v", took)
	}
	t.Logf("inserted in %v", time.Since(start))
	if d.Size() != 50<<30+1<<20 {
		t.Fatalf("size %d", d.Size())
	}
}

// PERF-8: 10 000 edits in a row with undo do not slow down
func TestPerfManyEdits(t *testing.T) {
	if raceEnabled {
		t.Skip("timings are not checked with the race detector")
	}
	path := hugeFile(t, 1<<30)
	d, err := Open(path, Options{JournalDir: filepath.Join(t.TempDir(), "j")})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	r := rand.New(rand.NewPCG(2, 2))
	const n = 10000
	times := make([]time.Duration, n)
	for i := range n {
		off := r.Int64N(d.Size())
		start := time.Now()
		switch i % 3 {
		case 0:
			d.Insert(off, []byte{byte(i)}, EditOptions{})
		case 1:
			d.Overwrite(off, []byte{1, 2, 3, 4}, EditOptions{})
		default:
			d.Delete(off, 3, EditOptions{})
		}
		times[i] = time.Since(start)
	}
	avg := func(ts []time.Duration) time.Duration {
		var sum time.Duration
		for _, t := range ts {
			sum += t
		}
		return sum / time.Duration(len(ts))
	}
	first, last := avg(times[:1000]), avg(times[n-1000:])
	t.Logf("an edit: %v at first, %v after %d edits", first, last, n)
	if last > 10*time.Millisecond {
		t.Errorf("an edit takes %v (PERF-4: up to 10 ms)", last)
	}
	start := time.Now()
	for range n {
		if _, err := d.Undo(); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("%d undos in %v", n, time.Since(start))
	if d.Modified() {
		t.Fatal("modified after undoing all")
	}
}

func BenchmarkInsert(b *testing.B) {
	path := hugeFile(b, 1<<30)
	d, _ := Open(path, Options{JournalDir: filepath.Join(b.TempDir(), "j")})
	defer d.Close()
	r := rand.New(rand.NewPCG(3, 3))
	for b.Loop() {
		d.Insert(r.Int64N(d.Size()), []byte{1}, EditOptions{})
	}
}

func BenchmarkReadScreen(b *testing.B) {
	path := hugeFile(b, 1<<30)
	d, _ := Open(path, Options{})
	defer d.Close()
	r := rand.New(rand.NewPCG(4, 4))
	for range 5000 {
		d.Insert(r.Int64N(d.Size()), []byte{1}, EditOptions{})
	}
	screen := make([]byte, 16*60)
	off := int64(1 << 29)
	d.ReadAt(screen, off)
	for b.Loop() {
		d.ReadAt(screen, off)
	}
}
