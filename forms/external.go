package forms

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ipoluianov/althex/app"
	"github.com/ipoluianov/althex/config"
	"github.com/ipoluianov/althex/core/api"
	"github.com/ipoluianov/althex/core/doc"
	"github.com/ipoluianov/althex/forms/hexview"
	"github.com/ipoluianov/althex/rpc"
	"github.com/ipoluianov/nui/ui"
)

// external serves the interfaces for other programs and AI agents (EXT-6,
// 7, 8): the local JSON-RPC API on a socket of the user and MCP over HTTP
// on 127.0.0.1, both off until switched on. The window is their workspace:
// its tabs are the documents, and what changes anything is asked of the user.
type external struct {
	c   *MainForm
	svc *api.Service

	mu       sync.Mutex
	cancel   context.CancelFunc
	listener net.Listener
	server   *http.Server
	running  string // what runs, for the settings dialog
	nextID   int
}

// askTimeout is how long a question waits for the user: then it is declined
const askTimeout = 5 * time.Minute

func newExternal(c *MainForm) *external {
	e := &external{c: c}
	e.svc = api.New(e, templates)
	return e
}

// restart starts what the settings switch on and stops the rest
func (e *external) restart() {
	e.stop()
	s := config.GetSettings().External
	if !s.API && !s.MCPHTTP {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	e.cancel = cancel
	opt := rpc.Options{Name: app.DisplayName, Version: app.Version}
	var running []string
	if s.API {
		network, addr := config.APIEndpoint()
		var l net.Listener
		var err error
		if network == "pipe" {
			l, err = rpc.ListenPipe(addr)
		} else if err = os.MkdirAll(filepath.Dir(addr), 0o700); err == nil {
			l, err = rpc.ListenUnix(addr)
		}
		if err != nil {
			ui.ShowToast(e.c, T().ExtStartFailed(err.Error()), ui.ToastError)
		} else {
			e.listener = l
			opt.Transport = network
			if network == "unix" {
				opt.Transport = "socket"
			}
			go rpc.Serve(ctx, l, e.svc, opt)
			running = append(running, addr)
		}
	}
	if s.MCPHTTP {
		port := s.MCPPort
		if port <= 0 {
			port = config.DefaultMCPPort
		}
		addr := fmt.Sprintf("127.0.0.1:%d", port)
		l, err := net.Listen("tcp", addr)
		if err != nil {
			ui.ShowToast(e.c, T().ExtStartFailed(err.Error()), ui.ToastError)
		} else {
			mcp := rpc.NewMCP(rpc.Options{Name: app.DisplayName, Version: app.Version}, e.svc.Tools(), e.svc, api.Instructions)
			mux := http.NewServeMux()
			mux.Handle("/mcp", mcp.HTTPHandler(s.Token))
			e.server = &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
			go e.server.Serve(l)
			running = append(running, "http://"+addr+"/mcp")
		}
	}
	e.running = strings.Join(running, ", ")
}

func (e *external) stop() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.cancel != nil {
		e.cancel()
		e.cancel = nil
	}
	if e.listener != nil {
		e.listener.Close()
		e.listener = nil
	}
	if e.server != nil {
		e.server.Close()
		e.server = nil
	}
	e.running = ""
}

// sync runs f on the goroutine of the window and waits for it
func (e *external) sync(f func()) error {
	form := e.c.Form()
	if form == nil {
		return errors.New("the window is closed")
	}
	done := make(chan struct{})
	form.Invoke(func() {
		defer close(done)
		f()
	})
	select {
	case <-done:
		return nil
	case <-time.After(30 * time.Second):
		return errors.New("the window does not answer")
	}
}

// tabID gives a tab the id the clients know it by
func (e *external) tabID(t *Tab) string {
	if t.apiID == "" {
		e.nextID++
		t.apiID = fmt.Sprint(e.nextID)
	}
	return t.apiID
}

// info is a tab as the clients see it; on the goroutine of the window
func (e *external) info(t *Tab) api.DocInfo {
	v := t.view()
	selStart, selSize := v.Selection()
	return api.DocInfo{ID: e.tabID(t), Name: t.name(), Path: t.doc.Path(), Size: t.doc.Size(),
		Modified: t.doc.Modified(), ReadOnly: t.doc.ReadOnly(), Current: t == e.c.tab(),
		Cursor: v.Caret(), SelStart: selStart, SelSize: selSize}
}

// find is the tab of a document id; on the goroutine of the window
func (e *external) find(id string) *Tab {
	for _, t := range e.c.tabs {
		if e.tabID(t) == id {
			return t
		}
	}
	return nil
}

func (e *external) Documents() []api.DocInfo {
	var out []api.DocInfo
	e.sync(func() {
		for _, t := range e.c.tabs {
			out = append(out, e.info(t))
		}
	})
	return out
}

func (e *external) Snapshot(id string) (*doc.Snapshot, api.DocInfo, error) {
	var snap *doc.Snapshot
	var info api.DocInfo
	err := e.sync(func() {
		if t := e.find(id); t != nil {
			snap, info = t.doc.Snapshot(), e.info(t)
		}
	})
	if err == nil && snap == nil {
		err = fmt.Errorf("%w: no document %q", api.ErrNotFound, id)
	}
	return snap, info, err
}

// ask shows a question of a client and waits for the answer; yes runs on
// the goroutine of the window when the user agrees
func (e *external) ask(ctx context.Context, client rpc.Client, text, okText string, yes func() error) error {
	answer := make(chan error, 1)
	var dlg *AskDialog
	err := e.sync(func() {
		dlg = NewAskDialog(T().ExtAskTitle(clientName(client)), text, okText,
			func() { answer <- yes() },
			func() { answer <- api.ErrRejected })
		e.c.BringToFront()
		e.c.ShowDialog(dlg)
	})
	if err != nil {
		return err
	}
	select {
	case err := <-answer:
		return err
	case <-ctx.Done():
	case <-time.After(askTimeout):
	}
	// Gone or not answered: the question goes away, declined
	e.c.Form().Invoke(func() {
		if f := dlg.Form(); f != nil {
			f.Close()
		}
	})
	return api.ErrRejected
}

func clientName(c rpc.Client) string {
	name := c.Name
	if name == "" || name == "unknown" {
		name = T().ExtUnknownClient
	}
	if c.Transport != "" {
		name += " (" + c.Transport + ")"
	}
	return name
}

func (e *external) Open(ctx context.Context, client rpc.Client, path string) (api.DocInfo, error) {
	var info api.DocInfo
	err := e.ask(ctx, client, T().ExtAskOpen(path), T().MenuOpen, func() error {
		e.c.OpenFile(path)
		if t := e.c.tab(); t != nil && t.doc.Path() != "" && samePath(t.doc.Path(), path) {
			info = e.info(t)
			return nil
		}
		return fmt.Errorf("%s was not opened", path)
	})
	return info, err
}

func samePath(a, b string) bool {
	return strings.EqualFold(strings.TrimRight(a, `/\`), strings.TrimRight(b, `/\`))
}

func (e *external) Apply(ctx context.Context, client rpc.Client, p api.Proposal) (api.DocInfo, error) {
	var info api.DocInfo
	var check error
	var name string
	e.sync(func() {
		t := e.find(p.Doc)
		switch {
		case t == nil:
			check = fmt.Errorf("%w: no document %q", api.ErrNotFound, p.Doc)
		case t.doc.ReadOnly():
			check = fmt.Errorf("%w: %s is read-only", api.ErrForbidden, t.name())
		case t.doc.IsDevice():
			check = fmt.Errorf("%w: disks are not written from outside", api.ErrForbidden)
		}
		if t != nil {
			name = t.name()
		}
	})
	if check != nil {
		return info, check
	}
	apply := func() error {
		t := e.find(p.Doc)
		if t == nil {
			return fmt.Errorf("%w: the document was closed", api.ErrNotFound)
		}
		if err := api.ApplyEdits(t.doc, p, T().ExtUndoKind(client.Name)); err != nil {
			return err
		}
		t.markExternal(p)
		info = e.info(t)
		e.c.updateStatus()
		ui.ShowToast(e.c, T().ExtApplied(clientName(client)), ui.ToastInfo)
		return nil
	}
	if config.GetSettings().External.TrustEdits {
		var err error
		e.sync(func() { err = apply() })
		return info, err
	}
	err := e.ask(ctx, client, T().ExtAskEdits(name, p.Description, describeEdits(p.Edits)), T().ExtApply, apply)
	return info, err
}

// describeEdits lists the edits for the question: the first ones in full
func describeEdits(edits []api.Edit) string {
	var sb strings.Builder
	var total int
	for i, e := range edits {
		total += len(e.Overwrite) + len(e.Insert)
		if i >= 12 {
			continue
		}
		switch {
		case e.Overwrite != nil:
			sb.WriteString(T().ExtOverwrite(len(e.Overwrite), e.Offset) + ": " + previewBytes(e.Overwrite))
		case e.Insert != nil:
			sb.WriteString(T().ExtInsert(len(e.Insert), e.Offset) + ": " + previewBytes(e.Insert))
		default:
			sb.WriteString(T().ExtDelete(e.Delete, e.Offset))
		}
		sb.WriteByte('\n')
	}
	if len(edits) > 12 {
		sb.WriteString(T().ExtMoreEdits(len(edits)-12, total) + "\n")
	}
	return sb.String()
}

func previewBytes(b []byte) string {
	s := strings.ToUpper(hex.EncodeToString(b[:min(len(b), 16)]))
	var sb strings.Builder
	for i := 0; i < len(s); i += 2 {
		if i > 0 {
			sb.WriteByte(' ')
		}
		sb.WriteString(s[i : i+2])
	}
	if len(b) > 16 {
		sb.WriteString(" …")
	}
	return sb.String()
}

func (e *external) Save(ctx context.Context, client rpc.Client, id string) (api.DocInfo, error) {
	var info api.DocInfo
	var name string
	var check error
	e.sync(func() {
		t := e.find(id)
		switch {
		case t == nil:
			check = fmt.Errorf("%w: no document %q", api.ErrNotFound, id)
		case t.doc.Path() == "":
			check = fmt.Errorf("%w: a new document is saved by the user (Save As)", api.ErrForbidden)
		case t.doc.IsDevice():
			check = fmt.Errorf("%w: disks are not written from outside", api.ErrForbidden)
		default:
			name = t.name()
		}
	})
	if check != nil {
		return info, check
	}
	saved := make(chan error, 1)
	err := e.ask(ctx, client, T().ExtAskSave(name), T().Save, func() error {
		t := e.find(id)
		if t == nil {
			return fmt.Errorf("%w: the document was closed", api.ErrNotFound)
		}
		t.onSaveError = func(err error) {
			t.onSaveError = nil
			saved <- err
		}
		e.c.save(t, false, func() {
			t.onSaveError = nil
			info = e.info(t)
			saved <- nil
		})
		return nil
	})
	if err != nil {
		return info, err
	}
	// Saving runs in the background; it calls back when done
	select {
	case err = <-saved:
	case <-time.After(10 * time.Minute):
		err = errors.New("saving takes too long")
	}
	return info, err
}

func (e *external) Show(id string, off, n int64) bool {
	shown := false
	e.sync(func() {
		for i, t := range e.c.tabs {
			if e.tabID(t) == id {
				e.c.selectTab(i)
				t.view().Select(off, n)
				t.view().ScrollToOffset(off)
				shown = true
			}
		}
	})
	return shown
}

// ---- the marks of the edits from outside (EXT-8) ----

// markExternal remembers where the edits of a proposal went, to draw them
// until the document changes again
func (t *Tab) markExternal(p api.Proposal) {
	var marks []hexview.Highlight
	for _, e := range p.Edits {
		// The edits after one move what is behind them
		shift := int64(len(e.Insert)) - e.Delete
		for i := range marks {
			if marks[i].Off >= e.Offset {
				marks[i].Off += shift
			}
		}
		switch {
		case e.Overwrite != nil:
			marks = append(marks, hexview.Highlight{Off: e.Offset, Len: int64(len(e.Overwrite))})
		case e.Insert != nil:
			marks = append(marks, hexview.Highlight{Off: e.Offset, Len: int64(len(e.Insert))})
		default:
			marks = append(marks, hexview.Highlight{Off: e.Offset, Len: 1, Frame: true})
		}
	}
	for i := range marks {
		marks[i].Color = externalColor
		if !marks[i].Frame {
			marks[i].Color.A = 140
		}
	}
	t.extMarks, t.extSnap = marks, t.doc.Snapshot()
}

var externalColor = ui.ColorFromHex("#b05ce6")

// externalMarks are the marks of the last edits from outside while the
// document is as they left it
func (t *Tab) externalMarks(from, to int64) []hexview.Highlight {
	if t.extSnap == nil || t.doc.Snapshot() != t.extSnap {
		t.extMarks, t.extSnap = nil, nil
		return nil
	}
	var hs []hexview.Highlight
	for _, m := range t.extMarks {
		if m.Off < to && m.Off+m.Len > from {
			hs = append(hs, m)
		}
	}
	return hs
}

// ---- the dialogs ----

// AskDialog asks the user about a request of a client: yes or no
type AskDialog struct {
	ui.DialogContent
	answered bool
}

// NewAskDialog calls onYes or onNo (also when closed) on the goroutine of
// the window below
func NewAskDialog(title, text, yesText string, onYes, onNo func()) *AskDialog {
	c := &AskDialog{}
	c.InitWidget()
	// A label is one line
	lines := ui.NewPanel()
	lines.SetPanelPadding(0)
	for i, line := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
		lbl := ui.NewLabel(line)
		lbl.SetXExpandable(true)
		lines.AddWidget(i, 0, lbl)
	}
	c.AddWidget(0, 0, lines)
	c.AddWidget(1, 0, ui.NewVSpacer())
	buttons := ui.NewPanel()
	c.AddWidget(2, 0, buttons)
	answer := func(f func()) func() {
		return func() {
			if !c.answered {
				c.answered = true
				c.RunInParent(f)
			}
			c.Form().Close()
		}
	}
	yes := ui.NewButton(yesText)
	yes.SetOnClick(answer(onYes))
	no := ui.NewButton(T().ExtDecline)
	no.SetOnClick(answer(onNo))
	buttons.AddWidget(0, 0, ui.NewHSpacer())
	buttons.AddWidget(0, 1, yes)
	buttons.AddWidget(0, 2, no)
	c.OnDialogShow = func() {
		c.Form().SetTitle(title)
		lines := strings.Count(text, "\n") + 1
		c.Form().SetSize(620, min(140+lines*22, 600))
		c.Form().MoveToCenterOfParent()
		c.Form().SetCancelButton(no)
		// No accept button: Enter must not agree to a change by mistake
		c.Form().OnClose = func() bool {
			if !c.answered {
				c.answered = true
				c.RunInParent(onNo)
			}
			return true
		}
	}
	return c
}

// ExternalDialog switches the external interfaces
type ExternalDialog struct {
	ui.DialogContent
	chkAPI, chkHTTP, chkTrust *ui.Checkbox
	txtPort, txtToken         *ui.TextBox
	onAccept                  func(config.ExternalSettings)
}

func NewExternalDialog(s config.ExternalSettings, running string, onAccept func(config.ExternalSettings)) *ExternalDialog {
	c := &ExternalDialog{onAccept: onAccept}
	c.InitWidget()
	if s.Token == "" {
		s.Token = newToken()
	}
	if s.MCPPort <= 0 {
		s.MCPPort = config.DefaultMCPPort
	}
	p := ui.NewPanel()
	c.AddWidget(0, 0, p)
	c.chkAPI = ui.NewCheckbox(T().ExtAPI)
	c.chkAPI.SetChecked(s.API)
	p.AddWidget(0, 0, c.chkAPI)
	_, addr := config.APIEndpoint()
	p.AddWidget(1, 0, secondary(addr))

	c.chkHTTP = ui.NewCheckbox(T().ExtHTTP)
	c.chkHTTP.SetChecked(s.MCPHTTP)
	p.AddWidget(2, 0, c.chkHTTP)
	grid := ui.NewPanel()
	grid.SetPanelPadding(0)
	p.AddWidget(3, 0, grid)
	grid.AddWidget(0, 0, ui.NewLabel(T().ExtPort))
	c.txtPort = ui.NewTextBox()
	c.txtPort.SetText(fmt.Sprint(s.MCPPort))
	grid.AddWidget(0, 1, c.txtPort)
	grid.AddWidget(1, 0, ui.NewLabel(T().ExtToken))
	c.txtToken = ui.NewTextBox()
	c.txtToken.SetText(s.Token)
	c.txtToken.SetXExpandable(true)
	grid.AddWidget(1, 1, c.txtToken)
	btnNew := ui.NewButton(T().ExtNewToken)
	btnNew.SetOnClick(func() { c.txtToken.SetText(newToken()) })
	grid.AddWidget(1, 2, btnNew)
	btnCopy := ui.NewButton(T().ExtCopyToken)
	btnCopy.SetOnClick(func() {
		ui.ClipboardSetText(c.txtToken.Text())
		ui.ShowToast(c, T().Copied, ui.ToastInfo)
	})
	grid.AddWidget(1, 3, btnCopy)

	c.chkTrust = ui.NewCheckbox(T().ExtTrust)
	c.chkTrust.SetChecked(s.TrustEdits)
	p.AddWidget(4, 0, c.chkTrust)
	p.AddWidget(5, 0, secondary(T().ExtHint))
	if running != "" {
		p.AddWidget(6, 0, secondary(T().ExtRunning(running)))
	}

	c.AddWidget(1, 0, ui.NewVSpacer())
	buttons := ui.NewPanel()
	c.AddWidget(2, 0, buttons)
	ok := ui.NewButton(ui.UIText().OK)
	ok.SetOnClick(c.accept)
	cancel := ui.NewButton(ui.UIText().Cancel)
	cancel.SetOnClick(func() { c.Form().Close() })
	buttons.AddWidget(0, 0, ui.NewHSpacer())
	buttons.AddWidget(0, 1, ok)
	buttons.AddWidget(0, 2, cancel)
	c.OnDialogShow = func() {
		c.Form().SetTitle(T().MenuExternal)
		c.Form().SetSize(640, 400)
		c.Form().MoveToCenterOfParent()
		c.Form().SetAcceptButton(ok)
		c.Form().SetCancelButton(cancel)
	}
	return c
}

func secondary(text string) *ui.Label {
	l := ui.NewLabel(text)
	l.SetForegroundColor(colorSecondaryText.get())
	return l
}

func (c *ExternalDialog) accept() {
	var s config.ExternalSettings
	s.API = c.chkAPI.Checked()
	s.MCPHTTP = c.chkHTTP.Checked()
	s.TrustEdits = c.chkTrust.Checked()
	s.Token = strings.TrimSpace(c.txtToken.Text())
	port, ok := ParseOffset(c.txtPort.Text())
	if !ok || port < 1 || port > 65535 {
		ui.ShowMessageBox(c, T().Error, T().ExtBadPort)
		return
	}
	s.MCPPort = int(port)
	if s.MCPHTTP && len(s.Token) < 16 {
		ui.ShowMessageBox(c, T().Error, T().ExtShortToken)
		return
	}
	c.RunInParent(func() { c.onAccept(s) })
	c.Form().Close()
}

// newToken is a random token for MCP over HTTP
func newToken() string {
	var b [24]byte
	rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// StartExternal starts the external interfaces the settings switch on
func (c *MainForm) StartExternal() { c.external.restart() }

// ShowExternal opens the settings of the external interfaces
func (c *MainForm) ShowExternal() {
	e := c.external
	e.mu.Lock()
	running := e.running
	e.mu.Unlock()
	c.ShowDialog(NewExternalDialog(config.GetSettings().External, running, func(s config.ExternalSettings) {
		all := config.GetSettings()
		all.External = s
		if err := config.SetSettings(all); err != nil {
			ui.ShowMessageBox(c, T().Error, err.Error())
		}
		c.external.restart()
	}))
}
