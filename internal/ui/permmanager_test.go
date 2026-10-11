package ui

import (
	"strings"
	"testing"

	"github.com/auth-com-br/tend/internal/vt"
)

// TestAClickInThePermissionsPanelLandsWhereItIsDrawn: a click on a tab is
// that agent, and a click on a rule's line is that rule. If it regresses,
// a click opens another agent's tab, or space then changes a rule the
// pointer was not on.
func TestAClickInThePermissionsPanelLandsWhereItIsDrawn(t *testing.T) {
	v := &PermManagerView{
		Where: "project ~/proj",
		Tabs:  []PermTab{{Name: "Claude Code", Rules: 2}, {Name: "Codex", Rules: 1}, {Name: "Cursor", Rules: 1, Warn: true}},
		Lines: []PermLine{
			{Mode: true, Scope: "global", Text: "permissions.defaultMode = (not set)"},
			{Decision: "allow", Scope: "local", Text: "Bash(make test)", Elsewhere: true},
			{Decision: "deny", Scope: "global", Text: "Bash(rm -rf *)", Why: "a reason"},
		},
	}
	const cols, rows = 120, 30
	g := vt.NewGrid(cols, rows, 0)
	drawPermManager(g, v, DefaultTheme())
	lines := gridText(g)
	geo := PermManagerLayout(v, cols, rows)

	for i, name := range []string{"Claude Code 2", "Codex 1", "Cursor 1 ⚠"} {
		x := runeIndex(lines[geo.TabY], name)
		if x < 0 {
			t.Fatalf("tab %q not drawn:\n%s", name, strings.Join(lines, "\n"))
		}
		if tab, ok := PermManagerTabAt(v, cols, rows, x+1, geo.TabY); !ok || tab != i {
			t.Errorf("a click on %q lands on tab %d (%v)", name, tab, ok)
		}
	}
	for i, text := range []string{"permissions.defaultMode", "Bash(make test)", "Bash(rm -rf *)"} {
		y := -1
		for row, line := range lines {
			if strings.Contains(line, text) {
				y = row
			}
		}
		if y < 0 {
			t.Fatalf("%q not drawn:\n%s", text, strings.Join(lines, "\n"))
		}
		if row, ok := PermManagerRowAt(v, cols, rows, geo.TextX, y); !ok || row != i {
			t.Errorf("a click on %q lands on line %d (%v)", text, row, ok)
		}
	}
	if !strings.Contains(lines[geo.List.Y+2], "⚠") || !strings.Contains(lines[geo.List.Y+1], "≡") {
		t.Errorf("the marks are not drawn:\n%s", strings.Join(lines, "\n"))
	}
}

func runeIndex(line, s string) int {
	i := strings.Index(line, s)
	if i < 0 {
		return -1
	}
	return len([]rune(line[:i]))
}
