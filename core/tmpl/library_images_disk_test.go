package tmpl

import (
	"strings"
	"testing"

	"github.com/ipoluianov/althex/core/image/diskimgtest"
)

// The disk of the tests of the disk image templates
var diskRaw = diskimgtest.Raw(1<<20 + 12<<10)

func TestVHDTemplate(t *testing.T) {
	fixed := diskimgtest.VHDFixed(diskRaw[:64<<10])
	res := libraryRun(t, fixed, "x.vhd", "vhd.bt")
	if v := value(t, res, "footer.diskType"); v != "VHD_FIXED (2)" {
		t.Fatalf("diskType %s", v)
	}
	if v := value(t, res, "footer.currentSize"); v != "65536" {
		t.Fatalf("currentSize %s", v)
	}
	dyn := diskimgtest.VHDDynamic(diskRaw, 512<<10)
	res = libraryRun(t, dyn, "x.vhd", "vhd.bt")
	for path, want := range map[string]string{
		"footerCopy.diskType":           "VHD_DYNAMIC (3)",
		"dynamicHeader.blockSize":       "524288",
		"dynamicHeader.maxTableEntries": "3",
		"footer.diskType":               "VHD_DYNAMIC (3)",
	} {
		if v := value(t, res, path); v != want {
			t.Errorf("%s = %s, want %s", path, v, want)
		}
	}
	if n := res.Root.Find("block[1]"); n == nil || n.Size != 512+512<<10 {
		t.Fatal("block[1]")
	}
	checkTruncated(t, "vhd.bt", dyn)

	if n := libraryRun(t, fixed, "x.vhd", "vhd.bt").Root.Find("diskData"); n == nil || n.Size != 64<<10 {
		t.Fatal("diskData")
	}
	// A differencing disk names its parent in UTF-16BE
	for i, c := range "parent.vhd" {
		dyn[512+64+i*2+1] = byte(c)
	}
	res = libraryRun(t, dyn, "x.vhd", "vhd.bt")
	if v := value(t, res, "dynamicHeader.parentUnicodeName"); v != `"parent.vhd"` {
		t.Errorf("parent %s", v)
	}
}

func TestVHDXTemplate(t *testing.T) {
	img := diskimgtest.VHDX(diskRaw, 1<<20, diskimgtest.VHDXOptions{})
	res := libraryRun(t, img, "x.vhdx", "vhdx.bt")
	for path, want := range map[string]string{
		"header2.sequenceNumber":           "2",
		"header1.version":                  "1",
		"regionTable1.entryCount":          "2",
		"regionTable1.entry[0].fileOffset": "0x0000000000300000",
		"metadata.entryCount":              "5",
		"fileParameters.blockSize":         "1048576",
		"virtualDiskSize":                  "1060864",
		"logicalSectorSize":                "512",
		"bat[0].state":                     "PAYLOAD_BLOCK_FULLY_PRESENT (6)",
		"bat[0].fileOffsetMB":              "4",
		"bat[1].state":                     "PAYLOAD_BLOCK_NOT_PRESENT (0)",
	} {
		if v := value(t, res, path); v != want {
			t.Errorf("%s = %s, want %s", path, v, want)
		}
	}
	if n := res.Root.Find("bat"); n == nil || n.ChildCount() != 2 {
		t.Fatal("bat")
	}
	checkTruncated(t, "vhdx.bt", diskimgtest.VHDX(nil, 1<<20, diskimgtest.VHDXOptions{Compact: true}))
}

func TestVMDKTemplate(t *testing.T) {
	img := diskimgtest.VMDKSparse(diskRaw, 16, 64)
	res := libraryRun(t, img, "x.vmdk", "vmdk.bt")
	for path, want := range map[string]string{
		"header.capacity":     "2072",
		"header.grainSize":    "16",
		"header.numGTEsPerGT": "64",
		"header.gdOffset":     "21",
	} {
		if v := value(t, res, path); v != want {
			t.Errorf("%s = %s, want %s", path, v, want)
		}
	}
	if v := value(t, res, "descriptor"); !strings.Contains(v, "monolithicSparse") {
		t.Errorf("descriptor %s", v)
	}
	if n := res.Root.Find("grainDirectory"); n == nil || n.Size != 3*4 {
		t.Fatal("grainDirectory")
	}
	if n := res.Root.Find("grainTable[1].entry"); n == nil || n.Size != 64*4 {
		t.Fatal("grainTable")
	}
	checkTruncated(t, "vmdk.bt", img)

	stream := diskimgtest.VMDKStream(diskRaw)
	res = libraryRun(t, stream, "x.vmdk", "vmdk.bt")
	if v := value(t, res, "header.compressedGrains"); v != "1" {
		t.Errorf("compressedGrains %s", v)
	}
	if v := value(t, res, "footer.gdOffset"); v == "0" || v == "18446744073709551615" {
		t.Errorf("footer.gdOffset %s", v)
	}
	// 16 grains of data, a grain table, the directory, the footer, the end
	m := res.Root.Find("marker")
	if m == nil || m.ChildCount() != 16+4 {
		t.Fatalf("markers %v", m)
	}
	if v := value(t, res, "marker[19].type"); v != "MARKER_EOS (0)" {
		t.Errorf("last marker %s", v)
	}
	checkTruncated(t, "vmdk.bt", stream)

	desc := []byte(diskimgtest.VMDKDescriptor("monolithicFlat", `RW 2048 FLAT "x-flat.vmdk" 0`))
	res = libraryRun(t, desc, "x.vmdk", "vmdk.bt")
	if v := value(t, res, "descriptor"); !strings.Contains(v, "x-flat.vmdk") {
		t.Errorf("descriptor %s", v)
	}
}

func TestQCOW2Template(t *testing.T) {
	img := diskimgtest.QCOW2(diskRaw, diskimgtest.QCOW2Options{Version: 3, ClusterBits: 9, Compress: true, Backing: "base.img"})
	res := libraryRun(t, img, "x.qcow2", "qcow2.bt")
	for path, want := range map[string]string{
		"header.version":              "3",
		"header.clusterBits":          "9",
		"header.size":                 "1060864",
		"header.l1Size":               "33",
		"header.headerLength":         "112",
		"header.refcountOrder":        "4",
		"header.dirty":                "0",
		"header.compressionTypeValue": "COMPRESSION_DEFLATE (0)",
		"extension[0].type":           "EXT_END (0)",
		"backingFile":                 `"base.img"`,
	} {
		if v := value(t, res, path); v != want {
			t.Errorf("%s = %s, want %s", path, v, want)
		}
	}
	if n := res.Root.Find("l2Table[1].entry"); n == nil || n.Size != 512 {
		t.Fatal("l2Table")
	}
	checkTruncated(t, "qcow2.bt", img)

	// The dirty bit is the lowest one
	img[79] = 1
	res = libraryRun(t, img, "x.qcow2", "qcow2.bt")
	if v := value(t, res, "header.dirty"); v != "1" {
		t.Errorf("dirty %s", v)
	}
	v2 := diskimgtest.QCOW2(diskRaw, diskimgtest.QCOW2Options{Version: 2, ClusterBits: 16})
	res = libraryRun(t, v2, "x.qcow2", "qcow2.bt")
	if v := value(t, res, "header.l1Size"); v != "1" {
		t.Errorf("l1Size %s", v)
	}
	checkTruncated(t, "qcow2.bt", v2)
}

func TestVDITemplate(t *testing.T) {
	img := diskimgtest.VDI(diskRaw, 64<<10)
	res := libraryRun(t, img, "x.vdi", "vdi.bt")
	for path, want := range map[string]string{
		"header.versionMajor":            "1",
		"header.imageType":               "VDI_NORMAL (1)",
		"header.diskSize":                "1060864",
		"header.blockSize":               "65536",
		"header.blocksInImage":           "17",
		"header.blocksAllocated":         "16",
		"header.offsetBlocks":            "0x00000200",
		"header.lchsGeometry.sectorSize": "0",
	} {
		if v := value(t, res, path); v != want {
			t.Errorf("%s = %s, want %s", path, v, want)
		}
	}
	if n := res.Root.Find("blockMap"); n == nil || n.Size != 17*4 {
		t.Fatal("blockMap")
	}
	checkTruncated(t, "vdi.bt", img)
}

func TestAndroidSparseTemplate(t *testing.T) {
	raw := diskimgtest.Raw(3<<20 + 64<<10)[3<<20-16384:]
	img := diskimgtest.AndroidSparse(raw, 4096)
	res := libraryRun(t, img, "x.simg", "android-sparse.bt")
	for path, want := range map[string]string{
		"header.magic":       "0xED26FF3A",
		"header.blockSize":   "4096",
		"header.totalBlocks": "20",
		"header.totalChunks": "3",
		"chunk[0].chunkType": "CHUNK_TYPE_RAW (51905)",
		"chunk[0].chunkSize": "4",
		"chunk[1].chunkType": "CHUNK_TYPE_FILL (51906)",
		"chunk[1].fillValue": "0xEFBEADDE",
		"chunk[2].chunkType": "CHUNK_TYPE_CRC32 (51908)",
	} {
		if v := value(t, res, path); v != want {
			t.Errorf("%s = %s, want %s", path, v, want)
		}
	}
	if n := res.Root.Find("chunk[0].data"); n == nil || n.Size != 4*4096 {
		t.Fatal("chunk data")
	}
	checkTruncated(t, "android-sparse.bt", img)
	checkTruncated(t, "android-sparse.bt", diskimgtest.AndroidSparse(diskRaw, 4096))
}
