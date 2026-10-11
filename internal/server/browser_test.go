//go:build unix

package server

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/auth-com-br/tend/internal/proto"
)

// TestCommandsGoToTheBrowsersAttached: a command reaches every browser
// attached and says how many; with none it is ErrNoBrowser, so the client
// opens the page itself; a page that is not http(s) is refused; a browser
// let go gets nothing more; what a browser captures is the browser's in the
// context. If it regresses, a page asked for goes nowhere and nobody is
// told, or a browser is sent a file: URL.
func TestCommandsGoToTheBrowsersAttached(t *testing.T) {
	s, err := New(Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if _, err := s.SendBrowser(BrowserCommand{Action: BrowserOpen, URL: "https://example.com"}); !errors.Is(err, ErrNoBrowser) {
		t.Errorf("none attached: %v", err)
	}
	cmds, detach := s.AttachBrowser("test")
	if names := s.Browsers(); len(names) != 1 || names[0] != "test" {
		t.Errorf("attached: %v", names)
	}
	if n, err := s.SendBrowser(BrowserCommand{Action: BrowserNavigate, URL: "https://example.com/login"}); err != nil || n != 1 {
		t.Fatalf("sent to %d: %v", n, err)
	}
	if got := <-cmds; got.Action != BrowserNavigate || got.URL != "https://example.com/login" {
		t.Errorf("received %+v", got)
	}
	if _, err := s.SendBrowser(BrowserCommand{Action: BrowserOpen, URL: "file:///etc/passwd"}); err == nil {
		t.Error("a file: URL is refused")
	}
	detach()
	if _, err := s.SendBrowser(BrowserCommand{Action: BrowserSelect, On: true}); !errors.Is(err, ErrNoBrowser) {
		t.Errorf("let go: %v", err)
	}

	it, err := s.BrowserCapture(proto.ContextItem{URL: "https://example.com/login", Selector: "#email"})
	if err != nil || it.Kind != "element" || it.Source != "browser" {
		t.Errorf("captured: %+v %v", it, err)
	}
	if _, err := s.BrowserSendToAgent([]proto.ContextItem{{Selector: "#go"}}, 0, ""); err == nil {
		t.Error("no pane in focus, none named: an error")
	}
}

// TestPickedElementsGoTogetherAndCannotAct: elements picked on a page, with
// the notes on them, are typed into the pane as one message. A program that
// takes pastes (an agent) gets it as a bracketed paste with its lines; one
// that does not (a shell) gets it on one line, since each break would be
// Enter — none of it is pressed. If it regresses, a page's text sent to a
// shell runs as commands, or the notes do not reach the agent.
func TestPickedElementsGoTogetherAndCannotAct(t *testing.T) {
	s := newServer(t)
	dir := t.TempDir()
	items := []proto.ContextItem{
		{URL: "https://example.com/", Selector: "#save", Tag: "button", Text: "Salvar", Note: "should be green"},
		{URL: "https://example.com/", Selector: "h1", Tag: "h1", Text: "Pedidos\nrm -rf ~", Note: "too big on phones"},
	}
	for _, c := range []struct {
		name, setup string
		paste       bool
	}{
		{"agent", `printf '\033[?2004h'; `, true},
		{"shell", ``, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			out := dir + "/" + c.name
			ws, err := s.NewWorkspace(c.name)
			if err != nil {
				t.Fatal(err)
			}
			_, pane, err := s.NewTab(ws, c.name, shell(c.setup+`stty -icanon -echo min 1; exec cat > `+out))
			if err != nil {
				t.Fatal(err)
			}
			if c.paste {
				waitFor(t, "bracketed paste asked for", func() bool {
					rt, err := s.runtime(pane)
					return err == nil && rt.modes().BracketedPaste
				})
			}
			time.Sleep(200 * time.Millisecond) // stty has run
			if _, err := s.BrowserSendToAgent(append([]proto.ContextItem(nil), items...), pane, "fix these on mobile"); err != nil {
				t.Fatal(err)
			}
			var got string
			waitFor(t, "the text typed", func() bool {
				raw, _ := os.ReadFile(out)
				got = string(raw)
				return strings.Contains(got, "too big on phones")
			})
			if !strings.Contains(got, "fix these on mobile") || strings.Index(got, "fix these on mobile") > strings.Index(got, "Context captured") {
				t.Errorf("the message first: %q", got)
			}
			if !strings.Contains(got, "note: should be green") || !strings.Contains(got, "[2] element (from browser)") {
				t.Errorf("both, with their notes: %q", got)
			}
			if c.paste && (!strings.HasPrefix(got, "\x1b[200~") || !strings.Contains(got, "\nrm -rf ~")) {
				t.Errorf("a paste, with its lines: %q", got)
			}
			if !c.paste && (strings.ContainsAny(got, "\n\r") || !strings.Contains(got, " ⏎ rm -rf ~")) {
				t.Errorf("one line, nothing pressed: %q", got)
			}
		})
	}
}

// TestWhatABrowserSendsLandsInTheContext: a browser's elements, with their
// notes and the user's message for them all, go into the context — the
// message first, as text — and the clients are told they arrived, for the
// context panel to show them. If it regresses, "send to tend" in the
// browser seems to do nothing.
func TestWhatABrowserSendsLandsInTheContext(t *testing.T) {
	s := newServer(t)
	sub := s.Subscribe(16)
	defer sub.Close()
	kept, err := s.BrowserCaptureAll([]proto.ContextItem{
		{URL: "https://example.com/", Selector: "#save", Note: "green"},
		{URL: "https://example.com/", Selector: "h1", Note: "smaller"},
	}, "fix these on mobile")
	if err != nil {
		t.Fatal(err)
	}
	if len(kept) != 3 || kept[0].Kind != "text" || kept[0].Text != "fix these on mobile" || kept[1].Selector != "#save" || kept[2].Source != "browser" {
		t.Errorf("kept: %+v", kept)
	}
	ev := waitForEvent(t, sub, func(e Event) bool { return e.Kind == EventContextArrived })
	if ev.Title != "browser" || ev.Body != "3" {
		t.Errorf("arrived: %+v", ev)
	}
	if _, err := s.BrowserCaptureAll(nil, "  "); err == nil {
		t.Error("nothing is refused")
	}
}
