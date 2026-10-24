package vt

import (
	"encoding/base64"
	"strconv"
	"strings"
	"testing"
)

func newScreen(t *testing.T, cols, rows, sb int, input string) *Screen {
	t.Helper()
	s := NewScreen(cols, rows, sb)
	if input != "" {
		if _, err := s.Write([]byte(input)); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}
	return s
}

func screenLines(s *Screen) []string {
	g := s.Grid()
	out := make([]string, g.Rows())
	for y := 0; y < g.Rows(); y++ {
		out[y] = g.Line(y).Text()
	}
	return out
}

func screenHistory(s *Screen) []string {
	g := s.MainGrid()
	out := make([]string, g.HistoryLen())
	for i := 0; i < g.HistoryLen(); i++ {
		out[i] = g.HistoryLine(i).Text()
	}
	return out
}

func wantLines(t *testing.T, s *Screen, want ...string) {
	t.Helper()
	eq(t, "viewport", screenLines(s), want)
}

func wantCursor(t *testing.T, s *Screen, x, y int) {
	t.Helper()
	c := s.Cursor()
	if c.X != x || c.Y != y {
		t.Errorf("cursor = (%d,%d), want (%d,%d)", c.X, c.Y, x, y)
	}
}

// --- printing and wrapping -------------------------------------------------

func TestScreenPrint(t *testing.T) {
	s := newScreen(t, 10, 2, 0, "hello")
	wantLines(t, s, "hello", "")
	wantCursor(t, s, 5, 0)
}

func TestScreenAutoWrap(t *testing.T) {
	s := newScreen(t, 3, 2, 0, "abcd")
	wantLines(t, s, "abc", "d")
	wantCursor(t, s, 1, 1)
}

// TestScreenPendingWrap pins the deferred-wrap rule: a line that exactly fills
// the width must not scroll until another character actually arrives.
func TestScreenPendingWrap(t *testing.T) {
	s := newScreen(t, 3, 2, 0, "abc")
	wantLines(t, s, "abc", "")
	wantCursor(t, s, 2, 0) // parked on the last column, not wrapped yet
	s.Write([]byte("d"))
	wantLines(t, s, "abc", "d")
}

func TestScreenPendingWrapCancelledByCR(t *testing.T) {
	s := newScreen(t, 3, 2, 0, "abc\rx")
	wantLines(t, s, "xbc", "")
}

func TestScreenNoAutoWrap(t *testing.T) {
	// With DECAWM off the last column is overwritten instead of wrapping.
	s := newScreen(t, 3, 2, 0, "\x1b[?7labcd")
	wantLines(t, s, "abd", "")
}

func TestScreenWideChar(t *testing.T) {
	s := newScreen(t, 6, 1, 0, "a世b")
	wantLines(t, s, "a世b")
	wantCursor(t, s, 4, 0)
	row := s.Grid().Line(0)
	if got := row.Cell(1).Width; got != 2 {
		t.Errorf("wide cell width = %d, want 2", got)
	}
	if !row.Cell(2).IsContinuation() {
		t.Error("the column after a wide character must be a continuation")
	}
}

func TestScreenWideCharWrapsRatherThanStraddle(t *testing.T) {
	// A double-width character cannot be split across the margin.
	s := newScreen(t, 3, 2, 0, "ab世")
	wantLines(t, s, "ab", "世")
}

func TestScreenOverwritingWideCharClearsBothHalves(t *testing.T) {
	// Writing over the left half must not leave the orphaned right half behind.
	s := newScreen(t, 4, 1, 0, "世x\r")
	s.Write([]byte("a"))
	wantLines(t, s, "a x")
}

func TestScreenCombiningMark(t *testing.T) {
	s := newScreen(t, 5, 1, 0, "éx")
	wantLines(t, s, "éx")
	wantCursor(t, s, 2, 0)
}

// --- controls --------------------------------------------------------------

func TestScreenCarriageReturnAndLineFeed(t *testing.T) {
	s := newScreen(t, 5, 3, 0, "ab\r\ncd")
	wantLines(t, s, "ab", "cd", "")
	wantCursor(t, s, 2, 1)
}

func TestScreenBackspace(t *testing.T) {
	s := newScreen(t, 5, 1, 0, "abc\b\bX")
	wantLines(t, s, "aXc")
}

func TestScreenBackspaceAtColumnZero(t *testing.T) {
	s := newScreen(t, 5, 1, 0, "\b\bx")
	wantLines(t, s, "x")
	wantCursor(t, s, 1, 0)
}

func TestScreenTab(t *testing.T) {
	s := newScreen(t, 20, 1, 0, "a\tb")
	wantCursor(t, s, 9, 0)
	if got := s.Grid().Line(0).Text(); !strings.HasPrefix(got, "a") || !strings.HasSuffix(got, "b") {
		t.Errorf("row = %q, want a tab jump between a and b", got)
	}
}

func TestScreenBellHook(t *testing.T) {
	s := NewScreen(4, 1, 0)
	rung := 0
	s.OnBell = func() { rung++ }
	s.Write([]byte("a\x07b\x07"))
	if rung != 2 {
		t.Errorf("bell fired %d times, want 2", rung)
	}
}

// --- scrolling and history -------------------------------------------------

func TestScreenScrollsIntoHistory(t *testing.T) {
	s := newScreen(t, 4, 3, 10, "a\r\nb\r\nc\r\nd")
	wantLines(t, s, "b", "c", "d")
	eq(t, "history", screenHistory(s), []string{"a"})
}

func TestScreenScrollRegion(t *testing.T) {
	// Region rows 2-3 (1-based), so lines 1-2 zero-based.
	s := newScreen(t, 4, 4, 10, "\x1b[2;3r")
	wantCursor(t, s, 0, 0)
	s.Write([]byte("\x1b[2;1Hx\r\ny\r\nz"))
	// The region scrolled; rows outside it are untouched.
	wantLines(t, s, "", "y", "z", "")
}

// TestScreenPartialRegionDoesNotFeedHistory is the rule that keeps an
// application's own pane redraws out of the user's scrollback.
func TestScreenPartialRegionDoesNotFeedHistory(t *testing.T) {
	s := newScreen(t, 4, 4, 10, "\x1b[1;2r")
	s.Write([]byte("a\r\nb\r\nc\r\nd"))
	if n := s.MainGrid().HistoryLen(); n != 0 {
		t.Errorf("HistoryLen = %d, want 0 — a partial region must not feed history: %q",
			n, screenHistory(s))
	}
}

func TestScreenReverseIndexScrollsDown(t *testing.T) {
	s := newScreen(t, 4, 3, 0, "a\r\nb\r\nc")
	s.Write([]byte("\x1b[1;1H\x1bM"))
	wantLines(t, s, "", "a", "b")
}

// --- cursor movement -------------------------------------------------------

func TestScreenCursorMovement(t *testing.T) {
	s := newScreen(t, 10, 5, 0, "\x1b[3;4H")
	wantCursor(t, s, 3, 2)
	s.Write([]byte("\x1b[2A"))
	wantCursor(t, s, 3, 0)
	s.Write([]byte("\x1b[3B"))
	wantCursor(t, s, 3, 3)
	s.Write([]byte("\x1b[2C"))
	wantCursor(t, s, 5, 3)
	s.Write([]byte("\x1b[4D"))
	wantCursor(t, s, 1, 3)
}

func TestScreenCursorClamping(t *testing.T) {
	s := newScreen(t, 5, 3, 0, "\x1b[99;99H")
	wantCursor(t, s, 4, 2)
	s.Write([]byte("\x1b[99A"))
	wantCursor(t, s, 4, 0)
}

func TestScreenCursorDefaultsToOne(t *testing.T) {
	// CSI H with no parameters homes the cursor; CSI A with none moves by one.
	s := newScreen(t, 5, 5, 0, "\x1b[3;3H\x1b[A")
	wantCursor(t, s, 2, 1)
	s.Write([]byte("\x1b[H"))
	wantCursor(t, s, 0, 0)
}

func TestScreenColumnAndRowAddressing(t *testing.T) {
	s := newScreen(t, 10, 5, 0, "\x1b[4G")
	wantCursor(t, s, 3, 0)
	s.Write([]byte("\x1b[3d"))
	wantCursor(t, s, 3, 2)
}

func TestScreenSaveRestoreCursor(t *testing.T) {
	s := newScreen(t, 10, 5, 0, "\x1b[3;4H\x1b7\x1b[1;1H\x1b8")
	wantCursor(t, s, 3, 2)
}

func TestScreenOriginMode(t *testing.T) {
	s := newScreen(t, 10, 6, 0, "\x1b[3;5r\x1b[?6h")
	// Origin mode homes the cursor to the top of the region.
	wantCursor(t, s, 0, 2)
	s.Write([]byte("\x1b[1;1H"))
	wantCursor(t, s, 0, 2)
	// Row 2 within the region is absolute row 3.
	s.Write([]byte("\x1b[2;1H"))
	wantCursor(t, s, 0, 3)
}

// --- erasing ---------------------------------------------------------------

func TestScreenEraseInLine(t *testing.T) {
	cases := []struct {
		name, seq, want string
	}{
		{"to end", "\x1b[0K", "ab"},
		{"to start", "\x1b[1K", "   de"},
		{"whole", "\x1b[2K", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := newScreen(t, 5, 1, 0, "abcde\x1b[3G"+c.seq)
			wantLines(t, s, c.want)
		})
	}
}

func TestScreenEraseInDisplay(t *testing.T) {
	const setup = "aa\r\nbb\r\ncc\x1b[2;2H"
	t.Run("to end", func(t *testing.T) {
		s := newScreen(t, 4, 3, 0, setup+"\x1b[0J")
		wantLines(t, s, "aa", "b", "")
	})
	t.Run("to start", func(t *testing.T) {
		s := newScreen(t, 4, 3, 0, setup+"\x1b[1J")
		wantLines(t, s, "", "", "cc")
	})
	t.Run("whole", func(t *testing.T) {
		s := newScreen(t, 4, 3, 0, setup+"\x1b[2J")
		wantLines(t, s, "", "", "")
	})
}

func TestScreenEraseKeepsBackground(t *testing.T) {
	// SGR 41 is a red background; erasing must leave it behind.
	s := newScreen(t, 4, 1, 0, "\x1b[41m\x1b[2K")
	for x := 0; x < 4; x++ {
		if got := s.Grid().Line(0).Cell(x).Style.BG; got != IndexedColor(1) {
			t.Fatalf("cell %d background = %v, want indexed 1", x, got)
		}
	}
}

func TestScreenEraseChars(t *testing.T) {
	s := newScreen(t, 6, 1, 0, "abcdef\x1b[2G\x1b[3X")
	wantLines(t, s, "a   ef")
}

// --- insert and delete -----------------------------------------------------

func TestScreenInsertAndDeleteChars(t *testing.T) {
	s := newScreen(t, 6, 1, 0, "abcdef\x1b[2G\x1b[2@")
	wantLines(t, s, "a  bcd")
	s = newScreen(t, 6, 1, 0, "abcdef\x1b[2G\x1b[2P")
	wantLines(t, s, "adef")
}

func TestScreenInsertMode(t *testing.T) {
	s := newScreen(t, 6, 1, 0, "abcd\x1b[2G\x1b[4hX")
	wantLines(t, s, "aXbcd")
}

func TestScreenInsertAndDeleteLines(t *testing.T) {
	s := newScreen(t, 4, 4, 0, "a\r\nb\r\nc\r\nd\x1b[2;1H\x1b[L")
	wantLines(t, s, "a", "", "b", "c")
	s = newScreen(t, 4, 4, 0, "a\r\nb\r\nc\r\nd\x1b[2;1H\x1b[M")
	wantLines(t, s, "a", "c", "d", "")
}

// TestScreenDeleteLinesDoesNotFeedHistory: lines an application deletes were
// never scrolled off the top, so they are not history.
func TestScreenDeleteLinesDoesNotFeedHistory(t *testing.T) {
	s := newScreen(t, 4, 3, 10, "a\r\nb\r\nc\x1b[1;1H\x1b[M")
	if n := s.MainGrid().HistoryLen(); n != 0 {
		t.Errorf("HistoryLen = %d, want 0", n)
	}
}

// --- SGR -------------------------------------------------------------------

func styleAt(s *Screen, x, y int) Style {
	return s.Grid().Line(y).Cell(x).Style
}

func TestScreenSGRAttributes(t *testing.T) {
	s := newScreen(t, 10, 1, 0, "\x1b[1;3;4mx")
	st := styleAt(s, 0, 0)
	if !st.Has(AttrBold) || !st.Has(AttrItalic) || !st.Has(AttrUnderline) {
		t.Errorf("attrs = %b, want bold+italic+underline", st.Attrs)
	}
	if st.Underline != UnderlineSingle {
		t.Errorf("underline = %v, want single", st.Underline)
	}
}

func TestScreenSGRReset(t *testing.T) {
	s := newScreen(t, 10, 1, 0, "\x1b[1;31m\x1b[0mx")
	if st := styleAt(s, 0, 0); !st.IsDefault() {
		t.Errorf("style = %+v, want default after SGR 0", st)
	}
}

func TestScreenSGRBareResets(t *testing.T) {
	// CSI m with no parameters is the same as CSI 0 m.
	s := newScreen(t, 10, 1, 0, "\x1b[1;31m\x1b[mx")
	if st := styleAt(s, 0, 0); !st.IsDefault() {
		t.Errorf("style = %+v, want default after a bare SGR", st)
	}
}

func TestScreenSGRColors(t *testing.T) {
	cases := []struct {
		name   string
		seq    string
		fg, bg Color
	}{
		{"basic", "\x1b[31;42m", IndexedColor(1), IndexedColor(2)},
		{"bright", "\x1b[91;102m", IndexedColor(9), IndexedColor(10)},
		{"256 semicolon", "\x1b[38;5;208;48;5;17m", IndexedColor(208), IndexedColor(17)},
		{"rgb semicolon", "\x1b[38;2;10;20;30m", RGBColor(10, 20, 30), DefaultColor},
		{"rgb colon", "\x1b[38:2::10:20:30m", RGBColor(10, 20, 30), DefaultColor},
		{"rgb colon short", "\x1b[38:2:10:20:30m", RGBColor(10, 20, 30), DefaultColor},
		{"256 colon", "\x1b[38:5:208m", IndexedColor(208), DefaultColor},
		{"default", "\x1b[31;41m\x1b[39;49m", DefaultColor, DefaultColor},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := newScreen(t, 4, 1, 0, c.seq+"x")
			st := styleAt(s, 0, 0)
			if st.FG != c.fg {
				t.Errorf("fg = %#v, want %#v", st.FG, c.fg)
			}
			if st.BG != c.bg {
				t.Errorf("bg = %#v, want %#v", st.BG, c.bg)
			}
		})
	}
}

// TestScreenSGRColorDoesNotLeakIntoAttributes: the arguments of a direct
// colour must be consumed, never applied as further attributes. 38;2;1;2;3
// ends with a 3, which would otherwise turn on italic.
func TestScreenSGRColorConsumesItsArguments(t *testing.T) {
	s := newScreen(t, 4, 1, 0, "\x1b[38;2;1;2;3mx")
	st := styleAt(s, 0, 0)
	if st.Has(AttrItalic) {
		t.Error("the blue component was applied as SGR 3 (italic)")
	}
	if st.FG != RGBColor(1, 2, 3) {
		t.Errorf("fg = %#v, want RGB(1,2,3)", st.FG)
	}
}

func TestScreenSGRUnderlineStyles(t *testing.T) {
	cases := map[string]UnderlineStyle{
		"\x1b[4:0m": UnderlineNone,
		"\x1b[4:1m": UnderlineSingle,
		"\x1b[4:2m": UnderlineDouble,
		"\x1b[4:3m": UnderlineCurly,
		"\x1b[21m":  UnderlineDouble,
	}
	for seq, want := range cases {
		s := newScreen(t, 4, 1, 0, seq+"x")
		if got := styleAt(s, 0, 0).Underline; got != want {
			t.Errorf("%q -> underline %v, want %v", seq, got, want)
		}
	}
}

func TestScreenSGRClearAttributes(t *testing.T) {
	s := newScreen(t, 4, 1, 0, "\x1b[1;3;4;7m\x1b[22;23;24;27mx")
	if st := styleAt(s, 0, 0); st.Attrs != 0 {
		t.Errorf("attrs = %b, want none left set", st.Attrs)
	}
}

// --- alternate screen ------------------------------------------------------

func TestScreenAltScreen(t *testing.T) {
	// 1049 clears the alternate screen but does not home the cursor, so an
	// application positions it itself — as this one does with CSI H.
	s := newScreen(t, 5, 2, 10, "main\x1b[?1049h")
	if !s.IsAlt() {
		t.Fatal("expected the alternate screen")
	}
	wantLines(t, s, "", "")
	s.Write([]byte("\x1b[Halt"))
	wantLines(t, s, "alt", "")

	s.Write([]byte("\x1b[?1049l"))
	if s.IsAlt() {
		t.Fatal("expected the main screen back")
	}
	wantLines(t, s, "main", "")
}

// TestScreenAltScreenDoesNotHomeCursor pins xterm's behaviour for 1049: the
// buffer is cleared but the cursor stays put. Homing it here would silently
// shift the first line an application draws without positioning first.
func TestScreenAltScreenDoesNotHomeCursor(t *testing.T) {
	s := newScreen(t, 10, 3, 0, "\x1b[2;5H\x1b[?1049h")
	wantCursor(t, s, 4, 1)
}

func TestScreenAltScreenRestoresCursor(t *testing.T) {
	s := newScreen(t, 10, 4, 0, "\x1b[3;5H\x1b[?1049h\x1b[1;1H\x1b[?1049l")
	wantCursor(t, s, 4, 2)
}

// TestScreenAltScreenKeepsNoHistory: applications on the alternate screen
// redraw in full, so their scrolling is not history.
func TestScreenAltScreenKeepsNoHistory(t *testing.T) {
	s := newScreen(t, 4, 2, 10, "\x1b[?1049h")
	s.Write([]byte("a\r\nb\r\nc\r\nd"))
	if n := s.MainGrid().HistoryLen(); n != 0 {
		t.Errorf("HistoryLen = %d, want 0 on the alternate screen", n)
	}
}

// --- modes -----------------------------------------------------------------

func TestScreenModes(t *testing.T) {
	s := newScreen(t, 10, 3, 0, "\x1b[?25l\x1b[?7l\x1b[?2004h\x1b[?1h")
	m := s.Modes()
	if m.CursorVisible {
		t.Error("DECTCEM should have hidden the cursor")
	}
	if m.AutoWrap {
		t.Error("DECAWM should be off")
	}
	if !m.BracketedPaste {
		t.Error("bracketed paste should be on")
	}
	if !m.ApplicationCur {
		t.Error("application cursor keys should be on")
	}
}

func TestScreenMouseModes(t *testing.T) {
	s := newScreen(t, 10, 3, 0, "\x1b[?1002h\x1b[?1006h")
	if got := s.Modes().Mouse; got != MouseButtonEvent {
		t.Errorf("mouse mode = %v, want button-event", got)
	}
	if got := s.Modes().MouseEncoding; got != MouseEncodingSGR {
		t.Errorf("mouse encoding = %v, want SGR", got)
	}
	// A reset for a mode that is not the active one must not disable tracking.
	s.Write([]byte("\x1b[?1000l"))
	if got := s.Modes().Mouse; got != MouseButtonEvent {
		t.Errorf("mouse mode = %v after an unrelated reset, want button-event", got)
	}
	s.Write([]byte("\x1b[?1002l"))
	if got := s.Modes().Mouse; got != MouseOff {
		t.Errorf("mouse mode = %v after its own reset, want off", got)
	}
}

// --- titles and replies ----------------------------------------------------

func TestScreenTitle(t *testing.T) {
	s := newScreen(t, 10, 1, 0, "\x1b]0;⠁ working\x07")
	if got := s.Title(); got != "⠁ working" {
		t.Errorf("Title = %q", got)
	}
	s.Write([]byte("\x1b]2;done\x1b\\"))
	if got := s.Title(); got != "done" {
		t.Errorf("Title = %q, want %q", got, "done")
	}
}

func TestScreenTitleHook(t *testing.T) {
	s := NewScreen(10, 1, 0)
	var seen []string
	s.OnTitle = func(t string) { seen = append(seen, t) }
	s.Write([]byte("\x1b]0;one\x07\x1b]0;one\x07\x1b]0;two\x07"))
	// An unchanged title must not fire the hook: it would wake every client.
	eq(t, "titles", seen, []string{"one", "two"})
}

func TestScreenCursorPositionReport(t *testing.T) {
	s := NewScreen(10, 5, 0)
	var got []byte
	s.Reply = func(b []byte) { got = append(got[:0], b...) }
	s.Write([]byte("\x1b[3;4H\x1b[6n"))
	if string(got) != "\x1b[3;4R" {
		t.Errorf("CPR = %q, want %q", got, "\x1b[3;4R")
	}
}

func TestScreenDeviceAttributes(t *testing.T) {
	s := NewScreen(10, 2, 0)
	var got string
	s.Reply = func(b []byte) { got = string(b) }
	s.Write([]byte("\x1b[c"))
	if got == "" || !strings.HasPrefix(got, "\x1b[?") {
		t.Errorf("DA reply = %q, want a primary device-attributes response", got)
	}
}

func TestScreenReplyIsSafeWithoutHook(t *testing.T) {
	// A screen used only to inspect output has no upstream; asking it for a
	// report must not panic.
	s := newScreen(t, 10, 2, 0, "\x1b[6n\x1b[c\x1b[5n")
	wantLines(t, s, "", "")
}

// --- resize and reset ------------------------------------------------------

func TestScreenResize(t *testing.T) {
	s := newScreen(t, 10, 3, 10, "hello")
	s.Resize(20, 5)
	if c, r := s.Size(); c != 20 || r != 5 {
		t.Fatalf("size = %dx%d, want 20x5", c, r)
	}
	if got := s.Grid().Line(0).Text(); got != "hello" {
		t.Errorf("row 0 = %q, want %q", got, "hello")
	}
	if top, bottom := s.ScrollRegion(); top != 0 || bottom != 4 {
		t.Errorf("scroll region = %d..%d, want 0..4", top, bottom)
	}
}

func TestScreenResizeClampsCursor(t *testing.T) {
	s := newScreen(t, 10, 5, 0, "\x1b[5;9H")
	s.Resize(4, 2)
	wantCursor(t, s, 3, 1)
}

func TestScreenDECALN(t *testing.T) {
	s := newScreen(t, 3, 2, 0, "\x1b#8")
	wantLines(t, s, "EEE", "EEE")
	wantCursor(t, s, 0, 0)
}

func TestScreenReset(t *testing.T) {
	s := newScreen(t, 6, 3, 10, "a\r\nb\r\nc\r\nd\x1b[31m\x1b[?1049h\x1b]0;t\x07")
	s.Write([]byte("\x1bc"))
	if s.IsAlt() {
		t.Error("RIS should return to the main screen")
	}
	if s.Title() != "" {
		t.Error("RIS should clear the title")
	}
	if n := s.MainGrid().HistoryLen(); n != 0 {
		t.Errorf("HistoryLen = %d, want 0 after RIS", n)
	}
	wantLines(t, s, "", "", "")
	wantCursor(t, s, 0, 0)
	if !s.Modes().AutoWrap || !s.Modes().CursorVisible {
		t.Error("RIS should restore the default modes")
	}
}

// --- robustness ------------------------------------------------------------

// TestScreenUnknownSequencesNeverReachTheGrid is the property that keeps a
// terminal from printing garbage when an application uses something tend does
// not implement.
func TestScreenUnknownSequencesNeverReachTheGrid(t *testing.T) {
	seqs := []string{
		"\x1b[>4;2m",        // xterm modifyOtherKeys
		"\x1b[?2026h",       // synchronised output
		"\x1b[18t",          // window reporting
		"\x1b]11;?\x07",     // background colour query
		"\x1bP+q544e\x1b\\", // XTGETTCAP
		"\x1b_Gf=100;AAAA\x1b\\",
		"\x1b[999;999;999Z",
	}
	for _, seq := range seqs {
		t.Run(strings.TrimSpace(seq), func(t *testing.T) {
			s := newScreen(t, 10, 2, 0, seq)
			wantLines(t, s, "", "")
		})
	}
}

// TestScreenChunkIndependence: PTY reads split at arbitrary byte boundaries,
// so feeding a session one byte at a time must land on the same screen.
func TestScreenChunkIndependence(t *testing.T) {
	session := "\x1b[?1049h\x1b[2J\x1b[1;1H" +
		"\x1b[1;32m⠁ working\x1b[0m\r\n" +
		"世界 wide\r\n\x1b[38:2::10:20:30mcolour\r\n" +
		"\x1b[3;5r\x1b[2;1Hscrolled\r\n\x1b[?1049l" +
		"back on main\r\n\x1b]0;title\x07"

	whole := NewScreen(20, 6, 20)
	whole.Write([]byte(session))

	split := NewScreen(20, 6, 20)
	for i := 0; i < len(session); i++ {
		split.Write([]byte(session[i : i+1]))
	}

	eq(t, "viewport", screenLines(split), screenLines(whole))
	eq(t, "history", screenHistory(split), screenHistory(whole))
	if split.Title() != whole.Title() {
		t.Errorf("title: split=%q whole=%q", split.Title(), whole.Title())
	}
	if split.Cursor() != whole.Cursor() {
		t.Errorf("cursor: split=%+v whole=%+v", split.Cursor(), whole.Cursor())
	}
}

func BenchmarkScreenPlainOutput(b *testing.B) {
	data := []byte(strings.Repeat("the quick brown fox jumps over the lazy dog\r\n", 64))
	s := NewScreen(100, 40, 1000)
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		s.Write(data)
	}
}

func BenchmarkScreenStyledOutput(b *testing.B) {
	data := []byte(strings.Repeat("\x1b[1;38:2::200:100:50mstatus\x1b[0m line here\r\n", 64))
	s := NewScreen(100, 40, 1000)
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		s.Write(data)
	}
}

// TestScreenClipboardWrite: a program that holds the mouse does its own
// selecting and hands the result over with OSC 52. Dropping it makes every
// such copy fail without a word.
func TestScreenClipboardWrite(t *testing.T) {
	s := NewScreen(40, 5, 0)
	var got [][]byte
	s.OnClipboard = func(b []byte) { got = append(got, append([]byte(nil), b...)) }

	// "hello world", to the clipboard selection, terminated either way.
	_, _ = s.Write([]byte("\x1b]52;c;aGVsbG8gd29ybGQ=\x07"))
	_, _ = s.Write([]byte("\x1b]52;c;c2Vjb25k\x1b\\"))
	if len(got) != 2 || string(got[0]) != "hello world" || string(got[1]) != "second" {
		t.Fatalf("clipboard writes = %q", got)
	}

	// A read request is not answered: that would let anything running in a
	// pane see whatever the user last copied anywhere.
	got = nil
	_, _ = s.Write([]byte("\x1b]52;c;?\x07"))
	// Nor is rubbish, nor an empty payload.
	_, _ = s.Write([]byte("\x1b]52;c;!!!not-base64!!!\x07"))
	_, _ = s.Write([]byte("\x1b]52;c;\x07"))
	if len(got) != 0 {
		t.Errorf("these should all be ignored, got %q", got)
	}

	// A long copy survives: the limit is sized for this, not for titles.
	long := strings.Repeat("a line of a long answer\n", 8000) // ~190KB
	_, _ = s.Write([]byte("\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte(long)) + "\x07"))
	if len(got) != 1 || string(got[0]) != long {
		t.Errorf("a long copy was dropped or damaged: %d writes", len(got))
	}
}

// TestHyperlinksAreKeptPerCellAndSurviveRendering: OSC 8 links the text
// printed under it, ends on an empty URI, keeps semicolons in the URI, and
// comes back from a render as the server sends a screen to a client. If it
// regresses, a link Claude Code writes as words cannot be opened.
func TestHyperlinksAreKeptPerCellAndSurviveRendering(t *testing.T) {
	s := NewScreen(40, 3, 10)
	_, _ = s.Write([]byte("see \x1b]8;;https://x.org/a;b\x07the docs\x1b]8;;\x07 now"))
	cell := func(scr *Screen, x int) string { return scr.Hyperlink(scr.Grid().Line(0).Cell(x).Link) }
	if cell(s, 0) != "" || cell(s, 4) != "https://x.org/a;b" || cell(s, 11) != "https://x.org/a;b" || cell(s, 13) != "" {
		t.Fatalf("links: %q %q %q %q", cell(s, 0), cell(s, 4), cell(s, 11), cell(s, 13))
	}

	copy := NewScreen(40, 3, 10)
	_, _ = copy.Write(RenderScreen(s))
	if cell(copy, 4) != "https://x.org/a;b" || cell(copy, 12) != "" {
		t.Errorf("after rendering: %q %q", cell(copy, 4), cell(copy, 12))
	}
	if !strings.Contains(copy.Grid().Line(0).Text(), "see the docs now") {
		t.Errorf("the text: %q", copy.Grid().Line(0).Text())
	}
}

// TestHyperlinkTableIsBounded: a program that links everything does not
// grow the table past its bound; the text still shows.
func TestHyperlinkTableIsBounded(t *testing.T) {
	s := NewScreen(20, 2, 0)
	for i := 0; i < maxLinks+10; i++ {
		_, _ = s.Write([]byte("\x1b]8;;https://x/" + strconv.Itoa(i) + "\x07a\x1b]8;;\x07\r"))
	}
	if len(s.links) > maxLinks {
		t.Errorf("%d links kept", len(s.links))
	}
}

// TestTheWheelIsArrowsOnTheAlternateScreenWithoutTheMouse: herdr's
// alternate scroll — mode 1007, on by default — makes the wheel arrow keys
// for a program on the alternate screen that did not ask for the mouse,
// and only for it. If it regresses, the wheel over a full-screen TUI that
// draws in place (the dsh TUI, a pager) scrolls nothing it drew.
func TestTheWheelIsArrowsOnTheAlternateScreenWithoutTheMouse(t *testing.T) {
	s := newScreen(t, 10, 3, 10, "")
	if !s.Modes().AlternateScroll {
		t.Fatal("1007 is off by default")
	}
	if s.WheelKeys() {
		t.Error("arrows on the main screen")
	}
	_, _ = s.Write([]byte("\x1b[?1049h"))
	if !s.WheelKeys() {
		t.Error("no arrows on the alternate screen")
	}
	_, _ = s.Write([]byte("\x1b[?1000h"))
	if s.WheelKeys() {
		t.Error("arrows for a program that takes the mouse")
	}
	_, _ = s.Write([]byte("\x1b[?1000l\x1b[?1007l"))
	if s.WheelKeys() {
		t.Error("arrows with 1007 turned off")
	}
}
