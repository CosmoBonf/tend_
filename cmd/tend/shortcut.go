package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/auth-com-br/tend/internal/launcher"
)

// `tend shortcut`: tend in the desktop's application launcher, opened with a
// click like any other program — a terminal with the session in it. It
// writes the user's own desktop entry and icon (internal/launcher); nothing
// system-wide, nothing as root.

const shortcutUsage = `usage: tend shortcut [-s session]   put tend in the application launcher
       tend shortcut -remove         take it out`

func runShortcut(args []string) error {
	fs := flag.NewFlagSet("shortcut", flag.ExitOnError)
	fs.Usage = func() { fmt.Fprintln(os.Stderr, shortcutUsage); fs.PrintDefaults() }
	session := fs.String("s", "", "the session the shortcut opens (default: the default one)")
	remove := fs.Bool("remove", false, "take the shortcut out of the launcher")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return errors.New(shortcutUsage)
	}
	if runtime.GOOS != "linux" {
		// macOS wants an application bundle and has no entry file to
		// write; said rather than writing a file nothing reads.
		return fmt.Errorf("tend shortcut is for Linux desktops so far; on %s, add tend to your terminal's profiles", runtime.GOOS)
	}
	dataHome, err := launcher.DataHome()
	if err != nil {
		return err
	}
	if *remove {
		p, err := launcher.Remove(dataHome)
		if err != nil {
			return err
		}
		refreshLauncher(dataHome)
		fmt.Printf("removed %s\n", p.Entry)
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	// The path as it is run, not what a symlink points to: a tend put on
	// the PATH by a link keeps working when what it links to is replaced.
	if exe, err = filepath.Abs(exe); err != nil {
		return err
	}
	p, err := launcher.Install(dataHome, exe, *session)
	if err != nil {
		return err
	}
	refreshLauncher(dataHome)
	fmt.Printf("tend is in the application launcher: look for \"tend\" there.\n  %s\n  it runs %s\n", p.Entry, exe)
	fmt.Println("`tend shortcut -remove` takes it out; run `tend shortcut` again if tend moves.")
	return nil
}

// refreshLauncher tells the desktop its entries changed, where the tool for
// that is installed. Desktops watch the directory and most need no telling;
// a failure here is not a failure of the shortcut.
func refreshLauncher(dataHome string) {
	if tool, err := exec.LookPath("update-desktop-database"); err == nil {
		_ = exec.Command(tool, "-q", filepath.Join(dataHome, "applications")).Run()
	}
}
