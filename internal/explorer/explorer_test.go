package explorer

import (
	"bufio"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/auth-com-br/tend/internal/vt"
)

// repo is a git repository with a committed file, a changed one, a new one
// and an ignored one.
func repo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("needs git")
	}
	dir := t.TempDir()
	gitIn(t, dir, "init", "-q", "-b", "main")
	write(t, dir, "kept.txt", "one\n")
	write(t, dir, "src/changed.go", "package src\n")
	write(t, dir, ".gitignore", "build/\n")
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-q", "-m", "first")
	write(t, dir, "src/changed.go", "package src\n\nvar x = 1\n")
	write(t, dir, "brandnew.md", "# new\n")
	write(t, dir, "build/out.bin", "x")
	return dir
}

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func write(t *testing.T, dir, name, text string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

// screen draws the model and returns its text, a line per row.
func screen(m *Model, cols, rows int) string {
	g := vt.NewGrid(cols, rows, 0)
	m.Draw(g)
	var b strings.Builder
	for y := 0; y < rows; y++ {
		row := g.Line(y)
		var line strings.Builder
		for x := 0; x < row.Len(); x++ {
			c := row.Cell(x)
			if c.Width == 0 {
				continue
			}
			if c.R == 0 {
				line.WriteRune(' ')
			} else {
				line.WriteRune(c.R)
			}
		}
		b.WriteString(strings.TrimRight(line.String(), " "))
		b.WriteByte('\n')
	}
	return b.String()
}

type recordingOpener struct {
	opened []string
	lines  []int
}

func (o *recordingOpener) Open(path string, line int) error {
	o.opened = append(o.opened, path)
	o.lines = append(o.lines, line)
	return nil
}

// click is a press and its release where the pointer pressed, as a terminal
// sends one.
func click(m *Model, x, y int) {
	m.Mouse(Mouse{X: x, Y: y, Press: true})
	m.Mouse(Mouse{X: x, Y: y, Release: true})
}

func keys(m *Model, names ...string) {
	for _, n := range names {
		k := Key{Name: n}
		if r := []rune(n); len(r) == 1 {
			k.Rune = r[0]
		}
		m.Key(k)
	}
}

// withIdentity gives git an author, which a machine running the
// tests may not have configured.
func withIdentity(t *testing.T) {
	t.Setenv("GIT_AUTHOR_NAME", "t")
	t.Setenv("GIT_AUTHOR_EMAIL", "t@t")
	t.Setenv("GIT_COMMITTER_NAME", "t")
	t.Setenv("GIT_COMMITTER_EMAIL", "t@t")
}

// TestStatusReadsGitsPorcelain: the letters, the branch and how far it is
// from upstream come from git's own machine format. If it regresses, the
// panel marks the wrong files, or a rename as a deletion.
func TestStatusReadsGitsPorcelain(t *testing.T) {
	out := strings.Join([]string{
		"# branch.oid abc",
		"# branch.head main",
		"# branch.upstream origin/main",
		"# branch.ab +2 -1",
		"1 .M N... 100644 100644 100644 a b src/changed.go",
		"1 A. N... 000000 100644 100644 a b added.txt",
		"2 R. N... 100644 100644 100644 a b R100 new/name.go",
		"old/name.go",
		"u UU N... 100644 100644 100644 100644 a b c both.go",
		"? brandnew.md",
		"? newdir/",
		"",
	}, "\x00")
	st := parseStatus([]byte(out))
	if st.Branch != "main" || st.Upstream != "origin/main" || st.Ahead != 2 || st.Behind != 1 {
		t.Errorf("branch = %q %q +%d -%d", st.Branch, st.Upstream, st.Ahead, st.Behind)
	}
	want := map[string]byte{
		"src/changed.go": 'M', "added.txt": 'A', "new/name.go": 'R',
		"both.go": '!', "brandnew.md": 'U', "newdir": 'U',
	}
	for path, letter := range want {
		c, ok := st.Of(path)
		if !ok || c.Letter() != letter {
			t.Errorf("%s: %q (listed %v), want %q", path, c.Letter(), ok, letter)
		}
	}
	if c, _ := st.Of("new/name.go"); c.Orig != "old/name.go" {
		t.Errorf("rename came from %q", c.Orig)
	}
	if st.DirLetter("src") != 'M' || st.DirLetter("new") != 'R' {
		t.Errorf("directories: src %q new %q", st.DirLetter("src"), st.DirLetter("new"))
	}
	if c, _ := st.Of("added.txt"); !c.Staged() || c.Unstaged() {
		t.Errorf("an added file is staged and nothing else: %+v", c)
	}
}

// TestTheTreeShowsTheProjectWithGitsNews: directories first, git's letter
// beside a changed file and a dot beside a directory holding one, ignored
// files dimmed rather than hidden. If it regresses, the panel says nothing a
// plain ls does not.
func TestTheTreeShowsTheProjectWithGitsNews(t *testing.T) {
	dir := repo(t)
	m := New(filepath.Join(dir, "src"), nil)
	text := screen(m, 40, 12)

	if !strings.Contains(text, "files │ search │ changes 2") {
		t.Errorf("header:\n%s", text)
	}
	if !strings.Contains(text, filepath.Base(dir)+" ⎇ main") {
		t.Errorf("the whole repository, not the pane's directory, with its branch:\n%s", text)
	}
	lines := strings.Split(text, "\n")
	find := func(name string) string {
		for _, l := range lines {
			if strings.Contains(l, name) {
				return l
			}
		}
		t.Fatalf("%s is not listed:\n%s", name, text)
		return ""
	}
	if strings.Index(text, "▸ src") > strings.Index(text, "kept.txt") {
		t.Errorf("directories come first:\n%s", text)
	}
	if !strings.HasSuffix(find("brandnew.md"), "U") || !strings.HasSuffix(find("▸ src"), "●") {
		t.Errorf("letters:\n%s", text)
	}
	if strings.HasSuffix(find("kept.txt"), "M") {
		t.Errorf("an unchanged file has no letter:\n%s", text)
	}
	var ignored *Node
	for _, n := range m.fileRows {
		if n.Name == "build" {
			ignored = n
		}
	}
	if ignored == nil || !ignored.Ignored {
		t.Errorf("build/ is ignored and still listed: %+v", ignored)
	}

	// Opening src shows what is in it, one level in.
	for i, n := range m.fileRows {
		if n.Name == "src" {
			m.cursor[ViewFiles] = i
		}
	}
	keys(m, "right")
	if text := screen(m, 40, 12); !strings.Contains(text, "    changed.go") {
		t.Errorf("src opened:\n%s", text)
	}
	keys(m, "left")
	if text := screen(m, 40, 12); strings.Contains(text, "changed.go") {
		t.Errorf("src closed:\n%s", text)
	}
}

// TestTheTreeFollowsTheDisk: a file written while the panel is up shows at
// the next refresh, with the cursor left on what it was on. If it
// regresses, the panel is a picture of the project as it was when opened.
func TestTheTreeFollowsTheDisk(t *testing.T) {
	dir := repo(t)
	m := New(dir, nil)
	screen(m, 40, 12)
	for i, n := range m.fileRows {
		if n.Name == "kept.txt" {
			m.cursor[ViewFiles] = i
		}
	}
	write(t, dir, "aaa-first.txt", "new\n")
	m.Refresh()
	text := screen(m, 40, 12)
	if !strings.Contains(text, "aaa-first.txt") {
		t.Errorf("the new file:\n%s", text)
	}
	if m.selectedNode().Name != "kept.txt" {
		t.Errorf("the cursor moved to %s", m.selectedNode().Name)
	}
}

// TestEnterOpensAFileAndSpaceShowsIt: enter hands the file to the editor,
// space shows it in the panel with line numbers. If it regresses, the
// panel is a list to look at and nothing more.
func TestEnterOpensAFileAndSpaceShowsIt(t *testing.T) {
	dir := repo(t)
	opener := &recordingOpener{}
	m := New(dir, opener)
	screen(m, 40, 12)
	for i, n := range m.fileRows {
		if n.Name == "kept.txt" {
			m.cursor[ViewFiles] = i
		}
	}
	keys(m, "enter")
	if len(opener.opened) != 1 || opener.opened[0] != filepath.Join(dir, "kept.txt") {
		t.Errorf("opened %v", opener.opened)
	}
	keys(m, "space")
	text := screen(m, 40, 12)
	if !strings.Contains(text, "‹ kept.txt") || !strings.Contains(text, "1 one") {
		t.Errorf("the viewer:\n%s", text)
	}
	keys(m, "q")
	if text := screen(m, 40, 12); !strings.Contains(text, "files │ search │ changes") {
		t.Errorf("q goes back to the list:\n%s", text)
	}
}

// TestTheChangesViewStagesDiffsAndCommits is the source-control half end to
// end against a real repository: stage a file, see its diff, commit it. If
// it regresses, the one place in tend to review an agent's work before
// committing it does not.
func TestTheChangesViewStagesDiffsAndCommits(t *testing.T) {
	withIdentity(t)
	dir := repo(t)
	m := New(dir, nil)
	keys(m, "3")
	text := screen(m, 50, 14)
	if !strings.Contains(text, "CHANGES 2") || !strings.Contains(text, "changed.go src") || !strings.Contains(text, "brandnew.md") {
		t.Fatalf("changes:\n%s", text)
	}

	// The diff of the changed file.
	for i, r := range m.changeRows {
		if r.change.Path == "src/changed.go" {
			m.cursor[ViewChanges] = i
		}
	}
	keys(m, "enter")
	if text := screen(m, 50, 14); !strings.Contains(text, "+var x = 1") || !strings.Contains(text, "unstaged") {
		t.Errorf("the diff:\n%s", text)
	}
	keys(m, "esc")

	// Committing with nothing staged says how to stage.
	keys(m, "c")
	if !strings.Contains(m.message, "nothing staged") {
		t.Errorf("c with nothing staged: %q", m.message)
	}

	keys(m, "s")
	text = screen(m, 50, 14)
	if !strings.Contains(text, "STAGED 1") || !strings.Contains(text, "CHANGES 1") {
		t.Fatalf("after staging:\n%s", text)
	}

	keys(m, "c")
	for _, r := range "tighten x" {
		m.Key(Key{Name: string(r), Rune: r})
	}
	if text := screen(m, 50, 14); !strings.Contains(text, "commit: tighten x") {
		t.Errorf("the commit line:\n%s", text)
	}
	keys(m, "enter")
	if log := gitIn(t, dir, "log", "--oneline", "-1"); !strings.Contains(log, "tighten x") {
		t.Errorf("git log: %s (panel says %q)", log, m.message)
	}
	if text := screen(m, 50, 14); strings.Contains(text, "STAGED") || !strings.Contains(text, "brandnew.md") {
		t.Errorf("after the commit only the untracked file is left:\n%s", text)
	}
}

// TestDiscardingTakesTwoPresses: x on a change asks, and only a second x
// puts the file back. An untracked file is refused, since git has no copy of
// it. If it regresses, one stray key loses an agent's work.
func TestDiscardingTakesTwoPresses(t *testing.T) {
	dir := repo(t)
	m := New(dir, nil)
	keys(m, "3")
	screen(m, 50, 14)
	pick := func(path string) {
		for i, r := range m.changeRows {
			if r.change.Path == path {
				m.cursor[ViewChanges] = i
			}
		}
	}
	pick("src/changed.go")
	keys(m, "x")
	if data, _ := os.ReadFile(filepath.Join(dir, "src/changed.go")); !strings.Contains(string(data), "var x") {
		t.Fatal("one x discarded the change")
	}
	keys(m, "j", "x")
	if data, _ := os.ReadFile(filepath.Join(dir, "src/changed.go")); !strings.Contains(string(data), "var x") {
		t.Fatal("an x after another key discarded the change")
	}
	pick("src/changed.go")
	keys(m, "x", "x")
	if data, _ := os.ReadFile(filepath.Join(dir, "src/changed.go")); strings.Contains(string(data), "var x") {
		t.Errorf("two x's should discard: %s (%q)", data, m.message)
	}
	pick("brandnew.md")
	keys(m, "x", "x")
	if _, err := os.Stat(filepath.Join(dir, "brandnew.md")); err != nil {
		t.Errorf("an untracked file must not be deleted: %v", err)
	}
}

// TestSearchFindsAFileByPartOfItsName: "/" and a few letters in order find
// a file anywhere in the project, its own name first; tab shows it in the
// tree. If it regresses, finding a file means opening directories one by
// one.
func TestSearchFindsAFileByPartOfItsName(t *testing.T) {
	dir := repo(t)
	write(t, dir, "deep/er/still/target_file.go", "x\n")
	write(t, dir, "tgt.txt", "x\n")
	opener := &recordingOpener{}
	m := New(dir, opener)
	keys(m, "/", "t", "a", "r", "g")
	text := screen(m, 50, 14)
	if !strings.Contains(text, "› targ") || !strings.Contains(text, "target_file.go deep/er/still") {
		t.Fatalf("search:\n%s", text)
	}
	if strings.Contains(text, "tgt.txt") {
		t.Errorf("tgt.txt does not have t-a-r-g in order:\n%s", text)
	}
	keys(m, "tab")
	if n := m.selectedNode(); n == nil || n.Rel != "deep/er/still/target_file.go" {
		t.Fatalf("tab should reveal the file in the tree, cursor on %+v", n)
	}
	keys(m, "/", "k", "e", "p", "t", "enter")
	if len(opener.opened) != 1 || opener.opened[0] != filepath.Join(dir, "kept.txt") {
		t.Errorf("enter opens the match: %v", opener.opened)
	}
	// Ignored files are not offered.
	keys(m, "/", "o", "u", "t", ".", "b")
	if len(m.search.results) != 0 {
		t.Errorf("an ignored file was offered: %v", m.search.results)
	}
}

// TestParseReadsKeysAndClicks: the terminal's bytes become keys and clicks,
// and a sequence cut between two reads is kept for the next. If it
// regresses, an arrow key typed fast arrives as escape, [ and A.
func TestParseReadsKeysAndClicks(t *testing.T) {
	events, rest := Parse([]byte("j\x1b[A\x1b[<0;5;3M\x1b[<32;6;3M\x1b[<0;5;3m\x1b[<65;1;1M\r\x7fé\x1b[5~\x1b[<0;1"))
	var names []string
	for _, ev := range events {
		switch e := ev.(type) {
		case Key:
			names = append(names, e.Name)
		case Mouse:
			switch {
			case e.Wheel != 0:
				names = append(names, "wheel"+string(rune('0'+e.Wheel+1)))
			case e.Press:
				names = append(names, "press@"+string(rune('0'+e.X))+","+string(rune('0'+e.Y)))
			case e.Release:
				names = append(names, "release")
			case e.Drag:
				names = append(names, "drag@"+string(rune('0'+e.X)))
			}
		}
	}
	want := "j up press@4,2 drag@5 release wheel2 enter backspace é pgup"
	if got := strings.Join(names, " "); got != want {
		t.Errorf("events = %q, want %q", got, want)
	}
	if string(rest) != "\x1b[<0;1" {
		t.Errorf("rest = %q", rest)
	}
}

// TestClicksSelectOpenAndSwitch: a click selects, a second click on the
// same row opens, a click on a directory opens it, and the header's names
// switch the view. If it regresses, the panel is keyboard-only in a program
// people drive with the mouse.
func TestClicksSelectOpenAndSwitch(t *testing.T) {
	dir := repo(t)
	opener := &recordingOpener{}
	m := New(dir, opener)
	clock := time.Unix(1000, 0)
	m.now = func() time.Time { return clock }
	screen(m, 40, 12)
	row := func(name string) int {
		for i, n := range m.fileRows {
			if n.Name == name {
				return 2 + i - m.scroll[ViewFiles]
			}
		}
		t.Fatalf("%s not listed", name)
		return 0
	}
	click(m, 5, row("kept.txt"))
	if m.selectedNode().Name != "kept.txt" || len(opener.opened) != 0 {
		t.Fatalf("one click selects: %s %v", m.selectedNode().Name, opener.opened)
	}
	clock = clock.Add(100 * time.Millisecond)
	click(m, 5, row("kept.txt"))
	if len(opener.opened) != 1 {
		t.Errorf("a double click opens: %v", opener.opened)
	}
	click(m, 5, row("src"))
	screen(m, 40, 12)
	if !strings.Contains(screen(m, 40, 12), "changed.go") {
		t.Errorf("a click opens a directory")
	}
	m.Mouse(Mouse{X: 20, Y: 0, Press: true})
	if m.view != ViewChanges {
		t.Errorf("clicking changes switches to it")
	}
}

// TestTheSessionOpenerOpensATabAndGoesBackToIt: the editor is started in a
// tab of its own in the panel's space, closing with the editor, and a file
// already open is gone back to rather than opened twice. If it regresses,
// every click on a file stacks another editor on the same file.
func TestTheSessionOpenerOpensATabAndGoesBackToIt(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "api.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	type call struct {
		Method string         `json:"method"`
		Params map[string]any `json:"params"`
	}
	calls := make(chan call, 16)
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			line, _ := bufio.NewReader(conn).ReadBytes('\n')
			var c call
			_ = json.Unmarshal(line, &c)
			calls <- c
			var result any = map[string]any{}
			switch c.Method {
			case "pane.layout":
				result = map[string]any{"layout": map[string]any{"workspace_id": "w_3"}}
			case "tab.create":
				result = map[string]any{"root_pane": map[string]any{"pane_id": "p_9"}}
			}
			reply, _ := json.Marshal(map[string]any{"id": "x", "result": result})
			_, _ = conn.Write(append(reply, '\n'))
			conn.Close()
		}
	}()

	o := &SessionOpener{Socket: sock, Pane: "p_1", Editor: "nvim -p"}
	if err := o.Open("/work/a b.go", 0); err != nil {
		t.Fatal(err)
	}
	var got []call
	for len(calls) > 0 {
		got = append(got, <-calls)
	}
	if len(got) != 3 || got[0].Method != "pane.layout" || got[1].Method != "tab.create" || got[2].Method != "pane.focus" {
		t.Fatalf("calls = %+v", got)
	}
	create := got[1].Params
	cmd, _ := create["command"].([]any)
	if create["workspace_id"] != "w_3" || create["close_on_exit"] != true || len(cmd) != 5 ||
		cmd[2] != `nvim -p "$1"` || cmd[4] != "/work/a b.go" || create["name"] != "a b.go" {
		t.Errorf("tab.create = %+v", create)
	}
	if got[2].Params["pane_id"] != "p_9" {
		t.Errorf("focus = %+v", got[2].Params)
	}

	if err := o.Open("/work/a b.go", 0); err != nil {
		t.Fatal(err)
	}
	if c := <-calls; c.Method != "pane.focus" || c.Params["pane_id"] != "p_9" || len(calls) != 0 {
		t.Errorf("a file already open is gone back to: %+v (then %d more)", c, len(calls))
	}

	if err := (&SessionOpener{}).Open("/x", 0); err == nil || !strings.Contains(err.Error(), "not running in a tend pane") {
		t.Errorf("outside a pane: %v", err)
	}
}

// TestOutsideARepositoryThePanelIsATree: a directory git knows nothing of
// is still a tree, and the changes view says why it is empty. If it
// regresses, the panel refuses to open in a plain directory.
func TestOutsideARepositoryThePanelIsATree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("needs git")
	}
	dir := t.TempDir()
	write(t, dir, "notes.txt", "x")
	m := New(dir, nil)
	if text := screen(m, 40, 10); !strings.Contains(text, "notes.txt") {
		t.Errorf("tree:\n%s", text)
	}
	keys(m, "3")
	if text := screen(m, 40, 10); !strings.Contains(text, "no git repository here") {
		t.Errorf("changes:\n%s", text)
	}
	keys(m, "/", "n", "o", "t")
	if len(m.search.results) != 1 {
		t.Errorf("search walks the directory: %v", m.search.results)
	}
}
