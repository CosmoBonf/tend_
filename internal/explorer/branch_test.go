package explorer

import (
	"path/filepath"
	"strings"
	"testing"
)

// remoteAndClone is a bare repository standing in for a remote, a clone of
// it to work in, and a second clone that pushes behind the first's back.
func remoteAndClone(t *testing.T) (work, other string) {
	t.Helper()
	withIdentity(t)
	base := t.TempDir()
	remote := filepath.Join(base, "remote.git")
	gitIn(t, base, "init", "-q", "--bare", "-b", "main", remote)
	seed := filepath.Join(base, "seed")
	gitIn(t, base, "clone", "-q", remote, seed)
	write(t, seed, "a.txt", "a\n")
	gitIn(t, seed, "add", "-A")
	gitIn(t, seed, "commit", "-q", "-m", "first")
	gitIn(t, seed, "push", "-q", "origin", "HEAD:main")
	gitIn(t, seed, "push", "-q", "origin", "HEAD:feature")
	work, other = filepath.Join(base, "work"), filepath.Join(base, "other")
	gitIn(t, base, "clone", "-q", remote, work)
	gitIn(t, base, "clone", "-q", remote, other)
	return work, other
}

// TestSyncPushesWhatIsAheadAndPullsWhatIsBehind: the sync button brings the
// branch level with its upstream both ways, fast-forward only, and says
// what it did; the header shows how far apart they are. If it regresses, a
// finished agent's commits sit unpushed behind a button that does nothing.
func TestSyncPushesWhatIsAheadAndPullsWhatIsBehind(t *testing.T) {
	work, other := remoteAndClone(t)
	write(t, work, "b.txt", "b\n")
	gitIn(t, work, "add", "-A")
	gitIn(t, work, "commit", "-q", "-m", "ahead")
	m := New(work, nil)
	if text := screen(m, 40, 10); !strings.Contains(text, "⎇ main ↑1") || !strings.Contains(text, "⟳") {
		t.Fatalf("the bar should say ahead by one, with the button:\n%s", text)
	}
	keys(m, "P")
	m.runJobNow()
	if !strings.Contains(m.message, "pushed 1") {
		t.Fatalf("sync said %q (err %v)", m.message, m.messageErr)
	}
	gitIn(t, other, "pull", "-q")
	if log := gitIn(t, other, "log", "--oneline", "-1"); !strings.Contains(log, "ahead") {
		t.Errorf("the commit did not reach the remote: %s", log)
	}

	write(t, other, "c.txt", "c\n")
	gitIn(t, other, "add", "-A")
	gitIn(t, other, "commit", "-q", "-m", "from elsewhere")
	gitIn(t, other, "push", "-q")
	// A click on the button, this time.
	screen(m, 40, 10)
	m.Mouse(Mouse{X: m.gitBarLayout().syncAt, Y: 1, Press: true})
	m.runJobNow()
	if !strings.Contains(m.message, "pulled 1") {
		t.Fatalf("sync said %q", m.message)
	}
	if log := gitIn(t, work, "log", "--oneline", "-1"); !strings.Contains(log, "from elsewhere") {
		t.Errorf("the work clone did not pull: %s", log)
	}
}

// TestSyncPublishesABranchWithNoUpstream: a new local branch is pushed to
// origin and set to track it. If it regresses, a branch made for an
// agent's work cannot be shared from the panel.
func TestSyncPublishesABranchWithNoUpstream(t *testing.T) {
	work, _ := remoteAndClone(t)
	gitIn(t, work, "switch", "-q", "-c", "agent/fix")
	m := New(work, nil)
	keys(m, "P")
	m.runJobNow()
	if !strings.Contains(m.message, "published agent/fix") {
		t.Fatalf("sync said %q", m.message)
	}
	if up := gitIn(t, work, "rev-parse", "--abbrev-ref", "@{upstream}"); strings.TrimSpace(up) != "origin/agent/fix" {
		t.Errorf("upstream = %q", up)
	}
}

// TestTheBranchPickerSwitchesAndTracksARemoteBranch: B (or a click on the
// branch) lists the local branches and the remote ones not here yet;
// typing filters; enter switches, making a local branch that tracks a
// remote one. If it regresses, changing branch means leaving the panel.
func TestTheBranchPickerSwitchesAndTracksARemoteBranch(t *testing.T) {
	work, _ := remoteAndClone(t)
	m := New(work, nil)
	screen(m, 40, 12)
	bar := m.gitBarLayout()
	m.Mouse(Mouse{X: bar.branchFrom + 1, Y: 1, Press: true})
	text := screen(m, 40, 12)
	if !strings.Contains(text, "switch branch") || !strings.Contains(text, "● main") || !strings.Contains(text, "origin/feature") {
		t.Fatalf("the picker:\n%s", text)
	}
	if strings.Contains(text, "origin/main") {
		t.Errorf("origin/main is main, already here:\n%s", text)
	}
	for _, r := range "feat" {
		m.Key(Key{Name: string(r), Rune: r})
	}
	keys(m, "enter")
	if head := gitIn(t, work, "branch", "--show-current"); strings.TrimSpace(head) != "feature" {
		t.Fatalf("on %q (panel says %q)", head, m.message)
	}
	if up := gitIn(t, work, "rev-parse", "--abbrev-ref", "@{upstream}"); strings.TrimSpace(up) != "origin/feature" {
		t.Errorf("upstream = %q", up)
	}
	if text := screen(m, 40, 12); !strings.Contains(text, "⎇ feature") {
		t.Errorf("the bar should say the new branch:\n%s", text)
	}
}

// TestTheBranchPickerCreatesABranchOfANameTyped: a name no branch has is
// offered as "create branch", last, and enter makes it from what is checked
// out and switches to it, uncommitted changes and all; a name a branch has,
// here or on origin, is not offered again. If it regresses, a new branch
// means leaving the panel for a shell.
func TestTheBranchPickerCreatesABranchOfANameTyped(t *testing.T) {
	work, _ := remoteAndClone(t)
	write(t, work, "a.txt", "changed\n")
	m := New(work, nil)
	screen(m, 50, 12)
	type_ := func(s string) {
		for _, r := range s {
			m.Key(Key{Name: string(r), Rune: r})
		}
	}

	keys(m, "B")
	type_("feature")
	if text := screen(m, 50, 12); strings.Contains(text, "create branch") {
		t.Errorf("feature is on origin; offered to create it again:\n%s", text)
	}
	keys(m, "esc")

	keys(m, "B")
	type_("fix/login")
	if text := screen(m, 50, 12); !strings.Contains(text, `+ create branch "fix/login"`) {
		t.Fatalf("no offer to create fix/login:\n%s", text)
	}
	keys(m, "enter")
	if head := gitIn(t, work, "branch", "--show-current"); strings.TrimSpace(head) != "fix/login" {
		t.Fatalf("on %q (panel says %q)", head, m.message)
	}
	if st := gitIn(t, work, "status", "--porcelain"); !strings.Contains(st, "a.txt") {
		t.Errorf("the uncommitted change did not come along: %q", st)
	}
	if text := screen(m, 50, 12); !strings.Contains(text, "fix/login") {
		t.Errorf("the bar should say the new branch:\n%s", text)
	}
}

// TestEachStepToTheRemoteHasItsOwnStage: a file not added, one added, one
// committed and not pushed, and one pushed are told apart — the owner could
// not see whether git add had worked, nor that a commit was still waiting
// for a push — and a folder takes the step furthest behind inside it. If it
// regresses, untracked and staged look the same, and a commit not pushed
// looks like one that was.
func TestEachStepToTheRemoteHasItsOwnStage(t *testing.T) {
	work, _ := remoteAndClone(t)
	write(t, work, "pushed/p.txt", "p\n")
	gitIn(t, work, "add", "-A")
	gitIn(t, work, "commit", "-q", "-m", "p")
	gitIn(t, work, "push", "-q")

	write(t, work, "sent/c.txt", "c\n")
	gitIn(t, work, "add", "-A")
	gitIn(t, work, "commit", "-q", "-m", "c")
	write(t, work, "mixed/added.txt", "a\n")
	gitIn(t, work, "add", "mixed/added.txt")
	write(t, work, "mixed/new.txt", "n\n")
	write(t, work, "a.txt", "changed\n")
	gitIn(t, work, "add", "a.txt")

	st, err := FindRepo(work).Status()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		path string
		dir  bool
		want Stage
	}{
		{"pushed/p.txt", false, StageNone},
		{"pushed", true, StageNone},
		{"sent/c.txt", false, StageAhead},
		{"sent", true, StageAhead},
		{"a.txt", false, StageStaged},
		{"mixed/added.txt", false, StageStaged},
		{"mixed/new.txt", false, StageUnstaged},
		{"mixed", true, StageUnstaged},
		{".", true, StageUnstaged},
	} {
		if got := st.StageOf(c.path, c.dir); got != c.want {
			t.Errorf("%s: stage %d, want %d", c.path, got, c.want)
		}
	}

	// Pushed, the arrow goes.
	gitIn(t, work, "push", "-q")
	st, _ = FindRepo(work).Status()
	if got := st.StageOf("sent/c.txt", false); got != StageNone {
		t.Errorf("sent/c.txt pushed, still stage %d", got)
	}

	// A branch never pushed lists what it committed as not pushed.
	gitIn(t, work, "switch", "-q", "-c", "agent/x")
	write(t, work, "x.txt", "x\n")
	gitIn(t, work, "add", "-A")
	gitIn(t, work, "commit", "-q", "-m", "x")
	st, _ = FindRepo(work).Status()
	if got := st.StageOf("x.txt", false); got != StageAhead {
		t.Errorf("x.txt on a branch never pushed: stage %d", got)
	}
	if text := screen(New(work, nil), 40, 12); !strings.Contains(text, "x.txt") || !strings.Contains(text, "↑") {
		t.Errorf("the tree should mark x.txt with an arrow:\n%s", text)
	}
}
