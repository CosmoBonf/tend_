// Package sshconn keeps the SSH connections of the sidebar's ssh list: a
// name, a group to file it under, where it goes and as whom, and the
// password for a machine that takes no key.
//
// It is tend's own, after Ásbrú Connection Manager, which the owner used: a
// tree of saved connections, each opened logged in with a click. herdr has
// nothing of the kind.
//
// The password is kept in the file, readable by the user alone, because the
// owner chose that over the Keychain. It reaches ssh through SSH_ASKPASS —
// ssh asks this program for it, and is told — rather than being typed at
// the prompt by watching the screen, which is how Ásbrú does it and which
// breaks on the first prompt worded differently.
package sshconn

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
)

// Connection is one saved connection.
type Connection struct {
	ID    string `toml:"id"`
	Name  string `toml:"name"`
	Group string `toml:"group,omitempty"`
	Host  string `toml:"host"`
	// Port is 0 for ssh's own default, which ~/.ssh/config may change.
	Port int    `toml:"port,omitempty"`
	User string `toml:"user,omitempty"`
	// Password is for a machine that takes no key; empty leaves the login
	// to ssh, which uses the keys and agent it always does.
	Password string `toml:"password,omitempty"`
}

// Store is the file's contents.
type Store struct {
	// GroupNames are the groups made as such, empty ones too: a group is a
	// folder made first and filled after, as in Ásbrú, not only a name
	// connections happen to share.
	GroupNames  []string     `toml:"groups,omitempty"`
	Connections []Connection `toml:"connection"`
}

// PathEnv overrides where the connections are kept, as TEND_CONFIG does the
// settings: for tests, which must not read or write the user's.
const PathEnv = "TEND_SSH_CONNECTIONS"

// Path is the file the connections are kept in, beside tend's settings.
func Path() (string, error) {
	if override := os.Getenv(PathEnv); override != "" {
		return override, nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("sshconn: finding the config directory: %w", err)
	}
	return filepath.Join(dir, "tend", "ssh.toml"), nil
}

// Load reads the connections; a file that is not there is none yet.
func Load(path string) (Store, error) {
	var s Store
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	if _, err := toml.Decode(string(data), &s); err != nil {
		return s, fmt.Errorf("sshconn: %s: %w", path, err)
	}
	return s, nil
}

// Save writes the connections, readable by the user alone since passwords
// are in it, through a temporary file renamed over the old one: a write
// cut short must not leave half a file and lose every connection.
func Save(path string, s Store) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	var buf bytes.Buffer
	buf.WriteString("# tend's SSH connections (the sidebar's ssh list). Passwords are kept\n# here as written: this file is readable by you alone.\n\n")
	if err := toml.NewEncoder(&buf).Encode(s); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".ssh-*.toml")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(buf.Bytes()); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// NewID is a connection's identifier: what the sidebar and the password
// prompt know it by, which a rename does not change.
func NewID() string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// Find is the connection with an identifier.
func (s Store) Find(id string) (Connection, bool) {
	for _, c := range s.Connections {
		if c.ID == id {
			return c, true
		}
	}
	return Connection{}, false
}

// Put adds a connection, or replaces the one with its identifier where it
// stands, giving a new one an identifier first. It returns the connection
// as kept.
func (s *Store) Put(c Connection) Connection {
	if c.ID == "" {
		c.ID = NewID()
	}
	for i := range s.Connections {
		if s.Connections[i].ID == c.ID {
			s.Connections[i] = c
			return c
		}
	}
	s.Connections = append(s.Connections, c)
	return c
}

// Remove forgets a connection.
func (s *Store) Remove(id string) {
	s.Connections = slices.DeleteFunc(s.Connections, func(c Connection) bool { return c.ID == id })
}

// Groups are the groups there are: those made, in the order they were
// made, then any a connection names that was not made as one (a file
// written by hand, or before groups were kept). Connections in none come
// before them all in the list.
func (s Store) Groups() []string {
	out := slices.Clone(s.GroupNames)
	for _, c := range s.Connections {
		if c.Group != "" && !slices.Contains(out, c.Group) {
			out = append(out, c.Group)
		}
	}
	return out
}

// AddGroup makes a group, empty. It reports what is wrong with the name.
func (s *Store) AddGroup(name string) error {
	name = strings.TrimSpace(name)
	switch {
	case name == "":
		return errors.New("a group needs a name")
	case slices.Contains(s.Groups(), name):
		return fmt.Errorf("there is a group %s already", name)
	}
	s.GroupNames = append(s.GroupNames, name)
	return nil
}

// RenameGroup renames a group, and moves its connections with it.
func (s *Store) RenameGroup(from, to string) error {
	to = strings.TrimSpace(to)
	switch {
	case to == "":
		return errors.New("a group needs a name")
	case to == from:
		return nil
	case slices.Contains(s.Groups(), to):
		return fmt.Errorf("there is a group %s already", to)
	}
	if i := slices.Index(s.GroupNames, from); i >= 0 {
		s.GroupNames[i] = to
	} else {
		s.GroupNames = append(s.GroupNames, to)
	}
	for i := range s.Connections {
		if s.Connections[i].Group == from {
			s.Connections[i].Group = to
		}
	}
	return nil
}

// RemoveGroup forgets a group. Its connections are kept, in no group:
// throwing a folder away is not throwing away the logins in it.
func (s *Store) RemoveGroup(name string) {
	s.GroupNames = slices.DeleteFunc(s.GroupNames, func(g string) bool { return g == name })
	for i := range s.Connections {
		if s.Connections[i].Group == name {
			s.Connections[i].Group = ""
		}
	}
}

// Clone is a copy that changing does not change this one, to go back to
// when a change cannot be saved.
func (s Store) Clone() Store {
	return Store{GroupNames: slices.Clone(s.GroupNames), Connections: slices.Clone(s.Connections)}
}

// Normalize trims what was typed and fills in what can be: a connection
// with no name is called after its host. It reports what is wrong with one
// that cannot be saved.
func Normalize(c Connection) (Connection, error) {
	c.Name = strings.TrimSpace(c.Name)
	c.Group = strings.TrimSpace(c.Group)
	c.Host = strings.TrimSpace(c.Host)
	c.User = strings.TrimSpace(c.User)
	// A host typed as user@host, as on the command line, is taken apart
	// rather than refused.
	if user, host, ok := strings.Cut(c.Host, "@"); ok && c.User == "" {
		c.User, c.Host = user, host
	}
	switch {
	case c.Host == "":
		return c, errors.New("a host is needed")
	case strings.ContainsAny(c.Host, " \t@") || strings.HasPrefix(c.Host, "-"):
		return c, fmt.Errorf("%q is not a host", c.Host)
	case strings.ContainsAny(c.User, " \t@") || strings.HasPrefix(c.User, "-"):
		return c, fmt.Errorf("%q is not a user", c.User)
	case c.Port < 0 || c.Port > 65535:
		return c, fmt.Errorf("port %d is not one", c.Port)
	}
	if c.Name == "" {
		c.Name = c.Host
	}
	return c, nil
}

// ParsePort reads a port as typed: empty is ssh's default.
func ParsePort(text string) (int, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(text)
	if err != nil || n < 1 || n > 65535 {
		return 0, fmt.Errorf("port %q is not one", text)
	}
	return n, nil
}

// Target is where a connection goes, as people write it: user@host:port.
func (c Connection) Target() string {
	t := c.Host
	if c.User != "" {
		t = c.User + "@" + t
	}
	if c.Port != 0 {
		t += ":" + strconv.Itoa(c.Port)
	}
	return t
}

// AskpassEnv names the connection whose password a tend started by ssh as
// its SSH_ASKPASS is to give. Set on ssh alone, so nothing else started in
// the pane is taken for a password prompt.
const AskpassEnv = "TEND_SSH_ASKPASS"

// Args are ssh's arguments for a connection, after "ssh".
//
// With a password, ssh asks for it through SSH_ASKPASS and there is no
// terminal to ask anything else on, so two answers are given up front: a
// machine never seen is trusted on first sight (accept-new — a key that
// changed is still refused, which is the case that matters), and a
// password refused once is not offered twice more.
//
// The keys are still tried first, as ssh always does: a key the machine
// knows logs in even when the password saved is wrong, and the password is
// what is left when no key will do. A key with a passphrase asks for it
// through askpass too, is refused there (Answer), and ssh moves on.
func (c Connection) Args() []string {
	var args []string
	if c.Port != 0 {
		args = append(args, "-p", strconv.Itoa(c.Port))
	}
	if c.User != "" {
		args = append(args, "-l", c.User)
	}
	if c.Password != "" {
		args = append(args,
			"-o", "StrictHostKeyChecking=accept-new",
			"-o", "NumberOfPasswordPrompts=1")
	}
	// The host last, after "--", so a host that looked like an option could
	// never be read as one.
	return append(args, "--", c.Host)
}

// script is what a connection's pane runs: ssh, and when it ends, the
// choice Ásbrú offers — connect again, or close. With a password, ssh alone
// is started with SSH_ASKPASS pointing at tend and forced, so it asks tend
// even with a terminal there, and with the connection to answer for: the
// shell's "VAR=value command" sets them for that one command, so nothing
// else ever finds them.
const script = `name=$1 id=$2
shift 2
while :; do
	if [ -n "$id" ]; then
		SSH_ASKPASS="${TEND_BIN_PATH:-tend}" SSH_ASKPASS_REQUIRE=force ` + AskpassEnv + `="$id" ssh "$@"
	else
		ssh "$@"
	fi
	printf '\n[tend] %s: disconnected. enter connects again, q closes: ' "$name"
	IFS= read -r answer || exit 0
	case $answer in q|Q) exit 0 ;; esac
done`

// PaneCommand is what a pane runs to be a connection.
func (c Connection) PaneCommand() []string {
	id := ""
	if c.Password != "" {
		id = c.ID
	}
	return append([]string{"/bin/sh", "-c", script, "tend-ssh", c.Name, id}, c.Args()...)
}

// Command is how the connection would be typed at a shell, for copying.
// The password is not in it: there is no way to give one on the command
// line, which is the point of SSH_ASKPASS.
func (c Connection) Command() string {
	parts := []string{"ssh"}
	for _, a := range c.Args() {
		parts = append(parts, shellQuote(a))
	}
	return strings.Join(parts, " ")
}

func shellQuote(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t\n'\"\\$`!*?[]{}()<>|&;#~") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// Answer is what tend says when ssh asks it something for a connection:
// the password, to a password prompt, and nothing to anything else — a
// one-time code or a passphrase is not the password, and answering one
// with it would spend a login attempt and say the password to the wrong
// question.
func Answer(c Connection, prompt string) (string, bool) {
	p := strings.ToLower(prompt)
	if c.Password == "" || !strings.Contains(p, "password") {
		return "", false
	}
	return c.Password, true
}
