package source

// DiskInfo is a disk or a partition of the computer
type DiskInfo struct {
	Path       string // to open it: /dev/sda, \\.\PhysicalDrive0, /dev/rdisk2
	Name       string
	Model      string
	Size       int64
	Removable  bool
	Partitions []DiskInfo
}
