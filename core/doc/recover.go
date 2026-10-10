package doc

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/ipoluianov/althex/core/source"
)

// JournalInfo is a journal left by a session that crashed
type JournalInfo struct {
	Dir  string
	Path string // the file of the document; "" - a new document
	// Created is when the document was opened, Modified the last edit
	Created  time.Time
	Modified time.Time
}

var (
	// ErrSourceChanged: the file was changed since the journal was written,
	// so the edits cannot be put on top of it
	ErrSourceChanged = errors.New("the file was changed after the edits were made")
	// ErrSourceLost: the state needs the bytes of a file replaced by saving
	// without a backup
	ErrSourceLost = errors.New("the file the edits were made on was replaced")
)

// FindJournals lists the journals in the directory, the newest first.
// Call it before any document is opened: the journals of open documents
// are there too.
func FindJournals(base string) ([]JournalInfo, error) {
	entries, err := os.ReadDir(base)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var list []JournalInfo
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(base, e.Name())
		var meta journalMeta
		bs, err := os.ReadFile(filepath.Join(dir, metaFile))
		if err != nil || json.Unmarshal(bs, &meta) != nil {
			continue
		}
		info := JournalInfo{Dir: dir, Path: meta.Path, Created: meta.Created, Modified: meta.Created}
		if st, err := os.Stat(filepath.Join(dir, logFile)); err == nil {
			info.Modified = st.ModTime()
			// Only opened, never edited: nothing to recover
			if !hasEdits(filepath.Join(dir, logFile)) {
				os.RemoveAll(dir)
				continue
			}
		}
		list = append(list, info)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Modified.After(list[j].Modified) })
	return list, nil
}

// hasEdits tells whether the log ends in a state that is not saved. A save
// writes a checkpoint of the saved state and keeps the history before it, so
// edits that were saved, or undone back to the save, leave nothing to recover.
func hasEdits(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	rr := &recordReader{r: bufio.NewReader(f)}
	cur, saved := -1, -1
	for {
		rec, ok := rr.next()
		if !ok {
			return cur != saved
		}
		fl := &fields{b: rec[1:]}
		switch rec[0] {
		case recCheckpoint:
			cur, saved = int(fl.int()), -1
			if fl.bool() {
				saved = cur
			}
		case recInsert, recDelete, recGoto:
			cur = int(fl.int())
		}
		if fl.err != nil {
			return true // damaged: offered rather than lost
		}
	}
}

// DiscardJournal removes the journal: its edits are lost
func DiscardJournal(info JournalInfo) error {
	return os.RemoveAll(info.Dir)
}

// missingBuffer stands for a source that cannot be opened any more; only
// older states of the history refer to such
type missingBuffer struct{ err error }

func (b missingBuffer) ReadAt(p []byte, off int64) (int, error) {
	clear(p)
	return 0, b.err
}

func (b missingBuffer) tryReadAt(p []byte, off int64, onReady func()) bool {
	clear(p)
	return true
}

// Recover opens the document of the journal with the edits it has. The
// history starts at the last checkpoint (opening or saving). A save in
// place that was cut by the crash is finished first. The journal goes on
// with the document.
func Recover(info JournalInfo, opts Options) (*Document, error) {
	f, err := os.Open(filepath.Join(info.Dir, logFile))
	if err != nil {
		return nil, err
	}
	var recs [][]byte
	rr := &recordReader{r: bufio.NewReader(f)}
	for {
		rec, ok := rr.next()
		if !ok {
			break
		}
		recs = append(recs, rec)
	}
	f.Close()

	addF, err := os.OpenFile(filepath.Join(info.Dir, addFile), os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	st, err := addF.Stat()
	if err != nil {
		addF.Close()
		return nil, err
	}
	d := &Document{path: info.Path, readOnly: opts.ReadOnly}
	d.add = newFileAddBuffer(addF, st.Size())
	d.bufs = []buffer{d.add, zeroBuffer{}}
	r := &replay{d: d, stats: make(map[int]source.Stat), files: make(map[int]*source.File), known: make(map[int]bool)}
	for _, rec := range recs {
		if err := r.apply(rec); err != nil {
			r.close()
			return nil, err
		}
	}
	if d.hist == nil {
		r.close()
		return nil, errors.New("the journal has no state to recover")
	}

	// A save in place cut by the crash: the log of its writes is complete,
	// so they are made again and that state is the one in the file
	walPath := filepath.Join(info.Dir, walFile)
	if _, err := os.Stat(walPath); err == nil && d.file != nil {
		id, ok, err := replayWAL(walPath, d.path)
		if err != nil {
			r.close()
			return nil, fmt.Errorf("finishing the save of %s: %w", d.path, err)
		}
		if ok {
			if n, found := d.hist.nodes[id]; found {
				d.hist.saved = n
			}
			old := r.stats[d.srcIdx]
			d.file.Refresh()
			st := d.file.Stat()
			for i, s := range r.stats {
				if s.Dev == old.Dev && s.Ino == old.Ino {
					r.stats[i] = st
				}
			}
			for i := range d.bufs {
				if b, ok := d.bufs[i].(*srcBuffer); ok {
					b.cache.Load().InvalidateAll()
				}
			}
		}
		os.Remove(walPath)
	}

	// The file must be as the journal last saw it
	if d.file != nil {
		now, err := source.StatFile(d.path)
		if err != nil || !now.Same(r.stats[d.srcIdx]) {
			r.close()
			return nil, fmt.Errorf("%s: %w", d.path, ErrSourceChanged)
		}
	}

	// The other sources (older states) must be as they were too
	for idx, f := range r.files {
		if idx == d.srcIdx && d.file != nil {
			continue
		}
		now, err := f.StatNow()
		if err != nil || !now.Same(r.stats[idx]) {
			r.setBuffer(idx, missingBuffer{ErrSourceLost})
		}
	}
	for _, n := range d.hist.nodes {
		walk(n.root, 0, 0, n.root.bytes(), func(_ int64, p piece) bool {
			if _, ok := d.bufs[p.buf].(missingBuffer); ok {
				n.Lost = true
				return false
			}
			return true
		})
	}
	if d.hist.cur.Lost {
		r.close()
		return nil, ErrSourceLost
	}

	log, err := os.OpenFile(filepath.Join(info.Dir, logFile), os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		r.close()
		return nil, err
	}
	// Records after a damaged one would not be read: the log goes on from
	// a checkpoint of the recovered state
	d.journal = &journal{dir: info.Dir, log: log, known: r.known}
	d.journal.logCheckpoint(d.hist.cur, d.hist.cur == d.hist.saved)
	d.journal.sync()
	d.publish()
	return d, nil
}

// replay rebuilds a document from the records of its journal
type replay struct {
	d     *Document
	stats map[int]source.Stat // what each source file was last seen as
	files map[int]*source.File
	known map[int]bool // states since the last checkpoint
}

func (r *replay) close() {
	if r.d.file != nil {
		r.d.file.Close()
	}
	for _, f := range r.d.oldFiles {
		f.Close()
	}
	r.d.add.close()
}

func (r *replay) setBuffer(idx int, b buffer) {
	for len(r.d.bufs) <= idx {
		r.d.bufs = append(r.d.bufs, missingBuffer{errors.New("no such source")})
	}
	r.d.bufs[idx] = b
}

func (r *replay) apply(rec []byte) error {
	d := r.d
	fl := &fields{b: rec[1:]}
	switch rec[0] {
	case recBuffer:
		idx := int(fl.int())
		main := fl.bool()
		path := fl.string()
		st := fl.stat()
		if fl.err != nil {
			return fl.err
		}
		r.stats[idx] = st
		if b, ok := r.d.bufs[min(idx, len(r.d.bufs)-1)].(*srcBuffer); ok && idx < len(r.d.bufs) && !main {
			// The same source under another name (a backup): only the file changes
			f, err := source.OpenFile(path)
			if err != nil {
				r.setBuffer(idx, missingBuffer{err})
				return nil
			}
			d.oldFiles = append(d.oldFiles, f)
			r.files[idx] = f
			b.file = f
			b.cache.Store(source.NewCache(f, source.DefaultCachePages))
			return nil
		}
		f, err := source.OpenFile(path)
		if err != nil {
			if main {
				return err
			}
			r.setBuffer(idx, missingBuffer{err})
			return nil
		}
		if main {
			if d.file != nil {
				d.oldFiles = append(d.oldFiles, d.file)
				// Saved in place: the file is the same, so its older versions
				// are valid as long as this one is (their bytes are kept)
				if old := r.stats[d.srcIdx]; old.Dev == st.Dev && old.Ino == st.Ino {
					for i, s := range r.stats {
						if s.Dev == st.Dev && s.Ino == st.Ino {
							r.stats[i] = st
						}
					}
				}
			}
			d.file = f
			d.path = path
			d.src = newSrcBuffer(f, nil, d.add)
			d.srcIdx = idx
			r.setBuffer(idx, d.src)
		} else {
			d.oldFiles = append(d.oldFiles, f)
			r.setBuffer(idx, newSrcBuffer(f, nil, d.add))
		}
		r.files[idx] = f

	case recCheckpoint:
		id := int(fl.int())
		saved := fl.bool()
		count := fl.int()
		ps := make([]piece, 0, max(count, 0))
		for i := int64(0); i < count && fl.err == nil; i++ {
			ps = append(ps, piece{int(fl.int()), fl.int(), fl.int()})
		}
		if fl.err != nil {
			return fl.err
		}
		next := 0
		if d.hist != nil {
			next = d.hist.nextID
		}
		d.hist = newHistory(buildTree(ps), id)
		d.hist.nextID = max(d.hist.nextID, next)
		if !saved {
			d.hist.saved = nil
		}
		clear(r.known)
		r.known[id] = true

	case recInsert, recDelete:
		if d.hist == nil {
			return errBadRecord
		}
		id := int(fl.int())
		off := fl.int()
		var p piece
		var n int64
		if rec[0] == recInsert {
			p = piece{int(fl.int()), fl.int(), fl.int()}
		} else {
			n = fl.int()
		}
		if fl.err != nil {
			return fl.err
		}
		h := d.hist
		node := h.cur
		if node.ID != id {
			node = h.add(h.cur, id, "recovered", h.cur.root)
			h.cur = node
		}
		if rec[0] == recInsert {
			node.root = insertPiece(node.root, off, p)
			node.Off, node.Len = off, p.len
		} else {
			node.root = deleteRange(node.root, off, n)
			node.Off, node.Len = off, 0
		}
		r.known[id] = true

	case recGoto:
		id := int(fl.int())
		if d.hist != nil {
			if n, ok := d.hist.nodes[id]; ok {
				d.hist.cur = n
			}
		}

	case recKeep:
		idx := int(fl.int())
		count := fl.int()
		var spans []span
		for i := int64(0); i < count && fl.err == nil; i++ {
			spans = append(spans, span{fl.int(), fl.int(), fl.int()})
		}
		if fl.err != nil {
			return fl.err
		}
		if idx < len(d.bufs) {
			if b, ok := d.bufs[idx].(*srcBuffer); ok {
				b.addSpans(spans)
			}
		}

	case recPath:
		d.path = fl.string()
	}
	return fl.err
}
