package integration

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ClaudeConfigDirEnv is Claude Code's override for its config directory.
const ClaudeConfigDirEnv = "CLAUDE_CONFIG_DIR"

// Env overrides matching herdr's integration/env.rs (Unix).
const (
	PiCodingAgentDirEnv        = "PI_CODING_AGENT_DIR"
	OmpConfigDirEnv            = "PI_CONFIG_DIR" // OMP reuses Pi's config-dir env name
	CodexHomeEnv               = "CODEX_HOME"
	CopilotHomeEnv             = "COPILOT_HOME"
	CursorConfigDirEnv         = "CURSOR_CONFIG_DIR"
	KimiCodeHomeEnv            = "KIMI_CODE_HOME"
	QoderConfigDirEnv          = "QODER_CONFIG_DIR"
	QwenHomeEnv                = "QWEN_HOME"
	AntigravityCLIConfigDirEnv = "ANTIGRAVITY_CLI_CONFIG_DIR"
	GrokConfigDirEnv           = "GROK_CONFIG_DIR"
	GrokHomeEnv                = "GROK_HOME"
	HermesHomeEnv              = "HERMES_HOME"
)

// HomeEnv is the variable that points an agent at a config directory of
// its own, which is where it keeps its login: two directories are two
// accounts (tend's [[accounts]]). "" is an agent with no such variable.
func HomeEnv(agent string) string {
	switch agent {
	case "claude":
		return ClaudeConfigDirEnv
	case "codex":
		return CodexHomeEnv
	case "copilot":
		return CopilotHomeEnv
	case "cursor":
		return CursorConfigDirEnv
	case "kimi":
		return KimiCodeHomeEnv
	case "qodercli":
		return QoderConfigDirEnv
	case "qwen":
		return QwenHomeEnv
	case "agy":
		return AntigravityCLIConfigDirEnv
	case "grok":
		return GrokHomeEnv
	case "pi":
		return PiCodingAgentDirEnv
	}
	return ""
}

// PiExtensionDir returns ~/.pi/agent/extensions, or $PI_CODING_AGENT_DIR/extensions.
func PiExtensionDir() (string, error) {
	agentDir, err := configDirFromEnvOrHome(PiCodingAgentDirEnv, ".pi", "agent")
	if err != nil {
		return "", err
	}
	return filepath.Join(agentDir, "extensions"), nil
}

// OmpExtensionDir returns the OMP extensions directory. When
// $PI_CODING_AGENT_DIR is set it wins (same as Pi), which is why InstallOmp
// refuses to run when that resolves to Pi's directory. Otherwise
// $HOME/$PI_CONFIG_DIR/agent/extensions, defaulting PI_CONFIG_DIR to ".omp".
func OmpExtensionDir() (string, error) {
	if v := os.Getenv(PiCodingAgentDirEnv); v != "" {
		expanded, err := expandTilde(v)
		if err != nil {
			return "", err
		}
		return filepath.Join(expanded, "extensions"), nil
	}
	configDir := os.Getenv(OmpConfigDirEnv)
	if configDir == "" {
		configDir = ".omp"
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, configDir, "agent", "extensions"), nil
}

// ClaudeDir returns ~/.claude, or $CLAUDE_CONFIG_DIR when set.
func ClaudeDir() (string, error) {
	return configDirFromEnvOrHome(ClaudeConfigDirEnv, ".claude")
}

// CodexDir returns ~/.codex, or $CODEX_HOME when set.
func CodexDir() (string, error) {
	return configDirFromEnvOrHome(CodexHomeEnv, ".codex")
}

// CopilotDir returns ~/.copilot, or $COPILOT_HOME when set.
func CopilotDir() (string, error) {
	return configDirFromEnvOrHome(CopilotHomeEnv, ".copilot")
}

// CursorDir returns ~/.cursor, or $CURSOR_CONFIG_DIR when set.
func CursorDir() (string, error) {
	return configDirFromEnvOrHome(CursorConfigDirEnv, ".cursor")
}

// DroidDir returns ~/.factory (Factory/Droid has no env override in herdr).
func DroidDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".factory"), nil
}

// DevinDir returns $XDG_CONFIG_HOME/devin, or ~/.config/devin.
func DevinDir() (string, error) {
	if v := os.Getenv("XDG_CONFIG_HOME"); v != "" {
		expanded, err := expandTilde(v)
		if err != nil {
			return "", err
		}
		return filepath.Join(expanded, "devin"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "devin"), nil
}

// KimiDir returns ~/.kimi-code, or $KIMI_CODE_HOME when set.
func KimiDir() (string, error) {
	return configDirFromEnvOrHome(KimiCodeHomeEnv, ".kimi-code")
}

// QodercliDir returns ~/.qoder, or $QODER_CONFIG_DIR when set.
func QodercliDir() (string, error) {
	return configDirFromEnvOrHome(QoderConfigDirEnv, ".qoder")
}

// QwenDir returns ~/.qwen, or $QWEN_HOME when set.
func QwenDir() (string, error) {
	return configDirFromEnvOrHome(QwenHomeEnv, ".qwen")
}

// LettaDir returns ~/.letta (no env override in herdr).
func LettaDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".letta"), nil
}

// MastracodeDir returns ~/.mastracode (no env override in herdr).
func MastracodeDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".mastracode"), nil
}

// AntigravityCLIDir returns ~/.gemini/config, or $ANTIGRAVITY_CLI_CONFIG_DIR.
// Antigravity CLI reads hooks.json from here, not from ~/.gemini/antigravity-cli.
func AntigravityCLIDir() (string, error) {
	return configDirFromEnvOrHome(AntigravityCLIConfigDirEnv, ".gemini", "config")
}

// GrokDir returns the grok config home. GROK_CONFIG_DIR is a tend/herdr test
// seam and wins when set; otherwise $GROK_HOME or ~/.grok (what the grok CLI
// actually reads).
func GrokDir() (string, error) {
	if v := os.Getenv(GrokConfigDirEnv); v != "" {
		return expandTilde(v)
	}
	return configDirFromEnvOrHome(GrokHomeEnv, ".grok")
}

// OpencodeDir returns ~/.config/opencode (no env override in herdr).
func OpencodeDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "opencode"), nil
}

// OpencodeStateDir returns $XDG_STATE_HOME/opencode, or ~/.local/state/opencode.
func OpencodeStateDir() (string, error) {
	if v := os.Getenv("XDG_STATE_HOME"); v != "" {
		expanded, err := expandTilde(v)
		if err != nil {
			return "", err
		}
		return filepath.Join(expanded, "opencode"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "state", "opencode"), nil
}

// KiloDir returns ~/.config/kilo (no env override in herdr).
func KiloDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "kilo"), nil
}

// HermesDir returns ~/.hermes, or $HERMES_HOME when set.
func HermesDir() (string, error) {
	return configDirFromEnvOrHome(HermesHomeEnv, ".hermes")
}

// HermesPluginDir returns ~/.hermes/plugins/tend-agent-state.
func HermesPluginDir() (string, error) {
	dir, err := HermesDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "plugins", HermesPluginInstallName), nil
}

func configDirFromEnvOrHome(envVar string, homeRelative ...string) (string, error) {
	if v := os.Getenv(envVar); v != "" {
		return expandTilde(v)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(append([]string{home}, homeRelative...)...), nil
}

func expandTilde(path string) (string, error) {
	if path == "~" {
		return os.UserHomeDir()
	}
	if strings.HasPrefix(path, "~/") || strings.HasPrefix(path, `~\`) {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, path[2:]), nil
	}
	if strings.HasPrefix(path, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, path[1:]), nil
	}
	return path, nil
}

// requireDir errors when dir is missing so install can tell the user to
// install the agent first.
func requireDir(dir, agent string) error {
	info, err := os.Stat(dir)
	if err != nil {
		if os.IsNotExist(err) {
			if agent == "claude" {
				return fmt.Errorf("claude directory not found at %s. install claude code first", dir)
			}
			return fmt.Errorf("%s directory not found at %s. install %s first", agent, dir, agent)
		}
		return err
	}
	if !info.IsDir() {
		if agent == "claude" {
			return fmt.Errorf("claude directory not found at %s. install claude code first", dir)
		}
		return fmt.Errorf("%s directory not found at %s. install %s first", agent, dir, agent)
	}
	return nil
}

// requireConfigDir matches herdr's "… config directory not found … install …"
// messages used by kimi, qodercli, qwen, letta, grok, and antigravity.
func requireConfigDir(dir, agentLabel string) error {
	return requireConfigDirHint(dir, agentLabel+" config directory", agentLabel)
}

// requireConfigDirHint is for agents whose install hint differs from the
// directory label (e.g. "github copilot cli", "cursor agent cli").
func requireConfigDirHint(dir, what, installHint string) error {
	info, err := os.Stat(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("%s not found at %s. install %s first", what, dir, installHint)
		}
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%s not found at %s. install %s first", what, dir, installHint)
	}
	return nil
}
