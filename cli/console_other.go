//go:build !windows

package cli

// attachConsole: on Unix the output goes to the terminal anyway
func attachConsole() {}
