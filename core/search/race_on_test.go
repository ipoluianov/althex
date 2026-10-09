//go:build race

package search

// raceEnabled: the race detector slows the code down many times, so the
// timings are not checked
const raceEnabled = true
