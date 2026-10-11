package perm

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

// geminiAgent is Gemini CLI: the tool lists of its settings.json, which
// tend edits, and its policy engine's TOML files (~/.gemini/policies, a
// project's .gemini/policies), which it shows and opens in an editor. A
// policy rule has a priority, a mode list and patterns of its own; shown as
// a line it can be read, and edited by hand where all of it can be seen.
type geminiAgent struct{ *listAgent }

// geminiPolicy is the part of a [[rule]] the panel shows (Gemini CLI
// 0.61.0's PolicyRuleSchema).
type geminiPolicy struct {
	Rule []struct {
		ToolName      any    `toml:"toolName"`
		MCPName       string `toml:"mcpName"`
		CommandPrefix any    `toml:"commandPrefix"`
		CommandRegex  string `toml:"commandRegex"`
		ArgsPattern   string `toml:"argsPattern"`
		Decision      string `toml:"decision"`
		Priority      int    `toml:"priority"`
		Modes         []string
	} `toml:"rule"`
}

func (g *geminiAgent) Read(p Place) Report {
	r := g.listAgent.Read(p)
	dirs := []struct {
		dir   string
		scope Scope
	}{{filepath.Join(geminiDir(), "policies"), Global}}
	if p.Root != "" {
		dirs = append(dirs, struct {
			dir   string
			scope Scope
		}{filepath.Join(p.Root, ".gemini", "policies"), Project})
	}
	for _, d := range dirs {
		paths, _ := filepath.Glob(filepath.Join(d.dir, "*.toml"))
		sort.Strings(paths)
		for _, path := range paths {
			if seenFile(r.Files, path) {
				continue
			}
			r.Files = append(r.Files, File{Path: path, Scope: d.scope, Exists: true, ReadOnly: true, Note: "a policy file: edit it in an editor"})
			rules, err := geminiPolicyRules(path, d.scope)
			if err != nil {
				r.Warnings = append(r.Warnings, err.Error())
			}
			r.Rules = append(r.Rules, rules...)
		}
	}
	return r
}

func seenFile(files []File, path string) bool {
	for _, f := range files {
		if filepath.Clean(f.Path) == filepath.Clean(path) {
			return true
		}
	}
	return false
}

func geminiPolicyRules(path string, scope Scope) ([]Rule, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var pol geminiPolicy
	if _, err := toml.Decode(string(data), &pol); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	var out []Rule
	for _, rule := range pol.Rule {
		d := Ask
		switch rule.Decision {
		case "allow":
			d = Allow
		case "deny":
			d = Deny
		}
		text := words(rule.ToolName)
		if rule.MCPName != "" {
			text = "mcp " + rule.MCPName + " " + text
		}
		if prefix := words(rule.CommandPrefix); prefix != "" {
			text += "(" + prefix + ")"
		} else if rule.CommandRegex != "" {
			text += "(/" + rule.CommandRegex + "/)"
		} else if rule.ArgsPattern != "" {
			text += "(args /" + rule.ArgsPattern + "/)"
		}
		why := fmt.Sprintf("priority %d", rule.Priority)
		if len(rule.Modes) > 0 {
			why += " · in " + strings.Join(rule.Modes, ", ")
		}
		out = append(out, Rule{Agent: "gemini", File: path, Scope: scope, Decision: d, Text: strings.TrimSpace(text), ReadOnly: true, Why: why})
	}
	return out, nil
}

// words is a TOML string or list of strings as one line.
func words(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case []any:
		var parts []string
		for _, p := range t {
			parts = append(parts, fmt.Sprint(p))
		}
		return strings.Join(parts, " | ")
	}
	return ""
}
