package doc

import (
	"bytes"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"path/filepath"
	"testing"
)

// readAll returns all the bytes of the document
func readAll(t *testing.T, d *Document) []byte {
	t.Helper()
	snap := d.Snapshot()
	buf := make([]byte, snap.Size())
	n, err := snap.ReadAt(buf, 0)
	if err != nil && err != io.EOF {
		t.Fatalf("read: %v", err)
	}
	if n != len(buf) {
		t.Fatalf("read %d of %d", n, len(buf))
	}
	return buf
}

func writeFile(t *testing.T, dir string, data []byte) string {
	t.Helper()
	path := filepath.Join(dir, "file.bin")
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

func randomBytes(r *rand.Rand, n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(r.UintN(256))
	}
	return b
}

// crash leaves the document as a crash would: the files are closed, the
// journal stays
func crash(d *Document) {
	if d.file != nil {
		d.file.Close()
	}
	for _, f := range d.oldFiles {
		f.Close()
	}
	if d.journal != nil {
		d.journal.log.Close()
	}
	d.add.close()
}

func recoverOne(t *testing.T, journals string) *Document {
	t.Helper()
	list, err := FindJournals(journals)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("journals: %d, want 1", len(list))
	}
	d, err := Recover(list[0], Options{})
	if err != nil {
		t.Fatalf("recover: %v", err)
	}
	return d
}

func TestEditsAndUndo(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, []byte("0123456789"))
	d, err := Open(path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	steps := []struct {
		do   func() error
		want string
	}{
		{func() error { return d.Insert(5, []byte("abc"), EditOptions{}) }, "01234abc56789"},
		{func() error { return d.Delete(0, 2, EditOptions{}) }, "234abc56789"},
		{func() error { return d.Overwrite(9, []byte("XYZ"), EditOptions{}) }, "234abc567XYZ"},
		{func() error { return d.InsertZeros(0, 2, EditOptions{}) }, "\x00\x00234abc567XYZ"},
	}
	for _, s := range steps {
		if err := s.do(); err != nil {
			t.Fatal(err)
		}
		if got := string(readAll(t, d)); got != s.want {
			t.Fatalf("got %q, want %q", got, s.want)
		}
	}
	for i := len(steps) - 2; i >= -1; i-- {
		if _, err := d.Undo(); err != nil {
			t.Fatal(err)
		}
		want := "0123456789"
		if i >= 0 {
			want = steps[i].want
		}
		if got := string(readAll(t, d)); got != want {
			t.Fatalf("undo: got %q, want %q", got, want)
		}
	}
	if d.Modified() {
		t.Fatal("modified after undoing all")
	}
	if _, err := d.Undo(); err != ErrNothingToDo {
		t.Fatalf("undo past the start: %v", err)
	}
	if _, err := d.Redo(); err != nil {
		t.Fatal(err)
	}
	if got := string(readAll(t, d)); got != steps[0].want {
		t.Fatalf("redo: got %q", got)
	}
}

func TestTypingIsOneUndo(t *testing.T) {
	d, err := New(0, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	typing := EditOptions{Kind: KindTyping, Typing: true}
	for i, c := range []byte("hello") {
		if err := d.Insert(int64(i), []byte{c}, typing); err != nil {
			t.Fatal(err)
		}
	}
	if p := d.Snapshot().Pieces(); p != 1 {
		t.Fatalf("typed bytes make %d pieces, want 1", p)
	}
	d.Insert(5, []byte("!"), EditOptions{})
	d.Undo()
	if got := string(readAll(t, d)); got != "hello" {
		t.Fatalf("got %q", got)
	}
	d.Undo()
	if got := string(readAll(t, d)); got != "" {
		t.Fatalf("typing is undone in parts: %q", got)
	}
}

func TestGroup(t *testing.T) {
	d, _ := FromBytes([]byte("aaaa"), Options{})
	defer d.Close()
	d.BeginGroup("replace all")
	for i := int64(0); i < 4; i++ {
		d.Overwrite(i, []byte("b"), EditOptions{})
	}
	d.EndGroup()
	if got := string(readAll(t, d)); got != "bbbb" {
		t.Fatalf("got %q", got)
	}
	d.Undo()
	if got := string(readAll(t, d)); got != "aaaa" {
		t.Fatalf("the group is undone in parts: %q", got)
	}
}

func TestReadOnly(t *testing.T) {
	d, _ := FromBytes([]byte("abc"), Options{ReadOnly: true})
	defer d.Close()
	if err := d.Insert(0, []byte("x"), EditOptions{}); err != ErrReadOnly {
		t.Fatalf("insert into a read-only document: %v", err)
	}
}

func TestSaveKeepsOtherFile(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, []byte("0123456789"))
	d, err := Open(path, Options{JournalDir: filepath.Join(dir, "j")})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	d.Overwrite(2, []byte("ab"), EditOptions{})
	if bs, _ := os.ReadFile(path); string(bs) != "0123456789" {
		t.Fatal("the file is changed before saving")
	}
	if err := d.Save(SaveOptions{}); err != nil {
		t.Fatal(err)
	}
	if bs, _ := os.ReadFile(path); string(bs) != "01ab456789" {
		t.Fatalf("saved in place: %q", bs)
	}
	// Undo after saving in place reads the bytes the file had
	d.Undo()
	if got := string(readAll(t, d)); got != "0123456789" {
		t.Fatalf("undo after saving: %q", got)
	}
	d.Insert(0, []byte(">"), EditOptions{})
	if err := d.Save(SaveOptions{Backup: true}); err != nil {
		t.Fatal(err)
	}
	if bs, _ := os.ReadFile(path); string(bs) != ">0123456789" {
		t.Fatalf("saved with an insert: %q", bs)
	}
	if bs, _ := os.ReadFile(path + ".bak"); string(bs) != "01ab456789" {
		t.Fatalf("backup: %q", bs)
	}
	d.Undo()
	d.Redo()
	d.Undo()
	if got := string(readAll(t, d)); got != "0123456789" {
		t.Fatalf("undo after saving with an insert: %q", got)
	}
}

func TestRecoverAfterCrash(t *testing.T) {
	dir := t.TempDir()
	journals := filepath.Join(dir, "j")
	path := writeFile(t, dir, []byte("0123456789"))
	d, err := Open(path, Options{JournalDir: journals})
	if err != nil {
		t.Fatal(err)
	}
	d.Insert(3, []byte("abc"), EditOptions{})
	d.Delete(0, 1, EditOptions{})
	d.Undo()
	d.Insert(0, []byte("!"), EditOptions{})
	want := string(readAll(t, d))
	crash(d)

	r := recoverOne(t, journals)
	defer r.Close()
	if got := string(readAll(t, r)); got != want {
		t.Fatalf("recovered %q, want %q", got, want)
	}
	if !r.Modified() {
		t.Fatal("the recovered document is not modified")
	}
	r.Undo()
	r.Undo()
	if got := string(readAll(t, r)); got != "0123456789" {
		t.Fatalf("undo in the recovered document: %q", got)
	}
}

func TestRecoverRefusesChangedFile(t *testing.T) {
	dir := t.TempDir()
	journals := filepath.Join(dir, "j")
	path := writeFile(t, dir, []byte("0123456789"))
	d, _ := Open(path, Options{JournalDir: journals})
	d.Insert(0, []byte("x"), EditOptions{})
	crash(d)
	os.WriteFile(path, []byte("changed"), 0644)
	list, _ := FindJournals(journals)
	if _, err := Recover(list[0], Options{}); err == nil {
		t.Fatal("recovered over a changed file")
	}
}

func TestOpenedOnlyIsNotRecovered(t *testing.T) {
	dir := t.TempDir()
	journals := filepath.Join(dir, "j")
	d, _ := Open(writeFile(t, dir, []byte("abc")), Options{JournalDir: journals})
	crash(d)
	if list, _ := FindJournals(journals); len(list) != 0 {
		t.Fatalf("journals of documents without edits: %d", len(list))
	}
}

func TestCrashDuringSaveInPlace(t *testing.T) {
	for _, point := range []string{"afterWAL", "afterWrite"} {
		t.Run(point, func(t *testing.T) {
			dir := t.TempDir()
			journals := filepath.Join(dir, "j")
			r := rand.New(rand.NewPCG(1, 2))
			orig := randomBytes(r, 300_000)
			path := writeFile(t, dir, orig)
			d, err := Open(path, Options{JournalDir: journals})
			if err != nil {
				t.Fatal(err)
			}
			d.Overwrite(100, []byte("hello"), EditOptions{})
			d.Overwrite(200_000, randomBytes(r, 70_000), EditOptions{})
			d.Insert(300_000, []byte("tail"), EditOptions{})
			want := readAll(t, d)

			testCrash = point
			err = d.Save(SaveOptions{})
			testCrash = ""
			if err != errTestCrash {
				t.Fatalf("save: %v", err)
			}
			crash(d)

			rd := recoverOne(t, journals)
			defer rd.Close()
			if got := readAll(t, rd); !bytes.Equal(got, want) {
				t.Fatal("the recovered document differs")
			}
			bs, _ := os.ReadFile(path)
			if !bytes.Equal(bs, want) {
				t.Fatal("the save was not finished on recovery")
			}
			if rd.Modified() {
				t.Fatal("the finished save does not count as saved")
			}
			// The state before the save is still there
			rd.Undo()
			rd.Undo()
			rd.Undo()
			if got := readAll(t, rd); !bytes.Equal(got, orig) {
				t.Fatal("undo after the recovered save differs from the original")
			}
		})
	}
}

// TestRandomEdits compares the document with a byte slice through random
// edits, undo, redo, jumps in the history, saves and crashes (PERF-8 too:
// thousands of edits)
func TestRandomEdits(t *testing.T) {
	for seed := uint64(1); seed <= 12; seed++ {
		t.Run(fmt.Sprint(seed), func(t *testing.T) { randomEdits(t, seed, 600) })
	}
}

func randomEdits(t *testing.T, seed uint64, steps int) {
	r := rand.New(rand.NewPCG(seed, seed*7))
	dir := t.TempDir()
	journals := filepath.Join(dir, "j")
	size := []int{0, 10, 1000, 70_000, 200_000}[r.IntN(5)]
	model := randomBytes(r, size)
	path := writeFile(t, dir, model)
	d, err := Open(path, Options{JournalDir: journals})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { d.Close() }()

	// The bytes of each state of the history
	states := map[int][]byte{0: bytes.Clone(model)}
	ids := []int{0}
	remember := func() {
		_, cur := d.History()
		if _, ok := states[cur.ID]; !ok {
			ids = append(ids, cur.ID)
		}
		states[cur.ID] = bytes.Clone(model)
	}
	check := func(what string) {
		t.Helper()
		got := readAll(t, d)
		if !bytes.Equal(got, model) {
			t.Fatalf("after %s: the document (%d bytes) differs from the model (%d bytes)", what, len(got), len(model))
		}
		// A few random reads across pieces
		for i := 0; i < 3 && len(model) > 0; i++ {
			off := r.IntN(len(model))
			n := 1 + r.IntN(min(len(model)-off, 5000))
			buf := make([]byte, n)
			d.ReadAt(buf, int64(off))
			if !bytes.Equal(buf, model[off:off+n]) {
				t.Fatalf("after %s: read at %d differs", what, off)
			}
		}
	}
	syncModel := func() {
		_, cur := d.History()
		m, ok := states[cur.ID]
		if !ok {
			t.Fatalf("unknown state %d", cur.ID)
		}
		model = bytes.Clone(m)
	}

	edited := false // a journal without edits is not offered for recovery
	lost := 0
	defer func() {
		if testing.Verbose() {
			t.Logf("sources lost on recovery: %d", lost)
		}
	}()
	for step := 0; step < steps; step++ {
		var what string
		op := r.IntN(100)
		if os.Getenv("TRACE") != "" {
			t.Logf("step %d op %d size %d pieces %v", step, op, len(model), listPieces(d.hist.cur.root))
		}
		switch {
		case op < 25:
			off := r.IntN(len(model) + 1)
			data := randomBytes(r, 1+r.IntN(20))
			typing := r.IntN(2) == 0
			d.Insert(int64(off), data, EditOptions{Typing: typing, Kind: map[bool]string{true: KindTyping}[typing]})
			model = append(model[:off:off], append(data, model[off:]...)...)
			what = "insert"
		case op < 40:
			if len(model) == 0 {
				continue
			}
			off := r.IntN(len(model))
			n := 1 + r.IntN(min(len(model)-off, 3000))
			d.Delete(int64(off), int64(n), EditOptions{})
			model = append(model[:off:off], model[off+n:]...)
			what = "delete"
		case op < 55:
			off := r.IntN(len(model) + 1)
			data := randomBytes(r, 1+r.IntN(40))
			d.Overwrite(int64(off), data, EditOptions{})
			end := off + len(data)
			if end > len(model) {
				model = append(model, make([]byte, end-len(model))...)
			}
			copy(model[off:], data)
			what = "overwrite"
		case op < 60:
			off := r.IntN(len(model) + 1)
			n := r.IntN(5000)
			d.InsertZeros(int64(off), int64(n), EditOptions{})
			model = append(model[:off:off], append(make([]byte, n), model[off:]...)...)
			what = "zeros"
		case op < 72:
			d.Undo()
			syncModel()
			check("undo")
			continue
		case op < 80:
			d.Redo()
			syncModel()
			check("redo")
			continue
		case op < 85:
			id := ids[r.IntN(len(ids))]
			if err := d.Goto(id); err != nil {
				continue // dropped from the history by a save
			}
			syncModel()
			check("goto")
			continue
		case op < 92:
			backup := r.IntN(4) == 0
			if os.Getenv("TRACE") != "" {
				_, ip := d.inPlaceWrites()
				t.Logf("save backup=%v inPlace=%v srcIdx=%d", backup, ip, d.srcIdx)
			}
			if err := d.Save(SaveOptions{Backup: backup}); err != nil {
				t.Fatalf("save: %v", err)
			}
			if bs, _ := os.ReadFile(path); !bytes.Equal(bs, model) {
				t.Fatalf("the saved file differs from the model")
			}
			if d.Modified() {
				t.Fatal("modified after saving")
			}
			marks := make([]bool, len(model))
			d.Snapshot().Edited(0, marks)
			for i, m := range marks {
				if m {
					t.Fatalf("byte %d is marked edited after saving", i)
				}
			}
			// The history may be dropped by saving: keep the states it has
			for id := range states {
				if _, ok := d.hist.nodes[id]; !ok {
					delete(states, id)
				}
			}
			ids = ids[:0]
			for id := range states {
				ids = append(ids, id)
			}
			remember()
			check("save")
			continue
		default:
			if !edited {
				continue
			}
			d.Sync()
			crash(d)
			list, _ := FindJournals(journals)
			if len(list) != 1 {
				t.Fatalf("journals: %d, want 1", len(list))
			}
			rd, err := Recover(list[0], Options{})
			if err == ErrSourceLost {
				// The state is on a file replaced by saving without a
				// backup: start again from the file
				lost++
				DiscardJournal(list[0])
				if rd, err = Open(path, Options{JournalDir: journals}); err != nil {
					t.Fatal(err)
				}
				model, _ = os.ReadFile(path)
				d = rd
				states = map[int][]byte{0: bytes.Clone(model)}
				ids = []int{0}
				edited = false
				check("reopen")
				continue
			}
			if err != nil {
				t.Fatalf("recover: %v", err)
			}
			d = rd
			for id := range states {
				if _, ok := d.hist.nodes[id]; !ok {
					delete(states, id)
				}
			}
			ids = ids[:0]
			for id := range states {
				ids = append(ids, id)
			}
			check("recover")
			continue
		}
		edited = true
		remember()
		check(what)
	}
}

func TestEditedMarks(t *testing.T) {
	dir := t.TempDir()
	d, err := Open(writeFile(t, dir, []byte("0123456789")), Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	d.Overwrite(2, []byte("ab"), EditOptions{})
	d.Insert(8, []byte("x"), EditOptions{})
	marks := make([]bool, 11)
	d.Snapshot().Edited(0, marks)
	got := ""
	for _, m := range marks {
		got += map[bool]string{true: "E", false: "."}[m]
	}
	// After the insert the bytes 8.. are at other offsets than in the file
	if want := "..EE....EEE"; got != want {
		t.Fatalf("marks %s, want %s", got, want)
	}
}
