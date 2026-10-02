package main

import (
	"github.com/auth-com-br/tend/internal/proto"
)

// The file explorer is a panel: a pane docked along the left of the tab,
// the tab's full height, running `tend files` on the session's machine.
// prefix+f is one key for all of it — open the panel, go to it, put it
// away — so the key does what the moment needs rather than asking.

// filesTitle is the name the panel's pane goes by, which is how the client
// finds the one it opened.
const filesTitle = "files"

// filesCommand runs the tend the server runs, which a pane is told of in
// TEND_BIN_PATH: the client's own path means nothing on a remote machine,
// and "tend" may not be on the PATH a pane starts with.
var filesCommand = []string{"/bin/sh", "-c", `exec "${TEND_BIN_PATH:-tend}" files`}

// filesIconsEnv carries the client's choice of icons to the panel. Which
// icons can be drawn is a fact about the terminal looking at the panel —
// a Nerd Font is the viewer's — while the panel runs where the session's
// server does, and reads that machine's settings: on a remote session one
// with no settings file at all, so the icons set here never showed there.
const filesIconsEnv = "TEND_FILES_ICONS"

// filesCommandFor is the panel's command with the client's icons, set on
// the command line rather than in the pane's environment, which a spec's
// Env would replace; a tend too old to read it ignores it.
func filesCommandFor(icons string) []string {
	switch icons {
	case "nerd", "emoji", "none":
		return []string{"/bin/sh", "-c", filesIconsEnv + "=" + icons + ` exec "${TEND_BIN_PATH:-tend}" files`}
	}
	return filesCommand
}

// toggleFiles opens the panel, goes to it, or closes it.
func (t *tui) toggleFiles() error {
	t.mu.Lock()
	focus := t.focus
	var panel uint64
	inTab := map[uint64]bool{}
	for _, r := range t.rects {
		inTab[r.Pane] = true
	}
	for _, p := range t.snap.Panes {
		if inTab[p.ID] && p.Title == filesTitle {
			panel = p.ID
		}
	}
	left, right := 0, 0
	for i, r := range t.rects {
		if i == 0 || r.X < left {
			left = r.X
		}
		right = max(right, r.X+r.Cols)
	}
	t.mu.Unlock()

	switch {
	case panel != 0 && panel == focus:
		// Closed by hand: the tab stays without it (ensureFiles).
		t.mu.Lock()
		t.snoozeFilesLocked(t.tab)
		t.mu.Unlock()
		return t.client.ClosePane(panel)
	case panel != 0:
		t.focusPane(panel)
		return nil
	case focus == 0:
		return nil
	}

	t.mu.Lock()
	// Opened by hand: the tab is back to having it on its own.
	delete(t.filesSnoozed, t.tab)
	share, onRight := t.filesShareLocked(right-left), !t.config.FilesOnLeft()
	t.mu.Unlock()
	created, err := t.client.DockPane(focus, share, onRight, proto.PaneSpec{Command: filesCommandFor(t.config.Files.Icons), Title: filesTitle})
	if err != nil {
		if t.reportStaleServer(err) {
			return nil
		}
		return err
	}
	t.mu.Lock()
	t.focus = created
	t.mu.Unlock()
	return t.refresh()
}

// filesShareLocked is the part of a tab width wide the panel opens at.
func (t *tui) filesShareLocked(width int) float64 {
	if width <= 0 {
		return 0.25
	}
	return float64(t.config.FilesWidth()) / float64(width)
}

// The panel in every tab, herdr-sidebar's auto_open (its ensure.rs, run on
// tab.created, workspace.created and pane.focused): each time the client
// shows a tab without the panel, it docks one beside the pane in focus and
// leaves the focus where it was — save in a tab where the panel was closed,
// by prefix+f, by q in it or by closing its pane, which keeps it closed
// until prefix+f opens it there again (herdr-sidebar's snooze). The server
// docks nothing when the tab has one already (pane.dock's unless), so two
// clients showing one tab cannot dock two.

// snoozeFilesLocked marks a tab the panel was closed in.
func (t *tui) snoozeFilesLocked(tab uint64) {
	if t.filesSnoozed == nil {
		t.filesSnoozed = map[uint64]bool{}
	}
	t.filesSnoozed[tab] = true
	delete(t.filesHad, tab)
}

// ensureFiles docks the panel in the tab shown when it should have one.
// Called after each refresh, which is when the tab shown or its panes may
// have changed.
func (t *tui) ensureFiles() {
	t.mu.Lock()
	tab, focus := t.tab, t.focus
	if tab == 0 || focus == 0 {
		t.mu.Unlock()
		return
	}
	inTab := map[uint64]bool{}
	left, right := 0, 0
	for i, r := range t.rects {
		inTab[r.Pane] = true
		if i == 0 || r.X < left {
			left = r.X
		}
		right = max(right, r.X+r.Cols)
	}
	has := false
	for _, p := range t.snap.Panes {
		if inTab[p.ID] && p.Title == filesTitle {
			has = true
		}
	}
	if t.filesHad == nil {
		t.filesHad = map[uint64]bool{}
	}
	switch {
	case has:
		t.filesHad[tab] = true
		t.mu.Unlock()
		return
	case t.filesHad[tab]:
		// It was here and is gone: closed, however it was.
		t.snoozeFilesLocked(tab)
	}
	if !t.config.FilesAutoOpen() || t.filesSnoozed[tab] || t.filesEnsuring[tab] ||
		t.zoom || t.popupRect != nil || !t.serverHas(proto.FeatureDockUnless) {
		t.mu.Unlock()
		return
	}
	if t.filesEnsuring == nil {
		t.filesEnsuring = map[uint64]bool{}
	}
	t.filesEnsuring[tab] = true
	share, onRight := t.filesShareLocked(right-left), !t.config.FilesOnLeft()
	t.mu.Unlock()

	go func() {
		_, _, err := t.client.DockPaneUnless(focus, share, onRight,
			proto.PaneSpec{Command: filesCommandFor(t.config.Files.Icons), Title: filesTitle}, filesTitle)
		t.mu.Lock()
		delete(t.filesEnsuring, tab)
		t.mu.Unlock()
		if err != nil {
			t.setMessage("files panel: "+err.Error(), true)
			return
		}
		if err := t.refresh(); err != nil {
			t.setMessage(err.Error(), true)
		}
	}()
}
