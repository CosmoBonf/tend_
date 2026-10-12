// Command shot draws a tend session as an HTML page, for the site's
// pictures: it attaches to a session on a pseudo-terminal of a given size, as
// a person's terminal would, does what it is told — keys, clicks, waits — and
// writes what tend drew, cell for cell, in the colours tend used. A browser
// turns the page into a PNG.
//
//	go run ./tools/shot -bin bin/tend -s demo -size 150x42 \
//	    waitfor=spaces keys='\x02M' sleep=1s out=mcp.html
//
// The pictures are of the real program, not drawn by hand: what the site
// shows is what tend does.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"html"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/auth-com-br/tend/internal/pty"
	"github.com/auth-com-br/tend/internal/vt"
)

func main() {
	bin := flag.String("bin", "tend", "the tend to run")
	session := flag.String("s", "default", "the session to attach to")
	size := flag.String("size", "150x42", "the terminal's size, columns x rows")
	flag.Parse()
	if err := run(*bin, *session, *size, flag.Args()); err != nil {
		fmt.Fprintln(os.Stderr, "shot:", err)
		os.Exit(1)
	}
}

type term struct {
	mu     sync.Mutex
	screen *vt.Screen
}

func (t *term) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.screen.Write(p)
}

func run(bin, session, size string, steps []string) error {
	var cols, rows int
	if _, err := fmt.Sscanf(size, "%dx%d", &cols, &rows); err != nil {
		return fmt.Errorf("-size %q is not columns x rows", size)
	}
	p, err := pty.Start(bin, []string{"attach", "-s", session}, pty.Options{Size: pty.Size{Cols: uint16(cols), Rows: uint16(rows)}})
	if err != nil {
		return err
	}
	defer p.Close()
	t := &term{screen: vt.NewScreen(cols, rows, 0)}
	go func() { _, _ = io.Copy(t, p) }()

	var rec *recording
	for _, step := range steps {
		verb, arg, _ := strings.Cut(step, "=")
		switch verb {
		case "sleep":
			d, err := time.ParseDuration(arg)
			if err != nil {
				return err
			}
			time.Sleep(d)
		case "keys":
			s, err := strconv.Unquote(`"` + arg + `"`)
			if err != nil {
				return fmt.Errorf("keys %q: %w", arg, err)
			}
			if _, err := p.Write([]byte(s)); err != nil {
				return err
			}
			time.Sleep(150 * time.Millisecond)
		case "click", "rclick":
			var x, y int
			if _, err := fmt.Sscanf(arg, "%d,%d", &x, &y); err != nil {
				return fmt.Errorf("%s %q is not column,row", verb, arg)
			}
			button := 0
			if verb == "rclick" {
				button = 2
			}
			fmt.Fprintf(p, "\x1b[<%d;%d;%dM\x1b[<%d;%d;%dm", button, x, y, button, x, y)
			time.Sleep(150 * time.Millisecond)
		case "waitfor":
			deadline := time.Now().Add(20 * time.Second)
			for !strings.Contains(t.text(), arg) {
				if time.Now().After(deadline) {
					return fmt.Errorf("%q never appeared:\n%s", arg, t.text())
				}
				time.Sleep(50 * time.Millisecond)
			}
		case "type":
			// Typed as a person types, a key at a time, so a recording
			// shows the words arriving.
			for _, r := range arg {
				if _, err := p.Write([]byte(string(r))); err != nil {
					return err
				}
				time.Sleep(45 * time.Millisecond)
			}
		case "sh":
			if out, err := exec.Command("/bin/sh", "-c", arg).CombinedOutput(); err != nil {
				return fmt.Errorf("sh %q: %w\n%s", arg, err, out)
			}
		case "record":
			rec = &recording{path: arg, stop: make(chan struct{}), done: make(chan struct{})}
			go rec.run(t)
		case "stop":
			if rec != nil {
				if err := rec.finish(); err != nil {
					return err
				}
				rec = nil
			}
		case "text":
			if err := os.WriteFile(arg, []byte(t.text()), 0o644); err != nil {
				return err
			}
		case "out":
			if err := os.WriteFile(arg, []byte(t.page()), 0o644); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unknown step %q", step)
		}
	}
	return nil
}

// recording is the screen sampled ten times a second, for a video: each
// frame that differs from the one before it, with when it was seen. A
// browser draws the frames and ffmpeg joins them (the page is the player:
// window.frames, and show(i)).
type recording struct {
	path       string
	stop, done chan struct{}
	frames     []frame
}

type frame struct {
	At   float64 `json:"t"`
	HTML string  `json:"html"`
}

func (r *recording) run(t *term) {
	defer close(r.done)
	start := time.Now()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	last := ""
	for {
		body := strings.TrimSuffix(strings.TrimPrefix(t.page(), pageHead), "</div>")
		if body != last {
			r.frames = append(r.frames, frame{At: time.Since(start).Seconds(), HTML: body})
			last = body
		}
		select {
		case <-r.stop:
			r.frames = append(r.frames, frame{At: time.Since(start).Seconds(), HTML: last})
			return
		case <-tick.C:
		}
	}
}

func (r *recording) finish() error {
	close(r.stop)
	<-r.done
	data, err := json.Marshal(r.frames)
	if err != nil {
		return err
	}
	page := pageHead + `</div><script>window.frames=` + string(data) +
		`;window.show=i=>{document.getElementById("t").innerHTML=window.frames[i].html};show(0)</script>`
	return os.WriteFile(r.path, []byte(page), 0o644)
}

// text is the screen as plain lines, numbered from 1 as clicks count them.
func (t *term) text() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	g := t.screen.Grid()
	var b strings.Builder
	for y := 0; y < g.Rows(); y++ {
		r := g.Line(y)
		for x := 0; x < r.Len(); x++ {
			c := r.Cell(x)
			switch {
			case c.IsContinuation():
			case c.R == 0:
				b.WriteByte(' ')
			default:
				b.WriteRune(c.R)
			}
		}
		b.WriteByte('\n')
	}
	return b.String()
}

const pageHead = `<!doctype html><meta charset="utf-8"><style>
:root{--fg:#d7dae0;--bg:#16181d}
html,body{margin:0;background:transparent}
#t{display:inline-block;background:var(--bg);color:var(--fg);padding:10px 12px;
font:15px/1.2 "FiraCode Nerd Font Mono","Noto Color Emoji",monospace;font-variant-ligatures:none;white-space:pre}
.r{height:1.2em;overflow:hidden}
.w{display:inline-block;overflow:hidden;text-align:center;vertical-align:top;height:1.2em}
</style><div id="t">`

// page is the screen as HTML. Runs of plain cells are text; a wide cell, or
// a symbol a font may draw at a width of its own (an emoji, an icon), is
// boxed at the width the terminal gave it, so the columns after it stay
// where tend put them.
func (t *term) page() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	g := t.screen.Grid()
	var b strings.Builder
	b.WriteString(pageHead)
	for y := 0; y < g.Rows(); y++ {
		r := g.Line(y)
		b.WriteString(`<div class="r">`)
		var run strings.Builder
		var style vt.Style
		flush := func() {
			if run.Len() == 0 {
				return
			}
			fmt.Fprintf(&b, `<span style="%s">%s</span>`, styleCSS(style), html.EscapeString(run.String()))
			run.Reset()
		}
		for x := 0; x < r.Len(); x++ {
			c := r.Cell(x)
			if c.IsContinuation() {
				continue
			}
			ch := c.R
			if ch == 0 {
				ch = ' '
			}
			width := 1
			if x+1 < r.Len() && r.Cell(x+1).IsContinuation() {
				width = 2
			}
			if width == 2 || !plain(ch) {
				flush()
				text := string(ch)
				for _, m := range r.Combining(x) {
					text += string(m)
				}
				fmt.Fprintf(&b, `<span class="w" style="width:%dch;%s">%s</span>`, width, styleCSS(c.Style), html.EscapeString(text))
				continue
			}
			if c.Style != style {
				flush()
				style = c.Style
			}
			run.WriteRune(ch)
			for _, m := range r.Combining(x) {
				run.WriteRune(m)
			}
		}
		flush()
		b.WriteString("</div>")
	}
	b.WriteString("</div>")
	return b.String()
}

// plain reports a rune every monospace font draws one cell wide: Latin text
// and the box-drawing and block characters panes are framed with.
func plain(r rune) bool {
	return r < 0x2000 || (r >= 0x2500 && r <= 0x259f)
}

var ansi16 = [16]string{
	"#1e1e1e", "#e06c75", "#98c379", "#e5c07b", "#61afef", "#c678dd", "#56b6c2", "#d0d0d0",
	"#5c6370", "#ff7b86", "#b5e890", "#ffd68a", "#7cc4ff", "#de95f0", "#7fd6e0", "#ffffff",
}

func styleCSS(s vt.Style) string {
	fg, bg := colorCSS(s.FG), colorCSS(s.BG)
	if s.Attrs&vt.AttrReverse != 0 {
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
	return strings.Join(parts, ";")
}

func colorCSS(c vt.Color) string {
	switch {
	case c.IsDefault():
		return ""
	case c.IsIndexed():
		i := int(c.Index())
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
		}
		g := 8 + (i-232)*10
		return fmt.Sprintf("#%02x%02x%02x", g, g, g)
	}
	r, g, b := c.RGB()
	return fmt.Sprintf("#%02x%02x%02x", r, g, b)
}
