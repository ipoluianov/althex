//go:build !windows

package source

import (
	"os"
	"syscall"
)

// openShared opens the file for reading; on Unix a reader never stops
// others from writing, renaming or deleting the file
func openShared(path string) (*os.File, error) {
	return os.Open(path)
}

// fileID returns the device and the inode of the file
func fileID(f *os.File, st os.FileInfo) (dev, ino uint64) {
	if sys, ok := st.Sys().(*syscall.Stat_t); ok {
		return uint64(sys.Dev), uint64(sys.Ino)
	}
	return 0, 0
}
