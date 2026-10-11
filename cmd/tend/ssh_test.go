//go:build unix

package main

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/auth-com-br/tend/internal/pty"
	"github.com/auth-com-br/tend/internal/sshconn"
	"github.com/auth-com-br/tend/internal/vt"
)

// TestAnSSHConnectionIsSavedOpenedLoggedInAndClosed is the SSH list from end
// to end: the button beside "menu" shows the connections, "new" saves one
// with a password, a click opens it in a space of its own with ssh given the
// password by tend, q at "connect again" closes the space, and delete
// forgets it. ssh here is a stand-in that asks SSH_ASKPASS as the real one
// does and prints what it was told. If it regresses, the saved connections
// are a list that does not log anybody in.
func TestAnSSHConnectionIsSavedOpenedLoggedInAndClosed(t *testing.T) {
	bin, store := t.TempDir(), filepath.Join(t.TempDir(), "ssh.toml")
	fake := `#!/bin/sh
pw=""
if [ -n "$SSH_ASKPASS" ]; then pw=$("$SSH_ASKPASS" "root@old.box's password: "); fi
echo "FAKE SSH $* PASSWORD:$pw"
`
	if err := os.WriteFile(filepath.Join(bin, "ssh"), []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	// The server here runs in the test, with no TEND_BIN_PATH for its panes,
	// so the tend ssh asks is the one on PATH: this build.
	if err := os.Symlink(tendBinary(t), filepath.Join(bin, "tend")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv(sshconn.PathEnv, store)

	a := startSession(t, 100, 30)
	a.waitForScreen(t, "a pane", func(s string) bool { return strings.Contains(s, "┌") })

	y := a.lineContaining(t, "⇄ ssh")
	a.clickAt(t, columnOfString(a.lines()[y-1], "⇄ ssh")+2, y)
	a.waitForScreen(t, "the ssh list", func(string) bool {
		return strings.Contains(a.sidebarText(), "no connections") && strings.Contains(a.sidebarText(), "⇄ dev")
	})

	a.newSSHConnection(t)
	// The form opens on the host; then port, user and password.
	a.send(t, "old.box\t2222\troot\thunter2\r")
	a.waitForScreen(t, "the connection listed", func(string) bool {
		side := a.sidebarText()
		return strings.Contains(side, "old.box") && strings.Contains(side, "root@old.box:2222")
	})
	info, err := os.Stat(store)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("the connections file is %v, %v; want 0600", info, err)
	}

	a.clickAt(t, 5, a.lineContaining(t, "○ old.box"))
	a.waitForScreen(t, "ssh logged in by tend", func(s string) bool {
		return strings.Contains(s, "FAKE SSH -p 2222 -l root") && strings.Contains(s, "PASSWORD:hunter2") &&
			strings.Contains(s, "disconnected")
	})
	if !strings.Contains(a.sidebarText(), "● old.box") {
		t.Errorf("an open connection should be marked:\n%s", a.sidebarText())
	}

	// q at "connect again" closes the connection's space.
	a.send(t, "q\r")
	a.waitForScreen(t, "the space closed", func(string) bool { return strings.Contains(a.sidebarText(), "○ old.box") })

	a.openMenuOn(t, 5, a.lineContaining(t, "○ old.box"), "copy ssh command")
	a.clickAt(t, 7, a.lineContaining(t, "delete"))
	a.waitForScreen(t, "asked again", func(s string) bool { return strings.Contains(s, "yes, delete it") })
	a.clickAt(t, 7, a.lineContaining(t, "yes, delete it"))
	a.waitForScreen(t, "the connection gone", func(string) bool { return strings.Contains(a.sidebarText(), "no connections") })
	if back, err := sshconn.Load(store); err != nil || len(back.Connections) != 0 {
		t.Errorf("the file still holds %+v, %v", back.Connections, err)
	}

	// And back to the spaces.
	y = a.lineContaining(t, "⇄ dev")
	a.clickAt(t, columnOfString(a.lines()[y-1], "⇄ dev")+2, y)
	a.waitForScreen(t, "the spaces again", func(string) bool { return strings.Contains(a.sidebarText(), "spaces") })
}

// TestAskpassGivesThePasswordToAPasswordPromptOnly runs tend as ssh runs its
// SSH_ASKPASS: with the question as the only argument. If it regresses, a
// saved password is never given, or is given to a one-time code prompt.
func TestAskpassGivesThePasswordToAPasswordPromptOnly(t *testing.T) {
	store := filepath.Join(t.TempDir(), "ssh.toml")
	var s sshconn.Store
	c := s.Put(sshconn.Connection{Name: "box", Host: "old.box", Password: "hunter2"})
	if err := sshconn.Save(store, s); err != nil {
		t.Fatal(err)
	}
	ask := func(id, prompt string) (string, error) {
		cmd := exec.Command(tendBinary(t), prompt)
		cmd.Env = append(os.Environ(), sshconn.PathEnv+"="+store, sshconn.AskpassEnv+"="+id)
		out, err := cmd.Output()
		return string(out), err
	}
	if out, err := ask(c.ID, "root@old.box's password: "); err != nil || out != "hunter2\n" {
		t.Errorf("a password prompt got %q, %v", out, err)
	}
	if out, err := ask(c.ID, "Verification code: "); err == nil || out != "" {
		t.Errorf("a code prompt got %q, %v; it should be refused", out, err)
	}
	if _, err := ask("gone", "password: "); err == nil {
		t.Error("a connection no longer saved was answered")
	}
}

// tendBinary builds this tend, for the tests that need it run as ssh runs
// it rather than in process.
func tendBinary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "tend")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("building: %v\n%s", err, out)
	}
	return bin
}

// TestAConnectionJoinsAGroupThatExists: the form offers the groups there
// are, a click on one files the new connection under it, and "move to
// group..." moves one already saved. If it regresses, adding a login to a
// group means typing its name again exactly, which the owner could not see
// how to do.
func TestAConnectionJoinsAGroupThatExists(t *testing.T) {
	store := filepath.Join(t.TempDir(), "ssh.toml")
	var s sshconn.Store
	s.Put(sshconn.Connection{Name: "web1", Group: "acme", Host: "web1.acme"})
	s.Put(sshconn.Connection{Name: "vps", Host: "vps.example"})
	if err := sshconn.Save(store, s); err != nil {
		t.Fatal(err)
	}
	t.Setenv(sshconn.PathEnv, store)

	a := startSession(t, 100, 30)
	a.waitForScreen(t, "a pane", func(s string) bool { return strings.Contains(s, "┌") })
	y := a.lineContaining(t, "⇄ ssh")
	a.clickAt(t, columnOfString(a.lines()[y-1], "⇄ ssh")+2, y)
	a.waitForScreen(t, "the group", func(string) bool { return strings.Contains(a.sidebarText(), "▼ acme") })

	// From the form: on to the group field, and a click on the group offered.
	a.newSSHConnection(t)
	a.send(t, "\x1b[Z")
	a.waitForScreen(t, "the groups offered", func(s string) bool { return strings.Contains(s, "groups: [acme]") })
	y = a.lineContaining(t, "groups: [acme]")
	a.clickAt(t, columnOfString(a.lines()[y-1], "[acme]")+2, y)
	a.send(t, "\tweb2.acme\r")
	a.waitForScreen(t, "web2 under acme", func(string) bool {
		side := a.sidebarText()
		return strings.Contains(side, "web2.acme") && strings.Index(side, "▼ acme") < strings.Index(side, "web2.acme") &&
			strings.Contains(side, "    ○ web2.acme")
	})

	// From the menu: vps into acme.
	a.openMenuOn(t, 5, a.lineContaining(t, "○ vps"), "move to group...")
	a.clickAt(t, 7, a.lineContaining(t, "move to group..."))
	a.waitForScreen(t, "the groups to move to", func(s string) bool { return strings.Contains(s, "move vps to") })
	y = a.lineContaining(t, "move vps to") + 1
	a.clickAt(t, columnOfString(a.lines()[y-1], "acme")+2, y)
	a.waitForScreen(t, "vps under acme", func(string) bool { return strings.Contains(a.sidebarText(), "    ○ vps") })

	back, err := sshconn.Load(store)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range back.Connections {
		if c.Group != "acme" {
			t.Errorf("%s is in %q, want acme", c.Name, c.Group)
		}
	}
}

// newSSHConnection opens the form for a new connection from the ssh list's
// "new", which asks first whether it is a connection or a group.
func (a *attached) newSSHConnection(t *testing.T) {
	t.Helper()
	a.clickAt(t, 3, a.lineContaining(t, "⇄ dev"))
	a.waitForScreen(t, "the new menu", func(s string) bool { return strings.Contains(s, "new connection...") })
	y := a.lineContaining(t, "new connection...")
	a.clickAt(t, columnOfString(a.lines()[y-1], "new connection...")+2, y)
	a.waitForScreen(t, "the form", func(s string) bool { return strings.Contains(s, "new ssh connection") })
}

// TestAGroupIsMadeFirstAndFilledAfter: "new" makes a group with nothing in
// it yet, a right-click on it puts a connection in it, and it can be renamed
// and deleted — the connections staying. If it regresses, there is nowhere
// to make a group: the owner looked for one and found none.
func TestAGroupIsMadeFirstAndFilledAfter(t *testing.T) {
	store := filepath.Join(t.TempDir(), "ssh.toml")
	t.Setenv(sshconn.PathEnv, store)

	a := startSession(t, 100, 30)
	a.waitForScreen(t, "a pane", func(s string) bool { return strings.Contains(s, "┌") })
	y := a.lineContaining(t, "⇄ ssh")
	a.clickAt(t, columnOfString(a.lines()[y-1], "⇄ ssh")+2, y)
	a.waitForScreen(t, "the ssh list", func(string) bool { return strings.Contains(a.sidebarText(), "⇄ dev") })

	a.clickAt(t, 3, a.lineContaining(t, "⇄ dev"))
	a.waitForScreen(t, "the new menu", func(s string) bool { return strings.Contains(s, "new group...") })
	y = a.lineContaining(t, "new group...")
	a.clickAt(t, columnOfString(a.lines()[y-1], "new group...")+2, y)
	a.waitForScreen(t, "the name asked", func(s string) bool { return strings.Contains(s, "new ssh group") })
	a.send(t, "acme\r")
	a.waitForScreen(t, "the group, empty", func(string) bool {
		side := a.sidebarText()
		return strings.Contains(side, "▼ acme") && strings.Contains(side, "empty")
	})

	a.openMenuOn(t, 4, a.lineContaining(t, "▼ acme"), "new connection here...")
	a.clickAt(t, 6, a.lineContaining(t, "new connection here..."))
	a.waitForScreen(t, "the form", func(s string) bool { return strings.Contains(s, "new ssh connection") })
	a.send(t, "web.acme\r")
	a.waitForScreen(t, "web in acme", func(string) bool {
		side := a.sidebarText()
		return strings.Contains(side, "    ○ web.acme") && !strings.Contains(side, "empty")
	})

	a.openMenuOn(t, 4, a.lineContaining(t, "▼ acme"), "rename group...")
	a.clickAt(t, 6, a.lineContaining(t, "rename group..."))
	a.waitForScreen(t, "the rename", func(s string) bool { return strings.Contains(s, "rename ssh group") })
	a.send(t, "\x15clients\r")
	a.waitForScreen(t, "renamed", func(string) bool {
		side := a.sidebarText()
		return strings.Contains(side, "▼ clients") && strings.Contains(side, "    ○ web.acme")
	})

	a.openMenuOn(t, 4, a.lineContaining(t, "▼ clients"), "delete group")
	a.clickAt(t, 6, a.lineContaining(t, "delete group"))
	a.waitForScreen(t, "the group gone, web kept", func(string) bool {
		side := a.sidebarText()
		return !strings.Contains(side, "clients") && strings.Contains(side, " ○ web.acme")
	})
	back, err := sshconn.Load(store)
	if err != nil || len(back.Connections) != 1 || len(back.Groups()) != 0 {
		t.Errorf("the file: %+v, groups %q, %v", back.Connections, back.Groups(), err)
	}
}

// TestAConnectionIsDraggedIntoAGroup: a connection pressed and dragged onto
// a group's heading is filed in that group, one dragged onto a connection in
// none leaves its group, and a press that only shakes over its own row is
// still a click that opens it. If it regresses, filing the logins already
// saved means editing each one, which the owner asked not to.
func TestAConnectionIsDraggedIntoAGroup(t *testing.T) {
	bin, store := t.TempDir(), filepath.Join(t.TempDir(), "ssh.toml")
	if err := os.WriteFile(filepath.Join(bin, "ssh"), []byte("#!/bin/sh\necho \"FAKE SSH $*\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	var s sshconn.Store
	if err := s.AddGroup("acme"); err != nil {
		t.Fatal(err)
	}
	s.Put(sshconn.Connection{Name: "vps", Host: "vps.example"})
	s.Put(sshconn.Connection{Name: "db", Host: "db.example"})
	if err := sshconn.Save(store, s); err != nil {
		t.Fatal(err)
	}
	t.Setenv(sshconn.PathEnv, store)

	a := startSession(t, 100, 30)
	a.waitForScreen(t, "a pane", func(s string) bool { return strings.Contains(s, "┌") })
	y := a.lineContaining(t, "⇄ ssh")
	a.clickAt(t, columnOfString(a.lines()[y-1], "⇄ ssh")+2, y)
	a.waitForScreen(t, "the list", func(string) bool { return strings.Contains(a.sidebarText(), "▼ acme") })

	a.dragFromTo(t, 0, 5, a.lineContaining(t, "○ vps"), 5, a.lineContaining(t, "▼ acme"))
	a.waitForScreen(t, "vps in acme", func(string) bool { return strings.Contains(a.sidebarText(), "    ○ vps") })

	// Back out, onto db, which is in no group.
	a.dragFromTo(t, 0, 7, a.lineContaining(t, "○ vps"), 5, a.lineContaining(t, "○ db"))
	a.waitForScreen(t, "vps out of acme", func(string) bool {
		side := a.sidebarText()
		return strings.Contains(side, "  ○ vps") && !strings.Contains(side, "    ○ vps")
	})

	// A press that drifts to the line under the name is still a click.
	dy := a.lineContaining(t, "○ db")
	a.dragFromTo(t, 0, 5, dy, 9, dy+1)
	a.waitForScreen(t, "db opened", func(s string) bool { return strings.Contains(s, "FAKE SSH -- db.example") })

	back, err := sshconn.Load(store)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range back.Connections {
		if c.Group != "" {
			t.Errorf("%s is in %q, want none", c.Name, c.Group)
		}
	}
}

// TestTheSpacesListLeavesTheConnectionsOut: a connection's space is the ssh
// list's, so the spaces list shows neither it nor the "ssh" group it is
// kept in, and a client started after it was opened still knows it is open.
// If it regresses, the spaces list carries an "ssh" group the owner asked to
// be rid of, or reopening tend opens the connection a second time.
func TestTheSpacesListLeavesTheConnectionsOut(t *testing.T) {
	bin, store := t.TempDir(), filepath.Join(t.TempDir(), "ssh.toml")
	if err := os.WriteFile(filepath.Join(bin, "ssh"), []byte("#!/bin/sh\necho FAKE SSH; exec sleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	var s sshconn.Store
	s.Put(sshconn.Connection{Name: "box", Host: "box.example"})
	if err := sshconn.Save(store, s); err != nil {
		t.Fatal(err)
	}
	t.Setenv(sshconn.PathEnv, store)

	a := startSession(t, 100, 30)
	a.waitForScreen(t, "a pane", func(s string) bool { return strings.Contains(s, "┌") })
	y := a.lineContaining(t, "⇄ ssh")
	a.clickAt(t, columnOfString(a.lines()[y-1], "⇄ ssh")+2, y)
	a.waitForScreen(t, "the list", func(string) bool { return strings.Contains(a.sidebarText(), "○ box") })
	a.clickAt(t, 5, a.lineContaining(t, "○ box"))
	a.waitForScreen(t, "connected", func(s string) bool { return strings.Contains(s, "FAKE SSH") })

	y = a.lineContaining(t, "⇄ dev")
	a.clickAt(t, columnOfString(a.lines()[y-1], "⇄ dev")+2, y)
	a.waitForScreen(t, "the spaces", func(string) bool { return strings.Contains(a.sidebarText(), "main") })
	if side := a.sidebarText(); strings.Contains(side, "ssh\n") || strings.Contains(side, "▼ ssh") || strings.Contains(side, "box") {
		t.Errorf("the spaces list shows the connection or its group:\n%s", side)
	}

	// A new client, which never saw the connection opened, still marks it
	// open and goes to it rather than opening it again.
	a.send(t, "\x02d")
	b := attachAgain(t, 100, 30)
	b.waitForScreen(t, "the session again", func(string) bool { return strings.Contains(b.sidebarText(), "⇄ ssh") })
	y = b.lineContaining(t, "⇄ ssh")
	b.clickAt(t, columnOfString(b.lines()[y-1], "⇄ ssh")+2, y)
	b.waitForScreen(t, "box marked open", func(string) bool { return strings.Contains(b.sidebarText(), "● box") })
}

// attachAgain is another client on the session startSession made, as
// running tend again is.
func attachAgain(t *testing.T, cols, rows int) *attached {
	t.Helper()
	p, err := pty.Start(buildBinary(t), []string{"attach", "-s", "tui"}, pty.Options{
		Size: pty.Size{Cols: uint16(cols), Rows: uint16(rows)},
		Env:  append(os.Environ(), "SHELL=/bin/sh", "TERM=xterm-256color"),
	})
	if err != nil {
		t.Fatalf("attaching again: %v", err)
	}
	a := &attached{pty: p, screen: vt.NewScreen(cols, rows, 200)}
	go func() { _, _ = io.Copy(a, p) }()
	t.Cleanup(func() { _ = p.Close() })
	return a
}
