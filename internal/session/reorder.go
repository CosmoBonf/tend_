package session

import (
	"errors"
	"fmt"
)

// Moving things around without making them again: two panes trade places,
// a tab or a space moves along its row. None of this touches a process — a
// pane that moves keeps running, its program never learns it went anywhere —
// which is what makes it different from closing one and opening another.
//
// The rules are herdr's (`layout.rs` swap_panes, `workspace.rs` move_tab,
// `app/actions.rs` move_workspace): a swap exchanges the two leaves and leaves
// the split shape and its ratios alone, and a move is "remove, then insert at
// this index", with the focused one staying focused wherever it ends up.

// ErrNoMove means the move asked for is not one: the same pane twice, panes in
// two different tabs, an index off the end.
var ErrNoMove = errors.New("session: nothing to move")

// SwapPanes exchanges two panes' places in their tab.
//
// Only within one tab: a pane's size and neighbours come from the tree it is
// in, and trading between two trees would leave each pane sized for a place
// in a layout it is no longer part of.
func (s *Session) SwapPanes(a, b PaneID) error {
	if a == b {
		return ErrNoMove
	}
	ta, ok := s.index[a]
	if !ok {
		return ErrNoSuchPane
	}
	tb, ok := s.index[b]
	if !ok {
		return ErrNoSuchPane
	}
	if ta != tb {
		return ErrNoMove
	}
	swapLeaves(ta.root, a, b)
	return nil
}

// swapLeaves renames every leaf that is a to b and every one that is b to a.
func swapLeaves(n *node, a, b PaneID) {
	if n == nil {
		return
	}
	if len(n.kids) == 0 {
		switch n.pane {
		case a:
			n.pane = b
		case b:
			n.pane = a
		}
		return
	}
	for _, kid := range n.kids {
		swapLeaves(kid, a, b)
	}
}

// SwapPaneToward exchanges a pane with its neighbour on one side, as laid out
// in area. It reports the neighbour, and ErrNoMove when there is none.
func (s *Session) SwapPaneToward(id PaneID, side Side, area Rect) (PaneID, error) {
	t, ok := s.index[id]
	if !ok {
		return 0, ErrNoSuchPane
	}
	other, found := Neighbor(t.Layout(area), id, side)
	if !found {
		return 0, ErrNoMove
	}
	if err := s.SwapPanes(id, other); err != nil {
		return 0, err
	}
	return other, nil
}

// MoveTab puts a tab at index in its space's row, counting as the row is
// after the tab has been taken out of it.
func (s *Session) MoveTab(id TabID, index int) error {
	for _, w := range s.workspaces {
		for from, t := range w.tabs {
			if t.ID != id {
				continue
			}
			if index < 0 || index >= len(w.tabs) {
				return ErrNoMove
			}
			if index == from {
				return ErrNoMove
			}
			var active TabID
			if w.active >= 0 && w.active < len(w.tabs) {
				active = w.tabs[w.active].ID
			}
			w.tabs = moveItem(w.tabs, from, index)
			for i, tab := range w.tabs {
				if tab.ID == active {
					w.active = i
				}
			}
			return nil
		}
	}
	return ErrNoSuchTab
}

// MoveWorkspace puts a space at index in the session's list.
func (s *Session) MoveWorkspace(id WorkspaceID, index int) error {
	for from, w := range s.workspaces {
		if w.ID != id {
			continue
		}
		if index < 0 || index >= len(s.workspaces) || index == from {
			return ErrNoMove
		}
		var active WorkspaceID
		if s.active >= 0 && s.active < len(s.workspaces) {
			active = s.workspaces[s.active].ID
		}
		s.workspaces = moveItem(s.workspaces, from, index)
		for i, ws := range s.workspaces {
			if ws.ID == active {
				s.active = i
			}
		}
		return nil
	}
	return ErrNoSuchWorkspace
}

// TabIndex is where a tab is in its space's row.
func (s *Session) TabIndex(id TabID) (int, bool) {
	for _, w := range s.workspaces {
		for i, t := range w.tabs {
			if t.ID == id {
				return i, true
			}
		}
	}
	return 0, false
}

// WorkspaceIndex is where a space is in the session's list.
func (s *Session) WorkspaceIndex(id WorkspaceID) (int, bool) {
	for i, w := range s.workspaces {
		if w.ID == id {
			return i, true
		}
	}
	return 0, false
}

// moveItem takes the item at from out and puts it back at to.
func moveItem[T any](items []T, from, to int) []T {
	item := items[from]
	rest := append(items[:from:from], items[from+1:]...)
	out := make([]T, 0, len(items))
	out = append(out, rest[:to]...)
	out = append(out, item)
	return append(out, rest[to:]...)
}

// MovePane takes a pane out of its tab and puts it beside another, which may
// be in a different tab or a different space.
//
// The pane itself is not remade: its record, and so the process behind it,
// goes on being the same pane with the same id. That is the whole point —
// moving a running agent to another tab must not restart it.
func (s *Session) MovePane(id, beside PaneID, dir Direction) error {
	from, ok := s.index[id]
	if !ok {
		return fmt.Errorf("%w: %d", ErrNoSuchPane, id)
	}
	to, ok := s.index[beside]
	if !ok {
		return fmt.Errorf("%w: %d", ErrNoSuchPane, beside)
	}
	if id == beside {
		return ErrNoMove
	}
	if from == to && len(from.panes) == 1 {
		return ErrNoMove // the only pane of a tab, moved within it
	}

	pane := from.panes[id]
	// Out first, so a move within one tab cannot put a pane beside itself.
	root, closed := from.root.closePane(id)
	if !closed {
		return fmt.Errorf("%w: %d", ErrNoSuchPane, id)
	}
	from.root = root
	delete(from.panes, id)
	delete(s.index, id)
	if from.active == id {
		if remaining := from.Panes(); len(remaining) > 0 {
			from.active = remaining[0]
		}
	}

	next, split := to.root.split(beside, id, dir, false)
	if !split {
		// Put it back rather than losing a running pane to a layout that
		// disagreed with the index.
		from.root = restoreInto(from.root, id)
		from.panes[id] = pane
		s.index[id] = from
		return fmt.Errorf("%w: %d", ErrNoSuchPane, beside)
	}
	to.root = next
	to.panes[id] = pane
	to.active = id
	s.index[id] = to

	if from != to && from.root == nil {
		// The tab it left is empty now, and so may be its space: a pane
		// brought beside another from a space it was alone in — an ssh
		// connection's — took the space with it. Left there, the space
		// listed nothing and opened onto nothing.
		ws, _ := s.workspaceOfTab(from.ID)
		_, _ = s.CloseTab(from.ID)
		if ws != nil && len(ws.tabs) == 0 {
			_, _ = s.CloseWorkspace(ws.ID)
		}
	}
	return nil
}

// BreakPane takes a pane out of its tab into a tab of its own, named name,
// beside the one it left in the same space, and focuses it: the way back
// from bringing a pane beside another. As with MovePane, the pane is not
// remade, so what runs in it goes on.
func (s *Session) BreakPane(id PaneID, name string) (TabID, error) {
	from, ok := s.index[id]
	if !ok {
		return 0, fmt.Errorf("%w: %d", ErrNoSuchPane, id)
	}
	if len(from.panes) == 1 {
		return 0, ErrNoMove // a tab of its own already
	}
	ws, at := s.workspaceOfTab(from.ID)
	if ws == nil {
		return 0, fmt.Errorf("%w: %d", ErrNoSuchPane, id)
	}
	root, closed := from.root.closePane(id)
	if !closed {
		return 0, fmt.Errorf("%w: %d", ErrNoSuchPane, id)
	}
	pane := from.panes[id]
	from.root = root
	delete(from.panes, id)
	if from.active == id {
		if remaining := from.Panes(); len(remaining) > 0 {
			from.active = remaining[0]
		}
	}

	s.nextTab++
	t := &Tab{ID: TabID(s.nextTab), Name: name, panes: map[PaneID]*Pane{id: pane}}
	t.root = leaf(id)
	t.active = id
	s.index[id] = t
	ws.tabs = append(ws.tabs[:at+1], append([]*Tab{t}, ws.tabs[at+1:]...)...)
	ws.active = at + 1
	for i, w := range s.workspaces {
		if w == ws {
			s.active = i
		}
	}
	return t.ID, nil
}

// workspaceOfTab is the space a tab is in, and the tab's place in it.
func (s *Session) workspaceOfTab(id TabID) (*Workspace, int) {
	for _, w := range s.workspaces {
		for i, t := range w.tabs {
			if t.ID == id {
				return w, i
			}
		}
	}
	return nil, 0
}

// restoreInto puts a pane back into a tree that has just lost it, which is
// only ever the undo of a move that could not be completed.
func restoreInto(root *node, id PaneID) *node {
	if root == nil {
		return leaf(id)
	}
	first := root.panes(nil)
	if len(first) == 0 {
		return leaf(id)
	}
	next, _ := root.split(first[0], id, Columns, false)
	return next
}

// MoveWorkspaceBlock moves several spaces together, in the order given, to
// just before another — or to the end when before is zero: herdr's
// workspace.move_block, which is how a group is moved as one. The space in
// view stays in view wherever it lands.
func (s *Session) MoveWorkspaceBlock(ids []WorkspaceID, before WorkspaceID) error {
	if len(ids) == 0 {
		return ErrNoMove
	}
	moving := make(map[WorkspaceID]bool, len(ids))
	block := make([]*Workspace, 0, len(ids))
	for _, id := range ids {
		if moving[id] {
			return fmt.Errorf("%w: space %d named twice", ErrNoMove, id)
		}
		w, ok := s.Workspace(id)
		if !ok {
			return fmt.Errorf("%w: %d", ErrNoSuchWorkspace, id)
		}
		moving[id] = true
		block = append(block, w)
	}
	if moving[before] {
		return fmt.Errorf("%w: a space cannot be moved before itself", ErrNoMove)
	}
	var active WorkspaceID
	if s.active >= 0 && s.active < len(s.workspaces) {
		active = s.workspaces[s.active].ID
	}
	rest := make([]*Workspace, 0, len(s.workspaces))
	for _, w := range s.workspaces {
		if !moving[w.ID] {
			rest = append(rest, w)
		}
	}
	at := len(rest)
	if before != 0 {
		at = -1
		for i, w := range rest {
			if w.ID == before {
				at = i
			}
		}
		if at < 0 {
			return fmt.Errorf("%w: %d", ErrNoSuchWorkspace, before)
		}
	}
	out := make([]*Workspace, 0, len(s.workspaces))
	out = append(out, rest[:at]...)
	out = append(out, block...)
	out = append(out, rest[at:]...)
	s.workspaces = out
	for i, w := range s.workspaces {
		if w.ID == active {
			s.active = i
		}
	}
	return nil
}
