//go:build !windows

package doc

import "os"

// syncDir makes a rename in the directory durable
func syncDir(dir string) {
	if f, err := os.Open(dir); err == nil {
		f.Sync()
		f.Close()
	}
}
