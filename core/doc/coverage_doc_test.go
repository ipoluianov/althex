package doc

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ipoluianov/althex/core/source"
)

// covBadSource is a source whose bytes in [badFrom, badTo) cannot be read (bad sectors)
type covBadSource struct {
	data           []byte
	badFrom, badTo int64
	reads          atomic.Int64
}

var covErrBadSector = errors.New("bad sector")

func (s *covBadSource) Size() int64  { return int64(len(s.data)) }
func (s *covBadSource) Close() error { return nil }
func (s *covBadSource) ReadAt(p []byte, off int64) (int, error) {
	s.reads.Add(1)
	if off >= int64(len(s.data)) {
		return 0, io.EOF
	}
	end := min(off+int64(len(p)), int64(len(s.data)))
	if off < s.badTo && end > s.badFrom {
		return 0, covErrBadSector
	}
	n := copy(p, s.data[off:end])
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

// covErrReader fails every read
type covErrReader struct{}

func (covErrReader) ReadAt(p []byte, off int64) (int, error) { return 0, covErrBadSector }

func covOpen(t *testing.T, path string, opts Options) *Document {
	t.Helper()
	d, err := Open(path, opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func TestCoverageNameAndPaths(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, []byte("abc"))
	d := covOpen(t, path, Options{})
	if d.Name() != "file.bin" || d.Path() != path {
		t.Fatalf("name %q path %q", d.Name(), d.Path())
	}
	if d.IsDevice() {
		t.Fatal("a regular file is a device")
	}
	if d.JournalDir() != "" {
		t.Fatal("a journal without JournalDir")
	}
	// A relative path is made absolute
	wd, _ := os.Getwd()
	rel, err := filepath.Rel(wd, path)
	if err == nil {
		r := covOpen(t, rel, Options{})
		if r.Path() != path {
			t.Fatalf("relative path kept: %q", r.Path())
		}
	}

	j := filepath.Join(dir, "j")
	dj := covOpen(t, path, Options{JournalDir: j})
	jd := dj.JournalDir()
	if filepath.Dir(jd) != j {
		t.Fatalf("journal dir %q is not in %q", jd, j)
	}
	for _, f := range []string{metaFile, addFile, logFile} {
		if _, err := os.Stat(filepath.Join(jd, f)); err != nil {
			t.Fatalf("journal file %s: %v", f, err)
		}
	}
	dj.Close()
	if _, err := os.Stat(jd); !os.IsNotExist(err) {
		t.Fatal("the journal stays after Close")
	}

	n, _ := New(4, Options{})
	defer n.Close()
	if n.Name() != "" || n.Path() != "" || !n.Modified() {
		t.Fatal("a new document has a name or is not modified")
	}
	if got := readAll(t, n); !bytes.Equal(got, make([]byte, 4)) {
		t.Fatalf("new document %v", got)
	}
}

func TestCoverageOpenErrors(t *testing.T) {
	dir := t.TempDir()
	if _, err := Open(filepath.Join(dir, "missing"), Options{}); err == nil {
		t.Fatal("opened a missing file")
	}
	if _, err := Open(dir, Options{}); err == nil {
		t.Fatal("opened a directory")
	}
	// The journal cannot be made under a file
	blocker := filepath.Join(dir, "blocker")
	os.WriteFile(blocker, nil, 0644)
	path := writeFile(t, dir, []byte("abc"))
	bad := Options{JournalDir: filepath.Join(blocker, "j")}
	if _, err := Open(path, bad); err == nil {
		t.Fatal("opened with a journal that cannot be made")
	}
	if _, err := New(1, bad); err == nil {
		t.Fatal("new with a journal that cannot be made")
	}
	if _, err := FromBytes([]byte("x"), bad); err == nil {
		t.Fatal("from bytes with a journal that cannot be made")
	}
}

func TestCoverageFromBytesWithJournal(t *testing.T) {
	dir := t.TempDir()
	j := filepath.Join(dir, "j")
	d, err := FromBytes([]byte("clipboard"), Options{JournalDir: j})
	if err != nil {
		t.Fatal(err)
	}
	if got := string(readAll(t, d)); got != "clipboard" {
		t.Fatalf("got %q", got)
	}
	if !d.Modified() {
		t.Fatal("a document from bytes is not modified")
	}
	d.Insert(0, []byte(">"), EditOptions{})
	want := string(readAll(t, d))
	crash(d)
	list, err := FindJournals(j)
	if err != nil || len(list) != 1 || list[0].Path != "" {
		t.Fatalf("journals %v %v", list, err)
	}
	r, err := Recover(list[0], Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if got := string(readAll(t, r)); got != want {
		t.Fatalf("recovered %q, want %q", got, want)
	}
	if r.Path() != "" {
		t.Fatalf("a new document recovered with path %q", r.Path())
	}
	// Empty bytes: an empty document
	e, err := FromBytes(nil, Options{})
	if err != nil || e.Size() != 0 {
		t.Fatalf("empty: %v %d", err, e.Size())
	}
	e.Close()
}

func TestCoverageEditEdges(t *testing.T) {
	d, _ := FromBytes([]byte("0123456789"), Options{})
	defer d.Close()
	none := EditOptions{}
	// Edits of nothing are no edits
	for _, err := range []error{
		d.Insert(3, nil, none), d.InsertZeros(3, 0, none), d.Delete(3, 0, none),
		d.Overwrite(3, nil, none), d.OverwriteZeros(3, -1, none),
		d.InsertFrom(3, covErrReader{}, 0, 0, none),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, cur := d.History(); cur.Parent != nil {
		t.Fatal("an empty edit made a state")
	}
	// Out of range
	if err := d.Insert(-1, []byte("x"), none); err != ErrOutOfRange {
		t.Fatalf("insert at -1: %v", err)
	}
	if err := d.Insert(11, []byte("x"), none); err != ErrOutOfRange {
		t.Fatalf("insert past the end: %v", err)
	}
	// Delete at the end deletes nothing; past it as many as there are
	if err := d.Delete(10, 5, none); err != nil {
		t.Fatal(err)
	}
	if _, cur := d.History(); cur.Parent != nil {
		t.Fatal("deleting nothing made a state")
	}
	d.Delete(8, 100, none)
	if got := string(readAll(t, d)); got != "01234567" {
		t.Fatalf("delete past the end: %q", got)
	}
	// OverwriteZeros past the end appends
	d.OverwriteZeros(6, 4, none)
	if got := string(readAll(t, d)); got != "012345\x00\x00\x00\x00" {
		t.Fatalf("overwrite zeros: %q", got)
	}
	if _, cur := d.History(); cur.Kind != KindFill || cur.Off != 6 || cur.Len != 4 {
		t.Fatalf("fill state %q %d %d", cur.Kind, cur.Off, cur.Len)
	}
	// InsertFrom reads a source in parts
	big := bytes.Repeat([]byte("xyz"), addChunkSize/2) // more than one chunk
	if err := d.InsertFrom(1, source.NewMemory(big), 3, int64(len(big)-3), none); err != nil {
		t.Fatal(err)
	}
	got := readAll(t, d)
	if !bytes.Equal(got[1:len(big)-2], big[3:]) || got[0] != '0' || string(got[len(big)-2:]) != "12345\x00\x00\x00\x00" {
		t.Fatal("insert from a source")
	}
	if _, cur := d.History(); cur.Kind != KindPaste {
		t.Fatalf("insert from is %q", cur.Kind)
	}
	// A failed read changes nothing
	size := d.Size()
	if err := d.InsertFrom(0, covErrReader{}, 0, 10, none); !errors.Is(err, covErrBadSector) {
		t.Fatalf("insert from a failing reader: %v", err)
	}
	if d.Size() != size {
		t.Fatal("a failed insert changed the document")
	}
	// The kind of an edit can be named
	d.Overwrite(0, []byte("Q"), EditOptions{Kind: "replace"})
	if _, cur := d.History(); cur.Kind != "replace" {
		t.Fatalf("kind %q", cur.Kind)
	}
}

func TestCoverageReadOnlyEverything(t *testing.T) {
	d, _ := FromBytes([]byte("abc"), Options{})
	defer d.Close()
	d.Overwrite(0, []byte("x"), EditOptions{})
	d.SetReadOnly(true)
	if !d.ReadOnly() {
		t.Fatal("not read-only")
	}
	for name, err := range map[string]error{
		"zeros":     d.InsertZeros(0, 1, EditOptions{}),
		"delete":    d.Delete(0, 1, EditOptions{}),
		"overwrite": d.Overwrite(0, []byte("y"), EditOptions{}),
		"fill":      d.OverwriteZeros(0, 1, EditOptions{}),
		"from":      d.InsertFrom(0, source.NewMemory([]byte("z")), 0, 1, EditOptions{}),
	} {
		if err != ErrReadOnly {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if _, err := d.Undo(); err != ErrReadOnly {
		t.Fatalf("undo: %v", err)
	}
	if err := d.Goto(0); err != ErrReadOnly {
		t.Fatalf("goto: %v", err)
	}
	if got := string(readAll(t, d)); got != "xbc" {
		t.Fatalf("got %q", got)
	}
	d.SetReadOnly(false)
	d.Undo()
	d.SetReadOnly(true)
	if _, err := d.Redo(); err != ErrReadOnly {
		t.Fatalf("redo: %v", err)
	}
}

func TestCoverageUndoRedoGoto(t *testing.T) {
	d, _ := FromBytes([]byte("abc"), Options{})
	defer d.Close()
	if d.CanUndo() || d.CanRedo() {
		t.Fatal("undo or redo at the start")
	}
	d.Overwrite(0, []byte("1"), EditOptions{}) // state A
	_, a := d.History()
	d.Overwrite(1, []byte("2"), EditOptions{}) // state B
	_, b := d.History()
	if !d.CanUndo() || d.CanRedo() {
		t.Fatal("after edits")
	}
	n, err := d.Undo()
	if err != nil || n != b || n.Off != 1 || n.Len != 1 {
		t.Fatalf("undo returned %v %v", n, err)
	}
	if !d.CanRedo() {
		t.Fatal("no redo after undo")
	}
	// A new branch: redo goes to it, B stays in the tree
	d.Overwrite(2, []byte("3"), EditOptions{}) // state C
	_, c := d.History()
	if len(a.Children) != 2 {
		t.Fatalf("branches: %d", len(a.Children))
	}
	if got := string(readAll(t, d)); got != "1b3" {
		t.Fatalf("branch C: %q", got)
	}
	if err := d.Goto(b.ID); err != nil {
		t.Fatal(err)
	}
	if got := string(readAll(t, d)); got != "12c" {
		t.Fatalf("goto B: %q", got)
	}
	if d.CanRedo() {
		t.Fatal("redo from a leaf")
	}
	if _, err := d.Redo(); err != ErrNothingToDo {
		t.Fatalf("redo from a leaf: %v", err)
	}
	// Undo from B then redo goes back to B (the one undone last), not C
	d.Undo()
	if n, _ := d.Redo(); n != b {
		t.Fatalf("redo went to %d, want B %d", n.ID, b.ID)
	}
	if err := d.Goto(c.ID); err != nil {
		t.Fatal(err)
	}
	if got := string(readAll(t, d)); got != "1b3" {
		t.Fatalf("goto C: %q", got)
	}
	if err := d.Goto(12345); err != ErrNothingToDo {
		t.Fatalf("goto an unknown state: %v", err)
	}
	root, _ := d.History()
	if root.Parent != nil || root.Kind != "open" {
		t.Fatalf("root %+v", root)
	}
	if err := d.Goto(root.ID); err != nil {
		t.Fatal(err)
	}
	if d.Modified() {
		// A document from bytes is never saved: every state is modified
	} else {
		t.Fatal("a document from bytes counts as saved")
	}
}

func TestCoverageGroupsAndSubscribe(t *testing.T) {
	d, _ := FromBytes([]byte("aaaa"), Options{})
	defer d.Close()
	calls := 0
	unsub := d.Subscribe(func() { calls++ })
	other := 0
	d.Subscribe(func() { other++ })

	d.EndGroup() // without a group: nothing
	d.Overwrite(0, []byte("b"), EditOptions{})
	if calls != 1 {
		t.Fatalf("calls after an edit: %d", calls)
	}
	d.BeginGroup("outer")
	d.BeginGroup("inner") // nested: one state, named by the outer group
	d.Overwrite(1, []byte("c"), EditOptions{})
	d.Overwrite(2, []byte("d"), EditOptions{})
	if _, err := d.Undo(); err != ErrNothingToDo {
		t.Fatalf("undo in a group: %v", err)
	}
	if _, err := d.Redo(); err != ErrNothingToDo {
		t.Fatalf("redo in a group: %v", err)
	}
	if err := d.Goto(0); err != ErrNothingToDo {
		t.Fatalf("goto in a group: %v", err)
	}
	dir := t.TempDir()
	if err := d.Save(SaveOptions{Path: filepath.Join(dir, "x")}); err == nil {
		t.Fatal("saved in the middle of a group")
	}
	d.EndGroup()
	if calls != 1 {
		t.Fatalf("told of edits inside a group: %d", calls)
	}
	d.EndGroup()
	if calls != 2 {
		t.Fatalf("not told once at the end of the group: %d", calls)
	}
	_, cur := d.History()
	if cur.Kind != "outer" {
		t.Fatalf("group kind %q", cur.Kind)
	}
	// A group without edits makes no state and tells no one
	d.BeginGroup("empty")
	d.EndGroup()
	if _, c2 := d.History(); c2 != cur || calls != 2 {
		t.Fatal("an empty group made a state")
	}
	unsub()
	d.Undo()
	if calls != 2 || other != 3 {
		t.Fatalf("after unsubscribe: calls %d other %d", calls, other)
	}
	if got := string(readAll(t, d)); got != "baaa" {
		t.Fatalf("group undone in parts: %q", got)
	}
	d.SetReadOnly(true)
	if other != 4 {
		t.Fatal("not told of read-only")
	}
}

func TestCoverageTypingContinues(t *testing.T) {
	d, _ := FromBytes(nil, Options{})
	defer d.Close()
	typing := EditOptions{Kind: KindTyping, Typing: true}
	// Typing over the same byte twice (a hex digit, then the other) is one state
	d.Insert(0, []byte{0x10}, typing)
	d.Overwrite(0, []byte{0x1f}, typing)
	d.Overwrite(1, []byte{0x20}, typing)
	if _, cur := d.History(); cur.Parent == nil || cur.Parent.Parent != nil {
		t.Fatal("typing made more than one state")
	}
	// An edit of another kind seals it
	d.Delete(0, 1, EditOptions{})
	d.Insert(0, []byte{1}, typing)
	if _, cur := d.History(); cur.Parent.Kind != KindDelete {
		t.Fatalf("typing continued past a delete: parent %q", cur.Parent.Kind)
	}
	// After undo typing starts a new state
	d.Undo()
	d.Insert(0, []byte{2}, typing)
	root, _ := d.History()
	if len(root.Children) != 1 || len(root.Children[0].Children) != 1 || len(root.Children[0].Children[0].Children) != 2 {
		t.Fatal("typing after undo continued an undone state")
	}
}

func TestCoverageSnapshotReads(t *testing.T) {
	d, _ := FromBytes([]byte("0123456789"), Options{})
	defer d.Close()
	snap := d.Snapshot()
	if _, err := snap.ReadAt(make([]byte, 1), -1); err != ErrOutOfRange {
		t.Fatalf("read at -1: %v", err)
	}
	if n, err := snap.ReadAt(make([]byte, 1), 10); n != 0 || err != io.EOF {
		t.Fatalf("read at the end: %d %v", n, err)
	}
	buf := make([]byte, 5)
	if n, err := d.ReadAt(buf, 7); n != 3 || err != io.EOF || string(buf[:3]) != "789" {
		t.Fatalf("short read: %d %v %q", n, err, buf[:n])
	}
	// TryReadAt of memory buffers is ready at once
	if n, ok := snap.TryReadAt(buf, 2, nil); !ok || n != 5 || string(buf) != "23456" {
		t.Fatalf("try read %d %v %q", n, ok, buf)
	}
	if n, ok := snap.TryReadAt(buf, -1, nil); n != 0 || !ok {
		t.Fatal("try read before the start")
	}
	if n, ok := snap.TryReadAt(buf, 10, nil); n != 0 || !ok {
		t.Fatal("try read at the end")
	}
	d.InsertZeros(0, 3, EditOptions{})
	if n, ok := d.Snapshot().TryReadAt(buf, 0, nil); !ok || n != 5 || string(buf) != "\x00\x00\x00012"[:5] {
		t.Fatalf("try read of zeros %q", buf)
	}
	// The old snapshot does not change
	if snap.Size() != 10 {
		t.Fatal("a snapshot changed")
	}
	// Edited past the end marks nothing
	marks := []bool{true, true}
	snap.Edited(20, marks)
	if marks[0] || marks[1] {
		t.Fatal("marks past the end")
	}
	// Nothing in memory buffers is unreadable
	if snap.Unreadable(0, make([]bool, 10)) {
		t.Fatal("memory unreadable")
	}
}

func TestCoverageTryReadAtFile(t *testing.T) {
	dir := t.TempDir()
	data := bytes.Repeat([]byte("0123456789abcdef"), 3*source.PageSize/16)
	path := writeFile(t, dir, data)
	d := covOpen(t, path, Options{})
	snap := d.Snapshot()
	buf := make([]byte, 100)
	off := int64(source.PageSize + 5)
	ready := make(chan struct{}, 4)
	n, ok := snap.TryReadAt(buf, off, func() { ready <- struct{}{} })
	if n != len(buf) {
		t.Fatalf("try read %d", n)
	}
	if !ok {
		select {
		case <-ready:
		case <-time.After(5 * time.Second):
			t.Fatal("onReady not called")
		}
		if _, ok = snap.TryReadAt(buf, off, nil); !ok {
			t.Fatal("not ready after onReady")
		}
	}
	if !bytes.Equal(buf, data[off:off+100]) {
		t.Fatal("try read differs")
	}
	// Bytes kept from the file by a save in place come from the add buffer
	d.Overwrite(off, []byte("XY"), EditOptions{})
	if err := d.Save(SaveOptions{}); err != nil {
		t.Fatal(err)
	}
	d.Undo()
	old := d.Snapshot()
	for i := 0; i < 50; i++ {
		if _, ok = old.TryReadAt(buf, off, nil); ok {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !ok || !bytes.Equal(buf, data[off:off+100]) {
		t.Fatalf("try read of the state before the save: ready %v, %q", ok, buf[:4])
	}
}

func TestCoverageUnreadableSource(t *testing.T) {
	data := bytes.Repeat([]byte{0xAA}, 3*source.PageSize)
	src := &covBadSource{data: data, badFrom: source.PageSize + 10, badTo: source.PageSize + 20}
	d, err := OpenSource(src, "bad disk")
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	snap := d.Snapshot()
	// Before anything is read nothing is known to be bad
	if snap.Unreadable(0, make([]bool, 100)) {
		t.Fatal("unreadable before reading")
	}
	buf := make([]byte, 3*source.PageSize)
	for i := range buf {
		buf[i] = 0x55
	}
	n, err := snap.ReadAt(buf, 0)
	if !errors.Is(err, covErrBadSector) {
		t.Fatalf("read over a bad sector: %d %v", n, err)
	}
	// The bad page reads as zeros
	if buf[0] != 0xAA || buf[source.PageSize] != 0 || buf[2*source.PageSize-1] != 0 {
		t.Fatalf("bytes around the bad page: %x %x %x", buf[0], buf[source.PageSize], buf[2*source.PageSize-1])
	}
	// The page is marked as a whole; the others are not
	mark := make([]bool, 3*source.PageSize)
	if !snap.Unreadable(0, mark) {
		t.Fatal("no unreadable bytes")
	}
	if mark[source.PageSize-1] || !mark[source.PageSize] || !mark[2*source.PageSize-1] || mark[2*source.PageSize] {
		t.Fatal("unreadable marks are not the bad page")
	}
	// The pages after the bad one are read in a later call
	if _, err := snap.ReadAt(buf[:10], 2*source.PageSize); err != nil {
		t.Fatal(err)
	}
	if snap.Unreadable(2*source.PageSize, make([]bool, 10)) {
		t.Fatal("a good page is unreadable")
	}
	// TryReadAt shows the bad page as zeros and is ready
	if _, ok := snap.TryReadAt(buf[:10], source.PageSize, nil); !ok {
		t.Fatal("a failed page is not ready")
	}
	if !bytes.Equal(buf[:10], make([]byte, 10)) {
		t.Fatal("a failed page is not zeros")
	}
	// Edits over the bad bytes are readable; only the source is marked
	d.SetReadOnly(false)
	d.Overwrite(source.PageSize, bytes.Repeat([]byte{1}, 100), EditOptions{})
	mark = make([]bool, 200)
	d.Snapshot().Unreadable(source.PageSize-50, mark)
	if mark[49] || mark[50] || mark[149] || !mark[150] {
		t.Fatal("marks around an edit over a bad page")
	}
	// Saving a copy fails on the bad bytes
	if err := d.Save(SaveOptions{Path: filepath.Join(t.TempDir(), "copy")}); !errors.Is(err, covErrBadSector) {
		t.Fatalf("saving over bad sectors: %v", err)
	}
}

func TestCoverageFileChanged(t *testing.T) {
	n, _ := New(1, Options{})
	defer n.Close()
	if ch, err := n.FileChanged(); ch || err != nil {
		t.Fatal("a new document has a changed file")
	}
	dir := t.TempDir()
	path := writeFile(t, dir, []byte("0123456789"))
	d := covOpen(t, path, Options{})
	if ch, err := d.FileChanged(); ch || err != nil {
		t.Fatalf("unchanged: %v %v", ch, err)
	}
	// Saving is not a change by someone else
	d.Overwrite(0, []byte("x"), EditOptions{})
	if err := d.Save(SaveOptions{}); err != nil {
		t.Fatal(err)
	}
	if ch, err := d.FileChanged(); ch || err != nil {
		t.Fatalf("after saving in place: %v %v", ch, err)
	}
	d.Insert(0, []byte("y"), EditOptions{})
	if err := d.Save(SaveOptions{}); err != nil {
		t.Fatal(err)
	}
	if ch, err := d.FileChanged(); ch || err != nil {
		t.Fatalf("after rewriting: %v %v", ch, err)
	}
	// Another program writes the file
	os.WriteFile(path, []byte("changed by someone else"), 0644)
	if ch, err := d.FileChanged(); !ch || err != nil {
		t.Fatalf("changed: %v %v", ch, err)
	}
	// The document still reads the file as it was opened... and when it is gone
	os.Remove(path)
	if _, err := d.FileChanged(); err == nil {
		t.Fatal("no error for a removed file")
	}
}

func TestCoverageSaveAs(t *testing.T) {
	dir := t.TempDir()
	j := filepath.Join(dir, "j")
	path := writeFile(t, dir, []byte("0123456789"))
	d, err := Open(path, Options{JournalDir: j})
	if err != nil {
		t.Fatal(err)
	}
	d.Overwrite(0, []byte("AB"), EditOptions{})
	var progress [][2]int64
	other := filepath.Join(dir, "other.bin")
	if err := d.Save(SaveOptions{Path: other, Progress: func(done, total int64) {
		progress = append(progress, [2]int64{done, total})
	}}); err != nil {
		t.Fatal(err)
	}
	if len(progress) == 0 || progress[len(progress)-1] != [2]int64{10, 10} {
		t.Fatalf("progress %v", progress)
	}
	if d.Path() != other || d.Name() != "other.bin" || d.Modified() {
		t.Fatalf("after save as: %q %v", d.Path(), d.Modified())
	}
	if bs, _ := os.ReadFile(path); string(bs) != "0123456789" {
		t.Fatalf("save as changed the old file: %q", bs)
	}
	if bs, _ := os.ReadFile(other); string(bs) != "AB23456789" {
		t.Fatalf("saved as %q", bs)
	}
	// The journal knows the new name
	d.Overwrite(9, []byte("Z"), EditOptions{})
	d.Sync()
	crash(d)
	list, err := FindJournals(j)
	if err != nil || len(list) != 1 {
		t.Fatalf("journals %v %v", list, err)
	}
	if list[0].Path != other {
		t.Fatalf("journal path %q", list[0].Path)
	}
	r, err := Recover(list[0], Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if r.Path() != other {
		t.Fatalf("recovered path %q", r.Path())
	}
	if got := string(readAll(t, r)); got != "AB2345678Z" {
		t.Fatalf("recovered %q", got)
	}
	// Undo back to the saved state, before it the old file was replaced by nothing: still the old file
	r.Undo()
	if r.Modified() {
		t.Fatal("the saved state is modified")
	}
	// Save the new document over a path that cannot be written
	n, _ := FromBytes([]byte("x"), Options{})
	defer n.Close()
	if err := n.Save(SaveOptions{}); err != ErrNoPath {
		t.Fatalf("no path: %v", err)
	}
	if err := n.Save(SaveOptions{Path: filepath.Join(dir, "no", "such", "dir", "f")}); err == nil {
		t.Fatal("saved into a missing directory")
	}
	if n.Path() != "" || !n.Modified() {
		t.Fatal("a failed save changed the document")
	}
	np := filepath.Join(dir, "new.bin")
	if err := n.Save(SaveOptions{Path: np}); err != nil {
		t.Fatal(err)
	}
	if n.Path() != np || n.Modified() {
		t.Fatal("saved a new document")
	}
	if ch, _ := n.FileChanged(); ch {
		t.Fatal("a just saved file is changed")
	}
}

func TestCoverageSaveKeepsPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no permissions")
	}
	dir := t.TempDir()
	path := writeFile(t, dir, []byte("abc"))
	os.Chmod(path, 0600)
	d := covOpen(t, path, Options{})
	d.Insert(0, []byte(">"), EditOptions{})
	if err := d.Save(SaveOptions{}); err != nil {
		t.Fatal(err)
	}
	st, _ := os.Stat(path)
	if st.Mode().Perm() != 0600 {
		t.Fatalf("permissions %v", st.Mode().Perm())
	}
}

func TestCoverageSaveCancel(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(d *Document)
		j    bool
	}{
		{"in place", func(d *Document) { d.Overwrite(5, []byte("XX"), EditOptions{}) }, false},
		{"in place with journal", func(d *Document) { d.Overwrite(5, []byte("XX"), EditOptions{}) }, true},
		{"rewrite", func(d *Document) { d.Insert(5, []byte("XX"), EditOptions{}) }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := writeFile(t, dir, []byte("0123456789"))
			opts := Options{}
			if tc.j {
				opts.JournalDir = filepath.Join(dir, "j")
			}
			d := covOpen(t, path, opts)
			tc.edit(d)
			want := readAll(t, d)
			err := d.Save(SaveOptions{Cancel: func() bool { return true }})
			if err != ErrCanceled {
				t.Fatalf("save: %v", err)
			}
			if bs, _ := os.ReadFile(path); string(bs) != "0123456789" {
				t.Fatalf("a canceled save changed the file: %q", bs)
			}
			if !d.Modified() || !bytes.Equal(readAll(t, d), want) {
				t.Fatal("a canceled save changed the document")
			}
			// No temporary files are left
			entries, _ := os.ReadDir(dir)
			for _, e := range entries {
				if e.Name() != "file.bin" && e.Name() != "j" {
					t.Fatalf("left behind: %s", e.Name())
				}
			}
			if tc.j {
				if _, err := os.Stat(filepath.Join(d.JournalDir(), walFile)); !os.IsNotExist(err) {
					t.Fatal("the log of a canceled save stays")
				}
			}
			if err := d.Save(SaveOptions{}); err != nil {
				t.Fatal(err)
			}
			if bs, _ := os.ReadFile(path); !bytes.Equal(bs, want) {
				t.Fatalf("saved %q", bs)
			}
		})
	}
}

func TestCoverageSaveInPlaceShrinksAndBackupless(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, []byte("0123456789"))
	d := covOpen(t, path, Options{JournalDir: filepath.Join(dir, "j")})
	// Deleting the tail keeps every byte at its offset: saved in place, truncated
	d.Delete(6, 4, EditOptions{})
	_, before := d.History()
	if err := d.Save(SaveOptions{}); err != nil {
		t.Fatal(err)
	}
	if bs, _ := os.ReadFile(path); string(bs) != "012345" {
		t.Fatalf("saved %q", bs)
	}
	if _, cur := d.History(); cur != before {
		t.Fatal("saving made a new state")
	}
	// The cut tail is still there for undo
	d.Undo()
	if got := string(readAll(t, d)); got != "0123456789" {
		t.Fatalf("undo after truncating: %q", got)
	}
	if !d.Modified() {
		t.Fatal("not modified after undo past the save")
	}
	// Saving it again grows the file in place
	if err := d.Save(SaveOptions{}); err != nil {
		t.Fatal(err)
	}
	if bs, _ := os.ReadFile(path); string(bs) != "0123456789" {
		t.Fatalf("saved again %q", bs)
	}
	if err := d.Sync(); err != nil {
		t.Fatal(err)
	}
}

// Saving in place that would keep more than keepLimit bytes for the history drops it
func TestCoverageSaveDropsHugeHistory(t *testing.T) {
	size := int64(keepLimit + 1<<20)
	path := hugeFile(t, size)
	d := covOpen(t, path, Options{JournalDir: filepath.Join(filepath.Dir(path), "j")})
	d.Overwrite(0, []byte("A"), EditOptions{})
	d.Delete(1, size-1, EditOptions{})
	if err := d.Save(SaveOptions{}); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(path); st.Size() != 1 {
		t.Fatalf("size after saving: %d", st.Size())
	}
	if d.CanUndo() {
		t.Fatal("the history before a huge save stays")
	}
	if _, err := d.Undo(); err != ErrNothingToDo {
		t.Fatalf("undo: %v", err)
	}
	root, cur := d.History()
	if root != cur || d.Modified() {
		t.Fatal("the saved state is not the only one")
	}
	if got := string(readAll(t, d)); got != "A" {
		t.Fatalf("got %q", got)
	}
	// The journal goes on from it
	d.Insert(1, []byte("B"), EditOptions{})
	d.Sync()
	crash(d)
	r := recoverOne(t, filepath.Join(filepath.Dir(path), "j"))
	defer r.Close()
	if got := string(readAll(t, r)); got != "AB" {
		t.Fatalf("recovered %q", got)
	}
}

func TestCoverageDevice(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("a device file of Linux")
	}
	const dev = "/dev/zero"
	d, err := Open(dev, Options{})
	if err != nil {
		t.Skipf("%s: %v", dev, err)
	}
	defer d.Close()
	if !d.IsDevice() {
		t.Fatal("not a device")
	}
	// A disk keeps its size
	d.Insert(0, []byte("x"), EditOptions{})
	if err := d.Save(SaveOptions{}); err != ErrDeviceSize {
		t.Fatalf("saving a disk with an insert: %v", err)
	}
	if ch, err := d.FileChanged(); err != nil || ch {
		// The size of a device is asked of the system, not its stat: it may differ
		t.Logf("device changed %v %v", ch, err)
	}
}

func TestCoverageJournalWriteErrorKeepsEditing(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, []byte("abc"))
	d := covOpen(t, path, Options{JournalDir: filepath.Join(dir, "j")})
	if err := d.Sync(); err != nil {
		t.Fatal(err)
	}
	// The log is lost under the journal: the editing goes on
	d.journal.log.Close()
	if err := d.Insert(0, []byte("x"), EditOptions{}); err != nil {
		t.Fatal(err)
	}
	if got := string(readAll(t, d)); got != "xabc" {
		t.Fatalf("got %q", got)
	}
	if err := d.Sync(); err == nil {
		t.Fatal("no error from a journal that failed")
	}
	d.Undo()
	if got := string(readAll(t, d)); got != "abc" {
		t.Fatalf("undo %q", got)
	}
	// Without a journal Sync does nothing
	n, _ := New(0, Options{})
	defer n.Close()
	if err := n.Sync(); err != nil {
		t.Fatal(err)
	}
}

func TestCoverageCloseReleases(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, []byte("0123456789"))
	d, _ := Open(path, Options{})
	d.Insert(0, []byte(">"), EditOptions{})
	d.Save(SaveOptions{}) // the old file stays open for the history
	if len(d.oldFiles) != 1 {
		t.Fatalf("old files %d", len(d.oldFiles))
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if d.oldFiles != nil {
		t.Fatal("old files stay open")
	}
	if _, err := d.file.ReadAt(make([]byte, 1), 0); !errors.Is(err, source.ErrClosed) {
		t.Fatalf("the file is open after Close: %v", err)
	}
}

func TestCoverageReplaceFile(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, []byte("0123456789"))
	d := covOpen(t, path, Options{})
	d.Overwrite(0, []byte("X"), EditOptions{})
	// As a system that does not replace an open file does: the versions read another handle
	f, err := source.OpenFile(path)
	if err != nil {
		t.Fatal(err)
	}
	old := d.file
	d.replaceFile(f)
	old.Close()
	if got := string(readAll(t, d)); got != "X123456789" {
		t.Fatalf("after replacing the handle: %q", got)
	}
	d.reopenOldFile("") // no backup: nothing
	if d.file != f {
		t.Fatal("reopen without a backup changed the file")
	}
	d.reopenOldFile(filepath.Join(dir, "missing.bak"))
	if d.file != f {
		t.Fatal("reopen of a missing backup changed the file")
	}
	bak := filepath.Join(dir, "copy.bak")
	os.WriteFile(bak, []byte("0123456789"), 0644)
	d.reopenOldFile(bak)
	if d.file.Path() != bak {
		t.Fatal("the backup is not reopened")
	}
	d.oldFiles = append(d.oldFiles, f)
	if d.closedSource() {
		t.Fatal("an open old file counts as closed")
	}
	f.Close()
	if !d.closedSource() {
		t.Fatal("a closed old file is not found")
	}
	d.oldFiles = nil
}
