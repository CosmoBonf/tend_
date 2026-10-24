package main

import (
	"unicode/utf8"

	"github.com/auth-com-br/tend/internal/proto"
	"github.com/auth-com-br/tend/internal/ui"
)

// "side by side...", tend's own and the owner's: the "new" of either list,
// and a pane's menu, open a popup listing what is open elsewhere — every
// pane of the other tabs and spaces, and the saved ssh connections — and the
// one picked is brought beside the pane in view, or below it. It is moved,
// not copied (a terminal is in one place), and not restarted: an agent goes
// on with its task, ssh stays logged in. A connection that is not open is
// opened there. "send to new tab" on a pane's menu is the way back.
//
// The popup is this client's, as every popup is; the move is the server's
// (pane.move), so every client sees the pane where it went.

// sideBySideState is the popup while it is up: what it shows, and the pane
// what is picked goes beside.
type sideBySideState struct {
	popup  ui.SideBySide
	target uint64
}

func (t *tui) sideBySideUp() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.sideBySide != nil
}

func (t *tui) sideBySideFrameLocked() *ui.SideBySide {
	if t.sideBySide == nil {
		return nil
	}
	s := t.sideBySide.popup
	return &s
}

// openSideBySide puts the popup up, to bring something beside target, on
// the list the sidebar is showing.
func (t *tui) openSideBySide(target uint64) {
	if !t.serverKnows(proto.MethodPaneMove) {
		t.setMessage("side by side needs the new server: run tend handoff", true)
		return
	}
	t.mu.Lock()
	if target == 0 {
		target = t.focus
	}
	if _, _, ok := t.paneWhereLocked(target); !ok {
		t.mu.Unlock()
		t.setMessage("side by side: no pane in view to put things beside", true)
		return
	}
	list := ui.SideBySideSpaces
	if t.ssh.list {
		list = ui.SideBySideSSH
	}
	t.sideBySide = &sideBySideState{target: target, popup: ui.SideBySide{
		List:   list,
		Spaces: t.sideBySideSpacesLocked(target),
		SSH:    t.sideBySideSSHLocked(target),
	}}
	t.dirty = true
	t.mu.Unlock()
	t.wakeUp()
}

func (t *tui) closeSideBySide() {
	t.mu.Lock()
	t.sideBySide = nil
	t.dirty = true
	t.mu.Unlock()
	t.wakeUp()
}

// sideBySideSpacesLocked is every pane that is not in target's tab, by
// space and tab. The ssh connections' spaces are left to the ssh list, as
// the sidebar leaves them.
func (t *tui) sideBySideSpacesLocked(target uint64) []ui.SideBySideEntry {
	_, here, _ := t.paneWhereLocked(target)
	panes := make(map[uint64]proto.PaneInfo, len(t.snap.Panes))
	for _, p := range t.snap.Panes {
		panes[p.ID] = p
	}
	var out []ui.SideBySideEntry
	for _, w := range t.snap.Workspaces {
		if w.Group == sshGroup {
			continue
		}
		for i, tab := range w.Tabs {
			if tab.ID == here {
				continue
			}
			tabLabel := tab.Name
			if tabLabel == "" {
				tabLabel = "tab " + itoaInt(i+1)
			}
			for j, id := range tab.Panes {
				p := panes[id]
				status := ""
				if s := navState(p); s != "unknown" {
					status = s
				}
				out = append(out, ui.SideBySideEntry{
					Label:  orDash(w.Name) + " › " + tabLabel + " · " + navigatorPaneLabel(p, j),
					Meta:   p.Dir,
					Status: status,
					Pane:   id,
				})
			}
		}
	}
	return out
}

// sideBySideSSHLocked is every saved connection: an open one is brought as
// it is, one that is not is opened. One already in target's tab is not
// offered: it is beside it already.
func (t *tui) sideBySideSSHLocked(target uint64) []ui.SideBySideEntry {
	t.sshLoadLocked()
	_, here, _ := t.paneWhereLocked(target)
	var out []ui.SideBySideEntry
	for _, c := range t.ssh.store.Connections {
		e := ui.SideBySideEntry{Label: c.Name, Meta: c.Group, Status: "connect", SSH: c.ID}
		if o, open := t.sshOpenLocked(c.ID); open {
			if _, tab, _ := t.paneWhereLocked(o.pane); tab == here {
				continue
			}
			e.Status, e.Pane = "● open", o.pane
		}
		out = append(out, e)
	}
	return out
}

// moveSideBySideLocked moves the cursor among the entries the filter leaves.
func (t *tui) moveSideBySideLocked(delta int) {
	s := &t.sideBySide.popup
	f := s.Filtered()
	if len(f) == 0 {
		return
	}
	_, sel, _ := s.SelectedEntry()
	pos := 0
	for i, idx := range f {
		if idx == sel {
			pos = i
		}
	}
	s.Selected = f[min(max(pos+delta, 0), len(f)-1)]
}

// switchSideBySideLocked shows the other list, its filter and cursor fresh.
func (t *tui) switchSideBySideLocked(list int) {
	s := &t.sideBySide.popup
	if s.List != list {
		s.List, s.Query, s.Selected, s.Error = list, "", 0, ""
	}
}

// sideBySideKeys: typing filters, the arrows move, tab changes list, enter
// brings beside and ctrl+d below, esc closes.
func (t *tui) sideBySideKeys(data []byte) {
	for _, key := range splitKeys(data) {
		t.mu.Lock()
		if t.sideBySide == nil {
			t.mu.Unlock()
			return
		}
		s := &t.sideBySide.popup
		t.dirty = true
		switch {
		case key == "\x1b" || key == "\x03":
			t.sideBySide = nil
			t.mu.Unlock()
		case key == "\r" || key == "\n" || key == "\x04":
			below := s.Below || key == "\x04"
			t.mu.Unlock()
			t.bringSideBySide(below)
		case key == "\t" || key == "\x1b[Z":
			t.switchSideBySideLocked(1 - s.List)
			t.mu.Unlock()
		case key == "\x1b[A":
			t.moveSideBySideLocked(-1)
			t.mu.Unlock()
		case key == "\x1b[B":
			t.moveSideBySideLocked(1)
			t.mu.Unlock()
		case key == "\x7f" || key == "\x08":
			if q := s.Query; q != "" {
				_, size := utf8.DecodeLastRuneInString(q)
				s.Query = q[:len(q)-size]
			}
			if f := s.Filtered(); len(f) > 0 {
				s.Selected = f[0]
			}
			t.mu.Unlock()
		case len(key) == 1 && key[0] >= 0x20 && key[0] != 0x7f || len(key) > 1 && key[0] >= 0x80:
			s.Query += key
			if f := s.Filtered(); len(f) > 0 {
				s.Selected = f[0]
			}
			t.mu.Unlock()
		default:
			t.mu.Unlock()
		}
	}
	t.wakeUp()
}

// sideBySideMouse answers the mouse while the popup is up: a list button
// shows that list, a placement button chooses beside or below, a click on
// an entry brings it, a click outside or on × closes.
func (t *tui) sideBySideMouse(ev ui.MouseEvent) (bool, error) {
	t.mu.Lock()
	if t.sideBySide == nil {
		t.mu.Unlock()
		return false, nil
	}
	s := &t.sideBySide.popup
	hit := ui.SideBySideAt(*s, t.cols, t.rows, ev.X, ev.Y)
	t.dirty = true
	switch ev.Kind {
	case ui.MouseWheelUp:
		t.moveSideBySideLocked(-1)
	case ui.MouseWheelDown:
		t.moveSideBySideLocked(1)
	case ui.MousePress:
		switch {
		case !hit.Inside || hit.Close:
			t.sideBySide = nil
		case hit.List >= 0:
			t.switchSideBySideLocked(hit.List)
		case hit.Place >= 0:
			s.Below = hit.Place == 1
		case hit.Entry >= 0:
			s.Selected = hit.Entry
			below := s.Below
			t.mu.Unlock()
			t.bringSideBySide(below)
			return true, nil
		}
	}
	t.mu.Unlock()
	t.wakeUp()
	return true, nil
}

// bringSideBySide brings the entry the cursor is on beside the target pane,
// or below it, and puts the focus on it there.
func (t *tui) bringSideBySide(below bool) {
	t.mu.Lock()
	st := t.sideBySide
	if st == nil {
		t.mu.Unlock()
		return
	}
	e, _, ok := st.popup.SelectedEntry()
	target := st.target
	t.mu.Unlock()
	if !ok {
		return
	}

	pane, err := e.Pane, error(nil)
	if pane != 0 {
		err = t.client.MovePane(pane, target, below)
	} else {
		pane, err = t.openSSHBeside(e.SSH, target, below)
	}
	if err != nil || pane == 0 {
		t.mu.Lock()
		if t.sideBySide != nil && err != nil {
			t.sideBySide.popup.Error = refusalText(err)
		}
		t.dirty = true
		t.mu.Unlock()
		t.wakeUp()
		return
	}

	t.mu.Lock()
	t.sideBySide = nil
	ws, tab, _ := t.paneWhereLocked(target)
	t.rememberFocusLocked()
	t.workspace, t.tab, t.focus, t.zoom = ws, tab, pane, false
	t.mu.Unlock()
	if err := t.refresh(); err != nil {
		t.setMessage(err.Error(), true)
	}
}

// openSSHBeside opens a saved connection in a new pane beside target, as
// connectSSH opens one in a space of its own, and says which pane it is.
func (t *tui) openSSHBeside(id string, target uint64, below bool) (uint64, error) {
	t.mu.Lock()
	c, ok := t.ssh.store.Find(id)
	remote := t.host != ""
	t.mu.Unlock()
	if !ok {
		return 0, nil
	}
	if c.Password != "" && remote {
		t.setMessage(c.Name+": a saved password works in a session on this machine only", true)
		return 0, nil
	}
	dir := "columns"
	if below {
		dir = "rows"
	}
	pane, err := t.client.SplitPane(target, dir, proto.PaneSpec{Command: c.PaneCommand(), Title: c.Name})
	if err != nil {
		return 0, err
	}
	// Named after the connection, as connectSSH names its pane, so the ssh
	// list knows it for this connection, here and in a client started later.
	_ = t.client.RenamePane(pane, c.Name)
	t.mu.Lock()
	if t.ssh.open == nil {
		t.ssh.open = make(map[string]sshOpen)
	}
	ws, _, _ := t.paneWhereLocked(target)
	t.ssh.open[id] = sshOpen{workspace: ws, pane: pane}
	t.mu.Unlock()
	return pane, nil
}

// sendToNewTab takes a pane out of a tab it shares into a tab of its own
// beside it, and goes there: the way back from "side by side...".
func (t *tui) sendToNewTab(pane uint64) error {
	if !t.serverKnows(proto.MethodPaneBreak) {
		t.setMessage("send to new tab needs the new server: run tend handoff", true)
		return nil
	}
	t.mu.Lock()
	name := ""
	for _, p := range t.snap.Panes {
		if p.ID == pane && p.Named {
			name = p.Title
		}
	}
	ws, _, _ := t.paneWhereLocked(pane)
	if name == "" {
		// Named as prefix+c names a tab: one with no name shows its number,
		// which reads as nothing beside "tab 1".
		for _, w := range t.snap.Workspaces {
			if w.ID == ws {
				name = t.nextName("tab", len(w.Tabs))
			}
		}
	}
	t.mu.Unlock()
	tab, err := t.client.BreakPane(pane, name)
	if err != nil {
		t.setMessage("send to new tab: "+refusalText(err), true)
		return nil
	}
	t.mu.Lock()
	t.rememberFocusLocked()
	t.workspace, t.tab, t.focus, t.zoom = ws, tab, pane, false
	t.mu.Unlock()
	return t.refresh()
}
