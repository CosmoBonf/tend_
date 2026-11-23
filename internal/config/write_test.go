package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAnEditKeepsTheFileTheUserWrote: a settings file is read by the person
// who wrote it. Rewriting the whole thing from the parsed values would take
// away their comments and their order, and they would notice.
func TestAnEditKeepsTheFileTheUserWrote(t *testing.T) {
	original := `# my settings
[keys]
# I use ctrl+a because screen did
prefix = "ctrl+a"

[ui]
sidebar = true   # keep it
grouped = false
`
	got := Upsert(original, "ui", "grouped", "true")
	for _, keep := range []string{"# my settings", "# I use ctrl+a because screen did", `prefix = "ctrl+a"`, "sidebar = true   # keep it"} {
		if !strings.Contains(got, keep) {
			t.Errorf("the edit lost %q:\n%s", keep, got)
		}
	}
	if !strings.Contains(got, "grouped = true") || strings.Contains(got, "grouped = false") {
		t.Errorf("the value was not changed:\n%s", got)
	}
}

// TestAnEditAddsWhatIsMissing covers a key or a whole section the file does
// not have yet, which is the ordinary case for a setting nobody has touched.
func TestAnEditAddsWhatIsMissing(t *testing.T) {
	got := Upsert("[ui]\nsidebar = true\n", "ui", "grouped", "true")
	if !strings.Contains(got, "sidebar = true\ngrouped = true") {
		t.Errorf("the key was not added to its section:\n%s", got)
	}

	got = Upsert("[ui]\nsidebar = true\n", "sound", "enabled", "true")
	if !strings.Contains(got, "[sound]\nenabled = true") {
		t.Errorf("the section was not added:\n%s", got)
	}

	// Into the right section, not the first one that happens to hold the key.
	two := "[notify]\ntoasts = \"tend\"\n\n[sound]\nenabled = false\n"
	got = Upsert(two, "sound", "enabled", "true")
	if !strings.Contains(got, "[notify]\ntoasts = \"tend\"") || !strings.Contains(got, "[sound]\nenabled = true") {
		t.Errorf("the edit went to the wrong section:\n%s", got)
	}

	// Spacing people actually write.
	got = Upsert("[ui]\nsidebar=true\n", "ui", "sidebar", "false")
	if !strings.Contains(got, "sidebar = false") || strings.Contains(got, "sidebar=true") {
		t.Errorf("an unspaced assignment was not replaced:\n%s", got)
	}
}

// TestAnEditThatWouldBreakTheFileIsRefused: writing a settings file tend will
// not start with locks the user out of their own settings.
func TestAnEditThatWouldBreakTheFileIsRefused(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tend.toml")
	if err := os.WriteFile(path, []byte("[keys]\nprefix = \"ctrl+b\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TEND_CONFIG", path)

	if err := Set("notify", "toasts", Quote("shouting")); err == nil {
		t.Error("a value the settings refuse was written anyway")
	}
	after, _ := os.ReadFile(path)
	if string(after) != "[keys]\nprefix = \"ctrl+b\"\n" {
		t.Errorf("the file was changed by a refused edit:\n%s", after)
	}

	if err := Set("ui", "grouped", Bool(true)); err != nil {
		t.Fatalf("a good edit failed: %v", err)
	}
	cfg, err := LoadFile(path)
	if err != nil || !cfg.UI.Grouped {
		t.Errorf("after the edit: %+v, %v", cfg.UI, err)
	}
}

// TestTopLevelKeysGoBeforeEveryTable is herdr's upsert_top_level_bool: a key
// already there is replaced where it is, one that is not goes first, a key of
// the same name inside a table and a commented-out line are left alone, and
// the result reads back. If it regresses, finishing the welcome writes
// `onboarding` into [keys], where it is an unknown setting and tend refuses
// the file.
func TestTopLevelKeysGoBeforeEveryTable(t *testing.T) {
	cases := map[string]string{
		"": "onboarding = false\n",
		"onboarding = true\n[keys]\nprefix = \"ctrl+b\"\n": "onboarding = false\n[keys]\nprefix = \"ctrl+b\"\n",
		"# onboarding = true\n[keys]\n":                    "onboarding = false\n# onboarding = true\n[keys]\n",
		"[keys]\nonboarding = true\n":                      "onboarding = false\n[keys]\nonboarding = true\n",
	}
	for in, want := range cases {
		if got := UpsertTopLevel(in, "onboarding", "false"); got != want {
			t.Errorf("UpsertTopLevel(%q) = %q, want %q", in, got, want)
		}
	}

	cfg, err := parse(UpsertTopLevel(Example, "onboarding", "false"), "example")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ShowOnboarding() {
		t.Error("onboarding = false should turn the welcome off")
	}
	if !Defaults().ShowOnboarding() {
		t.Error("with the key missing, the welcome shows")
	}
}

// TestAnAccountIsAddedOnceAndOnlyForAnAgentThatCanHaveOne: AddAccount puts a
// [[accounts]] table at the end of the settings, refuses the same name for
// the same agent, and an account of an agent with no directory of its own
// is refused when the file is read. If it regresses, a second account
// overwrites the first, or one that can never work is offered.
func TestAnAccountIsAddedOnceAndOnlyForAnAgentThatCanHaveOne(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[ui]\nmouse = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TEND_CONFIG", path)
	if err := AddAccount(Account{Name: "work", Agent: "claude", Home: "~/.claude-work"}); err != nil {
		t.Fatal(err)
	}
	if err := AddAccount(Account{Name: "work", Agent: "claude", Home: "~/.claude-work"}); err == nil {
		t.Error("the same account was added twice")
	}
	if err := AddAccount(Account{Name: "work", Agent: "codex", Home: "~/.codex-work"}); err != nil {
		t.Errorf("an account of the same name for another agent: %v", err)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.AccountsFor("claude"); len(got) != 1 || got[0].Name != "work" || !cfg.UI.Mouse {
		t.Errorf("accounts %+v, mouse %v", cfg.Accounts, cfg.UI.Mouse)
	}
	home, _ := os.UserHomeDir()
	if got := cfg.AccountsFor("claude")[0].HomeDir(); got != filepath.Join(home, ".claude-work") {
		t.Errorf("home = %q", got)
	}
	if _, err := parse("[[accounts]]\nname = \"x\"\nagent = \"aider\"\nhome = \"~/.x\"\n", "x"); err == nil {
		t.Error("an account of aider, which has no directory of its own, was accepted")
	}
}

// TestADefaultAccountIsKeptAndPutsNewPanesOnIt: SetDefaultAccount keeps
// default_accounts at the top of the file, and DefaultAccountEnv is the
// variable that puts a new pane on it; the agent's own login again takes
// it out. If it regresses, a claude typed in a new terminal is on whichever
// account its default directory holds, not the one chosen.
func TestADefaultAccountIsKeptAndPutsNewPanesOnIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[ui]\nmouse = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TEND_CONFIG", path)
	if err := AddAccount(Account{Name: "work", Agent: "claude", Home: "/tmp/claude-work"}); err != nil {
		t.Fatal(err)
	}
	if err := SetDefaultAccount("claude", "work"); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.DefaultAccountEnv(); len(got) != 1 || got[0] != "CLAUDE_CONFIG_DIR=/tmp/claude-work" || !cfg.UI.Mouse {
		t.Errorf("env %v, mouse %v", got, cfg.UI.Mouse)
	}
	if err := SetDefaultAccount("claude", ""); err != nil {
		t.Fatal(err)
	}
	cfg, _ = Load()
	if got := cfg.DefaultAccountEnv(); len(got) != 0 {
		t.Errorf("own login again, still %v", got)
	}
}
