package perm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/auth-com-br/tend/internal/agentfile"
)

// codexAgent is Codex CLI (0.160.1): its execpolicy rules — Starlark files
// in $CODEX_HOME/rules, every *.rules there, and a project's .codex/rules —
// and the modes in config.toml. Codex appends a line to default.rules each
// time "don't ask again for commands that start with …" is chosen, one
// prefix_rule a line; those are what tend edits, a line at a time, found by
// what they say. A rule written over several lines, or with match,
// justification or anything else, is shown and left to an editor.
//
// After a write the file is handed to `codex execpolicy check`, which
// parses it as Codex will; a file it refuses is put back as it was.
type codexAgent struct{ backups string }

func newCodex(backups string) Adapter { return &codexAgent{backups: backups} }

func (c *codexAgent) ID() string   { return "codex" }
func (c *codexAgent) Name() string { return "Codex" }

func codexHome() string {
	if dir := os.Getenv("CODEX_HOME"); dir != "" {
		return dir
	}
	return filepath.Join(home(), ".codex")
}

// codexDecision is each tend decision in Codex's words.
var codexDecision = map[Decision]string{Allow: "allow", Ask: "prompt", Deny: "forbidden"}

func fromCodex(word string) (Decision, bool) {
	for d, w := range codexDecision {
		if w == word {
			return d, true
		}
	}
	return "", false
}

func (c *codexAgent) files(p Place) []File {
	var files []File
	add := func(dir string, scope Scope, note string) {
		paths, _ := filepath.Glob(filepath.Join(dir, "*.rules"))
		sort.Strings(paths)
		def := filepath.Join(dir, "default.rules")
		if !contains(paths, def) {
			// Where Codex itself writes, and so where a rule goes.
			files = append(files, File{Path: def, Scope: scope, Note: note})
		}
		for _, path := range paths {
			files = append(files, File{Path: path, Scope: scope, Exists: true, Note: note})
		}
	}
	add(filepath.Join(codexHome(), "rules"), Global, "")
	if p.Root != "" {
		// Codex's --ignore-rules skips "user or project execpolicy .rules
		// files": a project's are in its .codex, next to its config.toml.
		add(filepath.Join(p.Root, ".codex", "rules"), Project, "")
	}
	config := filepath.Join(codexHome(), "config.toml")
	files = append(files, File{Path: config, Scope: Global, Exists: exists(config), ModesOnly: true, Note: "how Codex asks, and whether this project is trusted"})
	return dedupe(files)
}

// codexStatement is one statement of a rules file: the lines it spans and,
// when it is a one-line prefix_rule, what it says.
type codexStatement struct {
	start, end int // [start, end) in lines
	text       string
	decision   Decision
	editable   bool
}

// codexSimple is the one-line rule Codex writes itself.
var codexSimple = regexp.MustCompile(`^\s*prefix_rule\(\s*pattern\s*=\s*(\[.*\])\s*,\s*decision\s*=\s*"(\w+)"\s*,?\s*\)\s*$`)

// codexStatements splits a rules file into statements: a statement ends
// where its parentheses and brackets close, strings and comments aside.
func codexStatements(lines []string) []codexStatement {
	var out []codexStatement
	for i := 0; i < len(lines); {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			i++
			continue
		}
		start, depth := i, 0
		for ; i < len(lines); i++ {
			depth += codexDepth(lines[i])
			if depth <= 0 {
				i++
				break
			}
		}
		st := codexStatement{start: start, end: i}
		joined := strings.Join(lines[start:i], " ")
		if i-start == 1 {
			if m := codexSimple.FindStringSubmatch(lines[start]); m != nil {
				if text, ok := codexPatternText(m[1]); ok {
					if d, ok := fromCodex(m[2]); ok {
						st.text, st.decision, st.editable = text, d, true
					}
				}
			}
		}
		if !st.editable {
			st.text = strings.Join(strings.Fields(joined), " ")
			st.decision = Ask
			if m := regexp.MustCompile(`decision\s*=\s*"(\w+)"`).FindStringSubmatch(joined); m != nil {
				if d, ok := fromCodex(m[1]); ok {
					st.decision = d
				}
			}
		}
		out = append(out, st)
	}
	return out
}

// codexDepth is how many brackets a line opens and leaves open.
func codexDepth(line string) int {
	depth := 0
	var quote byte
	for i := 0; i < len(line); i++ {
		ch := line[i]
		if quote != 0 {
			if ch == '\\' {
				i++
			} else if ch == quote {
				quote = 0
			}
			continue
		}
		switch ch {
		case '"', '\'':
			quote = ch
		case '#':
			return depth
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			depth--
		}
	}
	return depth
}

// codexPatternText is a pattern as the panel writes it: the words with
// spaces between, a word with a space in it quoted, alternatives as
// {a|b}. ok is false for a pattern it cannot read as JSON.
func codexPatternText(pattern string) (string, bool) {
	var parts []any
	if json.Unmarshal([]byte(pattern), &parts) != nil || len(parts) == 0 {
		return "", false
	}
	var words []string
	for _, p := range parts {
		switch t := p.(type) {
		case string:
			words = append(words, codexWord(t))
		case []any:
			var alts []string
			for _, a := range t {
				s, ok := a.(string)
				if !ok {
					return "", false
				}
				alts = append(alts, codexWord(s))
			}
			words = append(words, "{"+strings.Join(alts, "|")+"}")
		default:
			return "", false
		}
	}
	return strings.Join(words, " "), true
}

func codexWord(s string) string {
	if s == "" || strings.ContainsAny(s, " \t\"'{}|") {
		return strconv.Quote(s)
	}
	return s
}

// codexPattern is the pattern a panel text stands for, written as Codex
// writes one: ["git", "push"].
func codexPattern(text string) (string, error) {
	words, err := splitWords(text)
	if err != nil {
		return "", err
	}
	if len(words) == 0 {
		return "", errors.New("the rule is empty: write the command it starts with, as git push")
	}
	var parts []string
	for _, w := range words {
		if strings.HasPrefix(w.text, "{") && strings.HasSuffix(w.text, "}") && !w.quoted {
			var alts []string
			for _, a := range strings.Split(w.text[1:len(w.text)-1], "|") {
				a = strings.TrimSpace(a)
				if uq, err := strconv.Unquote(a); err == nil {
					a = uq
				}
				raw, _ := agentfile.Marshal(a)
				alts = append(alts, string(raw))
			}
			parts = append(parts, "["+strings.Join(alts, ", ")+"]")
			continue
		}
		raw, _ := agentfile.Marshal(w.text)
		parts = append(parts, string(raw))
	}
	return "[" + strings.Join(parts, ", ") + "]", nil
}

type word struct {
	text   string
	quoted bool
}

// splitWords splits at spaces, keeping a "quoted string" and a {a|b} group
// whole.
func splitWords(text string) ([]word, error) {
	var out []word
	for i := 0; i < len(text); {
		switch {
		case text[i] == ' ' || text[i] == '\t':
			i++
		case text[i] == '"':
			j := i + 1
			for j < len(text) && text[j] != '"' {
				if text[j] == '\\' {
					j++
				}
				j++
			}
			if j >= len(text) {
				return nil, errors.New("a quote is not closed")
			}
			s, err := strconv.Unquote(text[i : j+1])
			if err != nil {
				return nil, err
			}
			out = append(out, word{text: s, quoted: true})
			i = j + 1
		case text[i] == '{':
			j := strings.IndexByte(text[i:], '}')
			if j < 0 {
				return nil, errors.New("a { is not closed")
			}
			out = append(out, word{text: text[i : i+j+1]})
			i += j + 1
		default:
			j := i
			for j < len(text) && text[j] != ' ' && text[j] != '\t' {
				j++
			}
			out = append(out, word{text: text[i:j]})
			i = j
		}
	}
	return out, nil
}

func codexLine(text string, d Decision) (string, error) {
	pattern, err := codexPattern(text)
	if err != nil {
		return "", err
	}
	w, ok := codexDecision[d]
	if !ok {
		return "", fmt.Errorf("Codex: %q: %w", d, ErrDecision)
	}
	return fmt.Sprintf("prefix_rule(pattern=%s, decision=%q)\n", pattern, w), nil
}

func (c *codexAgent) Read(p Place) Report {
	r := Report{
		Files:     c.files(p),
		Decisions: []Decision{Allow, Ask, Deny},
		Effect:    "Codex reads it in its next session",
	}
	var errs []string
	for _, f := range r.Files {
		if !f.Exists || !strings.HasSuffix(f.Path, ".rules") {
			continue
		}
		data, err := os.ReadFile(f.Path)
		if err != nil {
			errs = append(errs, err.Error())
			continue
		}
		for _, st := range codexStatements(strings.SplitAfter(string(data), "\n")) {
			rule := Rule{Agent: "codex", File: f.Path, Scope: f.Scope, Decision: st.decision, Text: st.text}
			if !st.editable {
				rule.ReadOnly = true
				rule.Why = "written in a form tend does not edit: open it in an editor"
			}
			r.Rules = append(r.Rules, rule)
		}
	}
	config := filepath.Join(codexHome(), "config.toml")
	values := map[string]any{}
	if data, err := os.ReadFile(config); err == nil {
		if _, err := toml.Decode(string(data), &values); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", config, err))
		}
	}
	text := func(v any) string { s, _ := v.(string); return s }
	r.Modes = append(r.Modes,
		Mode{Agent: "codex", File: config, Scope: Global, Key: "approval_policy", Value: text(values["approval_policy"]),
			Choices: []string{"untrusted", "on-failure", "on-request", "never"}, Help: "when Codex asks before running a command"},
		Mode{Agent: "codex", File: config, Scope: Global, Key: "sandbox_mode", Value: text(values["sandbox_mode"]),
			Choices: []string{"read-only", "workspace-write", "danger-full-access"}, Help: "what a command may touch"},
	)
	if p.Root != "" {
		projects, _ := values["projects"].(map[string]any)
		project, _ := projects[p.Root].(map[string]any)
		r.Modes = append(r.Modes, Mode{Agent: "codex", File: config, Scope: Project, Key: codexTrustKey(p.Root), Value: text(project["trust_level"]),
			Choices: []string{"trusted", "untrusted"}, Help: "whether Codex trusts this project (and reads its .codex)"})
	}
	if len(errs) > 0 {
		r.Error = strings.Join(errs, "; ")
	}
	return r
}

func codexTrustKey(root string) string { return "projects." + strconv.Quote(root) + ".trust_level" }

func (c *codexAgent) Check(text string) error {
	if strings.ContainsAny(text, "\n\r") {
		return errors.New("a rule is one line")
	}
	_, err := codexPattern(text)
	return err
}

// editRules edits a rules file by lines, and has Codex read the result.
func (c *codexAgent) editRules(file string, change func(lines []string) ([]string, error)) error {
	if !strings.HasSuffix(file, ".rules") {
		return ErrReadOnly
	}
	var before, after []byte
	err := agentfile.Edit(file, options(c.backups, "codex", file, 0o644), func(data []byte) ([]byte, error) {
		lines, err := change(strings.SplitAfter(string(data), "\n"))
		if err != nil {
			return nil, err
		}
		before, after = data, []byte(strings.Join(lines, ""))
		return after, nil
	})
	if err != nil || bytes.Equal(before, after) {
		return err
	}
	if problem := codexRefuses(file); problem != "" {
		if codexRefusesBytes(before) != "" {
			// It could not read the file before either: the change did not
			// break it, and putting it back would mend nothing.
			return nil
		}
		// Put it back, unless something else wrote it since.
		_ = agentfile.Edit(file, options(c.backups, "codex", file, 0o644), func(now []byte) ([]byte, error) {
			if !bytes.Equal(now, after) {
				return now, nil
			}
			return before, nil
		})
		return fmt.Errorf("Codex could not read the file after the change, so it was put back: %s", problem)
	}
	return nil
}

// codexRefuses is why Codex cannot read a rules file, "" when it can or
// when there is no codex to ask.
func codexRefuses(file string) string {
	bin, err := exec.LookPath("codex")
	if err != nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "execpolicy", "check", "--rules", file, "true").CombinedOutput()
	if err == nil {
		return ""
	}
	msg := strings.TrimSpace(string(out))
	// Only Codex reading the file and refusing it is a verdict: one that
	// does not answer, or does not start (an asdf shim with no version
	// set), says nothing about the file.
	if ctx.Err() != nil || !strings.Contains(msg, "failed to parse policy") {
		return ""
	}
	if i := strings.Index(msg, "\n\n"); i > 0 {
		msg = strings.TrimSpace(msg[i:])
	}
	return strings.Join(strings.Fields(msg), " ")
}

// codexRefusesBytes is codexRefuses for a file's former contents.
func codexRefusesBytes(data []byte) string {
	f, err := os.CreateTemp("", "tend-rules-*.rules")
	if err != nil {
		return ""
	}
	defer os.Remove(f.Name())
	_, err = f.Write(data)
	f.Close()
	if err != nil {
		return ""
	}
	return codexRefuses(f.Name())
}

// findStatement is the statement of lines that is the rule.
func findStatement(lines []string, text string, d Decision) (codexStatement, error) {
	for _, st := range codexStatements(lines) {
		if st.text == text && st.decision == d {
			if !st.editable {
				return st, ErrReadOnly
			}
			return st, nil
		}
	}
	return codexStatement{}, ErrNoRule
}

func (c *codexAgent) Add(file string, d Decision, text string) error {
	line, err := codexLine(text, d)
	if err != nil {
		return err
	}
	return c.editRules(file, func(lines []string) ([]string, error) {
		canonical := codexStatements([]string{line})[0]
		for _, st := range codexStatements(lines) {
			if st.editable && st.text == canonical.text {
				return nil, ErrExists
			}
		}
		if n := len(lines); n > 0 && lines[n-1] != "" && !strings.HasSuffix(lines[n-1], "\n") {
			lines[n-1] += "\n"
		}
		if n := len(lines); n > 0 && lines[n-1] == "" {
			lines = lines[:n-1]
		}
		return append(lines, line), nil
	})
}

func (c *codexAgent) Remove(file string, d Decision, text string) error {
	return c.editRules(file, func(lines []string) ([]string, error) {
		st, err := findStatement(lines, text, d)
		if err != nil {
			return nil, err
		}
		return append(lines[:st.start:st.start], lines[st.end:]...), nil
	})
}

func (c *codexAgent) SetDecision(file, text string, from, to Decision) error {
	line, err := codexLine(text, to)
	if err != nil {
		return err
	}
	return c.editRules(file, func(lines []string) ([]string, error) {
		st, err := findStatement(lines, text, from)
		if err != nil {
			return nil, err
		}
		out := append([]string(nil), lines[:st.start]...)
		out = append(out, line)
		return append(out, lines[st.end:]...), nil
	})
}

// SetMode edits a key of config.toml by lines — a top-level key, or a
// project's trust_level — and checks that nothing else in the file changed.
func (c *codexAgent) SetMode(file, key, value string) error {
	if !strings.HasSuffix(file, "config.toml") {
		return fmt.Errorf("Codex: %q: %w", key, ErrNoMode)
	}
	var table, name string
	var path []string
	switch {
	case key == "approval_policy" || key == "sandbox_mode":
		name, path = key, []string{key}
	case strings.HasPrefix(key, "projects.") && strings.HasSuffix(key, ".trust_level"):
		root, err := strconv.Unquote(strings.TrimSuffix(strings.TrimPrefix(key, "projects."), ".trust_level"))
		if err != nil {
			return fmt.Errorf("Codex: %q: %w", key, ErrNoMode)
		}
		table, name, path = "projects."+strconv.Quote(root), "trust_level", []string{"projects", root, "trust_level"}
	default:
		return fmt.Errorf("Codex: %q: %w", key, ErrNoMode)
	}
	return agentfile.Edit(file, options(c.backups, "codex", file, 0o600), func(data []byte) ([]byte, error) {
		lines := strings.SplitAfter(string(data), "\n")
		if n := len(lines); n > 0 && lines[n-1] == "" {
			lines = lines[:n-1]
		}
		if n := len(lines); n > 0 && !strings.HasSuffix(lines[n-1], "\n") {
			lines[n-1] += "\n"
		}
		out := setTOMLKey(lines, table, name, value)
		after := []byte(strings.Join(out, ""))
		return checkTOML(data, after, path, value)
	})
}

// setTOMLKey sets or removes `name = "value"` in a table ("" is the top
// level, before the first table), making the table at the end when it is
// not there.
func setTOMLKey(lines []string, table, name, value string) []string {
	start, end := -1, len(lines)
	if table == "" {
		start = 0
		for i, l := range lines {
			if _, ok := tomlHeader(l); ok {
				end = i
				break
			}
		}
	} else {
		for i, l := range lines {
			h, ok := tomlHeader(l)
			if !ok {
				continue
			}
			if start >= 0 {
				end = i
				break
			}
			if sameTable(h, table) {
				start = i + 1
			}
		}
	}
	entry := name + " = " + strconv.Quote(value) + "\n"
	if start < 0 {
		if value == "" {
			return lines
		}
		if n := len(lines); n > 0 && strings.TrimSpace(lines[n-1]) != "" {
			lines = append(lines, "\n")
		}
		return append(lines, "["+table+"]\n", entry)
	}
	for i := start; i < end; i++ {
		if isKeyLine(lines[i], name) {
			if value == "" {
				return append(lines[:i:i], lines[i+1:]...)
			}
			out := append([]string(nil), lines...)
			out[i] = entry
			return out
		}
	}
	if value == "" {
		return lines
	}
	// After the table's last key: before its trailing blank lines.
	at := end
	for at > start && strings.TrimSpace(lines[at-1]) == "" {
		at--
	}
	out := append([]string(nil), lines[:at]...)
	out = append(out, entry)
	return append(out, lines[at:]...)
}

// tomlHeader is a [table] line's name.
func tomlHeader(line string) (string, bool) {
	t := strings.TrimSpace(line)
	if !strings.HasPrefix(t, "[") || strings.HasPrefix(t, "[[") {
		return "", false
	}
	end := strings.LastIndex(t, "]")
	if end < 0 {
		return "", false
	}
	return strings.TrimSpace(t[1:end]), true
}

// sameTable compares two table names as TOML reads them.
func sameTable(a, b string) bool {
	var x, y map[string]any
	if _, err := toml.Decode("["+a+"]\n", &x); err != nil {
		return false
	}
	if _, err := toml.Decode("["+b+"]\n", &y); err != nil {
		return false
	}
	return reflect.DeepEqual(x, y)
}

func isKeyLine(line, key string) bool {
	t := strings.TrimSpace(line)
	if !strings.HasPrefix(t, key) {
		return false
	}
	return strings.HasPrefix(strings.TrimLeft(t[len(key):], " \t"), "=")
}

// checkTOML parses an edit back and checks that the one key is as wanted
// and nothing else changed.
func checkTOML(before, after []byte, path []string, value string) ([]byte, error) {
	var b, a map[string]any
	if _, err := toml.Decode(string(before), &b); err != nil {
		return nil, err
	}
	if _, err := toml.Decode(string(after), &a); err != nil {
		return nil, fmt.Errorf("the edit would not read back: %w", err)
	}
	got, _ := dig(a, path).(string)
	if got != value {
		return nil, errors.New("the edit did not do what was meant; config.toml is left as it was")
	}
	prune(b, path)
	prune(a, path)
	if !reflect.DeepEqual(b, a) {
		return nil, errors.New("the edit would have changed more than the one setting; config.toml is left as it was")
	}
	return after, nil
}

func dig(m map[string]any, path []string) any {
	var v any = m
	for _, k := range path {
		mm, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		v = mm[k]
	}
	return v
}

// prune removes a key, and the tables it leaves empty.
func prune(m map[string]any, path []string) {
	if m == nil {
		return
	}
	if len(path) == 1 {
		delete(m, path[0])
		return
	}
	child, ok := m[path[0]].(map[string]any)
	if !ok {
		return
	}
	prune(child, path[1:])
	if len(child) == 0 {
		delete(m, path[0])
	}
}
