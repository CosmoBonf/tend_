package perm

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/auth-com-br/tend/internal/agentfile"
)

// dshAgent is the DeepSeek Harness (0.1.2-rc.1). It keeps no rules: an
// approval is good once (dsh-user-approval's outcomes are allowed-once,
// rejected, cancelled), and what lasts is the preset a new session starts
// with — dsh-permission-presets' `permission.defaultPreset` in
// settings.yaml, a sandbox and an approval policy together. That one
// setting is what the panel edits, keeping the file's comments (yaml.v3's
// tree, as the MCP manager edits cordis.patch.yml).
type dshAgent struct{ backups string }

func newDsh(backups string) Adapter { return &dshAgent{backups: backups} }

func (d *dshAgent) ID() string   { return "dsh" }
func (d *dshAgent) Name() string { return "DeepSeek Harness" }

// dshPresets are dsh-base's presets: read-only and workspace-write ask
// before leaving the sandbox; danger-full-access has none and never asks.
var dshPresets = []string{"read-only", "workspace-write", "danger-full-access"}

func dshSettings() string {
	dir := strings.TrimSpace(os.Getenv("DSH_HOME"))
	switch {
	case dir == "":
		dir = filepath.Join(home(), ".dsh")
	case strings.HasPrefix(dir, "~/"):
		dir = filepath.Join(home(), dir[2:])
	}
	return filepath.Join(dir, "settings.yaml")
}

func (d *dshAgent) Read(Place) Report {
	path := dshSettings()
	r := Report{
		Files:    []File{{Path: path, Scope: Global, Exists: exists(path), ModesOnly: true}},
		Effect:   "dsh starts its next new session with it; open sessions keep theirs",
		Warnings: []string{"dsh keeps no rules: each approval is good once, and the preset decides when it asks"},
	}
	value := ""
	if data, err := os.ReadFile(path); err == nil {
		var doc struct {
			Permission struct {
				DefaultPreset string `yaml:"defaultPreset"`
			} `yaml:"permission"`
		}
		if err := yaml.Unmarshal(data, &doc); err != nil {
			r.Error = fmt.Sprintf("%s: %v", path, err)
		}
		value = doc.Permission.DefaultPreset
	}
	r.Modes = []Mode{{Agent: "dsh", File: path, Scope: Global, Key: "permission.defaultPreset", Value: value, Choices: dshPresets,
		Help: "the sandbox and approval policy a new session starts with (unset: workspace-write)"}}
	return r
}

func (d *dshAgent) Check(string) error { return fmt.Errorf("dsh keeps no rules") }

func (d *dshAgent) Add(string, Decision, string) error { return fmt.Errorf("dsh keeps no rules") }

func (d *dshAgent) Remove(string, Decision, string) error { return ErrNoRule }

func (d *dshAgent) SetDecision(string, string, Decision, Decision) error { return ErrNoRule }

func (d *dshAgent) SetMode(file, key, value string) error {
	if key != "permission.defaultPreset" {
		return fmt.Errorf("dsh: %q: %w", key, ErrNoMode)
	}
	if value != "" && !contains(dshPresets, value) {
		return fmt.Errorf("the presets are %s", strings.Join(dshPresets, ", "))
	}
	return agentfile.Edit(file, options(d.backups, "dsh", file, 0o600), func(data []byte) ([]byte, error) {
		var doc yaml.Node
		if err := yaml.Unmarshal(data, &doc); err != nil {
			return nil, err
		}
		if doc.Kind == 0 {
			doc = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode}}}
		}
		top := doc.Content[0]
		if top.Kind != yaml.MappingNode {
			return nil, fmt.Errorf("settings.yaml is not a mapping; change it by hand")
		}
		perm := mappingValue(top, "permission")
		if perm == nil {
			if value == "" {
				return data, nil
			}
			perm = &yaml.Node{Kind: yaml.MappingNode}
			top.Content = append(top.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: "permission"}, perm)
		}
		if perm.Kind != yaml.MappingNode {
			return nil, fmt.Errorf("permission is not a mapping; change it by hand")
		}
		setScalar(perm, "defaultPreset", value)
		if len(perm.Content) == 0 {
			removeKey(top, "permission")
		}
		var b bytes.Buffer
		enc := yaml.NewEncoder(&b)
		enc.SetIndent(2)
		if err := enc.Encode(&doc); err != nil {
			return nil, err
		}
		if err := enc.Close(); err != nil {
			return nil, err
		}
		// What is written reads back with the one value as meant.
		var back struct {
			Permission struct {
				DefaultPreset string `yaml:"defaultPreset"`
			} `yaml:"permission"`
		}
		if err := yaml.Unmarshal(b.Bytes(), &back); err != nil || back.Permission.DefaultPreset != value {
			return nil, fmt.Errorf("the edited settings.yaml does not read back as meant; left as it was")
		}
		return b.Bytes(), nil
	})
}

func mappingValue(m *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

func setScalar(m *yaml.Node, key, value string) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			if value == "" {
				m.Content = append(m.Content[:i:i], m.Content[i+2:]...)
			} else {
				m.Content[i+1] = &yaml.Node{Kind: yaml.ScalarNode, Value: value}
			}
			return
		}
	}
	if value != "" {
		m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: key}, &yaml.Node{Kind: yaml.ScalarNode, Value: value})
	}
}

func removeKey(m *yaml.Node, key string) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content = append(m.Content[:i:i], m.Content[i+2:]...)
			return
		}
	}
}
