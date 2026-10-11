package main

import (
	"fmt"
	"strings"

	"github.com/auth-com-br/tend/internal/ui"
)

// Sleeping agents, from the client's side: the server ends an agent and
// keeps a shell and its conversation in the pane (internal/server/
// dormant.go); going to the pane wakes it, as the focus the client already
// reports says so. What is left here is the menu's "sleep", and saying what
// it gave back.

// sleepAgent puts a pane's agent to sleep and says how much memory that
// gave back, or why it could not.
func (t *tui) sleepAgent(pane uint64) {
	res, err := t.client.SleepPane(pane)
	if err != nil {
		if t.reportStaleServer(err) {
			return
		}
		t.setMessage(refusalText(err), true)
		return
	}
	who := res.Agent
	if who == "" {
		who = "the agent"
	}
	msg := who + " is asleep — focus its pane to wake it"
	if res.Freed > 0 {
		msg = fmt.Sprintf("%s is asleep, %s given back — focus its pane to wake it", who, humanBytes(res.Freed))
	}
	t.setMessage(msg, false)
}

// humanBytes is a size as it is read: 412 MB, 1.2 GB.
func humanBytes(n int64) string {
	const mb = 1 << 20
	if n >= 1<<30 {
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	}
	return fmt.Sprintf("%d MB", (n+mb/2)/mb)
}

// sleepChoicesLocked are the agents that are awake, in the order of the
// spaces and tabs, each said as the agent, what it is about and where it
// is — enough to tell twenty Claude Codes apart. The caller holds the lock.
func (t *tui) sleepChoicesLocked() []ui.SleepChoice {
	info := make(map[uint64]int, len(t.snap.Panes))
	for i, p := range t.snap.Panes {
		info[p.ID] = i
	}
	var out []ui.SleepChoice
	for _, w := range t.snap.Workspaces {
		for _, tab := range w.Tabs {
			for _, id := range tab.Panes {
				i, ok := info[id]
				if !ok {
					continue
				}
				p := t.snap.Panes[i]
				if p.Agent == "" || p.Dormant || !p.Running {
					continue
				}
				about := strings.TrimSpace(strings.TrimLeft(p.Title, "✳◐⠂⠐·*● "))
				if about == "" || strings.Contains(about, "@") {
					about = "pane " + fmt.Sprint(p.ID)
				}
				label := p.Agent + " · " + truncateRunes(about, 40) + " · " + orDash(w.Name)
				idle := p.State != "working" && p.State != "blocked"
				if !idle {
					label += "  (" + p.State + ")"
				}
				out = append(out, ui.SleepChoice{Pane: id, Label: label, Idle: idle})
			}
		}
	}
	return out
}

// sleepAgents puts several agents to sleep, one after another, and says
// how many went and what they gave back.
func (t *tui) sleepAgents(panes []uint64) {
	var freed int64
	slept, failed := 0, 0
	for _, pane := range panes {
		t.setMessage(fmt.Sprintf("putting agents to sleep… %d of %d", slept+failed+1, len(panes)), false)
		res, err := t.client.SleepPane(pane)
		if err != nil {
			failed++
			continue
		}
		slept++
		freed += res.Freed
	}
	msg := fmt.Sprintf("%d agents asleep, %s given back", slept, humanBytes(freed))
	if failed > 0 {
		msg += fmt.Sprintf(" — %d left awake (tend does not know their conversation)", failed)
	}
	t.setMessage(msg, false)
}

// truncateRunes cuts text to n characters, marking the cut.
func truncateRunes(text string, n int) string {
	r := []rune(text)
	if len(r) <= n {
		return text
	}
	return string(r[:n-1]) + "…"
}
