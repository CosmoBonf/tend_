//go:build unix

package server

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/auth-com-br/tend/internal/agent"
	"github.com/auth-com-br/tend/internal/detect"
	"github.com/auth-com-br/tend/internal/pty"
	"github.com/auth-com-br/tend/internal/session"
)

// fakeClaude puts a stand-in for claude on PATH that says how it was
// started and stays, and returns its path.
func fakeClaude(t *testing.T) string {
	t.Helper()
	bin := t.TempDir()
	path := filepath.Join(bin, "claude")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nprintf 'resumed: %s\\n' \"$*\"\nsleep 300\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return path
}

func sleepyServer(t *testing.T, stateFile string) *Server {
	t.Helper()
	s, err := New(Config{
		StateFile:      stateFile,
		DetectInterval: 10 * time.Millisecond,
		AdoptInterval:  20 * time.Millisecond,
		DefaultSize:    pty.Size{Cols: 80, Rows: 24},
		ShutdownGrace:  time.Second,
		Shell:          []string{"/bin/sh"},
		SleepRestored:  true,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func statusOf(t *testing.T, s *Server, id session.PaneID) PaneStatus {
	t.Helper()
	for _, st := range s.Statuses() {
		if st.ID == id {
			return st
		}
	}
	t.Fatalf("no pane %d", id)
	return PaneStatus{}
}

func screenHas(s *Server, id session.PaneID, want string) bool {
	text, _ := s.RecentText(id, 0)
	return strings.Contains(text, want)
}

// TestARestoredAgentSleepsUntilItsPaneIsFocused: a restart brings every
// agent back asleep, a shell in its place, and focusing the pane resumes it
// in its conversation. If it regresses, a restart starts every agent of
// every company at once again — the machine out of memory — or a sleeping
// agent cannot be woken into the conversation it was in.
func TestARestoredAgentSleepsUntilItsPaneIsFocused(t *testing.T) {
	fakeClaude(t)
	stateFile := filepath.Join(t.TempDir(), "session.json")

	first := sleepyServer(t, stateFile)
	ws, _ := first.NewWorkspace("main")
	_, pane, err := first.NewTab(ws, "t", PaneSpec{Command: []string{"claude"}, Agent: "claude"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := first.ReportAgentSession(pane, "tend:claude", "claude",
		agent.SessionRefFromReport("tend:claude", "claude", "conversation-42", ""), nil); err != nil {
		t.Fatal(err)
	}
	first.saveStructure()
	_ = first.Close()

	second := sleepyServer(t, stateFile)
	waitFor(t, "the restored pane", func() bool { return len(second.Statuses()) == 1 })
	id := second.Statuses()[0].ID
	if st := statusOf(t, second, id); st.Dormant != "claude" {
		t.Fatalf("the restored agent is not asleep: %+v", st)
	}
	waitFor(t, "the line saying it sleeps", func() bool { return screenHas(second, id, "claude is asleep") })
	time.Sleep(300 * time.Millisecond)
	// The first server's "resumed: " is in the history; a resume is not.
	if screenHas(second, id, "resumed: --resume") {
		t.Fatal("the agent was started although nobody went to it")
	}
	// What the next restore needs is still there.
	if p, ok, _ := second.AgentSession(id); !ok || p.Session.ID != "conversation-42" {
		t.Errorf("the sleeping pane's conversation = %+v, %v", p, ok)
	}

	second.FocusPane(id, 0)
	waitFor(t, "the agent back in its conversation", func() bool {
		return screenHas(second, id, "resumed: --resume conversation-42")
	})
	if st := statusOf(t, second, id); st.Dormant != "" {
		t.Errorf("still asleep once woken: %+v", st)
	}
	// Woken, it is the agent again: seen as claude, and put to sleep again.
	waitFor(t, "the woken agent seen as claude", func() bool { return statusOf(t, second, id).Agent == "claude" })
	if _, err := second.SleepAgent(id); err != nil {
		t.Fatalf("sleeping it again: %v", err)
	}
}

// TestRestoredAgentsStartAtOnceWhenSleepingIsOff: with the setting off, a
// restart is what it always was.
func TestRestoredAgentsStartAtOnceWhenSleepingIsOff(t *testing.T) {
	fakeClaude(t)
	stateFile := filepath.Join(t.TempDir(), "session.json")
	first := persistentServer(t, stateFile)
	ws, _ := first.NewWorkspace("main")
	_, pane, _ := first.NewTab(ws, "t", PaneSpec{Command: []string{"claude"}, Agent: "claude"})
	_, _ = first.ReportAgentSession(pane, "tend:claude", "claude",
		agent.SessionRefFromReport("tend:claude", "claude", "c-1", ""), nil)
	first.saveStructure()
	_ = first.Close()

	second := persistentServer(t, stateFile)
	waitFor(t, "the restored pane", func() bool { return len(second.Statuses()) == 1 })
	id := second.Statuses()[0].ID
	waitFor(t, "the agent resumed at once", func() bool { return screenHas(second, id, "resumed: --resume c-1") })
}

// TestAnAgentTypedAtThePromptSleepsAndWakesInItsConversation: an agent
// running as the shell's child is ended and the shell stays; waking types
// its resume at the prompt. If it regresses, sleeping kills the pane, or
// the agent comes back in a new conversation.
func TestAnAgentTypedAtThePromptSleepsAndWakesInItsConversation(t *testing.T) {
	fakeClaude(t)
	s := sleepyServer(t, "")
	ws, _ := s.NewWorkspace("main")
	_, pane, err := s.NewTab(ws, "t", PaneSpec{})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Submit(pane, "claude --resume first-run"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the agent detected", func() bool { return statusOf(t, s, pane).Agent == "claude" })
	if _, err := s.ReportAgentSession(pane, "tend:claude", "claude",
		agent.SessionRefFromReport("tend:claude", "claude", "conv-7", ""), nil); err != nil {
		t.Fatal(err)
	}
	shell := statusOf(t, s, pane).Pid

	res, err := s.SleepAgent(pane)
	if err != nil {
		t.Fatal(err)
	}
	if res.Agent != "claude" {
		t.Errorf("slept %q", res.Agent)
	}
	st := statusOf(t, s, pane)
	if st.Dormant != "claude" || st.Pid != shell || !st.Running {
		t.Fatalf("after sleeping: %+v (shell pid %d)", st, shell)
	}
	waitFor(t, "the line saying it sleeps", func() bool { return screenHas(s, pane, "claude is asleep") })

	if err := s.Wake(pane); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the agent back in its conversation", func() bool { return screenHas(s, pane, "resumed: --resume conv-7") })

	// And, woken, it can be put to sleep again.
	waitFor(t, "the woken agent detected", func() bool { return statusOf(t, s, pane).Agent == "claude" })
	if _, err := s.SleepAgent(pane); err != nil {
		t.Fatalf("sleeping again: %v (status %+v)", err, statusOf(t, s, pane))
	}
	if st := statusOf(t, s, pane); st.Dormant != "claude" {
		t.Fatalf("not asleep again: %+v", st)
	}
}

// TestAnAgentThatIsThePanesOwnProcessSleepsInPlace: `tend new -- claude`
// has no shell to fall back to; the pane is started again in place, the
// same pane with the same screen, as a sleeping shell.
func TestAnAgentThatIsThePanesOwnProcessSleepsInPlace(t *testing.T) {
	fakeClaude(t)
	s := sleepyServer(t, "")
	ws, _ := s.NewWorkspace("main")
	_, pane, err := s.NewTab(ws, "t", PaneSpec{Command: []string{"claude", "hello"}, Agent: "claude"})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the agent's first words", func() bool { return screenHas(s, pane, "resumed: hello") })
	if _, err := s.ReportAgentSession(pane, "tend:claude", "claude",
		agent.SessionRefFromReport("tend:claude", "claude", "conv-9", ""), nil); err != nil {
		t.Fatal(err)
	}
	before := statusOf(t, s, pane).Pid
	if _, err := s.SleepAgent(pane); err != nil {
		t.Fatal(err)
	}
	st := statusOf(t, s, pane)
	if st.Dormant != "claude" || !st.Running || st.Pid == before {
		t.Fatalf("after sleeping in place: %+v (agent pid %d)", st, before)
	}
	if !screenHas(s, pane, "resumed: hello") || !screenHas(s, pane, "claude is asleep") {
		text, _ := s.RecentText(pane, 0)
		t.Fatalf("the screen was not kept:\n%s", text)
	}
	if err := s.Wake(pane); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the agent back in its conversation", func() bool { return screenHas(s, pane, "resumed: --resume conv-9") })
}

// TestAnAgentWhoseConversationIsUnknownIsNotPutToSleep: without knowing
// the conversation, sleeping would lose it; tend says so and leaves it.
func TestAnAgentWhoseConversationIsUnknownIsNotPutToSleep(t *testing.T) {
	fakeClaude(t)
	s := sleepyServer(t, "")
	ws, _ := s.NewWorkspace("main")
	_, pane, _ := s.NewTab(ws, "t", PaneSpec{Command: []string{"claude"}, Agent: "claude"})
	waitFor(t, "the agent", func() bool { return screenHas(s, pane, "resumed:") })
	if _, err := s.SleepAgent(pane); !errors.Is(err, ErrNoConversation) {
		t.Fatalf("got %v", err)
	}
	if st := statusOf(t, s, pane); st.Dormant != "" || !st.Running {
		t.Fatalf("the agent was touched: %+v", st)
	}
}

// TestOnlyIdleUnwatchedAgentsSleepByThemselves: an agent working, one
// focused, one on somebody's screen, and one idle for less than the limit
// are left; the idle one nobody looks at goes to sleep.
func TestOnlyIdleUnwatchedAgentsSleepByThemselves(t *testing.T) {
	s := sleepyServer(t, "")
	s.cfg.SleepAfter = time.Hour
	ws, _ := s.NewWorkspace("main")
	var panes []session.PaneID
	for i := 0; i < 4; i++ {
		_, p, err := s.NewTab(ws, "t", PaneSpec{Command: []string{"/bin/sh", "-c", "sleep 300"}})
		if err != nil {
			t.Fatal(err)
		}
		panes = append(panes, p)
	}
	now := time.Now()
	set := func(id session.PaneID, state detect.State, since time.Duration) {
		s.mu.Lock()
		rt := s.runtimes[id]
		s.mu.Unlock()
		rt.mu.Lock()
		rt.shown.Agent, rt.shown.State = "claude", state
		rt.idleSince = now.Add(-since)
		rt.mu.Unlock()
	}
	set(panes[0], detect.StateIdle, 2*time.Hour)    // sleeps
	set(panes[1], detect.StateWorking, 2*time.Hour) // working
	set(panes[2], detect.StateIdle, 2*time.Hour)    // focused
	set(panes[3], detect.StateIdle, time.Minute)    // not long enough
	s.mu.Lock()
	s.focusedPane = panes[2]
	s.mu.Unlock()

	got := s.sleepyAgents(now)
	if len(got) != 1 || got[0] != panes[0] {
		t.Fatalf("sleepy %v, want only %d", got, panes[0])
	}
	s.cfg.SleepAfter = 0
	if got := s.sleepyAgents(now); len(got) != 0 {
		t.Fatalf("with sleeping off, %v", got)
	}
}

// TestShellLineQuotesWhatNeedsIt: a session id or a path with a space
// reaches the agent whole.
func TestShellLineQuotesWhatNeedsIt(t *testing.T) {
	got := shellLine([]string{"claude", "--resume", "abc-1", "/a b/c", "it's"})
	if got != `claude --resume abc-1 '/a b/c' 'it'\''s'` {
		t.Fatalf("got %s", got)
	}
}
