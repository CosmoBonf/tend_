package mcp

import (
	"path/filepath"
	"strings"
	"testing"
)

// dshFixture is a machine patch layer as people write it: comments, a row
// that is not about MCP, a !!js header, and a server that is off.
const dshFixture = `# my dsh preferences
- id: session-telemetry-otel
  disabled: true
- insert:
    # memory for every profile
    - id: mcp-memory
      name: '@deepseek-ai/dsh-mcp-client'
      config:
        serverName: memory
        transport: stdio
        command: npx
        args: ['-y', '@modelcontextprotocol/server-memory']
        toolCallTimeoutMs: 30000
    - id: mcp-web
      name: '@deepseek-ai/dsh-mcp-client'
      disabled: true
      config:
        serverName: web
        transport: streamable-http
        url: http://localhost:3000/mcp
        headers:
          Authorization: !!js '` + "`Bearer ${process.env.MCP_TOKEN}`" + `'
`

func dshPatch(home string) string { return filepath.Join(home, ".dsh", "cordis.patch.yml") }

// TestDshListsItsPatchLayersServers: the servers are the dsh-mcp-client
// entries of ~/.dsh/cordis.patch.yml, off when the entry is disabled, with
// a !!js value and dsh's own settings named as extras. If it regresses,
// the DeepSeek Harness column shows nothing, or an off server as on.
func TestDshListsItsPatchLayersServers(t *testing.T) {
	home, m := fakeHome(t)
	write(t, dshPatch(home), dshFixture)
	mem, ok := entry(t, m, "dsh", "memory")
	if !ok || !mem.Enabled || mem.Def.Command != "npx" || len(mem.Def.Args) != 2 {
		t.Fatalf("memory = %+v", mem)
	}
	if strings.Join(mem.Extras, ",") != "toolCallTimeoutMs" {
		t.Errorf("memory extras = %v", mem.Extras)
	}
	web, ok := entry(t, m, "dsh", "web")
	if !ok || web.Enabled || web.Via != "native" || web.Def.Transport != HTTP || web.Def.URL != "http://localhost:3000/mcp" {
		t.Fatalf("web = %+v", web)
	}
	if !strings.Contains(strings.Join(web.Extras, ","), "!!js") {
		t.Errorf("web extras = %v, want the !!js header named", web.Extras)
	}
}

// TestDshOffAndOnIsTheFileItWas: off is dsh's own disabled: true on the
// entry, and on takes it away again, leaving the comments, the other rows
// and the !!js value as they were. If it regresses, switching a server in
// tend rewrites what the owner wrote by hand.
func TestDshOffAndOnIsTheFileItWas(t *testing.T) {
	home, m := fakeHome(t)
	path := dshPatch(home)
	write(t, path, dshFixture)
	if err := m.SetEnabled("dsh", "memory", false); err != nil {
		t.Fatal(err)
	}
	got := read(t, path)
	if !strings.Contains(got, "    - id: mcp-memory\n      name: '@deepseek-ai/dsh-mcp-client'\n      disabled: true\n") {
		t.Errorf("off wrote:\n%s", got)
	}
	if e, _ := entry(t, m, "dsh", "memory"); e.Enabled {
		t.Error("memory still on")
	}
	if err := m.SetEnabled("dsh", "memory", true); err != nil {
		t.Fatal(err)
	}
	if err := m.SetEnabled("dsh", "web", true); err != nil {
		t.Fatal(err)
	}
	if err := m.SetEnabled("dsh", "web", false); err != nil {
		t.Fatal(err)
	}
	if got := read(t, path); got != dshFixture {
		t.Errorf("off and on again changed the file:\n%s", got)
	}
}

// TestCopyingIntoDshWritesAPluginEntry: a server copied from Claude Code
// into a dsh with no patch layer yet makes the file, with an insert row of
// dsh-mcp-client entries; SSE, and names dsh refuses, are not written. If
// it regresses, a copied server is one dsh cannot load.
func TestCopyingIntoDshWritesAPluginEntry(t *testing.T) {
	home, m := fakeHome(t)
	write(t, filepath.Join(home, ".claude.json"), `{"mcpServers": {
  "fs": {"type": "stdio", "command": "npx", "args": ["-y", "server-fs", "/tmp"], "env": {"MODE": "ro"}},
  "siyuan": {"type": "http", "url": "http://127.0.0.1:6806/mcp"},
  "old": {"type": "sse", "url": "http://localhost:9/sse"},
  "My Server": {"type": "stdio", "command": "x"}
}}`)
	for _, name := range []string{"fs", "siyuan"} {
		if _, err := m.Copy("claude", name, "dsh"); err != nil {
			t.Fatal(name, err)
		}
	}
	got := read(t, dshPatch(home))
	for _, want := range []string{
		"- insert:\n    - id: mcp-fs\n      name: '@deepseek-ai/dsh-mcp-client'\n      config:\n        serverName: fs\n        transport: stdio\n        command: npx\n        args: [-y, server-fs, /tmp]\n        env:\n          MODE: ro\n",
		"    - id: mcp-siyuan\n      name: '@deepseek-ai/dsh-mcp-client'\n      config:\n        serverName: siyuan\n        transport: streamable-http\n        url: http://127.0.0.1:6806/mcp\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("file lacks\n%s\nin\n%s", want, got)
		}
	}
	if e, ok := entry(t, m, "dsh", "fs"); !ok || e.Def.Env["MODE"] != "ro" || len(e.Def.Args) != 3 {
		t.Errorf("fs read back as %+v", e)
	}
	if _, err := m.Copy("claude", "old", "dsh"); err == nil {
		t.Error("an SSE server was written into dsh")
	}
	if _, err := m.Copy("claude", "My Server", "dsh"); err == nil {
		t.Error("a name dsh-mcp-client refuses was written")
	}
}

// TestRemovingTheLastDshServerTakesItsRow: removing a server takes its
// entry, and the insert row with it once it is empty; the rest of the file
// stays. If it regresses, dsh is left an empty insert, or loses a row.
func TestRemovingTheLastDshServerTakesItsRow(t *testing.T) {
	home, m := fakeHome(t)
	path := dshPatch(home)
	write(t, path, dshFixture)
	for _, name := range []string{"web", "memory"} {
		if err := m.Remove("dsh", name); err != nil {
			t.Fatal(err)
		}
	}
	if got := read(t, path); got != "# my dsh preferences\n- id: session-telemetry-otel\n  disabled: true\n" {
		t.Errorf("left:\n%q", got)
	}
}
