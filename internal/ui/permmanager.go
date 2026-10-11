package ui

import (
	"strconv"

	"github.com/mattn/go-runewidth"

	"github.com/auth-com-br/tend/internal/vt"
)

// The permissions manager is tend's own (herdr has none): a panel over the
// screen with a tab per agent and, under it, what that agent may do without
// asking — its modes first, then its rules, each with the file it is in.
// It is drawn as the MCP manager is, the two being one kind of thing. What
// the keys do is the client's; this draws, and says where a click landed.

// PermTab is one agent's tab.
type PermTab struct {
	Name  string
	Rules int
	// Warn marks an agent with something to say: a list it does not read,
	// a file that could not be read.
	Warn bool
}

// PermLine is one line of the list: a mode, or a rule.
type PermLine struct {
	// Mode is set for a mode's line, whose Text is "key = value".
	Mode     bool
	Decision string
	Scope    string
	Text     string
	// Elsewhere and Why are the rule's marks: the same rule in another
	// file, and why it does not do what it seems to.
	Elsewhere bool
	Why       string
	ReadOnly  bool
}

// PermManagerView is the panel while it is up.
type PermManagerView struct {
	// Where says what project the panel is for.
	Where string
	Tabs  []PermTab
	Tab   int
	// Notes are said under the tabs: the agent's warnings, and when a
	// change takes effect.
	Notes   []string
	Lines   []PermLine
	Row     int
	Scroll  int
	Loading bool
	Message string
	Alert   bool
	// Confirm is a question waiting for enter (yes) or esc (no).
	Confirm string
	// Typing is a line being typed — a rule, a filter — with its label.
	Typing     bool
	TypeLabel  string
	TypeText   string
	TypeHint   string
	Filter     string
	Detail     []string
	EmptyLabel string
}

// PermManagerGeometry is where the panel's parts are.
type PermManagerGeometry struct {
	Box  Rect
	List Rect
	// TabX are where the tabs start, TabW their widths, on the row TabY.
	TabX, TabW []int
	TabY       int
	// DecisionX, ScopeX, MarkX and TextX are the list's columns.
	DecisionX, ScopeX, MarkX, TextX int
	Add, Edit, Editor, Refresh      Rect
	Help, Close                     Rect
}

// PermHelp is the panel's help, for somebody meeting it for the first time.
var PermHelp = []string{
	"WHAT IS THIS",
	"  Each coding agent keeps a list of what it may do without asking you:",
	"  commands it may run, files it may edit, sites it may fetch. Pressing",
	"  \"always allow\" in an agent adds to it. This shows each agent's list, for",
	"  the project of the pane you opened it over, and lets you change it.",
	"",
	"THE COLUMNS",
	"  allow / ask / deny  what the agent does when the rule matches",
	"  global              yours, in every project",
	"  project             the project's, kept in its repository",
	"  local               yours, in this project only (not committed)",
	"  ≡  the same rule is in another of the agent's files",
	"  ⚠  the rule does not do what it seems to (enter says why)",
	"",
	"WHAT YOU CAN DO",
	"  ← →  another agent          ↑ ↓  another rule",
	"  space   change what the rule says (allow → ask → deny), or a mode's value",
	"  a       add a rule: type it, then pick allow/ask/deny and the file",
	"  d       remove the rule (asks first; a backup is kept)",
	"  m       move it to another of the agent's files (local → global, say)",
	"  e       open its file in your editor; E picks the editor; o picks the file",
	"  /       show only the rules with some text in them",
	"  enter   the rule's file and what its marks mean",
	"  right-click or the menu key: all of this, in words",
	"",
	"  Claude Code reads a change at once; the others in their next session.",
}

const (
	permDecisionW = 6
	permScopeW    = 8
)

// PermManagerLayout is the panel's geometry on a screen of cols by rows.
func PermManagerLayout(v *PermManagerView, cols, rows int) PermManagerGeometry {
	w := min(cols, 124)
	h := min(rows, max(len(v.Lines)+12, len(v.Detail)+8, 16))
	box := Rect{X: (cols - w) / 2, Y: (rows - h) / 2, Cols: w, Rows: h}
	g := PermManagerGeometry{Box: box, TabY: box.Y + 2}
	x := box.X + 2
	for _, tab := range v.Tabs {
		width := runewidth.StringWidth(permTabLabel(tab))
		g.TabX = append(g.TabX, x)
		g.TabW = append(g.TabW, width)
		x += width + 2
	}
	g.List = Rect{X: box.X + 2, Y: box.Y + 6, Cols: box.Cols - 4, Rows: max(box.Rows-12, 0)}
	g.DecisionX = g.List.X
	g.ScopeX = g.DecisionX + permDecisionW + 1
	g.MarkX = g.ScopeX + permScopeW + 1
	g.TextX = g.MarkX + 3
	bottom := box.Y + box.Rows - 2
	at := box.X + 2
	button := func(label string) Rect {
		r := Rect{X: at, Y: bottom, Cols: runewidth.StringWidth(label), Rows: 1}
		at += r.Cols + 2
		return r
	}
	g.Add = button("[ Add ]")
	g.Edit = button("[ Edit file ]")
	g.Editor = button("[ Editor… ]")
	g.Refresh = button("[ Refresh ]")
	g.Help = button("[ Help ]")
	g.Close = Rect{X: box.X + box.Cols - 2 - runewidth.StringWidth("[ Close ]"), Y: bottom, Cols: runewidth.StringWidth("[ Close ]"), Rows: 1}
	return g
}

// permTabLabel is how a tab reads: the agent and how many rules it has.
func permTabLabel(t PermTab) string {
	s := t.Name + " " + strconv.Itoa(t.Rules)
	if t.Warn {
		s += " ⚠"
	}
	return s
}

func permTop(v *PermManagerView, g PermManagerGeometry) int {
	return min(v.Scroll, max(len(v.Lines)-g.List.Rows, 0))
}

// PermManagerTabAt is the tab at a point, if any.
func PermManagerTabAt(v *PermManagerView, cols, rows, x, y int) (int, bool) {
	g := PermManagerLayout(v, cols, rows)
	if y != g.TabY {
		return 0, false
	}
	for i, tx := range g.TabX {
		if x >= tx && x < tx+g.TabW[i] {
			return i, true
		}
	}
	return 0, false
}

// PermManagerRowAt is the list's line at a point, if any.
func PermManagerRowAt(v *PermManagerView, cols, rows, x, y int) (int, bool) {
	g := PermManagerLayout(v, cols, rows)
	if v.Detail != nil || x < g.List.X || x >= g.List.X+g.List.Cols || y < g.List.Y || y >= g.List.Y+g.List.Rows {
		return 0, false
	}
	row := permTop(v, g) + y - g.List.Y
	if row >= len(v.Lines) {
		return 0, false
	}
	return row, true
}

// PermManagerScrollFor is the scroll that keeps the cursor's line in view.
func PermManagerScrollFor(v *PermManagerView, cols, rows int) int {
	g := PermManagerLayout(v, cols, rows)
	top := permTop(v, g)
	if v.Row < top {
		top = v.Row
	}
	if g.List.Rows > 0 && v.Row >= top+g.List.Rows {
		top = v.Row - g.List.Rows + 1
	}
	return max(top, 0)
}

func drawPermManager(dst *vt.Grid, v *PermManagerView, theme Theme) {
	g := PermManagerLayout(v, dst.Cols(), dst.Rows())
	box := g.Box
	for y := box.Y; y < box.Y+box.Rows; y++ {
		for x := box.X; x < box.X+box.Cols; x++ {
			setCell(dst, x, y, ' ', theme.Notes)
		}
	}
	drawBox(dst, box, theme.NotesAccent)
	drawCloseMark(dst, box, withBold(theme.NotesAccent))
	if box.Rows < 14 || box.Cols < 60 {
		return
	}
	right := box.X + box.Cols - 1
	end := g.List.X + g.List.Cols
	title := "PERMISSIONS"
	if v.Where != "" {
		title += " · " + v.Where
	}
	title = truncateLeft(title, box.Cols-6)
	writeString(dst, box.X+(box.Cols-runewidth.StringWidth(title))/2, box.Y+1, title, withBold(theme.NotesAccent), right)
	for i, tab := range v.Tabs {
		style := theme.NotesSub
		if i == v.Tab {
			style = theme.NotesButton
		}
		writeString(dst, g.TabX[i], g.TabY, permTabLabel(tab), style, end)
	}
	for i, note := range v.Notes {
		if i >= 2 {
			break
		}
		style := theme.NotesSub
		if len(note) > 0 && note[0] == '!' {
			style = theme.Blocked
		}
		writeString(dst, g.List.X, box.Y+3+i, truncate(note, g.List.Cols), style, end)
	}

	if v.Detail != nil {
		for i, line := range v.Detail {
			if i >= box.Rows-8 {
				break
			}
			writeString(dst, g.List.X, box.Y+5+i, truncate(line, g.List.Cols), theme.Notes, end)
		}
	} else {
		head := box.Y + 5
		writeString(dst, g.DecisionX, head, "RULE", theme.NotesSub, end)
		writeString(dst, g.ScopeX, head, "SCOPE", theme.NotesSub, end)
		label := "WHAT IT MATCHES"
		if v.Filter != "" {
			label += "  (only: " + v.Filter + ")"
		}
		writeString(dst, g.TextX, head, label, theme.NotesSub, end)
		switch {
		case v.Loading:
			writeString(dst, g.List.X, g.List.Y, "reading the agents' settings…", theme.NotesSub, end)
		case len(v.Lines) == 0 && v.EmptyLabel != "":
			writeString(dst, g.List.X, g.List.Y, truncate(v.EmptyLabel, g.List.Cols), theme.NotesSub, end)
		}
		top := permTop(v, g)
		for line := 0; line < g.List.Rows && top+line < len(v.Lines); line++ {
			i := top + line
			l := v.Lines[i]
			y := g.List.Y + line
			text, decision := theme.Notes, theme.NotesSub
			switch l.Decision {
			case "allow":
				decision = theme.NotesAccent
			case "deny":
				decision = theme.Blocked
			case "ask":
				decision = theme.Notes
			}
			if l.ReadOnly || l.Mode {
				text = theme.NotesSub
			}
			if i == v.Row {
				text, decision = theme.NotesButton, theme.NotesButton
				for x := g.List.X; x < end; x++ {
					setCell(dst, x, y, ' ', text)
				}
			}
			word := l.Decision
			if l.Mode {
				word = "mode"
			}
			writeString(dst, g.DecisionX, y, word, decision, end)
			writeString(dst, g.ScopeX, y, l.Scope, text, end)
			mark := ""
			if l.Why != "" {
				mark = "⚠"
			}
			if l.Elsewhere {
				mark += "≡"
			}
			writeString(dst, g.MarkX, y, mark, text, end)
			writeString(dst, g.TextX, y, truncate(l.Text, max(end-g.TextX, 0)), text, end)
		}
	}

	msgY := box.Y + box.Rows - 4
	hintY := box.Y + box.Rows - 3
	switch {
	case v.Typing:
		line := v.TypeLabel + v.TypeText + "█"
		writeString(dst, box.X+2, msgY, truncateLeft(line, box.Cols-4), withBold(theme.Notes), right)
		writeString(dst, box.X+2, hintY, truncate(v.TypeHint, box.Cols-4), theme.NotesSub, right)
	case v.Confirm != "":
		writeString(dst, box.X+2, msgY, truncate(v.Confirm, box.Cols-4), withBold(theme.Notes), right)
		writeString(dst, box.X+2, hintY, "enter yes · esc no", theme.NotesSub, right)
	default:
		if v.Message != "" {
			style := theme.NotesSub
			if v.Alert {
				style = theme.Blocked
			}
			writeString(dst, box.X+2, msgY, truncate(v.Message, box.Cols-4), style, right)
		}
		hint := "← → agent · space change · a add · d remove · m move · e edit file · / filter · ? help · esc close"
		if v.Detail != nil {
			hint = "esc back · e edit this file"
		}
		writeString(dst, box.X+2, hintY, truncate(hint, box.Cols-4), theme.NotesSub, right)
	}
	for _, b := range []struct {
		r     Rect
		label string
	}{{g.Add, "[ Add ]"}, {g.Edit, "[ Edit file ]"}, {g.Editor, "[ Editor… ]"}, {g.Refresh, "[ Refresh ]"}, {g.Help, "[ Help ]"}, {g.Close, "[ Close ]"}} {
		writeString(dst, b.r.X, b.r.Y, b.label, theme.NotesAccent, right)
	}
}
