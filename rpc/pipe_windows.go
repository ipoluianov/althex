package rpc

import (
	"errors"
	"net"
	"os"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const pipeBuffer = 64 << 10

// pipeAddr is the address of a named pipe, its full path
type pipeAddr string

func (a pipeAddr) Network() string { return "pipe" }
func (a pipeAddr) String() string  { return string(a) }

func pipePath(name string) string {
	return `\\.\pipe\` + name
}

// pipeConn is a connected pipe. The handle is opened for overlapped I/O,
// so os.File runs it through the runtime poller: Close unblocks a Read and
// the deadlines work.
type pipeConn struct {
	*os.File
	addr pipeAddr
}

func (c *pipeConn) LocalAddr() net.Addr  { return c.addr }
func (c *pipeConn) RemoteAddr() net.Addr { return c.addr }

// pipeListener accepts clients of a named pipe. One instance of the pipe
// always waits for the next client, so a client never finds the name gone
// between two Accepts.
type pipeListener struct {
	path string
	sa   *windows.SecurityAttributes
	ov   *windows.Overlapped // on the heap: the system writes to it while Accept waits

	acceptMu sync.Mutex
	mu       sync.Mutex
	h        windows.Handle // the instance waiting for a client, 0 if none
	waiting  bool           // Accept waits on h
	closed   bool
}

// ListenPipe creates the named pipe \\.\pipe\<name>, open to the current
// user only and to no other computer. Close unblocks Accept.
func ListenPipe(name string) (net.Listener, error) {
	sa, err := ownerOnly()
	if err != nil {
		return nil, err
	}
	l := &pipeListener{path: pipePath(name), sa: sa, ov: new(windows.Overlapped)}
	// The first instance makes sure the name is not someone else's pipe
	l.h, err = l.create(true)
	if err != nil {
		return nil, &net.OpError{Op: "listen", Net: "pipe", Addr: pipeAddr(l.path), Err: err}
	}
	return l, nil
}

// ownerOnly makes the security attributes that let only the current user in
func ownerOnly() (*windows.SecurityAttributes, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, err
	}
	sd, err := windows.SecurityDescriptorFromString("D:P(A;;GA;;;" + user.User.Sid.String() + ")")
	if err != nil {
		return nil, err
	}
	sa := &windows.SecurityAttributes{SecurityDescriptor: sd}
	sa.Length = uint32(unsafe.Sizeof(*sa))
	return sa, nil
}

func (l *pipeListener) create(first bool) (windows.Handle, error) {
	path, err := windows.UTF16PtrFromString(l.path)
	if err != nil {
		return 0, err
	}
	flags := uint32(windows.PIPE_ACCESS_DUPLEX | windows.FILE_FLAG_OVERLAPPED)
	if first {
		flags |= windows.FILE_FLAG_FIRST_PIPE_INSTANCE
	}
	mode := uint32(windows.PIPE_TYPE_BYTE | windows.PIPE_READMODE_BYTE | windows.PIPE_WAIT | windows.PIPE_REJECT_REMOTE_CLIENTS)
	return windows.CreateNamedPipe(path, flags, mode, windows.PIPE_UNLIMITED_INSTANCES, pipeBuffer, pipeBuffer, 0, l.sa)
}

func (l *pipeListener) Accept() (net.Conn, error) {
	l.acceptMu.Lock()
	defer l.acceptMu.Unlock()
	for {
		l.mu.Lock()
		if l.closed {
			l.mu.Unlock()
			return nil, net.ErrClosed
		}
		if l.h == 0 {
			h, err := l.create(false)
			if err != nil {
				l.mu.Unlock()
				return nil, &net.OpError{Op: "accept", Net: "pipe", Addr: pipeAddr(l.path), Err: err}
			}
			l.h = h
		}
		h := l.h
		l.waiting = true
		l.mu.Unlock()

		err := l.connect(h)

		l.mu.Lock()
		l.waiting = false
		if l.closed {
			l.h = 0
			l.mu.Unlock()
			windows.CloseHandle(h)
			return nil, net.ErrClosed
		}
		if errors.Is(err, windows.ERROR_NO_DATA) {
			// The client came and went: the instance can wait for the next one
			windows.DisconnectNamedPipe(h)
			l.mu.Unlock()
			continue
		}
		l.h = 0 // the instance is the connection's now, or broken
		l.mu.Unlock()
		if err != nil {
			windows.CloseHandle(h)
			return nil, &net.OpError{Op: "accept", Net: "pipe", Addr: pipeAddr(l.path), Err: err}
		}
		return &pipeConn{File: os.NewFile(uintptr(h), l.path), addr: pipeAddr(l.path)}, nil
	}
}

// connect waits for a client on the instance h, until Close cancels it
func (l *pipeListener) connect(h windows.Handle) error {
	ev, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(ev)
	*l.ov = windows.Overlapped{HEvent: ev}
	switch err := windows.ConnectNamedPipe(h, l.ov); err {
	case nil, windows.ERROR_PIPE_CONNECTED:
		return nil
	case windows.ERROR_IO_PENDING:
	default:
		return err
	}
	// Close may have come before the wait began and found nothing to cancel
	l.mu.Lock()
	closed := l.closed
	l.mu.Unlock()
	if closed {
		windows.CancelIoEx(h, l.ov)
	}
	var n uint32
	return windows.GetOverlappedResult(h, l.ov, &n, true)
}

func (l *pipeListener) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil
	}
	l.closed = true
	if l.waiting {
		windows.CancelIoEx(l.h, nil) // Accept closes the instance
	} else if l.h != 0 {
		windows.CloseHandle(l.h)
		l.h = 0
	}
	return nil
}

func (l *pipeListener) Addr() net.Addr {
	return pipeAddr(l.path)
}

// DialPipe connects to the named pipe \\.\pipe\<name>, waiting up to
// timeout while all its instances are busy
func DialPipe(name string, timeout time.Duration) (net.Conn, error) {
	path := pipePath(name)
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(timeout)
	for {
		// SECURITY_IDENTIFICATION: the server may learn who connects, not act as them
		h, err := windows.CreateFile(p, windows.GENERIC_READ|windows.GENERIC_WRITE, 0, nil, windows.OPEN_EXISTING,
			windows.FILE_FLAG_OVERLAPPED|windows.SECURITY_SQOS_PRESENT|windows.SECURITY_IDENTIFICATION, 0)
		if err == nil {
			return &pipeConn{File: os.NewFile(uintptr(h), path), addr: pipeAddr(path)}, nil
		}
		if err != windows.ERROR_PIPE_BUSY || time.Now().After(deadline) {
			return nil, &net.OpError{Op: "dial", Net: "pipe", Addr: pipeAddr(path), Err: err}
		}
		time.Sleep(10 * time.Millisecond)
	}
}
