package server

import (
	"os"
	"path/filepath"
	"testing"
)

// TestTheEditorChosenInTheSettingsComesBeforeTheEnvironments: [files]
// editor, set from the settings screen, is what files and a pane's history
// open in, ahead of $VISUAL and $EDITOR, which fall back to vi. It is read
// at each call. If it regresses, micro chosen on the settings screen still
// opens vi, the editor the owner could not save in.
func TestTheEditorChosenInTheSettingsComesBeforeTheEnvironments(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "config.toml")
	t.Setenv("TEND_CONFIG", cfg)
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "")
	if got := Editor(); got != "vi" {
		t.Errorf("with nothing set, Editor is %q, want vi", got)
	}
	t.Setenv("EDITOR", "nano")
	if got := Editor(); got != "nano" {
		t.Errorf("with $EDITOR set, Editor is %q, want nano", got)
	}
	if err := os.WriteFile(cfg, []byte("[files]\neditor = \"micro\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := Editor(); got != "micro" {
		t.Errorf("with micro chosen, Editor is %q, want micro", got)
	}
}
