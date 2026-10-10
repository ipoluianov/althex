package bootimg

import (
	"fmt"
	"io"
	"strings"

	ptable "github.com/ipoluianov/althex/core/part"
)

// Found is a boot image inside a partition of a disk
type Found struct {
	Partition ptable.Partition
	Image     *Image // the offsets of its parts are from the start of the disk
}

// maxPartitionsScanned keeps a table full of garbage from taking long
const maxPartitionsScanned = 256

// FindInPartitions recognizes the boot images in the partitions of a disk
// (an Android boot_a partition, a FIT in a partition of an SD card...);
// the offsets of the parts found are those in the disk. Extended MBR
// partitions and boot sectors (any FAT or NTFS partition has one) are
// skipped.
func FindInPartitions(r io.ReaderAt, size int64) []Found {
	t, err := ptable.Read(r, size, 0)
	if err != nil {
		return nil
	}
	var found []Found
	for i, p := range t.Partitions {
		if i >= maxPartitionsScanned {
			break
		}
		if p.Extended || p.Size <= 0 || p.Start < 0 || p.Start >= size {
			continue
		}
		n := min(p.Size, size-p.Start)
		img, err := Detect(io.NewSectionReader(r, p.Start, n), n)
		if err != nil || strings.HasSuffix(img.Format, " boot sector") || img.Format == "GRUB boot.img" {
			continue
		}
		for j := range img.Parts {
			img.Parts[j].Off += p.Start
		}
		where := fmt.Sprintf("partition %d", p.Index)
		if p.Name != "" {
			where += " (" + p.Name + ")"
		}
		img.Info = append([]KV{{"in", fmt.Sprintf("%s at 0x%X", where, p.Start)}}, img.Info...)
		found = append(found, Found{Partition: p, Image: img})
	}
	return found
}
