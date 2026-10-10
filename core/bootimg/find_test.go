package bootimg

import (
	"bytes"
	"encoding/binary"
	"testing"

	bt "github.com/ipoluianov/althex/core/bootimg/bootimgtest"
)

// An MBR disk: an Android boot image in the first partition, a FAT
// file system (its boot sector is not a boot image to show) in the second
func TestFindInPartitions(t *testing.T) {
	disk := make([]byte, 4<<20)
	entry := func(i int, typ byte, lba, n uint32) {
		e := disk[446+16*i:]
		e[4] = typ
		binary.LittleEndian.PutUint32(e[8:], lba)
		binary.LittleEndian.PutUint32(e[12:], n)
	}
	entry(0, 0x83, 2048, 2048)
	entry(1, 0x01, 4096, 2048)
	disk[510], disk[511] = 0x55, 0xAA
	boot := bt.AndroidBoot(bt.Android{Version: 2, PageSize: 2048, Kernel: gz(t, []byte("kernel")), Ramdisk: lz4Legacy(bt.Cpio(cpioSample()), lz4LegacyBlock)})
	copy(disk[2048*512:], boot)
	copy(disk[4096*512:], bt.FAT12(2048))

	found := FindInPartitions(bytes.NewReader(disk), int64(len(disk)))
	if len(found) != 1 {
		t.Fatalf("%d found: %+v", len(found), found)
	}
	img := found[0].Image
	if found[0].Partition.Index != 1 || img.Info[0].Key != "in" {
		t.Fatalf("%+v", found[0])
	}
	inner := detect(t, boot)
	for i, p := range img.Parts {
		if p.Off != inner.Parts[i].Off+2048*512 || p.Size != inner.Parts[i].Size {
			t.Errorf("%s at 0x%X, want 0x%X", p.Name, p.Off, inner.Parts[i].Off+2048*512)
		}
		if p.Name == "ramdisk" {
			cp, _, err := DetectPart(bytes.NewReader(disk), p, 0)
			if err != nil || len(cp.Parts) != len(cpioSample()) {
				t.Errorf("ramdisk: %v %+v", err, cp)
			}
		}
	}
	if FindInPartitions(bytes.NewReader(boot), int64(len(boot))) != nil {
		t.Error("found in a disk with no table")
	}
}

func TestZImageAppendedDTB(t *testing.T) {
	z := make([]byte, 0x1000)
	binary.LittleEndian.PutUint32(z[0x24:], 0x016F2818)
	binary.LittleEndian.PutUint32(z[0x2C:], 0x1000)
	dtb := bt.FDT(&bt.Node{Name: ""}, nil)
	img := detect(t, append(z, dtb...))
	part(t, img, "kernel", 0, 0x1000)
	part(t, img, "dtb", 0x1000, int64(len(dtb)))
	if img := detect(t, z); len(img.Parts) != 1 {
		t.Errorf("a dtb without one: %+v", img.Parts)
	}
}
