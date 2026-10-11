//go:build unix

package server

import (
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/auth-com-br/tend/internal/agent"
	"github.com/auth-com-br/tend/internal/pty"
	"github.com/auth-com-br/tend/internal/session"
)

// echoer is a program that proves who it is: it prints its pid when it starts
// and again with every line it is sent. The same pid after a handoff is the
// same process, which a restored pane — a new shell — could never show.
const echoer = `echo "born:$$"; while read line; do echo "said:$line:$$"; done`

var bornPid = regexp.MustCompile(`born:(\d+)`)

func bornAs(t *testing.T, s *Server, pane session.PaneID) string {
	t.Helper()
	var pid string
	waitFor(t, "the program to say its pid", func() bool {
		text, _ := s.ScreenText(pane)
		if m := bornPid.FindStringSubmatch(text); m != nil {
			pid = m[1]
			return true
		}
		return false
	})
	return pid
}

func handoffConfig() Config {
	return Config{
		DetectInterval: 10 * time.Millisecond,
		AdoptInterval:  20 * time.Millisecond,
		DefaultSize:    pty.Size{Cols: 80, Rows: 24},
	}
}

// TestHandoffKeepsTheProgramRunning is the reason the feature exists. If it
// regresses, replacing the server goes back to costing the user every agent
// that was mid-task.
func TestHandoffKeepsTheProgramRunning(t *testing.T) {
	old := newServer(t)
	_, pane := openTab(t, old, echoer)
	pid := bornAs(t, old, pane)

	h, err := old.BeginHandoff()
	if err != nil {
		t.Fatalf("BeginHandoff: %v", err)
	}
	if len(h.Files) != 1 || len(h.Manifest.Panes) != 1 {
		t.Fatalf("handed over %d terminals for %d panes, want 1 and 1", len(h.Files), len(h.Manifest.Panes))
	}
	files := h.Files
	h.Files = nil // the replacement's now

	acked := false
	next, err := NewFromHandoff(handoffConfig(), h.Manifest, files, func() error {
		acked = true
		return nil
	})
	if err != nil {
		t.Fatalf("NewFromHandoff: %v", err)
	}
	t.Cleanup(func() { _ = next.Close() })
	if !acked {
		t.Fatal("the replacement never said it was ready")
	}
	if err := old.CommitHandoff(h); err != nil {
		t.Fatalf("CommitHandoff: %v", err)
	}

	// What was on the screen came across.
	if text, _ := next.ScreenText(pane); !strings.Contains(text, "born:"+pid) {
		t.Errorf("the replacement's screen is %q, want what the old one showed", text)
	}

	// And the process behind it is the one that was there before.
	if err := next.Write(pane, []byte("hello\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	waitFor(t, "the same process to answer", func() bool {
		text, _ := next.ScreenText(pane)
		return strings.Contains(text, "said:hello:"+pid)
	})
}

// TestHandoffKeepsTheAgentsConversation: the conversation a hook named for a
// pane goes across with the pane. If it regresses, the new server writes a
// state file without it, and the restart after a handoff starts the agent
// fresh instead of resuming it — the loss shows up days later, on a reboot.
func TestHandoffKeepsTheAgentsConversation(t *testing.T) {
	old := newServer(t)
	ws, _ := old.NewWorkspace("main")
	_, pane, err := old.NewTab(ws, "t", PaneSpec{
		Command: []string{"/bin/sh", "-c", "sleep 30"}, Agent: "claude",
	})
	if err != nil {
		t.Fatal(err)
	}
	old.SetWindowTitle("deploying")
	if _, err := old.ReportAgentSession(pane, "tend:claude", "claude",
		agent.SessionRefFromReport("tend:claude", "claude", "conversation-42", ""), nil); err != nil {
		t.Fatal(err)
	}

	h, err := old.BeginHandoff()
	if err != nil {
		t.Fatalf("BeginHandoff: %v", err)
	}
	files := h.Files
	h.Files = nil
	next, err := NewFromHandoff(handoffConfig(), h.Manifest, files, func() error { return nil })
	if err != nil {
		t.Fatalf("NewFromHandoff: %v", err)
	}
	t.Cleanup(func() { _ = next.Close() })
	if err := old.CommitHandoff(h); err != nil {
		t.Fatalf("CommitHandoff: %v", err)
	}

	if got := next.snapshot().WindowTitle; got != "deploying" {
		t.Errorf("window title after the handoff = %q, want the script's", got)
	}
	got, ok, err := next.AgentSession(pane)
	if err != nil || !ok || got.Session.ID != "conversation-42" || got.Agent != "claude" {
		t.Errorf("after the handoff the pane's conversation is %+v, %v, %v; want conversation-42", got, ok, err)
	}
}

// TestAbortedHandoffLosesNothing covers the replacement failing to start. If
// this regresses, a failed upgrade eats whatever the panes printed while it
// was being tried, or leaves them frozen.
func TestAbortedHandoffLosesNothing(t *testing.T) {
	s := newServer(t)
	_, pane := openTab(t, s, echoer)
	pid := bornAs(t, s, pane)

	h, err := s.BeginHandoff()
	if err != nil {
		t.Fatalf("BeginHandoff: %v", err)
	}
	// Sent while nobody is reading. The answer sits in the kernel.
	if err := s.Write(pane, []byte("during\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, _, err := s.NewTab(1, "late", shell("sleep 5")); !errors.Is(err, ErrHandingOff) {
		t.Errorf("opening a pane during a handoff: err = %v, want ErrHandingOff", err)
	}
	s.AbortHandoff(h)

	waitFor(t, "what was said during the attempt", func() bool {
		text, _ := s.ScreenText(pane)
		return strings.Contains(text, "said:during:"+pid)
	})

	// A second attempt must work: the first left nothing behind.
	h, err = s.BeginHandoff()
	if err != nil {
		t.Fatalf("second BeginHandoff: %v", err)
	}
	s.AbortHandoff(h)
	if err := s.Write(pane, []byte("after\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	waitFor(t, "the pane to carry on", func() bool {
		text, _ := s.ScreenText(pane)
		return strings.Contains(text, "said:after:"+pid)
	})
}

// TestHandoffWithoutAReplacementIsRefused: a server that was never told how to
// start another must say so, not stop its readers and sit there.
func TestHandoffWithoutAReplacementIsRefused(t *testing.T) {
	s := newServer(t)
	if _, err := s.Replace(""); !errors.Is(err, ErrHandoffUnavailable) {
		t.Errorf("err = %v, want ErrHandoffUnavailable", err)
	}
}

// TestHandoffKeepsASleepingAgentAsleep: a dormant pane is a shell like any
// other and crosses as one; what wakes its agent crosses with it. If it
// regresses, `tend handoff` turns every sleeping agent into a plain shell
// that focusing no longer wakes.
func TestHandoffKeepsASleepingAgentAsleep(t *testing.T) {
	old := newServer(t)
	ws, _ := old.NewWorkspace("main")
	_, pane, err := old.NewTab(ws, "t", PaneSpec{Command: []string{"/bin/sh"}})
	if err != nil {
		t.Fatal(err)
	}
	old.mu.Lock()
	rt := old.runtimes[pane]
	old.mu.Unlock()
	conversation := agent.PersistedSession{Source: "tend:claude", Agent: "claude", Session: agent.SessionRef{ID: "c-5"}}
	rt.mu.Lock()
	rt.dormant = &dormantAgent{Agent: "claude", Argv: []string{"claude", "--resume", "c-5"}, Session: &conversation}
	rt.mu.Unlock()

	h, err := old.BeginHandoff()
	if err != nil {
		t.Fatalf("BeginHandoff: %v", err)
	}
	files := h.Files
	h.Files = nil
	next, err := NewFromHandoff(handoffConfig(), h.Manifest, files, func() error { return nil })
	if err != nil {
		t.Fatalf("NewFromHandoff: %v", err)
	}
	t.Cleanup(func() { _ = next.Close() })
	if err := old.CommitHandoff(h); err != nil {
		t.Fatalf("CommitHandoff: %v", err)
	}
	for _, st := range next.Statuses() {
		if st.ID == pane && st.Dormant != "claude" {
			t.Fatalf("after the handoff the pane is %+v, want claude asleep", st)
		}
	}
	next.mu.Lock()
	d := next.runtimes[pane].dormant
	next.mu.Unlock()
	if d == nil || strings.Join(d.Argv, " ") != "claude --resume c-5" || d.Session == nil || d.Session.Session.ID != "c-5" {
		t.Fatalf("what wakes it did not cross: %+v", d)
	}
}
