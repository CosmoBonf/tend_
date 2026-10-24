//go:build unix

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/auth-com-br/tend/internal/config"
	"github.com/auth-com-br/tend/internal/integration"
)

// TestTheSettingsScreenInstallsAnAgentsHooks is herdr's integrations
// section: an agent found on this machine gets a row, and stepping it
// installs the hooks and takes them out again. If it regresses, the only way
// to install them is a command somebody has to know about.
func TestTheSettingsScreenInstallsAnAgentsHooks(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	// Installed means its settings directory is there, as Claude Code makes it.
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o700); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)

	var row *settingRow
	for _, r := range integrationRows(false) {
		if strings.HasSuffix(r.label, "claude") {
			r := r
			row = &r
		}
	}
	if row == nil {
		t.Fatal("claude is on PATH and has no row")
	}
	if len(integrationRows(true)) != 0 {
		t.Error("a client on another machine must not offer to install here")
	}

	if _, err := row.apply(row.choices[1]); err != nil {
		t.Fatalf("install: %v", err)
	}
	if got := row.value(config.Defaults()); got != string(integration.StatusCurrent) {
		t.Errorf("after installing, the row says %q", got)
	}
	if _, err := row.apply(row.choices[0]); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if got := row.value(config.Defaults()); got != string(integration.StatusNotInstalled) {
		t.Errorf("after removing, the row says %q", got)
	}
}

// TestTheEditorRowOffersTheEditorsInstalledHere: the settings screen lists
// the editors found on this machine, after "automatic", keeps one written in
// the file by hand, and is not offered to a client on another machine. If it
// regresses, choosing micro means a shell profile and a server restart, which
// is what the owner went to the settings screen to avoid.
func TestTheEditorRowOffersTheEditorsInstalledHere(t *testing.T) {
	bin := t.TempDir()
	for _, name := range []string{"micro", "nano"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin)

	rows := editorRows(false, "")
	if len(rows) != 1 {
		t.Fatalf("want one editor row, got %d", len(rows))
	}
	var got []string
	for _, c := range rows[0].choices {
		got = append(got, c.label+"="+c.toml)
	}
	if want := `automatic="" micro="micro" nano="nano"`; strings.Join(got, " ") != want {
		t.Errorf("choices are %s, want %s", strings.Join(got, " "), want)
	}
	cfg := config.Defaults()
	if v := rows[0].value(cfg); v != `""` {
		t.Errorf("with nothing chosen the row reads %s, want automatic", v)
	}
	cfg.Files.Editor = "micro"
	if v := rows[0].value(cfg); v != `"micro"` {
		t.Errorf("with micro chosen the row reads %s", v)
	}

	custom := editorRows(false, "code --wait")[0].choices
	if last := custom[len(custom)-1]; last.toml != `"code --wait"` {
		t.Errorf("an editor set by hand is not kept as a choice: %+v", custom)
	}
	if len(editorRows(true, "")) != 0 {
		t.Error("a client on another machine must not be offered this machine's editors")
	}
}
