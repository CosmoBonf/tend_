//go:build unix

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// permFixture is a home with Claude Code and Codex rules in it, and a
// project with Claude rules of its own; every agent's settings point into
// it. It returns the home and the project.
func permFixture(t *testing.T) (string, string) {
	t.Helper()
	home := t.TempDir()
	// The server runs in the test's process, and Claude's local file in the
	// home folder is found from HOME: the person's own must not be read.
	// A shell with an empty home: sh, which asks nothing on its first run.
	t.Setenv("HOME", home)
	t.Setenv("SHELL", "/bin/sh")
	for _, v := range []string{"CLAUDE_CONFIG_DIR", "CODEX_HOME", "CURSOR_CONFIG_DIR", "GEMINI_CLI_HOME", "XDG_CONFIG_HOME", "DSH_HOME"} {
		t.Setenv(v, "")
	}
	project := filepath.Join(home, "proj")
	files := map[string]string{
		filepath.Join(home, ".claude", "settings.json"):             `{"theme": "dark", "permissions": {"allow": ["Bash(ls)"]}}`,
		filepath.Join(project, ".claude", "settings.local.json"):    `{"permissions": {"allow": ["Bash(make test)"]}}`,
		filepath.Join(home, ".codex", "rules", "default.rules"):     "prefix_rule(pattern=[\"git\", \"status\"], decision=\"allow\")\n",
		filepath.Join(home, ".cursor", "cli-config.json"):           `{"approvalMode": "allowlist", "permissions": {"allow": ["Shell(ls)"], "deny": []}}`,
		filepath.Join(home, ".config", "opencode", "opencode.json"): `{"permission": {"bash": {"*": "ask"}}}`,
	}
	for path, data := range files {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return home, project
}

func fileHas(t *testing.T, path, want string) bool {
	t.Helper()
	data, err := os.ReadFile(path)
	return err == nil && strings.Contains(string(data), want)
}

// TestThePermissionsManagerChangesMovesAndAddsRules: prefix+P shows the
// project's Claude rules and the global ones; space makes a rule ask, m
// moves the project's rule to the global file, and a adds a deny rule —
// each one in the file, the rest of the file kept. If it regresses, the
// panel cannot be reached, or a change does not reach the agent's file,
// or reaches the wrong one.
func TestThePermissionsManagerChangesMovesAndAddsRules(t *testing.T) {
	home, project := permFixture(t)
	global := filepath.Join(home, ".claude", "settings.json")
	local := filepath.Join(project, ".claude", "settings.local.json")

	a := startSessionIn(t, 130, 36, project)
	a.waitForScreen(t, "a pane", func(s string) bool { return strings.Contains(s, "┌") })
	// The panel is for the project the pane's shell is in.
	a.send(t, "cd "+project+"\r")
	a.send(t, "\x02P")
	a.waitForScreen(t, "Claude's rules, global and the project's", func(s string) bool {
		return strings.Contains(s, "PERMISSIONS") && strings.Contains(s, "Claude Code 2") &&
			strings.Contains(s, "Bash(ls)") && strings.Contains(s, "Bash(make test)")
	})

	// The modes come first: the global defaultMode, and the local file's.
	a.send(t, "jj")
	a.send(t, " ")
	a.waitForScreen(t, "Bash(ls) asking", func(s string) bool { return strings.Contains(s, "Bash(ls) is ask now") })
	if !fileHas(t, global, `"ask": [`) || !fileHas(t, global, `"theme": "dark"`) {
		data, _ := os.ReadFile(global)
		t.Fatalf("the change is not in the file, or the rest was lost:\n%s", data)
	}

	a.send(t, "j")
	a.send(t, "m")
	a.waitForScreen(t, "the files to move it to", func(s string) bool { return strings.Contains(s, "move Bash(make test) to") })
	// No item starts marked (openMenu): down to the first, the global file.
	a.send(t, "\x1b[B\r")
	a.waitForScreen(t, "the rule moved", func(s string) bool { return strings.Contains(s, "moved Bash(make test)") })
	if !fileHas(t, global, "Bash(make test)") || fileHas(t, local, "Bash(make test)") {
		g, _ := os.ReadFile(global)
		l, _ := os.ReadFile(local)
		t.Fatalf("not moved:\nglobal %s\nlocal %s", g, l)
	}

	a.send(t, "a")
	a.waitForScreen(t, "the line a rule is typed on", func(s string) bool { return strings.Contains(s, "new rule for Claude Code:") })
	a.send(t, "Bash(rm -rf *)\r")
	a.waitForScreen(t, "the decisions", func(s string) bool { return strings.Contains(s, "deny — never") })
	a.send(t, "\x1b[B\x1b[B\x1b[B\r") // allow, ask, deny
	a.waitForScreen(t, "the files", func(s string) bool { return strings.Contains(s, "into which file?") })
	a.send(t, "\x1b[B\r")
	a.waitForScreen(t, "the rule added", func(s string) bool { return strings.Contains(s, "added deny Bash(rm -rf *)") })
	if !fileHas(t, global, `"deny": [`) || !fileHas(t, global, "Bash(rm -rf *)") {
		data, _ := os.ReadFile(global)
		t.Fatalf("not added:\n%s", data)
	}

	a.send(t, "\x1b")
	a.waitForScreen(t, "the panel to close", func(s string) bool { return !strings.Contains(s, "PERMISSIONS") })
}

// TestThePermissionsManagerOpensAFileInTheEditorAndComesBack: e opens the
// rule's file in the editor beside the pane; when the editor is closed the
// panel is back, read again, on the same agent. If it regresses, editing a
// file by hand leaves the person outside the panel, or looking at what the
// file said before.
func TestThePermissionsManagerOpensAFileInTheEditorAndComesBack(t *testing.T) {
	home, project := permFixture(t)
	rules := filepath.Join(home, ".codex", "rules", "default.rules")
	// The editor appends a rule and quits, as someone would in nano.
	editor := filepath.Join(home, "editor.sh")
	script := "#!/bin/sh\nprintf 'prefix_rule(pattern=[\"npm\", \"test\"], decision=\"allow\")\\n' >> \"$1\"\n"
	if err := os.WriteFile(editor, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VISUAL", editor)

	a := startSessionIn(t, 130, 36, project)
	a.waitForScreen(t, "a pane", func(s string) bool { return strings.Contains(s, "┌") })
	// The panel is for the project the pane's shell is in.
	a.send(t, "cd "+project+"\r")
	a.send(t, "\x02P")
	a.waitForScreen(t, "the panel", func(s string) bool { return strings.Contains(s, "Codex 1") })
	a.send(t, "l")
	a.waitForScreen(t, "Codex's rule", func(s string) bool { return strings.Contains(s, "git status") })
	a.send(t, "jjj") // past approval_policy, sandbox_mode and the project's trust
	a.send(t, "e")
	a.waitForScreen(t, "the panel back over the edited file", func(s string) bool {
		return strings.Contains(s, "PERMISSIONS") && strings.Contains(s, "Codex 2") && strings.Contains(s, "npm test")
	})
	if !fileHas(t, rules, "npm") {
		t.Fatal("the editor did not get the file")
	}
}
