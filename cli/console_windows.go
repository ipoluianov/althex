package cli

import (
	"os"

	"golang.org/x/sys/windows"
)

// attachConsole sends the output to the console the program was started
// from: the exe is built as a window application, which has none of its own
func attachConsole() {
	const attachParentProcess = ^uint32(0) // ATTACH_PARENT_PROCESS
	proc := windows.NewLazySystemDLL("kernel32.dll").NewProc("AttachConsole")
	if r, _, _ := proc.Call(uintptr(attachParentProcess)); r == 0 {
		return
	}
	if f, err := os.OpenFile("CONOUT$", os.O_WRONLY, 0); err == nil {
		os.Stdout, os.Stderr = f, f
	}
}
