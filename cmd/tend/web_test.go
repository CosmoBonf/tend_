//go:build unix

package main

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/auth-com-br/tend/internal/pty"
	"github.com/auth-com-br/tend/internal/server"
	"github.com/auth-com-br/tend/internal/transport"
	"github.com/auth-com-br/tend/internal/web"
)

// TestThePhonePageWatchesAndMessagesARealAgent: against a real session, the
// page lists a pane that is an agent, streams its screen, and a message
// typed on the phone reaches it with its Enter; a plain shell in the same
// session is neither listed nor reachable. If it regresses, `tend web`
// shows nothing, cannot talk to the agent, or talks to a shell.
func TestThePhonePageWatchesAndMessagesARealAgent(t *testing.T) {
	t.Setenv("TEND_RUNTIME_DIR", t.TempDir())
	path, err := transport.SocketPath("webtest")
	if err != nil {
		t.Fatal(err)
	}
	ln, err := transport.Listen(path)
	if err != nil {
		t.Fatal(err)
	}
	srv, err := server.New(server.Config{DetectInterval: 10 * time.Millisecond, DefaultSize: pty.Size{Cols: 80, Rows: 24}})
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	ws, _ := srv.NewWorkspace("api")
	// An "agent" that echoes what it is sent, line by line.
	_, agent, err := srv.NewTab(ws, "t", server.PaneSpec{
		Command: []string{"/bin/sh", "-c", `echo READY; while read l; do echo "GOT:$l"; done`}, Agent: "claude",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, shell, err := srv.NewTab(ws, "s", server.PaneSpec{Command: []string{"/bin/sh"}})
	if err != nil {
		t.Fatal(err)
	}

	link := &webSession{name: "webtest"}
	const token = "abcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcd"
	page := web.New(link, token)
	link.srv = page
	if err := link.dial(); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(page.Handler())
	defer ts.Close()
	cookie := &http.Cookie{Name: "tend_web", Value: token}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", ts.URL+"/api/events?pane="+strconv.FormatUint(uint64(agent), 10), nil)
	req.AddCookie(cookie)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	lines := make(chan string, 1024)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		for sc.Scan() {
			lines <- sc.Text()
		}
		close(lines)
	}()
	waitFor := func(what string, want ...string) {
		t.Helper()
		var seen strings.Builder
		for {
			select {
			case l, ok := <-lines:
				if !ok {
					t.Fatalf("the stream ended waiting for %s:\n%s", what, seen.String())
				}
				seen.WriteString(l + "\n")
				all := true
				for _, w := range want {
					all = all && strings.Contains(seen.String(), w)
				}
				if all {
					return
				}
			case <-ctx.Done():
				t.Fatalf("timed out waiting for %s:\n%s", what, seen.String())
			}
		}
	}
	waitFor("the agent listed and its screen", `"agent":"claude"`, "READY")

	post := func(body string) int {
		r, _ := http.NewRequestWithContext(ctx, "POST", ts.URL+"/api/prompt", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.AddCookie(cookie)
		res, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}
	if code := post(`{"pane":` + strconv.FormatUint(uint64(agent), 10) + `,"text":"hello from the phone"}`); code != http.StatusOK {
		t.Fatalf("prompt answered %d", code)
	}
	waitFor("the message to reach the agent", "GOT:hello from the phone")
	if code := post(`{"pane":` + strconv.FormatUint(uint64(shell), 10) + `,"text":"touch /tmp/x"}`); code != http.StatusForbidden {
		t.Fatalf("a shell took a message: %d", code)
	}
}
