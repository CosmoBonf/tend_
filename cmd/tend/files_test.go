//go:build unix

package main

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/auth-com-br/tend/internal/pty"
	"github.com/auth-com-br/tend/internal/sshconn"
	"github.com/auth-com-br/tend/internal/vt"
)

// gitProject is a repository with a committed file, a changed one and a new
// one, which is enough for every part of the panel to have something to say.
func gitProject(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("needs git")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	write := func(name, text string) {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("kept.txt", "one\n")
	write("src/changed.go", "package src\n")
	run("add", "-A")
	run("commit", "-q", "-m", "first")
	write("src/changed.go", "package src\n\nvar x = 1\n")
	write("brandnew.md", "# new\n")
	return dir
}

// TestTheFilesPanelDocksOnTheLeftAndShowsTheProject: prefix+f opens the
// explorer along the left of the tab, in the directory the pane is in, with
// git's news beside each file; pressed in the panel it closes it again. If
// it regresses, the key that shows the project shows nothing, or a panel
// that cannot be put away.
func TestTheFilesPanelDocksOnTheRightAndShowsTheProject(t *testing.T) {
	project := gitProject(t)
	// The test's server runs in this process and names no binary for its
	// panes (TEND_BIN_PATH), so the panel finds tend on the PATH: this build.
	t.Setenv("PATH", filepath.Dir(buildBinary(t))+string(os.PathListSeparator)+os.Getenv("PATH"))
	a := startSession(t, 120, 30)
	a.waitForScreen(t, "a pane", func(s string) bool { return strings.Contains(s, "┌") })
	a.sendUntil(t, "cd "+project+" && echo in-project\n", "the shell in the project",
		func(s string) bool {
			return strings.Contains(s, "\nin-project") || strings.Contains(s, "│in-project")
		})

	a.send(t, "\x02f")
	a.waitForScreen(t, "the panel", func(s string) bool {
		return strings.Contains(s, "files │ search │ changes") && strings.Contains(s, "brandnew.md") && strings.Contains(s, "kept.txt")
	})
	// Docked on the right unless the settings say left: the panel's frame
	// comes after the shell's.
	if top := a.lines()[1]; strings.Index(top, "files") < strings.Index(top, "sh ") {
		t.Errorf("the panel should be on the right of the tab:\n%s", a.text())
	}
	// The branch, and the letters git gives the new file and the changed
	// directory.
	if !strings.Contains(a.text(), "⎇ main") {
		t.Errorf("the branch should be shown:\n%s", a.text())
	}
	if !strings.Contains(a.lines()[a.lineContaining(t, "brandnew.md")-1], "U") {
		t.Errorf("a new file is marked U:\n%s", a.text())
	}

	// A click reaches the panel through the client, which forwards the
	// mouse to a pane that asked for it: one on a folder opens it.
	row := a.lineContaining(t, "▸ src")
	col := len([]rune(a.lines()[row-1][:strings.Index(a.lines()[row-1], "▸ src")]))
	a.clickAt(t, col+3, row)
	a.waitForScreen(t, "src opened by a click", func(s string) bool { return strings.Contains(s, "changed.go") })

	// The changes view lists what changed.
	a.send(t, "3")
	a.waitForScreen(t, "the changes", func(s string) bool {
		return strings.Contains(s, "CHANGES 2") && strings.Contains(s, "changed.go")
	})

	// prefix+f in the panel puts it away.
	a.send(t, "\x02f")
	a.waitForScreen(t, "the panel to close", func(s string) bool { return !strings.Contains(s, "files │ search │ changes") })
}

// TestAFileChosenInThePanelOpensInTheEditorInATab is the panel in a real
// session — the server a client starts, its automation socket, the tend it
// names to its panes: a file found with / and enter opens in $EDITOR in a
// tab of its own. If it regresses, the panel shows files that cannot be
// opened from it.
func TestAFileChosenInThePanelOpensInTheEditorInATab(t *testing.T) {
	project := gitProject(t)
	if err := os.WriteFile(filepath.Join(project, "note.txt"), []byte("hello-from-note\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runtimeDir, err := os.MkdirTemp("", "tf")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(runtimeDir) })
	t.Setenv("TEND_RUNTIME_DIR", runtimeDir)
	env := append(os.Environ(),
		"TEND_RUNTIME_DIR="+runtimeDir, "SHELL=/bin/sh",
		"TEND_CONFIG="+quietConfig(t),
		// An editor that shows the file and stays, so the tab is there to
		// be seen.
		`EDITOR=sh -c 'cat "$0"; sleep 60'`, "VISUAL=",
	)

	bin := buildBinary(t)
	p, err := pty.Start(bin, []string{"attach", "-s", "panel"}, pty.Options{
		Size: pty.Size{Cols: 120, Rows: 30},
		Env:  env,
	})
	if err != nil {
		t.Fatal(err)
	}
	a := &attached{pty: p, screen: vt.NewScreen(120, 30, 100)}
	go func() { _, _ = io.Copy(a, p) }()
	t.Cleanup(func() {
		_ = p.Close()
		stopSession(t, "panel")
	})
	a.waitForScreen(t, "a pane", func(s string) bool { return strings.Contains(s, "┌") })
	a.sendUntil(t, "cd "+project+" && echo in-project\n", "the shell in the project",
		func(s string) bool {
			return strings.Contains(s, "\nin-project") || strings.Contains(s, "│in-project")
		})

	a.send(t, "\x02f")
	a.waitForScreen(t, "the panel", func(s string) bool { return strings.Contains(s, "note.txt") })
	a.send(t, "/note")
	a.waitForScreen(t, "the search", func(s string) bool { return strings.Contains(s, "› note") })
	a.send(t, "\r")
	a.waitForScreen(t, "the editor in a tab of its own", func(s string) bool {
		return strings.Contains(s, "hello-from-note") && strings.Contains(a.lines()[0], "note.txt")
	})
}

// TestTheFilesPanelOpensFromThePaneMenu: the panel is on a pane's
// right-click menu as well as on prefix+f. If it regresses, someone driving
// tend with the mouse has no way to find it.
func TestTheFilesPanelOpensFromThePaneMenu(t *testing.T) {
	t.Setenv("PATH", filepath.Dir(buildBinary(t))+string(os.PathListSeparator)+os.Getenv("PATH"))
	a := startSession(t, 120, 30)
	a.waitForScreen(t, "a pane", func(s string) bool { return strings.Contains(s, "┌") })
	a.openMenuOn(t, 70, 12, "files panel")
	a.clickAt(t, 72, a.lineContaining(t, "files panel"))
	a.waitForScreen(t, "the panel", func(s string) bool { return strings.Contains(s, "files │ search │ changes") })
}

// TestTextFoundInThePanelOpensTheEditorOnItsLine: ctrl+f in the panel
// searches the project's files for text, and enter on a result opens the
// editor there — vi is given +line. If it regresses, the panel finds where
// something is written and then opens the file at its top.
func TestTextFoundInThePanelOpensTheEditorOnItsLine(t *testing.T) {
	project := gitProject(t)
	if err := os.WriteFile(filepath.Join(project, "notes.txt"), []byte("one\ntwo\nfind-this-line\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A vi that says what it was asked to open, and stays.
	fake := t.TempDir()
	if err := os.WriteFile(filepath.Join(fake, "vi"), []byte("#!/bin/sh\necho \"EDITING $*\"\nsleep 60\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	runtimeDir, err := os.MkdirTemp("", "tf")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(runtimeDir) })
	t.Setenv("TEND_RUNTIME_DIR", runtimeDir)
	env := append(os.Environ(),
		"TEND_RUNTIME_DIR="+runtimeDir, "SHELL=/bin/sh",
		"TEND_CONFIG="+quietConfig(t),
		"PATH="+fake+string(os.PathListSeparator)+os.Getenv("PATH"),
		"EDITOR=vi", "VISUAL=",
	)
	bin := buildBinary(t)
	p, err := pty.Start(bin, []string{"attach", "-s", "grep"}, pty.Options{Size: pty.Size{Cols: 130, Rows: 30}, Env: env})
	if err != nil {
		t.Fatal(err)
	}
	a := &attached{pty: p, screen: vt.NewScreen(130, 30, 100)}
	go func() { _, _ = io.Copy(a, p) }()
	t.Cleanup(func() {
		_ = p.Close()
		stopSession(t, "grep")
	})
	a.waitForScreen(t, "a pane", func(s string) bool { return strings.Contains(s, "┌") })
	a.sendUntil(t, "cd "+project+" && echo in-project\n", "the shell in the project",
		func(s string) bool {
			return strings.Contains(s, "\nin-project") || strings.Contains(s, "│in-project")
		})
	a.send(t, "\x02f")
	a.waitForScreen(t, "the panel", func(s string) bool { return strings.Contains(s, "notes.txt") })

	a.send(t, "\x06find-this")
	a.waitForScreen(t, "the result", func(s string) bool {
		return strings.Contains(s, "1 in 1 files") && strings.Contains(s, "3 find-this-line")
	})
	a.send(t, "\r\x1b[B\r")
	a.waitForScreen(t, "vi on line 3", func(s string) bool {
		return strings.Contains(s, "EDITING +3 "+filepath.Join(project, "notes.txt"))
	})
}

// TestThePanelFollowsThePaneBesideItToAnotherProject: with the panel open,
// the shell beside it cd'ing into another repository takes the panel there
// at its next look. If it regresses, the panel shows the project the agent
// has left.
func TestThePanelFollowsThePaneBesideItToAnotherProject(t *testing.T) {
	first, second := gitProject(t), gitProject(t)
	if err := os.WriteFile(filepath.Join(second, "only-in-second.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runtimeDir, err := os.MkdirTemp("", "tf")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(runtimeDir) })
	t.Setenv("TEND_RUNTIME_DIR", runtimeDir)
	env := append(os.Environ(), "TEND_RUNTIME_DIR="+runtimeDir, "SHELL=/bin/sh",
		"TEND_CONFIG="+quietConfig(t))
	bin := buildBinary(t)
	p, err := pty.Start(bin, []string{"attach", "-s", "follow"}, pty.Options{Size: pty.Size{Cols: 130, Rows: 30}, Env: env})
	if err != nil {
		t.Fatal(err)
	}
	a := &attached{pty: p, screen: vt.NewScreen(130, 30, 100)}
	go func() { _, _ = io.Copy(a, p) }()
	t.Cleanup(func() {
		_ = p.Close()
		stopSession(t, "follow")
	})
	a.waitForScreen(t, "a pane", func(s string) bool { return strings.Contains(s, "┌") })
	a.sendUntil(t, "cd "+first+" && echo in-first\n", "the shell in the first project",
		func(s string) bool { return strings.Contains(s, "\nin-first") || strings.Contains(s, "│in-first") })
	a.send(t, "\x02f")
	a.waitForScreen(t, "the panel on the first project", func(s string) bool {
		return strings.Contains(s, "brandnew.md") && !strings.Contains(s, "only-in-second.txt")
	})

	// Back to the shell, by a click on it — left of the panel, which is on
	// the right — and into the other project.
	a.clickAt(t, 50, 15)
	a.sendUntil(t, "cd "+second+" && echo in-second\n", "the shell in the second project",
		func(s string) bool { return strings.Contains(s, "in-second") })
	a.waitForScreen(t, "the panel to follow", func(s string) bool { return strings.Contains(s, "only-in-second.txt") })
}

// TestThePanelPreviewsInATabAndReusesIt: space on a file shows it read-only
// in a tab of its own named after it, with its line numbers, and goes
// there; back on the panel's tab, the panel has the keys again, as it was
// left with them, and space on another file shows that one in the same tab,
// renamed. If it regresses, looking at a file squeezes the terminal being
// worked in, every file looked at leaves a tab behind, or going back hands
// the panel's keys to the shell beside it.
func TestThePanelPreviewsInATabAndReusesIt(t *testing.T) {
	project := gitProject(t)
	runtimeDir, err := os.MkdirTemp("", "tf")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(runtimeDir) })
	t.Setenv("TEND_RUNTIME_DIR", runtimeDir)
	env := append(os.Environ(), "TEND_RUNTIME_DIR="+runtimeDir, "SHELL=/bin/sh",
		"TEND_CONFIG="+quietConfig(t))
	bin := buildBinary(t)
	p, err := pty.Start(bin, []string{"attach", "-s", "preview"}, pty.Options{Size: pty.Size{Cols: 140, Rows: 30}, Env: env})
	if err != nil {
		t.Fatal(err)
	}
	a := &attached{pty: p, screen: vt.NewScreen(140, 30, 100)}
	go func() { _, _ = io.Copy(a, p) }()
	t.Cleanup(func() {
		_ = p.Close()
		stopSession(t, "preview")
	})
	a.waitForScreen(t, "a pane", func(s string) bool { return strings.Contains(s, "┌") })
	a.sendUntil(t, "cd "+project+" && echo in-project\n", "the shell in the project",
		func(s string) bool {
			return strings.Contains(s, "\nin-project") || strings.Contains(s, "│in-project")
		})
	a.send(t, "\x02f")
	a.waitForScreen(t, "the panel", func(s string) bool { return strings.Contains(s, "kept.txt") })

	// The tree is src/, brandnew.md, kept.txt: the last row.
	a.send(t, "G ")
	// The tab bar is the first line: "tab 1  kept.txt  +".
	tabs := func(s string) string { return strings.SplitN(s, "\n", 2)[0] }
	a.waitForScreen(t, "the preview of kept.txt, in its own tab", func(s string) bool {
		return strings.Contains(s, "1 one") && strings.Contains(tabs(s), "kept.txt") && !strings.Contains(s, "brandnew.md")
	})

	// Back to the panel's tab, and the file above.
	a.send(t, "\x021")
	a.waitForScreen(t, "the panel again", func(s string) bool { return strings.Contains(s, "brandnew.md") })
	a.send(t, "k ")
	a.waitForScreen(t, "brandnew.md in the same tab, renamed", func(s string) bool {
		// Markdown, so rendered: "# new" reads as the heading NEW.
		return strings.Contains(s, "NEW") && !strings.Contains(s, "1 one") &&
			strings.Contains(tabs(s), "brandnew.md") && !strings.Contains(tabs(s), "kept.txt")
	})
}

// TestTheFilesPanelDocksOnTheLeftWhenSetTo: dock = "left" in [files]
// opens the panel on the other edge. If it regresses, the setting is
// written and ignored.
func TestTheFilesPanelDocksOnTheLeftWhenSetTo(t *testing.T) {
	t.Setenv("PATH", filepath.Dir(buildBinary(t))+string(os.PathListSeparator)+os.Getenv("PATH"))
	cfg := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(cfg, []byte("[files]\ndock = \"left\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	configOverride = cfg
	t.Cleanup(func() { configOverride = "" })
	a := startSession(t, 120, 30)
	a.waitForScreen(t, "a pane", func(s string) bool { return strings.Contains(s, "┌") })
	a.send(t, "\x02f")
	a.waitForScreen(t, "the panel", func(s string) bool { return strings.Contains(s, "files │ search │ changes") })
	if top := a.lines()[1]; strings.Index(top, "files") > strings.Index(top, "sh ") {
		t.Errorf("the panel's frame should come before the shell's:\n%s", top)
	}
}

// TestThePanelKeepsItsIconsBesideASettingItDoesNotKnow: the panel reads
// the settings file for its own part and passes over a key from a newer
// tend. If it regresses, a panel started before an update shows no icons
// once the settings screen of the new one has written anything.
func TestThePanelKeepsItsIconsBesideASettingItDoesNotKnow(t *testing.T) {
	project := gitProject(t)
	t.Setenv("PATH", filepath.Dir(buildBinary(t))+string(os.PathListSeparator)+os.Getenv("PATH"))
	cfg := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(cfg, []byte("[files]\nicons = \"emoji\"\n\n[from_a_newer_tend]\nx = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The client is strict about the file, and refuses it; the panel, in
	// its pane, is given it after the client started — the in-process
	// server hands its environment to the panes it starts.
	a := startSession(t, 120, 30)
	t.Setenv("TEND_CONFIG", cfg)
	a.waitForScreen(t, "a pane", func(s string) bool { return strings.Contains(s, "┌") })
	a.sendUntil(t, "cd "+project+" && echo in-project\n", "the shell in the project",
		func(s string) bool {
			return strings.Contains(s, "\nin-project") || strings.Contains(s, "│in-project")
		})
	a.send(t, "\x02f")
	a.waitForScreen(t, "the panel with icons", func(s string) bool {
		return strings.Contains(s, "📁 src") || strings.Contains(s, "📁") && strings.Contains(s, "src")
	})
}

// TestAnOpenPanelMovesToANewBuildByItself: with the panel open, a new tend
// binary put where it was started from replaces it in the same process and
// pane, with the folder that was open still open. If it regresses, every
// install needs the panel closed and opened again before it shows.
func TestAnOpenPanelMovesToANewBuildByItself(t *testing.T) {
	project := gitProject(t)
	dir := t.TempDir()
	bin := filepath.Join(dir, "tend")
	copyFile := func(to string) {
		t.Helper()
		raw, err := os.ReadFile(buildBinary(t))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(to, raw, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	copyFile(bin)
	p, err := pty.Start(bin, []string{"files", project}, pty.Options{
		Size: pty.Size{Cols: 40, Rows: 16},
		// Not in a session, even when the tests run inside one: a panel
		// that finds a session follows the pane beside it elsewhere.
		Env: append(withoutEnv(withoutEnv(os.Environ(), "TEND_SOCKET_PATH"), "TEND_PANE_ID"),
			"TEND_CONFIG="+quietConfig(t), "TERM=xterm-256color"),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Close() })
	a := &attached{pty: p, screen: vt.NewScreen(40, 16, 0)}
	go func() { _, _ = io.Copy(a, p) }()
	a.waitForScreen(t, "the tree", func(s string) bool { return strings.Contains(s, "▸ src") })
	a.send(t, "l")
	a.waitForScreen(t, "src open", func(s string) bool { return strings.Contains(s, "▾ src") })

	// A new build, put in place as an install does: written beside, then
	// renamed over. Its time is set back so it has already settled.
	copyFile(bin + ".new")
	past := time.Now().Add(-5 * time.Second)
	_ = os.Chtimes(bin+".new", past, past)
	if err := os.Rename(bin+".new", bin); err != nil {
		t.Fatal(err)
	}
	exe := "/proc/" + itoa(p.Pid()) + "/exe"
	deadline := time.Now().Add(15 * time.Second)
	for {
		target, err := os.Readlink(exe)
		if err == nil && !strings.HasSuffix(target, "(deleted)") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the panel still runs the old build: %s %v", target, err)
		}
		time.Sleep(200 * time.Millisecond)
	}
	a.waitForScreen(t, "the new build, with src still open", func(s string) bool {
		return strings.Contains(s, "▾ src") && strings.Contains(s, "kept.txt")
	})
}

// TestThePanelOpensInEveryTabUnlessClosedThere: with [files] auto_open, a
// tab shown without the panel gets one beside the pane in focus, which keeps
// the keys; a panel closed in a tab stays closed there; a new tab gets its
// own. If it regresses, every new tab needs prefix+f again, or a panel the
// user closed comes straight back.
func TestThePanelOpensInEveryTabUnlessClosedThere(t *testing.T) {
	t.Setenv("PATH", filepath.Dir(buildBinary(t))+string(os.PathListSeparator)+os.Getenv("PATH"))
	cfg := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(cfg, []byte("[files]\nauto_open = true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	withConfig(t, cfg)
	a := startSession(t, 120, 30)
	panel := func(s string) bool { return strings.Contains(s, " files ─") }
	a.waitForScreen(t, "the panel, opened by itself", panel)
	a.sendUntil(t, "echo still-the-shell\n", "the keys still with the shell", func(s string) bool {
		return strings.Contains(s, "still-the-shell")
	})

	// prefix+f goes to the panel, and again closes it.
	a.send(t, "\x02f")
	time.Sleep(300 * time.Millisecond)
	a.send(t, "\x02f")
	a.waitForScreen(t, "the panel closed", func(s string) bool { return !panel(s) })
	time.Sleep(time.Second)
	if panel(strings.Join(a.lines(), "\n")) {
		t.Fatal("a panel closed by hand came back by itself")
	}

	a.send(t, "\x02c")
	a.waitForScreen(t, "a new tab, with its panel", func(s string) bool {
		return strings.Contains(s, "tab 2") && panel(s)
	})
}

// TestAnSSHConnectionsSpaceHasNoFilesPanelUnlessAskedFor: with the panel
// opening in every tab, a space the ssh list opens for a connection gets
// none — it would show this machine's files beside the other machine's
// shell, in the width a wide lxc list needs — and prefix+f still opens one
// there, which then stays. If it regresses, every connection opens beside
// a panel of the wrong machine's files, as the owner's did.
func TestAnSSHConnectionsSpaceHasNoFilesPanelUnlessAskedFor(t *testing.T) {
	t.Setenv("PATH", filepath.Dir(buildBinary(t))+string(os.PathListSeparator)+os.Getenv("PATH"))
	bin, store := t.TempDir(), filepath.Join(t.TempDir(), "ssh.toml")
	if err := os.WriteFile(filepath.Join(bin, "ssh"), []byte("#!/bin/sh\necho \"FAKE SSH $*\"\nexec sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	var s sshconn.Store
	s.Put(sshconn.Connection{Name: "box", Host: "box.example"})
	if err := sshconn.Save(store, s); err != nil {
		t.Fatal(err)
	}
	t.Setenv(sshconn.PathEnv, store)
	cfg := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(cfg, []byte("[files]\nauto_open = true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	withConfig(t, cfg)

	a := startSession(t, 140, 30)
	panel := func(s string) bool { return strings.Contains(s, " files ─") }
	a.waitForScreen(t, "the panel in the first space", panel)

	a.clickText(t, "⇄ ssh")
	a.waitForScreen(t, "the ssh list", func(string) bool { return strings.Contains(a.sidebarText(), "○ box") })
	a.clickAt(t, 5, a.lineContaining(t, "○ box"))
	a.clickAt(t, 5, a.lineContaining(t, "○ box"))
	a.waitForScreen(t, "box open", func(s string) bool { return strings.Contains(s, "FAKE SSH -- box") })
	// Absence has no event to wait for: long enough for the panel, which
	// opened within a frame or two in the first space, to have come.
	time.Sleep(1500 * time.Millisecond)
	if screen := strings.Join(a.lines(), "\n"); panel(screen) {
		t.Fatalf("the connection's space got a files panel:\n%s", screen)
	}

	a.send(t, "\x02f")
	a.waitForScreen(t, "the panel, asked for", panel)
	time.Sleep(time.Second)
	if !panel(strings.Join(a.lines(), "\n")) {
		t.Fatal("a panel opened by hand in the connection's space was put away")
	}
}

// TestTheWheelTurnsTheOtherWayWhenSetTo: with [ui] invert_wheel, the wheel
// read as down scrolls back into what a pane printed, and up comes forward
// again — for a system whose wheel reaches the terminal reversed, as the
// owner's did in every pane. If it regresses, the setting is written and
// the wheel still goes the wrong way.
func TestTheWheelTurnsTheOtherWayWhenSetTo(t *testing.T) {
	log := filepath.Join(t.TempDir(), "x.log")
	var lines strings.Builder
	for i := 1; i <= 200; i++ {
		lines.WriteString("LINE-" + itoa(1000 + i)[1:] + "\n")
	}
	if err := os.WriteFile(log, []byte(lines.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(cfg, []byte(quietSettings+"\n[ui]\ninvert_wheel = true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	withConfig(t, cfg)
	a := startSession(t, 100, 20)
	a.waitForScreen(t, "a pane", func(s string) bool { return strings.Contains(s, "┌") })
	a.send(t, "exec tail -n 40 -F "+log+"\r")
	a.waitForScreen(t, "the end of the log", func(s string) bool { return strings.Contains(s, "LINE-200") })

	a.wheelAt(t, 50, 8, false) // down, which is back here
	a.wheelAt(t, 50, 8, false)
	a.waitForScreen(t, "scrolled back", func(s string) bool { return !strings.Contains(s, "LINE-200") })
	a.wheelAt(t, 50, 8, true)
	a.wheelAt(t, 50, 8, true)
	a.wheelAt(t, 50, 8, true)
	a.waitForScreen(t, "forward to the end again", func(s string) bool { return strings.Contains(s, "LINE-200") })
}

// TestAFilesPanelThatCannotStartIsNotDockedAgainAndAgain: with the panel
// opening in every tab and no tend to run it — none on the PATH of the
// machine the session is on — the panel dies as it starts, and the tab
// counts it as closed rather than docking another on the next refresh. If
// it regresses, tend docks a panel many times a second, as it did on
// GitHub's runner, and the client falls behind on keys and frames: a
// ctrl+b R went unanswered.
func TestAFilesPanelThatCannotStartIsNotDockedAgainAndAgain(t *testing.T) {
	t.Setenv("TEND_BIN_PATH", filepath.Join(t.TempDir(), "no-tend-here"))
	cfg := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(cfg, []byte("[files]\nauto_open = true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	withConfig(t, cfg)
	a := startSession(t, 120, 24)
	a.waitForScreen(t, "a pane", func(s string) bool { return strings.Contains(s, "┌ 1 ") })
	// Long enough for a loop to have docked dozens; a panel that died once
	// is pane 2, and a new tab after it is 3.
	time.Sleep(2 * time.Second)
	a.send(t, "\x02c")
	a.waitForScreen(t, "a new tab", func(s string) bool { return strings.Contains(s, "tab 2") })
	title := a.lines()[1]
	n := 0
	if i := strings.Index(title, "┌ "); i >= 0 {
		for _, r := range title[i+len("┌ "):] {
			if r < '0' || r > '9' {
				break
			}
			n = n*10 + int(r-'0')
		}
	}
	if n == 0 || n > 4 {
		t.Errorf("the new tab's pane is number %d: the panel was docked again and again\n%s", n, title)
	}
}
