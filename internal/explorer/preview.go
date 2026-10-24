package explorer

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mattn/go-runewidth"
	"golang.org/x/term"

	"github.com/auth-com-br/tend/internal/vt"
)

// The preview (`tend view`) is a file shown read-only in a pane beside the
// main one, with line numbers and syntax colour: herdr-sidebar's preview,
// in a pane rather than a tab, because in tend the files panel lives in one
// tab and a tab opened for the preview would hide it. The panel keeps one
// preview pane and points it at the next file rather than opening another;
// it says so through the pane's input, as retargetPrefix below.

// retargetPrefix opens the private OSC the panel sends a preview to show
// another file: ESC ] tend-view ; line ; path BEL.
const retargetPrefix = "\x1b]tend-view;"

// RetargetSequence is what to type into a preview to make it show a file.
func RetargetSequence(path string, line int) string {
	return retargetPrefix + strconv.Itoa(line) + ";" + path + "\x07"
}

// Retarget is a preview told to show another file.
type Retarget struct {
	Path string
	Line int
}

// Preview is the viewer's state.
type Preview struct {
	path    string
	lines   []string
	spans   [][]span
	binary  bool
	err     error
	modTime time.Time

	// markdown is shown rendered unless raw; an image is drawn, at the size
	// it was last drawn for.
	markdown bool
	raw      bool
	image    bool
	drawn    [][]span
	drawnFor [2]int
	imgW     int
	imgH     int

	top, left int
	wrap      bool
	mark      int
	cols      int
	rows      int
	quit      bool
	message   string

	opener Opener
	// editor, when set, is the editor o runs in the preview's own pane,
	// asked at each o so one chosen on the settings screen is used; and
	// editing is the o waiting for RunPreview to hand the terminal over.
	editor  func() string
	editing bool
}

// EditInPlace makes o edit the file in this pane: the preview steps aside
// for the editor and comes back, reading what was saved, when it exits. The
// owner asked for it: o used to open the editor in a tab of its own, and
// closing it went back to the preview tab, one tab too many.
func (p *Preview) EditInPlace(editor func() string) { p.editor = editor }

// NewPreview shows a file, with line (from one) in view and lit.
func NewPreview(path string, line int, opener Opener) *Preview {
	p := &Preview{opener: opener}
	p.load(path, line)
	return p
}

func (p *Preview) load(path string, line int) {
	*p = Preview{opener: p.opener, editor: p.editor, cols: p.cols, rows: p.rows, wrap: p.wrap, raw: p.raw, path: path, mark: line}
	info, err := os.Stat(path)
	if err != nil {
		p.err = err
		return
	}
	p.modTime = info.ModTime()
	if isImage(path) {
		// Drawn when the size to draw it at is known, in Draw.
		p.image = true
		return
	}
	p.markdown = langFor(path) != nil && langFor(path).markdown
	if p.markdown && line > 0 {
		// A search result is a line of the source; rendered, it would not
		// be there to light.
		p.raw = true
	}
	f, err := os.Open(path)
	if err != nil {
		p.err = err
		return
	}
	defer f.Close()
	data, _ := io.ReadAll(io.LimitReader(f, maxViewBytes+1))
	if bytes.IndexByte(data[:min(len(data), 8000)], 0) >= 0 {
		p.binary = true
		return
	}
	cut := len(data) > maxViewBytes
	if cut {
		data = data[:maxViewBytes]
	}
	text := strings.ReplaceAll(strings.TrimSuffix(string(data), "\n"), "\t", "    ")
	p.lines = strings.Split(text, "\n")
	if cut {
		p.lines = append(p.lines, "… the rest is past 2 MB; o opens it in the editor")
	}
	l := langFor(path)
	var st hlState
	p.spans = make([][]span, len(p.lines))
	for i, ln := range p.lines {
		p.spans[i], st = l.highlight(ln, st)
	}
	if line > 0 {
		p.top = max(line-1-max(p.rows-2, 1)/3, 0)
	}
}

// Quit reports whether the user closed the preview.
func (p *Preview) Quit() bool { return p.quit }

// body is how many rows the file gets: all but the title and the hints.
func (p *Preview) body() int { return max(p.rows-2, 1) }

// shown is the lines as drawn: the source with its colour, markdown
// rendered, or the picture; and whether line numbers go beside them, which
// they do only for the source.
func (p *Preview) shown() ([][]span, bool) {
	switch {
	case p.image:
		if p.drawnFor != [2]int{p.cols, p.rows} {
			p.drawn, p.imgW, p.imgH, p.err = renderImage(p.path, p.cols-2, p.body())
			p.drawnFor = [2]int{p.cols, p.rows}
		}
		return p.drawn, false
	case p.markdown && !p.raw:
		if p.drawnFor != [2]int{p.cols, p.rows} || p.drawn == nil {
			p.drawn = renderMarkdown(p.lines, p.cols-2)
			p.drawnFor = [2]int{p.cols, p.rows}
		}
		return p.drawn, false
	}
	return p.spans, true
}

func (p *Preview) scroll(delta int) {
	lines, _ := p.shown()
	p.top = min(max(p.top+delta, 0), max(len(lines)-p.body(), 0))
}

// Key handles a key.
func (p *Preview) Key(k Key) {
	p.message = ""
	switch k.Name {
	case "q", "esc", "ctrl+c":
		p.quit = true
	case "up", "k":
		p.scroll(-1)
	case "down", "j", "enter":
		p.scroll(1)
	case "pgup", "b", "ctrl+u":
		p.scroll(-p.body())
	case "pgdown", "space", "ctrl+d":
		p.scroll(p.body())
	case "home", "g":
		p.top = 0
	case "end", "G":
		p.scroll(len(p.lines))
	case "left", "h":
		p.left = max(p.left-8, 0)
	case "right", "l":
		if !p.wrap {
			p.left += 8
		}
	case "w":
		p.wrap, p.left = !p.wrap, 0
	case "m":
		// Markdown's source, or back to it read as it reads. The line asked
		// for is a line of the source, so the source is where it is lit.
		if p.markdown {
			p.raw, p.top, p.drawn = !p.raw, 0, nil
		}
	case "r":
		p.load(p.path, p.mark)
	case "o":
		if p.editor != nil && canEditInPlace {
			p.editing = true
			return
		}
		if p.opener == nil {
			p.message = "nowhere to open files: not running in a tend pane"
			return
		}
		line := p.mark
		if line == 0 {
			line = p.top + 1
		}
		if err := p.opener.Open(p.path, line); err != nil {
			p.message = err.Error()
		}
	}
}

// Mouse scrolls with the wheel.
func (p *Preview) Mouse(ev Mouse) {
	if ev.Wheel != 0 {
		p.scroll(3 * ev.Wheel)
	}
}

// Tick reads the file again when it changed on disk, keeping the place: an
// agent editing the file being looked at is the common case.
func (p *Preview) Tick() {
	info, err := os.Stat(p.path)
	if err != nil || info.ModTime().Equal(p.modTime) {
		return
	}
	top, left := p.top, p.left
	p.load(p.path, p.mark)
	p.top, p.left = top, left
	p.scroll(0)
}

// Draw paints the preview.
func (p *Preview) Draw(g *vt.Grid) {
	p.cols, p.rows = g.Cols(), g.Rows()
	g.Clear(styleNormal)
	if p.cols < 4 || p.rows < 3 {
		return
	}
	dir, name := filepath.Split(p.path)
	x := put(g, 1, 0, name, styleBold, p.cols)
	put(g, x+1, 0, truncateLeft(strings.TrimSuffix(dir, "/"), p.cols-x-2), styleDim, p.cols)

	switch {
	case p.err != nil:
		put(g, 1, 2, truncate(p.err.Error(), p.cols-2), styleErr, p.cols)
	case p.binary:
		put(g, 1, 2, "binary file — o opens it elsewhere", styleDim, p.cols)
	default:
		p.drawLines(g)
	}
	if p.image && p.err == nil && p.imgW > 0 {
		size := strconv.Itoa(p.imgW) + "×" + strconv.Itoa(p.imgH)
		put(g, p.cols-1-len([]rune(size)), 0, size, styleDim, p.cols)
	}

	hint := "q close  o edit  w wrap"
	switch {
	case p.image:
		hint = "q close  o open"
	case p.markdown && !p.raw:
		hint = "q close  o edit  m source"
	case p.markdown:
		hint = "q close  o edit  m rendered"
	case p.wrap:
		hint = "q close  o edit  w unwrap"
	}
	if p.message != "" {
		put(g, 1, p.rows-1, truncate(p.message, p.cols-2), styleErr, p.cols)
	} else {
		put(g, 1, p.rows-1, truncate(hint, p.cols-2), styleDim, p.cols)
	}
}

func (p *Preview) drawLines(g *vt.Grid) {
	lines, numbered := p.shown()
	gutter := 0
	if numbered {
		gutter = len(strconv.Itoa(len(lines))) + 1
	}
	width := p.cols - gutter - 1
	y := 1
	for i := p.top; i < len(lines) && y < p.rows-1; i++ {
		lit := numbered && i+1 == p.mark
		if numbered {
			num := strconv.Itoa(i + 1)
			numStyle := styleDim
			if lit {
				numStyle = vt.Style{FG: vt.IndexedColor(3), Attrs: vt.AttrBold | vt.AttrReverse}
				fill(g, y, gutter, p.cols, vt.Style{Attrs: vt.AttrReverse | vt.AttrDim})
			}
			put(g, gutter-len(num), y, num, numStyle, p.cols)
		}
		// The spans, cut from the left when scrolled sideways, and wrapped
		// onto the rows below when wrapping is on.
		col, skip := 0, p.left
		x := gutter + 1
		for _, s := range lines[i] {
			style := s.style
			if lit {
				style.Attrs |= vt.AttrBold
			}
			for _, r := range s.text {
				w := runewidth.RuneWidth(r)
				if skip > 0 {
					skip -= w
					continue
				}
				if col+w > width {
					if !p.wrap {
						break
					}
					y++
					if y >= p.rows-1 {
						return
					}
					col, x = 0, gutter+1
				}
				x = put(g, x, y, string(r), style, p.cols)
				col += w
			}
		}
		y++
	}
}

// RunPreview runs a preview on the terminal until it is closed. Besides
// keys and the wheel it takes retargets, and reads the file again when it
// changes.
func RunPreview(p *Preview, in *os.File, out io.Writer) error {
	fd := int(in.Fd())
	state, err := term.MakeRaw(fd)
	if err != nil {
		return err
	}
	defer func() { _ = term.Restore(fd, state) }()
	_, _ = io.WriteString(out, enterScreen)
	defer func() { _, _ = io.WriteString(out, leaveScreen) }()

	keys := newKeyReader(in)
	go keys.run()
	resized := make(chan struct{}, 1)
	stop := notifyResize(resized)
	defer stop()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()

	painter := vt.NewPainter()
	var grid *vt.Grid
	var pending []byte
	draw := func() {
		cols, rows, err := term.GetSize(fd)
		if err != nil || cols <= 0 || rows <= 0 {
			cols, rows = 80, 24
		}
		if grid == nil || grid.Cols() != cols || grid.Rows() != rows {
			grid = vt.NewGrid(cols, rows, 0)
			painter.Invalidate()
		}
		p.Draw(grid)
		_, _ = out.Write(painter.Paint(grid, 0, 0, false))
	}
	draw()
	for !p.Quit() {
		select {
		case data, ok := <-keys.out:
			if !ok {
				return nil
			}
			events, rest := Parse(append(pending, data...))
			pending = rest
			for _, ev := range events {
				switch e := ev.(type) {
				case Key:
					p.Key(e)
				case Mouse:
					p.Mouse(e)
				case Retarget:
					p.load(e.Path, e.Line)
				}
				if p.editing {
					break
				}
			}
		case <-resized:
		case <-tick.C:
			p.Tick()
		}
		if p.editing {
			// What was typed after o was typed before the editor was there
			// to take it, and is not the editor's.
			p.editing, pending = false, nil
			p.editHere(keys, fd, state, in, out)
			painter.Invalidate()
		}
		draw()
	}
	return nil
}

// editHere runs the editor on the preview's file in this pane, as less runs
// it on v: the key reader stops, so no key meant for the editor is read here;
// the screen and the terminal's modes are put back as a shell left them; and
// after the editor, the preview takes the terminal back and reads the file
// again, keeping its place.
func (p *Preview) editHere(keys *keyReader, fd int, raw *term.State, in *os.File, out io.Writer) {
	keys.pause()
	defer keys.resume()
	_, _ = io.WriteString(out, leaveScreen)
	_ = term.Restore(fd, raw)

	line := p.mark
	if line == 0 {
		line = p.top + 1
	}
	editor := strings.TrimSpace(p.editor())
	if editor == "" {
		editor = "vi"
	}
	// Through a shell, so an editor with flags in it works, and the path as
	// an argument, so one with spaces in it reaches the editor as it is: the
	// command the tab used to run.
	cmd := exec.Command("/bin/sh", "-c", editorAt(editor, line)+` "$1"`, "tend-edit", p.path)
	cmd.Dir = filepath.Dir(p.path)
	cmd.Stdin, cmd.Stderr = in, os.Stderr
	cmd.Stdout = os.Stdout
	if f, ok := out.(*os.File); ok {
		cmd.Stdout = f
	}
	restore := holdInterrupts()
	err := cmd.Run()
	restore()

	if _, rerr := term.MakeRaw(fd); rerr != nil && err == nil {
		err = rerr
	}
	_, _ = io.WriteString(out, enterScreen)
	p.Tick()
	if err != nil {
		p.message = editor + ": " + err.Error()
	}
}

// truncateLeft keeps the end of text, which for a path is the part that
// says which directory it is.
func truncateLeft(text string, cols int) string {
	if cols <= 0 {
		return ""
	}
	if runewidth.StringWidth(text) <= cols {
		return text
	}
	runes := []rune(text)
	for len(runes) > 0 && runewidth.StringWidth(string(runes))+1 > cols {
		runes = runes[1:]
	}
	return "…" + string(runes)
}
