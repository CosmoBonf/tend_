package explorer

import (
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Key is one key press, by name: "enter", "up", "ctrl+w", or the character
// itself ("a", "G", "/"), in which case Rune is set too.
type Key struct {
	Name string
	Rune rune
}

// Mouse is one mouse report, in cells from the top left, counted from zero.
type Mouse struct {
	X, Y    int
	Button  int
	Press   bool
	Release bool
	// Drag is the pointer moving with a button held: a drag over the tree
	// copies the paths it covers.
	Drag  bool
	Wheel int // -1 up, +1 down
}

// Parse reads keys (Key) and mouse reports (Mouse) out of what the terminal
// sent, in order. It returns what it could not finish reading yet — an
// escape sequence or a character split across two reads — for the caller to
// put in front of the next read.
func Parse(data []byte) (events []any, rest []byte) {
	for len(data) > 0 {
		b := data[0]
		switch {
		case b == 0x1b:
			if len(data) == 1 {
				// A lone escape is the escape key; a terminal sends a
				// sequence in one write, so a split one is rare enough to
				// read as a key rather than wait for.
				events = append(events, Key{Name: "esc"})
				data = data[1:]
				continue
			}
			n, ev, complete := parseEscape(data)
			if !complete {
				return events, data
			}
			if ev != nil {
				events = append(events, ev)
			}
			data = data[n:]
		case b == '\r' || b == '\n':
			events = append(events, Key{Name: "enter"})
			data = data[1:]
		case b == '\t':
			events = append(events, Key{Name: "tab"})
			data = data[1:]
		case b == 0x7f || b == 0x08:
			events = append(events, Key{Name: "backspace"})
			data = data[1:]
		case b == ' ':
			events = append(events, Key{Name: "space", Rune: ' '})
			data = data[1:]
		case b == 0:
			data = data[1:]
		case b < 0x20:
			events = append(events, Key{Name: "ctrl+" + string(rune('a'+b-1))})
			data = data[1:]
		default:
			r, size := utf8.DecodeRune(data)
			if r == utf8.RuneError && size <= 1 && !utf8.FullRune(data) {
				return events, data
			}
			events = append(events, Key{Name: string(r), Rune: r})
			data = data[size:]
		}
	}
	return events, nil
}

// parseEscape reads one escape sequence: a CSI (arrows, page keys, SGR
// mouse), an SS3 (arrows in application mode), or alt+key, which is read as
// the key.
func parseEscape(data []byte) (n int, ev any, complete bool) {
	switch data[1] {
	case '[':
		end := 2
		for end < len(data) && (data[end] < 0x40 || data[end] > 0x7e) {
			end++
		}
		if end >= len(data) {
			return 0, nil, false
		}
		params, final := string(data[2:end]), data[end]
		return end + 1, csiEvent(params, final), true
	case 'O':
		if len(data) < 3 {
			return 0, nil, false
		}
		return 3, arrowKey(data[2]), true
	case ']':
		// An OSC, ended by BEL or ST: the only one a pane is sent is the
		// files panel pointing a preview at another file.
		end, size := -1, 0
		for i := 2; i < len(data); i++ {
			if data[i] == 0x07 {
				end, size = i, 1
				break
			}
			if data[i] == 0x1b && i+1 < len(data) && data[i+1] == '\\' {
				end, size = i, 2
				break
			}
		}
		if end < 0 {
			return 0, nil, false
		}
		payload := string(data[2:end])
		if rest, ok := strings.CutPrefix(payload, "tend-view;"); ok {
			if line, path, ok := strings.Cut(rest, ";"); ok {
				n, _ := strconv.Atoi(line)
				return end + size, Retarget{Path: path, Line: n}, true
			}
		}
		return end + size, nil, true
	default:
		// Escape and a printable character together is that character with
		// alt held, which is how a terminal sends alt+c.
		if b := data[1]; b > 0x20 && b < 0x7f {
			return 2, Key{Name: "alt+" + string(rune(b))}, true
		}
		return 1, Key{Name: "esc"}, true
	}
}

func arrowKey(final byte) any {
	switch final {
	case 'A':
		return Key{Name: "up"}
	case 'B':
		return Key{Name: "down"}
	case 'C':
		return Key{Name: "right"}
	case 'D':
		return Key{Name: "left"}
	case 'H':
		return Key{Name: "home"}
	case 'F':
		return Key{Name: "end"}
	}
	return nil
}

func csiEvent(params string, final byte) any {
	if strings.HasPrefix(params, "<") && (final == 'M' || final == 'm') {
		f := strings.Split(params[1:], ";")
		if len(f) != 3 {
			return nil
		}
		code, _ := strconv.Atoi(f[0])
		x, _ := strconv.Atoi(f[1])
		y, _ := strconv.Atoi(f[2])
		m := Mouse{X: x - 1, Y: y - 1, Button: code & 3}
		switch {
		case code&64 != 0:
			m.Wheel = 1
			if code&1 == 0 {
				m.Wheel = -1
			}
		case code&32 != 0:
			m.Drag = true
		case final == 'm':
			m.Release = true
		default:
			m.Press = true
		}
		return m
	}
	if final == 'Z' {
		return Key{Name: "shift+tab"}
	}
	if final == '~' {
		switch strings.SplitN(params, ";", 2)[0] {
		case "1", "7":
			return Key{Name: "home"}
		case "4", "8":
			return Key{Name: "end"}
		case "5":
			return Key{Name: "pgup"}
		case "6":
			return Key{Name: "pgdown"}
		case "3":
			return Key{Name: "delete"}
		}
		return nil
	}
	return arrowKey(final)
}

// doubleClick is how close two clicks on one row must be to open it.
const doubleClick = 400 * time.Millisecond

// Mouse handles a click or a turn of the wheel.
func (m *Model) Mouse(ev Mouse) {
	if m.press != nil && (ev.Drag || ev.Release) {
		// A gesture begun on the tree is the tree's until the button comes
		// up, wherever the pointer goes meanwhile: over the activity bar,
		// a release would otherwise be the bar's and the click never land.
		if m.bar {
			ev.Y -= barRows
		}
		m.treeGesture(ev)
		return
	}
	// Under the activity bar, the panel is where it always was, two rows
	// lower: a press on the bar is the bar's, and anything else is moved
	// up to the rows the rest of this reads.
	if m.bar {
		if ev.Wheel == 0 && m.barClick(ev) {
			return
		}
		if ev.Wheel == 0 && m.gitFooter >= 0 && ev.Y == m.gitFooter {
			if ev.Press && ev.Button == 0 && m.mode == modeList {
				m.gitFooterClick(ev.X)
			}
			return
		}
		if ev.Wheel == 0 && ev.Y == barRows+1 {
			return // the project's name, where the git bar would be
		}
		ev.Y = max(ev.Y-barRows, 0)
	}
	if ev.Drag || ev.Release {
		// Everywhere but the tree acts on the press and has no use for the
		// rest of the gesture.
		return
	}
	switch m.mode {
	case modeViewer:
		if ev.Wheel != 0 {
			m.viewer.top = min(max(m.viewer.top+3*ev.Wheel, 0), max(len(m.viewer.lines)-m.listRows(), 0))
		} else if ev.Press && ev.Y == 0 {
			m.mode = modeList
		}
		return
	case modeSearch:
		if ev.Wheel != 0 {
			m.search.cursor = min(max(m.search.cursor+ev.Wheel, 0), max(len(m.search.results)-1, 0))
			return
		}
		if ev.Press && ev.Y >= 2 {
			i := searchTop(m) + ev.Y - 2
			if i < len(m.search.results) {
				if i == m.search.cursor {
					m.searchKey(Key{Name: "enter"})
				} else {
					m.search.cursor = i
				}
			}
		}
		return
	case modeBranch:
		m.branchMouse(ev)
		return
	case modeMenu:
		m.menuMouse(ev)
		return
	case modeHistory:
		m.historyMouse(ev)
		return
	case modeSettings:
		m.settingsMouse(ev)
		return
	case modePrompt:
		return
	case modeHelp, modeCommit:
		if ev.Press {
			m.mode = modeList
		}
		return
	}

	if ev.Wheel != 0 {
		v := m.view
		m.scroll[v] = min(max(m.scroll[v]+3*ev.Wheel, 0), max(m.listLen()-m.listRows(), 0))
		// The cursor stays where it is on the list, which may now be off
		// screen; the next key brings it back, as in an editor.
		return
	}
	if ev.Press && ev.Button == 2 && m.view == ViewFiles {
		// A right-click is the menu, on the entry under it or, below the
		// last, on the project.
		i := m.scroll[ViewFiles] + ev.Y - 2
		if ev.Y >= 2 && i < len(m.fileRows) {
			m.cursor[ViewFiles] = i
			m.openMenu(m.fileRows[i])
		} else if ev.Y >= 2 {
			m.openMenu(nil)
		}
		return
	}
	if !ev.Press || ev.Button != 0 {
		return
	}
	if ev.Y == 0 {
		// The names on the header, each up to the divider after it.
		switch v := headerViewAt(ev.X); v {
		case ViewSearch:
			m.openContentSearch()
		default:
			m.switchView(v)
		}
		return
	}
	if ev.Y == 1 {
		m.gitBarClick(ev.X)
		return
	}
	if m.view == ViewSearch {
		m.searchMouse(ev)
		return
	}
	if ev.Y < 2 || ev.Y >= 2+m.listRows() {
		return
	}
	i := m.scroll[m.view] + ev.Y - 2
	if i >= m.listLen() {
		return
	}
	now := m.now()
	double := i == m.lastClickRow && now.Sub(m.lastClick) < doubleClick
	m.lastClick, m.lastClickRow = now, i
	m.message = ""
	if m.view == ViewFiles {
		// The press selects; what the click does waits for the button to
		// come up, because the press may be the start of a drag that copies
		// paths instead. Acting on the press would preview the file — a tab
		// that takes the screen — before the drag had begun.
		m.cursor[ViewFiles] = i
		n := m.fileRows[i]
		m.press = &treePress{from: n, to: n, double: double}
		m.clamp()
		return
	}
	if m.changeRows[i].heading != "" {
		return
	}
	m.cursor[ViewChanges] = i
	m.clamp()
	if double {
		m.showDiff(m.changeRows[i])
		m.lastClick = time.Time{}
	}
}

// treePress is a press on the tree waiting for its button to come up: a
// click if the pointer stayed put, a drag over rows if it moved.
type treePress struct {
	from, to *Node
	double   bool
	dragged  bool
}

// treeGesture follows a press on the tree to its release. A drag selects the
// rows between where it began and where the pointer is, and letting go copies
// their full paths, one a line — the panel's answer to selecting text with
// the mouse, which in a program that holds the mouse would give the indent
// and the icons rather than where the file is. Without a drag the release
// is the click: a folder opens, a file is previewed, or opened on a second.
func (m *Model) treeGesture(ev Mouse) {
	p := m.press
	if m.mode != modeList || m.view != ViewFiles || len(m.fileRows) == 0 {
		m.press = nil
		return
	}
	if ev.Drag {
		// The client keeps the pointer inside the pane; here it is kept on
		// the rows shown, so dragging past the last one takes the last.
		top := m.scroll[ViewFiles]
		last := min(top+m.listRows(), len(m.fileRows)) - 1
		i := min(max(top+ev.Y-2, top), last)
		p.to, p.dragged = m.fileRows[i], true
		m.cursor[ViewFiles] = i
		return
	}
	m.press = nil
	if p.dragged {
		m.copyPaths(p.from, p.to)
		return
	}
	m.clickFile(p.from, p.double)
}

// dragRange is the rows a drag in progress covers, first to last, or -1 and
// -1 with none.
func (m *Model) dragRange() (int, int) {
	p := m.press
	if p == nil || !p.dragged {
		return -1, -1
	}
	from, to := m.rowOf(p.from), m.rowOf(p.to)
	if from < 0 || to < 0 {
		return -1, -1
	}
	return min(from, to), max(from, to)
}

// rowOf is where a node is on the tree as listed now, or -1. The tree is
// read again every two seconds, so a row number from the press may no
// longer be the same entry by the release; the node is.
func (m *Model) rowOf(n *Node) int {
	for i, r := range m.fileRows {
		if r == n {
			return i
		}
	}
	return -1
}

// copyPaths puts the full paths of the rows from one node to another on the
// clipboard, by the same OSC 52 the menu's "Copy path" writes.
func (m *Model) copyPaths(from, to *Node) {
	paths := []string{m.tree.Path(from)}
	if lo, hi := m.rowOf(from), m.rowOf(to); lo >= 0 && hi >= 0 {
		paths = paths[:0]
		for i := min(lo, hi); i <= max(lo, hi); i++ {
			paths = append(paths, m.tree.Path(m.fileRows[i]))
		}
	}
	m.copyText(strings.Join(paths, "\n"))
	if len(paths) == 1 {
		m.say("copied "+paths[0], false)
	} else {
		m.say("copied "+strconv.Itoa(len(paths))+" paths", false)
	}
}

// clickFile is a click on a row of the tree, acted on as the button comes up.
func (m *Model) clickFile(n *Node, double bool) {
	if m.rowOf(n) < 0 {
		return // gone from the tree between press and release
	}
	switch {
	case n.Dir:
		// One click opens a directory, as in an editor's explorer: there
		// is nothing else to do to one.
		m.tree.Toggle(n, m.git)
		m.layout()
		m.lastClick = time.Time{}
	case double:
		m.open(m.tree.Path(n), 0)
		m.lastClick = time.Time{}
	default:
		// One click previews, as herdr-sidebar's does. The preview
		// is a tab of its own and takes the screen, so a second click
		// lands there rather than here: o in the preview, or enter
		// on the panel, is how a file is opened for editing.
		if _, ok := m.opener.(Previewer); ok {
			m.preview(n.Rel, m.tree.Path(n), 0)
		}
	}
	m.clamp()
}

// searchMouse answers a click in the search view: the query line starts
// typing, the switches switch, a result is selected by one click and
// opened by a second.
func (m *Model) searchMouse(ev Mouse) {
	c := &m.csearch
	switch {
	case ev.Y == 2:
		c.editing, c.field = true, fieldQuery
		return
	case ev.Y == 3:
		// "Aa ab .*" from column 1, two wide with a space between.
		switch {
		case ev.X >= 1 && ev.X <= 2:
			m.toggleSearchOption("case")
		case ev.X >= 4 && ev.X <= 5:
			m.toggleSearchOption("word")
		case ev.X >= 7 && ev.X <= 8:
			m.toggleSearchOption("regex")
		}
		return
	case ev.Y < searchListTop || ev.Y >= searchListTop+m.listRows():
		return
	}
	i := m.scroll[ViewSearch] + ev.Y - searchListTop
	if i >= len(c.rows) {
		return
	}
	c.editing = false
	now := m.now()
	double := i == m.lastClickRow && now.Sub(m.lastClick) < doubleClick
	m.lastClick, m.lastClickRow = now, i
	m.cursor[ViewSearch] = i
	m.clamp()
	if double {
		if path, line, ok := m.selectedSearchHit(); ok {
			m.openAt(path, line)
		}
		m.lastClick = time.Time{}
	}
}
