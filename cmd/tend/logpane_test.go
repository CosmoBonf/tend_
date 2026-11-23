package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestTheLogButtonOpensAndHidesTheLivePane: "▴ log" on the status bar opens
// a pane following ~/tend-dev.log under the agent, the button turns to
// "▾ log", and a second click puts the pane away. If it regresses, the
// owner is back to typing a tail command to watch what an agent runs.
func TestTheLogButtonOpensAndHidesTheLivePane(t *testing.T) {
	home := t.TempDir()
	for _, v := range []string{"GOPATH", "GOMODCACHE", "GOCACHE"} {
		if out, err := exec.Command("go", "env", v).Output(); err == nil {
			t.Setenv(v, strings.TrimSpace(string(out)))
		}
	}
	t.Setenv("HOME", home)
	if err := os.WriteFile(filepath.Join(home, "tend-dev.log"), []byte("==== 10:00:00 · make check ====\nok  all green\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	a := startSession(t, 160, 40)
	bar := func() string { return a.lines()[len(a.lines())-1] }
	a.waitForScreen(t, "the log button", func(string) bool { return strings.Contains(bar(), "▴ log") })
	a.clickAt(t, columnOfString(bar(), "▴ log")+2, len(a.lines()))
	a.waitForScreen(t, "the log in a pane", func(s string) bool {
		return strings.Contains(s, "ok  all green") && strings.Contains(bar(), "▾ log")
	})

	a.clickAt(t, columnOfString(bar(), "▾ log")+2, len(a.lines()))
	a.waitForScreen(t, "the log put away", func(s string) bool {
		return !strings.Contains(s, "ok  all green") && strings.Contains(bar(), "▴ log")
	})
}
