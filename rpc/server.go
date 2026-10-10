package rpc

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"runtime"
	"sync"
	"time"
)

// ServeConn serves JSON-RPC 2.0 on a stream of newline-delimited messages
// (a batch is a JSON array on one line). Requests run concurrently and are
// answered in the order they finish. The method "initialize" with params
// {"clientInfo":{"name","version"}}, as in MCP, is answered here: it names
// the client for the later calls (see ClientFrom) and returns the server
// name and version. Params reach the handler as sent, nil when absent.
// ServeConn returns when the stream ends or ctx is done, and closes conn.
func ServeConn(ctx context.Context, conn io.ReadWriteCloser, h Handler, opt Options) error {
	var mu sync.Mutex
	client := clientInfo{}.client(opt.Transport)
	handle := func(ctx context.Context, method string, params json.RawMessage) (any, error) {
		if method == "initialize" {
			var p struct {
				ClientInfo clientInfo `json:"clientInfo"`
			}
			if len(params) > 0 {
				if err := json.Unmarshal(params, &p); err != nil {
					return nil, &Error{Code: InvalidParams, Message: "invalid params: " + err.Error()}
				}
			}
			mu.Lock()
			client = p.ClientInfo.client(opt.Transport)
			mu.Unlock()
			return map[string]string{"server": opt.Name, "version": opt.Version}, nil
		}
		mu.Lock()
		c := client
		mu.Unlock()
		return h.Call(WithClient(ctx, c), method, params)
	}
	defer conn.Close()
	return serveStream(ctx, conn, conn, conn, newProcessor(handle))
}

// serveStream reads newline-delimited messages from r and writes the
// replies to w, one per line. closer, when not nil, is closed on ctx done
// to unblock the reading; without it the reading goroutine stays until r
// ends.
func serveStream(ctx context.Context, r io.Reader, w io.Writer, closer io.Closer, p *processor) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var wmu sync.Mutex
	var werr error
	write := func(b []byte) {
		wmu.Lock()
		defer wmu.Unlock()
		if werr != nil {
			return
		}
		if _, werr = w.Write(append(b, '\n')); werr != nil {
			cancel() // the peer is gone, nobody reads the answers
		}
	}

	lines := make(chan []byte)
	readErr := make(chan error, 1)
	go func() {
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 64<<10), maxMessage+2) // with \r\n
		for sc.Scan() {
			line := append([]byte(nil), sc.Bytes()...)
			select {
			case lines <- line:
			case <-ctx.Done():
				return
			}
		}
		readErr <- sc.Err()
	}()

	var wg sync.WaitGroup
	defer wg.Wait()
	for {
		select {
		case line := <-lines:
			wg.Go(func() {
				if out := p.process(ctx, line); out != nil {
					write(out)
				}
			})
		case err := <-readErr:
			if errors.Is(err, bufio.ErrTooLong) {
				write(encodeError(nil, &Error{Code: TooLarge, Message: fmt.Sprintf("message larger than %d bytes", maxMessage)}))
				cancel()
				p.cancelAll()
				if closer != nil {
					closer.Close()
				}
				return err
			}
			// The peer has said all; the calls it made still get their answers
			// for a while ("echo request | althex rpc"), then they are cancelled:
			// one waiting for the user would keep the process forever
			waitGrace(&wg, p.cancelAll)
			wmu.Lock()
			defer wmu.Unlock()
			if err == nil {
				err = werr
			}
			return err
		case <-ctx.Done():
			p.cancelAll()
			if closer != nil {
				closer.Close()
			}
			wmu.Lock()
			defer wmu.Unlock()
			if werr != nil {
				return werr
			}
			return ctx.Err()
		}
	}
}

// eofGrace is how long the calls in flight may finish once the input ended
var eofGrace = 5 * time.Second

func waitGrace(wg *sync.WaitGroup, cancelAll func()) {
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(eofGrace):
		cancelAll()
		<-done
	}
}

// ListenUnix listens on a unix socket at path, readable and writable by
// the owner only. A socket left by a process that crashed is removed; one
// that answers is in use and is an error. Windows 10 and later have unix
// sockets too. Put the socket in a directory others cannot write to: the
// mode is set right after the socket appears.
func ListenUnix(path string) (net.Listener, error) {
	if fi, err := os.Lstat(path); err == nil {
		if c, err := net.DialTimeout("unix", path, time.Second); err == nil {
			c.Close()
			return nil, fmt.Errorf("%s: another server is listening", path)
		}
		// Windows does not always tell a socket from a file
		if fi.Mode()&os.ModeSocket == 0 && runtime.GOOS != "windows" {
			return nil, fmt.Errorf("%s: exists and is not a socket", path)
		}
		if err := os.Remove(path); err != nil {
			return nil, err
		}
	}
	l, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0600); err != nil && runtime.GOOS != "windows" {
		l.Close()
		return nil, err
	}
	return l, nil
}

// Serve accepts connections on l and serves each with ServeConn until ctx
// is done; then it closes l and the connections and returns nil.
func Serve(ctx context.Context, l net.Listener, h Handler, opt Options) error {
	if opt.Transport == "" {
		switch l.Addr().Network() {
		case "unix":
			opt.Transport = "socket"
		default:
			opt.Transport = l.Addr().Network()
		}
	}
	ctx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	defer wg.Wait()
	defer cancel()
	go func() {
		<-ctx.Done()
		l.Close()
	}()

	var delay time.Duration
	for {
		conn, err := l.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if errors.Is(err, net.ErrClosed) {
				return err
			}
			// Out of files and the like: wait for them to free, as net/http does
			delay = min(max(2*delay, 5*time.Millisecond), time.Second)
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return nil
			}
			continue
		}
		delay = 0
		wg.Go(func() { ServeConn(ctx, conn, h, opt) })
	}
}
