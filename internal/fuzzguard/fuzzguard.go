// Package fuzzguard turns a hang in a fuzz target into a crash: the fuzzer
// has no time limit per input, and a crash is what it keeps and minimizes
package fuzzguard

import (
	"fmt"
	"os"
	"runtime"
	"time"
)

// Timeout is how long one input may take under the coverage of the fuzzer
// (much slower than a normal build) on a loaded machine; the environment
// variable ALTHEX_FUZZ_TIMEOUT changes it ("5s")
var Timeout = 30 * time.Second

func init() {
	if d, err := time.ParseDuration(os.Getenv("ALTHEX_FUZZ_TIMEOUT")); err == nil && d > 0 {
		Timeout = d
	}
}

// Start starts the clock of one input; call the function returned when it
// is done. Past Timeout the stacks of all goroutines are printed and the
// process exits, so the input is saved as a crasher.
func Start(name string) (stop func()) {
	t := time.AfterFunc(Timeout, func() {
		buf := make([]byte, 4<<20)
		n := runtime.Stack(buf, true)
		fmt.Fprintf(os.Stderr, "%s: an input took more than %v\n%s", name, Timeout, buf[:n])
		panic(fmt.Sprintf("%s: hang (more than %v)", name, Timeout))
	})
	return func() { t.Stop() }
}
