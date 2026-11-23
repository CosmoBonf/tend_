package explorer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/mattn/go-runewidth"

	"github.com/auth-com-br/tend/internal/vt"
)

// The CI of the branch in view, tend's own and the owner's: GitHub Actions'
// runs of the commit checked out, summed up beside the branch at the foot of
// the panel (✓ passed, ✗ failed, ● running), and a view of the branch's runs
// that opens into each run's jobs and each job's steps, with a job's log, a
// rerun of what failed, and the run on github.com. Everything is asked of
// `gh`, as the issues panel asks it (internal/github): tend keeps no token
// and needs no login of its own. The repository is the one the branch
// pushes to — its upstream's remote, else origin — since that is where the
// runs of a push are.

// CIRun is one workflow run, as `gh run list --json` says it.
type CIRun struct {
	ID         int64     `json:"databaseId"`
	Workflow   string    `json:"workflowName"`
	Title      string    `json:"displayTitle"`
	Branch     string    `json:"headBranch"`
	SHA        string    `json:"headSha"`
	Status     string    `json:"status"`
	Conclusion string    `json:"conclusion"`
	URL        string    `json:"url"`
	Event      string    `json:"event"`
	CreatedAt  time.Time `json:"createdAt"`
}

// CIJob is one job of a run, with its steps.
type CIJob struct {
	ID          int64     `json:"databaseId"`
	Name        string    `json:"name"`
	Status      string    `json:"status"`
	Conclusion  string    `json:"conclusion"`
	URL         string    `json:"url"`
	StartedAt   time.Time `json:"startedAt"`
	CompletedAt time.Time `json:"completedAt"`
	Steps       []CIStep  `json:"steps"`
}

// CIStep is one step of a job.
type CIStep struct {
	Name        string    `json:"name"`
	Number      int       `json:"number"`
	Status      string    `json:"status"`
	Conclusion  string    `json:"conclusion"`
	StartedAt   time.Time `json:"startedAt"`
	CompletedAt time.Time `json:"completedAt"`
}

// How a run, a job or a step stands, as the panel draws it.
type ciState int

const (
	ciNone ciState = iota
	ciPassed
	ciSkipped
	ciRunning
	ciFailed
)

// ciStateOf reads GitHub's status and conclusion.
func ciStateOf(status, conclusion string) ciState {
	if status != "completed" {
		return ciRunning
	}
	switch conclusion {
	case "success":
		return ciPassed
	case "skipped", "neutral":
		return ciSkipped
	case "":
		return ciNone
	}
	return ciFailed // failure, cancelled, timed_out, action_required…
}

func ciGlyph(s ciState) (string, vt.Style) {
	switch s {
	case ciPassed:
		return "✓", styleGreen
	case ciFailed:
		return "✗", styleRed
	case ciRunning:
		return "●", styleYellow
	case ciSkipped:
		return "–", styleDim
	}
	return "○", styleDim
}

// How often the runs are asked for: often while one runs, so a step that
// finishes shows soon; seldom otherwise, since nothing changes until a push.
const (
	ciPollRunning = 10 * time.Second
	ciPollIdle    = 60 * time.Second
	ciPollFailed  = 5 * time.Minute
)

// ci is the panel's CI state.
type ci struct {
	// gh is the GitHub CLI's path, "" when it is not installed, which
	// leaves the panel with no CI at all.
	gh      string
	checked bool
	// slug is owner/repo of the repository asked about, and key what was
	// last asked: the repository, the branch and the commit.
	slug, key string
	runs      []CIRun
	err       string
	next      time.Time
	busy      bool
	job       func() func(*Model)
	// running are the runs seen running, to say so when one ends.
	running map[int64]bool

	// The view.
	cursor   int
	expanded map[int64]bool
	jobs     map[int64][]CIJob
	open     map[int64]bool // jobs open on their steps
}

func (c *ci) ghPath() string {
	if !c.checked {
		c.checked = true
		c.gh, _ = exec.LookPath("gh")
	}
	return c.gh
}

// ciGh runs gh, given half a minute, and says gh's own reason when it fails.
func ciGh(gh string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, gh, args...)
	// gh asks nothing on a terminal it does not have, and an update notice
	// on stderr is not an error.
	cmd.Env = append(os.Environ(), "GH_PROMPT_DISABLED=1", "GH_NO_UPDATE_NOTIFIER=1", "NO_COLOR=1")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	switch {
	case ctx.Err() != nil:
		return nil, errors.New("gh took too long to answer")
	case err != nil:
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return out, errors.New(strings.SplitN(msg, "\n", 2)[0])
		}
		return out, err
	}
	return out, nil
}

var githubRemote = regexp.MustCompile(`github\.com[:/]([^/\s]+)/([^/\s]+?)(?:\.git)?/?$`)

// ciSlug is owner/repo of the GitHub repository the branch pushes to, or ""
// when it is not on GitHub.
func ciSlug(g Git, st *Status) string {
	remote := "origin"
	if i := strings.Index(st.Upstream, "/"); i > 0 {
		remote = st.Upstream[:i]
	}
	out, err := g.run("remote", "get-url", remote)
	if err != nil {
		return ""
	}
	m := githubRemote.FindStringSubmatch(strings.TrimSpace(string(out)))
	if m == nil {
		return ""
	}
	return m[1] + "/" + m[2]
}

// ciRunFields are what a run is read with.
const ciRunFields = "databaseId,workflowName,displayTitle,headBranch,headSha,status,conclusion,url,event,createdAt"

// CIDue hands the loop the CI work waiting: what the view asked for, or the
// runs asked for again when it is time.
func (m *Model) CIDue(now time.Time) (Job, bool) {
	c := &m.ci
	if c.busy {
		return nil, false
	}
	if c.job != nil {
		job := c.job
		c.job, c.busy = nil, true
		return job, true
	}
	st, g := m.status, m.git
	if st == nil || st.Branch == "" || st.Head == "" || g.Top == "" || c.ghPath() == "" {
		return nil, false
	}
	key := g.Top + " " + st.Branch + " " + st.Head + " " + st.Upstream
	if key == c.key && now.Before(c.next) {
		return nil, false
	}
	if key != c.key {
		// Another repository, branch or commit: what was read of the last
		// one says nothing of this, and shown against it would say wrong.
		c.slug = ciSlug(g, st)
		c.key, c.runs, c.err = key, nil, ""
		c.jobs, c.expanded, c.open = map[int64][]CIJob{}, map[int64]bool{}, map[int64]bool{}
		c.cursor = 0
	}
	if c.slug == "" {
		c.runs, c.next = nil, now.Add(ciPollFailed)
		return nil, false
	}
	gh, slug, branch := c.gh, c.slug, st.Branch
	// The runs open in the view are read again with them, so their steps
	// move as the run does.
	var expanded []int64
	for id := range c.expanded {
		expanded = append(expanded, id)
	}
	c.busy = true
	return func() func(*Model) {
		runs, err := ciRunsShared(gh, slug, branch)
		jobs := map[int64][]CIJob{}
		for _, id := range expanded {
			if js, err := ciJobs(gh, slug, id); err == nil {
				jobs[id] = js
			}
		}
		return func(m *Model) { m.applyRuns(key, runs, jobs, err) }
	}, true
}

// ApplyCI takes CI work's answer back on the loop.
func (m *Model) ApplyCI(apply func(*Model)) {
	m.ci.busy = false
	if apply != nil {
		apply(m)
	}
}

// ciShareFor is how fresh another panel's answer must be to be used: under
// the shortest poll, so a run's progress is never older than one poll.
const ciShareFor = 8 * time.Second

// ciRunsShared is the branch's runs, read by whichever panel asks first.
// Every tab has a panel of its own, and the owner keeps fifteen of them
// open; each asking GitHub every ten seconds while a run is going is over
// five thousand calls an hour, which is GitHub's limit. The answer is kept
// in a file the others read while it is fresh.
func ciRunsShared(gh, slug, branch string) ([]CIRun, error) {
	path := filepath.Join(os.TempDir(), "tend-ci-"+ciFileName(slug+"@"+branch)+".json")
	if info, err := os.Stat(path); err == nil && time.Since(info.ModTime()) < ciShareFor {
		if data, err := os.ReadFile(path); err == nil {
			var runs []CIRun
			if json.Unmarshal(data, &runs) == nil {
				return runs, nil
			}
		}
	}
	out, err := ciGh(gh, "run", "list", "-R", slug, "--branch", branch, "-L", "15", "--json", ciRunFields)
	if err != nil {
		return nil, err
	}
	var runs []CIRun
	if err := json.Unmarshal(out, &runs); err != nil {
		return nil, err
	}
	// Written whole and then moved, so a panel reading never reads half.
	tmp := path + "." + strconv.Itoa(os.Getpid())
	if os.WriteFile(tmp, out, 0o600) == nil {
		_ = os.Rename(tmp, path)
	}
	return runs, nil
}

// ciFileName is a name made safe for a file.
func ciFileName(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '.' {
			return r
		}
		return '_'
	}, s)
}

// ciFirstToSay reports whether this panel is the first to tell of a run's
// end: every panel on the repository sees it end, and one notification is
// what the owner wants, not one a panel.
func ciFirstToSay(run int64) bool {
	f, err := os.OpenFile(filepath.Join(os.TempDir(), "tend-ci-said-"+strconv.FormatInt(run, 10)), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return false
	}
	_ = f.Close()
	return true
}

func ciJobs(gh, slug string, run int64) ([]CIJob, error) {
	out, err := ciGh(gh, "run", "view", strconv.FormatInt(run, 10), "-R", slug, "--json", "jobs")
	if err != nil {
		return nil, err
	}
	var v struct {
		Jobs []CIJob `json:"jobs"`
	}
	if err := json.Unmarshal(out, &v); err != nil {
		return nil, err
	}
	return v.Jobs, nil
}

// applyRuns takes the runs read, and says so when one seen running ended.
func (m *Model) applyRuns(key string, runs []CIRun, jobs map[int64][]CIJob, err error) {
	c := &m.ci
	now := time.Now()
	if key != c.key {
		return // the branch moved while gh was asked; the next poll asks again
	}
	if err != nil {
		c.err, c.next = err.Error(), now.Add(ciPollFailed)
		return
	}
	c.err = ""
	if c.running == nil {
		c.running = map[int64]bool{}
	}
	if c.jobs == nil {
		c.jobs = map[int64][]CIJob{}
	}
	for id, js := range jobs {
		c.jobs[id] = js
	}
	busy := false
	for _, r := range runs {
		s := ciStateOf(r.Status, r.Conclusion)
		switch {
		case s == ciRunning:
			busy = true
			c.running[r.ID] = true
		case c.running[r.ID]:
			delete(c.running, r.ID)
			m.ciFinished(r, s)
		}
	}
	c.runs = runs
	c.next = now.Add(ciPollIdle)
	if busy {
		c.next = now.Add(ciPollRunning)
	}
}

// ciFinished says that a run seen running has ended: in the footer, and on
// the desktop for somebody looking elsewhere.
func (m *Model) ciFinished(r CIRun, s ciState) {
	word := "passed"
	if s == ciFailed {
		word = "failed"
	}
	msg := "CI " + word + ": " + r.Workflow + " on " + r.Branch
	m.say(msg, s == ciFailed)
	if !ciFirstToSay(r.ID) {
		return
	}
	desktopNotify("tend · CI "+word, r.Workflow+" on "+r.Branch+" — "+r.Title)
}

// desktopNotify raises a desktop notification where the system has a way,
// and says nothing where it has none.
var desktopNotify = func(title, body string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("osascript", "-e", "display notification "+strconv.Quote(body)+" with title "+strconv.Quote(title))
	case "linux", "freebsd":
		if _, err := exec.LookPath("notify-send"); err != nil {
			return
		}
		cmd = exec.Command("notify-send", title, body)
	default:
		return
	}
	go func() { _ = cmd.Run() }()
}

// ciSummary is how the commit checked out stands: the worst of its runs.
// ok is false when no run is of this commit.
func (m *Model) ciSummary() (ciState, bool) {
	if m.status == nil {
		return ciNone, false
	}
	worst, any := ciNone, false
	for _, r := range m.ci.runs {
		if r.SHA != m.status.Head {
			continue
		}
		any = true
		s := ciStateOf(r.Status, r.Conclusion)
		if s == ciSkipped {
			s = ciPassed
		}
		worst = max(worst, s)
	}
	return worst, any
}

// ciFooterText is what the footer says of the CI, "" for nothing.
func (m *Model) ciFooterText() (string, vt.Style) {
	if len(m.ci.runs) == 0 {
		return "", styleDim
	}
	s, ok := m.ciSummary()
	if !ok {
		// Runs, none of this commit: not pushed yet, or not run yet.
		return "CI –", styleDim
	}
	glyph, style := ciGlyph(s)
	return "CI " + glyph, style
}

// --- the view ------------------------------------------------------------------

// ciRow is one line of the view: a run, a job of it, or a step of a job.
type ciRow struct {
	run  int // index into runs
	job  int // -1 for the run's own line
	step int // -1 for a job's own line
}

func (m *Model) ciRows() []ciRow {
	c := &m.ci
	var rows []ciRow
	for i, r := range c.runs {
		rows = append(rows, ciRow{run: i, job: -1, step: -1})
		if !c.expanded[r.ID] {
			continue
		}
		for j, job := range c.jobs[r.ID] {
			rows = append(rows, ciRow{run: i, job: j, step: -1})
			if !c.open[job.ID] {
				continue
			}
			for k := range job.Steps {
				rows = append(rows, ciRow{run: i, job: j, step: k})
			}
		}
	}
	return rows
}

// openCI shows the branch's runs.
func (m *Model) openCI() {
	if m.ci.ghPath() == "" {
		m.say("CI needs the GitHub CLI: install gh and run gh auth login", true)
		return
	}
	if m.ci.slug == "" && m.ci.key != "" {
		m.say("this branch does not push to GitHub", true)
		return
	}
	c := &m.ci
	if c.expanded == nil {
		c.expanded, c.open, c.jobs = map[int64]bool{}, map[int64]bool{}, map[int64][]CIJob{}
	}
	c.cursor = 0
	// The run of the commit checked out opens on its jobs: it is the one
	// asked about.
	for _, r := range c.runs {
		if m.status != nil && r.SHA == m.status.Head {
			m.toggleRun(r)
			break
		}
	}
	c.next = time.Time{} // and read again now
	m.mode = modeCI
}

// toggleRun opens a run on its jobs, or closes it.
func (m *Model) toggleRun(r CIRun) {
	c := &m.ci
	if c.expanded[r.ID] {
		delete(c.expanded, r.ID)
		return
	}
	c.expanded[r.ID] = true
	if _, ok := c.jobs[r.ID]; ok {
		return
	}
	gh, slug, id := c.gh, c.slug, r.ID
	c.job = func() func(*Model) {
		jobs, err := ciJobs(gh, slug, id)
		return func(m *Model) {
			if err != nil {
				m.fail(err)
				return
			}
			m.ci.jobs[id] = jobs
			// A job that failed opens on its steps, which is where the
			// reason is.
			for _, j := range jobs {
				if ciStateOf(j.Status, j.Conclusion) == ciFailed {
					m.ci.open[j.ID] = true
				}
			}
		}
	}
}

func (m *Model) ciKey(k Key) {
	c := &m.ci
	rows := m.ciRows()
	switch k.Name {
	case "esc", "q", "ctrl+c":
		m.mode = modeList
		return
	case "up", "k", "ctrl+p":
		c.cursor = max(c.cursor-1, 0)
		return
	case "down", "j", "ctrl+n":
		c.cursor = min(c.cursor+1, max(len(rows)-1, 0))
		return
	}
	if c.cursor >= len(rows) {
		return
	}
	row := rows[c.cursor]
	run := c.runs[row.run]
	switch k.Name {
	case "enter", "right", "l", "space":
		m.ciEnter(row)
	case "left", "h":
		switch {
		case row.step >= 0:
			delete(c.open, c.jobs[run.ID][row.job].ID)
			m.ciCursorTo(ciRow{run: row.run, job: row.job, step: -1})
		case row.job >= 0:
			delete(c.expanded, run.ID)
			m.ciCursorTo(ciRow{run: row.run, job: -1, step: -1})
		default:
			delete(c.expanded, run.ID)
		}
	case "o":
		url := run.URL
		if row.job >= 0 {
			url = c.jobs[run.ID][row.job].URL
		}
		if err := openWithSystem(url); err != nil {
			m.fail(err)
		}
	case "r":
		m.ciRerun(run)
	case "L":
		if row.job >= 0 {
			m.ciLog(run, c.jobs[run.ID][row.job])
		}
	case "y":
		if row.job < 0 {
			m.say("y copies a job's log: on a job or one of its steps", false)
			break
		}
		m.ciCopyLog(c.jobs[run.ID][row.job])
	}
	if k.Rune == 'R' {
		m.ci.next = time.Time{}
		m.say("reading the runs again…", false)
	}
}

// ciEnter is enter on a row: a run or a job opens or closes, a step shows
// its job's log.
func (m *Model) ciEnter(row ciRow) {
	c := &m.ci
	run := c.runs[row.run]
	switch {
	case row.job < 0:
		m.toggleRun(run)
	case row.step < 0:
		id := c.jobs[run.ID][row.job].ID
		c.open[id] = !c.open[id]
	default:
		m.ciLog(run, c.jobs[run.ID][row.job])
	}
}

func (m *Model) ciCursorTo(want ciRow) {
	for i, r := range m.ciRows() {
		if r == want {
			m.ci.cursor = i
		}
	}
}

// ciRerun runs again what failed in a run, or the whole run when nothing
// failed in it.
func (m *Model) ciRerun(r CIRun) {
	if ciStateOf(r.Status, r.Conclusion) == ciRunning {
		m.say("still running", false)
		return
	}
	gh, slug, id := m.ci.gh, m.ci.slug, strconv.FormatInt(r.ID, 10)
	failed := ciStateOf(r.Status, r.Conclusion) == ciFailed
	m.ci.job = func() func(*Model) {
		args := []string{"run", "rerun", id, "-R", slug}
		if failed {
			args = append(args, "--failed")
		}
		_, err := ciGh(gh, args...)
		return func(m *Model) {
			if err != nil {
				m.fail(err)
				return
			}
			m.say("running "+r.Workflow+" again", false)
			delete(m.ci.jobs, r.ID)
			m.ci.next = time.Time{}
		}
	}
	m.say("asking GitHub to run it again…", false)
}

// ciLog shows a job's log in the preview tab, at the first error when it
// has one.
func (m *Model) ciLog(r CIRun, j CIJob) {
	m.ciWithLog(j, func(m *Model, text, path string, line int) { m.preview(j.Name, path, line) })
}

// ciCopyLog puts a job's log on the clipboard, the owner's ask: to paste
// it to an agent, or anywhere, without going through the browser.
func (m *Model) ciCopyLog(j CIJob) {
	m.ciWithLog(j, func(m *Model, text, path string, line int) {
		m.copyText(text)
		m.say(fmt.Sprintf("copied the log of %s (%d lines)", j.Name, strings.Count(text, "\n")), false)
	})
}

// ciWithLog reads a job's log away from the loop, and hands it, cleaned
// and kept in a file, to then.
func (m *Model) ciWithLog(j CIJob, then func(m *Model, text, path string, line int)) {
	if ciStateOf(j.Status, j.Conclusion) == ciRunning {
		m.say("the log is GitHub's once the job ends; o opens it running", false)
		return
	}
	gh, slug, id := m.ci.gh, m.ci.slug, j.ID
	m.ci.job = func() func(*Model) {
		// The job's own log, which GitHub serves as soon as the job ends.
		// gh run view --log waits for the whole run: a Linux job that
		// failed in four minutes could not be read while the macOS one ran
		// on for ten. The escape sequences are the log's colours, which the
		// preview draws.
		out, err := ciGh(gh, "api", "--allow-escape-sequences", "repos/"+slug+"/actions/jobs/"+strconv.FormatInt(id, 10)+"/logs")
		if err != nil {
			return func(m *Model) { m.fail(err) }
		}
		text, line := cleanCILog(out)
		path := filepath.Join(os.TempDir(), fmt.Sprintf("tend-ci-%d.log", id))
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			return func(m *Model) { m.fail(err) }
		}
		return func(m *Model) { then(m, text, path, line) }
	}
	m.say("reading the log of "+j.Name+"…", false)
}

var ciTimestamp = regexp.MustCompile(`^\x{feff}?\d{4}-\d\d-\d\dT[0-9:.]+Z ?`)

// cleanCILog is a job's log as GitHub serves it — the time before every
// line — made the lines alone, with the line of the first error.
func cleanCILog(out []byte) (string, int) {
	var b strings.Builder
	first := 0
	for i, line := range strings.Split(strings.TrimRight(strings.ReplaceAll(string(out), "\r\n", "\n"), "\n"), "\n") {
		line = ciTimestamp.ReplaceAllString(line, "")
		if first == 0 && strings.Contains(line, "##[error]") {
			first = i + 1
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String(), first
}

func (m *Model) ciMouse(ev Mouse) {
	c := &m.ci
	rows := m.ciRows()
	switch {
	case ev.Wheel != 0:
		c.cursor = min(max(c.cursor+ev.Wheel, 0), max(len(rows)-1, 0))
	case ev.Press && ev.Y >= 2 && ev.Y < m.rows-1:
		i := ciTop(m, len(rows)) + ev.Y - 2
		if i < len(rows) {
			if i == c.cursor {
				m.ciEnter(rows[i])
			} else {
				c.cursor = i
			}
		}
	}
}

func ciTop(m *Model, n int) int {
	visible := max(m.rows-3, 1)
	return max(min(m.ci.cursor-visible+1, n-visible), 0)
}

func ciDuration(from, to time.Time) string {
	if from.IsZero() {
		return ""
	}
	if to.IsZero() {
		to = time.Now()
	}
	d := to.Sub(from).Round(time.Second)
	if d < time.Minute {
		return strconv.Itoa(int(d.Seconds())) + "s"
	}
	return strconv.Itoa(int(d.Minutes())) + "m" + fmt.Sprintf("%02d", int(d.Seconds())%60) + "s"
}

func ciAge(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Hour:
		return strconv.Itoa(max(int(d.Minutes()), 0)) + "m"
	case d < 48*time.Hour:
		return strconv.Itoa(int(d.Hours())) + "h"
	}
	return strconv.Itoa(int(d.Hours()/24)) + "d"
}

func (m *Model) drawCI(g *vt.Grid) {
	c := &m.ci
	title := "CI"
	if m.status != nil {
		title += " · " + m.status.Branch
	}
	if c.slug != "" {
		title += " · " + c.slug
	}
	put(g, 1, 0, truncate(title, m.cols-2), styleAccent, m.cols)
	if c.busy {
		put(g, m.cols-2, 0, "…", styleDim, m.cols)
	}
	rows := m.ciRows()
	if len(rows) == 0 {
		msg := "no runs of this branch on GitHub Actions"
		switch {
		case c.err != "":
			msg = c.err
		case c.key == "" || c.busy:
			msg = "reading the runs…"
		}
		put(g, 2, 2, truncate(msg, m.cols-3), styleDim, m.cols)
	}
	top := ciTop(m, len(rows))
	for i := 0; i < m.rows-3 && top+i < len(rows); i++ {
		at := top + i
		row, y := rows[at], 2+i
		run := c.runs[row.run]
		var indent, glyph, name, right string
		var gs vt.Style
		switch {
		case row.job < 0:
			mark := "▸ "
			if c.expanded[run.ID] {
				mark = "▾ "
			}
			glyph, gs = ciGlyph(ciStateOf(run.Status, run.Conclusion))
			indent, name = mark, run.Workflow+" · "+run.Title
			if m.status != nil && run.SHA == m.status.Head {
				name = run.Workflow + " · this commit"
			}
			right = ciAge(run.CreatedAt)
		case row.step < 0:
			job := c.jobs[run.ID][row.job]
			mark := "▸ "
			if c.open[job.ID] {
				mark = "▾ "
			}
			glyph, gs = ciGlyph(ciStateOf(job.Status, job.Conclusion))
			indent, name, right = "  "+mark, job.Name, ciDuration(job.StartedAt, job.CompletedAt)
		default:
			st := c.jobs[run.ID][row.job].Steps[row.step]
			glyph, gs = ciGlyph(ciStateOf(st.Status, st.Conclusion))
			if st.Status == "queued" || st.Status == "pending" || st.Status == "waiting" {
				glyph, gs = "○", styleDim
			}
			indent, name, right = "      ", st.Name, ciDuration(st.StartedAt, st.CompletedAt)
		}
		style := styleNormal
		if at == c.cursor {
			fill(g, y, 0, m.cols, styleSel)
			style, gs = styleSel, vt.Style{FG: gs.FG, BG: styleSel.BG, Attrs: gs.Attrs | styleSel.Attrs}
		}
		x := put(g, 1, y, indent, style, m.cols)
		x = put(g, x, y, glyph+" ", gs, m.cols)
		rw := runewidth.StringWidth(right)
		limit := m.cols - rw - 2
		put(g, x, y, truncate(name, max(limit-x, 1)), style, limit)
		if right != "" && limit > x {
			put(g, m.cols-rw-1, y, right, style, m.cols)
		}
	}
	put(g, 1, m.rows-1, truncate("enter open · L log · y copy log · r rerun · o browser · esc", m.cols-2), styleDim, m.cols)
}
