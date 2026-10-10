//go:build !windows

package rpc

import (
	"errors"
	"net"
	"time"
)

var errNoPipes = errors.New("rpc: named pipes are only on Windows")

// ListenPipe is for Windows only; elsewhere use ListenUnix
func ListenPipe(name string) (net.Listener, error) {
	return nil, errNoPipes
}

// DialPipe is for Windows only
func DialPipe(name string, timeout time.Duration) (net.Conn, error) {
	return nil, errNoPipes
}
