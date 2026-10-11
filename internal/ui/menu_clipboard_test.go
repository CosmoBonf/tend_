package ui

import "testing"

// TestThePaneMenuOffersCopyForASelectionAndPasteAlways: right-clicking text
// selected in a pane offers to copy it, and paste is there either way; both
// come first. If it regresses, the menu a person opens on a selection has no
// way to copy it, or paste is buried under the layout actions.
func TestThePaneMenuOffersCopyForASelectionAndPasteAlways(t *testing.T) {
	with := PaneMenu(1, 0, 0, true, false, true)
	if with.Items[0].Action != MenuCopy || with.Items[1].Action != MenuPaste {
		t.Errorf("with a selection: %v, %v first", with.Items[0].Label, with.Items[1].Label)
	}
	without := PaneMenu(1, 0, 0, true, false, false)
	if without.Items[0].Action != MenuPaste {
		t.Errorf("without one: %v first", without.Items[0].Label)
	}
	for _, it := range without.Items {
		if it.Action == MenuCopy {
			t.Error("copy offered with nothing selected")
		}
	}
}

// TestAGroupMenuOffersToMoveItToAnotherCompany: with companies made, a
// group's menu offers to move it, and the list offers the companies given
// it, titled with the one it leaves. If it regresses, there is no way to
// move a client's group from one company to another.
func TestAGroupMenuOffersToMoveItToAnotherCompany(t *testing.T) {
	has := func(m Menu, action string) bool {
		for _, it := range m.Items {
			if it.Action == action {
				return true
			}
		}
		return false
	}
	if !has(GroupMenu("client", false, "", true, 0, 0), MenuMoveToCompany) {
		t.Error("no move with companies made")
	}
	if has(GroupMenu("client", false, "", false, 0, 0), MenuMoveToCompany) {
		t.Error("a move offered with no company to move to")
	}
	pick := CompanyPickMenu("client", "Acme", []CompanyChoice{{ID: "2", Name: "Auth"}}, 0, 0)
	if pick.Title != "move from Acme to" || pick.Items[0].Label != "Auth" || pick.Items[0].Arg != "2" || pick.Group != "client" {
		t.Errorf("pick menu %+v", pick)
	}
}
