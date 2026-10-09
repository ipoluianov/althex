//go:build !windows

package instance

import (
	"os"
	"syscall"
)

// tryLock takes the lock on the file without waiting; the system releases it when the process ends
func tryLock(f *os.File) bool {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) == nil
}

func allowForeground() {}
