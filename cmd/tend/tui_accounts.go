package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/auth-com-br/tend/internal/config"
	"github.com/auth-com-br/tend/internal/integration"
	"github.com/auth-com-br/tend/internal/ui"
)

// The accounts in use, tend's own and the owner's: the status bar says at
// its right which GitHub login is active and which account Claude starts
// on — "git CosmoBonf · claude work" — and a click there opens the menu to
// change either. The owner's reason: when one Claude account runs out, the
// next agent goes on another. GitHub's logins are the GitHub CLI's, asked
// of it; the agents' are [[accounts]], and the one each starts on is
// default_accounts in the settings, which the server reads for every new
// pane (daemon.go), so a claude typed in a new terminal is on it too.
//
// All of it is this machine's: over --remote the panes, gh and the
// settings are the other machine's, and the bar says nothing of accounts.

// githubAccounts is what gh last said of its logins, and when.
type githubAccounts struct {
	list    []ui.GitHubAccount
	read    time.Time
	reading bool
}

// githubAccountsEvery is how often gh is asked again: a login made or
// switched outside tend shows within this.
const githubAccountsEvery = 5 * time.Minute

// refreshGitHubAccounts asks gh for its logins in the background, when it
// is time.
func (t *tui) refreshGitHubAccounts(force bool) {
	t.mu.Lock()
	g := &t.github
	if t.host != "" || g.reading || !force && time.Since(g.read) < githubAccountsEvery {
		t.mu.Unlock()
		return
	}
	g.reading = true
	t.mu.Unlock()
	go func() {
		list := readGitHubAccounts()
		t.mu.Lock()
		t.github = githubAccounts{list: list, read: time.Now()}
		t.dirty = true
		t.mu.Unlock()
		t.wakeUp()
	}()
}

// skipGitHubAccountsEnv keeps the tests, which start tend by the hundred,
// from asking the machine's gh and showing its login on the bar they read:
// a test-only switch, as skipOnboardingEnv is.
const skipGitHubAccountsEnv = "TEND_TEST_SKIP_GITHUB_ACCOUNTS"

// readGitHubAccounts is gh's logins on github.com, the active one first.
func readGitHubAccounts() []ui.GitHubAccount {
	if os.Getenv(skipGitHubAccountsEnv) != "" {
		return nil
	}
	gh, err := exec.LookPath("gh")
	if err != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, gh, "auth", "status", "--hostname", "github.com", "--json", "hosts")
	cmd.Env = append(os.Environ(), "GH_PROMPT_DISABLED=1", "GH_NO_UPDATE_NOTIFIER=1")
	// gh exits 1 when a login has a problem, and still prints them all.
	out, _ := cmd.Output()
	var v struct {
		Hosts map[string][]struct {
			Login  string `json:"login"`
			Active bool   `json:"active"`
		} `json:"hosts"`
	}
	if json.Unmarshal(out, &v) != nil {
		return nil
	}
	var list []ui.GitHubAccount
	for _, a := range v.Hosts["github.com"] {
		list = append(list, ui.GitHubAccount{Login: a.Login, Active: a.Active})
	}
	sort.SliceStable(list, func(i, j int) bool { return list[i].Active && !list[j].Active })
	return list
}

// accountsLabelLocked is what the status bar says of the accounts: the
// active GitHub login, and the account each agent with accounts starts on
// — its own login said too, once it has others to be told from.
func (t *tui) accountsLabelLocked(cfg config.Config) string {
	if t.host != "" {
		return ""
	}
	var parts []string
	for _, a := range t.github.list {
		if a.Active {
			parts = append(parts, "git "+a.Login)
		}
	}
	for _, ag := range accountAgents(cfg) {
		if len(ag.Accounts) == 0 {
			continue
		}
		name := ag.Default
		if name == "" {
			name = "own login"
		}
		parts = append(parts, ag.ID+" "+name)
	}
	return strings.Join(parts, " · ")
}

// accountAgents are the agents the accounts menu lists: Claude, and every
// agent that has an account.
func accountAgents(cfg config.Config) []ui.AgentAccounts {
	names := map[string]string{"claude": "Claude Code", "codex": "Codex"}
	ids := []string{"claude"}
	seen := map[string]bool{"claude": true}
	for _, a := range cfg.Accounts {
		if !seen[a.Agent] {
			seen[a.Agent] = true
			ids = append(ids, a.Agent)
		}
	}
	var out []ui.AgentAccounts
	for _, id := range ids {
		ag := ui.AgentAccounts{ID: id, Name: names[id], Default: cfg.DefaultAccounts[id]}
		if ag.Name == "" {
			ag.Name = id
		}
		for _, a := range cfg.AccountsFor(id) {
			ag.Accounts = append(ag.Accounts, a.Name)
		}
		out = append(out, ag)
	}
	return out
}

// openAccountsMenu puts the accounts menu up over the status bar.
func (t *tui) openAccountsMenu(x, y int) {
	if t.host != "" {
		return
	}
	cfg, _ := config.LoadLenient()
	t.mu.Lock()
	list := t.github.list
	t.mu.Unlock()
	t.openMenu(ui.AccountsMenu(list, accountAgents(cfg), x, y))
	t.refreshGitHubAccounts(true)
}

// switchGitHub makes a GitHub login the active one.
func (t *tui) switchGitHub(login string) {
	gh, err := exec.LookPath("gh")
	if err != nil {
		t.setMessage("GitHub needs the GitHub CLI: https://cli.github.com", true)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, gh, "auth", "switch", "--hostname", "github.com", "--user", login).CombinedOutput()
	if err != nil {
		t.setMessage("GitHub: "+strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0]), true)
		return
	}
	t.setMessage("GitHub: now "+login, false)
	t.refreshGitHubAccounts(true)
}

// setDefaultAccount makes an account the one its agent starts on: new
// agents tend starts, and a claude typed in a new terminal. tend's hooks
// go into its directory now, so the agent's state is followed there from
// its first start.
func (t *tui) setDefaultAccount(agent, name string) {
	if err := config.SetDefaultAccount(agent, name); err != nil {
		t.setMessage(err.Error(), true)
		return
	}
	cfg, _ := config.LoadLenient()
	if name != "" {
		for _, a := range cfg.AccountsFor(agent) {
			if a.Name == name {
				go installHooksIn(agent, a.HomeDir())
			}
		}
	}
	shown := name
	if shown == "" {
		shown = "its own login"
	}
	t.setMessage(agent+" starts on "+shown+": new agents and new terminals (running ones stay as they are)", false)
	t.mu.Lock()
	t.config.DefaultAccounts = cfg.DefaultAccounts
	t.config.Accounts = cfg.Accounts
	t.dirty = true
	t.mu.Unlock()
	t.wakeUp()
}

// installHooksIn installs tend's hooks for an agent in an account's
// directory, by this very tend, with the agent's variable pointing there.
func installHooksIn(agent, dir string) {
	self, err := os.Executable()
	if err != nil || integration.HomeEnv(agent) == "" {
		return
	}
	_ = os.MkdirAll(dir, 0o700)
	cmd := exec.Command(self, "integration", "install", agent)
	cmd.Env = append(os.Environ(), integration.HomeEnv(agent)+"="+dir)
	_ = cmd.Run()
}
