package ops

import (
	"bytes"
	"context"
	"math/rand/v2"
	"testing"

	"github.com/ipoluianov/althex/core/doc"
)

func newDoc(t *testing.T, data []byte) *doc.Document {
	t.Helper()
	d, err := doc.FromBytes(data, doc.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func all(d *doc.Document) []byte {
	b := make([]byte, d.Size())
	d.ReadAt(b, 0)
	return b
}

func TestFill(t *testing.T) {
	ctx := context.Background()
	d := newDoc(t, make([]byte, 10))
	if err := DoFill(ctx, d, 2, 5, Fill{Kind: FillPattern, Pattern: []byte("ab")}, nil); err != nil {
		t.Fatal(err)
	}
	if got := string(all(d)); got != "\x00\x00ababa\x00\x00\x00" {
		t.Fatalf("pattern: %q", got)
	}
	d = newDoc(t, make([]byte, 8))
	DoFill(ctx, d, 0, 8, Fill{Kind: FillIncrement, Start: 0x0102, Step: 1, Width: 2, BigEndian: true}, nil)
	if got := all(d); !bytes.Equal(got, []byte{1, 2, 1, 3, 1, 4, 1, 5}) {
		t.Fatalf("increment: % x", got)
	}
	d = newDoc(t, []byte("xy"))
	DoFill(ctx, d, 1, 3<<20, Fill{Kind: FillPattern, Pattern: []byte{0}, Insert: true}, nil)
	if d.Size() != 2+3<<20 || d.Snapshot().Pieces() > 3 {
		t.Fatalf("zeros take %d pieces, size %d", d.Snapshot().Pieces(), d.Size())
	}
	d = newDoc(t, make([]byte, 3<<20))
	DoFill(ctx, d, 0, 3<<20, Fill{Kind: FillRandom}, nil)
	if bytes.Count(all(d), []byte{0}) > 3<<20/100 {
		t.Fatal("random bytes are mostly zeros")
	}
}

func TestTransform(t *testing.T) {
	ctx := context.Background()
	for _, c := range []struct {
		in   []byte
		tr   Transform
		want []byte
	}{
		{[]byte{0xF0, 0x0F}, Transform{Op: OpXor, Operand: []byte{0xFF}}, []byte{0x0F, 0xF0}},
		{[]byte{0xF0, 0x0F}, Transform{Op: OpAnd, Operand: []byte{0x3C}}, []byte{0x30, 0x0C}},
		{[]byte{0x81}, Transform{Op: OpRol, Bits: 1}, []byte{0x03}},
		{[]byte{0x81}, Transform{Op: OpShr, Bits: 4}, []byte{0x08}},
		{[]byte{0xFE}, Transform{Op: OpAdd, Operand: []byte{3}}, []byte{0x01}},
		{[]byte{1, 2, 3, 4, 5}, Transform{Op: OpSwap, Width: 2}, []byte{2, 1, 4, 3, 5}},
		{[]byte{1, 2, 3, 4, 5}, Transform{Op: OpReverse}, []byte{5, 4, 3, 2, 1}},
		{[]byte{0x00}, Transform{Op: OpNot}, []byte{0xFF}},
	} {
		d := newDoc(t, c.in)
		if err := DoTransform(ctx, d, 0, int64(len(c.in)), c.tr, nil); err != nil {
			t.Fatal(err)
		}
		if got := all(d); !bytes.Equal(got, c.want) {
			t.Errorf("%v: got % x, want % x", c.tr.Op, got, c.want)
		}
	}
}

func TestReverseLarge(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	for _, n := range []int{0, 1, 2, 3, chunk - 1, 2*chunk + 3, 5*chunk + 1} {
		data := make([]byte, n)
		for i := range data {
			data[i] = byte(r.UintN(256))
		}
		d := newDoc(t, data)
		d.BeginGroup("reverse")
		if err := DoTransform(context.Background(), d, 0, int64(n), Transform{Op: OpReverse}, nil); err != nil {
			t.Fatal(err)
		}
		d.EndGroup()
		want := bytes.Clone(data)
		reverseBytes(want)
		if !bytes.Equal(all(d), want) {
			t.Fatalf("reverse of %d bytes differs", n)
		}
		// One undo for the whole operation
		if n > 1 {
			d.Undo()
			if !bytes.Equal(all(d), data) {
				t.Fatalf("undo of the reverse of %d bytes", n)
			}
		}
	}
}

func TestCancel(t *testing.T) {
	d := newDoc(t, make([]byte, 4<<20))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := DoTransform(ctx, d, 0, d.Size(), Transform{Op: OpNot}, nil); err == nil {
		t.Fatal("not canceled")
	}
}
