//go:build unix

package main

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/auth-com-br/tend/internal/pty"
	"github.com/auth-com-br/tend/internal/vt"
)

// TestAgentsComeBackAsleepAndWakeWhenGoneTo: two agents with their
// conversations; the server restarts; both come back asleep and only the
// one being looked at wakes, in its conversation. A click on the other
// wakes it; its menu puts it to sleep again. The agent is a stand-in that
// says how it was started; the restart, the panes, the focus and the menu
// are real. If it regresses, a restart starts every agent at once again,
// or a sleeping one cannot be woken, or not into its conversation.
func TestAgentsComeBackAsleepAndWakeWhenGoneTo(t *testing.T) {
	bin := t.TempDir()
	claude := "#!/bin/sh\necho \"CLAUDE-RUNNING:$*\"\nwhile read line; do echo \"got $line\"; done\n"
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(claude), 0o755); err != nil {
		t.Fatal(err)
	}
	runtimeDir := t.TempDir()
	t.Setenv("TEND_RUNTIME_DIR", runtimeDir)
	configPath := quietConfig(t)
	t.Setenv("TEND_CONFIG", configPath)
	env := append(os.Environ(), "TEND_RUNTIME_DIR="+runtimeDir, "TEND_CONFIG="+configPath,
		"SHELL=/bin/sh", "TERM=xterm-256color", "PATH="+bin+":"+os.Getenv("PATH"))
	tend := buildBinary(t)
	p, err := pty.Start(tend, []string{"attach", "-s", "sleepy"}, pty.Options{Size: pty.Size{Cols: 130, Rows: 30}, Env: env})
	if err != nil {
		t.Fatal(err)
	}
	a := &attached{pty: p, screen: vt.NewScreen(130, 30, 100)}
	go func() { _, _ = io.Copy(a, p) }()
	t.Cleanup(func() { _ = p.Close(); stopSession(t, "sleepy") })
	a.waitForScreen(t, "a pane", func(s string) bool { return strings.Contains(s, "┌") })

	// Two agents, each typed at a prompt, each with its conversation
	// reported as Claude Code's hook reports it.
	a.sendUntil(t, "claude first\n", "the first agent", func(s string) bool { return strings.Contains(s, "CLAUDE-RUNNING:first") })
	a.send(t, "\x02|")
	a.waitForScreen(t, "two panes", func(s string) bool { return strings.Count(s, "┌") == 2 })
	a.sendUntil(t, "claude second\n", "the second agent", func(s string) bool { return strings.Contains(s, "CLAUDE-RUNNING:second") })
	for pane, id := range map[string]string{"p_1": "conv-1", "p_2": "conv-2"} {
		cmd := exec.Command(tend, "api", "-s", "sleepy", "pane.report_agent_session",
			`{"pane_id":"`+pane+`","source":"tend:claude","agent":"claude","agent_session_id":"`+id+`","seq":1}`)
		cmd.Env = env
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
	}
	// What is saved is what comes back: wait for the conversations to be
	// in the state file, written within a second.
	state := filepath.Join(runtimeDir, "state", "sleepy.json")
	a.waitForScreen(t, "the conversations written down", func(string) bool {
		data, _ := os.ReadFile(state)
		return strings.Contains(string(data), "conv-1") && strings.Contains(string(data), "conv-2")
	})

	c, err := connect("sleepy", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Shutdown(); err != nil {
		t.Fatal(err)
	}
	_ = c.Close()

	// Back: the pane in front wakes in its conversation; the other sleeps.
	woken := func(s, conv string) bool { return strings.Contains(s, "CLAUDE-RUNNING:--resume "+conv) }
	a.waitForScreen(t, "one awake, one asleep", func(s string) bool {
		return woken(s, "conv-1") != woken(s, "conv-2") && strings.Contains(s, "claude asleep")
	})

	// A click on the sleeping one wakes it: the left pane is the first.
	x, other := 90, "conv-2"
	if woken(a.text(), "conv-2") {
		x, other = 40, "conv-1"
	}
	a.clickAt(t, x, 12)
	a.waitForScreen(t, "the other awake too", func(s string) bool { return woken(s, other) })

	// And its menu puts it back to sleep.
	a.waitForScreen(t, "the agent detected", func(s string) bool { return sidebarHas(s, "claude") })
	a.rightClickAt(t, x, 12)
	a.waitForScreen(t, "the menu", func(s string) bool { return strings.Contains(s, "sleep (free its memory)") })
	row := a.lineContaining(t, "sleep (free its memory)")
	a.clickAt(t, columnOfString(a.lines()[row-1], "sleep (free")+2, row)
	// Said on the status line, with what it gave back; the pane's own line
	// says "asleep" too, and is not what is checked.
	a.waitForScreen(t, "it asleep again, and what that gave back", func(string) bool {
		lines := a.lines()
		status := lines[len(lines)-1]
		return strings.Contains(status, "claude is asleep, ") && strings.Contains(status, "given back")
	})

	// The other one, from the main menu: "sleep an agent..." lists the
	// agents awake, and choosing one puts it to sleep.
	a.waitForScreen(t, "the status line free", func(string) bool {
		lines := a.lines()
		return !strings.Contains(lines[len(lines)-1], "given back")
	})
	row = a.lineContaining(t, "menu")
	a.clickAt(t, columnOf(a.lines()[row-1], 'm')+1, row)
	a.waitForScreen(t, "the main menu", func(s string) bool { return strings.Contains(s, "sleep an agent...") })
	row = a.lineContaining(t, "sleep an agent...")
	a.clickAt(t, columnOfString(a.lines()[row-1], "sleep an agent")+2, row)
	a.waitForScreen(t, "the agents awake", func(s string) bool { return strings.Contains(s, "sleep which agent?") })
	row = a.lineContaining(t, "claude · ")
	a.clickAt(t, columnOfString(a.lines()[row-1], "claude · ")+2, row)
	a.waitForScreen(t, "both asleep", func(s string) bool { return strings.Count(s, "claude asleep") >= 2 })
}
