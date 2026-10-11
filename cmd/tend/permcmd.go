package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/auth-com-br/tend/internal/proto"
)

// `tend perm`: the permissions manager's view from the command line — what
// each agent may do without asking, rule by rule, for a folder's project.
// It asks the session's server, which is where the agents and their files
// are; changing them is the panel's (prefix+P), where the rule can be seen
// before it goes.

const permUsage = `usage: tend perm list [-s session] [-dir folder] [-agent id]   the agents' permission rules`

func runPerm(args []string) error {
	if len(args) == 0 || args[0] != "list" {
		return errors.New(permUsage)
	}
	fs := flag.NewFlagSet("perm list", flag.ExitOnError)
	name := sessionFlag(fs)
	dir := fs.String("dir", "", "the folder whose project to read (default: this one)")
	agent := fs.String("agent", "", "only this agent (claude, codex, cursor, gemini, opencode, dsh)")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if *dir == "" {
		*dir, _ = os.Getwd()
	}
	abs, err := filepath.Abs(*dir)
	if err != nil {
		return err
	}
	c, err := connect(*name, nil)
	if err != nil {
		return err
	}
	defer c.Close()
	if !c.Supports(proto.MethodPermList) {
		return fmt.Errorf("session %q's server is older than the permissions manager; %s moves it to this build", *name, handoffCommand(*name))
	}
	list, err := c.PermList(0, abs)
	if err != nil {
		return err
	}
	printPermList(list, *agent)
	return nil
}

func printPermList(list proto.PermListResult, only string) {
	if list.Project != "" {
		fmt.Printf("project: %s\n\n", list.Project)
	}
	for _, a := range list.Agents {
		if only != "" && a.ID != only {
			continue
		}
		fmt.Printf("%s (%d rules)\n", a.Name, len(a.Rules))
		for _, m := range a.Modes {
			value := m.Value
			if value == "" {
				value = "(unset)"
			}
			fmt.Printf("  %s = %s  [%s]\n", m.Key, value, m.Scope)
		}
		for _, warning := range a.Warnings {
			fmt.Printf("  ! %s\n", warning)
		}
		if a.Error != "" {
			fmt.Printf("  error: %s\n", a.Error)
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
		for _, r := range a.Rules {
			marks := ""
			if r.Elsewhere {
				marks += " ≡"
			}
			if r.Why != "" {
				marks += " ⚠ " + r.Why
			}
			fmt.Fprintf(w, "  %s\t%s\t%s%s\t%s\n", r.Decision, r.Scope, r.Text, marks, r.File)
		}
		w.Flush()
		fmt.Println()
	}
}
