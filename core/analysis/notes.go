package analysis

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
)

// Notes are what the user tells about a firmware: the names of functions
// and labels, comments, extra entry points, the device and the base, kept
// for the next time it is opened (FW-7)
type Notes struct {
	Device   string            `json:",omitempty"`
	Arch     string            `json:",omitempty"`
	Base     uint64            `json:",omitempty"`
	Labels   map[string]string `json:",omitempty"` // by address in hex
	Comments map[string]string `json:",omitempty"`
	Entries  []Entry           `json:",omitempty"`
}

func key(addr uint64) string { return strconv.FormatUint(addr, 16) }

// Label returns the user's name of an address
func (n *Notes) Label(addr uint64) (string, bool) {
	s, ok := n.Labels[key(addr)]
	return s, ok
}

func (n *Notes) SetLabel(addr uint64, name string) {
	if n.Labels == nil {
		n.Labels = make(map[string]string)
	}
	if name == "" {
		delete(n.Labels, key(addr))
		return
	}
	n.Labels[key(addr)] = name
}

func (n *Notes) Comment(addr uint64) string { return n.Comments[key(addr)] }

func (n *Notes) SetComment(addr uint64, text string) {
	if n.Comments == nil {
		n.Comments = make(map[string]string)
	}
	if text == "" {
		delete(n.Comments, key(addr))
		return
	}
	n.Comments[key(addr)] = text
}

// CommentMap returns the comments by address, for WriteAsm
func (n *Notes) CommentMap() map[uint64]string {
	m := make(map[uint64]string, len(n.Comments))
	for k, v := range n.Comments {
		if a, err := strconv.ParseUint(k, 16, 64); err == nil {
			m[a] = v
		}
	}
	return m
}

// Apply puts the user's names into a result
func (n *Notes) Apply(r *Result) {
	for k, v := range n.Labels {
		if a, err := strconv.ParseUint(k, 16, 64); err == nil {
			if v = Ident(v); v != "" {
				r.Labels[a] = v
			}
		}
	}
}

// NotesPath is where the notes of a file are kept: in dir, by the path of the file
func NotesPath(dir, file string) string {
	if abs, err := filepath.Abs(file); err == nil {
		file = abs
	}
	sum := sha256.Sum256([]byte(file))
	return filepath.Join(dir, hex.EncodeToString(sum[:12])+".json")
}

// LoadNotes reads notes; missing ones are empty
func LoadNotes(path string) *Notes {
	n := &Notes{}
	if bs, err := os.ReadFile(path); err == nil {
		json.Unmarshal(bs, n)
	}
	return n
}

// Save writes the notes
func (n *Notes) Save(path string) error {
	bs, err := json.MarshalIndent(n, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, bs, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
