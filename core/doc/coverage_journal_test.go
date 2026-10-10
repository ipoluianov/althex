package doc

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCoverageFieldsDamaged(t *testing.T) {
	f := &fields{b: []byte{0x80}} // a varint cut short
	if f.int() != 0 || f.err != errBadRecord {
		t.Fatal("int of a cut varint")
	}
	f = &fields{b: []byte{0x80}}
	if f.uint() != 0 || f.err != errBadRecord {
		t.Fatal("uint of a cut varint")
	}
	f = &fields{}
	if f.bool() || f.err != errBadRecord {
		t.Fatal("bool of nothing")
	}
	f = &fields{b: []byte{5, 'a', 'b'}} // a string longer than the record
	if f.string() != "" || f.err != errBadRecord {
		t.Fatal("string past the end")
	}
	// Good fields read back what was written
	var b []byte
	b = appendInt(b, -42)
	b = binary.AppendUvarint(b, 7)
	b = appendBool(b, true)
	b = appendString(b, "path")
	f = &fields{b: b}
	if f.int() != -42 || f.uint() != 7 || !f.bool() || f.string() != "path" || f.err != nil || len(f.b) != 0 {
		t.Fatal("fields read back")
	}
}

func TestCoverageRecordReaderStops(t *testing.T) {
	frame := func(rec []byte) []byte {
		b := binary.AppendUvarint(nil, uint64(len(rec)))
		b = append(b, rec...)
		return binary.LittleEndian.AppendUint32(b, crc32IEEE(rec))
	}
	good := frame([]byte("Ggood"))
	bad := frame([]byte("Gbad!"))
	bad[len(bad)-1] ^= 0xFF
	for name, tc := range map[string]struct {
		data []byte
		want int
	}{
		"empty":     {nil, 0},
		"good":      {append(bytes.Clone(good), good...), 2},
		"zero":      {append(bytes.Clone(good), 0), 1},
		"cut":       {append(bytes.Clone(good), good[:len(good)-2]...), 1},
		"checksum":  {append(append(bytes.Clone(good), bad...), good...), 1},
		"too large": {binary.AppendUvarint(nil, 1<<31), 0},
	} {
		rr := &recordReader{r: bufioReader(tc.data)}
		n := 0
		for {
			if _, ok := rr.next(); !ok {
				break
			}
			n++
		}
		if n != tc.want {
			t.Errorf("%s: %d records, want %d", name, n, tc.want)
		}
	}
}

func TestCoverageFindJournals(t *testing.T) {
	dir := t.TempDir()
	if list, err := FindJournals(filepath.Join(dir, "missing")); list != nil || err != nil {
		t.Fatalf("missing dir: %v %v", list, err)
	}
	file := filepath.Join(dir, "file")
	os.WriteFile(file, nil, 0644)
	if _, err := FindJournals(file); err == nil {
		t.Fatal("journals of a file")
	}
	j := filepath.Join(dir, "j")
	os.MkdirAll(filepath.Join(j, "no-meta"), 0700)
	os.MkdirAll(filepath.Join(j, "bad-meta"), 0700)
	os.WriteFile(filepath.Join(j, "bad-meta", metaFile), []byte("{"), 0600)
	os.WriteFile(filepath.Join(j, "stray"), nil, 0600)

	// Two documents with edits: the one edited last is first
	p1 := filepath.Join(dir, "one")
	p2 := filepath.Join(dir, "two")
	os.WriteFile(p1, []byte("1111"), 0644)
	os.WriteFile(p2, []byte("2222"), 0644)
	d1, _ := Open(p1, Options{JournalDir: j})
	d2, _ := Open(p2, Options{JournalDir: j})
	d1.Insert(0, []byte("a"), EditOptions{})
	d2.Insert(0, []byte("b"), EditOptions{})
	crash(d1)
	crash(d2)
	old := time.Now().Add(-time.Hour)
	os.Chtimes(filepath.Join(d1.journal.dir, logFile), old, old)

	list, err := FindJournals(j)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].Path != p2 || list[1].Path != p1 {
		t.Fatalf("journals %+v", list)
	}
	// A journal without its log is listed by its meta
	os.Remove(filepath.Join(d1.journal.dir, logFile))
	list, _ = FindJournals(j)
	if len(list) != 2 {
		t.Fatalf("journals without a log: %d", len(list))
	}
	var noLog JournalInfo
	for _, l := range list {
		if l.Path == p1 {
			noLog = l
		}
	}
	if !noLog.Modified.Equal(noLog.Created) {
		t.Fatal("a journal without a log has a modified time")
	}
	if _, err := Recover(noLog, Options{}); err == nil {
		t.Fatal("recovered without a log")
	}
	if err := DiscardJournal(noLog); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(noLog.Dir); !os.IsNotExist(err) {
		t.Fatal("a discarded journal stays")
	}
}

func TestCoverageRecoverErrors(t *testing.T) {
	setup := func(t *testing.T) (JournalInfo, string) {
		dir := t.TempDir()
		j := filepath.Join(dir, "j")
		path := writeFile(t, dir, []byte("0123456789"))
		d, err := Open(path, Options{JournalDir: j})
		if err != nil {
			t.Fatal(err)
		}
		d.Insert(0, []byte("x"), EditOptions{})
		crash(d)
		list, _ := FindJournals(j)
		if len(list) != 1 {
			t.Fatalf("journals %d", len(list))
		}
		return list[0], path
	}
	t.Run("no add buffer", func(t *testing.T) {
		info, _ := setup(t)
		os.Remove(filepath.Join(info.Dir, addFile))
		if _, err := Recover(info, Options{}); err == nil {
			t.Fatal("recovered without the add buffer")
		}
	})
	t.Run("file removed", func(t *testing.T) {
		info, path := setup(t)
		os.Remove(path)
		if _, err := Recover(info, Options{}); err == nil {
			t.Fatal("recovered without the file")
		}
	})
	t.Run("no state", func(t *testing.T) {
		info, _ := setup(t)
		// Only an edit, no checkpoint before it: a damaged journal
		rec := []byte{recInsert}
		rec = appendInt(rec, 1)
		rec = appendInt(rec, 0)
		rec = appendInt(rec, 0)
		rec = appendInt(rec, 0)
		rec = appendInt(rec, 1)
		writeLog(t, info, rec)
		if _, err := Recover(info, Options{}); !errors.Is(err, errBadRecord) {
			t.Fatalf("an edit without a state: %v", err)
		}
		writeLog(t, info, append([]byte{recGoto}, 2))
		if _, err := Recover(info, Options{}); err == nil {
			t.Fatal("recovered a journal without a state")
		}
	})
	t.Run("damaged fields", func(t *testing.T) {
		info, _ := setup(t)
		for _, rec := range [][]byte{
			{recBuffer, 0x80},
			{recCheckpoint, 2, 0, 2, 0x80},
			{recKeep, 6, 2, 0x80},
		} {
			writeLog(t, info, rec)
			if _, err := Recover(info, Options{}); !errors.Is(err, errBadRecord) {
				t.Fatalf("record %q: %v", rec[0], err)
			}
		}
	})
	t.Run("read-only", func(t *testing.T) {
		info, _ := setup(t)
		d, err := Recover(info, Options{ReadOnly: true})
		if err != nil {
			t.Fatal(err)
		}
		defer d.Close()
		if !d.ReadOnly() || d.Insert(0, []byte("y"), EditOptions{}) != ErrReadOnly {
			t.Fatal("a read-only recovery can be edited")
		}
		if got := string(readAll(t, d)); got != "x0123456789" {
			t.Fatalf("got %q", got)
		}
	})
	t.Run("incomplete save log", func(t *testing.T) {
		info, path := setup(t)
		// A log of a save cut while it was written: the file was not touched
		os.WriteFile(filepath.Join(info.Dir, walFile), []byte(walMagic+"\x00\x00garbage"), 0600)
		d, err := Recover(info, Options{})
		if err != nil {
			t.Fatal(err)
		}
		defer d.Close()
		if bs, _ := os.ReadFile(path); string(bs) != "0123456789" {
			t.Fatalf("an incomplete log changed the file: %q", bs)
		}
		if _, err := os.Stat(filepath.Join(info.Dir, walFile)); !os.IsNotExist(err) {
			t.Fatal("the incomplete log stays")
		}
	})
}

func TestCoverageReplayWALErrors(t *testing.T) {
	dir := t.TempDir()
	wal := filepath.Join(dir, "wal")
	if _, ok, err := replayWAL(wal, filepath.Join(dir, "f")); ok || err != nil {
		t.Fatal("a missing log")
	}
	mk := func(body []byte) {
		os.WriteFile(wal, binary.LittleEndian.AppendUint32(bytes.Clone(body), crc32IEEE(body)), 0600)
	}
	var body []byte
	body = append(body, walMagic...)
	body = appendInt(body, 3)  // id
	body = appendInt(body, 4)  // new size
	body = appendInt(body, 1)  // writes
	body = appendInt(body, 1)  // off
	body = appendInt(body, 10) // len: more than there is
	body = append(body, "ab"...)
	mk(body)
	if _, _, err := replayWAL(wal, filepath.Join(dir, "missing")); err == nil {
		t.Fatal("replayed onto a missing file")
	}
	path := writeFile(t, dir, []byte("0123456789"))
	if _, ok, err := replayWAL(wal, path); ok || err != errBadRecord {
		t.Fatalf("a write past the log: %v %v", ok, err)
	}
	// A complete one writes and truncates
	body = body[:len(body)-3]
	body = appendInt(body, 2)
	body = append(body, "ab"...)
	mk(body)
	id, ok, err := replayWAL(wal, path)
	if err != nil || !ok || id != 3 {
		t.Fatalf("replay: %d %v %v", id, ok, err)
	}
	if bs, _ := os.ReadFile(path); string(bs) != "0ab3" {
		t.Fatalf("replayed %q", bs)
	}
	// Another magic is not a log of a save
	bad := bytes.Clone(body)
	bad[0] = 'X'
	mk(bad)
	if _, ok, _ := replayWAL(wal, path); ok {
		t.Fatal("replayed a log with another magic")
	}
}

// A state that reads a backup removed since is lost on recovery; the others stay
func TestCoverageRecoverLostBackup(t *testing.T) {
	dir := t.TempDir()
	j := filepath.Join(dir, "j")
	path := writeFile(t, dir, []byte("0123456789"))
	d, err := Open(path, Options{JournalDir: j})
	if err != nil {
		t.Fatal(err)
	}
	d.Insert(0, []byte(">"), EditOptions{})
	if err := d.Save(SaveOptions{Backup: true}); err != nil {
		t.Fatal(err)
	}
	// Back to the state before the save: it reads the backup
	d.Undo()
	// A state of the add buffer alone, on top of it
	d.Delete(0, d.Size(), EditOptions{})
	d.Insert(0, []byte("new"), EditOptions{})
	d.Sync()
	crash(d)
	os.Remove(path + ".bak")

	r := recoverOne(t, j)
	defer r.Close()
	if got := string(readAll(t, r)); got != "new" {
		t.Fatalf("recovered %q", got)
	}
	// The undo chain reaches the lost state and stops there
	r.Undo()
	if r.Size() != 0 {
		t.Fatalf("after one undo: %d bytes", r.Size())
	}
	if r.CanUndo() {
		t.Fatal("undo into a lost state")
	}
	if _, err := r.Undo(); err != ErrNothingToDo {
		t.Fatalf("undo into a lost state: %v", err)
	}
	rootR, _ := r.History()
	if !rootR.Lost {
		t.Fatal("the state of the removed backup is not lost")
	}
	if err := r.Goto(rootR.ID); err != ErrSourceLost {
		t.Fatalf("goto a lost state: %v", err)
	}
	// Its bytes read as zeros with the error
	snap := &Snapshot{root: rootR.root, bufs: r.bufs}
	buf := []byte{1, 2, 3}
	if _, err := snap.ReadAt(buf, 0); !errors.Is(err, ErrSourceLost) || !bytes.Equal(buf, []byte{0, 0, 0}) {
		t.Fatalf("read of a lost state: %v %v", err, buf)
	}
	if _, ok := snap.TryReadAt(buf, 0, nil); !ok {
		t.Fatal("try read of a lost state waits")
	}
}

// Recovering when the current state reads the removed backup fails
func TestCoverageRecoverCurrentLost(t *testing.T) {
	dir := t.TempDir()
	j := filepath.Join(dir, "j")
	path := writeFile(t, dir, []byte("0123456789"))
	d, _ := Open(path, Options{JournalDir: j})
	d.Insert(0, []byte(">"), EditOptions{})
	d.Save(SaveOptions{Backup: true})
	d.Undo()
	d.Overwrite(5, []byte("!"), EditOptions{})
	d.Sync()
	crash(d)
	os.Remove(path + ".bak")
	list, _ := FindJournals(j)
	if len(list) != 1 {
		t.Fatalf("journals %d", len(list))
	}
	if _, err := Recover(list[0], Options{}); err != ErrSourceLost {
		t.Fatalf("recover: %v", err)
	}
}

func crc32IEEE(b []byte) uint32 { return crc32.ChecksumIEEE(b) }

func bufioReader(b []byte) *bufio.Reader { return bufio.NewReader(bytes.NewReader(b)) }

// writeLog makes the log of the journal the one record
func writeLog(t *testing.T, info JournalInfo, rec []byte) {
	t.Helper()
	b := binary.AppendUvarint(nil, uint64(len(rec)))
	b = append(b, rec...)
	b = binary.LittleEndian.AppendUint32(b, crc32IEEE(rec))
	if err := os.WriteFile(filepath.Join(info.Dir, logFile), b, 0600); err != nil {
		t.Fatal(err)
	}
}

// A journal whose last state is the saved one has nothing to recover, though
// it keeps the edits before the save; undone past the save, it has
func TestJournalSavedNotOffered(t *testing.T) {
	for _, tc := range []struct {
		name  string
		after func(d *Document)
		want  int
	}{
		{"saved", func(d *Document) {}, 0},
		{"undone past the save", func(d *Document) { d.Undo() }, 1},
		{"edited, undone to the save", func(d *Document) {
			d.Overwrite(5, []byte("!"), EditOptions{})
			d.Undo()
		}, 0},
		{"edited after the save", func(d *Document) { d.Overwrite(5, []byte("!"), EditOptions{}) }, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			j := filepath.Join(dir, "j")
			path := writeFile(t, dir, []byte("0123456789"))
			d, err := Open(path, Options{JournalDir: j})
			if err != nil {
				t.Fatal(err)
			}
			d.Overwrite(0, []byte("AB"), EditOptions{})
			if err := d.Save(SaveOptions{}); err != nil {
				t.Fatal(err)
			}
			tc.after(d)
			d.Sync()
			crash(d)
			list, err := FindJournals(j)
			if err != nil || len(list) != tc.want {
				t.Fatalf("journals %d (%v), want %d", len(list), err, tc.want)
			}
		})
	}
}
