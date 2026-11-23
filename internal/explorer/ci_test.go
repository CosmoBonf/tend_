//go:build unix

package explorer

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/auth-com-br/tend/internal/vt"
)

// fakeGH puts a stand-in gh first on PATH that answers from files in dir:
// runs.json for run list, jobs.json for run view --json, log.txt for a
// job's log, served as GitHub's API serves it. It records each call in calls.txt.
func fakeGH(t *testing.T) (dir string) {
	t.Helper()
	dir, bin := t.TempDir(), t.TempDir()
	script := `#!/bin/sh
echo "$*" >> "` + dir + `/calls.txt"
case "$1 $2" in
"run list") cat "` + dir + `/runs.json" ;;
"run view")
	case "$*" in
	*) cat "` + dir + `/jobs.json" ;;
	esac ;;
"run rerun") echo ok ;;
"api --allow-escape-sequences") cat "` + dir + `/log.txt" ;;
esac
`
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	// The shared answer (ciRunsShared) and the said-once marks go here, not
	// into the machine's /tmp where another panel could read them.
	t.Setenv("TMPDIR", t.TempDir())
	return dir
}

func writeFile(t *testing.T, path, text string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

// pollCI does what the loop does: the CI work due, run and applied.
func pollCI(m *Model) {
	if job, ok := m.CIDue(time.Now()); ok {
		m.ApplyCI(job())
	}
}

// TestTheBranchsCIIsBesideItAndOpensOnItsSteps: the footer says how the
// commit checked out stands on GitHub Actions — running, then passed — a
// run that ends is said once, and C lists the runs, the run of this commit
// open on its jobs, a failed job open on its steps, and enter on a step
// shows the job's log at its first error. If it regresses, the owner is
// back to the browser to see whether a push passed and why it did not.
func TestTheBranchsCIIsBesideItAndOpensOnItsSteps(t *testing.T) {
	gh := fakeGH(t)
	work, _ := remoteAndClone(t)
	gitIn(t, work, "remote", "set-url", "origin", "git@github.com:owner/repo.git")
	head := strings.TrimSpace(gitIn(t, work, "rev-parse", "HEAD"))
	var said []string
	orig := desktopNotify
	desktopNotify = func(title, body string) { said = append(said, title) }
	defer func() { desktopNotify = orig }()

	run := func(status, conclusion string) string {
		return `[{"databaseId":7,"workflowName":"CI","displayTitle":"a commit","headBranch":"main","headSha":"` + head +
			`","status":"` + status + `","conclusion":"` + conclusion + `","url":"https://github.com/owner/repo/actions/runs/7","event":"push","createdAt":"2026-11-22T10:00:00Z"},` +
			`{"databaseId":6,"workflowName":"CI","displayTitle":"older","headBranch":"main","headSha":"0000","status":"completed","conclusion":"success","url":"u","event":"push","createdAt":"2026-11-21T10:00:00Z"}]`
	}
	writeFile(t, filepath.Join(gh, "runs.json"), run("in_progress", ""))
	writeFile(t, filepath.Join(gh, "jobs.json"), `{"jobs":[{"databaseId":70,"name":"check (ubuntu)","status":"completed","conclusion":"failure","url":"j","startedAt":"2026-11-22T10:00:00Z","completedAt":"2026-11-22T10:04:00Z","steps":[`+
		`{"name":"Checkout","number":1,"status":"completed","conclusion":"success"},`+
		`{"name":"Tests","number":2,"status":"completed","conclusion":"failure"}]}]}`)
	writeFile(t, filepath.Join(gh, "log.txt"), "\ufeff2026-11-22T10:01:00.0000000Z go test ./...\r\n"+
		"2026-11-22T10:02:00.0000000Z --- FAIL: TestX\r\n"+
		"2026-11-22T10:02:01.0000000Z ##[error]Process completed with exit code 1.\r\n")

	opener := &previewingOpener{}
	m := New(work, opener)
	screen(m, 50, 14)
	pollCI(m)
	if text := screen(m, 50, 14); !strings.Contains(text, "CI ●") {
		t.Fatalf("the footer should say the run is going:\n%s", text)
	}

	writeFile(t, filepath.Join(gh, "runs.json"), run("completed", "success"))
	m.ci.next = time.Time{}
	time.Sleep(ciShareFor + 100*time.Millisecond) // past the shared answer
	pollCI(m)
	if text := screen(m, 50, 14); !strings.Contains(text, "CI ✓") {
		t.Fatalf("the footer should say it passed:\n%s", text)
	}
	if len(said) != 1 || !strings.Contains(said[0], "passed") || !strings.Contains(m.message, "CI passed") {
		t.Errorf("the end was said %v, footer %q", said, m.message)
	}
	// A second panel on the same repository sees the same end and keeps quiet.
	other := New(work, nil)
	other.ci.running = map[int64]bool{7: true}
	other.ciFinished(m.ci.runs[0], ciPassed)
	if len(said) != 1 {
		t.Errorf("a second panel said it again: %v", said)
	}

	keys(m, "C")
	m.runCIJobNow()
	text := screen(m, 50, 14)
	for _, want := range []string{"CI · main · owner/repo", "this commit", "check (ubuntu)", "✗ Tests", "✓ Checkout", "older"} {
		if !strings.Contains(text, want) {
			t.Errorf("the CI view lacks %q:\n%s", want, text)
		}
	}
	// To the failed step, and its log.
	for i, r := range m.ciRows() {
		if r.step == 1 {
			m.ci.cursor = i
		}
	}
	keys(m, "enter")
	m.runCIJobNow()
	if len(opener.previewed) != 1 {
		t.Fatalf("the log was not shown (%q)", m.message)
	}
	log, _ := os.ReadFile(opener.previewed[0])
	if strings.Contains(string(log), "\r") || strings.Contains(string(log), "2026-11-22T") || !strings.Contains(string(log), "--- FAIL: TestX") {
		t.Errorf("the log should be the lines alone:\n%s", log)
	}
	// y puts the same log on the clipboard, by the OSC 52 tend passes on.
	var clip strings.Builder
	clipboardOut = &clip
	defer func() { clipboardOut = os.Stdout }()
	keys(m, "y")
	m.runCIJobNow()
	if !strings.Contains(clip.String(), "\x1b]52;c;") || !strings.Contains(m.message, "copied the log of check (ubuntu)") {
		t.Errorf("y did not copy the log: %q, footer %q", clip.String(), m.message)
	}
	raw, _ := os.ReadFile(filepath.Join(gh, "log.txt"))
	if _, line := cleanCILog(raw); line != 3 {
		t.Errorf("the log opens on line %d, not on the error's, 3", line)
	}
}

// runCIJobNow runs the CI work waiting, as the loop would.
func (m *Model) runCIJobNow() {
	m.ci.next = time.Now().Add(time.Hour) // only the job asked for, no poll
	if job, ok := m.CIDue(time.Now()); ok {
		m.ApplyCI(job())
	}
}

// TestTheCIRepositoryIsTheOneTheBranchPushesTo: github remotes in both
// spellings are read as owner/repo; anything else has no CI.
func TestTheCIRepositoryIsTheOneTheBranchPushesTo(t *testing.T) {
	for url, want := range map[string]string{
		"git@github.com:CosmoBonf/tend_.git":   "CosmoBonf/tend_",
		"https://github.com/auth-com-br/tend":  "auth-com-br/tend",
		"https://github.com/o/r.git":           "o/r",
		"git@gitlab.com:o/r.git":               "",
		"ssh://git@github.com/owner/name.git/": "owner/name",
	} {
		got := ""
		if m := githubRemote.FindStringSubmatch(url); m != nil {
			got = m[1] + "/" + m[2]
		}
		if got != want {
			t.Errorf("%s: %q, want %q", url, got, want)
		}
	}
}

// TestAPreviewCopiesWhatIsDraggedOverAndAllOnY: the preview takes the
// mouse for its wheel, so tend selects nothing in it; a drag over its lines
// selects them, lit, and letting go copies them by OSC 52 without the line
// numbers; y copies the whole file. If it regresses, a value in a .env
// cannot be copied out of the preview, as the owner found.
func TestAPreviewCopiesWhatIsDraggedOverAndAllOnY(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	writeFile(t, path, "DB_HOST=localhost\nDB_PASS=s3cret\nPORT=8080\n")
	var clip strings.Builder
	clipboardOut = &clip
	defer func() { clipboardOut = os.Stdout }()
	decoded := func() string {
		t.Helper()
		s := clip.String()
		i := strings.LastIndex(s, "\x1b]52;c;")
		if i < 0 {
			return ""
		}
		b64 := strings.TrimSuffix(s[i+len("\x1b]52;c;"):], "\x07")
		out, err := base64.StdEncoding.DecodeString(b64)
		if err != nil {
			t.Fatal(err)
		}
		return string(out)
	}

	p := NewPreview(path, 0, nil)
	g := vt.NewGrid(40, 10, 0)
	p.Draw(g)
	// Row 1 is DB_HOST, row 2 DB_PASS; from DB_PASS's value to its end.
	x := p.textFrom + len("DB_PASS=")
	p.Mouse(Mouse{X: x, Y: 2, Press: true})
	p.Mouse(Mouse{X: 38, Y: 2, Press: true, Drag: true})
	p.Draw(g)
	if g.Line(2).Cell(x).Style.Attrs&vt.AttrReverse == 0 {
		t.Error("the selection is not lit")
	}
	p.Mouse(Mouse{X: 38, Y: 2, Release: true})
	if got := decoded(); got != "s3cret" {
		t.Errorf("dragged over the value, copied %q", got)
	}
	// Over two lines, from the start of the first.
	p.Mouse(Mouse{X: 0, Y: 1, Press: true})
	p.Mouse(Mouse{X: 39, Y: 2, Press: true, Drag: true})
	p.Mouse(Mouse{X: 39, Y: 2, Release: true})
	if got := decoded(); got != "DB_HOST=localhost\nDB_PASS=s3cret" {
		t.Errorf("two lines copied as %q", got)
	}
	p.Key(Key{Name: "y", Rune: 'y'})
	if got := decoded(); got != "DB_HOST=localhost\nDB_PASS=s3cret\nPORT=8080\n" || !strings.Contains(p.message, "copied 3 lines") {
		t.Errorf("y copied %q, said %q", got, p.message)
	}
}
