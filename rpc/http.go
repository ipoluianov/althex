package rpc

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
)

// maxSessions bounds the HTTP sessions kept: a client that never ends its
// session must not grow the memory forever. The least recently used goes.
const maxSessions = 256

const sessionHeader = "Mcp-Session-Id"

// HTTPHandler serves the Streamable HTTP transport of MCP at one endpoint
// (mount it at /mcp). Replies are plain JSON, without SSE. A session starts
// at initialize and is named by the Mcp-Session-Id header from then on;
// DELETE ends it. Requests from a web page of another site (an Origin
// other than localhost) are refused, against DNS rebinding. When token is
// not empty, each request must carry "Authorization: Bearer <token>".
func (m *MCP) HTTPHandler(token string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !localOrigin(r.Header.Get("Origin")) {
			http.Error(w, "origin not allowed", http.StatusForbidden)
			return
		}
		if token != "" && !validToken(r.Header.Get("Authorization"), token) {
			w.Header().Set("WWW-Authenticate", "Bearer")
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if v := r.Header.Get("MCP-Protocol-Version"); v != "" && !slices.Contains(protocolVersions, v) {
			http.Error(w, "unsupported MCP-Protocol-Version", http.StatusBadRequest)
			return
		}
		switch r.Method {
		case http.MethodPost:
			m.post(w, r)
		case http.MethodDelete:
			id := r.Header.Get(sessionHeader)
			if id == "" {
				http.Error(w, "missing "+sessionHeader, http.StatusBadRequest)
				return
			}
			m.mu.Lock()
			s := m.sessions[id]
			delete(m.sessions, id)
			m.mu.Unlock()
			if s == nil {
				http.Error(w, "unknown session", http.StatusNotFound)
				return
			}
			s.proc.cancelAll()
			w.WriteHeader(http.StatusNoContent)
		default:
			w.Header().Set("Allow", "POST, DELETE")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
}

func (m *MCP) post(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxMessage))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			http.Error(w, "message too large", http.StatusRequestEntityTooLarge)
		} else {
			http.Error(w, "cannot read the request", http.StatusBadRequest)
		}
		return
	}
	if !json.Valid(body) {
		writeJSON(w, http.StatusBadRequest, encodeError(nil, &Error{Code: ParseError, Message: "parse error"}))
		return
	}

	var s *session
	id := r.Header.Get(sessionHeader)
	switch {
	case id != "":
		m.mu.Lock()
		s = m.sessions[id]
		m.mu.Unlock()
		if s == nil {
			http.Error(w, "unknown session", http.StatusNotFound)
			return
		}
	case hasInitialize(body):
		s = m.newSession("http")
		id = rand.Text()
		m.addSession(id, s)
	default:
		http.Error(w, "missing "+sessionHeader, http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	s.lastUsed = time.Now()
	s.mu.Unlock()

	w.Header().Set(sessionHeader, id)
	out := s.proc.process(r.Context(), body)
	if out == nil {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func writeJSON(w http.ResponseWriter, status int, b []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	w.Write(b)
}

func (m *MCP) addSession(id string, s *session) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.sessions) >= maxSessions {
		var oldestID string
		var oldest time.Time
		for k, v := range m.sessions {
			v.mu.Lock()
			t := v.lastUsed
			v.mu.Unlock()
			if oldestID == "" || t.Before(oldest) {
				oldestID, oldest = k, t
			}
		}
		m.sessions[oldestID].proc.cancelAll()
		delete(m.sessions, oldestID)
	}
	m.sessions[id] = s
}

// hasInitialize tells whether the message or batch holds an initialize request
func hasInitialize(body []byte) bool {
	type msg struct {
		Method string `json:"method"`
	}
	var one msg
	if json.Unmarshal(body, &one) == nil {
		return one.Method == "initialize"
	}
	var batch []msg
	if json.Unmarshal(body, &batch) == nil {
		for _, m := range batch {
			if m.Method == "initialize" {
				return true
			}
		}
	}
	return false
}

// localOrigin tells whether a request may come from a page at origin: no
// origin (not a browser) or a page served from this computer
func localOrigin(origin string) bool {
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return false
	}
	switch u.Hostname() {
	case "localhost", "127.0.0.1", "::1":
		return true
	}
	return false
}

// validToken checks the Authorization header in constant time
func validToken(header, token string) bool {
	got, ok := strings.CutPrefix(header, "Bearer ")
	if !ok {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(strings.TrimSpace(got)), []byte(token)) == 1
}
