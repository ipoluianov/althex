package forms

import (
	"syscall"

	"github.com/ipoluianov/nui/ui"
	"golang.org/x/sys/windows"
)

var (
	user32                  = windows.NewLazySystemDLL("user32.dll")
	procIsIconic            = user32.NewProc("IsIconic")
	procShowWindow          = user32.NewProc("ShowWindow")
	procSetForegroundWindow = user32.NewProc("SetForegroundWindow")
)

// raiseWindow restores a minimized window and brings it to the foreground.
// Form.Restore would also turn a maximized window to normal, so it is restored only when minimized.
func raiseWindow(form *ui.Form) {
	hwnd, ok := form.SystemHandle().(syscall.Handle)
	if !ok {
		form.Show()
		return
	}
	const swRestore = 9
	if minimized, _, _ := procIsIconic.Call(uintptr(hwnd)); minimized != 0 {
		procShowWindow.Call(uintptr(hwnd), swRestore)
	}
	procSetForegroundWindow.Call(uintptr(hwnd))
}
