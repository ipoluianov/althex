package api

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ipoluianov/althex/core/codec"
	"github.com/ipoluianov/althex/core/doc"
	checksum "github.com/ipoluianov/althex/core/hash"
	"github.com/ipoluianov/althex/core/search"
	"github.com/ipoluianov/althex/core/tmpl"
	"github.com/ipoluianov/althex/rpc"
)

// Limits of one call
const (
	maxRead      = 1 << 20  // bytes read at once
	maxDump      = 64 << 10 // bytes read as a dump
	maxEditBytes = 16 << 20 // bytes of one proposal
	maxHits      = 10000
	maxChildren  = 1000
	maxDepth     = 8
)

// Service runs the tools over a workspace; it is the rpc.Handler of the
// transports
type Service struct {
	ws        Workspace
	templates func() []*tmpl.Template

	mu      sync.Mutex
	results map[string]*applied // the trees of the templates applied, by document
}

// applied is a template run over a snapshot of a document
type applied struct {
	snap *doc.Snapshot
	tm   *tmpl.Template
	res  *tmpl.Result
}

// New makes the service of a workspace; templates gives the templates to
// choose from (the built-in and the user's ones)
func New(ws Workspace, templates func() []*tmpl.Template) *Service {
	return &Service{ws: ws, templates: templates, results: make(map[string]*applied)}
}

// tool is a tool of the service with its schema
type tool struct {
	rpc.Tool
	run func(s *Service, ctx context.Context, params json.RawMessage) (any, error)
}

// Tools are the tools for MCP's tools/list; the local API takes the same names
func (s *Service) Tools() []rpc.Tool {
	out := make([]rpc.Tool, len(tools))
	for i, t := range tools {
		out[i] = t.Tool
	}
	return out
}

// Call runs a tool (rpc.Handler)
func (s *Service) Call(ctx context.Context, method string, params json.RawMessage) (any, error) {
	for _, t := range tools {
		if t.Name == method {
			if len(params) == 0 || string(params) == "null" {
				params = json.RawMessage("{}")
			}
			res, err := t.run(s, ctx, params)
			return res, rpcError(err)
		}
	}
	return nil, &rpc.Error{Code: rpc.MethodNotFound, Message: "no method " + method}
}

func sprintf(format string, args ...any) string { return fmt.Sprintf(format, args...) }

// decode reads the parameters of a call
func decode(params json.RawMessage, v any) error {
	if err := json.Unmarshal(params, v); err != nil {
		return invalid("parameters: %v", err)
	}
	return nil
}

// docParam is the document a call is about: an id from list_documents;
// "" - the current one
type docParam struct {
	Document string `json:"document"`
}

func (s *Service) snapshot(id string) (*doc.Snapshot, DocInfo, error) {
	if id == "" {
		for _, d := range s.ws.Documents() {
			if d.Current {
				id = d.ID
				break
			}
		}
		if id == "" {
			return nil, DocInfo{}, fmt.Errorf("%w: no document is open", ErrNotFound)
		}
	}
	return s.ws.Snapshot(id)
}

// span checks a range of a document: length 0 - to the end
func span(off, n, size int64) (int64, int64, error) {
	if off < 0 || off > size {
		return 0, 0, invalid("offset %d is outside the document (size %d)", off, size)
	}
	if n <= 0 || n > size-off {
		n = size - off
	}
	return off, n, nil
}

// ---- list_documents, open_document, show ----

func listDocuments(s *Service, ctx context.Context, params json.RawMessage) (any, error) {
	return map[string]any{"documents": s.ws.Documents()}, nil
}

func openDocument(s *Service, ctx context.Context, params json.RawMessage) (any, error) {
	var p struct {
		Path string `json:"path"`
	}
	if err := decode(params, &p); err != nil {
		return nil, err
	}
	if p.Path == "" {
		return nil, invalid("path is required")
	}
	return s.ws.Open(ctx, rpc.ClientFrom(ctx), p.Path)
}

func show(s *Service, ctx context.Context, params json.RawMessage) (any, error) {
	var p struct {
		docParam
		Offset int64 `json:"offset"`
		Length int64 `json:"length"`
	}
	if err := decode(params, &p); err != nil {
		return nil, err
	}
	snap, info, err := s.snapshot(p.Document)
	if err != nil {
		return nil, err
	}
	if p.Offset < 0 || p.Offset > snap.Size() || p.Length < 0 || p.Length > snap.Size()-p.Offset {
		return nil, invalid("the range is outside the document (size %d)", snap.Size())
	}
	return map[string]any{"shown": s.ws.Show(info.ID, p.Offset, p.Length)}, nil
}

// ---- read ----

func read(s *Service, ctx context.Context, params json.RawMessage) (any, error) {
	var p struct {
		docParam
		Offset   int64  `json:"offset"`
		Length   int64  `json:"length"`
		Format   string `json:"format"`
		Encoding string `json:"encoding"`
	}
	p.Length = 256
	if err := decode(params, &p); err != nil {
		return nil, err
	}
	snap, info, err := s.snapshot(p.Document)
	if err != nil {
		return nil, err
	}
	if p.Format == "" {
		p.Format = "dump"
	}
	limit := int64(maxRead)
	if p.Format == "dump" {
		limit = maxDump
	}
	if p.Length > limit {
		return nil, tooLarge("at most %d bytes as %s at once", limit, p.Format)
	}
	if p.Length <= 0 {
		return nil, invalid("length must be positive")
	}
	off, n, err := span(p.Offset, p.Length, snap.Size())
	if err != nil {
		return nil, err
	}
	b := make([]byte, n)
	got, err := snap.ReadAt(b, off)
	if err != nil && err != io.EOF {
		return nil, err
	}
	b = b[:got]
	out := map[string]any{"document": info.ID, "offset": off, "length": len(b), "format": p.Format}
	switch p.Format {
	case "dump":
		out["data"] = dump(b, off)
	case "hex":
		out["data"] = hex.EncodeToString(b)
	case "base64":
		out["data"] = base64.StdEncoding.EncodeToString(b)
	case "text":
		enc := encoding(p.Encoding)
		if enc == nil {
			return nil, invalid("unknown encoding %q", p.Encoding)
		}
		out["data"] = decodeText(enc, b)
	default:
		return nil, invalid("format is dump, hex, base64 or text")
	}
	mark := make([]bool, len(b))
	if snap.Unreadable(off, mark) {
		var bad [][2]int64
		for i := 0; i < len(mark); {
			if !mark[i] {
				i++
				continue
			}
			j := i
			for j < len(mark) && mark[j] {
				j++
			}
			bad = append(bad, [2]int64{off + int64(i), int64(j - i)})
			i = j
		}
		out["unreadable"] = bad // offset, length: bad sectors, read as zeros
	}
	return out, nil
}

// dump is the classic hex dump: offset, 16 bytes, their ASCII
func dump(b []byte, off int64) string {
	var sb strings.Builder
	for i := 0; i < len(b); i += 16 {
		line := b[i:min(i+16, len(b))]
		fmt.Fprintf(&sb, "%08X  ", off+int64(i))
		for j := 0; j < 16; j++ {
			if j < len(line) {
				fmt.Fprintf(&sb, "%02X ", line[j])
			} else {
				sb.WriteString("   ")
			}
			if j == 7 {
				sb.WriteByte(' ')
			}
		}
		sb.WriteString(" |")
		for _, c := range line {
			if c < 0x20 || c > 0x7E {
				c = '.'
			}
			sb.WriteByte(c)
		}
		sb.WriteString("|\n")
	}
	return sb.String()
}

// encoding finds an encoding by its name as people write it: "UTF-8",
// "utf16le", "cp1251"; "" - UTF-8
func encoding(name string) codec.Encoding {
	norm := func(s string) string {
		return strings.NewReplacer("-", "", "_", "", " ", "").Replace(strings.ToLower(s))
	}
	if name == "" {
		name = "utf8"
	}
	for _, e := range codec.Encodings() {
		if norm(e.ID()) == norm(name) || norm(e.Name()) == norm(name) {
			return e
		}
	}
	return nil
}

// decodeText is the text of the bytes; what is not a printable character
// becomes \xNN
func decodeText(enc codec.Encoding, b []byte) string {
	var sb strings.Builder
	for i := 0; i < len(b); {
		r, n, ok := enc.Decode(b, i)
		switch {
		case ok:
			sb.WriteRune(r)
		case b[i] == '\n' || b[i] == '\r' || b[i] == '\t':
			sb.WriteByte(b[i])
			n = 1
		default:
			fmt.Fprintf(&sb, "\\x%02X", b[i])
			n = 1
		}
		i += max(n, 1)
	}
	return sb.String()
}

// ---- search ----

func find(s *Service, ctx context.Context, params json.RawMessage) (any, error) {
	var p struct {
		docParam
		Hex        string `json:"hex"`
		Text       string `json:"text"`
		Encoding   string `json:"encoding"`
		IgnoreCase bool   `json:"ignore_case"`
		From       int64  `json:"from"`
		To         int64  `json:"to"`
		Align      int    `json:"align"`
		MaxResults int    `json:"max_results"`
	}
	if err := decode(params, &p); err != nil {
		return nil, err
	}
	snap, info, err := s.snapshot(p.Document)
	if err != nil {
		return nil, err
	}
	var pat search.Pattern
	switch {
	case p.Hex != "" && p.Text != "":
		return nil, invalid("give hex or text, not both")
	case p.Hex != "":
		pat, err = search.ParseHex(p.Hex)
	case p.Text != "":
		enc := encoding(p.Encoding)
		if enc == nil {
			return nil, invalid("unknown encoding %q", p.Encoding)
		}
		pat, err = search.Text(p.Text, enc.Encode, p.IgnoreCase)
	default:
		return nil, invalid("hex or text is required")
	}
	if err != nil {
		return nil, invalid("pattern: %v", err)
	}
	if p.MaxResults <= 0 {
		p.MaxResults = 100
	}
	p.MaxResults = min(p.MaxResults, maxHits)
	if p.From < 0 || p.From > snap.Size() || p.To < 0 || p.To > snap.Size() {
		return nil, invalid("from and to must be inside the document (size %d)", snap.Size())
	}
	start := time.Now()
	hits, truncated, err := search.Collect(ctx, snap, snap.Size(), pat,
		search.Options{From: p.From, To: p.To, Align: p.Align}, p.MaxResults, nil)
	if err != nil {
		return nil, err
	}
	offs := make([]int64, len(hits))
	for i, h := range hits {
		offs[i] = h.Off
	}
	return map[string]any{"document": info.ID, "length": pat.Len(), "offsets": offs,
		"truncated": truncated, "milliseconds": time.Since(start).Milliseconds()}, nil
}

// ---- hash ----

func hashes(s *Service, ctx context.Context, params json.RawMessage) (any, error) {
	var p struct {
		docParam
		Algorithms []string `json:"algorithms"`
		Offset     int64    `json:"offset"`
		Length     int64    `json:"length"`
	}
	if err := decode(params, &p); err != nil {
		return nil, err
	}
	snap, info, err := s.snapshot(p.Document)
	if err != nil {
		return nil, err
	}
	if len(p.Algorithms) == 0 {
		p.Algorithms = []string{"crc32", "md5", "sha256"}
	}
	var algs []checksum.Algorithm
	for _, id := range p.Algorithms {
		a, ok := checksum.ByID(strings.ToLower(id))
		if !ok {
			return nil, invalid("unknown algorithm %q; known: %s", id, strings.Join(algorithmIDs(), ", "))
		}
		algs = append(algs, a)
	}
	off, n, err := span(p.Offset, p.Length, snap.Size())
	if err != nil {
		return nil, err
	}
	sums, err := checksum.Compute(ctx, snap, off, n, algs, nil)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for i, a := range algs {
		out[a.ID] = hex.EncodeToString(sums[i])
	}
	return map[string]any{"document": info.ID, "offset": off, "length": n, "hashes": out}, nil
}

func algorithmIDs() []string {
	var ids []string
	for _, a := range checksum.Algorithms() {
		ids = append(ids, a.ID)
	}
	return ids
}

// ---- templates ----

func listTemplates(s *Service, ctx context.Context, params json.RawMessage) (any, error) {
	var p docParam
	if err := decode(params, &p); err != nil {
		return nil, err
	}
	type entry struct {
		Name       string   `json:"name"`
		Extensions []string `json:"extensions,omitempty"`
		Builtin    bool     `json:"builtin"`
	}
	var list []entry
	for _, t := range s.templates() {
		list = append(list, entry{t.Name, t.Meta.Extensions, t.Builtin})
	}
	sort.Slice(list, func(i, j int) bool { return strings.ToLower(list[i].Name) < strings.ToLower(list[j].Name) })
	out := map[string]any{"templates": list}
	if p.Document != "" {
		if snap, info, err := s.snapshot(p.Document); err == nil {
			if t := tmpl.Detect(s.templates(), snap, info.Path+info.Name); t != nil {
				out["detected"] = t.Name
			}
		}
	}
	return out, nil
}

func applyTemplate(s *Service, ctx context.Context, params json.RawMessage) (any, error) {
	var p struct {
		docParam
		Template string `json:"template"`
		Start    int64  `json:"start"`
	}
	if err := decode(params, &p); err != nil {
		return nil, err
	}
	a, err := s.apply(ctx, p.Document, p.Template, p.Start)
	if err != nil {
		return nil, err
	}
	return a.summary(), nil
}

// apply runs a template over a document (name "" - the one that fits) and
// keeps its tree for get_structure
func (s *Service) apply(ctx context.Context, id, name string, start int64) (*applied, error) {
	snap, info, err := s.snapshot(id)
	if err != nil {
		return nil, err
	}
	var tm *tmpl.Template
	if name == "" {
		if tm = tmpl.Detect(s.templates(), snap, info.Path+info.Name); tm == nil {
			return nil, fmt.Errorf("%w: no template fits %s; list_templates names them", ErrNotFound, info.Name)
		}
	} else {
		for _, t := range s.templates() {
			if strings.EqualFold(t.Name, name) || strings.EqualFold(t.File, name) {
				tm = t
				break
			}
		}
		if tm == nil {
			return nil, fmt.Errorf("%w: no template %q", ErrNotFound, name)
		}
	}
	prog, err := tm.Program()
	if err != nil {
		return nil, fmt.Errorf("template %s: %v", tm.Name, err)
	}
	if start < 0 || start > snap.Size() {
		return nil, invalid("start %d is outside the document", start)
	}
	// Writer nil: a template applied from outside does not write
	res := prog.Run(ctx, tmpl.Env{Data: snap, Start: start, SelStart: info.SelStart, SelSize: info.SelSize, Cursor: info.Cursor})
	a := &applied{snap: snap, tm: tm, res: res}
	s.mu.Lock()
	s.results[info.ID] = a
	s.mu.Unlock()
	return a, nil
}

func (a *applied) summary() map[string]any {
	out := map[string]any{
		"template":     a.tm.Name,
		"fields":       a.res.Nodes,
		"milliseconds": a.res.Duration.Milliseconds(),
	}
	if a.res.Err != nil {
		out["error"] = a.res.Err.Error() // the fields read before it stay
	}
	if len(a.res.Warnings) > 0 {
		out["warnings"] = a.res.Warnings
	}
	if len(a.res.Output) > 0 {
		lines := a.res.Output
		if len(lines) > 100 {
			lines = append(lines[:100:100], fmt.Sprintf("... %d lines more", len(a.res.Output)-100))
		}
		out["output"] = lines
	}
	if a.res.Root != nil {
		out["root"] = nodeJSON(a.res.Root, 1, 50)
	}
	return out
}

func getStructure(s *Service, ctx context.Context, params json.RawMessage) (any, error) {
	var p struct {
		docParam
		Path        string `json:"path"`
		Offset      *int64 `json:"offset"`
		Depth       int    `json:"depth"`
		MaxChildren int    `json:"max_children"`
	}
	p.Depth, p.MaxChildren = 2, 100
	if err := decode(params, &p); err != nil {
		return nil, err
	}
	snap, info, err := s.snapshot(p.Document)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	a := s.results[info.ID]
	s.mu.Unlock()
	if a == nil || a.snap != snap {
		// Not applied yet, or the document changed since: the same template again
		name := ""
		if a != nil {
			name = a.tm.Name
		}
		if a, err = s.apply(ctx, info.ID, name, 0); err != nil {
			return nil, err
		}
	}
	if a.res.Root == nil {
		return nil, fmt.Errorf("template %s made no tree: %v", a.tm.Name, a.res.Err)
	}
	n := a.res.Root
	switch {
	case p.Offset != nil:
		if n = n.NodeAt(*p.Offset); n == nil {
			return nil, fmt.Errorf("%w: no field at offset %d", ErrNotFound, *p.Offset)
		}
	case p.Path != "":
		if n = n.Find(p.Path); n == nil {
			return nil, fmt.Errorf("%w: no field %q", ErrNotFound, p.Path)
		}
	}
	depth := min(max(p.Depth, 0), maxDepth)
	children := min(max(p.MaxChildren, 1), maxChildren)
	return map[string]any{"document": info.ID, "template": a.tm.Name, "node": nodeJSON(n, depth, children)}, nil
}

// nodeJSON is a field of a tree with its children down to depth
func nodeJSON(n *tmpl.Node, depth, maxChildren int) map[string]any {
	m := map[string]any{
		"name":   n.Name,
		"path":   n.Path(),
		"type":   n.TypeName,
		"offset": n.Off,
		"size":   n.Size,
	}
	if v := n.Value(); v != "" {
		m["value"] = v
	}
	if n.Comment != "" {
		m["comment"] = n.Comment
	}
	count := n.ChildCount()
	if count > 0 {
		m["children_count"] = count
	}
	if depth > 0 && count > 0 {
		var kids []map[string]any
		for i := int64(0); i < count && i < int64(maxChildren); i++ {
			if c := n.Child(i); c != nil && !c.Hidden {
				kids = append(kids, nodeJSON(c, depth-1, maxChildren))
			}
		}
		m["children"] = kids
		if count > int64(maxChildren) {
			m["children_truncated"] = true
		}
	}
	return m
}

// ---- edits ----

func proposeEdits(s *Service, ctx context.Context, params json.RawMessage) (any, error) {
	var p struct {
		docParam
		Description string `json:"description"`
		Edits       []struct {
			Offset    int64   `json:"offset"`
			Overwrite *string `json:"overwrite"`
			Insert    *string `json:"insert"`
			Delete    int64   `json:"delete"`
		} `json:"edits"`
	}
	if err := decode(params, &p); err != nil {
		return nil, err
	}
	snap, info, err := s.snapshot(p.Document)
	if err != nil {
		return nil, err
	}
	if len(p.Edits) == 0 {
		return nil, invalid("edits are required")
	}
	// Checked against the size as the edits before change it
	size, total := snap.Size(), 0
	prop := Proposal{Doc: info.ID, Description: p.Description}
	for i, e := range p.Edits {
		var ed Edit
		ed.Offset = e.Offset
		kinds := 0
		if e.Overwrite != nil {
			if ed.Overwrite, err = hexBytes(*e.Overwrite); err != nil {
				return nil, invalid("edit %d: overwrite: %v", i, err)
			}
			kinds++
		}
		if e.Insert != nil {
			if ed.Insert, err = hexBytes(*e.Insert); err != nil {
				return nil, invalid("edit %d: insert: %v", i, err)
			}
			kinds++
		}
		if e.Delete != 0 {
			ed.Delete = e.Delete
			kinds++
		}
		if kinds != 1 {
			return nil, invalid("edit %d: give one of overwrite, insert, delete", i)
		}
		total += len(ed.Overwrite) + len(ed.Insert)
		switch {
		case e.Offset < 0 || e.Offset > size:
			return nil, invalid("edit %d: offset %d is outside the document (size %d then)", i, e.Offset, size)
		case ed.Overwrite != nil && int64(len(ed.Overwrite)) > size-e.Offset:
			return nil, invalid("edit %d: overwrite goes past the end; insert the rest", i)
		case ed.Delete < 0 || ed.Delete > size-e.Offset:
			return nil, invalid("edit %d: delete goes past the end", i)
		case ed.Overwrite != nil && len(ed.Overwrite) == 0 || ed.Insert != nil && len(ed.Insert) == 0:
			return nil, invalid("edit %d: no bytes", i)
		}
		size += int64(len(ed.Insert)) - ed.Delete
		prop.Edits = append(prop.Edits, ed)
	}
	if total > maxEditBytes {
		return nil, tooLarge("at most %d bytes in one proposal", maxEditBytes)
	}
	info, err = s.ws.Apply(ctx, rpc.ClientFrom(ctx), prop)
	if err != nil {
		return nil, err
	}
	return map[string]any{"status": "applied", "document": info}, nil
}

func save(s *Service, ctx context.Context, params json.RawMessage) (any, error) {
	var p docParam
	if err := decode(params, &p); err != nil {
		return nil, err
	}
	_, info, err := s.snapshot(p.Document)
	if err != nil {
		return nil, err
	}
	info, err = s.ws.Save(ctx, rpc.ClientFrom(ctx), info.ID)
	if err != nil {
		return nil, err
	}
	return map[string]any{"status": "saved", "document": info}, nil
}

// hexBytes reads bytes written in hex, spaces allowed
func hexBytes(s string) ([]byte, error) {
	s = strings.Map(func(r rune) rune {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == ',' {
			return -1
		}
		return r
	}, strings.TrimPrefix(strings.TrimPrefix(s, "0x"), "0X"))
	return hex.DecodeString(s)
}
