//go:build unix

package server

import (
	"errors"
	"testing"
)

// TestAPopupIsOneAtATimeInNoLayoutAndGoesWithItsProgram: herdr's popup —
// a pane over the tab, in none of its layout, refusing a second, gone when
// its program ends, when it is closed, or when its tab closes; listed in
// the snapshot so a client draws it. If it regresses, a popup takes a
// place in the layout, or outlives what it was over.
func TestAPopupIsOneAtATimeInNoLayoutAndGoesWithItsProgram(t *testing.T) {
	s := newServer(t)
	tab, pane := openTab(t, s, "sleep 30")

	popup, err := s.OpenPopup(pane, shell("sleep 30"), "40", "50%")
	if err != nil {
		t.Fatal(err)
	}
	if n := len(paneIDs(s)); n != 1 {
		t.Errorf("the layout has %d panes; a popup takes none", n)
	}
	p, ok := s.PopupOpen()
	if !ok || p.Pane != popup || p.Tab != tab || p.Width != "40" || p.Height != "50%" {
		t.Errorf("popup = %+v %v", p, ok)
	}
	snap := s.Snapshot()
	if snap.Popup == nil || snap.Popup.Pane != uint64(popup) {
		t.Errorf("the snapshot should carry the popup: %+v", snap.Popup)
	}
	if _, err := s.OpenPopup(pane, shell("true"), "", ""); !errors.Is(err, ErrPopupOpen) {
		t.Errorf("a second popup: %v", err)
	}
	if err := s.ClosePopup(); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.PopupOpen(); ok {
		t.Error("closed, still open")
	}
	if err := s.ClosePopup(); !errors.Is(err, ErrNoPopup) {
		t.Errorf("closing none: %v", err)
	}

	// Its program ending closes it.
	if _, err := s.OpenPopup(pane, shell("exit 0"), "", ""); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the popup to close with its program", func() bool { _, ok := s.PopupOpen(); return !ok })

	// Its tab closing closes it.
	if _, err := s.OpenPopup(pane, shell("sleep 30"), "", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.CloseTab(tab); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.PopupOpen(); ok {
		t.Error("the popup outlived its tab")
	}

	if err := ParsePopupSize("0"); err == nil {
		t.Error("zero cells is no size")
	}
	if err := ParsePopupSize("101%"); err == nil {
		t.Error("over 100% is no size")
	}
}
