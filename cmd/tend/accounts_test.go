//go:build unix

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestAnAgentIsStartedOnAnAccountAddedFromTheMenu: "new agent..." under the
// spaces' new offers to add an account to an agent that can have one; the
// name typed is written to the settings, and the agent starts on it, in a
// directory of its own given through the agent's variable — where its login
// is kept — and the account is offered by name from then on. If it
// regresses, a second Claude login, or a company's, cannot be used from
// tend at all, which is what the owner ran into.
//
// The agent is a stand-in named claude that says which directory it was
// given: what is tested is tend starting it on the account, not Claude
// Code's login.
func TestAnAgentIsStartedOnAnAccountAddedFromTheMenu(t *testing.T) {
	home, bin := t.TempDir(), t.TempDir()
	// Go's own directories stay where they are: they default to under
	// HOME, and on GitHub's runner something the session started ran go
	// with this HOME, filling it with a module cache whose read-only files
	// the test's cleanup could not remove.
	for _, v := range []string{"GOPATH", "GOMODCACHE", "GOCACHE"} {
		if out, err := exec.Command("go", "env", v).Output(); err == nil {
			t.Setenv(v, strings.TrimSpace(string(out)))
		}
	}
	t.Setenv("HOME", home)
	fake := "#!/bin/sh\ncase \"$1\" in --version) echo 9.9.9; exit 0;; esac\necho \"FAKE AGENT HOME=$CLAUDE_CONFIG_DIR HOME-SET=${HOME:+yes}\"\n"
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	a := startSession(t, 180, 30)
	a.waitForScreen(t, "a pane", func(s string) bool { return strings.Contains(s, "┌") })

	openNewAgent := func() {
		t.Helper()
		a.clickAt(t, 2, a.lineContaining(t, "new"))
		a.waitForScreen(t, "the new menu", func(s string) bool { return strings.Contains(s, "new agent...") })
		a.clickText(t, "new agent...")
		a.waitForScreen(t, "the agents", func(s string) bool {
			return strings.Contains(s, "+ account for Claude Code...")
		})
	}
	openNewAgent()
	a.clickText(t, "+ account for Claude Code...")
	a.waitForScreen(t, "the name prompt", func(s string) bool { return strings.Contains(s, "new claude account") })
	a.send(t, "Work X\r")

	want := filepath.Join(home, ".claude-work-x")
	a.waitForScreen(t, "the agent on the account", func(s string) bool {
		return strings.Contains(s, "FAKE AGENT HOME="+want+" HOME-SET=yes") && strings.Contains(a.sidebarText(), "Claude Code · Work X")
	})
	if info, err := os.Stat(want); err != nil || !info.IsDir() {
		t.Errorf("the account's directory was not made: %v", err)
	}
	settings, err := os.ReadFile(os.Getenv("TEND_CONFIG"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(settings), "[[accounts]]") || !strings.Contains(string(settings), `name = "Work X"`) ||
		!strings.Contains(string(settings), `home = "~/.claude-work-x"`) {
		t.Errorf("the account was not written to the settings:\n%s", settings)
	}

	// The shell left when the agent exits keeps the account.
	a.send(t, "echo LEFT=$CLAUDE_CONFIG_DIR\r")
	a.waitForScreen(t, "the shell on the account", func(s string) bool { return strings.Contains(s, "LEFT="+want) })

	openNewAgent()
	a.waitForScreen(t, "the account by name", func(s string) bool { return strings.Contains(s, "Claude Code · Work X") })
}
