// Package perm reads and edits what each coding agent may do without
// asking: Claude Code's allow/ask/deny lists, Codex's prefix rules,
// Cursor's allowlist, Gemini's tool lists, OpenCode's permission map, and
// the one setting the DeepSeek Harness has. The agents write these files
// themselves — Claude Code and Codex add a rule whenever "always allow" is
// pressed — so every write goes through internal/agentfile, which keeps
// what the agent wrote in the meantime.
//
// herdr has nothing like it; the owner asked for it (docs/PORTING.md).
package perm

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/auth-com-br/tend/internal/agentfile"
)

// Decision is what a rule says. Each agent names them its own way (Codex's
// "prompt" and "forbidden", Gemini's "ask_user"); these are tend's names.
type Decision string

const (
	Allow Decision = "allow"
	Ask   Decision = "ask"
	Deny  Decision = "deny"
)

// Scope is whose a file is: the person's, everywhere (global); the
// project's, kept in the repository (project); or the person's, for this
// project only and not committed (local).
type Scope string

const (
	Global  Scope = "global"
	Project Scope = "project"
	Local   Scope = "local"
)

// File is one file an agent reads its permissions from.
type File struct {
	Path  string
	Scope Scope
	// Exists is whether the file is there; one that is not is where a
	// rule of its scope would be written.
	Exists bool
	// ReadOnly is a file tend shows and opens in an editor but does not
	// write: a JSONC file, a Gemini policy, a file of an agent tend has no
	// adapter for.
	ReadOnly bool
	// Note says what is particular about the file: when it applies, why it
	// is read-only.
	Note string
	// ModesOnly is a file that holds modes and no rules: Codex's
	// config.toml, the harness's settings.yaml.
	ModesOnly bool
}

// Rule is one rule as the panel shows it. A write names a rule by its file,
// decision and text — never by a position, which the agent may have moved
// since the panel read it.
type Rule struct {
	Agent    string
	File     string
	Scope    Scope
	Decision Decision
	Text     string
	// ReadOnly is a rule tend shows but does not edit (a multi-line Codex
	// rule, a Gemini policy rule): the editor is the way to change it.
	ReadOnly bool
	// Why says why the rule does not do what it seems to, when it does
	// not; empty when it does.
	Why string
	// Elsewhere is set when the same rule is in another of the agent's
	// files too.
	Elsewhere bool
}

// Mode is a setting that decides how an agent asks at all: Claude Code's
// defaultMode, Codex's approval_policy, Cursor's approvalMode.
type Mode struct {
	Agent, File string
	Scope       Scope
	Key, Value  string
	// Choices are the values the agent takes; "" in Value is unset, the
	// agent's default.
	Choices []string
	Help    string
}

// Report is one agent's permissions in one project.
type Report struct {
	ID, Name string
	Files    []File
	Rules    []Rule
	Modes    []Mode
	// Decisions are those the agent's rules can have, in the order the
	// panel cycles through them.
	Decisions []Decision
	// Warnings are about the agent as a whole: an allowlist the agent does
	// not consult, an agent with no rules at all.
	Warnings []string
	// Effect is when a change is seen by the agent.
	Effect string
	Error  string
}

// Adapter is one agent. Writes name the file; the Manager checks it is one
// of the agent's before the adapter is asked.
type Adapter interface {
	ID() string
	Name() string
	// Read is the agent's files, rules and modes in a place.
	Read(p Place) Report
	Add(file string, d Decision, text string) error
	Remove(file string, d Decision, text string) error
	SetDecision(file, text string, from, to Decision) error
	SetMode(file, key, value string) error
	// Check says whether text is a rule the agent can read, before it is
	// written.
	Check(text string) error
}

var (
	// ErrNoRule is a rule that is not in its file any more: the agent, or
	// the person in an editor, changed the file since the panel read it.
	ErrNoRule = errors.New("the rule is no longer in the file; reload")
	// ErrExists is a rule the file already has.
	ErrExists = errors.New("the file already has that rule")
	// ErrReadOnly is a write to a file or rule tend does not edit.
	ErrReadOnly = errors.New("tend does not edit this one; open it in an editor")
	// ErrNotTheirs is a write to a file that is not one of the agent's.
	ErrNotTheirs = errors.New("that file is not one of the agent's")
	// ErrNoAgent is an agent tend does not know.
	ErrNoAgent = errors.New("no such agent")
	// ErrDecision is a decision the agent has no word for.
	ErrDecision = errors.New("the agent has no such decision")
	// ErrNoMode is a setting the agent does not have.
	ErrNoMode = errors.New("the agent has no such setting")
)

// Manager is every agent's permissions together. It serialises its own
// writes and holds no lock of the tend server's: everything here is I/O.
type Manager struct {
	mu       sync.Mutex
	adapters []Adapter
}

// Extra is a file of an agent tend has no adapter for, from the config's
// [[permissions.files]]: listed, and opened in an editor, never written.
type Extra struct {
	Agent, Path string
	Scope       Scope
}

// NewManager is the agents tend knows, with their backups under
// stateDir/permissions, and the extra files the config names.
func NewManager(stateDir string, extras []Extra) *Manager {
	backups := filepath.Join(stateDir, "permissions", "backups")
	adapters := []Adapter{
		newClaude(backups), newCodex(backups), newCursor(backups),
		newGemini(backups), newOpenCode(backups), newDsh(backups),
	}
	return NewManagerWith(append(adapters, extraAdapters(extras)...)...)
}

// SetExtras replaces the agents named in the config, which can change
// while the server runs.
func (m *Manager) SetExtras(extras []Extra) {
	m.mu.Lock()
	defer m.mu.Unlock()
	known := m.adapters[:0:0]
	for _, a := range m.adapters {
		if _, extra := a.(*extraAgent); !extra {
			known = append(known, a)
		}
	}
	m.adapters = append(known, extraAdapters(extras)...)
}

// NewManagerWith is a manager over given adapters, for tests.
func NewManagerWith(adapters ...Adapter) *Manager {
	return &Manager{adapters: adapters}
}

// Snapshot is everything the panel shows for one project.
type Snapshot struct {
	Project string
	Agents  []Report
}

// Place is where the panel was opened: the pane's directory, and the
// project it is in. Both are "" when there is no pane to go by.
type Place struct{ Root, Dir string }

// PlaceOf is the place of a pane's directory.
func PlaceOf(dir string) Place {
	if dir == "" {
		return Place{}
	}
	return Place{Root: ProjectRoot(dir), Dir: filepath.Clean(dir)}
}

// ProjectRoot is the directory a pane's working directory stands for: the
// top of its git repository, or the directory itself outside one.
func ProjectRoot(dir string) string {
	if dir == "" {
		return ""
	}
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return filepath.Clean(dir)
	}
	return strings.TrimSpace(string(out))
}

// List reads every agent's permissions for a project directory. An agent
// whose files cannot be read is listed with the reason rather than taking
// the others down with it.
func (m *Manager) List(p Place) Snapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	snap := Snapshot{Project: p.Root}
	for _, a := range m.adapters {
		r := a.Read(p)
		r.ID, r.Name = a.ID(), a.Name()
		markElsewhere(r.Rules)
		snap.Agents = append(snap.Agents, r)
	}
	return snap
}

// markElsewhere marks the rules that are in more than one of the agent's
// files: a rule "always allowed" in one project and then made global is
// left behind in the project, and is noise.
func markElsewhere(rules []Rule) {
	files := map[string]map[string]bool{}
	for _, r := range rules {
		k := string(r.Decision) + "\x00" + r.Text
		if files[k] == nil {
			files[k] = map[string]bool{}
		}
		files[k][r.File] = true
	}
	for i, r := range rules {
		rules[i].Elsewhere = len(files[string(r.Decision)+"\x00"+r.Text]) > 1
	}
}

// writable is the adapter for agent and the file, when the file is one of
// the agent's in this project and one tend writes.
func (m *Manager) writable(p Place, agent, file string) (Adapter, error) {
	for _, a := range m.adapters {
		if a.ID() != agent {
			continue
		}
		for _, f := range a.Read(p).Files {
			if f.Path == file {
				if f.ReadOnly {
					return nil, ErrReadOnly
				}
				return a, nil
			}
		}
		return nil, fmt.Errorf("%s: %w", file, ErrNotTheirs)
	}
	return nil, fmt.Errorf("%s: %w", agent, ErrNoAgent)
}

// Add writes a rule into one of the agent's files.
func (m *Manager) Add(p Place, agent, file string, d Decision, text string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, err := m.writable(p, agent, file)
	if err != nil {
		return err
	}
	text = strings.TrimSpace(text)
	if err := a.Check(text); err != nil {
		return err
	}
	return a.Add(file, d, text)
}

// Remove takes a rule out of its file.
func (m *Manager) Remove(p Place, agent, file string, d Decision, text string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, err := m.writable(p, agent, file)
	if err != nil {
		return err
	}
	return a.Remove(file, d, text)
}

// SetDecision changes what a rule says, in its place.
func (m *Manager) SetDecision(p Place, agent, file, text string, from, to Decision) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, err := m.writable(p, agent, file)
	if err != nil {
		return err
	}
	return a.SetDecision(file, text, from, to)
}

// Move takes a rule from one of the agent's files to another: written to
// the new file first, then taken out of the old, so a failure in between
// leaves it in both rather than in neither.
func (m *Manager) Move(p Place, agent, from, to string, d Decision, text string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	src, err := m.writable(p, agent, from)
	if err != nil {
		return err
	}
	dst, err := m.writable(p, agent, to)
	if err != nil {
		return err
	}
	if err := dst.Add(to, d, text); err != nil && !errors.Is(err, ErrExists) {
		return err
	}
	return src.Remove(from, d, text)
}

// SetMode sets one of the agent's modes; "" unsets it.
func (m *Manager) SetMode(p Place, agent, file, key, value string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, err := m.writable(p, agent, file)
	if err != nil {
		return err
	}
	return a.SetMode(file, key, value)
}

// --- helpers the adapters share -----------------------------------------

// options are the write options for one of an agent's files: its backups
// named after the agent, the file and its directory, so two projects'
// settings.local.json are two backups.
func options(backups, agent, path string, mode os.FileMode) agentfile.Options {
	sum := sha256.Sum256([]byte(path))
	name := agent + "-" + filepath.Base(path) + "-" + hex.EncodeToString(sum[:4])
	return agentfile.Options{Backups: backups, BackupName: name, Mode: mode}
}

// exists is whether a path is a file.
func exists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// home is the person's home directory, "" when it cannot be told.
func home() string {
	h, _ := os.UserHomeDir()
	return h
}

// projectDirs are the directories between a project's root and the pane's
// directory, root first — where Cursor and OpenCode look for a project's
// files. A directory outside the root is the root alone.
func projectDirs(p Place) []string {
	root, dir := p.Root, p.Dir
	if root == "" {
		return nil
	}
	dirs := []string{root}
	if dir == "" || dir == root {
		return dirs
	}
	rel, err := filepath.Rel(root, dir)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return dirs
	}
	at := root
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		at = filepath.Join(at, part)
		dirs = append(dirs, at)
	}
	return dirs
}

// dedupe drops files listed twice — the project in the home directory is
// the global one — keeping the first.
func dedupe(files []File) []File {
	seen := map[string]bool{}
	out := files[:0]
	for _, f := range files {
		p := filepath.Clean(f.Path)
		if seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, f)
	}
	return out
}

// removeString takes the first s out of list, and says whether it was
// there.
func removeString(list []string, s string) ([]string, bool) {
	for i, v := range list {
		if v == s {
			return append(list[:i:i], list[i+1:]...), true
		}
	}
	return list, false
}

// sortedKeys is a map's keys in order.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
