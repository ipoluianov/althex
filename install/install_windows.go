package install

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	_ "image/png"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"github.com/ipoluianov/althex/app"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// uninstallKey is the entry in "Installed apps"; one per application, so
// installing over an older version updates it instead of adding another
const uninstallKey = `Software\Microsoft\Windows\CurrentVersion\Uninstall\` + appName

var (
	ole32                = windows.NewLazySystemDLL("ole32.dll")
	procCoInitializeEx   = ole32.NewProc("CoInitializeEx")
	procCoUninitialize   = ole32.NewProc("CoUninitialize")
	procCoCreateInstance = ole32.NewProc("CoCreateInstance")
	procMessageBoxW      = windows.NewLazySystemDLL("user32.dll").NewProc("MessageBoxW")
)

// ExePath is the installed binary
func ExePath() string {
	return filepath.Join(Dir(), appName+".exe")
}

// iconPath is the icon of the shortcuts, next to the binary like on Linux
func iconPath() string {
	return filepath.Join(Dir(), "."+appName+"-icon.ico")
}

// CurrentStatus compares this copy with the installed one
func CurrentStatus() Status {
	if IsInstalledCopy() {
		return StatusUninstall
	}
	if _, err := os.Stat(ExePath()); err != nil {
		return StatusInstall
	}
	if versionNewer(app.Version, installedVersion()) {
		return StatusUpdate
	}
	return StatusUninstall
}

// installedVersion is the version of the installed copy from its entry in
// "Installed apps", or "" if it cannot be read
func installedVersion() string {
	k, err := registry.OpenKey(registry.CURRENT_USER, uninstallKey, registry.QUERY_VALUE)
	if err != nil {
		return ""
	}
	defer k.Close()
	v, _, err := k.GetStringValue("DisplayVersion")
	if err != nil {
		return ""
	}
	return v
}

// IsInstalledCopy tells whether the running binary is the installed one
func IsInstalledCopy() bool {
	exe, err := runningExe()
	if err != nil {
		return false
	}
	return strings.EqualFold(filepath.Clean(exe), filepath.Clean(ExePath()))
}

func runningExe() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return exe, nil
}

// Install copies the running binary to ~/.altbins, adds the shortcuts to the
// Start menu and the desktop and the entry to "Installed apps". Run over an
// installed copy, it replaces the binary and updates the rest.
func Install() error {
	src, err := runningExe()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(Dir(), 0755); err != nil {
		return err
	}
	if err := replaceFile(src, ExePath()); err != nil {
		return err
	}
	icon := ExePath()
	if err := writeIcon(iconPath()); err == nil {
		icon = iconPath()
	}
	if err := createShortcuts(icon); err != nil {
		return err
	}
	return register(icon)
}

// replaceFile copies src over dst. A running binary cannot be overwritten but
// can be renamed, so the old one is moved aside first. (The installed copy is
// not running while this one is: they share the instance lock.)
func replaceFile(src string, dst string) error {
	tmp := dst + ".new"
	if err := copyFile(src, tmp); err != nil {
		os.Remove(tmp)
		return err
	}
	old := dst + ".old"
	os.Remove(old)
	if err := os.Rename(dst, old); err != nil && !os.IsNotExist(err) {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		os.Rename(old, dst)
		return err
	}
	os.Remove(old) // fails if it is still running; then it goes on the next update
	return nil
}

func copyFile(src string, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// writeIcon saves the PNG icon as an .ico, which may hold a PNG as it is
func writeIcon(path string) error {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(iconPNG))
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	// ICONDIR: reserved, type 1 (icon), one image
	binary.Write(&buf, binary.LittleEndian, [3]uint16{0, 1, 1})
	// ICONDIRENTRY: width and height (0 means 256), no palette, 1 plane, 32 bpp, size, offset
	buf.Write([]byte{byte(cfg.Width), byte(cfg.Height), 0, 0})
	binary.Write(&buf, binary.LittleEndian, [2]uint16{1, 32})
	binary.Write(&buf, binary.LittleEndian, [2]uint32{uint32(len(iconPNG)), 6 + 16})
	buf.Write(iconPNG)
	return os.WriteFile(path, buf.Bytes(), 0644)
}

// shortcutPaths are the Start menu and desktop shortcuts
func shortcutPaths() []string {
	var paths []string
	for _, folder := range []*windows.KNOWNFOLDERID{windows.FOLDERID_Programs, windows.FOLDERID_Desktop} {
		if dir, err := windows.KnownFolderPath(folder, 0); err == nil {
			paths = append(paths, filepath.Join(dir, app.DisplayName+".lnk"))
		}
	}
	return paths
}

func createShortcuts(icon string) error {
	for _, lnk := range shortcutPaths() {
		if err := createShortcut(lnk, ExePath(), icon); err != nil {
			return fmt.Errorf("%s: %w", lnk, err)
		}
	}
	return nil
}

// register adds or updates the entry in "Installed apps", which runs the
// installed binary with UninstallArg to remove it
func register(icon string) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, uninstallKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	exe := ExePath()
	var sizeKB uint32
	if fi, err := os.Stat(exe); err == nil {
		sizeKB = uint32(fi.Size() / 1024)
	}
	values := []struct{ name, value string }{
		{"DisplayName", app.DisplayName},
		{"DisplayVersion", strings.TrimPrefix(app.Version, "v")},
		{"Publisher", app.Author},
		{"DisplayIcon", icon},
		{"InstallLocation", Dir()},
		{"InstallDate", time.Now().Format("20060102")},
		{"URLInfoAbout", app.Website},
		{"HelpLink", app.DocsURL},
		{"UninstallString", `"` + exe + `" ` + UninstallArg},
		{"QuietUninstallString", `"` + exe + `" ` + UninstallArg + " " + QuietArg},
	}
	for _, v := range values {
		if err := k.SetStringValue(v.name, v.value); err != nil {
			return err
		}
	}
	for name, value := range map[string]uint32{"NoModify": 1, "NoRepair": 1, "EstimatedSize": sizeKB} {
		if err := k.SetDWordValue(name, value); err != nil {
			return err
		}
	}
	return nil
}

// Uninstall removes the entry in "Installed apps", the shortcuts and the
// binary; the settings in ~/.altbins/.althex stay. The
// running binary cannot delete itself, so it is deleted once it has quit.
func Uninstall() error {
	err := registry.DeleteKey(registry.CURRENT_USER, uninstallKey)
	if err != nil && err != registry.ErrNotExist {
		return err
	}
	for _, lnk := range shortcutPaths() {
		os.Remove(lnk)
	}
	os.Remove(iconPath())
	os.Remove(ExePath() + ".old")
	if IsInstalledCopy() {
		return deleteAfterExit(ExePath())
	}
	if err := os.Remove(ExePath()); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// deleteAfterExit has cmd try to delete the file every second, for up to a
// minute, until this process has quit (the window may take a while to close)
func deleteAfterExit(path string) error {
	cmd := exec.Command("cmd.exe")
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CmdLine: `cmd.exe /d /c for /l %i in (1,1,60) do (ping -n 2 127.0.0.1 >nul & del /f /q "` + path +
			`" 2>nul & if not exist "` + path + `" exit /b)`,
		HideWindow:    true,
		CreationFlags: windows.CREATE_NO_WINDOW,
	}
	return cmd.Start()
}

// StartInstalled starts the installed copy, telling it that it has just been installed
func StartInstalled() error {
	cmd := exec.Command(ExePath(), InstalledArg)
	cmd.Dir = Dir()
	return cmd.Start()
}

const (
	mbOK              = 0x0
	mbYesNo           = 0x4
	mbIconQuestion    = 0x20
	mbIconInformation = 0x40
	mbSetForeground   = 0x10000
	idYes             = 6
)

// Confirm asks a yes/no question; the uninstaller has no window of its own
func Confirm(title string, text string) bool {
	return messageBox(title, text, mbYesNo|mbIconQuestion) == idYes
}

// Inform shows a message; the uninstaller has no window of its own
func Inform(title string, text string) {
	messageBox(title, text, mbOK|mbIconInformation)
}

func messageBox(title string, text string, flags uintptr) uintptr {
	t, _ := windows.UTF16PtrFromString(title)
	m, _ := windows.UTF16PtrFromString(text)
	r, _, _ := procMessageBoxW.Call(0, uintptr(unsafe.Pointer(m)), uintptr(unsafe.Pointer(t)), flags|mbSetForeground)
	return r
}

// The shell link COM objects (shobjidl.h): IShellLinkW to fill in the
// shortcut and IPersistFile to save it
var (
	clsidShellLink   = windows.GUID{Data1: 0x00021401, Data4: [8]byte{0xc0, 0, 0, 0, 0, 0, 0, 0x46}}
	iidIShellLinkW   = windows.GUID{Data1: 0x000214f9, Data4: [8]byte{0xc0, 0, 0, 0, 0, 0, 0, 0x46}}
	iidIPersistFile  = windows.GUID{Data1: 0x0000010b, Data4: [8]byte{0xc0, 0, 0, 0, 0, 0, 0, 0x46}}
	errComNotCreated = fmt.Errorf("cannot create the shortcut object")
)

// Method numbers in the interfaces' vtables
const (
	vtQueryInterface      = 0
	vtRelease             = 2
	vtSetDescription      = 7
	vtSetWorkingDirectory = 9
	vtSetIconLocation     = 17
	vtSetPath             = 20
	vtPersistFileSave     = 6
)

type comObject struct {
	vtbl *[32]uintptr
}

func (o *comObject) call(method int, args ...uintptr) uintptr {
	r, _, _ := syscall.SyscallN(o.vtbl[method], append([]uintptr{uintptr(unsafe.Pointer(o))}, args...)...)
	return r
}

func (o *comObject) callStr(method int, s string) error {
	p, err := windows.UTF16PtrFromString(s)
	if err != nil {
		return err
	}
	if hr := o.call(method, uintptr(unsafe.Pointer(p))); hr != 0 {
		return syscall.Errno(hr)
	}
	return nil
}

// createShortcut saves a .lnk to target. COM wants all the calls on one thread.
func createShortcut(lnk string, target string, icon string) error {
	result := make(chan error)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		result <- createShortcutCOM(lnk, target, icon)
	}()
	return <-result
}

func createShortcutCOM(lnk string, target string, icon string) error {
	const (
		coinitApartmentThreaded = 0x2
		clsctxInprocServer      = 0x1
		rpcEChangedMode         = 0x80010106
	)
	hr, _, _ := procCoInitializeEx.Call(0, coinitApartmentThreaded)
	switch uint32(hr) {
	case 0, 1: // S_OK, S_FALSE: initialized here, to be balanced
		defer procCoUninitialize.Call()
	case rpcEChangedMode: // already initialized on this thread in another mode, which works too
	default:
		return syscall.Errno(hr)
	}

	var link *comObject
	hr, _, _ = procCoCreateInstance.Call(uintptr(unsafe.Pointer(&clsidShellLink)), 0, clsctxInprocServer,
		uintptr(unsafe.Pointer(&iidIShellLinkW)), uintptr(unsafe.Pointer(&link)))
	if hr != 0 || link == nil {
		return errComNotCreated
	}
	defer link.call(vtRelease)

	for _, set := range []struct {
		method int
		value  string
	}{
		{vtSetPath, target},
		{vtSetWorkingDirectory, filepath.Dir(target)},
		{vtSetDescription, app.DisplayName},
	} {
		if err := link.callStr(set.method, set.value); err != nil {
			return err
		}
	}
	// SetIconLocation also takes the index of the icon in the file
	iconPtr, err := windows.UTF16PtrFromString(icon)
	if err != nil {
		return err
	}
	if hr := link.call(vtSetIconLocation, uintptr(unsafe.Pointer(iconPtr)), 0); hr != 0 {
		return syscall.Errno(hr)
	}

	var file *comObject
	if hr := link.call(vtQueryInterface, uintptr(unsafe.Pointer(&iidIPersistFile)), uintptr(unsafe.Pointer(&file))); hr != 0 || file == nil {
		return errComNotCreated
	}
	defer file.call(vtRelease)
	lnkPtr, err := windows.UTF16PtrFromString(lnk)
	if err != nil {
		return err
	}
	if hr := file.call(vtPersistFileSave, uintptr(unsafe.Pointer(lnkPtr)), 1); hr != 0 {
		return syscall.Errno(hr)
	}
	return nil
}
