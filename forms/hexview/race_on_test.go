//go:build race

package hexview

// raceEnabled: the race detector slows the code down many times, so the
// timings are not checked
const raceEnabled = true
