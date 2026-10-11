// Package web is `tend web`: a page for a phone, served on the home network
// (or a VPN), to watch the agents of a session and talk to them. It is a
// client of the session like the TUI, and it can do less than the TUI on
// purpose: read the agents, send an agent a message or one of the keys an
// agent asks for, wake it and put it to sleep — never type into a shell.
// herdr has nothing like it; the owner asked for it (docs/PORTING.md).
package web

import (
	"fmt"
	"html"
	"strings"

	"github.com/auth-com-br/tend/internal/vt"
)

// ScreenHTML draws a pane's screen — the whole screen as ANSI, as the
// session sends it — as HTML: one line a row, runs of one style as spans.
// It is drawn here rather than by a terminal emulator in the page, so the
// page is light on a phone and its text selects and copies as text does.
func ScreenHTML(ansi []byte, cols, rows int) string {
	if cols <= 0 || rows <= 0 {
		return ""
	}
	s := vt.NewScreen(cols, rows, 0)
	_, _ = s.Write(ansi)
	grid := s.Grid()
	var b strings.Builder
	last := -1
	lines := make([]string, grid.Rows())
	for y := 0; y < grid.Rows(); y++ {
		lines[y] = rowHTML(grid.Line(y), cols)
		if lines[y] != "" {
			last = y
		}
	}
	for y := 0; y <= last; y++ {
		if y > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(lines[y])
	}
	return b.String()
}

// rowHTML is one row: its cells grouped into runs of one style, the blank
// cells at its end dropped.
func rowHTML(r *vt.Row, cols int) string {
	if cols > r.Len() {
		cols = r.Len()
	}
	end := cols
	for end > 0 {
		c := r.Cell(end - 1)
		if (c.R == 0 || c.R == ' ') && c.Style.BG.IsDefault() && c.Style.Attrs&vt.AttrReverse == 0 {
			end--
			continue
		}
		break
	}
	var b strings.Builder
	var run strings.Builder
	var style vt.Style
	open := false
	flush := func() {
		if run.Len() == 0 {
			return
		}
		if css := styleCSS(style); css != "" {
			fmt.Fprintf(&b, `<span style="%s">%s</span>`, css, html.EscapeString(run.String()))
		} else {
			b.WriteString(html.EscapeString(run.String()))
		}
		run.Reset()
	}
	for x := 0; x < end; x++ {
		c := r.Cell(x)
		if c.IsContinuation() {
			continue
		}
		if !open || c.Style != style {
			flush()
			style, open = c.Style, true
		}
		if c.R == 0 {
			run.WriteByte(' ')
		} else {
			run.WriteRune(c.R)
		}
		for _, m := range r.Combining(x) {
			run.WriteRune(m)
		}
	}
	flush()
	return b.String()
}

// styleCSS is a cell style as inline CSS; "" for the default.
func styleCSS(s vt.Style) string {
	fg, bg := colorCSS(s.FG), colorCSS(s.BG)
	if s.Attrs&vt.AttrReverse != 0 {
		// The page's own colours stand in for "default" when they swap.
		if fg == "" {
			fg = "var(--fg)"
		}
		if bg == "" {
			bg = "var(--bg)"
		}
		fg, bg = bg, fg
	}
	var parts []string
	if fg != "" {
		parts = append(parts, "color:"+fg)
	}
	if bg != "" {
		parts = append(parts, "background:"+bg)
	}
	if s.Attrs&vt.AttrBold != 0 {
		parts = append(parts, "font-weight:bold")
	}
	if s.Attrs&vt.AttrDim != 0 {
		parts = append(parts, "opacity:.6")
	}
	if s.Attrs&vt.AttrItalic != 0 {
		parts = append(parts, "font-style:italic")
	}
	if s.Attrs&vt.AttrUnderline != 0 {
		parts = append(parts, "text-decoration:underline")
	}
	if s.Attrs&vt.AttrStrike != 0 {
		parts = append(parts, "text-decoration:line-through")
	}
	if s.Attrs&vt.AttrHidden != 0 {
		parts = append(parts, "visibility:hidden")
	}
	return strings.Join(parts, ";")
}

// ansi16 are the sixteen colours as a dark terminal draws them.
var ansi16 = [16]string{
	"#1e1e1e", "#e06c75", "#98c379", "#e5c07b", "#61afef", "#c678dd", "#56b6c2", "#d0d0d0",
	"#5c6370", "#ff7b86", "#b5e890", "#ffd68a", "#7cc4ff", "#de95f0", "#7fd6e0", "#ffffff",
}

// colorCSS is a colour as CSS; "" for the default.
func colorCSS(c vt.Color) string {
	switch {
	case c.IsDefault():
		return ""
	case c.IsIndexed():
		return indexedCSS(int(c.Index()))
	}
	r, g, b := c.RGB()
	return fmt.Sprintf("#%02x%02x%02x", r, g, b)
}

// indexedCSS is one of the 256 indexed colours: the sixteen, the 6×6×6
// cube, then the greys.
func indexedCSS(i int) string {
	switch {
	case i < 16:
		return ansi16[i]
	case i < 232:
		i -= 16
		level := func(v int) int {
			if v == 0 {
				return 0
			}
			return 55 + v*40
		}
		return fmt.Sprintf("#%02x%02x%02x", level(i/36), level(i/6%6), level(i%6))
	default:
		g := 8 + (i-232)*10
		return fmt.Sprintf("#%02x%02x%02x", g, g, g)
	}
}
