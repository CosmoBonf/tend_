package main

import (
	"strings"
	"unicode"

	"github.com/auth-com-br/tend/internal/config"
	"github.com/auth-com-br/tend/internal/integration"
	"github.com/auth-com-br/tend/internal/proto"
	"github.com/auth-com-br/tend/internal/ui"
)

// Starting an agent from a group's menu: "new agent here..." lists the
// agents installed on the server's machine — the agent manager's catalog,
// found the same way — and the one picked starts in a new space of the
// group, in the group's folder. When the agent exits the terminal stays, a
// shell in the same folder, as a tab handed a task does (resumeScript).

// openGroupAgentMenu puts up the list of agents to start in a group, where
// its menu was. The catalog asks each agent for its version, which takes a
// moment, so the list the manager last read is used when there is one.
func (t *tui) openGroupAgentMenu(group string, x, y int) {
	if !t.client.Supports(proto.MethodAgentsCatalog) {
		t.setMessage("this server is older than the agent list; "+handoffCommand(t.session)+" moves it to this build", true)
		return
	}
	t.mu.Lock()
	list := t.agentStatus
	t.mu.Unlock()
	if list == nil {
		t.setMessage("looking for the agents installed here…", false)
		res, err := t.client.AgentsCatalog()
		if err != nil {
			t.setMessage("could not list the agents: "+refusalText(err), true)
			return
		}
		list = res.Agents
		t.mu.Lock()
		t.agentStatus = list
		t.mu.Unlock()
		t.setMessage("", false)
	}
	choices := withAccounts(installedAgents(list), t.accounts())
	if len(choices) == 0 {
		t.setMessage("no agent is installed here; ctrl+b A installs one", true)
		return
	}
	t.openMenu(ui.GroupAgentMenu(group, choices, x, y))
}

// installedAgents are the agents a group can start: installed, and agents
// rather than the memory tools the catalog lists with them.
func installedAgents(list []proto.AgentStatus) []ui.AgentChoice {
	var out []ui.AgentChoice
	for _, a := range list {
		if a.Installed && a.Kind != "memory" {
			out = append(out, ui.AgentChoice{ID: a.ID, Name: a.Name, CanAddAccount: integration.HomeEnv(a.ID) != ""})
		}
	}
	return out
}

// accounts are the [[accounts]] in the settings file as it is now: one
// written there is offered without restarting tend.
func (t *tui) accounts() []config.Account {
	if c, err := config.LoadLenient(); err == nil {
		return c.Accounts
	}
	return t.config.Accounts
}

// withAccounts puts each agent's [[accounts]] after it, one choice each:
// the agent's own login, then the others by name.
func withAccounts(agents []ui.AgentChoice, accounts []config.Account) []ui.AgentChoice {
	var out []ui.AgentChoice
	for _, a := range agents {
		out = append(out, a)
		for _, acc := range accounts {
			if acc.Agent == a.ID {
				out = append(out, ui.AgentChoice{ID: a.ID, Name: a.Name, Account: acc.Name})
			}
		}
	}
	return out
}

// addAccount writes a new account of an agent, in a directory named after
// it beside the agent's own (~/.claude-work), and starts the agent on it,
// whose first start is where the account is logged in to.
func (t *tui) addAccount(agentID, name string) error {
	if t.host != "" {
		// The directory and the settings would be this machine's, and the
		// agent runs on the other one.
		t.setMessage("an account is added on the machine the session runs on: tend config there", true)
		return nil
	}
	acc := config.Account{Name: name, Agent: agentID, Home: "~/." + agentID + "-" + accountSlug(name)}
	if err := config.AddAccount(acc); err != nil {
		t.setMessage(err.Error(), true)
		return nil
	}
	t.setMessage(name+" added: log in to it in the space that opens", false)
	return t.startAgentInGroup("", ui.AgentChoiceArg(ui.AgentChoice{ID: agentID, Account: name}))
}

// accountSlug is a name made fit for a directory: lower case, with what is
// not a letter or a digit made a dash.
func accountSlug(name string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(name) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			dash = false
		} else if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	if s := strings.TrimSuffix(b.String(), "-"); s != "" {
		return s
	}
	return "account"
}

// accountScript starts an agent on an account: the agent's variable set to
// the account's directory ($1=$2), the directory made if it is new, tend's
// hooks put in it — they are per directory, and without them tend would not
// hear what the agent is doing — then the agent, and a shell after it, as
// resumeScript. The variable is exported, so the shell left behind starts
// the agent on the same account again.
//
// It is set here and not in the pane's Env: an Env is the whole
// environment a pane starts with, and one naming only the account left the
// agent with no PATH, HOME or TERM — the client cannot add the server's
// own, which are another machine's over --remote.
const accountScript = `export "$1=$2"
mkdir -p "$2" && "${TEND_BIN_PATH:-tend}" integration install "$3" >/dev/null 2>&1
shift 3
"$@"
exec "${SHELL:-/bin/sh}"`

// startAgentInGroup starts an agent in a new space of a group, named after
// the agent, in the group's folder. arg is a menu item's: the agent, and
// the account to start it on. Group "" is "new agent..." at the foot of the
// spaces: the space goes beside the one in view, in its project.
func (t *tui) startAgentInGroup(group, arg string) error {
	id, accountName := ui.ParseAgentChoiceArg(arg)
	accounts := t.accounts() // a file read, so not under the lock
	t.mu.Lock()
	var agent proto.AgentStatus
	for _, a := range t.agentStatus {
		if a.ID == id {
			agent = a
		}
	}
	var account config.Account
	for _, acc := range accounts {
		if acc.Agent == id && acc.Name == accountName {
			account = acc
		}
	}
	var dirOf uint64
	if group == "" {
		dirOf = t.focus
		for _, w := range t.snap.Workspaces {
			if w.ID == t.workspace && w.Group != sshGroup {
				group = w.Group
			}
		}
	}
	t.mu.Unlock()
	if agent.ID == "" || accountName != "" && account.Name == "" {
		return nil
	}
	// The path the catalog found it at: the program a pane runs is the
	// server machine's, and a name looked up again could find another.
	program := agent.Path
	if program == "" {
		program = agent.ID
	}
	name := agent.Name
	command := []string{"/bin/sh", "-c", resumeScript, "tend-agent", program}
	if account.Name != "" {
		name += " · " + account.Name
		command = []string{"/bin/sh", "-c", accountScript, "tend-agent",
			integration.HomeEnv(agent.ID), account.HomeDir(), agent.ID, program}
	}
	err := t.newWorkspaceRunning(group, name, name, func(dir string) proto.PaneSpec {
		spec := proto.PaneSpec{Command: command, Dir: dir, Agent: agent.ID}
		if dir == "" {
			spec.DirOf = dirOf
		}
		return spec
	})
	if err != nil {
		t.setMessage(agent.Name+" did not start: "+refusalText(err), true)
	}
	return nil
}
