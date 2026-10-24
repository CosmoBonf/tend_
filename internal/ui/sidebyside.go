package ui

import (
	"strings"

	"github.com/mattn/go-runewidth"

	"github.com/auth-com-br/tend/internal/vt"
)

// "side by side..." is tend's own, the owner's: a popup in the middle of the
// screen listing the saved ssh connections. Picking one brings it beside the
// pane in view, or below it, without logging in again; a connection that is
// not open is opened there. It listed the panes of the other spaces too, under
// a spaces button, until the owner asked for ssh alone. herdr moves a pane
// only over its socket (pane.move), with no key or menu for it.

// SideBySideEntry is one thing that can be brought beside the pane in view.
type SideBySideEntry struct {
	Label string
	// Meta is said dimmed after the label: a pane's directory.
	Meta string
	// Status is said against the right edge: an agent's state, "● open".
	Status string
	// Pane is the pane to bring, or SSH the connection to bring or open.
	Pane uint64
	SSH  string
}

// SideBySide is the popup's state.
type SideBySide struct {
	SSH []SideBySideEntry
	// Query filters the list; typing goes to it.
	Query string
	// Selected is the cursor, as an index into the list.
	Selected int
	// Below puts what is picked below the pane in view rather than beside.
	Below bool
	Error string
}

// Entries is the list, unfiltered.
func (s SideBySide) Entries() []SideBySideEntry { return s.SSH }

// Filtered is the entries that the query leaves, by index.
func (s SideBySide) Filtered() []int {
	q := strings.ToLower(strings.TrimSpace(s.Query))
	var out []int
	for i, e := range s.Entries() {
		if q == "" || strings.Contains(strings.ToLower(e.Label+" "+e.Meta+" "+e.Status), q) {
			out = append(out, i)
		}
	}
	return out
}

// SelectedEntry is the entry the cursor is on, or the first the filter
// leaves when the cursor is on one it hid.
func (s SideBySide) SelectedEntry() (SideBySideEntry, int, bool) {
	f := s.Filtered()
	for _, i := range f {
		if i == s.Selected {
			return s.Entries()[i], i, true
		}
	}
	if len(f) > 0 {
		return s.Entries()[f[0]], f[0], true
	}
	return SideBySideEntry{}, 0, false
}

// SideBySideRect is the popup: 76 wide at most, as tall as the list needs
// between 11 and 24 rows, in the middle. It does not change size as the
// filter narrows the list, so the buttons stay under the pointer.
func SideBySideRect(s SideBySide, cols, rows int) Rect {
	r := Rect{Cols: min(76, cols), Rows: min(min(max(len(s.SSH)+7, 11), 24), rows)}
	r.X, r.Y = (cols-r.Cols)/2, (rows-r.Rows)/2
	return r
}

// sideBySideBody is where the entries go: under the title, the filter and
// the rule, over the placement and the keys.
func sideBySideBody(s SideBySide, cols, rows int) Rect {
	r := SideBySideRect(s, cols, rows)
	return Rect{X: r.X + 1, Y: r.Y + 4, Cols: r.Cols - 2, Rows: max(r.Rows-7, 0)}
}

// sideBySidePlaces are the placement buttons, beside then below, on the
// line over the keys.
func sideBySidePlaces(s SideBySide, cols, rows int) []Rect {
	r := SideBySideRect(s, cols, rows)
	x, y := r.X+2+runewidth.StringWidth("place "), r.Y+r.Rows-3
	out := make([]Rect, 2)
	for i, name := range []string{"beside", "below"} {
		w := runewidth.StringWidth(" " + name + " ")
		out[i] = Rect{X: x, Y: y, Cols: w, Rows: 1}
		x += w + 1
	}
	return out
}

// sideBySideStart is the first entry shown, of the filtered, keeping the
// selection in view.
func sideBySideStart(s SideBySide, visible int) int {
	f := s.Filtered()
	_, sel, _ := s.SelectedEntry()
	pos := 0
	for i, idx := range f {
		if idx == sel {
			pos = i
		}
	}
	return max(min(pos-(visible-1), len(f)-visible), 0)
}

// SideBySideHit is what a point in the popup is on.
type SideBySideHit struct {
	Inside bool
	Close  bool
	// Place is a placement button (0 beside, 1 below), Entry an entry of
	// the list; -1 for none.
	Place, Entry int
}

// SideBySideAt is what is at a point, from the same geometry the popup is
// drawn with.
func SideBySideAt(s SideBySide, cols, rows, x, y int) SideBySideHit {
	hit := SideBySideHit{Place: -1, Entry: -1}
	r := SideBySideRect(s, cols, rows)
	if x < r.X || x >= r.X+r.Cols || y < r.Y || y >= r.Y+r.Rows {
		return hit
	}
	hit.Inside = true
	if OnCloseMark(r, x, y) {
		hit.Close = true
		return hit
	}
	in := func(b Rect) bool { return y == b.Y && x >= b.X && x < b.X+b.Cols }
	for i, b := range sideBySidePlaces(s, cols, rows) {
		if in(b) {
			hit.Place = i
		}
	}
	body := sideBySideBody(s, cols, rows)
	if y >= body.Y && y < body.Y+body.Rows {
		f := s.Filtered()
		at := sideBySideStart(s, max(body.Rows, 1)) + (y - body.Y)
		if at < len(f) {
			hit.Entry = f[at]
		}
	}
	return hit
}

// SideBySideCursor is where the cursor goes: at the end of the filter,
// which is where typing lands.
func SideBySideCursor(s SideBySide, cols, rows int) (int, int, bool) {
	r := SideBySideRect(s, cols, rows)
	return r.X + 1 + 3 + runewidth.StringWidth(s.Query), r.Y + 2, true
}

func drawSideBySide(dst *vt.Grid, s SideBySide, theme Theme) {
	cols, rows := dst.Cols(), dst.Rows()
	r := SideBySideRect(s, cols, rows)
	if r.Cols < 30 || r.Rows < 10 {
		return
	}
	for y := r.Y; y < r.Y+r.Rows; y++ {
		fill(dst, y, r.X, r.X+r.Cols, theme.Menu)
	}
	drawBox(dst, r, theme.BorderFocused)
	drawCloseMark(dst, r, theme.BorderFocused)
	inner := Rect{X: r.X + 1, Y: r.Y + 1, Cols: r.Cols - 2, Rows: r.Rows - 2}
	limit := inner.X + inner.Cols
	bold, dim := theme.Menu, theme.Menu
	bold.Attrs |= vt.AttrBold
	dim.Attrs |= vt.AttrDim
	writeString(dst, inner.X+1, inner.Y, "side by side · ssh", bold, limit)

	filter, style := " / type to filter", dim
	if s.Query != "" {
		filter, style = " / "+s.Query, theme.Menu
	}
	writeString(dst, inner.X, inner.Y+1, truncate(filter, inner.Cols), style, limit)
	writeString(dst, inner.X, inner.Y+2, strings.Repeat("─", inner.Cols), dim, limit)

	body := sideBySideBody(s, cols, rows)
	f := s.Filtered()
	_, sel, _ := s.SelectedEntry()
	start := sideBySideStart(s, max(body.Rows, 1))
	for i := 0; i < body.Rows && start+i < len(f); i++ {
		e := s.Entries()[f[start+i]]
		y := body.Y + i
		top, under := theme.Menu, dim
		if f[start+i] == sel {
			top, under = theme.MenuSelected, theme.MenuSelected
			fill(dst, y, body.X, limit, top)
		}
		statusW := 0
		if e.Status != "" {
			statusW = runewidth.StringWidth(e.Status) + 2
			writeString(dst, limit-statusW+1, y, e.Status, top, limit)
		}
		room := body.Cols - statusW - 1
		x := writeString(dst, body.X, y, " "+truncate(e.Label, room-1), top, limit-statusW)
		if e.Meta != "" && x+3 < limit-statusW {
			writeString(dst, x+2, y, truncate(e.Meta, limit-statusW-x-3), under, limit-statusW)
		}
	}
	if len(f) == 0 {
		empty := " no ssh connections saved"
		if s.Query != "" {
			empty = " nothing matches"
		}
		writeString(dst, body.X, body.Y, empty, dim, limit)
	}

	places := sideBySidePlaces(s, cols, rows)
	writeString(dst, inner.X+1, places[0].Y, "place", dim, limit)
	for i, b := range places {
		style := theme.NotesSub
		if (i == 1) == s.Below {
			style = theme.NotesButton
		}
		writeString(dst, b.X, b.Y, " "+[]string{"beside", "below"}[i]+" ", style, limit)
	}
	if s.Error != "" {
		x := places[1].X + places[1].Cols + 2
		writeString(dst, x, places[0].Y, truncate(s.Error, limit-x), theme.Blocked, limit)
	}
	writeString(dst, inner.X, inner.Y+inner.Rows-1, " ↵ bring · ctrl+d below · esc", dim, limit)
}
