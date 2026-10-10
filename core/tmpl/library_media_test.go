package tmpl

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Tests of the media and archive templates: zip.bt, pdf.bt, wav.bt, mp4.bt

// mdTemplate returns a built-in template by its file name
func mdTemplate(t *testing.T, file string) *Program {
	t.Helper()
	for _, tm := range Library() {
		if tm.File == file {
			p, err := tm.Program()
			if err != nil {
				t.Fatal(err)
			}
			return p
		}
	}
	t.Fatalf("no template %s", file)
	return nil
}

// mdWant checks that the field has the value
func mdWant(t *testing.T, res *Result, path, want string) {
	t.Helper()
	if v := value(t, res, path); v != want {
		t.Errorf("%s = %s, want %s", path, v, want)
	}
}

// mdWantPrefix checks that the value of the field starts with want
func mdWantPrefix(t *testing.T, res *Result, path, want string) {
	t.Helper()
	if v := value(t, res, path); !strings.HasPrefix(v, want) {
		t.Errorf("%s = %s, want %s...", path, v, want)
	}
}

// mdTruncated runs the template over cuts of the data: it must finish
// without a panic, and preferably without an error
func mdTruncated(t *testing.T, file string, data []byte, step int) {
	t.Helper()
	p := mdTemplate(t, file)
	if step < 1 {
		step = 1
	}
	cuts := []int{0, 1, 2, 3, 4, 7, 8, 11, 12, 21, 22, 30}
	for n := 0; n < len(data); n += step {
		cuts = append(cuts, n)
	}
	for _, n := range cuts {
		if n > len(data) {
			continue
		}
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("%s cut at %d: panic %v", file, n, r)
				}
			}()
			d := append([]byte(nil), data[:n]...)
			res := p.Run(context.Background(), Env{Data: &mem{b: d}, MaxSteps: 5_000_000})
			if res.Err != nil {
				t.Errorf("%s cut at %d: %v", file, n, res.Err)
			}
		}()
	}
}

// mdFindFile returns a file of the system with the extension, smaller than
// max; "" - none
func mdFindFile(ext string, max int64) string {
	roots := []string{"/usr/share", "/usr/lib", "/opt"}
	if h, err := os.UserHomeDir(); err == nil {
		for _, d := range []string{"Videos", "Music", "Downloads", "Documents", ""} {
			roots = append(roots, filepath.Join(h, d))
		}
	}
	for _, root := range roots {
		found := ""
		visited := 0
		filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			visited++
			if found != "" || visited > 300_000 {
				return filepath.SkipAll
			}
			if err != nil {
				if d != nil && d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if d.IsDir() {
				if (strings.HasPrefix(d.Name(), ".") || d.Name() == "node_modules") && path != root {
					return filepath.SkipDir
				}
				if rel, err := filepath.Rel(root, path); err == nil && strings.Count(rel, string(filepath.Separator)) >= 5 {
					return filepath.SkipDir
				}
				return nil
			}
			if strings.EqualFold(filepath.Ext(path), "."+ext) && d.Type().IsRegular() {
				if fi, err := d.Info(); err == nil && fi.Size() > 64 && fi.Size() < max {
					found = path
					return filepath.SkipAll
				}
			}
			return nil
		})
		if found != "" {
			return found
		}
	}
	return ""
}

// mdRealFile runs the template on a file of the system, if there is one
func mdRealFile(t *testing.T, ext, file string) {
	path := mdFindFile(ext, 2<<20)
	if path == "" {
		t.Skipf("no .%s file found", ext)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skip(err)
	}
	p := mdTemplate(t, file)
	start := time.Now()
	res := p.Run(context.Background(), Env{Data: &mem{b: data}})
	if res.Err != nil {
		t.Errorf("%s: %v", path, res.Err)
	}
	if d := time.Since(start); d > 10*time.Second && !raceEnabled {
		t.Errorf("%s: %v", path, d)
	}
	t.Logf("%s: %d bytes, %d fields, %v", path, len(data), res.Nodes, res.Duration)
}

// ---- ZIP ----

func mdMakeZIP(t *testing.T, comment string) []byte {
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	// Stored, sizes in the header (no data descriptor)
	stored := []byte("hello, stored world\n")
	fh := &zip.FileHeader{Name: "stored.txt", Method: zip.Store,
		CRC32: crc32.ChecksumIEEE(stored), CompressedSize64: uint64(len(stored)), UncompressedSize64: uint64(len(stored))}
	fh.ModifiedTime = 13<<11 | 45<<5 | 30/2
	fh.ModifiedDate = (2024-1980)<<9 | 5<<5 | 17
	fw, err := w.CreateRaw(fh)
	if err != nil {
		t.Fatal(err)
	}
	fw.Write(stored)
	// Deflated and streamed: a data descriptor follows the data
	fw, err = w.Create("dir/deflated.txt")
	if err != nil {
		t.Fatal(err)
	}
	fw.Write(bytes.Repeat([]byte("compress me please "), 50))
	// Stored and streamed
	fw, err = w.CreateHeader(&zip.FileHeader{Name: "streamed.bin", Method: zip.Store})
	if err != nil {
		t.Fatal(err)
	}
	fw.Write([]byte{0, 1, 2, 'P', 'K', 7, 8, 3, 4, 5})
	if comment != "" {
		w.SetComment(comment)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestZIPTemplate(t *testing.T) {
	data := mdMakeZIP(t, "an archive comment")
	res := libraryRun(t, data, "a.zip", "zip.bt")
	mdWant(t, res, "localFile[0].fileName", `"stored.txt"`)
	mdWant(t, res, "localFile[0].compressionMethod", "STORED (0)")
	mdWant(t, res, "localFile[0].signature", "0x04034B50")
	mdWant(t, res, "localFile[0].lastModFileTime", "13:45:30")
	mdWant(t, res, "localFile[0].lastModFileDate", "2024-05-17")
	mdWant(t, res, "localFile[0]", "stored.txt (20 -> 20 bytes, stored)")
	if n := res.Root.Find("localFile[0].fileData"); n == nil || n.Size != 20 {
		t.Errorf("stored data %+v", n)
	}
	mdWant(t, res, "localFile[1].compressionMethod", "DEFLATE (8)")
	mdWant(t, res, "localFile[1].flags.dataDescriptor", "1")
	mdWant(t, res, "localFile[1].compressedSize", "0")
	mdWantPrefix(t, res, "localFile[1]", "dir/deflated.txt (950 -> ")
	mdWant(t, res, "localFile[1].dataDescriptor.signature", "0x08074B50")
	mdWant(t, res, "localFile[1].dataDescriptor.uncompressedSize", "950")
	cs := value(t, res, "localFile[1].dataDescriptor.compressedSize")
	if n := res.Root.Find("localFile[1].fileData"); n == nil || fmt.Sprint(n.Size) != cs {
		t.Errorf("deflated data %+v, descriptor %s", n, cs)
	}
	if n := res.Root.Find("localFile[2].fileData"); n == nil || n.Size != 10 {
		t.Errorf("streamed stored data %+v", n)
	}
	mdWant(t, res, "localFile[2]", "streamed.bin (10 -> 10 bytes, stored)")
	if n := res.Root.Find("centralDir"); n == nil || n.ChildCount() != 3 {
		t.Fatalf("central directory %+v", n)
	}
	mdWant(t, res, "centralDir[1].fileName", `"dir/deflated.txt"`)
	mdWantPrefix(t, res, "centralDir[1]", "dir/deflated.txt (950 -> ")
	mdWant(t, res, "endOfCentralDir.totalNumberOfCentralDirectoryRecords", "3")
	mdWant(t, res, "endOfCentralDir.comment", `"an archive comment"`)
	if res.Root.Find("unknownData") != nil {
		t.Error("unknown data in a good archive")
	}
	mdTruncated(t, "zip.bt", data, 7)

	// The same without the central directory: the streamed data is found
	// by the data descriptor
	cd := bytes.Index(data, []byte("PK\x01\x02"))
	res = libraryRun(t, data[:cd], "a.zip", "zip.bt")
	if n := res.Root.Find("localFile[2].fileData"); n == nil || n.Size != 10 {
		t.Errorf("streamed data without the central directory %+v", n)
	}
	mdWant(t, res, "localFile[1].dataDescriptor.uncompressedSize", "950")
}

func TestZIPSelfExtracting(t *testing.T) {
	zipData := mdMakeZIP(t, "")
	// A shell-script stub (an "MZ" one would be detected as an executable)
	stub := append([]byte("#!/bin/sh\nexit 0\n"), bytes.Repeat([]byte{'#'}, 983)...)
	data := append(stub, zipData...)
	res := libraryRun(t, data, "setup.exe", "zip.bt")
	if n := res.Root.Find("leadingData"); n == nil || n.Size != 1000 {
		t.Fatalf("leading data %+v", n)
	}
	n := res.Root.Find("localFile[0]")
	if n == nil || n.Off != 1000 {
		t.Fatalf("local header %+v", n)
	}
	mdWant(t, res, "localFile[0].fileName", `"stored.txt"`)
	mdWantPrefix(t, res, "localFile[1]", "dir/deflated.txt (950 -> ")
	if n := res.Root.Find("localFile[2].fileData"); n == nil || n.Size != 10 {
		t.Errorf("streamed data %+v", n)
	}
	mdWant(t, res, "centralDir[2].fileName", `"streamed.bin"`)
	mdWant(t, res, "endOfCentralDir.commentLength", "0")
	mdTruncated(t, "zip.bt", data, 13)
}

func TestZIPEmpty(t *testing.T) {
	var buf bytes.Buffer
	zip.NewWriter(&buf).Close()
	res := libraryRun(t, buf.Bytes(), "e.zip", "zip.bt")
	mdWant(t, res, "endOfCentralDir.totalNumberOfCentralDirectoryRecords", "0")
}

// mdMakeZIP64 builds by hand an archive with ZIP64 sizes and records
func mdMakeZIP64() []byte {
	le64 := func(v uint64) []byte { b := make([]byte, 8); binary.LittleEndian.PutUint64(b, v); return b }
	content := []byte("hello")
	crc := crc32.ChecksumIEEE(content)
	name := []byte("a.txt")
	local := mdCat(mdLE32(0x04034B50), mdLE16(45), mdLE16(0), mdLE16(0), mdLE16(0), mdLE16(0x21),
		mdLE32(crc), mdLE32(0xFFFFFFFF), mdLE32(0xFFFFFFFF), mdLE16(uint16(len(name))), mdLE16(20), name,
		mdLE16(1), mdLE16(16), le64(5), le64(5), content)
	cdOff := len(local)
	central := mdCat(mdLE32(0x02014B50), mdLE16(0x031E), mdLE16(45), mdLE16(0), mdLE16(0), mdLE16(0), mdLE16(0x21),
		mdLE32(crc), mdLE32(0xFFFFFFFF), mdLE32(0xFFFFFFFF), mdLE16(uint16(len(name))), mdLE16(28), mdLE16(0),
		mdLE16(0), mdLE16(0), mdLE32(0), mdLE32(0xFFFFFFFF), name,
		mdLE16(1), mdLE16(24), le64(5), le64(5), le64(0))
	z64Off := cdOff + len(central)
	z64 := mdCat(mdLE32(0x06064B50), le64(44), mdLE16(45), mdLE16(45), mdLE32(0), mdLE32(0),
		le64(1), le64(1), le64(uint64(len(central))), le64(uint64(cdOff)))
	loc := mdCat(mdLE32(0x07064B50), mdLE32(0), le64(uint64(z64Off)), mdLE32(1))
	eocd := mdCat(mdLE32(0x06054B50), mdLE16(0xFFFF), mdLE16(0xFFFF), mdLE16(0xFFFF), mdLE16(0xFFFF),
		mdLE32(0xFFFFFFFF), mdLE32(0xFFFFFFFF), mdLE16(0))
	return mdCat(local, central, z64, loc, eocd)
}

func TestZIP64(t *testing.T) {
	data := mdMakeZIP64()
	for _, lead := range []int{0, 300} {
		d := append(bytes.Repeat([]byte{'#'}, lead), data...)
		res := libraryRun(t, d, "a.zip", "zip.bt")
		mdWant(t, res, "localFile", "a.txt (5 -> 5 bytes, stored)")
		mdWant(t, res, "localFile.extraField.field.compressedSize", "5")
		if n := res.Root.Find("localFile.fileData"); n == nil || n.Size != 5 || n.Off != int64(lead+30+5+20) {
			t.Errorf("data %+v", n)
		}
		mdWant(t, res, "centralDir", "a.txt (5 -> 5 bytes, stored)")
		mdWant(t, res, "centralDir.extraField.field.relativeHeaderOffset", "0x0000000000000000")
		mdWant(t, res, "centralDir.versionMadeBy", "3.0 UNIX")
		mdWant(t, res, "zip64EndOfCentralDir.totalNumberOfCentralDirectoryRecords", "1")
		mdWant(t, res, "zip64Locator.offsetOfZip64EndOfCentralDirectory", fmt.Sprintf("0x%016X", 30+5+20+5+46+5+28))
		mdWant(t, res, "endOfCentralDir.totalNumberOfCentralDirectoryRecords", "65535")
		mdTruncated(t, "zip.bt", d, 3)
	}
}

// An archive of many files: the first MAX_RECORDS are shown one by one
func TestZIPManyFiles(t *testing.T) {
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for i := 0; i < 6100; i++ {
		fw, _ := w.CreateHeader(&zip.FileHeader{Name: fmt.Sprintf("f%d", i), Method: zip.Store})
		fw.Write([]byte{byte(i)})
	}
	w.Close()
	res := libraryRun(t, buf.Bytes(), "many.zip", "zip.bt")
	if n := res.Root.Find("localFile"); n == nil || n.ChildCount() != 6000 {
		t.Fatalf("local files %+v", n)
	}
	if n := res.Root.Find("centralDir"); n == nil || n.ChildCount() != 6000 {
		t.Fatalf("central entries %+v", n)
	}
	if res.Root.Find("moreLocalFiles") == nil || res.Root.Find("moreCentralEntries") == nil {
		t.Fatal("the rest of the records")
	}
	mdWant(t, res, "endOfCentralDir.totalNumberOfCentralDirectoryRecords", "6100")
	t.Logf("%d fields, %v", res.Nodes, res.Duration)
}

func TestZIPRealFile(t *testing.T) { mdRealFile(t, "zip", "zip.bt") }

// ---- PDF ----

// mdMakePDF writes a small PDF: a catalog, pages, a page, a content stream
// with a direct /Length, a stream with an indirect one, the xref table and
// the trailer
func mdMakePDF(bigStream int) []byte {
	var b bytes.Buffer
	var offs []int
	b.WriteString("%PDF-1.4\n%\xE2\xE3\xCF\xD3\n")
	obj := func(s string) {
		offs = append(offs, b.Len())
		b.WriteString(s)
	}
	obj("1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n")
	obj("2 0 obj\n<< /Type /Pages /Kids [3 0 R] /Count 1 >>\nendobj\n")
	obj("3 0 obj\n<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 4 0 R >>\nendobj\n")
	content := "BT /F1 24 Tf 72 712 Td (Hello endobj world) Tj ET"
	obj(fmt.Sprintf("4 0 obj\n<< /Length %d >>\nstream\r\n%s\r\nendstream\nendobj\n", len(content), content))
	data := []byte("\x00\x01binary endstream-less data\xFF\x00")
	if bigStream > 0 {
		data = bytes.Repeat([]byte("A"), bigStream)
	}
	obj("5 0 obj\n<< /Length 6 0 R /Filter /FlateDecode >>\nstream\n" + string(data) + "\nendstream\nendobj\n")
	obj(fmt.Sprintf("6 0 obj\n%d\nendobj\n", len(data)))
	xref := b.Len()
	fmt.Fprintf(&b, "xref\n0 %d\n0000000000 65535 f\r\n", len(offs)+1)
	for _, o := range offs {
		fmt.Fprintf(&b, "%010d 00000 n\r\n", o)
	}
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R /Info << /Title (a >> b) >> >>\nstartxref\n%d\n%%%%EOF\n", len(offs)+1, xref)
	return b.Bytes()
}

func TestPDFTemplate(t *testing.T) {
	data := mdMakePDF(0)
	res := libraryRun(t, data, "a.pdf", "pdf.bt")
	mdWant(t, res, "header", `"%PDF-1.4"`)
	if n := res.Root.Find("obj"); n == nil || n.ChildCount() != 6 {
		t.Fatalf("objects %+v", n)
	}
	mdWant(t, res, "obj[0].number", `"1"`)
	mdWant(t, res, "obj[0].generation", `"0"`)
	mdWant(t, res, "obj[0].body", `"<< /Type /Catalog /Pages 2 0 R >>"`)
	mdWant(t, res, "obj[0]", "1 0 obj << /Type /Catalog /Pages 2 0 R >>")
	if n := res.Root.Find("obj[3].streamData"); n == nil || n.Size != 49 {
		t.Errorf("stream with a direct length %+v", n)
	}
	mdWant(t, res, "obj[3].endstreamKeyword", `"endstream"`)
	mdWant(t, res, "obj[3].endobjKeyword", `"endobj"`)
	// The stream has "endstream" in it: its length is found by the reference
	if n := res.Root.Find("obj[4].streamData"); n == nil || n.Size != 30 {
		t.Errorf("stream with an indirect length %+v", n)
	}
	mdWant(t, res, "obj[5].body", `"30"`)
	mdWant(t, res, "xref.keyword", `"xref"`)
	mdWant(t, res, "xref.subsection.header", `"0 7"`)
	if n := res.Root.Find("xref.subsection.entry"); n == nil || n.ChildCount() != 7 {
		t.Fatalf("xref entries %+v", n)
	}
	mdWant(t, res, "xref.subsection.entry[1]", fmt.Sprintf("%010d 00000 n", bytes.Index(data, []byte("1 0 obj"))))
	mdWant(t, res, "trailer.dictionary", `"<< /Size 7 /Root 1 0 R /Info << /Title (a >> b) >> >>"`)
	mdWant(t, res, "startxref.offset", fmt.Sprintf(`"%d"`, bytes.Index(data, []byte("xref\n"))))
	mdWant(t, res, "eofMarker", `"%%EOF"`)
	mdTruncated(t, "pdf.bt", data, 5)
}

// A PDF over the scanned size: the middle is not scanned, the end is
func TestPDFBig(t *testing.T) {
	data := mdMakePDF(5 << 20)
	start := time.Now()
	res := libraryRun(t, data, "big.pdf", "pdf.bt")
	if d := time.Since(start); d > 10*time.Second && !raceEnabled {
		t.Errorf("%v for %d bytes", d, len(data))
	}
	if n := res.Root.Find("obj[4].streamData"); n == nil || n.Size != 5<<20 {
		t.Errorf("a big stream %+v", n)
	}
	mdWant(t, res, "xref.subsection.header", `"0 7"`)
	mdWant(t, res, "eofMarker", `"%%EOF"`)

	// Many small objects, a text file with no zero byte
	var b bytes.Buffer
	b.WriteString("%PDF-1.7\n")
	for i := 1; i <= 20000; i++ {
		fmt.Fprintf(&b, "%d 0 obj\n<< /Type /Annot /N %d /S (text text text text) >>\nendobj\n", i, i)
	}
	b.WriteString("trailer\n<< /Size 20001 >>\n%%EOF\n")
	start = time.Now()
	res = libraryRun(t, b.Bytes(), "many.pdf", "pdf.bt")
	if n := res.Root.Find("obj"); n == nil || n.ChildCount() != 20000 {
		t.Fatalf("objects %+v", n)
	}
	t.Logf("%d objects, %d bytes: %v", 20000, b.Len(), time.Since(start))
	if d := time.Since(start); d > 20*time.Second && !raceEnabled {
		t.Errorf("%v", d)
	}
}

func TestPDFRealFile(t *testing.T) { mdRealFile(t, "pdf", "pdf.bt") }

// ---- WAV ----

func mdLE16(v uint16) []byte { b := make([]byte, 2); binary.LittleEndian.PutUint16(b, v); return b }
func mdLE32(v uint32) []byte { b := make([]byte, 4); binary.LittleEndian.PutUint32(b, v); return b }

func mdChunk(id string, data []byte) []byte {
	out := append([]byte(id), mdLE32(uint32(len(data)))...)
	out = append(out, data...)
	if len(data)%2 == 1 {
		out = append(out, 0)
	}
	return out
}

func mdRIFF(form string, chunks ...[]byte) []byte {
	body := []byte(form)
	for _, c := range chunks {
		body = append(body, c...)
	}
	return append(append([]byte("RIFF"), mdLE32(uint32(len(body)))...), body...)
}

func mdMakeWAV(extensible bool) []byte {
	var f []byte
	if extensible {
		f = append(f, mdLE16(0xFFFE)...)
	} else {
		f = append(f, mdLE16(1)...)
	}
	f = append(f, mdLE16(2)...)
	f = append(f, mdLE32(44100)...)
	f = append(f, mdLE32(44100*4)...)
	f = append(f, mdLE16(4)...)
	f = append(f, mdLE16(16)...)
	if extensible {
		f = append(f, mdLE16(22)...)
		f = append(f, mdLE16(16)...)
		f = append(f, mdLE32(3)...)
		// KSDATAFORMAT_SUBTYPE_PCM
		f = append(f, 1, 0, 0, 0, 0, 0, 0x10, 0, 0x80, 0, 0, 0xAA, 0, 0x38, 0x9B, 0x71)
	}
	samples := make([]byte, 400)
	for i := range samples {
		samples[i] = byte(i * 7)
	}
	info := append([]byte("INFO"), mdChunk("INAM", []byte("Test\x00"))...)
	info = append(info, mdChunk("ISFT", []byte("althex\x00"))...)
	return mdRIFF("WAVE",
		mdChunk("fmt ", f),
		mdChunk("fact", mdLE32(100)),
		mdChunk("data", samples),
		mdChunk("LIST", info),
		mdChunk("junk", []byte{1, 2, 3}))
}

func TestWAVTemplate(t *testing.T) {
	data := mdMakeWAV(false)
	res := libraryRun(t, data, "a.wav", "wav.bt")
	mdWant(t, res, "header.ckID", `"RIFF"`)
	mdWant(t, res, "header.formType", `"WAVE"`)
	mdWant(t, res, "chunk[0]", "fmt PCM 2ch 44100 Hz 16 bit")
	mdWant(t, res, "chunk[0].wFormatTag", "WAVE_FORMAT_PCM (1)")
	mdWant(t, res, "chunk[0].nBlockAlign", "4")
	mdWant(t, res, "chunk[1].dwSampleLength", "100")
	mdWant(t, res, "chunk[2]", "data 400 bytes")
	if n := res.Root.Find("chunk[2].samples"); n == nil || n.Size != 400 {
		t.Errorf("samples %+v", n)
	}
	mdWant(t, res, "chunk[3].listType", `"INFO"`)
	mdWant(t, res, "chunk[3].subchunk[0].text", `"Test"`)
	mdWant(t, res, "chunk[3].subchunk[0]", `INAM "Test"`)
	if res.Root.Find("chunk[3].subchunk[0].pad") == nil {
		t.Error("no pad byte after an odd chunk")
	}
	mdWant(t, res, "chunk[3].subchunk[1].text", `"althex"`)
	mdWant(t, res, "chunk[4]", "junk 3 bytes")
	if res.Root.Find("chunk[4].pad") == nil || res.Root.Find("trailing") != nil {
		t.Error("the last pad byte")
	}
	mdTruncated(t, "wav.bt", data, 3)

	data = mdMakeWAV(true)
	res = libraryRun(t, data, "a.wav", "wav.bt")
	mdWant(t, res, "chunk[0]", "fmt EXTENSIBLE PCM 2ch 44100 Hz 16 bit")
	mdWant(t, res, "chunk[0].wValidBitsPerSample", "16")
	mdWant(t, res, "chunk[0].dwChannelMask", "0x00000003")
	mdWant(t, res, "chunk[0].dwChannelMask.SPEAKER_FRONT_RIGHT", "1")
	mdWant(t, res, "chunk[0].SubFormat", "{00000001-0000-0010-8000-00AA00389B71}")
	mdTruncated(t, "wav.bt", data, 5)
}

// RF64: the sizes over 4 GB are in the ds64 chunk
func TestWAVRF64(t *testing.T) {
	le64 := func(v uint64) []byte { b := make([]byte, 8); binary.LittleEndian.PutUint64(b, v); return b }
	wav := mdMakeWAV(false)
	fmtChunk := wav[12 : 12+8+16]
	samples := bytes.Repeat([]byte{1, 2, 3, 4}, 10)
	ds64 := mdChunk("ds64", mdCat(le64(0), le64(uint64(len(samples))), le64(10), mdLE32(0)))
	data := mdCat([]byte("RF64"), mdLE32(0xFFFFFFFF), []byte("WAVE"), ds64, fmtChunk,
		[]byte("data"), mdLE32(0xFFFFFFFF), samples)
	res := libraryRun(t, data, "a.rf64", "wav.bt")
	mdWant(t, res, "chunk[0].dataSize", "40")
	mdWant(t, res, "chunk[1]", "fmt PCM 2ch 44100 Hz 16 bit")
	if n := res.Root.Find("chunk[2].samples"); n == nil || n.Size != 40 {
		t.Errorf("samples %+v", n)
	}
	mdTruncated(t, "wav.bt", data, 3)
}

// Another RIFF form (AVI-like) is walked the same way
func TestWAVTemplateOnAVI(t *testing.T) {
	avih := mdChunk("avih", make([]byte, 56))
	strl := mdChunk("LIST", append([]byte("strl"), mdChunk("strh", make([]byte, 56))...))
	hdrl := mdChunk("LIST", append(append([]byte("hdrl"), avih...), strl...))
	movi := mdChunk("LIST", append([]byte("movi"), mdChunk("00dc", []byte{1, 2, 3, 4, 5})...))
	data := mdRIFF("AVI ", hdrl, movi, mdChunk("idx1", make([]byte, 16)))
	p := mdTemplate(t, "wav.bt")
	res := p.Run(context.Background(), Env{Data: &mem{b: data}})
	if res.Err != nil {
		t.Fatal(res.Err)
	}
	mdWant(t, res, "chunk[0].listType", `"hdrl"`)
	mdWant(t, res, "chunk[0].subchunk[1].subchunk", "strh 56 bytes")
	mdWant(t, res, "chunk[1]", "LIST movi 18 bytes")
	mdWant(t, res, "chunk[2]", "idx1 16 bytes")
	mdTruncated(t, "wav.bt", data, 9)
	if tm := Detect(Library(), &mem{b: data}, "x.avi"); tm != nil && tm.File == "wav.bt" {
		t.Error("an AVI file is detected as WAV")
	}
}

func TestWAVRealFile(t *testing.T) { mdRealFile(t, "wav", "wav.bt") }

// ---- MP4 ----

func mdBE16(v uint16) []byte { b := make([]byte, 2); binary.BigEndian.PutUint16(b, v); return b }
func mdBE32(v uint32) []byte { b := make([]byte, 4); binary.BigEndian.PutUint32(b, v); return b }
func mdBE64(v uint64) []byte { b := make([]byte, 8); binary.BigEndian.PutUint64(b, v); return b }

func mdCat(parts ...[]byte) []byte {
	var out []byte
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

func mdBox(typ string, parts ...[]byte) []byte {
	body := mdCat(parts...)
	return mdCat(mdBE32(uint32(8+len(body))), []byte(typ), body)
}

// mdFull is the version and the flags of a full box
func mdFull(version byte, flags uint32) []byte {
	return mdBE32(uint32(version)<<24 | flags)
}

func mdMakeMP4() []byte {
	matrix := mdCat(mdBE32(0x10000), mdBE32(0), mdBE32(0), mdBE32(0), mdBE32(0x10000), mdBE32(0), mdBE32(0), mdBE32(0), mdBE32(0x40000000))
	mvhd := mdBox("mvhd", mdFull(0, 0), mdBE32(100), mdBE32(200), mdBE32(1000), mdBE32(5000),
		mdBE32(0x10000), mdBE16(0x100), make([]byte, 10), matrix, make([]byte, 24), mdBE32(2))
	tkhd := mdBox("tkhd", mdFull(1, 3), mdBE64(100), mdBE64(200), mdBE32(1), mdBE32(0), mdBE64(5000),
		make([]byte, 8), mdBE16(0), mdBE16(0), mdBE16(0), mdBE16(0), matrix, mdBE32(320<<16), mdBE32(240<<16))
	mdhd := mdBox("mdhd", mdFull(0, 0), mdBE32(100), mdBE32(200), mdBE32(90000), mdBE32(450000),
		mdBE16(('e'-0x60)<<10|('n'-0x60)<<5|('g'-0x60)), mdBE16(0))
	hdlr := mdBox("hdlr", mdFull(0, 0), mdBE32(0), []byte("vide"), make([]byte, 12), []byte("VideoHandler\x00"))
	avcC := mdBox("avcC", []byte{1, 0x64, 0, 0x1F, 0xFF})
	compressor := make([]byte, 32)
	avc1 := mdBox("avc1", make([]byte, 6), mdBE16(1), make([]byte, 16), mdBE16(320), mdBE16(240),
		mdBE32(72<<16), mdBE32(72<<16), mdBE32(0), mdBE16(1), compressor, mdBE16(24), mdBE16(0xFFFF), avcC)
	stsd := mdBox("stsd", mdFull(0, 0), mdBE32(1), avc1)
	stts := mdBox("stts", mdFull(0, 0), mdBE32(2), mdBE32(3), mdBE32(3000), mdBE32(1), mdBE32(1500))
	stsc := mdBox("stsc", mdFull(0, 0), mdBE32(1), mdBE32(1), mdBE32(4), mdBE32(1))
	stsz := mdBox("stsz", mdFull(0, 0), mdBE32(0), mdBE32(4), mdBE32(10), mdBE32(20), mdBE32(30), mdBE32(40))
	stco := mdBox("stco", mdFull(0, 0), mdBE32(1), mdBE32(0)) // patched below
	stbl := mdBox("stbl", stsd, stts, stsc, stsz, stco)
	vmhd := mdBox("vmhd", mdFull(0, 1), make([]byte, 8))
	dinf := mdBox("dinf", mdBox("dref", mdFull(0, 0), mdBE32(1), mdBox("url ", mdFull(0, 1))))
	minf := mdBox("minf", vmhd, dinf, stbl)
	mdia := mdBox("mdia", mdhd, hdlr, minf)
	trak := mdBox("trak", tkhd, mdia)
	udta := mdBox("udta", mdBox("meta", mdFull(0, 0), mdBox("hdlr", mdFull(0, 0), mdBE32(0), []byte("mdir"), make([]byte, 12), []byte{0}),
		mdBox("ilst", mdBox("\xA9nam", mdBox("data", mdBE32(1), mdBE32(0), []byte("Title"))))))
	moov := mdBox("moov", mvhd, trak, udta)
	ftyp := mdBox("ftyp", []byte("isom"), mdBE32(0x200), []byte("isomiso2avc1mp41"))
	mdat := mdBox("mdat", bytes.Repeat([]byte{0xAB}, 100))
	data := mdCat(ftyp, moov, mdat)
	// stco points at the media data
	at := bytes.Index(data, []byte("stco")) + 12
	binary.BigEndian.PutUint32(data[at:], uint32(len(ftyp)+len(moov)+8))
	return data
}

func TestMP4Template(t *testing.T) {
	data := mdMakeMP4()
	res := libraryRun(t, data, "a.mp4", "mp4.bt")
	mdWant(t, res, "box[0]", "ftyp 32 bytes")
	mdWant(t, res, "box[0].major_brand", `"isom"`)
	mdWant(t, res, "box[0].minor_version", "0x00000200")
	mdWant(t, res, "box[0].compatible_brands[3]", `"mp41"`)
	mdWantPrefix(t, res, "box[1]", "moov ")
	mdWant(t, res, "box[1].box[0].timescale", "1000")
	mdWant(t, res, "box[1].box[0].duration", "5000")
	mdWant(t, res, "box[1].box[0].next_track_ID", "2")
	trak := "box[1].box[1]"
	mdWant(t, res, trak+".box[0].version", "1")
	mdWant(t, res, trak+".box[0].track_ID", "1")
	mdWant(t, res, trak+".box[0].duration", "5000")
	mdWant(t, res, trak+".box[0].width", "320")
	mdia := trak + ".box[1]"
	mdWant(t, res, mdia+".box[0].timescale", "90000")
	mdWant(t, res, mdia+".box[0].language", "eng")
	mdWant(t, res, mdia+".box[1].handler_type", `"vide"`)
	stbl := mdia + ".box[2].box[2]"
	mdWantPrefix(t, res, stbl, "stbl ")
	mdWant(t, res, mdia+".box[2].box[1].box.entry_count", "1")
	mdWant(t, res, stbl+".box[0].entry_count", "1")
	mdWant(t, res, stbl+".box[0].entry.width", "320")
	mdWant(t, res, stbl+".box[0].entry.box", "avcC 13 bytes")
	mdWant(t, res, stbl+".box[1].entries[1].sample_delta", "1500")
	mdWant(t, res, stbl+".box[2].entries[0].samples_per_chunk", "4")
	mdWant(t, res, stbl+".box[3].sample_count", "4")
	mdWant(t, res, stbl+".box[3].entry_size[2]", "30")
	mdWant(t, res, stbl+".box[4].chunk_offset[0]", fmt.Sprintf("0x%08X", bytes.Index(data, []byte("mdat"))+4))
	mdWant(t, res, "box[1].box[2].box.version", "0")
	mdWant(t, res, "box[1].box[2].box.box[1].box.box.value", `"Title"`)
	mdWant(t, res, "box[2]", "mdat 108 bytes")
	if n := res.Root.Find("box[2].data"); n == nil || n.Size != 100 {
		t.Errorf("mdat data %+v", n)
	}
	mdTruncated(t, "mp4.bt", data, 3)

	// largesize and a box to the end of the file
	big := mdCat(mdBox("ftyp", []byte("mp42"), mdBE32(0)),
		mdBE32(1), []byte("free"), mdBE64(24), mdBE64(7),
		mdBE32(0), []byte("mdat"), []byte{1, 2, 3})
	res = libraryRun(t, big, "b.mp4", "mp4.bt")
	mdWant(t, res, "box[1].largesize", "24")
	mdWant(t, res, "box[1]", "free 24 bytes")
	mdWant(t, res, "box[2]", "mdat 11 bytes")

	// A damaged size
	bad := mdCat(mdBox("ftyp", []byte("mp42"), mdBE32(0)), mdBE32(5), []byte("junk"), make([]byte, 20))
	res = libraryRun(t, bad, "c.mp4", "mp4.bt")
	if n := res.Root.Find("trailing"); n == nil || n.Size != 28 {
		t.Errorf("trailing %+v", n)
	}
	// Boxes nested deeper than the limit of the language
	deep := []byte{}
	for i := 0; i < 400; i++ {
		deep = mdBox("moov", deep)
	}
	deep = mdCat(mdBox("ftyp", []byte("mp42"), mdBE32(0)), deep)
	p := mdTemplate(t, "mp4.bt")
	if res := p.Run(context.Background(), Env{Data: &mem{b: deep}}); res.Err != nil {
		t.Errorf("deep boxes: %v", res.Err)
	}
}

func TestMP4RealFile(t *testing.T) { mdRealFile(t, "mp4", "mp4.bt") }
