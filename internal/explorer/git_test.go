//go:build unix

package explorer

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestThePanelsStatusTakesNoLockOnTheIndex: reading the status leaves the
// index as it was, even when a file's timestamps changed and git would
// otherwise write back what it refreshed — which it does under the index's
// lock. If it regresses, the user's git add or commit, run while the panel
// polls, fails with "index.lock: File exists", as the owner's did.
func TestThePanelsStatusTakesNoLockOnTheIndex(t *testing.T) {
	dir := t.TempDir()
	env := append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = env
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	file := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(file, []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "a.txt")
	git("commit", "-q", "-m", "a")
	// Same content, new times: the index's cached stat is now stale, and a
	// plain git status would refresh it and write the index back.
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(file, later, later); err != nil {
		t.Fatal(err)
	}
	index := filepath.Join(dir, ".git", "index")
	before, err := os.ReadFile(index)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := FindRepo(dir).Status(); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(index)
	if !bytes.Equal(before, after) {
		t.Error("reading the status wrote the index, under its lock")
	}
}

// TestTheBranchFirstByNameIsListedWhenAnotherIsCurrent: on main, a branch
// named before it — develop — is in the picker as the local branch it is,
// and switching to it works. If it regresses, the first branch drops out of
// the list, its origin/ copy stands in, and picking that fails with "a
// branch named 'develop' already exists", as it did for the owner.
func TestTheBranchFirstByNameIsListedWhenAnotherIsCurrent(t *testing.T) {
	dir := t.TempDir()
	env := append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = env
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q", "-b", "main")
	git("commit", "-q", "--allow-empty", "-m", "a")
	git("branch", "develop")
	// A remote copy of both, as a clone has.
	git("update-ref", "refs/remotes/origin/develop", "HEAD")
	git("update-ref", "refs/remotes/origin/main", "HEAD")

	repo := FindRepo(dir)
	branches, err := repo.Branches()
	if err != nil {
		t.Fatal(err)
	}
	var develop *Branch
	for i, b := range branches {
		if b.Name == "origin/develop" {
			t.Errorf("origin/develop listed beside the local develop: %+v", branches)
		}
		if b.Name == "develop" {
			develop = &branches[i]
		}
	}
	if develop == nil || develop.Remote {
		t.Fatalf("develop is not listed as a local branch: %+v", branches)
	}
	if err := repo.Switch(*develop); err != nil {
		t.Fatalf("switching to develop: %v", err)
	}
}
