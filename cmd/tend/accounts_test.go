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
		// The footer itself, not only a pane: under the race detector the
		// frame came before the sidebar's foot was drawn.
		a.waitForScreen(t, "the spaces' new", func(s string) bool { return strings.Contains(s, "⇄ ssh menu") })
		a.clickAt(t, 2, a.lineContaining(t, "⇄ ssh menu"))
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

// TestTheStatusBarSaysTheAccountsAndSwitchesThem: the bar's right says the
// active GitHub login and the account Claude starts on; a click there opens
// the accounts menu, where an account becomes Claude's default — written to
// the settings, which the server gives every new pane — and another GitHub
// login is made the active one through gh. If it regresses, the owner
// cannot see which account is in use, nor move to another when one runs
// out.
func TestTheStatusBarSaysTheAccountsAndSwitchesThem(t *testing.T) {
	bin := t.TempDir()
	calls := filepath.Join(t.TempDir(), "gh-calls")
	gh := "#!/bin/sh\necho \"$*\" >> " + calls + "\n" +
		"case \"$1 $2\" in \"auth status\") echo '{\"hosts\":{\"github.com\":[{\"login\":\"me\",\"active\":true},{\"login\":\"work-me\",\"active\":false}]}}' ;; esac\n"
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(gh), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv(skipGitHubAccountsEnv, "")
	cfg := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(cfg, []byte(quietSettings+"\n[[accounts]]\nname = \"job\"\nagent = \"claude\"\nhome = \"~/.claude-job\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	withConfig(t, cfg)

	a := startSession(t, 160, 30)
	a.waitForScreen(t, "the accounts on the bar", func(string) bool {
		return strings.Contains(a.lines()[len(a.lines())-1], "git me · claude own login")
	})
	bar := len(a.lines())
	a.clickAt(t, columnOfString(a.lines()[bar-1], "claude own login")+2, bar)
	a.waitForScreen(t, "the accounts menu", func(s string) bool {
		return strings.Contains(s, "● own login") && strings.Contains(s, "  job") && strings.Contains(s, "● me")
	})
	a.clickText(t, "  job")
	a.waitForScreen(t, "claude on job", func(string) bool {
		return strings.Contains(a.lines()[len(a.lines())-1], "claude job")
	})
	if data, _ := os.ReadFile(cfg); !strings.Contains(string(data), `default_accounts = { claude = "job" }`) {
		t.Errorf("the default was not written:\n%s", data)
	}

	a.clickAt(t, columnOfString(a.lines()[bar-1], "git me")+2, bar)
	a.waitForScreen(t, "the menu again", func(s string) bool { return strings.Contains(s, "  work-me") })
	a.clickText(t, "  work-me")
	a.waitForScreen(t, "the switch said", func(string) bool { return strings.Contains(a.text(), "GitHub: now work-me") })
	if data, _ := os.ReadFile(calls); !strings.Contains(string(data), "auth switch --hostname github.com --user work-me") {
		t.Errorf("gh was asked %q", data)
	}
}

// TestAnAccountShowsItsOrganizationAndIsRenamedAndRemoved: the accounts
// menu says which organization each Claude login is in, read from where
// Claude keeps it — the owner has one e-mail in a Pro and a company Team —
// and an account is renamed, then removed with yes typed, its folder
// deleted. If it regresses, the owner cannot tell which login is which, nor
// tidy away one made by mistake.
func TestAnAccountShowsItsOrganizationAndIsRenamedAndRemoved(t *testing.T) {
	home := t.TempDir()
	for _, v := range []string{"GOPATH", "GOMODCACHE", "GOCACHE"} {
		if out, err := exec.Command("go", "env", v).Output(); err == nil {
			t.Setenv(v, strings.TrimSpace(string(out)))
		}
	}
	t.Setenv("HOME", home)
	org := func(path, name string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(`{"oauthAccount":{"emailAddress":"me@x","organizationName":"`+name+`"}}`), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	org(filepath.Join(home, ".claude.json"), "me@x's Organization")
	org(filepath.Join(home, ".claude-acme", ".claude.json"), "Acme")
	cfg := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(cfg, []byte(quietSettings+"\n[[accounts]]\nname = \"acme\"\nagent = \"claude\"\nhome = \"~/.claude-acme\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	withConfig(t, cfg)

	a := startSession(t, 160, 34)
	openMenu := func() {
		t.Helper()
		a.waitForScreen(t, "the accounts on the bar", func(string) bool {
			return strings.Contains(a.lines()[len(a.lines())-1], "claude ")
		})
		bar := len(a.lines())
		a.clickAt(t, columnOfString(a.lines()[bar-1], "claude ")+2, bar)
		a.waitForScreen(t, "the accounts menu", func(s string) bool { return strings.Contains(s, "own login") })
	}
	openMenu()
	a.waitForScreen(t, "each login's organization", func(s string) bool {
		return strings.Contains(s, "own login · personal") && strings.Contains(s, "acme · Acme")
	})

	a.clickText(t, "rename acme...")
	// The prompt's own frame, not the menu's item of the same words.
	a.waitForScreen(t, "the rename prompt", func(s string) bool { return strings.Contains(s, "┌─ rename acme") })
	a.send(t, "sapcon\r") // over the old name, which starts selected
	a.waitForScreen(t, "renamed", func(s string) bool { return strings.Contains(s, "acme is now sapcon") })

	openMenu()
	a.waitForScreen(t, "the new name, the same login", func(s string) bool { return strings.Contains(s, "sapcon · Acme") })
	a.clickText(t, "remove sapcon...")
	a.waitForScreen(t, "the question", func(s string) bool {
		return strings.Contains(s, "type yes") && !strings.Contains(s, "remove sapcon...")
	})
	a.send(t, "yes\r")
	a.waitForScreen(t, "removed", func(s string) bool { return strings.Contains(s, "sapcon removed") })
	if _, err := os.Stat(filepath.Join(home, ".claude-acme")); !os.IsNotExist(err) {
		t.Errorf("the account's folder is still there: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude.json")); err != nil {
		t.Errorf("the own login was touched: %v", err)
	}
	if data, _ := os.ReadFile(cfg); strings.Contains(string(data), "[[accounts]]") {
		t.Errorf("the account is still in the settings:\n%s", data)
	}
}
