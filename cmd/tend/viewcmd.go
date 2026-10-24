package main

import (
	"errors"
	"flag"
	"os"
	"path/filepath"

	"golang.org/x/term"

	"github.com/auth-com-br/tend/internal/api"
	"github.com/auth-com-br/tend/internal/explorer"
	"github.com/auth-com-br/tend/internal/server"
)

// runView is the files panel's preview: one file, read-only, with line
// numbers and syntax colour, read again when it changes. The panel opens it
// in a pane beside the main one and points it at the next file; run by hand
// it is a pager that knows code.
func runView(args []string) error {
	fs := flag.NewFlagSet("view", flag.ExitOnError)
	line := fs.Int("line", 0, "line to show and mark, from 1")
	editor := fs.String("editor", "", "command o edits with (default: [files] editor, $VISUAL, $EDITOR, vi)")
	fs.Usage = func() {
		fs.Output().Write([]byte("usage: tend view [-line n] <file>\n\n" +
			"Shows a file read-only with line numbers and syntax colour;\n" +
			"the files panel's preview. q closes, o edits it here and comes\n" +
			"back when the editor exits,\n" +
			"w wraps long lines.\n\n"))
		fs.PrintDefaults()
	}
	_ = fs.Parse(args)
	if fs.NArg() != 1 {
		fs.Usage()
		os.Exit(2)
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return errors.New("view draws on a terminal, and stdin is not one")
	}
	path, err := filepath.Abs(fs.Arg(0))
	if err != nil {
		return err
	}
	// -editor fixes the editor; without it, it is looked up at each open,
	// so one chosen on the settings screen is used by a panel already open.
	opener := &explorer.SessionOpener{
		Socket: os.Getenv(api.EnvSocketPath),
		Pane:   os.Getenv(api.EnvPaneID),
		Editor: *editor,
		Choose: server.Editor,
	}
	preview := explorer.NewPreview(path, *line, opener)
	// o edits here, in the preview's own pane, and comes back to it.
	preview.EditInPlace(func() string {
		if *editor != "" {
			return *editor
		}
		return server.Editor()
	})
	return explorer.RunPreview(preview, os.Stdin, os.Stdout)
}
