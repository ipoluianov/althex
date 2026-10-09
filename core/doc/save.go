package doc

import (
	"bufio"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"slices"

	"github.com/ipoluianov/althex/core/source"
)

// SaveOptions tell how to save
type SaveOptions struct {
	// Path to save to; "" - the file of the document
	Path string
	// Backup keeps the file as it was in <name>.bak (SAVE-3)
	Backup bool
	// Progress is called now and then with the bytes written and to write
	Progress func(done, total int64)
	// Cancel is checked now and then; true stops saving, the file stays as it was
	Cancel func() bool
}

var (
	ErrCanceled = errors.New("canceled")
	ErrNoPath   = errors.New("the document has no file yet")
)

// keepLimit is how many bytes of the file saving in place copies to the
// journal, so the states before the save can still be gone back to. Over
// it the history before the save is dropped instead (e.g. a fill of 10 GB).
const keepLimit = 256 << 20

// saveChunk is how much is copied at once
const saveChunk = 1 << 20

// testCrash stops saving in place at a point, as a crash there would (tests only)
var (
	testCrash    string
	errTestCrash = errors.New("test crash")
)

// rng is a range of the document
type rng struct{ off, len int64 }

// Save writes the document to its file or to opts.Path. Without inserts
// and deletes in the middle only the changed blocks are written (SAVE-1),
// with a write-ahead log in the journal so a crash in the middle is
// finished on recovery. Otherwise a new file is written next to the old
// one and put in its place at once (SAVE-2). Edits wait while it runs;
// reading does not.
func (d *Document) Save(opts SaveOptions) error {
	d.mu.Lock()
	err := d.saveLocked(opts)
	d.mu.Unlock()
	if err == nil {
		d.notify()
	}
	return err
}

func (d *Document) saveLocked(opts SaveOptions) error {
	target := opts.Path
	if target == "" {
		target = d.path
	}
	if target == "" {
		return ErrNoPath
	}
	if abs, err := filepath.Abs(target); err == nil {
		target = abs
	}
	if d.groupDeep > 0 {
		return errors.New("an edit is in progress")
	}
	if target == d.path && d.file != nil && !opts.Backup {
		if writes, ok := d.inPlaceWrites(); ok {
			return d.saveInPlace(writes, opts)
		}
	}
	return d.saveRewrite(target, opts)
}

// inPlaceWrites returns the ranges that differ from the file, if the
// document can be saved by writing them alone: every byte of the file that
// stays is at the same offset
func (d *Document) inPlaceWrites() ([]rng, bool) {
	var writes []rng
	add := func(off, n int64) {
		if n <= 0 {
			return
		}
		if k := len(writes) - 1; k >= 0 && writes[k].off+writes[k].len == off {
			writes[k].len += n
			return
		}
		writes = append(writes, rng{off, n})
	}
	ok := true
	root := d.hist.cur.root
	walk(root, 0, 0, root.bytes(), func(start int64, p piece) bool {
		if p.buf != d.srcIdx {
			add(start, p.len)
			return true
		}
		if p.off != start {
			ok = false
			return false
		}
		// Bytes overwritten by an earlier save differ on the disk
		d.src.parts(p.off, p.len, func(at, k int64, kept bool, _ int64) bool {
			if kept {
				add(at, k)
			}
			return true
		})
		return true
	})
	return writes, ok
}

func (d *Document) saveInPlace(writes []rng, opts SaveOptions) error {
	snap := &Snapshot{root: d.hist.cur.root, bufs: d.bufs, src: d.srcIdx}
	newSize := snap.Size()
	diskSize := d.file.Stat().Size
	// Every version of the file up to the longest loses the bytes past the new end
	maxSize := diskSize
	for _, v := range d.versions() {
		maxSize = max(maxSize, v.size())
	}

	// The bytes the file loses are kept for the history first
	lost := append(slices.Clone(writes), rng{newSize, maxSize - newSize})
	toKeep := int64(0)
	for _, r := range lost {
		toKeep += d.toKeepFile(r.off, r.len)
	}
	keepHistory := toKeep <= keepLimit
	if keepHistory {
		kept := make(map[int][]span)
		for _, r := range lost {
			added, err := d.keepFile(r.off, r.len)
			if err != nil {
				return err
			}
			for idx, s := range added {
				kept[idx] = append(kept[idx], s...)
			}
		}
		for idx, s := range kept {
			d.journal.logKeep(idx, s)
		}
		if err := d.add.sync(); err != nil {
			return err
		}
		if err := d.journal.sync(); err != nil {
			return err
		}
	}

	total := int64(0)
	for _, w := range writes {
		total += w.len
	}
	walPath := ""
	if d.journal != nil {
		walPath = filepath.Join(d.journal.dir, walFile)
		if err := writeWAL(walPath, d.hist.cur.ID, newSize, snap, writes, opts); err != nil {
			os.Remove(walPath)
			return err
		}
	}

	if testCrash == "afterWAL" {
		return errTestCrash
	}
	f, err := os.OpenFile(d.path, os.O_WRONLY, 0)
	if err != nil {
		os.Remove(walPath)
		return err
	}
	err = copyRanges(f, snap, writes, total, opts, false)
	if testCrash == "afterWrite" {
		f.Close()
		return errTestCrash
	}
	if err == nil && newSize != diskSize {
		err = f.Truncate(newSize)
	}
	if err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	cache := d.src.cache.Load()
	for _, w := range lost {
		cache.Invalidate(w.off, w.len)
	}
	if err != nil {
		// The file may be written in part: the log finishes it on recovery,
		// and the document still has all of it
		return err
	}
	d.file.Refresh()

	// The file as it is now is a new version: the saved state is all of it
	v := newSrcBuffer(d.file, cache, d.add)
	d.src = v
	d.srcIdx = len(d.bufs)
	d.bufs = append(d.bufs, v)
	d.journal.logBuffer(d.srcIdx, true, d.path, d.file.Stat())
	cur := d.hist.cur
	cur.root = buildTree([]piece{{d.srcIdx, 0, newSize}})
	cur.sealed = true
	d.hist.saved = cur
	if keepHistory {
		d.journal.logCheckpoint(cur, true)
	} else {
		d.dropHistory()
	}
	d.journal.sync()
	if walPath != "" {
		os.Remove(walPath)
	}
	d.publish()
	return nil
}

// dropHistory makes the current state the only one
func (d *Document) dropHistory() {
	cur := d.hist.cur
	saved := d.hist.saved == cur
	d.hist = newHistory(cur.root, d.hist.nextID)
	if !saved {
		d.hist.saved = nil
	}
	d.journal.logCheckpoint(d.hist.cur, saved)
}

// writeWAL writes the bytes of the ranges with a checksum to the log of the
// save, and makes it durable before the file is touched
func writeWAL(path string, id int, newSize int64, snap *Snapshot, writes []rng, opts SaveOptions) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	sum := crc32.NewIEEE()
	w := bufio.NewWriterSize(io.MultiWriter(f, sum), saveChunk)
	var hdr []byte
	hdr = append(hdr, walMagic...)
	hdr = binary.AppendVarint(hdr, int64(id))
	hdr = binary.AppendVarint(hdr, newSize)
	hdr = binary.AppendVarint(hdr, int64(len(writes)))
	w.Write(hdr)
	buf := make([]byte, saveChunk)
	for _, r := range writes {
		var b []byte
		b = binary.AppendVarint(b, r.off)
		b = binary.AppendVarint(b, r.len)
		w.Write(b)
		for done := int64(0); done < r.len; {
			if opts.Cancel != nil && opts.Cancel() {
				return ErrCanceled
			}
			k := min(int64(len(buf)), r.len-done)
			if _, err := snap.ReadAt(buf[:k], r.off+done); err != nil && err != io.EOF {
				return err
			}
			if _, err := w.Write(buf[:k]); err != nil {
				return err
			}
			done += k
		}
	}
	if err := w.Flush(); err != nil {
		return err
	}
	if _, err := f.Write(binary.LittleEndian.AppendUint32(nil, sum.Sum32())); err != nil {
		return err
	}
	return f.Sync()
}

const walMagic = "ALTHEXWAL1"

// replayWAL finishes a save in place that a crash stopped: the log is
// complete, so its writes are made again. Returns the state that was being
// saved; ok is false when the log is not complete (the file was not touched).
func replayWAL(walPath, filePath string) (id int, ok bool, err error) {
	data, err := os.ReadFile(walPath)
	if err != nil || len(data) < len(walMagic)+4 {
		return 0, false, nil
	}
	body := data[:len(data)-4]
	if crc32.ChecksumIEEE(body) != binary.LittleEndian.Uint32(data[len(data)-4:]) ||
		string(body[:len(walMagic)]) != walMagic {
		return 0, false, nil
	}
	fl := &fields{b: body[len(walMagic):]}
	id = int(fl.int())
	newSize := fl.int()
	count := fl.int()
	f, err := os.OpenFile(filePath, os.O_WRONLY, 0)
	if err != nil {
		return 0, false, err
	}
	defer f.Close()
	for i := int64(0); i < count && fl.err == nil; i++ {
		off, n := fl.int(), fl.int()
		if fl.err != nil || int64(len(fl.b)) < n {
			return 0, false, errBadRecord
		}
		if _, err := f.WriteAt(fl.b[:n], off); err != nil {
			return 0, false, err
		}
		fl.b = fl.b[n:]
	}
	if fl.err != nil {
		return 0, false, fl.err
	}
	if err := f.Truncate(newSize); err != nil {
		return 0, false, err
	}
	return id, true, f.Sync()
}

// copyRanges writes the ranges of the snapshot to f at their offsets
// (sequential: one after another from the start of f)
func copyRanges(f *os.File, snap *Snapshot, ranges []rng, total int64, opts SaveOptions, sequential bool) error {
	buf := make([]byte, saveChunk)
	written := int64(0)
	for _, r := range ranges {
		for done := int64(0); done < r.len; {
			if opts.Cancel != nil && opts.Cancel() {
				return ErrCanceled
			}
			k := min(int64(len(buf)), r.len-done)
			if _, err := snap.ReadAt(buf[:k], r.off+done); err != nil && err != io.EOF {
				return err
			}
			var err error
			if sequential {
				_, err = f.Write(buf[:k])
			} else {
				_, err = f.WriteAt(buf[:k], r.off+done)
			}
			if err != nil {
				return err
			}
			done += k
			written += k
			if opts.Progress != nil {
				opts.Progress(written, total)
			}
		}
	}
	return nil
}

func (d *Document) saveRewrite(target string, opts SaveOptions) error {
	snap := &Snapshot{root: d.hist.cur.root, bufs: d.bufs, src: d.srcIdx}
	size := snap.Size()
	dir, base := filepath.Split(target)
	tmp, err := os.CreateTemp(dir, "."+base+".althex-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	err = copyRanges(tmp, snap, []rng{{0, size}}, size, opts, true)
	if err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		os.Remove(tmpPath)
		return err
	}
	// The new file gets the permissions of the old one
	if st, err := os.Stat(target); err == nil {
		os.Chmod(tmpPath, st.Mode().Perm())
	}

	sameFile := target == d.path && d.file != nil
	backup := ""
	if opts.Backup {
		if _, err := os.Stat(target); err == nil {
			backup = target + ".bak"
			os.Remove(backup)
			if err := os.Rename(target, backup); err != nil {
				os.Remove(tmpPath)
				return err
			}
		}
	}
	err = os.Rename(tmpPath, target)
	if err != nil && sameFile {
		// A system that does not replace an open file: the history before the
		// save then reads the backup, or is dropped
		d.file.Close()
		if err = os.Rename(tmpPath, target); err == nil {
			d.reopenOldFile(backup)
		} else if f, reErr := source.OpenFile(target); reErr == nil {
			d.replaceFile(f) // the file stays as it was
		}
	}
	if err != nil {
		if backup != "" {
			os.Rename(backup, target)
		}
		os.Remove(tmpPath)
		return err
	}
	syncDir(dir)
	// The older states read the file as it was: under its new name now
	if sameFile && backup != "" && d.file != nil {
		for idx := range d.versions() {
			d.journal.logBuffer(idx, false, backup, d.file.Stat())
		}
	}

	f, err := source.OpenFile(target)
	if err != nil {
		return err
	}
	if d.file != nil {
		d.oldFiles = append(d.oldFiles, d.file)
	}
	if target != d.path {
		d.path = target
		d.journal.logPath(target)
	}
	d.attachFile(f)
	cur := d.hist.cur
	cur.root = buildTree([]piece{{d.srcIdx, 0, size}})
	cur.sealed = true
	d.hist.saved = cur
	if d.closedSource() {
		d.dropHistory()
	} else {
		d.journal.logCheckpoint(cur, true)
	}
	d.journal.sync()
	d.publish()
	return nil
}

// reopenOldFile opens the backup in place of the file closed to replace it
func (d *Document) reopenOldFile(backup string) {
	if backup == "" {
		return
	}
	f, err := source.OpenFile(backup)
	if err != nil {
		return
	}
	d.replaceFile(f)
}

// replaceFile makes the versions of the file read another handle of it
func (d *Document) replaceFile(f *source.File) {
	cache := source.NewCache(f, source.DefaultCachePages)
	for _, v := range d.versions() {
		v.file = f
		v.cache.Store(cache)
	}
	d.file = f
}

// closedSource tells whether the source of the older states was closed by
// saving (see saveRewrite), so they cannot be read any more
func (d *Document) closedSource() bool {
	for _, f := range d.oldFiles {
		if _, err := f.ReadAt(make([]byte, 1), 0); errors.Is(err, source.ErrClosed) {
			return true
		}
	}
	return false
}
