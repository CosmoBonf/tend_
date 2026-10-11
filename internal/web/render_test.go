package web

import (
	"strings"
	"testing"
)

// TestAScreenIsDrawnAsHTML: colours, bold and the page's own escaping. If
// it regresses, an agent's output shows as markup — or worse, a "<script>"
// an agent printed runs in the phone's page.
func TestAScreenIsDrawnAsHTML(t *testing.T) {
	ansi := []byte("\x1b[H\x1b[0;1;31mred bold\x1b[0m plain\r\n<script>alert(1)</script> & \x1b[38;2;1;2;3mrgb\x1b[0m")
	got := ScreenHTML(ansi, 40, 5)
	for _, want := range []string{
		`<span style="color:#e06c75;font-weight:bold">red bold</span> plain`,
		`&lt;script&gt;alert(1)&lt;/script&gt; &amp; <span style="color:#010203">rgb</span>`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("%q missing from:\n%s", want, got)
		}
	}
	if strings.Count(got, "\n") != 1 {
		t.Fatalf("blank rows at the end were kept:\n%q", got)
	}
}

func TestIndexedColoursFollowTheXtermPalette(t *testing.T) {
	for i, want := range map[int]string{16: "#000000", 21: "#0000ff", 196: "#ff0000", 232: "#080808", 255: "#eeeeee"} {
		if got := indexedCSS(i); got != want {
			t.Errorf("colour %d = %s, want %s", i, got, want)
		}
	}
}
