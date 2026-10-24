//go:build unix

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/auth-com-br/tend/internal/sshconn"
)

// clickText clicks the middle of the first place on the screen showing text.
func (a *attached) clickText(t *testing.T, text string) {
	t.Helper()
	y := a.lineContaining(t, text)
	a.clickAt(t, columnOfString(a.lines()[y-1], text)+2, y)
}

// openSideBySide opens "side by side..." from the sidebar's "new".
func (a *attached) openSideBySide(t *testing.T) {
	t.Helper()
	a.clickAt(t, 2, a.lineContaining(t, "new"))
	a.waitForScreen(t, "the new menu", func(s string) bool { return strings.Contains(s, "side by side...") })
	a.clickText(t, "side by side...")
	a.waitForScreen(t, "the popup", func(s string) bool { return strings.Contains(s, "type to filter") })
}

// TestAPaneOpenElsewhereIsBroughtBesideAndSentBack is "side by side..." from
// end to end: a shell in another space, picked from the spaces list, comes
// beside the pane in view still running what it ran, its emptied space goes,
// and "send to new tab" on its menu takes it into a tab of its own. If it
// regresses, two things open at once cannot be seen together without
// starting one of them again.
func TestAPaneOpenElsewhereIsBroughtBesideAndSentBack(t *testing.T) {
	a := startSession(t, 120, 24)
	a.waitForScreen(t, "a pane", func(s string) bool { return strings.Contains(s, "┌") })

	a.send(t, "\x02N")
	a.waitForScreen(t, "the second space", func(string) bool { return sidebarHas(a.text(), "space 2") })
	a.send(t, "echo MARK-B-$((6*7))\r")
	a.waitForScreen(t, "its shell ran", func(s string) bool { return strings.Contains(s, "MARK-B-42") })
	a.send(t, "\x02(")
	a.waitForScreen(t, "back in the first space", func(s string) bool { return !strings.Contains(s, "MARK-B-42") })
	a.send(t, "echo MARK-A-$((6*7))\r")
	a.waitForScreen(t, "the first shell ran", func(s string) bool { return strings.Contains(s, "MARK-A-42") })

	a.openSideBySide(t)
	a.waitForScreen(t, "the other space's pane listed", func(s string) bool { return strings.Contains(s, "space 2 ›") })
	a.send(t, "\r")
	a.waitForScreen(t, "both shells side by side, the second space gone", func(s string) bool {
		return strings.Contains(s, "MARK-A-42") && strings.Contains(s, "MARK-B-42") &&
			!sidebarHas(s, "space 2") && !strings.Contains(s, "type to filter")
	})

	y := a.lineContaining(t, "MARK-B-42")
	a.openMenuOn(t, columnOfString(a.lines()[y-1], "MARK-B-42"), y, "send to new tab")
	a.clickText(t, "send to new tab")
	a.waitForScreen(t, "a tab of its own", func(s string) bool {
		return strings.Contains(a.lines()[0], "tab 2") && strings.Contains(s, "MARK-B-42") && !strings.Contains(s, "MARK-A-42")
	})
}

// TestAnSSHConnectionIsBroughtBesideAndStaysKnown: an open connection,
// picked from the ssh list of "side by side...", comes beside the pane in
// view and the ssh list still marks it open; a connection not open is opened
// there; and when one of them ends, q closes that pane alone, not the space
// it was brought to. If it regresses, the ● is lost and a click opens the
// connection twice, or quitting ssh closes somebody's work.
func TestAnSSHConnectionIsBroughtBesideAndStaysKnown(t *testing.T) {
	bin, store := t.TempDir(), filepath.Join(t.TempDir(), "ssh.toml")
	if err := os.WriteFile(filepath.Join(bin, "ssh"), []byte("#!/bin/sh\necho \"FAKE SSH $*\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	var s sshconn.Store
	s.Put(sshconn.Connection{Name: "box", Host: "box.example"})
	s.Put(sshconn.Connection{Name: "db", Host: "db.example"})
	if err := sshconn.Save(store, s); err != nil {
		t.Fatal(err)
	}
	t.Setenv(sshconn.PathEnv, store)

	a := startSession(t, 120, 24)
	a.waitForScreen(t, "a pane", func(s string) bool { return strings.Contains(s, "┌") })
	a.send(t, "echo MARK-A-$((6*7))\r")
	a.waitForScreen(t, "the shell ran", func(s string) bool { return strings.Contains(s, "MARK-A-42") })

	// box, opened in a space of its own from the ssh list.
	a.clickText(t, "⇄ ssh")
	a.waitForScreen(t, "the ssh list", func(string) bool { return strings.Contains(a.sidebarText(), "○ box") })
	a.clickAt(t, 5, a.lineContaining(t, "○ box"))
	a.clickAt(t, 5, a.lineContaining(t, "○ box"))
	a.waitForScreen(t, "box open", func(s string) bool { return strings.Contains(s, "FAKE SSH -- box") })
	a.send(t, "\x02(")
	a.waitForScreen(t, "back in the first space", func(s string) bool {
		return strings.Contains(s, "MARK-A-42") && !strings.Contains(s, "FAKE SSH")
	})

	a.openSideBySide(t)
	// The ssh button, as a hand would; the popup opens on the list the
	// sidebar shows, which is the ssh list here already.
	by := a.lineContaining(t, "spaces   ssh")
	a.clickAt(t, columnOfString(a.lines()[by-1], "spaces   ssh")+10, by)
	a.waitForScreen(t, "the connections", func(s string) bool { return strings.Contains(s, "● open") && strings.Contains(s, "connect") })
	a.send(t, "box\r")
	a.waitForScreen(t, "box beside the shell", func(s string) bool {
		return strings.Contains(s, "MARK-A-42") && strings.Contains(s, "FAKE SSH -- box") && !strings.Contains(s, "type to filter")
	})
	if side := a.sidebarText(); !strings.Contains(side, "● box") {
		t.Errorf("the ssh list lost box once it was moved:\n%s", side)
	}

	a.openSideBySide(t)
	a.send(t, "db\r")
	a.waitForScreen(t, "db opened beside", func(s string) bool {
		return strings.Contains(s, "FAKE SSH -- db") && strings.Contains(s, "FAKE SSH -- box") && strings.Contains(s, "MARK-A-42")
	})
	a.waitForScreen(t, "db marked open", func(string) bool { return strings.Contains(a.sidebarText(), "● db") })

	// db is in focus, at "connect again"; q closes it, and only it.
	a.send(t, "q\r")
	a.waitForScreen(t, "db closed alone", func(s string) bool {
		return !strings.Contains(s, "FAKE SSH -- db") && strings.Contains(s, "FAKE SSH -- box") &&
			strings.Contains(s, "MARK-A-42") && strings.Contains(a.sidebarText(), "○ db")
	})
}
