package perm

import (
	"errors"
	"path/filepath"
	"strings"
)

// extraAgent is an agent tend has no adapter for, named in the config with
// the files that hold its permissions: the panel lists them and opens them
// in an editor, and writes none of them. It is how a harness tend does not
// know yet gets a place in the panel before it gets an adapter.
type extraAgent struct {
	name  string
	files []Extra
}

func extraAdapters(extras []Extra) []Adapter {
	var out []Adapter
	byName := map[string]*extraAgent{}
	for _, e := range extras {
		name := strings.TrimSpace(e.Agent)
		if name == "" || strings.TrimSpace(e.Path) == "" {
			continue
		}
		a, ok := byName[name]
		if !ok {
			a = &extraAgent{name: name}
			byName[name] = a
			out = append(out, a)
		}
		a.files = append(a.files, e)
	}
	return out
}

func (e *extraAgent) ID() string   { return "extra:" + e.name }
func (e *extraAgent) Name() string { return e.name }

func (e *extraAgent) Read(Place) Report {
	r := Report{Warnings: []string{"tend has no adapter for " + e.name + ": open its files in an editor"}}
	for _, f := range e.files {
		path := f.Path
		if rest, ok := strings.CutPrefix(path, "~/"); ok {
			path = filepath.Join(home(), rest)
		}
		scope := f.Scope
		if scope == "" {
			scope = Global
		}
		r.Files = append(r.Files, File{Path: path, Scope: scope, Exists: exists(path), ReadOnly: true, Note: "from tend's config"})
	}
	return r
}

var errExtra = errors.New("tend has no adapter for this agent; open its file in an editor")

func (e *extraAgent) Check(string) error                                   { return errExtra }
func (e *extraAgent) Add(string, Decision, string) error                   { return errExtra }
func (e *extraAgent) Remove(string, Decision, string) error                { return errExtra }
func (e *extraAgent) SetDecision(string, string, Decision, Decision) error { return errExtra }
func (e *extraAgent) SetMode(string, string, string) error                 { return errExtra }
