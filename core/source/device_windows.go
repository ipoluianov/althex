package source

import (
	"fmt"
	"os"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// isDevicePath tells a disk (\\.\PhysicalDrive0) or a volume (\\.\C:)
func isDevicePath(path string) bool { return strings.HasPrefix(path, `\\.\`) }

const ioctlDiskGetLengthInfo = 0x7405C // IOCTL_DISK_GET_LENGTH_INFO

// deviceSize asks the disk for its length
func deviceSize(f *os.File) (int64, error) {
	var length int64
	var ret uint32
	err := windows.DeviceIoControl(windows.Handle(f.Fd()), ioctlDiskGetLengthInfo, nil, 0,
		(*byte)(unsafe.Pointer(&length)), 8, &ret, nil)
	return length, err
}

// ListDisks tries the physical drives 0..31 and the volumes of the drive
// letters; the ones that need administrator rights to open are left out
func ListDisks() ([]DiskInfo, error) {
	var disks []DiskInfo
	for i := 0; i < 32; i++ {
		path := fmt.Sprintf(`\\.\PhysicalDrive%d`, i)
		if size := sizeOf(path); size > 0 {
			disks = append(disks, DiskInfo{Path: path, Name: fmt.Sprintf("PhysicalDrive%d", i), Size: size})
		}
	}
	mask, _ := windows.GetLogicalDrives()
	for i := 0; i < 26; i++ {
		if mask&(1<<uint(i)) == 0 {
			continue
		}
		letter := string(rune('A'+i)) + ":"
		path := `\\.\` + letter
		if size := sizeOf(path); size > 0 {
			disks = append(disks, DiskInfo{Path: path, Name: letter, Size: size})
		}
	}
	return disks, nil
}

func sizeOf(path string) int64 {
	f, err := openShared(path)
	if err != nil {
		return 0
	}
	defer f.Close()
	n, _ := deviceSize(f)
	return n
}
