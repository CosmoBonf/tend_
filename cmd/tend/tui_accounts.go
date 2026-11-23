package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
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
	agents := accountAgents(cfg)
	for i := range agents {
		agents[i].Orgs = accountOrgs(cfg, agents[i])
	}
	t.openMenu(ui.AccountsMenu(list, agents, x, y))
	t.refreshGitHubAccounts(true)
}

// accountOrgs is which organization each of an agent's logins is in, read
// from where the agent keeps it: the owner has one e-mail in two Claude
// organizations, a Pro and a company Team, and the name an account was
// given says nothing of which it is logged in to. Claude only: the others
// keep no such thing tend knows how to read.
func accountOrgs(cfg config.Config, ag ui.AgentAccounts) map[string]string {
	if ag.ID != "claude" {
		return nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	// The own login's is ~/.claude.json, beside ~/.claude; an account's is
	// inside its directory.
	orgs := map[string]string{"": claudeOrg(filepath.Join(home, ".claude.json"))}
	for _, a := range cfg.AccountsFor("claude") {
		orgs[a.Name] = claudeOrg(filepath.Join(a.HomeDir(), ".claude.json"))
	}
	return orgs
}

// claudeOrg is the organization a Claude Code config file says it is
// logged in to — "personal" for the one an account has of its own — or
// "not logged in". Only the organization's name is read, nothing secret.
func claudeOrg(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return "not logged in"
	}
	var v struct {
		OAuth *struct {
			Org string `json:"organizationName"`
		} `json:"oauthAccount"`
	}
	if json.Unmarshal(data, &v) != nil || v.OAuth == nil || v.OAuth.Org == "" {
		return "not logged in"
	}
	if strings.HasSuffix(v.OAuth.Org, "'s Organization") {
		return "personal"
	}
	return v.OAuth.Org
}

// renameAccount gives an account another name, its directory and login
// kept.
func (t *tui) renameAccount(arg, to string) {
	agent, from := ui.ParseAgentChoiceArg(arg)
	if err := config.RenameAccount(agent, from, to); err != nil {
		t.setMessage(err.Error(), true)
		return
	}
	t.reloadAccounts()
	t.setMessage(from+" is now "+to+" (spaces already open keep the old name)", false)
}

// removeAccount takes an account out of the settings and deletes its
// directory, which holds its login: asked first, with yes typed. A
// directory that is the agent's own, or not under the home, is never
// deleted.
func (t *tui) removeAccount(arg string) {
	agent, name := ui.ParseAgentChoiceArg(arg)
	cfg, _ := config.LoadLenient()
	dir := ""
	for _, a := range cfg.AccountsFor(agent) {
		if a.Name == name {
			dir = a.HomeDir()
		}
	}
	if err := config.RemoveAccount(agent, name); err != nil {
		t.setMessage(err.Error(), true)
		return
	}
	t.reloadAccounts()
	said := name + " removed"
	if safeAccountDir(agent, dir) {
		if err := os.RemoveAll(dir); err != nil {
			said += "; its folder could not be deleted: " + err.Error()
		} else {
			said += ", and its folder " + tildeHome(dir)
		}
	} else if dir != "" {
		said += "; its folder " + tildeHome(dir) + " was left, not being one tend made"
	}
	t.setMessage(said, false)
}

// safeAccountDir is whether tend may delete an account's directory: one
// under the home, deeper than it, and not where the agent keeps its own
// login. A hand-written home pointing at ~/.claude, or at /, is left alone.
func safeAccountDir(agent, dir string) bool {
	home, err := os.UserHomeDir()
	if err != nil || dir == "" {
		return false
	}
	clean := filepath.Clean(dir)
	own := map[string]bool{
		filepath.Join(home, "."+agent): true,
		filepath.Join(home, ".claude"): true,
		filepath.Join(home, ".codex"):  true,
		filepath.Join(home, ".config"): true,
	}
	rel, err := filepath.Rel(home, clean)
	return err == nil && rel != "." && !strings.HasPrefix(rel, "..") && !strings.Contains(rel, string(filepath.Separator)) && !own[clean]
}

// reloadAccounts takes the accounts and defaults just written into the
// client's settings, for the bar.
func (t *tui) reloadAccounts() {
	cfg, err := config.LoadLenient()
	if err != nil {
		return
	}
	t.mu.Lock()
	t.config.Accounts, t.config.DefaultAccounts = cfg.Accounts, cfg.DefaultAccounts
	t.dirty = true
	t.mu.Unlock()
	t.wakeUp()
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
