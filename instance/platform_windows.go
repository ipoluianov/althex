package instance

import (
	"os"

	"golang.org/x/sys/windows"
)

var procAllowSetForegroundWindow = windows.NewLazySystemDLL("user32.dll").NewProc("AllowSetForegroundWindow")

// tryLock takes the lock on the file without waiting; the system releases it when the process ends
func tryLock(f *os.File) bool {
	err := windows.LockFileEx(windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &windows.Overlapped{})
	return err == nil
}

// allowForeground lets any process bring its window to the foreground (ASFW_ANY)
func allowForeground() {
	const asfwAny = ^uintptr(0)
	procAllowSetForegroundWindow.Call(asfwAny)
}
