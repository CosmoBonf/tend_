//go:build unix

package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/auth-com-br/tend/internal/pty"
	"github.com/auth-com-br/tend/internal/vt"
)

// TestOInThePreviewEditsInTheSamePaneAndComesBack: o runs the editor in the
// preview's own pane, every key typed reaches the editor and none the
// preview, and when the editor exits the preview is back, showing what was
// saved. If it regresses, o opens a second tab again — the one the owner
// asked to be rid of — or the preview steals the editor's keys.
func TestOInThePreviewEditsInTheSamePaneAndComesBack(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "note.txt")
	if err := os.WriteFile(file, []byte("first-line\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// An editor that says it has the terminal, reads one line from it, adds
	// it to the file and exits, as saving and quitting does.
	editor := filepath.Join(dir, "fake-editor")
	script := "#!/bin/sh\nprintf 'IN-THE-EDITOR\\n'\nread -r line\nprintf '%s\\n' \"$line\" >> \"$1\"\n"
	if err := os.WriteFile(editor, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	p, err := pty.Start(buildBinary(t), []string{"view", "-editor", editor, file}, pty.Options{
		Size: pty.Size{Cols: 60, Rows: 12},
		Env: append(withoutEnv(withoutEnv(os.Environ(), "TEND_SOCKET_PATH"), "TEND_PANE_ID"),
			"TEND_CONFIG="+quietConfig(t), "TERM=xterm-256color"),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Close() })
	a := &attached{pty: p, screen: vt.NewScreen(60, 12, 0)}
	go func() { _, _ = io.Copy(a, p) }()

	a.waitForScreen(t, "the preview", func(s string) bool { return strings.Contains(s, "first-line") })
	a.send(t, "o")
	a.waitForScreen(t, "the editor in this pane", func(s string) bool { return strings.Contains(s, "IN-THE-EDITOR") })
	// q and w are the preview's keys: taken by it, the line would be short.
	a.send(t, "qwritten-in-the-editor\r")
	a.waitForScreen(t, "the preview again, with what was saved", func(s string) bool {
		return strings.Contains(s, "qwritten-in-the-editor") && !strings.Contains(s, "IN-THE-EDITOR")
	})
	if got, _ := os.ReadFile(file); string(got) != "first-line\nqwritten-in-the-editor\n" {
		t.Errorf("the file is %q", got)
	}

	// Still the preview, and q still closes it.
	a.send(t, "q")
	done := make(chan struct{})
	go func() { _ = p.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("q did not close the preview after the editor")
	}
}
