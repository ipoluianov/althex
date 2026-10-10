package doc

import (
	"bufio"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/ipoluianov/althex/core/source"
)

// The journal of a document is a directory with:
//
//	meta.json - the file of the document, for the list of journals to recover
//	add.bin   - the add buffer (addbuf.go)
//	log.bin   - the records of the edits, each with its checksum
//	wal.bin   - the writes of a save in place while it runs (save.go)
//
// A document closed in the usual way removes its journal; one left behind
// is of a session that crashed, and Recover replays it.
type journal struct {
	dir string
	log *os.File
	// known are the states logged since the last checkpoint: going to one of
	// them is a short record, to an older one a checkpoint
	known map[int]bool
	// err is the first write error; the journal stops then, the editing goes on
	err error
}

const (
	metaFile = "meta.json"
	addFile  = "add.bin"
	logFile  = "log.bin"
	walFile  = "wal.bin"
)

// Record types
const (
	recBuffer     = 'F' // idx, main, path, stat: a source
	recCheckpoint = 'C' // id, saved, pieces: the whole state
	recInsert     = 'I' // id, off, buf, bufOff, len
	recDelete     = 'D' // id, off, len
	recGoto       = 'G' // id
	recKeep       = 'K' // buf idx, spans: bytes of a source kept before writing it
	recPath       = 'P' // path: the document is saved under another name
)

type journalMeta struct {
	Path    string
	Created time.Time
}

func createJournal(base, path string) (*journal, *addBuffer, error) {
	var rnd [8]byte
	rand.Read(rnd[:])
	dir := filepath.Join(base, time.Now().Format("20060102-150405")+"-"+hex.EncodeToString(rnd[:]))
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, nil, err
	}
	j := &journal{dir: dir, known: make(map[int]bool)}
	if err := j.writeMeta(path); err != nil {
		os.RemoveAll(dir)
		return nil, nil, err
	}
	add, err := os.OpenFile(filepath.Join(dir, addFile), os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		os.RemoveAll(dir)
		return nil, nil, err
	}
	j.log, err = os.OpenFile(filepath.Join(dir, logFile), os.O_WRONLY|os.O_CREATE|os.O_TRUNC|os.O_APPEND, 0600)
	if err != nil {
		add.Close()
		os.RemoveAll(dir)
		return nil, nil, err
	}
	return j, newFileAddBuffer(add, 0), nil
}

func (j *journal) writeMeta(path string) error {
	bs, _ := json.Marshal(journalMeta{Path: path, Created: time.Now()})
	tmp := filepath.Join(j.dir, metaFile+".tmp")
	if err := os.WriteFile(tmp, bs, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(j.dir, metaFile))
}

// write appends a record: its length, the record, its CRC-32
func (j *journal) write(rec []byte) {
	if j == nil || j.err != nil {
		return
	}
	frame := binary.AppendUvarint(make([]byte, 0, len(rec)+14), uint64(len(rec)))
	frame = append(frame, rec...)
	frame = binary.LittleEndian.AppendUint32(frame, crc32.ChecksumIEEE(rec))
	if _, err := j.log.Write(frame); err != nil {
		j.err = err
	}
}

// sync makes the records durable
func (j *journal) sync() error {
	if j == nil {
		return nil
	}
	if j.err != nil {
		return j.err
	}
	return j.log.Sync()
}

func (j *journal) remove() {
	if j == nil {
		return
	}
	j.log.Close()
	os.RemoveAll(j.dir)
}

func appendInt(b []byte, v int64) []byte { return binary.AppendVarint(b, v) }
func appendString(b []byte, s string) []byte {
	return append(binary.AppendUvarint(b, uint64(len(s))), s...)
}
func appendStat(b []byte, st source.Stat) []byte {
	b = appendInt(b, st.Size)
	b = appendInt(b, st.ModTime.UnixNano())
	b = binary.AppendUvarint(b, st.Dev)
	return binary.AppendUvarint(b, st.Ino)
}

func appendBool(b []byte, v bool) []byte {
	if v {
		return append(b, 1)
	}
	return append(b, 0)
}

func (j *journal) logBuffer(idx int, main bool, path string, st source.Stat) {
	if j == nil {
		return
	}
	rec := []byte{recBuffer}
	rec = appendInt(rec, int64(idx))
	rec = appendBool(rec, main)
	rec = appendString(rec, path)
	rec = appendStat(rec, st)
	j.write(rec)
}

func (j *journal) logCheckpoint(n *HistNode, saved bool) {
	if j == nil {
		return
	}
	ps := listPieces(n.root)
	rec := []byte{recCheckpoint}
	rec = appendInt(rec, int64(n.ID))
	rec = appendBool(rec, saved)
	rec = appendInt(rec, int64(len(ps)))
	for _, p := range ps {
		rec = appendInt(rec, int64(p.buf))
		rec = appendInt(rec, p.off)
		rec = appendInt(rec, p.len)
	}
	j.write(rec)
	clear(j.known)
	j.known[n.ID] = true
}

func (j *journal) logInsert(id int, off int64, p piece) {
	if j == nil {
		return
	}
	rec := []byte{recInsert}
	rec = appendInt(rec, int64(id))
	rec = appendInt(rec, off)
	rec = appendInt(rec, int64(p.buf))
	rec = appendInt(rec, p.off)
	rec = appendInt(rec, p.len)
	j.write(rec)
	j.known[id] = true
}

func (j *journal) logDelete(id int, off, n int64) {
	if j == nil {
		return
	}
	rec := []byte{recDelete}
	rec = appendInt(rec, int64(id))
	rec = appendInt(rec, off)
	rec = appendInt(rec, n)
	j.write(rec)
	j.known[id] = true
}

func (j *journal) logGoto(n *HistNode, saved bool) {
	if j == nil {
		return
	}
	if !j.known[n.ID] {
		j.logCheckpoint(n, saved)
		return
	}
	j.write(appendInt([]byte{recGoto}, int64(n.ID)))
}

func (j *journal) logKeep(idx int, spans []span) {
	if j == nil || len(spans) == 0 {
		return
	}
	rec := []byte{recKeep}
	rec = appendInt(rec, int64(idx))
	rec = appendInt(rec, int64(len(spans)))
	for _, s := range spans {
		rec = appendInt(rec, s.off)
		rec = appendInt(rec, s.len)
		rec = appendInt(rec, s.addOff)
	}
	j.write(rec)
}

func (j *journal) logPath(path string) {
	if j == nil {
		return
	}
	j.write(appendString([]byte{recPath}, path))
	j.writeMeta(path)
}

// journalCheckpoint logs the whole current state, e.g. after opening
func (d *Document) journalCheckpoint() error {
	if d.journal == nil {
		return nil
	}
	d.journal.logCheckpoint(d.hist.cur, d.hist.cur == d.hist.saved)
	return d.journal.err
}

// Sync makes the journal durable: after it a power loss keeps the edits.
// Writing goes to the system at once anyway, so a crash of the application
// alone loses nothing; call Sync now and then, e.g. when the user pauses.
func (d *Document) Sync() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.journal == nil {
		return nil
	}
	if err := d.add.sync(); err != nil {
		return err
	}
	return d.journal.sync()
}

// recordReader reads the records of a log; it stops at the first one that
// is cut or damaged (the moment of the crash)
type recordReader struct {
	r *bufio.Reader
}

func (rr *recordReader) next() ([]byte, bool) {
	n, err := binary.ReadUvarint(rr.r)
	if err != nil || n == 0 || n > 1<<30 {
		return nil, false
	}
	rec := make([]byte, n+4)
	if _, err := io.ReadFull(rr.r, rec); err != nil {
		return nil, false
	}
	sum := binary.LittleEndian.Uint32(rec[n:])
	rec = rec[:n]
	if crc32.ChecksumIEEE(rec) != sum {
		return nil, false
	}
	return rec, true
}

// fields reads the fields of a record
type fields struct {
	b   []byte
	err error
}

var errBadRecord = errors.New("damaged journal record")

func (f *fields) int() int64 {
	v, n := binary.Varint(f.b)
	if n <= 0 {
		f.err = errBadRecord
		f.b = nil
		return 0
	}
	f.b = f.b[n:]
	return v
}

func (f *fields) uint() uint64 {
	v, n := binary.Uvarint(f.b)
	if n <= 0 {
		f.err = errBadRecord
		f.b = nil
		return 0
	}
	f.b = f.b[n:]
	return v
}

func (f *fields) stat() source.Stat {
	st := source.Stat{Size: f.int(), ModTime: time.Unix(0, f.int())}
	st.Dev = f.uint()
	st.Ino = f.uint()
	return st
}

func (f *fields) bool() bool {
	if len(f.b) == 0 {
		f.err = errBadRecord
		return false
	}
	v := f.b[0] != 0
	f.b = f.b[1:]
	return v
}

func (f *fields) string() string {
	n, k := binary.Uvarint(f.b)
	if k <= 0 || uint64(len(f.b)-k) < n {
		f.err = errBadRecord
		f.b = nil
		return ""
	}
	s := string(f.b[k : k+int(n)])
	f.b = f.b[k+int(n):]
	return s
}
