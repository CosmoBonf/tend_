package main

import (
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

// logPaneLocked is the log pane in the tab shown, or 0.
func (t *tui) logPaneLocked() uint64 {
	inTab := map[uint64]bool{}
	for _, r := range t.rects {
		inTab[r.Pane] = true
	}
	for _, p := range t.snap.Panes {
		if inTab[p.ID] && p.Title == logTitle {
			return p.ID
		}
	}
	return 0
}

// toggleLog closes the tab's log pane, or opens one under the pane in
// focus — under the tallest other pane when the focus is the files panel,
// whose narrow column is no place for a log — and leaves the focus where
// it was, so a click on the button never takes the keyboard from the agent.
func (t *tui) toggleLog() error {
	t.mu.Lock()
	if pane := t.logPaneLocked(); pane != 0 {
		t.mu.Unlock()
		if err := t.client.ClosePane(pane); err != nil {
			return err
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
	// A split halves the pane; the log takes logRows of it, by moving its
	// top edge down as a drag on the divider would.
	if half := rows / 2; half > logRows {
		area := t.layoutArea()
		_ = t.client.AdjustSplit(created, "up", half-logRows, area.Cols, area.Rows)
	}
	if focus != 0 {
		t.focusPane(focus)
	}
	return t.refresh()
}
