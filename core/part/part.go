// Package part reads the partition table of a disk or a disk image: MBR
// with its chain of EBRs, GPT with its backup, the Apple Partition Map,
// the BSD disklabel and the Sun VTOC (BOOT-2). It also checks the table
// and reports what is wrong with it, with the bytes to highlight (BOOT-3).
//
// Damaged data is the normal case here: Read never fails on a table it
// found, whatever is in it; the problems go to Table.Issues.
package part

import (
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
)

// ErrNoTable: the data has no partition table that Read knows
var ErrNoTable = errors.New("no partition table")

// Partition is one partition of a table
type Partition struct {
	Index    int    // 1-based as tools show them
	Scheme   string // "MBR", "EBR", "GPT", "APM", "BSD", "VTOC"
	Start    int64  // bytes from the start of the disk
	Size     int64  // bytes
	TypeID   string // "0x83" for MBR, the GUID for GPT
	Type     string // a readable name of the type
	Name     string // the GPT name, "" for MBR
	GUID     string // the GPT unique partition GUID
	Bootable bool   // MBR active flag, GPT legacy BIOS bootable attribute
	Attrs    uint64 // GPT attributes
	Extended bool   // an MBR extended partition (a container of logical ones)
	Logical  bool   // inside an extended one
	EntryOff int64  // where its table entry is, for going to it
	EntryLen int64
}

// Severity of an issue
type Severity int

const (
	Error   Severity = iota // the table is wrong: tools may refuse it or lose data
	Warning                 // unusual, but it works
)

func (s Severity) String() string {
	if s == Error {
		return "error"
	}
	return "warning"
}

// Issue is a problem found in a table
type Issue struct {
	Severity Severity
	Off, Len int64  // the bytes the problem is in (e.g. the CRC field), for highlighting
	Message  string // short, readable: "GPT header CRC32 is 0x1234, should be 0xABCD"
}

// Table is the partition table of a disk
type Table struct {
	Scheme     string // "MBR", "GPT", "GPT (hybrid MBR)", "APM", "BSD", "VTOC"
	SectorSize int
	DiskGUID   string // the GPT disk GUID, the MBR disk signature ("0x1234ABCD")
	Partitions []Partition
	Issues     []Issue

	// whole marks partitions that stand for the whole disk (BSD 'c', the
	// VTOC backup slice): they overlap everything by design
	whole map[int]bool
}

// maxIssues keeps a table full of garbage from making millions of issues
const maxIssues = 1000

func (t *Table) issue(sev Severity, off, n int64, format string, args ...any) {
	if len(t.Issues) >= maxIssues {
		return
	}
	t.Issues = append(t.Issues, Issue{Severity: sev, Off: off, Len: n, Message: fmt.Sprintf(format, args...)})
}

// Read finds the partition table of a disk; sectorSize 0 - try 512 and
// 4096 for GPT (other tables use 512). ErrNoTable when there is none.
func Read(r io.ReaderAt, size int64, sectorSize int) (*Table, error) {
	if sectorSize != 0 && (sectorSize < 512 || sectorSize > 65536 || sectorSize&(sectorSize-1) != 0) {
		return nil, fmt.Errorf("unsupported sector size %d", sectorSize)
	}
	if size < 0 {
		size = 0
	}
	d := &disk{r: r, size: size}
	sizes := []int{512, 4096}
	if sectorSize != 0 {
		sizes = []int{sectorSize}
	}

	// GPT first: its protective MBR is a valid MBR too
	for _, ss := range sizes {
		if t := readGPT(d, ss, false); t != nil {
			return finish(d, t), nil
		}
	}
	// A protective MBR without the primary header: the backup may be there
	if mbrHasType(d, 0xEE) {
		for _, ss := range sizes {
			if t := readGPT(d, ss, true); t != nil {
				return finish(d, t), nil
			}
		}
	}
	ss := 512
	if sectorSize != 0 {
		ss = sectorSize
	}
	for _, f := range []func(*disk, int) *Table{readVTOC, readAPM, readBSD, readMBR} {
		if t := f(d, ss); t != nil {
			return finish(d, t), nil
		}
	}
	return nil, ErrNoTable
}

// finish makes the checks common to all tables
func finish(d *disk, t *Table) *Table {
	for _, p := range t.Partitions {
		if p.Size > 0 && endOf(p) > d.size {
			t.issue(Error, p.EntryOff, p.EntryLen, "partition %s (%s) ends at %d, beyond the end of the disk (%d bytes)",
				label(p), p.Scheme, endOf(p), d.size)
		}
	}
	checkOverlaps(t)
	return t
}

// checkOverlaps reports every two partitions that share bytes, but an
// extended one with its logical ones and the ones for the whole disk
func checkOverlaps(t *Table) {
	idx := make([]int, 0, len(t.Partitions))
	for i, p := range t.Partitions {
		if p.Size > 0 && !t.whole[i] {
			idx = append(idx, i)
		}
	}
	sort.SliceStable(idx, func(a, b int) bool { return t.Partitions[idx[a]].Start < t.Partitions[idx[b]].Start })
	// Sorted by start: only the following ones that start before the end of
	// one can overlap it
	found := 0
	for a := 0; a < len(idx) && found < 100; a++ {
		p := t.Partitions[idx[a]]
		end := endOf(p)
		for b := a + 1; b < len(idx) && found < 100; b++ {
			q := t.Partitions[idx[b]]
			if q.Start >= end {
				break
			}
			if (p.Extended && q.Logical) || (q.Extended && p.Logical) {
				continue
			}
			found++
			later := q
			if p.EntryOff > q.EntryOff {
				later = p
			}
			t.issue(Error, later.EntryOff, later.EntryLen, "partitions %s and %s overlap (bytes %d-%d)",
				label(p), label(q), q.Start, min(end, endOf(q))-1)
		}
	}
}

// label names a partition in messages: "3", "BSD a"
func label(p Partition) string {
	if p.Scheme == "BSD" && p.Name != "" {
		return p.Name
	}
	return fmt.Sprint(p.Index)
}

// disk reads the data with its size known; reads past the end are short
type disk struct {
	r    io.ReaderAt
	size int64
}

// maxRead caps what one read takes: tables are small, a bigger read is a
// damaged field
const maxRead = 32 << 20

// read returns the bytes at off, fewer than n at the end of the data
func (d *disk) read(off, n int64) []byte {
	if off < 0 || n <= 0 || off >= d.size {
		return nil
	}
	n = min(n, d.size-off, maxRead)
	b := make([]byte, n)
	k, err := d.r.ReadAt(b, off)
	if k < len(b) && err != nil {
		b = b[:k]
	}
	return b
}

// readFull returns exactly n bytes at off, or nil
func (d *disk) readFull(off, n int64) []byte {
	b := d.read(off, n)
	if int64(len(b)) != n {
		return nil
	}
	return b
}

// bytesOf turns a number of sectors into bytes without overflow
func bytesOf(sectors uint64, ss int) int64 {
	if sectors > uint64(math.MaxInt64)/uint64(ss) {
		return math.MaxInt64
	}
	return int64(sectors) * int64(ss)
}

// endOf is the end of a partition, at most math.MaxInt64
func endOf(p Partition) int64 {
	if p.Start > math.MaxInt64-p.Size {
		return math.MaxInt64
	}
	return p.Start + p.Size
}

// mulSat multiplies without going past the largest uint64
func mulSat(a, b uint64) uint64 {
	if a != 0 && b > math.MaxUint64/a {
		return math.MaxUint64
	}
	return a * b
}
