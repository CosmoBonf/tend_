package sshconn

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestConnectionsAreKeptAndReadBack: what the form saves is what the list
// shows next time, in the order it was made, and the file holding passwords
// is the user's alone. If it regresses, saved connections vanish, or a
// password sits in a file anyone on the machine can read.
func TestConnectionsAreKeptAndReadBack(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tend", "ssh.toml")
	if s, err := Load(path); err != nil || len(s.Connections) != 0 {
		t.Fatalf("no file should be no connections: %+v %v", s, err)
	}
	var s Store
	web := s.Put(Connection{Name: "web", Group: "acme", Host: "10.0.0.5", Port: 2222, User: "deploy", Password: "s3cr'et"})
	db := s.Put(Connection{Name: "db", Group: "acme", Host: "db.acme"})
	s.Put(Connection{Name: "vps", Host: "vps.example"})
	if web.ID == "" || web.ID == db.ID {
		t.Fatalf("each connection needs its own id: %q %q", web.ID, db.ID)
	}
	if err := Save(path, s); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("the file is %v, want 0600: %v", info.Mode(), err)
	}

	back, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(back.Connections) != 3 || back.Connections[0] != web || back.Connections[1] != db {
		t.Fatalf("read back %+v", back.Connections)
	}
	if got := back.Groups(); !slices.Equal(got, []string{"acme"}) {
		t.Errorf("groups = %q", got)
	}

	// Editing keeps the place; removing forgets.
	web.Name = "web-01"
	back.Put(web)
	back.Remove(db.ID)
	if len(back.Connections) != 2 || back.Connections[0].Name != "web-01" {
		t.Errorf("after edit and remove: %+v", back.Connections)
	}
	if _, ok := back.Find(db.ID); ok {
		t.Error("a removed connection is still found")
	}
}

// TestWhatIsTypedIsTidiedOrRefused: a host typed as user@host is taken
// apart, a missing name is the host's, and what ssh would take for an
// option is refused. If it regresses, a host such as "-oProxyCommand=..."
// would run a command of its own the moment it was clicked.
func TestWhatIsTypedIsTidiedOrRefused(t *testing.T) {
	c, err := Normalize(Connection{Host: " deploy@web.acme ", Group: " acme "})
	if err != nil || c.User != "deploy" || c.Host != "web.acme" || c.Name != "web.acme" || c.Group != "acme" {
		t.Errorf("Normalize = %+v, %v", c, err)
	}
	for _, bad := range []Connection{
		{Host: ""},
		{Host: "-oProxyCommand=touch /tmp/x"},
		{Host: "a host"},
		{Host: "web", User: "-l root"},
		{Host: "web", Port: 70000},
	} {
		if _, err := Normalize(bad); err == nil {
			t.Errorf("Normalize(%+v) took it", bad)
		}
	}
	if p, err := ParsePort(""); p != 0 || err != nil {
		t.Errorf("an empty port is ssh's default: %d %v", p, err)
	}
	if _, err := ParsePort("ssh"); err == nil {
		t.Error("a port that is not a number was taken")
	}
}

// TestSSHIsToldWhatItNeeds: a key connection is plain ssh; a password one
// asks tend for the password and nothing it cannot answer, and the host
// always comes after "--". If it regresses, a password connection stops at
// a prompt nobody can see, a key connection is forced through a password it
// does not have, or a wrong saved password locks out a key that would work.
func TestSSHIsToldWhatItNeeds(t *testing.T) {
	key := Connection{ID: "k", Host: "web", User: "deploy", Port: 2222}
	if got := strings.Join(key.Args(), " "); got != "-p 2222 -l deploy -- web" {
		t.Errorf("key args = %q", got)
	}
	if cmd := key.PaneCommand(); cmd[5] != "" {
		t.Errorf("a key connection should not go through askpass: %q", cmd)
	}
	if got := key.Target(); got != "deploy@web:2222" {
		t.Errorf("target = %q", got)
	}

	pass := Connection{ID: "p1", Host: "old.box", User: "root", Password: "hunter2"}
	args := strings.Join(pass.Args(), " ")
	for _, want := range []string{"StrictHostKeyChecking=accept-new", "NumberOfPasswordPrompts=1", "-- old.box"} {
		if !strings.Contains(args, want) {
			t.Errorf("password args %q lack %q", args, want)
		}
	}
	// The keys are tried before the password: a key the machine knows logs
	// in even when the saved password is wrong.
	if strings.Contains(args, "PreferredAuthentications") {
		t.Errorf("password args %q keep ssh from trying the keys first", args)
	}
	cmd := pass.PaneCommand()
	if cmd[4] != pass.Name || cmd[5] != "p1" || !strings.Contains(cmd[2], "SSH_ASKPASS_REQUIRE=force") || slices.Contains(cmd, "hunter2") {
		t.Errorf("pane command = %q", cmd)
	}
	if cmd := pass.Command(); strings.Contains(cmd, "hunter2") || !strings.HasPrefix(cmd, "ssh ") {
		t.Errorf("the command to copy is %q; it must not carry the password", cmd)
	}
}

// TestOnlyAPasswordPromptIsAnswered: ssh asks askpass every question it
// has. If it regresses, a one-time code prompt is answered with the
// password, spending the login and saying it to the wrong question.
func TestOnlyAPasswordPromptIsAnswered(t *testing.T) {
	c := Connection{Password: "hunter2"}
	for prompt, want := range map[string]bool{
		"root@old.box's password: ":  true,
		"(root@old.box) Password: ":  true,
		"Verification code: ":        false,
		"Enter passphrase for key: ": false,
	} {
		if _, ok := Answer(c, prompt); ok != want {
			t.Errorf("Answer(%q) answered = %v, want %v", prompt, ok, want)
		}
	}
	if _, ok := Answer(Connection{}, "password: "); ok {
		t.Error("a connection with no password answered")
	}
}

// TestAGroupIsAFolderOfItsOwn: a group can be made empty and filled after,
// renamed with its connections, and thrown away without them. If it
// regresses, there is nowhere to make a group, or deleting one deletes the
// logins in it.
func TestAGroupIsAFolderOfItsOwn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ssh.toml")
	var s Store
	if err := s.AddGroup("acme"); err != nil {
		t.Fatal(err)
	}
	if err := s.AddGroup(" acme "); err == nil {
		t.Error("a second group of the same name was made")
	}
	if err := s.AddGroup(""); err == nil {
		t.Error("a group with no name was made")
	}
	if err := Save(path, s); err != nil {
		t.Fatal(err)
	}
	back, err := Load(path)
	if err != nil || !slices.Equal(back.Groups(), []string{"acme"}) {
		t.Fatalf("an empty group did not survive the file: %q %v", back.Groups(), err)
	}

	web := back.Put(Connection{Name: "web", Group: "acme", Host: "web"})
	back.Put(Connection{Name: "db", Group: "legacy", Host: "db"})
	if got := back.Groups(); !slices.Equal(got, []string{"acme", "legacy"}) {
		t.Errorf("groups = %q", got)
	}
	if err := back.RenameGroup("acme", "clients"); err != nil {
		t.Fatal(err)
	}
	if c, _ := back.Find(web.ID); c.Group != "clients" {
		t.Errorf("a renamed group left its connection in %q", c.Group)
	}
	before := back.Clone()
	back.RemoveGroup("clients")
	if c, ok := back.Find(web.ID); !ok || c.Group != "" {
		t.Errorf("removing the group should keep web, in none: %+v %v", c, ok)
	}
	if c, _ := before.Find(web.ID); c.Group != "clients" {
		t.Error("the clone changed with the store")
	}
}
