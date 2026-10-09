package tmpl

import (
	"embed"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// The templates that come with AltHex (ANL-5)
//
//go:embed library/*.bt
var libraryFS embed.FS

// Template is a template file: built in or of the user
type Template struct {
	Name    string // from //! name, or the file name
	File    string // the file name; the path for the user's ones
	Meta    Meta
	Source  string
	Builtin bool

	once sync.Once
	prog *Program
	err  error
}

// Program compiles the template once
func (t *Template) Program() (*Program, error) {
	t.once.Do(func() { t.prog, t.err = Compile(t.File, t.Source) })
	return t.prog, t.err
}

func newTemplate(file, src string, builtin bool) *Template {
	t := &Template{File: file, Source: src, Builtin: builtin, Meta: parseMeta(src)}
	t.Name = t.Meta.Name
	if t.Name == "" {
		t.Name = strings.TrimSuffix(filepath.Base(file), filepath.Ext(file))
	}
	return t
}

var (
	libOnce sync.Once
	lib     []*Template
)

// Library returns the built-in templates, sorted by name
func Library() []*Template {
	libOnce.Do(func() {
		entries, _ := libraryFS.ReadDir("library")
		for _, e := range entries {
			src, err := libraryFS.ReadFile("library/" + e.Name())
			if err == nil {
				lib = append(lib, newTemplate(e.Name(), string(src), true))
			}
		}
		sortTemplates(lib)
	})
	return lib
}

// LoadDir reads the templates (*.bt) of a directory: the user's own; a
// missing directory has none
func LoadDir(dir string) []*Template {
	files, _ := filepath.Glob(filepath.Join(dir, "*.bt"))
	var out []*Template
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err == nil {
			out = append(out, newTemplate(f, string(src), false))
		}
	}
	sortTemplates(out)
	return out
}

func sortTemplates(ts []*Template) {
	sort.Slice(ts, func(i, j int) bool { return strings.ToLower(ts[i].Name) < strings.ToLower(ts[j].Name) })
}

// Detect chooses the template for the data (ANL-4): the ones whose magic
// bytes match, the highest priority first; else one for the extension of
// the file name. nil - none fits.
func Detect(ts []*Template, d Data, fileName string) *Template {
	var best *Template
	for _, t := range ts {
		if t.Meta.Matches(d) && (best == nil || t.Meta.Priority > best.Meta.Priority) {
			best = t
		}
	}
	if best != nil {
		return best
	}
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(fileName), "."))
	if ext == "" {
		return nil
	}
	for _, t := range ts {
		// Only when the template has no magic: a wrong file would fail it
		if len(t.Meta.Magic) > 0 {
			continue
		}
		for _, e := range t.Meta.Extensions {
			if e == ext {
				return t
			}
		}
	}
	return nil
}
