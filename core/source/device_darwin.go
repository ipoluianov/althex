package source

import (
	"os"
	"path/filepath"
	"regexp"
	"unsafe"

	"golang.org/x/sys/unix"
)

func isDevicePath(path string) bool { return false }

const (
	dkiocGetBlockSize  = 0x40046418 // DKIOCGETBLOCKSIZE
	dkiocGetBlockCount = 0x40086419 // DKIOCGETBLOCKCOUNT
)

// deviceSize asks the disk for its block size and count
func deviceSize(f *os.File) (int64, error) {
	var bs uint32
	var count uint64
	if _, _, e := unix.Syscall(unix.SYS_IOCTL, f.Fd(), dkiocGetBlockSize, uintptr(unsafe.Pointer(&bs))); e != 0 {
		return 0, e
	}
	if _, _, e := unix.Syscall(unix.SYS_IOCTL, f.Fd(), dkiocGetBlockCount, uintptr(unsafe.Pointer(&count))); e != 0 {
		return 0, e
	}
	return int64(bs) * int64(count), nil
}

var diskName = regexp.MustCompile(`^disk[0-9]+$`)
var partName = regexp.MustCompile(`^disk[0-9]+s[0-9]+$`)

// ListDisks lists /dev/disk*: the raw devices (rdisk) are faster to read.
// The sizes need the rights to open them; without, they are 0.
func ListDisks() ([]DiskInfo, error) {
	all, err := filepath.Glob("/dev/disk*")
	if err != nil {
		return nil, err
	}
	byName := map[string]*DiskInfo{}
	var order []string
	for _, p := range all {
		name := filepath.Base(p)
		if diskName.MatchString(name) {
			d := &DiskInfo{Path: "/dev/r" + name, Name: name, Size: sizeOf("/dev/r" + name)}
			byName[name] = d
			order = append(order, name)
		}
	}
	for _, p := range all {
		name := filepath.Base(p)
		if partName.MatchString(name) {
			disk := regexp.MustCompile(`s[0-9]+$`).ReplaceAllString(name, "")
			if d := byName[disk]; d != nil {
				d.Partitions = append(d.Partitions, DiskInfo{Path: "/dev/r" + name, Name: name, Size: sizeOf("/dev/r" + name)})
			}
		}
	}
	var out []DiskInfo
	for _, n := range order {
		out = append(out, *byName[n])
	}
	return out, nil
}

func sizeOf(path string) int64 {
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()
	n, _ := deviceSize(f)
	return n
}
