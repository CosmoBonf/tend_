package session

import (
	"errors"
	"strings"
	"testing"
)

func threePanes(t *testing.T) (*Session, *Tab, PaneID, PaneID, PaneID) {
	t.Helper()
	s := New()
	w := s.AddWorkspace("main")
	tab, a, err := s.AddTab(w.ID, "t", PaneSpec{Command: []string{"sh"}})
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.SplitPane(a.ID, Columns, PaneSpec{Command: []string{"sh"}})
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.SplitPane(b.ID, Rows, PaneSpec{Command: []string{"sh"}})
	if err != nil {
		t.Fatal(err)
	}
	return s, tab, a.ID, b.ID, c.ID
}

// TestASwapTradesPlacesAndKeepsTheShape: swapping two panes must not resize
// anything. A swap that redistributed the space would be a second operation
// the user did not ask for.
func TestASwapTradesPlacesAndKeepsTheShape(t *testing.T) {
	s, tab, a, b, _ := threePanes(t)
	area := Rect{W: 120, H: 40}
	before := map[PaneID]Rect{}
	for _, pr := range tab.Layout(area) {
		before[pr.Pane] = pr.Rect
	}

	if err := s.SwapPanes(a, b); err != nil {
		t.Fatal(err)
	}
	after := map[PaneID]Rect{}
	for _, pr := range tab.Layout(area) {
		after[pr.Pane] = pr.Rect
	}
	if after[a] != before[b] || after[b] != before[a] {
		t.Errorf("a is at %v (b was at %v), b at %v (a was at %v)", after[a], before[b], after[b], before[a])
	}
	if err := s.CheckInvariants(); err != nil {
		t.Fatal(err)
	}
	if err := s.SwapPanes(a, a); !errors.Is(err, ErrNoMove) {
		t.Errorf("swapping a pane with itself: %v", err)
	}
}

// TestASwapTowardFindsTheNeighbour covers prefix+shift+h/j/k/l.
func TestASwapTowardFindsTheNeighbour(t *testing.T) {
	s, _, a, b, _ := threePanes(t)
	area := Rect{W: 120, H: 40}
	other, err := s.SwapPaneToward(a, Right, area)
	if err != nil || other != b {
		t.Fatalf("SwapPaneToward right = %v, %v; want %v", other, err, b)
	}
	// a is now on the right, so there is nothing further right of it.
	if _, err := s.SwapPaneToward(a, Right, area); !errors.Is(err, ErrNoMove) {
		t.Errorf("a swap with no neighbour: %v", err)
	}
}

// TestMovingATabKeepsTheFocusedOneFocused: moving a tab is rearranging, not
// switching, and the tab being looked at must stay the one being looked at.
func TestMovingATabKeepsTheFocusedOneFocused(t *testing.T) {
	s := New()
	w := s.AddWorkspace("main")
	var ids []TabID
	for _, name := range []string{"one", "two", "three"} {
		tab, _, err := s.AddTab(w.ID, name, PaneSpec{Command: []string{"sh"}})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, tab.ID)
	}
	if err := s.FocusTab(ids[1]); err != nil {
		t.Fatal(err)
	}
	if err := s.MoveTab(ids[0], 2); err != nil {
		t.Fatal(err)
	}
	var order []string
	for _, tab := range w.Tabs() {
		order = append(order, tab.Name)
	}
	if got := order[0] + "," + order[1] + "," + order[2]; got != "two,three,one" {
		t.Errorf("order = %s", got)
	}
	if w.ActiveTab().Name != "two" {
		t.Errorf("focused tab is %q, want the one that was focused", w.ActiveTab().Name)
	}
	if err := s.MoveTab(ids[0], 9); !errors.Is(err, ErrNoMove) {
		t.Errorf("moving past the end: %v", err)
	}
}

func TestMovingASpaceKeepsTheActiveOneActive(t *testing.T) {
	s := New()
	a := s.AddWorkspace("a")
	b := s.AddWorkspace("b")
	c := s.AddWorkspace("c")
	if err := s.FocusWorkspace(c.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.MoveWorkspace(a.ID, 2); err != nil {
		t.Fatal(err)
	}
	got := s.Workspaces()
	if got[0] != b || got[1] != c || got[2] != a {
		t.Errorf("order = %s %s %s", got[0].Name, got[1].Name, got[2].Name)
	}
	if s.ActiveWorkspace() != c {
		t.Errorf("active = %s, want c", s.ActiveWorkspace().Name)
	}
}

// TestMovingAPaneToAnotherTabKeepsIt: a running agent moved to another tab
// must be the same pane afterwards. Remaking it would mean restarting what is
// in it, which is the one thing moving is for avoiding.
func TestMovingAPaneToAnotherTabKeepsIt(t *testing.T) {
	s := New()
	w := s.AddWorkspace("main")
	first, a, err := s.AddTab(w.ID, "one", PaneSpec{Command: []string{"agent"}})
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.SplitPane(a.ID, Columns, PaneSpec{Command: []string{"sh"}})
	if err != nil {
		t.Fatal(err)
	}
	second, c, err := s.AddTab(w.ID, "two", PaneSpec{Command: []string{"sh"}})
	if err != nil {
		t.Fatal(err)
	}

	if err := s.MovePane(a.ID, c.ID, Rows); err != nil {
		t.Fatalf("MovePane: %v", err)
	}
	if got := len(first.Panes()); got != 1 || first.Panes()[0] != b.ID {
		t.Errorf("the tab it left holds %v", first.Panes())
	}
	moved, ok := second.Pane(a.ID)
	if !ok {
		t.Fatal("the pane is not in the tab it moved to")
	}
	if moved.Command[0] != "agent" {
		t.Errorf("the pane was remade: %v", moved.Command)
	}
	if err := s.CheckInvariants(); err != nil {
		t.Fatal(err)
	}

	// The last pane of a tab moving away closes the tab it left.
	if err := s.MovePane(b.ID, a.ID, Columns); err != nil {
		t.Fatalf("second MovePane: %v", err)
	}
	if _, ok := s.Tab(first.ID); ok {
		t.Error("the emptied tab is still there")
	}
	if err := s.CheckInvariants(); err != nil {
		t.Fatal(err)
	}
}

// TestAPaneBroughtFromASpaceItWasAloneInClosesThatSpace: an ssh
// connection's space holds one pane; bringing that pane beside another
// leaves the space with nothing in it, and it goes. If it regresses, the
// space stays, listing no tab, and opening onto nothing.
func TestAPaneBroughtFromASpaceItWasAloneInClosesThatSpace(t *testing.T) {
	s := New()
	work := s.AddWorkspace("work")
	_, here, err := s.AddTab(work.ID, "one", PaneSpec{Command: []string{"sh"}})
	if err != nil {
		t.Fatal(err)
	}
	ssh := s.AddWorkspace("vps")
	_, conn, err := s.AddTab(ssh.ID, "vps", PaneSpec{Command: []string{"ssh"}})
	if err != nil {
		t.Fatal(err)
	}

	if err := s.MovePane(conn.ID, here.ID, Columns); err != nil {
		t.Fatalf("MovePane: %v", err)
	}
	if _, ok := s.Workspace(ssh.ID); ok {
		t.Error("the space the pane was alone in is still there")
	}
	if _, ok := s.Workspace(work.ID); !ok {
		t.Error("the space it was brought to went")
	}
	if err := s.CheckInvariants(); err != nil {
		t.Fatal(err)
	}
}

// TestAPaneIsSentToATabOfItsOwn is "send to new tab", the way back from
// "side by side...": the pane leaves its tab for a new one right after it,
// in the same space, focused, and is the same pane. If it regresses, a pane
// brought beside another can only be closed, and what runs in it with it.
func TestAPaneIsSentToATabOfItsOwn(t *testing.T) {
	s := New()
	w := s.AddWorkspace("work")
	first, a, err := s.AddTab(w.ID, "one", PaneSpec{Command: []string{"sh"}})
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.SplitPane(a.ID, Columns, PaneSpec{Command: []string{"ssh"}})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = s.AddTab(w.ID, "two", PaneSpec{Command: []string{"sh"}})
	if err != nil {
		t.Fatal(err)
	}

	tab, err := s.BreakPane(b.ID, "vps")
	if err != nil {
		t.Fatalf("BreakPane: %v", err)
	}
	var order []string
	for _, tb := range w.Tabs() {
		order = append(order, tb.Name)
	}
	if got := strings.Join(order, " "); got != "one vps two" {
		t.Errorf("tabs are %s, want the new one after the one it left", got)
	}
	if got := first.Panes(); len(got) != 1 || got[0] != a.ID {
		t.Errorf("the tab it left holds %v", got)
	}
	nt, ok := s.Tab(tab)
	if !ok {
		t.Fatal("no new tab")
	}
	if p, ok := nt.Pane(b.ID); !ok || p.Command[0] != "ssh" {
		t.Errorf("the pane was not carried over as it was: %+v %v", p, ok)
	}
	if s.ActiveWorkspace().ActiveTab().ID != tab {
		t.Error("the new tab is not the one in view")
	}
	if _, err := s.BreakPane(a.ID, "x"); !errors.Is(err, ErrNoMove) {
		t.Errorf("a pane alone in its tab was broken out: %v", err)
	}
	if err := s.CheckInvariants(); err != nil {
		t.Fatal(err)
	}
}

// TestSpacesMoveAsABlock is herdr's workspace.move_block: the spaces named
// keep their order and land together, before another or at the end, and the
// one in view stays in view. If it regresses, moving a group scatters it.
func TestSpacesMoveAsABlock(t *testing.T) {
	s := New()
	var ids []WorkspaceID
	for _, name := range []string{"a", "b", "c", "d", "e"} {
		ids = append(ids, s.AddWorkspace(name).ID)
	}
	names := func() string {
		var out string
		for _, w := range s.Workspaces() {
			out += w.Name
		}
		return out
	}
	if err := s.MoveWorkspaceBlock([]WorkspaceID{ids[3], ids[1]}, ids[0]); err != nil {
		t.Fatal(err)
	}
	if got := names(); got != "dbace" {
		t.Errorf("order = %s, want dbace", got)
	}
	if err := s.MoveWorkspaceBlock([]WorkspaceID{ids[3]}, 0); err != nil {
		t.Fatal(err)
	}
	if got := names(); got != "baced" {
		t.Errorf("order = %s, want bace then d at the end", got)
	}
	if err := s.MoveWorkspaceBlock([]WorkspaceID{ids[0], ids[0]}, 0); err == nil {
		t.Error("a space named twice should be refused")
	}
	if err := s.MoveWorkspaceBlock([]WorkspaceID{ids[0]}, ids[0]); err == nil {
		t.Error("a space cannot go before itself")
	}
}
