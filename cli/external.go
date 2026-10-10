package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/ipoluianov/althex/app"
	"github.com/ipoluianov/althex/config"
	"github.com/ipoluianov/althex/core/api"
	"github.com/ipoluianov/althex/core/tmpl"
	"github.com/ipoluianov/althex/rpc"
)

// The external interfaces as commands (EXT-6, EXT-7): an AI agent or a
// program starts "althex mcp" or "althex rpc" and talks over stdin and
// stdout. With the window running and its local API on, the calls go to
// the window: the documents open there, the user asked about changes.
// Without it the command works on the files it is given, read-only unless
// the one who starts it allows more.

// externalFlags are the flags of mcp and rpc
type externalFlags struct {
	headless, allowOpen, allowWrite bool
	http, token                     string
}

func parseExternal(name string, args []string) (externalFlags, []string, error) {
	var f externalFlags
	fs := newFlags(name)
	fs.BoolVar(&f.headless, "headless", false, "do not use the window even when it runs")
	fs.BoolVar(&f.allowOpen, "allow-open", false, "without the window: the clients may open other files")
	fs.BoolVar(&f.allowWrite, "allow-write", false, "without the window: the clients may edit and save the files")
	if name == "mcp" {
		fs.StringVar(&f.http, "http", "", "serve MCP over HTTP at this address (127.0.0.1:PORT) instead of stdio")
		fs.StringVar(&f.token, "token", os.Getenv("ALTHEX_MCP_TOKEN"), "the bearer token HTTP clients must send (or ALTHEX_MCP_TOKEN)")
	}
	if err := fs.Parse(args); err != nil {
		return f, nil, err
	}
	return f, fs.Args(), nil
}

// backend is where the calls go: the window, or the files of the command
func backend(ctx context.Context, f externalFlags, files []string) (rpc.Handler, func(), error) {
	if !f.headless {
		network, addr := config.APIEndpoint()
		if conn, err := rpc.Dial(network, addr, time.Second); err == nil {
			if len(files) > 0 {
				fmt.Fprintln(os.Stderr, "althex: the window runs; the files are opened by the clients (open_document)")
			}
			fwd := &forward{conn: conn}
			return fwd, func() { conn.Close() }, nil
		}
	}
	ws := &api.Files{AllowOpen: f.allowOpen, AllowWrite: f.allowWrite}
	for _, path := range files {
		if _, err := ws.Add(path); err != nil {
			ws.Close()
			return nil, nil, err
		}
	}
	return api.New(ws, allTemplates), ws.Close, nil
}

// forward sends the calls to the window over its local API, introducing
// the client by the name it gave this command
type forward struct {
	conn *rpc.Conn
	once sync.Once
}

func (f *forward) Call(ctx context.Context, method string, params json.RawMessage) (any, error) {
	f.once.Do(func() {
		cl := rpc.ClientFrom(ctx)
		cl.Name += " via althex mcp"
		f.conn.Initialize(ctx, cl)
	})
	var res json.RawMessage
	if err := f.conn.Call(ctx, method, params, &res); err != nil {
		var e *rpc.Error
		if errors.As(err, &e) {
			return nil, e
		}
		return nil, fmt.Errorf("the window: %w", err)
	}
	return res, nil
}

func allTemplates() []*tmpl.Template {
	all := append([]*tmpl.Template{}, tmpl.Library()...)
	return append(all, tmpl.LoadDir(config.TemplatesDirectory())...)
}

func runMCP(ctx context.Context, args []string) error {
	f, files, err := parseExternal("mcp", args)
	if err != nil {
		return err
	}
	h, closeFn, err := backend(ctx, f, files)
	if err != nil {
		return err
	}
	defer closeFn()
	// The tools are the same wherever the calls go
	tools := api.New(&api.Files{}, allTemplates).Tools()
	opt := rpc.Options{Name: app.DisplayName, Version: app.Version}
	mcp := rpc.NewMCP(opt, tools, h, api.Instructions)
	if f.http == "" {
		return mcp.ServeStream(ctx, os.Stdin, os.Stdout)
	}
	host, _, err := net.SplitHostPort(f.http)
	if err != nil {
		return err
	}
	if ip := net.ParseIP(host); host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return errors.New("-http takes local addresses only (127.0.0.1:PORT)")
	}
	if len(f.token) < 16 {
		return errors.New("-token of at least 16 characters is required with -http")
	}
	mux := http.NewServeMux()
	mux.Handle("/mcp", mcp.HTTPHandler(f.token))
	srv := &http.Server{Addr: f.http, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		srv.Close()
	}()
	fmt.Fprintf(os.Stderr, "althex: MCP at http://%s/mcp\n", f.http)
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func runRPC(ctx context.Context, args []string) error {
	f, files, err := parseExternal("rpc", args)
	if err != nil {
		return err
	}
	h, closeFn, err := backend(ctx, f, files)
	if err != nil {
		return err
	}
	defer closeFn()
	return rpc.ServeConn(ctx, stdio{}, h, rpc.Options{Name: app.DisplayName, Version: app.Version, Transport: "stdio"})
}

// stdio is stdin and stdout as one stream
type stdio struct{}

func (stdio) Read(p []byte) (int, error)  { return os.Stdin.Read(p) }
func (stdio) Write(p []byte) (int, error) { return os.Stdout.Write(p) }
func (stdio) Close() error                { return os.Stdin.Close() }
