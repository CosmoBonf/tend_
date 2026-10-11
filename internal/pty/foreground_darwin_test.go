//go:build darwin

package pty

import "testing"

// TestAScriptIsNamedAfterItselfNotItsInterpreter: macOS names a process after
// the interpreter, Linux after the script. An agent shipped as a script
// (codex is "node /opt/homebrew/bin/codex") must read as the agent, or a Mac
// never sees it start; a shell at its prompt must stay a shell.
func TestAScriptIsNamedAfterItselfNotItsInterpreter(t *testing.T) {
	for _, c := range []struct {
		argv []string
		want string
	}{
		{[]string{"node", "/opt/homebrew/bin/codex"}, "codex"},
		{[]string{"node", "--no-warnings", "/usr/local/bin/gemini", "chat"}, "gemini"},
		{[]string{"/bin/sh", "/tmp/x/claude"}, "claude"},
		{[]string{"-zsh"}, ""},
		{[]string{"zsh", "-i"}, ""},
		{[]string{"sh", "-c", "exec claude"}, ""},
	} {
		if got := scriptName(c.argv); got != c.want {
			t.Errorf("scriptName(%q) = %q, want %q", c.argv, got, c.want)
		}
	}
}
