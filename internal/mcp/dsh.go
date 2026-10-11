package mcp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// The DeepSeek Harness (dsh) connects MCP servers through a plugin,
// @deepseek-ai/dsh-mcp-client, one plugin entry per server in a Cordis patch
// file. The machine's own patch layer, $DSH_HOME/cordis.patch.yml (~/.dsh),
// applies over every profile — the user scope the other agents have — and is
// the file kept here. It is a YAML list of patch rows: `- insert: [entries]`
// adds plugins, and an entry's `disabled: true` is dsh's own switch.
//
// The file is YAML that people write by hand, with comments and `!!js`
// values (`!!js process.env.TOKEN`), so it is edited as a YAML tree, not
// re-marshalled from data: yaml.v3 keeps comments, quoting and flow style,
// and every edit is parsed back and compared before it is written.

const dshMCPPlugin = "@deepseek-ai/dsh-mcp-client"

// dshServerName is dsh-mcp-client's own rule for serverName.
var dshServerName = regexp.MustCompile(`^[A-Za-z0-9_-]{1,32}$`)

// dshHome is the harness's home: $DSH_HOME, else ~/.dsh (dsh-home-paths).
func dshHome() (string, error) {
	if dir := strings.TrimSpace(os.Getenv("DSH_HOME")); dir != "" {
		if rest, ok := strings.CutPrefix(dir, "~/"); ok {
			home, err := os.UserHomeDir()
			if err != nil {
				return "", err
			}
			return filepath.Join(home, rest), nil
		}
		return filepath.Abs(dir)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".dsh"), nil
}

type dshAgent struct{ backups string }

func newDsh(backups string) Adapter { return &dshAgent{backups: backups} }

func (*dshAgent) ID() string   { return "dsh" }
func (*dshAgent) Name() string { return "DeepSeek Harness" }

// Supports: dsh-mcp-client speaks stdio and streamable HTTP, not SSE.
func (*dshAgent) Supports(t Transport) bool { return t == Stdio || t == HTTP }

func (*dshAgent) Path() (string, error) {
	dir, err := dshHome()
	return filepath.Join(dir, "cordis.patch.yml"), err
}

func (d *dshAgent) opts() writeOpts {
	return writeOpts{Backups: d.backups, BackupName: "dsh-cordis.patch.yml", Mode: 0o600}
}

// dshEntry is one MCP server's plugin entry where it stands in the tree.
type dshEntry struct {
	entry  *yaml.Node // the entry's mapping
	insert *yaml.Node // the insert list it is in
	row    *yaml.Node // the top-level row holding that list
	name   string
}

// dshParse is the file as a YAML tree; a missing or empty file is an empty
// list.
func dshParse(data []byte) (*yaml.Node, error) {
	var doc yaml.Node
	if len(bytes.TrimSpace(data)) > 0 {
		if err := yaml.Unmarshal(data, &doc); err != nil {
			return nil, fmt.Errorf("cordis.patch.yml: %w", err)
		}
	}
	if doc.Kind == 0 {
		doc = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.SequenceNode, Tag: "!!seq"}}}
	}
	if len(doc.Content) != 1 || doc.Content[0].Kind != yaml.SequenceNode {
		return nil, fmt.Errorf("cordis.patch.yml is not a list of patch rows; left as it is")
	}
	return &doc, nil
}

// dshEntries are the MCP servers' entries in the file's insert lists.
func dshEntries(doc *yaml.Node) []dshEntry {
	var out []dshEntry
	for _, row := range doc.Content[0].Content {
		insert := mapValue(row, "insert")
		if insert == nil || insert.Kind != yaml.SequenceNode {
			continue
		}
		for _, e := range insert.Content {
			if scalar(mapValue(e, "name")) != dshMCPPlugin {
				continue
			}
			name := scalar(mapValue(mapValue(e, "config"), "serverName"))
			if name == "" {
				continue
			}
			out = append(out, dshEntry{entry: e, insert: insert, row: row, name: name})
		}
	}
	return out
}

func findDsh(entries []dshEntry, name string) (dshEntry, bool) {
	for _, e := range entries {
		if Key(e.name) == Key(name) {
			return e, true
		}
	}
	return dshEntry{}, false
}

// mapValue is a mapping's value for key, or nil.
func mapValue(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

func scalar(n *yaml.Node) string {
	if n == nil || n.Kind != yaml.ScalarNode {
		return ""
	}
	return n.Value
}

// dshDef reads an entry's config. A `!!js` value is dsh's to evaluate; it is
// carried as written and named among the extras, so the detail says so.
func dshDef(e *yaml.Node) (Def, []string) {
	cfg := mapValue(e, "config")
	var d Def
	var extras []string
	js := false
	field := func(key string) string {
		n := mapValue(cfg, key)
		if n != nil && n.Tag == "!!js" {
			js = true
		}
		return scalar(n)
	}
	strMap := func(key string) map[string]string {
		n := mapValue(cfg, key)
		if n == nil || n.Kind != yaml.MappingNode {
			return nil
		}
		out := map[string]string{}
		for i := 0; i+1 < len(n.Content); i += 2 {
			if n.Content[i+1].Tag == "!!js" {
				js = true
			}
			out[n.Content[i].Value] = n.Content[i+1].Value
		}
		return out
	}
	if field("transport") == "streamable-http" {
		d = Def{Transport: HTTP, URL: field("url"), Headers: strMap("headers")}
	} else {
		d = Def{Transport: Stdio, Command: field("command"), Env: strMap("env")}
		if n := mapValue(cfg, "args"); n != nil && n.Kind == yaml.SequenceNode {
			for _, a := range n.Content {
				if a.Tag == "!!js" {
					js = true
				}
				d.Args = append(d.Args, a.Value)
			}
		}
	}
	if cfg != nil && cfg.Kind == yaml.MappingNode {
		for i := 0; i < len(cfg.Content); i += 2 {
			switch k := cfg.Content[i].Value; k {
			case "serverName", "transport", "command", "args", "env", "url", "headers":
			default:
				extras = append(extras, k)
			}
		}
	}
	if js {
		extras = append(extras, "!!js values")
	}
	sort.Strings(extras)
	return d, extras
}

func dshDisabled(e *yaml.Node) bool {
	n := mapValue(e, "disabled")
	return n != nil && n.Value == "true"
}

func (d *dshAgent) read() (*yaml.Node, error) {
	path, err := d.Path()
	if err != nil {
		return nil, err
	}
	data, _, err := readFile(path, 0)
	if err != nil {
		return nil, err
	}
	return dshParse(data)
}

func (d *dshAgent) List() ([]Entry, error) {
	doc, err := d.read()
	if err != nil {
		return nil, err
	}
	var out []Entry
	for _, e := range dshEntries(doc) {
		def, extras := dshDef(e.entry)
		entry := Entry{Agent: d.ID(), Name: e.name, Enabled: true, Def: def, Extras: extras}
		if dshDisabled(e.entry) {
			entry.Enabled, entry.Via = false, "native"
		}
		out = append(out, entry)
	}
	return out, nil
}

// Raw is the entry as JSON: dsh's servers are never stashed, so this is for
// showing, not for restoring.
func (d *dshAgent) Raw(name string) (string, json.RawMessage, error) {
	doc, err := d.read()
	if err != nil {
		return "", nil, err
	}
	e, ok := findDsh(dshEntries(doc), name)
	if !ok {
		return "", nil, fmt.Errorf("%s: %w: %s", d.Name(), ErrNoServer, name)
	}
	var v any
	if err := e.entry.Decode(&v); err != nil {
		// A !!js value does not decode; the definition is shown instead.
		def, _ := dshDef(e.entry)
		raw, _ := json.Marshal(def)
		return e.name, raw, nil
	}
	raw, err := json.Marshal(v)
	return e.name, raw, err
}

func (d *dshAgent) AddRaw(string, json.RawMessage) error {
	return fmt.Errorf("%s has a switch of its own; nothing of it is stashed", d.Name())
}

// edit applies change to the file's tree and writes it back, after
// checking that what was written reads back as the tree that was meant.
func (d *dshAgent) edit(change func(doc *yaml.Node) error) error {
	path, err := d.Path()
	if err != nil {
		return err
	}
	return editFile(path, d.opts(), func(data []byte) ([]byte, error) {
		doc, err := dshParse(data)
		if err != nil {
			return nil, err
		}
		if err := change(doc); err != nil {
			return nil, err
		}
		var b bytes.Buffer
		enc := yaml.NewEncoder(&b)
		enc.SetIndent(2)
		if err := enc.Encode(doc); err != nil {
			return nil, err
		}
		if err := enc.Close(); err != nil {
			return nil, err
		}
		back, err := dshParse(b.Bytes())
		if err != nil {
			return nil, fmt.Errorf("the edited cordis.patch.yml does not read back: %w", err)
		}
		if canonYAML(back) != canonYAML(doc) {
			return nil, fmt.Errorf("the edited cordis.patch.yml does not read back as written; left as it was")
		}
		return b.Bytes(), nil
	})
}

// canonYAML is a tree's content without its layout: what it means, for
// comparing an edit with what it reads back as.
func canonYAML(n *yaml.Node) string {
	var b strings.Builder
	var walk func(n *yaml.Node)
	walk = func(n *yaml.Node) {
		if n == nil {
			return
		}
		if n.Kind == yaml.AliasNode {
			walk(n.Alias)
			return
		}
		fmt.Fprintf(&b, "(%d %s %q", n.Kind, n.ShortTag(), n.Value)
		for _, c := range n.Content {
			walk(c)
		}
		b.WriteString(")")
	}
	walk(n)
	return b.String()
}

func (d *dshAgent) Add(name string, def Def) error {
	if !d.Supports(def.Transport) {
		return fmt.Errorf("%s cannot reach a server over %s", d.Name(), def.Transport)
	}
	if !dshServerName.MatchString(name) {
		return fmt.Errorf("%s names a server with letters, digits, - and _ only, up to 32: %q", d.Name(), name)
	}
	return d.edit(func(doc *yaml.Node) error {
		entries := dshEntries(doc)
		if _, ok := findDsh(entries, name); ok {
			return fmt.Errorf("%s %w: %s", d.Name(), ErrExists, name)
		}
		entry := dshEntryNode(dshUniqueID(doc, "mcp-"+strings.ToLower(name)), name, def)
		// Beside the other servers when there are some; else a row of
		// their own, at the end, saying whose they are.
		if len(entries) > 0 {
			last := entries[len(entries)-1].insert
			last.Content = append(last.Content, entry)
			return nil
		}
		row := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map",
			HeadComment: "MCP servers (dsh-mcp-client), one entry each; tend's MCP manager (prefix+M)\nadds, removes and switches them, and editing them here is as good.",
			Content: []*yaml.Node{
				yamlStr("insert"),
				{Kind: yaml.SequenceNode, Tag: "!!seq", Content: []*yaml.Node{entry}},
			}}
		doc.Content[0].Content = append(doc.Content[0].Content, row)
		return nil
	})
}

// dshEntryNode is a server's plugin entry as dsh-mcp-client documents it.
func dshEntryNode(id, name string, def Def) *yaml.Node {
	cfg := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	add := func(k string, v *yaml.Node) { cfg.Content = append(cfg.Content, yamlStr(k), v) }
	add("serverName", yamlStr(name))
	if def.Transport == Stdio {
		add("transport", yamlStr("stdio"))
		add("command", yamlStr(def.Command))
		if len(def.Args) > 0 {
			args := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Style: yaml.FlowStyle}
			for _, a := range def.Args {
				args.Content = append(args.Content, yamlStr(a))
			}
			add("args", args)
		}
		if len(def.Env) > 0 {
			add("env", strMapNode(def.Env))
		}
	} else {
		add("transport", yamlStr("streamable-http"))
		add("url", yamlStr(def.URL))
		if len(def.Headers) > 0 {
			add("headers", strMapNode(def.Headers))
		}
	}
	return &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Content: []*yaml.Node{
		yamlStr("id"), yamlStr(id),
		yamlStr("name"), {Kind: yaml.ScalarNode, Tag: "!!str", Value: dshMCPPlugin, Style: yaml.SingleQuotedStyle},
		yamlStr("config"), cfg,
	}}
}

// yamlStr is a string scalar, quoted by the encoder only when it has to be.
func yamlStr(s string) *yaml.Node { return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: s} }

func strMapNode(m map[string]string) *yaml.Node {
	n := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		n.Content = append(n.Content, yamlStr(k), yamlStr(m[k]))
	}
	return n
}

// dshUniqueID is an entry id no other row of the file has; a clash would
// make dsh patch one entry with the other.
func dshUniqueID(doc *yaml.Node, base string) string {
	used := map[string]bool{}
	var walk func(n *yaml.Node)
	walk = func(n *yaml.Node) {
		if n == nil {
			return
		}
		if id := scalar(mapValue(n, "id")); id != "" {
			used[id] = true
		}
		for _, c := range n.Content {
			walk(c)
		}
	}
	walk(doc)
	id := base
	for i := 2; used[id]; i++ {
		id = fmt.Sprintf("%s-%d", base, i)
	}
	return id
}

func (d *dshAgent) Remove(name string) error {
	return d.edit(func(doc *yaml.Node) error {
		e, ok := findDsh(dshEntries(doc), name)
		if !ok {
			return fmt.Errorf("%s: %w: %s", d.Name(), ErrNoServer, name)
		}
		e.insert.Content = without(e.insert.Content, e.entry)
		// An insert row left with nothing to insert goes with it, unless it
		// says more than that.
		if len(e.insert.Content) == 0 && len(e.row.Content) == 2 {
			doc.Content[0].Content = without(doc.Content[0].Content, e.row)
		}
		return nil
	})
}

func (d *dshAgent) SetEnabled(name string, on bool) error {
	return d.edit(func(doc *yaml.Node) error {
		e, ok := findDsh(dshEntries(doc), name)
		if !ok {
			return fmt.Errorf("%s: %w: %s", d.Name(), ErrNoServer, name)
		}
		c := e.entry.Content
		for i := 0; i+1 < len(c); i += 2 {
			if c[i].Value == "disabled" {
				// On takes the field away, so off and on again is the file
				// it was.
				e.entry.Content = append(c[:i:i], c[i+2:]...)
				break
			}
		}
		if !on {
			// After name, where a reader looks for it.
			at := len(e.entry.Content)
			for i := 0; i+1 < len(e.entry.Content); i += 2 {
				if e.entry.Content[i].Value == "name" {
					at = i + 2
				}
			}
			val := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: "true"}
			e.entry.Content = append(e.entry.Content[:at:at], append([]*yaml.Node{yamlStr("disabled"), val}, e.entry.Content[at:]...)...)
		}
		return nil
	})
}

func without(list []*yaml.Node, n *yaml.Node) []*yaml.Node {
	out := list[:0:0]
	for _, x := range list {
		if x != n {
			out = append(out, x)
		}
	}
	return out
}
