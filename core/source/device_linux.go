package source

import (
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func isDevicePath(path string) bool { return false }

// deviceSize: a block device can be sought to its end
func deviceSize(f *os.File) (int64, error) {
	return f.Seek(0, io.SeekEnd)
}

// ListDisks lists the disks and their partitions from /sys/block
func ListDisks() ([]DiskInfo, error) {
	dirs, err := filepath.Glob("/sys/block/*")
	if err != nil {
		return nil, err
	}
	var disks []DiskInfo
	for _, dir := range dirs {
		name := filepath.Base(dir)
		if strings.HasPrefix(name, "ram") || strings.HasPrefix(name, "zram") {
			continue
		}
		size := sysSize(dir)
		if size == 0 {
			continue // an empty loop device, a card reader without a card
		}
		d := DiskInfo{
			Path:      "/dev/" + name,
			Name:      name,
			Model:     strings.TrimSpace(readSys(filepath.Join(dir, "device", "model"))),
			Size:      size,
			Removable: readSys(filepath.Join(dir, "removable")) == "1\n",
		}
		parts, _ := filepath.Glob(filepath.Join(dir, name+"*"))
		for _, p := range parts {
			if _, err := os.Stat(filepath.Join(p, "partition")); err != nil {
				continue
			}
			pn := filepath.Base(p)
			d.Partitions = append(d.Partitions, DiskInfo{Path: "/dev/" + pn, Name: pn, Size: sysSize(p)})
		}
		disks = append(disks, d)
	}
	return disks, nil
}

func readSys(path string) string {
	b, _ := os.ReadFile(path)
	return string(b)
}

// sysSize is the size in /sys, counted in 512-byte sectors whatever the disk's are
func sysSize(dir string) int64 {
	n, _ := strconv.ParseInt(strings.TrimSpace(readSys(filepath.Join(dir, "size"))), 10, 64)
	return n * 512
}
