package source

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// Reads in the background may come while the file is closed (a cancelled
// search, the minimap): they get ErrClosed, without a race
func TestReadWhileClosing(t *testing.T) {
	p := filepath.Join(t.TempDir(), "f.bin")
	os.WriteFile(p, make([]byte, 1<<16), 0o644)
	for round := 0; round < 20; round++ {
		f, err := OpenFile(p)
		if err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		for i := 0; i < 4; i++ {
			wg.Go(func() {
				b := make([]byte, 512)
				for k := 0; k < 200; k++ {
					if _, err := f.ReadAt(b, int64(k*256)); err != nil && !errors.Is(err, ErrClosed) && !errors.Is(err, os.ErrClosed) {
						t.Errorf("read: %v", err)
						return
					}
				}
			})
		}
		f.Close()
		wg.Wait()
		if _, err := f.ReadAt(make([]byte, 1), 0); !errors.Is(err, ErrClosed) {
			t.Errorf("after Close: %v", err)
		}
		if err := f.Close(); err != nil {
			t.Errorf("second Close: %v", err)
		}
	}
}
