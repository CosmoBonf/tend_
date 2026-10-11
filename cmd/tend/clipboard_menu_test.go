//go:build unix

package main

import (
	"strings"
	"testing"
)

// TestThePaneMenuCopiesTheSelectionAndPastesIt: text selected with the mouse
// is offered for copying by a right-click on it, "copy" puts it on the
// clipboard, and "paste" types it into the pane. The desktop's clipboard
// tools are kept out of reach, so the paste is of what tend itself copied —
// what it pastes over ssh, where the clipboard cannot be read. If it
// regresses, a selection's menu cannot copy it, or paste does nothing.
func TestThePaneMenuCopiesTheSelectionAndPastesIt(t *testing.T) {
	t.Setenv("WAYLAND_DISPLAY", "")
	t.Setenv("DISPLAY", "")
	a := startSession(t, 100, 16)
	a.waitForScreen(t, "a pane", func(s string) bool { return strings.Contains(s, "┌") })
	clip := clipboardOf(a)
	row, col := a.writeLines(t, "alpha bravo charlie", "delta echo foxtrot")

	a.dragFromTo(t, 0, col, row, col+10, row)
	a.waitForScreen(t, "the copy on release", func(s string) bool {
		return strings.Contains(s, "copied 11 characters")
	})

	a.openMenuOn(t, col+3, row, "copy")
	if !strings.Contains(a.text(), "paste") {
		t.Fatalf("no paste beside copy:\n%s", a.text())
	}
	a.clickAt(t, col+5, a.lineContaining(t, " copy"))
	a.waitForScreen(t, "copied from the menu", func(s string) bool {
		return clip() == "alpha bravo"
	})

	a.openMenuOn(t, col+3, row, "paste")
	a.clickAt(t, col+5, a.lineContaining(t, " paste"))
	a.waitForScreen(t, "the paste typed into the prompt", func(s string) bool {
		return strings.Contains(s, "pasted 11 characters") && strings.Count(s, "alpha bravo") >= 2
	})
}

// TestThePaneMenuOffersWhatAMouseTakingProgramCopied: an agent that takes
// the mouse selects for itself and copies what it selected (OSC 52), so
// tend has no selection of its own there; what the program copied stands
// for it, and the menu offers to copy it until a key is typed into the
// pane. If it regresses, right-clicking text selected in Claude Code shows
// no copy — what the owner found.
func TestThePaneMenuOffersWhatAMouseTakingProgramCopied(t *testing.T) {
	t.Setenv("WAYLAND_DISPLAY", "")
	t.Setenv("DISPLAY", "")
	a := startSession(t, 100, 16)
	a.waitForScreen(t, "a pane", func(s string) bool { return strings.Contains(s, "┌") })
	// The program's copy: "selected" in base64, as Claude Code sends it.
	a.send(t, "printf '\\033]52;c;c2VsZWN0ZWQ=\\007'; echo sent\r")
	a.waitForScreen(t, "the program's copy taken", func(s string) bool {
		return strings.Contains(s, "copied 8 characters")
	})
	a.openMenuOn(t, 50, 8, "copy")
	a.clickAt(t, 52, a.lineContaining(t, " copy"))
	a.waitForScreen(t, "copied again from the menu", func(s string) bool {
		return strings.Contains(s, "copied 8 characters")
	})

	// A key typed into the pane: the moment has passed.
	a.send(t, "x")
	a.waitForScreen(t, "the key in the pane", func(s string) bool { return strings.Contains(s, "$ x") })
	a.openMenuOn(t, 50, 8, "paste")
	if strings.Contains(a.text(), " copy ") {
		t.Errorf("copy still offered after typing:\n%s", a.text())
	}
}

// TestTheWheelOverAFullScreenProgramIsArrowKeys: a program on the
// alternate screen that does not take the mouse gets the wheel as the
// arrow keys, as herdr sends it. If it regresses, the wheel over the dsh
// TUI or a pager opens tend's scrollback instead, where nothing the program
// drew is.
func TestTheWheelOverAFullScreenProgramIsArrowKeys(t *testing.T) {
	a := startSession(t, 100, 16)
	a.waitForScreen(t, "a pane", func(s string) bool { return strings.Contains(s, "┌") })
	a.send(t, "printf '\\033[?1049h'; cat -v\r")
	a.waitForScreen(t, "the alternate screen", func(s string) bool { return !strings.Contains(s, "cat -v") })
	// The snapshot learns the pane's modes on the next poll.
	a.sendUntil(t, "\x1b[<64;50;8M", "an arrow up from the wheel", func(s string) bool {
		return strings.Contains(s, "^[[A")
	})
	a.wheelAt(t, 50, 8, false)
	a.waitForScreen(t, "an arrow down from the wheel", func(s string) bool { return strings.Contains(s, "^[[B") })
}
