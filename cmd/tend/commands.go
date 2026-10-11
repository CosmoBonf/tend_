package main

import (
	"github.com/auth-com-br/tend/internal/config"
	"github.com/auth-com-br/tend/internal/proto"
)

// The user's own commands ([[keys.command]]), from the client's side: the key
// is read here and the command runs on the server, where the panes are.
//
// A command that opens a pane is shown the way herdr shows it
// (`spawn_pane_command`): zoomed, so it has the screen while it runs, and when
// it ends the pane closes and the view goes back to the pane and the zoom
// there were before. A lazygit bound to a key is a visit, not a new layout.

// transientPane is a pane opened for one command, and what to go back to.
type transientPane struct {
	pane         uint64
	previous     uint64
	previousZoom bool
	// then runs once the pane has gone: the permissions manager is put back
	// up over an agent's file closed in the editor, read again.
	then func()
}

// runCustomCommand runs the user's command at index i.
func (t *tui) runCustomCommand(i int) error {
	t.mu.Lock()
	if i < 0 || i >= len(t.config.Keys.Command) {
		t.mu.Unlock()
		return nil
	}
	c := t.config.Keys.Command[i]
	focus, zoom := t.focus, t.zoom
	t.mu.Unlock()

	pane, err := t.client.RunCommand(focus, c.Kind(), c.Command, config.PopupSize(c.Width), config.PopupSize(c.Height))
	if err != nil {
		if t.reportStaleServer(err) {
			return nil
		}
		t.setMessage(err.Error(), true)
		return nil
	}
	if pane == 0 {
		if c.Kind() == config.CommandShell || c.Kind() == config.CommandPluginAction {
			// It runs where nobody sees it; saying so is the only sign it did.
			t.setMessage("ran "+commandLabel(c), false)
		}
		return nil
	}

	if c.Kind() == config.CommandPopup && t.serverHas(proto.FeaturePopup) {
		// A popup floats over the tab and has the keys by itself; nothing
		// to zoom or to come back from.
		return t.refresh()
	}
	t.mu.Lock()
	t.transient = &transientPane{pane: pane, previous: focus, previousZoom: zoom}
	t.focus, t.zoom = pane, true
	t.dirty = true
	t.mu.Unlock()
	return t.refresh()
}

// commandLabel is what a command is called on the status line.
func commandLabel(c config.CommandKey) string {
	if c.Description != "" {
		return c.Description
	}
	return c.Command
}

// returnFromTransientLocked is called when the focused pane has left the
// layout. If it was a command's pane, the view goes back to where it was and
// it reports true; otherwise the caller picks a pane as it always has. The
// caller holds the lock.
func (t *tui) returnFromTransientLocked(inLayout func(uint64) bool) bool {
	tr := t.transient
	if tr == nil || tr.pane != t.focus {
		return false
	}
	t.transient = nil
	if tr.then != nil {
		// Not under the caller's lock: it takes it.
		go tr.then()
	}
	if tr.previous == 0 || !inLayout(tr.previous) {
		return false
	}
	t.focus, t.zoom = tr.previous, tr.previousZoom
	return true
}
