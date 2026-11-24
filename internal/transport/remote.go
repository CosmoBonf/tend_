package transport

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// A session on another machine is reached by running tend there and talking to
// it through the command that started it. Over ssh that means no port to open,
// no second authentication scheme and nothing listening on the network: the
// socket stays private to its machine, and ssh is the only door in.
//
// The remote end is `tend bridge`, which does nothing but join its stdin and
// stdout to the session's socket. Everything else is the same protocol over a
// different pipe, so a remote session is not a second kind of session.

// RemoteCommandEnv overrides the program used to reach another machine. It
// defaults to ssh; setting it lets a test stand in a local shell, and lets a
// user whose ssh is wrapped in something else say so.
const RemoteCommandEnv = "TEND_SSH"

// stderrLimit bounds how much of the remote side's complaint is kept. It is
// kept at all because "connection closed" says nothing, and the reason — tend
// not installed there, a host key refused — is on stderr.
const stderrLimit = 8 * 1024

// remoteCloseGrace is how long the remote command is given to end once its
// input is closed.
const remoteCloseGrace = 2 * time.Second

// RemoteTend is the tend to run on the far side: "tend" on its PATH unless
// the attach found, or put, one somewhere else (see cmd/tend's
// prepareRemote). It goes to the far side's shell as it is, so a path may
// use $HOME.
// Empty means the one tend installs itself as there (~/.local/bin/tend, as
// herdr prefers its own managed install) when it exists, and the one on the
// far side's PATH otherwise: a non-interactive ssh often has no
// ~/.local/bin on its PATH, and a tend put there by an attach would be
// invisible to every other -ssh command.
var RemoteTend = ""

// remoteTendWord is the far side's tend as a word for its shell.
func remoteTendWord() string {
	if RemoteTend != "" {
		return RemoteTend
	}
	return `"$( [ -x "$HOME/.local/bin/tend" ] && echo "$HOME/.local/bin/tend" || echo tend )"`
}

// sshOptions starts the ssh that carries a session, tuned for typing over
// a slow link — the owner's laptop on a VPN, where typing stuck:
//
//   - Compression: what goes back is a pane's whole screen each frame, about
//     6 KB of nearly the one before, and ssh's compression is one stream, so
//     each screen is sent as its difference from the last. Measured on the
//     owner's Claude pane while it worked: 47 KB/s, compressed 0.6 KB/s.
//   - IPQoS: with no terminal (-T) ssh marks its packets as bulk transfer
//     (TOS 8, seen with strace; cs1 on a newer ssh), which Wi-Fi puts in its
//     background queue behind everything else. A session is typed into: it is
//     marked interactive, as ssh marks one with a terminal.
func sshOptions() []string {
	return []string{"ssh", "-T", "-o", "Compression=yes", "-o", "IPQoS=af21"}
}

// RemoteArgv is the command that carries a session from host.
func RemoteArgv(host, session string) []string {
	program := os.Getenv(RemoteCommandEnv)
	if program == "" {
		// -T: no terminal on the far side. The protocol is binary, and a pty
		// in the middle would translate line endings into it.
		return append(sshOptions(), host, remoteTendWord(), "bridge", "-s", session)
	}
	// A wrapper is given the plain word: it may not run a shell at all.
	word := RemoteTend
	if word == "" {
		word = "tend"
	}
	return append(strings.Fields(program), host, word, "bridge", "-s", session)
}

// WatchArgv is RemoteArgv for a connection nobody is watching being made: a
// saved machine the client keeps up to date in the background. It must never
// ask anything — the terminal is the client's screen, and a password prompt
// written over it would be answered by whatever the user types next — so it
// carries herdr's non-interactive options (remote/attach.rs,
// apply_noninteractive_ssh_options). A wrapper in TEND_SSH is left alone: its
// arguments are its own.
func WatchArgv(host, session string) []string {
	argv := RemoteArgv(host, session)
	if os.Getenv(RemoteCommandEnv) != "" {
		return argv
	}
	return append([]string{"ssh",
		"-o", "BatchMode=yes",
		"-o", "NumberOfPasswordPrompts=0",
		"-o", "StrictHostKeyChecking=yes",
		"-o", "ConnectTimeout=10",
		"-o", "ConnectionAttempts=1",
		"-o", "ServerAliveInterval=15",
		"-o", "ServerAliveCountMax=4",
	}, argv[1:]...)
}

// RemoteShell runs a shell script on host and returns what it printed, with
// stdin given to it — the probe and the install of a remote tend. Only over
// plain ssh: the script is the far side's shell's to run.
func RemoteShell(host, script string, stdin io.Reader) ([]byte, error) {
	cmd := exec.Command("ssh", "-T", "-o", "BatchMode=yes", host, script)
	cmd.Stdin = stdin
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return nil, fmt.Errorf("%s: %s", host, msg)
		}
		return nil, fmt.Errorf("%s: %w", host, err)
	}
	return stdout.Bytes(), nil
}

// RemoteAPIArgv is the command that carries a session's automation socket
// from host: the same bridge, told to join the API socket instead, so a
// script's -ssh reaches the machine it names.
func RemoteAPIArgv(host, session string) []string {
	argv := RemoteArgv(host, session)
	return append(argv, "-api")
}

// Remote starts a command and returns its stdio as a connection.
func Remote(argv []string) (*RemoteConn, error) {
	if len(argv) == 0 {
		return nil, errors.New("transport: no remote command")
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	rc := &RemoteConn{cmd: cmd, stdin: stdin, stdout: stdout}
	cmd.Stderr = &rc.stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("transport: starting %s: %w", argv[0], err)
	}
	return rc, nil
}

// RemoteConn is a connection made of a running command's stdin and stdout.
type RemoteConn struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	stderr limitedBuffer

	closeOnce sync.Once
	closeErr  error
}

func (c *RemoteConn) Read(p []byte) (int, error)  { return c.stdout.Read(p) }
func (c *RemoteConn) Write(p []byte) (int, error) { return c.stdin.Write(p) }

// Complaint is what the command said on stderr, trimmed.
func (c *RemoteConn) Complaint() string { return strings.TrimSpace(c.stderr.String()) }

// Close ends the command. Closing its input is the polite request; it is
// killed if it has not gone once the grace period runs out, so a hung ssh does
// not hold the client open.
func (c *RemoteConn) Close() error {
	c.closeOnce.Do(func() {
		_ = c.stdin.Close()
		done := make(chan struct{})
		go func() {
			_ = c.cmd.Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(remoteCloseGrace):
			_ = c.cmd.Process.Kill()
			<-done
		}
		_ = c.stdout.Close()
	})
	return c.closeErr
}

// limitedBuffer keeps the first stderrLimit bytes written to it and drops the
// rest, so a remote side that will not stop talking cannot grow this forever.
type limitedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if room := stderrLimit - b.buf.Len(); room > 0 {
		b.buf.Write(p[:min(len(p), room)])
	}
	return len(p), nil
}

func (b *limitedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
