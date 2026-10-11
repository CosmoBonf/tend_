package perm

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/auth-com-br/tend/internal/agentfile"
)

// listAgent is an agent whose rules are lists of strings in a JSON file, a
// list per decision: Claude Code (permissions.allow/ask/deny), Cursor
// (permissions.allow/deny), Gemini (tools.allowed/confirmationRequired/
// exclude). Its modes are strings in the same files.
type listAgent struct {
	id, name string
	backups  string
	files    func(Place) []File
	// lists is where each decision's list is: a path of keys.
	lists     map[Decision][]string
	decisions []Decision
	modes     []modeSpec
	check     func(string) error
	// why says why a rule does not do what it seems to, or "".
	why func(text string) string
	// warn adds the agent-wide warnings once the files are read.
	warn     func(r *Report)
	effect   string
	fileMode os.FileMode
}

// modeSpec is one mode: where it is, what it takes, and in which scopes of
// file it is offered.
type modeSpec struct {
	path    []string
	choices []string
	help    string
	scopes  []Scope
}

func (a *listAgent) ID() string   { return a.id }
func (a *listAgent) Name() string { return a.name }

func (a *listAgent) Read(p Place) Report {
	r := Report{Files: a.files(p), Decisions: a.decisions, Effect: a.effect}
	var errs []string
	for _, f := range r.Files {
		if !f.Exists {
			for _, m := range a.modes {
				if hasScope(m.scopes, f.Scope) {
					r.Modes = append(r.Modes, Mode{Agent: a.id, File: f.Path, Scope: f.Scope, Key: strings.Join(m.path, "."), Choices: m.choices, Help: m.help})
				}
			}
			continue
		}
		data, err := os.ReadFile(f.Path)
		if err != nil {
			errs = append(errs, err.Error())
			continue
		}
		top, err := agentfile.ParseObject(data)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s %v", f.Path, err))
			continue
		}
		for _, d := range a.decisions {
			for _, text := range listAt(top, a.lists[d]) {
				rule := Rule{Agent: a.id, File: f.Path, Scope: f.Scope, Decision: d, Text: text, ReadOnly: f.ReadOnly}
				if a.why != nil {
					rule.Why = a.why(text)
				}
				r.Rules = append(r.Rules, rule)
			}
		}
		for _, m := range a.modes {
			if hasScope(m.scopes, f.Scope) {
				r.Modes = append(r.Modes, Mode{Agent: a.id, File: f.Path, Scope: f.Scope, Key: strings.Join(m.path, "."), Value: stringAt(top, m.path), Choices: m.choices, Help: m.help})
			}
		}
	}
	if len(errs) > 0 {
		r.Error = strings.Join(errs, "; ")
	}
	if a.warn != nil {
		a.warn(&r)
	}
	return r
}

func hasScope(scopes []Scope, s Scope) bool {
	if len(scopes) == 0 {
		return true
	}
	for _, v := range scopes {
		if v == s {
			return true
		}
	}
	return false
}

func (a *listAgent) Check(text string) error {
	if text == "" {
		return errors.New("the rule is empty")
	}
	if strings.ContainsAny(text, "\n\r") {
		return errors.New("a rule is one line")
	}
	if a.check != nil {
		return a.check(text)
	}
	return nil
}

func (a *listAgent) path(d Decision) ([]string, error) {
	p, ok := a.lists[d]
	if !ok {
		return nil, fmt.Errorf("%s: %q: %w", a.name, d, ErrDecision)
	}
	return p, nil
}

// edit changes one list of a file, and nothing else in it.
func (a *listAgent) edit(file string, d Decision, change func([]string) ([]string, error)) error {
	keys, err := a.path(d)
	if err != nil {
		return err
	}
	return agentfile.Edit(file, options(a.backups, a.id, file, a.fileMode), func(data []byte) ([]byte, error) {
		top, err := agentfile.ParseObject(data)
		if err != nil {
			return nil, err
		}
		list, err := change(listAt(top, keys))
		if err != nil {
			return nil, err
		}
		if err := setAt(top, keys, list); err != nil {
			return nil, err
		}
		return top.Bytes()
	})
}

func (a *listAgent) Add(file string, d Decision, text string) error {
	return a.edit(file, d, func(list []string) ([]string, error) {
		for _, v := range list {
			if v == text {
				return nil, ErrExists
			}
		}
		return append(list, text), nil
	})
}

func (a *listAgent) Remove(file string, d Decision, text string) error {
	return a.edit(file, d, func(list []string) ([]string, error) {
		out, ok := removeString(list, text)
		if !ok {
			return nil, ErrNoRule
		}
		return out, nil
	})
}

// SetDecision moves a rule from one list to the other in one write: two
// writes would leave a moment with the rule in neither.
func (a *listAgent) SetDecision(file, text string, from, to Decision) error {
	fromKeys, err := a.path(from)
	if err != nil {
		return err
	}
	toKeys, err := a.path(to)
	if err != nil {
		return err
	}
	return agentfile.Edit(file, options(a.backups, a.id, file, a.fileMode), func(data []byte) ([]byte, error) {
		top, err := agentfile.ParseObject(data)
		if err != nil {
			return nil, err
		}
		src, ok := removeString(listAt(top, fromKeys), text)
		if !ok {
			return nil, ErrNoRule
		}
		if err := setAt(top, fromKeys, src); err != nil {
			return nil, err
		}
		dst := listAt(top, toKeys)
		if _, there := removeString(dst, text); !there {
			dst = append(dst, text)
		}
		if err := setAt(top, toKeys, dst); err != nil {
			return nil, err
		}
		return top.Bytes()
	})
}

func (a *listAgent) SetMode(file, key, value string) error {
	var spec *modeSpec
	for i := range a.modes {
		if strings.Join(a.modes[i].path, ".") == key {
			spec = &a.modes[i]
		}
	}
	if spec == nil {
		return fmt.Errorf("%s: %q: %w", a.name, key, ErrNoMode)
	}
	if value != "" && !contains(spec.choices, value) {
		return fmt.Errorf("%s takes %s, not %q", key, strings.Join(spec.choices, ", "), value)
	}
	return agentfile.Edit(file, options(a.backups, a.id, file, a.fileMode), func(data []byte) ([]byte, error) {
		top, err := agentfile.ParseObject(data)
		if err != nil {
			return nil, err
		}
		var raw json.RawMessage
		if value != "" {
			raw, _ = agentfile.Marshal(value)
		}
		if err := setRaw(top, spec.path, raw); err != nil {
			return nil, err
		}
		return top.Bytes()
	})
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// --- paths of keys in a JSON object -------------------------------------

// listAt is the list of strings at a path of keys; empty when a key is
// missing or a value is not a list of strings — such a value is left alone,
// and the rules in it are not shown rather than shown wrong.
func listAt(top *agentfile.Object, keys []string) []string {
	raw, ok := rawAt(top, keys)
	if !ok {
		return nil
	}
	var list []string
	if json.Unmarshal(raw, &list) != nil {
		return nil
	}
	return list
}

// stringAt is the string at a path of keys, "" when there is none.
func stringAt(top *agentfile.Object, keys []string) string {
	raw, ok := rawAt(top, keys)
	if !ok {
		return ""
	}
	var s string
	_ = json.Unmarshal(raw, &s)
	return s
}

func rawAt(top *agentfile.Object, keys []string) (json.RawMessage, bool) {
	o := top
	for i, k := range keys {
		raw, ok := o.Get(k)
		if !ok {
			return nil, false
		}
		if i == len(keys)-1 {
			return raw, true
		}
		next, err := agentfile.ObjectOf(raw)
		if err != nil {
			return nil, false
		}
		o = next
	}
	return nil, false
}

// setAt writes a list at a path of keys. An empty list is taken out, and
// so is an object it leaves empty: a rule added and removed again leaves
// the file as it was.
func setAt(top *agentfile.Object, keys []string, list []string) error {
	if len(list) == 0 {
		return setRaw(top, keys, nil)
	}
	raw, err := agentfile.Marshal(list)
	if err != nil {
		return err
	}
	return setRaw(top, keys, raw)
}

// setRaw sets a value at a path of keys, making the objects on the way;
// a nil value removes the key, and the objects it leaves empty.
func setRaw(o *agentfile.Object, keys []string, raw json.RawMessage) error {
	k := keys[0]
	if len(keys) == 1 {
		if raw == nil {
			o.Delete(k)
		} else {
			o.Set(k, raw)
		}
		return nil
	}
	cur, _ := o.Get(k)
	child, err := agentfile.ObjectOf(cur)
	if err != nil {
		return fmt.Errorf("%q is not an object; change it by hand: %w", k, err)
	}
	if err := setRaw(child, keys[1:], raw); err != nil {
		return err
	}
	if child.Len() == 0 {
		o.Delete(k)
		return nil
	}
	childRaw, err := child.Raw()
	if err != nil {
		return err
	}
	o.Set(k, childRaw)
	return nil
}

// --- Claude Code ----------------------------------------------------------

// claudeDir is Claude Code's settings directory: $CLAUDE_CONFIG_DIR, else
// ~/.claude.
func claudeDir() string {
	if dir := os.Getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		return dir
	}
	return filepath.Join(home(), ".claude")
}

// claudeModes are Claude Code's permission modes, from its settings schema
// (2.1.292): "manual" is taken as an alias of default, and not offered.
var claudeModes = []string{"default", "acceptEdits", "plan", "auto", "dontAsk", "bypassPermissions"}

func newClaude(backups string) Adapter {
	return &listAgent{
		id: "claude", name: "Claude Code", backups: backups, fileMode: 0o644,
		files: func(p Place) []File {
			dir := claudeDir()
			files := []File{
				{Path: filepath.Join(dir, "settings.json"), Scope: Global},
			}
			// Claude reads a project's settings from the folder it starts
			// in. The pane's folder and its repository's root are both
			// offered when they differ: which one Claude was started in is
			// the person's to know.
			for _, d := range uniqueDirs(p.Root, p.Dir) {
				files = append(files,
					File{Path: filepath.Join(d, ".claude", "settings.json"), Scope: Project, Note: "shared: committed with the project"},
					File{Path: filepath.Join(d, ".claude", "settings.local.json"), Scope: Local, Note: "yours: Claude writes \"always allow\" here"},
				)
			}
			// Claude's own local file in the home directory is what it
			// reads when started there, and where its "always allow" went
			// all along for someone who starts it in ~.
			if h := home(); h != "" {
				files = append(files, File{Path: filepath.Join(h, ".claude", "settings.local.json"), Scope: Local, Note: "applies when Claude starts in your home folder"})
			}
			return withExists(dedupe(files))
		},
		lists: map[Decision][]string{
			Allow: {"permissions", "allow"},
			Ask:   {"permissions", "ask"},
			Deny:  {"permissions", "deny"},
		},
		decisions: []Decision{Allow, Ask, Deny},
		modes: []modeSpec{{
			path: []string{"permissions", "defaultMode"}, choices: claudeModes,
			help: "how Claude asks when no rule matches",
		}},
		check:  checkClaude,
		why:    whyClaude,
		effect: "Claude Code reads the change at once, in sessions already open",
	}
}

// checkClaude is Claude Code's own validation of a rule, as far as it
// refuses one: Tool or Tool(content), the tool capitalised; mcp__ rules
// without parentheses; no empty parentheses; no :* alone.
func checkClaude(text string) error {
	if strings.HasPrefix(text, "mcp__") {
		if strings.ContainsAny(text, "()") {
			return errors.New("MCP rules take no parentheses: mcp__server or mcp__server__tool")
		}
		return nil
	}
	name, rest, paren := strings.Cut(text, "(")
	if name == "" || name[0] < 'A' || name[0] > 'Z' {
		return errors.New("a rule starts with the tool's name, capitalised: Bash(...), Read(...), WebFetch")
	}
	if strings.ContainsAny(name, " )") {
		return errors.New("a rule is Tool or Tool(content)")
	}
	if !paren {
		return nil
	}
	if !strings.HasSuffix(rest, ")") {
		return errors.New("the rule's parenthesis is not closed")
	}
	content := strings.TrimSuffix(rest, ")")
	if strings.TrimSpace(content) == "" {
		return errors.New("empty parentheses: write the tool alone to match all of it")
	}
	if content == ":*" {
		return errors.New("the prefix before :* is empty")
	}
	return nil
}

// whyClaude is what Claude Code warns about a rule it reads but does not
// apply as written.
func whyClaude(text string) string {
	name, _, _ := strings.Cut(text, "(")
	switch name {
	case "Write", "NotebookEdit", "MultiEdit":
		return "Claude checks file edits against Edit(...) rules, not " + name + "(...)"
	case "Glob", "Grep", "LS":
		if strings.Contains(text, "(") {
			return "Claude checks file reads against Read(...) rules, not " + name + "(...)"
		}
	}
	if !strings.HasPrefix(name, "Bash") && strings.Contains(text, ":*") {
		return `":*" is for Bash rules; files take globs (Read(src/**))`
	}
	return ""
}

// --- Cursor -----------------------------------------------------------------

func cursorDir() string {
	if dir := os.Getenv("CURSOR_CONFIG_DIR"); dir != "" {
		return dir
	}
	return filepath.Join(home(), ".cursor")
}

func newCursor(backups string) Adapter {
	return &listAgent{
		id: "cursor", name: "Cursor", backups: backups, fileMode: 0o644,
		files: func(p Place) []File {
			files := []File{{Path: filepath.Join(cursorDir(), "cli-config.json"), Scope: Global}}
			// cursor-agent merges every .cursor/cli.json from the
			// repository's root down to the folder it runs in.
			for _, d := range projectDirs(p) {
				files = append(files, File{Path: filepath.Join(d, ".cursor", "cli.json"), Scope: Project})
			}
			return withExists(dedupe(files))
		},
		lists: map[Decision][]string{
			Allow: {"permissions", "allow"},
			Deny:  {"permissions", "deny"},
		},
		decisions: []Decision{Allow, Deny},
		modes: []modeSpec{{
			path: []string{"approvalMode"}, choices: []string{"allowlist", "unrestricted", "auto-review"},
			help: "allowlist: ask unless a rule allows; unrestricted: never ask (the list is not read)",
		}},
		check: func(text string) error {
			for _, kind := range []string{"Shell(", "Bash(", "Read(", "Write(", "Mcp(", "WebFetch("} {
				if strings.HasPrefix(text, kind) && strings.HasSuffix(text, ")") {
					return nil
				}
			}
			return errors.New("Cursor takes Shell(cmd), Read(glob), Write(glob), Mcp(server:tool) or WebFetch(domain)")
		},
		warn: func(r *Report) {
			// The mode that applies: a project's file overrides the
			// user's, as cursor-agent merges them.
			mode := ""
			for _, m := range r.Modes {
				if m.Value != "" {
					mode = m.Value
				}
			}
			if mode != "" && mode != "allowlist" {
				r.Warnings = append(r.Warnings, fmt.Sprintf("approvalMode is %q: Cursor does not read the rules below while it is", mode))
			}
		},
		effect: "Cursor reads it in its next session",
	}
}

// --- Gemini CLI -------------------------------------------------------------

// geminiDir is Gemini CLI's settings directory: $GEMINI_CLI_HOME/.gemini,
// else ~/.gemini.
func geminiDir() string {
	h := os.Getenv("GEMINI_CLI_HOME")
	if h == "" {
		h = home()
	}
	return filepath.Join(h, ".gemini")
}

func newGemini(backups string) Adapter {
	lists := &listAgent{
		id: "gemini", name: "Gemini CLI", backups: backups, fileMode: 0o644,
		files: func(p Place) []File {
			files := []File{{Path: filepath.Join(geminiDir(), "settings.json"), Scope: Global}}
			if p.Root != "" {
				files = append(files, File{Path: filepath.Join(p.Root, ".gemini", "settings.json"), Scope: Project})
			}
			return withExists(dedupe(files))
		},
		// tools.allowed skips the confirmation; confirmationRequired asks
		// always; exclude hides the tool from the model.
		lists: map[Decision][]string{
			Allow: {"tools", "allowed"},
			Ask:   {"tools", "confirmationRequired"},
			Deny:  {"tools", "exclude"},
		},
		decisions: []Decision{Allow, Ask, Deny},
		modes: []modeSpec{{
			path: []string{"general", "defaultApprovalMode"}, choices: []string{"default", "auto_edit", "plan"},
			help: "how Gemini asks when no rule matches",
		}},
		check: func(text string) error {
			if strings.HasPrefix(text, "(") {
				return errors.New("a rule starts with the tool's name: run_shell_command(git)")
			}
			return nil
		},
		effect: "Gemini reads it in its next session",
	}
	return &geminiAgent{listAgent: lists}
}

// uniqueDirs is the directories given, without repeats or empties.
func uniqueDirs(dirs ...string) []string {
	var out []string
	for _, d := range dirs {
		if d != "" && !contains(out, d) {
			out = append(out, d)
		}
	}
	return out
}

// withExists fills in whether each file is there.
func withExists(files []File) []File {
	for i := range files {
		files[i].Exists = exists(files[i].Path)
	}
	return files
}
