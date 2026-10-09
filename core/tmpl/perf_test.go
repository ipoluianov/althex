package tmpl

import (
	"context"
	"testing"
	"time"
)

// A template reading 100 000 small structs runs in a fraction of a second
func TestPerfManyStructs(t *testing.T) {
	if raceEnabled {
		t.Skip("timings are not checked with the race detector")
	}
	data := make([]byte, 100_000*8)
	for i := 0; i < len(data); i += 8 {
		data[i] = 4 // len
	}
	p, err := Compile("perf", `
		struct REC {
			uint32 len;
			if (len > 0)
				uchar data[len];
		};
		while (!FEof())
			REC rec;
	`)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	res := p.Run(context.Background(), Env{Data: &mem{b: data}})
	took := time.Since(start)
	if res.Err != nil {
		t.Fatal(res.Err)
	}
	t.Logf("%d fields in %v", res.Nodes, took)
	if took > 2*time.Second {
		t.Errorf("too slow: %v", took)
	}
}
