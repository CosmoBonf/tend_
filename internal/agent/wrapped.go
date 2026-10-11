package agent

import (
	"path"
	"strings"
)

// An agent written in JavaScript or Python runs as node, bun or python with
// the agent's script as an argument: the DeepSeek Harness shows up as `node
// …/bin/dsh`, so the process's name says "node" and nothing about which
// agent it is. herdr reads the script out of the arguments in that case
// (detect/mod.rs: wrapped_agent_name_from_runtime_argv, script_arg_agent_name,
// agent_name_from_path_token); this is that, for the runtimes and shells it
// handles on Unix. Windows' cmd and PowerShell, and Cursor's bundled node on
// Windows, are left out with the platform.

// IsRuntimeOrShell reports whether a process name is a language runtime or a
// shell, whose own name does not say which program it is running.
func IsRuntimeOrShell(name string) bool {
	n := lookupName(path.Base(name))
	switch n {
	case "sh", "bash", "zsh", "fish", "tmux", "node", "bun":
		return true
	}
	return isPython(n)
}

// WrappedAgentName is the name of the script a runtime or a shell is running,
// as an agent would be looked up by — "" when it runs code given inline (-e,
// -c), a module (python -m), or nothing. Whether the name is an agent is the
// caller's to look up.
func WrappedAgentName(runtime string, argv []string) string {
	if len(argv) == 0 {
		return ""
	}
	switch n := lookupName(path.Base(runtime)); {
	case n == "node", n == "bun":
		return scriptArgName(argv, []string{"-e", "--eval", "-p", "--print"}, nil)
	case isPython(n):
		return scriptArgName(argv, []string{"-c"}, []string{"-m"})
	case n == "sh", n == "bash", n == "zsh", n == "fish":
		return scriptArgName(argv, []string{"-c"}, nil)
	}
	return ""
}

// scriptArgName is the first argument after the runtime that is not an
// option: the script. An eval or module flag means there is none to name.
func scriptArgName(argv []string, evalFlags, moduleFlags []string) string {
	args := argv[1:]
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			if i+1 < len(args) {
				return pathTokenName(args[i+1])
			}
			return ""
		}
		if flagMatches(arg, evalFlags) || flagMatches(arg, moduleFlags) {
			return ""
		}
		if strings.HasPrefix(arg, "-") {
			if optionTakesValue(arg) {
				i++
			}
			continue
		}
		return pathTokenName(arg)
	}
	return ""
}

// flagMatches is herdr's: the flag itself, a short flag with its value
// joined (-e'code'), or a long one with = (--eval=code).
func flagMatches(arg string, flags []string) bool {
	for _, f := range flags {
		switch {
		case arg == f:
			return true
		case !strings.HasPrefix(f, "--") && strings.HasPrefix(arg, f) && len(arg) > len(f):
			return true
		case strings.HasPrefix(f, "--") && strings.HasPrefix(arg, f+"="):
			return true
		}
	}
	return false
}

// optionTakesValue are the runtimes' options whose value is the next
// argument, which is not the script.
func optionTakesValue(arg string) bool {
	switch arg {
	case "-r", "--require", "--loader", "--import", "--experimental-loader",
		"--inspect-port", "-W", "-X", "-S", "-L", "-o":
		return true
	}
	return false
}

// pathTokenName is a script path's name as an agent is looked up by:
// quotes trimmed, the last component, lowercased, without .js and its kin.
func pathTokenName(token string) string {
	t := strings.Trim(token, `"'`)
	if t == "" || strings.HasPrefix(t, "-") {
		return ""
	}
	return lookupName(path.Base(t))
}

func lookupName(name string) string {
	n := strings.ToLower(strings.TrimSpace(name))
	for _, suffix := range []string{".exe", ".cmd", ".bat", ".ps1", ".js"} {
		if strings.HasSuffix(n, suffix) {
			return strings.TrimSuffix(n, suffix)
		}
	}
	return n
}

// isPython is python, or python followed by a version (python3, python3.12).
func isPython(name string) bool {
	if name == "python" {
		return true
	}
	v, ok := strings.CutPrefix(name, "python")
	if !ok || v == "" {
		return false
	}
	for _, part := range strings.Split(v, ".") {
		if part == "" || strings.Trim(part, "0123456789") != "" {
			return false
		}
	}
	return true
}
