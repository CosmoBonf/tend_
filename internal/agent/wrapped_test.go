package agent

import "testing"

// TestAnAgentRunByARuntimeIsKnownByItsScript: node, bun, python and the
// shells running an agent's script are named after the script — how the
// DeepSeek Harness, `node …/bin/dsh`, is told apart from any other node —
// and not when they run code given inline or a module. If it regresses, an
// agent written in JavaScript or Python started from a shell is never
// recognised, and its pane never says it is working.
func TestAnAgentRunByARuntimeIsKnownByItsScript(t *testing.T) {
	for _, c := range []struct {
		runtime string
		argv    []string
		want    string
	}{
		{"node", []string{"node", "/home/u/.asdf/installs/nodejs/22/bin/dsh", "--profile", "tui"}, "dsh"},
		{"node", []string{"node", "--require", "x.js", "/opt/agent/cli.js"}, "cli"},
		{"node", []string{"node", "-e", "console.log(1)"}, ""},
		{"node", []string{"node", "--eval=1"}, ""},
		{"bun", []string{"bun", "/x/qwen.js"}, "qwen"},
		{"python3.12", []string{"python3.12", "/usr/bin/aider", "--yes"}, "aider"},
		{"python3", []string{"python3", "-m", "aider"}, ""},
		{"zsh", []string{"zsh", "-c", "claude"}, ""},
		{"bash", []string{"bash", "/x/run-agent.sh"}, "run-agent.sh"},
		{"claude", []string{"claude"}, ""},
		{"node", nil, ""},
	} {
		if got := WrappedAgentName(c.runtime, c.argv); got != c.want {
			t.Errorf("%s %v → %q, want %q", c.runtime, c.argv, got, c.want)
		}
	}
	for name, want := range map[string]bool{"node": true, "python3": true, "python3.12": true, "zsh": true, "claude": false, "pythonista": false} {
		if IsRuntimeOrShell(name) != want {
			t.Errorf("IsRuntimeOrShell(%q) = %v", name, !want)
		}
	}
}
