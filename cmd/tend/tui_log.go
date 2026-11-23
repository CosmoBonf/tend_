package main

import (
	"strings"

	"github.com/auth-com-br/tend/internal/proto"
)

// The live log pane: a short pane under the agent, following
// ~/tend-dev.log, where an agent working on the owner's machine writes the
// long commands it runs (make check, go test, gh run watch) as they run.
// The owner asked to watch them without typing a command, and to put the
// pane away and bring it back from a button: "▴ log" on the status bar
// opens it, "▾ log" hides it.

// logTitle is the name the log pane goes by, which is how the button finds
// the one it opened.
const logTitle = "log"

// logRows is how tall the pane opens: enough to see a test run go by, not
// so much that the agent above loses its screen.
const logRows = 12

// logCommand follows the file on the machine the session runs on. The file
// is made first, because tail -F on one that is not there says so in the
// pane until an agent writes it, which reads as an error.
var logCommand = []string{"/bin/sh", "-c", `f="$HOME/tend-dev.log"; touch "$f"; exec tail -n 40 -F "$f"`}

// isLogTitle is whether a pane is a log pane by its title: the button's,
// or one opened by hand before the button, which the owner's tab had as
// "claude · log" — not seeing it, the button offered to open a second.
func isLogTitle(title string) bool {
	return title == logTitle || strings.HasSuffix(title, "· "+logTitle)
}

// logPanesLocked is every log pane in the tab shown: the one the button
// opened, known by number since its title is only the name it started
// with and a program in it may set another, and any titled as one.
func (t *tui) logPanesLocked() []uint64 {
	inTab := map[uint64]bool{}
	for _, r := range t.rects {
		inTab[r.Pane] = true
	}
	var panes []uint64
	opened := t.logPanes[t.tab]
	if inTab[opened] {
		panes = append(panes, opened)
	}
	for _, p := range t.snap.Panes {
		if inTab[p.ID] && p.ID != opened && isLogTitle(p.Title) {
			panes = append(panes, p.ID)
		}
	}
	return panes
}

// toggleLog closes the tab's log pane, or opens one under the pane in
// focus — under the tallest other pane when the focus is the files panel,
// whose narrow column is no place for a log — and leaves the focus where
// it was, so a click on the button never takes the keyboard from the agent.
func (t *tui) toggleLog() error {
	t.mu.Lock()
	// One at a time, so a click that comes while the last one's pane is not
	// in the snapshot yet does not open a second.
	if t.logToggling {
		t.mu.Unlock()
		return nil
	}
	t.logToggling = true
	t.mu.Unlock()
	defer func() {
		t.mu.Lock()
		t.logToggling = false
		t.mu.Unlock()
	}()

	t.mu.Lock()
	if panes := t.logPanesLocked(); len(panes) > 0 {
		t.mu.Unlock()
		// Every one in the tab: the owner's had the one opened by hand and
		// the button's, and closing one left "▾ log" on the bar with a log
		// still showing.
		for _, pane := range panes {
			if err := t.client.ClosePane(pane); err != nil {
				return err
			}
		}
		return t.refresh()
	}
	focus, target, rows := t.focus, uint64(0), 0
	files := map[uint64]bool{}
	for _, p := range t.snap.Panes {
		if p.Title == filesTitle {
			files[p.ID] = true
		}
	}
	for _, r := range t.rects {
		if r.Pane == focus && !files[r.Pane] {
			target, rows = r.Pane, r.Rows
			break
		}
		if !files[r.Pane] && r.Rows > rows {
			target, rows = r.Pane, r.Rows
		}
	}
	t.mu.Unlock()
	if target == 0 {
		return nil
	}

	created, err := t.client.SplitPane(target, "rows", proto.PaneSpec{Command: logCommand, Title: logTitle})
	if err != nil {
		if t.reportStaleServer(err) {
			return nil
		}
		return err
	}
	t.mu.Lock()
	if t.logPanes == nil {
		t.logPanes = map[uint64]uint64{}
	}
	t.logPanes[t.tab] = created
	t.mu.Unlock()
	// A split halves the pane; the log takes logRows of it, by moving its
	// top edge down as a drag on the divider would.
	if half := rows / 2; half > logRows {
		area := t.layoutArea()
		_ = t.client.AdjustSplit(created, "up", half-logRows, area.Cols, area.Rows)
	}
	if focus != 0 {
		t.focusPane(focus)
	}
	// The refresh is inside the guard, so the next click sees the pane.
	return t.refresh()
}
