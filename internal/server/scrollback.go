package server

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/auth-com-br/tend/internal/config"
	"github.com/auth-com-br/tend/internal/session"
)

// Reading a long answer in a pane means scrolling it. Reading it in an editor
// means searching it, copying from it and keeping it — which is what people do
// with the output of a build or an agent's explanation. herdr opens the
// scrollback in `$EDITOR` on a key (`EditScrollback`); this is that.
//
// The text is written here because it is here, and the editor runs in a pane
// of its own beside the one it came from. The file is deleted by the command
// that opened it, so nothing has to remember it: an editor that is still open
// when tend stops still has its file, and a tend that never sees that editor
// exit leaves nothing behind either way.

// EditScrollback opens a pane's history in an editor, in a new pane beside it.
func (s *Server) EditScrollback(id session.PaneID, editor string) (session.PaneID, error) {
	text, err := s.RecentText(id, 0)
	if err != nil {
		return 0, err
	}
	if strings.TrimSpace(text) == "" {
		return 0, fmt.Errorf("server: pane %d has said nothing to read", id)
	}

	path, err := writeScrollback(id, text)
	if err != nil {
		return 0, err
	}
	if editor == "" {
		editor = Editor()
	}
	// The editor and the removal in one command, so the file goes when the
	// editor is closed without tend having to watch for it.
	command := []string{
		"/bin/sh", "-c",
		editor + " \"$1\"; rm -f \"$1\"", "tend-scrollback", path,
	}
	pane, err := s.SplitPane(id, session.Rows, PaneSpec{
		Command: command, Title: "scrollback", Named: true,
		// Gone with the editor, as herdr's overlay pane is: its frame
		// sitting there "exited" is one more thing to close by hand.
		CloseOnExit: true,
	})
	if err != nil {
		_ = os.Remove(path)
		return 0, err
	}
	return pane, nil
}

// Editor is what to open text with: the one chosen in the settings
// ([files] editor), else the environment's, else vi, which is on every
// machine that has a terminal.
//
// The settings file is read on each call, so a choice made on the settings
// screen is used by the next file opened, with no restart: the server's
// environment is fixed when it starts.
func Editor() string {
	if cfg, err := config.LoadLenient(); err == nil {
		if e := strings.TrimSpace(cfg.Files.Editor); e != "" {
			return e
		}
	}
	for _, name := range []string{"VISUAL", "EDITOR"} {
		if value := strings.TrimSpace(os.Getenv(name)); value != "" {
			return value
		}
	}
	return "vi"
}

// writeScrollback puts the text where the editor can read it.
func writeScrollback(id session.PaneID, text string) (string, error) {
	dir := os.TempDir()
	f, err := os.CreateTemp(dir, fmt.Sprintf("tend-pane-%d-*.txt", id))
	if err != nil {
		return "", err
	}
	name := f.Name()
	if _, err := f.WriteString(text + "\n"); err != nil {
		f.Close()
		_ = os.Remove(name)
		return "", err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(name)
		return "", err
	}
	// Readable by its owner only: a pane's history is whatever was on
	// somebody's screen, and /tmp is shared.
	if err := os.Chmod(name, 0o600); err != nil {
		_ = os.Remove(name)
		return "", err
	}
	return filepath.Clean(name), nil
}
