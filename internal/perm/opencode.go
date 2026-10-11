package perm

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/auth-com-br/tend/internal/agentfile"
)

// opencodeAgent is OpenCode's `permission` (1.18.35): per tool, an action
// (allow, ask, deny) or a map of patterns to actions; and the whole of it
// can be one action. OpenCode applies the LAST rule that matches, so the
// map's order is its meaning: tend keeps it (agentfile.Object), adds new
// rules at the end — where they win — and says when a rule is covered by
// one after it.
//
// A rule is shown as tool or tool(pattern): `bash(git *)`.
type opencodeAgent struct{ backups string }

func newOpenCode(backups string) Adapter { return &opencodeAgent{backups: backups} }

func (o *opencodeAgent) ID() string   { return "opencode" }
func (o *opencodeAgent) Name() string { return "OpenCode" }

// opencodeFlat are the tools whose permission is one action, never a map
// of patterns.
var opencodeFlat = map[string]bool{"todowrite": true, "question": true, "webfetch": true, "websearch": true, "doom_loop": true}

func opencodeConfigDir() string {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, "opencode")
	}
	return filepath.Join(home(), ".config", "opencode")
}

// files are the global config and the project's, from the repository's
// root down to the pane's folder: opencode.json, opencode.jsonc, and the
// same under .opencode. Of the ones not there, the plain opencode.json is
// offered, as the place a rule would go.
func (o *opencodeAgent) files(p Place) []File {
	var files []File
	add := func(dir string, scope Scope, offer bool) {
		found := false
		for _, name := range []string{"opencode.json", "opencode.jsonc", filepath.Join(".opencode", "opencode.json"), filepath.Join(".opencode", "opencode.jsonc")} {
			path := filepath.Join(dir, name)
			if exists(path) {
				files = append(files, File{Path: path, Scope: scope, Exists: true})
				found = true
			}
		}
		if !found && offer {
			files = append(files, File{Path: filepath.Join(dir, "opencode.json"), Scope: scope})
		}
	}
	add(opencodeConfigDir(), Global, true)
	for i, d := range projectDirs(p) {
		add(d, Project, i == 0)
	}
	files = dedupe(files)
	for i, f := range files {
		if !f.Exists {
			continue
		}
		if data, err := os.ReadFile(f.Path); err == nil {
			if _, err := agentfile.ParseObject(data); err != nil {
				files[i].ReadOnly = true
				files[i].Note = "has comments, which tend would lose: edit it in an editor"
			}
		}
	}
	return files
}

// opencodeRule is one rule of the permission object.
type opencodeRule struct {
	tool, pattern string
	action        Decision
	// whole is the permission set as one action for everything.
	whole bool
}

func (r opencodeRule) text() string {
	if r.whole {
		return "*"
	}
	if r.pattern == "" {
		return r.tool
	}
	return r.tool + "(" + r.pattern + ")"
}

// parseOpencodeText reads tool or tool(pattern).
func parseOpencodeText(text string) (tool, pattern string) {
	if i := strings.Index(text, "("); i > 0 && strings.HasSuffix(text, ")") {
		return text[:i], text[i+1 : len(text)-1]
	}
	return text, ""
}

// opencodeRules is a config's permission as rules, in its order.
func opencodeRules(top *agentfile.Object) ([]opencodeRule, error) {
	raw, ok := top.Get("permission")
	if !ok {
		return nil, nil
	}
	var whole string
	if json.Unmarshal(raw, &whole) == nil {
		return []opencodeRule{{whole: true, action: Decision(whole)}}, nil
	}
	perm, err := agentfile.ObjectOf(raw)
	if err != nil {
		return nil, fmt.Errorf("permission is neither an action nor an object")
	}
	var out []opencodeRule
	for _, tool := range perm.Keys() {
		v, _ := perm.Get(tool)
		var action string
		if json.Unmarshal(v, &action) == nil {
			out = append(out, opencodeRule{tool: tool, action: Decision(action)})
			continue
		}
		patterns, err := agentfile.ObjectOf(v)
		if err != nil {
			continue
		}
		for _, pat := range patterns.Keys() {
			pv, _ := patterns.Get(pat)
			if json.Unmarshal(pv, &action) == nil {
				out = append(out, opencodeRule{tool: tool, pattern: pat, action: Decision(action)})
			}
		}
	}
	return out, nil
}

// opencodePattern is OpenCode's wildcard as a regexp: * is anything, ? one
// character, and a trailing " *" also matches the command alone ("git *"
// matches "git").
func opencodePattern(p string) *regexp.Regexp {
	trail := strings.HasSuffix(p, " *")
	if trail {
		p = strings.TrimSuffix(p, " *")
	}
	q := regexp.QuoteMeta(p)
	q = strings.ReplaceAll(q, `\*`, ".*")
	q = strings.ReplaceAll(q, `\?`, ".")
	if trail {
		q += "( .*)?"
	}
	re, err := regexp.Compile("^" + q + "$")
	if err != nil {
		return nil
	}
	return re
}

// shadowed is the later rule of the same tool that matches all a rule
// matches, so OpenCode never applies the earlier one; "" when none does.
// Only the plain case is told: a later pattern that matches the earlier
// pattern's own text.
func shadowed(rules []opencodeRule, i int) string {
	r := rules[i]
	if r.whole || r.pattern == "" {
		return ""
	}
	for _, later := range rules[i+1:] {
		if later.tool != r.tool || later.pattern == "" || later.action == r.action {
			continue
		}
		if re := opencodePattern(later.pattern); re != nil && re.MatchString(r.pattern) {
			return fmt.Sprintf("covered by %s after it: OpenCode applies the last rule that matches", later.text())
		}
	}
	return ""
}

func (o *opencodeAgent) Read(p Place) Report {
	r := Report{
		Files:     o.files(p),
		Decisions: []Decision{Allow, Ask, Deny},
		Effect:    "OpenCode reads it in its next session",
	}
	var errs []string
	for _, f := range r.Files {
		if !f.Exists {
			continue
		}
		data, err := os.ReadFile(f.Path)
		if err != nil {
			errs = append(errs, err.Error())
			continue
		}
		top, err := agentfile.ParseObject(data)
		if err != nil {
			// A JSONC file: listed, read-only, its rules not shown.
			continue
		}
		rules, err := opencodeRules(top)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", f.Path, err))
			continue
		}
		for i, rule := range rules {
			r.Rules = append(r.Rules, Rule{Agent: "opencode", File: f.Path, Scope: f.Scope, Decision: rule.action, Text: rule.text(), ReadOnly: f.ReadOnly, Why: shadowed(rules, i)})
		}
	}
	if len(errs) > 0 {
		r.Error = strings.Join(errs, "; ")
	}
	return r
}

func (o *opencodeAgent) Check(text string) error {
	if text == "" {
		return errors.New("the rule is empty")
	}
	tool, pattern := parseOpencodeText(text)
	if tool == "" || strings.ContainsAny(tool, " ()") {
		return errors.New("a rule is tool or tool(pattern): bash(git *), edit, webfetch")
	}
	if pattern != "" && opencodeFlat[tool] {
		return fmt.Errorf("%s takes one action, without a pattern", tool)
	}
	return nil
}

// edit changes the permission object of a file and nothing else in it.
func (o *opencodeAgent) edit(file string, change func(perm *agentfile.Object) error) error {
	return agentfile.Edit(file, options(o.backups, "opencode", file, 0o644), func(data []byte) ([]byte, error) {
		top, err := agentfile.ParseObject(data)
		if err != nil {
			return nil, err
		}
		raw, _ := top.Get("permission")
		var whole string
		var perm *agentfile.Object
		if json.Unmarshal(raw, &whole) == nil {
			// One action for everything is the same as "*" first, which
			// rules can then follow.
			perm, _ = agentfile.ObjectOf(nil)
			v, _ := agentfile.Marshal(whole)
			perm.Set("*", v)
		} else if perm, err = agentfile.ObjectOf(raw); err != nil {
			return nil, errors.New("permission is neither an action nor an object; change it by hand")
		}
		if err := change(perm); err != nil {
			return nil, err
		}
		if perm.Len() == 0 {
			top.Delete("permission")
		} else {
			out, err := perm.Raw()
			if err != nil {
				return nil, err
			}
			top.Set("permission", out)
		}
		return top.Bytes()
	})
}

func action(d Decision) (json.RawMessage, error) {
	switch d {
	case Allow, Ask, Deny:
		return agentfile.Marshal(string(d))
	}
	return nil, fmt.Errorf("OpenCode: %q: %w", d, ErrDecision)
}

func (o *opencodeAgent) Add(file string, d Decision, text string) error {
	v, err := action(d)
	if err != nil {
		return err
	}
	tool, pattern := parseOpencodeText(text)
	return o.edit(file, func(perm *agentfile.Object) error {
		cur, has := perm.Get(tool)
		var flat string
		isFlat := has && json.Unmarshal(cur, &flat) == nil
		if pattern == "" {
			if !has || isFlat {
				if has {
					return ErrExists
				}
				perm.Set(tool, v)
				return nil
			}
			// The tool has patterns: the tool alone is its "*", first, so
			// the patterns after it still win.
			patterns, err := agentfile.ObjectOf(cur)
			if err != nil {
				return err
			}
			if _, ok := patterns.Get("*"); ok {
				return ErrExists
			}
			first, _ := agentfile.ObjectOf(nil)
			first.Set("*", v)
			for _, k := range patterns.Keys() {
				pv, _ := patterns.Get(k)
				first.Set(k, pv)
			}
			out, err := first.Raw()
			if err != nil {
				return err
			}
			perm.Set(tool, out)
			return nil
		}
		patterns, _ := agentfile.ObjectOf(nil)
		if isFlat {
			// A tool with one action gets patterns: the action becomes its
			// "*", first, and means what it meant.
			patterns.Set("*", cur)
		} else if has {
			if patterns, err = agentfile.ObjectOf(cur); err != nil {
				return err
			}
		}
		if _, ok := patterns.Get(pattern); ok {
			return ErrExists
		}
		patterns.Set(pattern, v)
		out, err := patterns.Raw()
		if err != nil {
			return err
		}
		perm.Set(tool, out)
		return nil
	})
}

// find calls f with the object that holds a rule and the rule's key in it,
// once the rule is found with the decision expected.
func (o *opencodeAgent) find(perm *agentfile.Object, text string, d Decision, f func(holder *agentfile.Object, key string) error) error {
	tool, pattern := parseOpencodeText(text)
	cur, ok := perm.Get(tool)
	if !ok {
		return ErrNoRule
	}
	var flat string
	if json.Unmarshal(cur, &flat) == nil {
		if pattern != "" || Decision(flat) != d {
			return ErrNoRule
		}
		return f(perm, tool)
	}
	patterns, err := agentfile.ObjectOf(cur)
	if err != nil {
		return ErrNoRule
	}
	pv, ok := patterns.Get(pattern)
	if !ok || json.Unmarshal(pv, &flat) != nil || Decision(flat) != d {
		return ErrNoRule
	}
	if err := f(patterns, pattern); err != nil {
		return err
	}
	if patterns.Len() == 0 {
		perm.Delete(tool)
		return nil
	}
	out, err := patterns.Raw()
	if err != nil {
		return err
	}
	perm.Set(tool, out)
	return nil
}

func (o *opencodeAgent) Remove(file string, d Decision, text string) error {
	return o.edit(file, func(perm *agentfile.Object) error {
		return o.find(perm, text, d, func(holder *agentfile.Object, key string) error {
			holder.Delete(key)
			return nil
		})
	})
}

func (o *opencodeAgent) SetDecision(file, text string, from, to Decision) error {
	v, err := action(to)
	if err != nil {
		return err
	}
	return o.edit(file, func(perm *agentfile.Object) error {
		return o.find(perm, text, from, func(holder *agentfile.Object, key string) error {
			holder.Set(key, v) // in its place: its place is its meaning
			return nil
		})
	})
}

func (o *opencodeAgent) SetMode(file, key, value string) error {
	return fmt.Errorf("OpenCode: %q: %w", key, ErrNoMode)
}
