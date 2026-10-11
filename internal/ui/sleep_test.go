package ui

import "testing"

// TestASleepingAgentIsOfferedToWakeAndAnAwakeOneToSleep: the item sits
// before "close pane", which stays last; and a sleeping agent reads as
// asleep, never as exited. If it regresses, the menu offers to sleep an
// agent already asleep, or a sleeping agent looks like a dead pane.
func TestASleepingAgentIsOfferedToWakeAndAnAwakeOneToSleep(t *testing.T) {
	awake := WithSleep(PaneMenu(1, 0, 0, true, true, false), false)
	n := len(awake.Items)
	if awake.Items[n-1].Action != MenuClose || awake.Items[n-2].Action != MenuSleep {
		t.Fatalf("items %+v", awake.Items)
	}
	asleep := WithSleep(AgentMenu(1, 2, 3, 0, 0), true)
	found := false
	for _, it := range asleep.Items {
		if it.Action == MenuSleep {
			t.Fatal("a sleeping agent is offered sleep")
		}
		found = found || it.Action == MenuWake
	}
	if !found {
		t.Fatalf("no wake in %+v", asleep.Items)
	}
	if stateMarker("asleep", true) != "asleep " || StatusIcon("asleep", true, false) != "z" {
		t.Fatal("a sleeping agent is not drawn asleep")
	}
}

// TestTheSleepMenuListsTheAwakeAgentsAndEveryIdleOne: one item puts every
// idle agent to sleep; each agent is its own item, a working one marked.
func TestTheSleepMenuListsTheAwakeAgentsAndEveryIdleOne(t *testing.T) {
	m := SleepMenu([]SleepChoice{
		{Pane: 3, Label: "claude · a · main", Idle: true},
		{Pane: 7, Label: "codex · b · api", Idle: true},
		{Pane: 9, Label: "claude · c · web  (working)"},
	}, 0, 0)
	if len(m.Items) != 4 || m.Items[0].Action != MenuSleepIdle || m.Items[0].Label != "every idle agent (2)" {
		t.Fatalf("items %+v", m.Items)
	}
	if m.Items[2].Action != MenuSleepOne || m.Items[2].Arg != "7" {
		t.Fatalf("an agent's item %+v", m.Items[2])
	}
	if empty := SleepMenu(nil, 0, 0); len(empty.Items) != 1 || empty.Items[0].Action != "" {
		t.Fatalf("no agents: %+v — the item must do nothing", empty.Items)
	}
}

// TestALongMenuScrollsToTheCursorAndClicksWhatItShows: a menu longer than
// the screen shows the part the cursor is in, and a click runs the item
// drawn under it, not the one that would be there unscrolled. If it
// regresses, the agents past the bottom of the screen cannot be chosen, or
// a click puts the wrong agent to sleep.
func TestALongMenuScrollsToTheCursorAndClicksWhatItShows(t *testing.T) {
	var choices []SleepChoice
	for i := 1; i <= 30; i++ {
		choices = append(choices, SleepChoice{Pane: uint64(i), Label: "agent " + itoa(uint64(i))})
	}
	m := SleepMenu(choices, 0, 0)
	const cols, rows = 80, 12
	if m.Top(cols, rows) != 0 {
		t.Fatalf("an unmoved menu starts at %d", m.Top(cols, rows))
	}
	m.Selected = 25
	m.Scroll = m.Top(cols, rows)
	r := m.Rect(cols, rows)
	// The cursor's item is the last one shown.
	item, onItem, _ := MenuItemAt(m, cols, rows, r.X+3, r.Y+r.Rows-2)
	if !onItem || item.Label != "agent 26" {
		t.Fatalf("the bottom row is %q", item.Label)
	}
	m.Selected = 29
	if top := m.Top(cols, rows); top != len(m.Items)-(r.Rows-2) {
		t.Fatalf("the end is at %d", top)
	}
}
