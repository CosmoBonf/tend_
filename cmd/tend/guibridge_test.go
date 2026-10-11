//go:build unix

package main

import (
	"bufio"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/auth-com-br/tend/internal/pty"
	"github.com/auth-com-br/tend/internal/server"
	"github.com/auth-com-br/tend/internal/transport"
)

// TestTheDesktopBridgeRelaysASessionAsJSONLines: through gui-bridge's
// JSON lines a page makes a space and a pane, subscribes to it, types into
// it and gets its screen back with what it typed; a broken line is answered
// with an error and the bridge goes on. If it regresses, the desktop app
// (issue #40) shows nothing, or one malformed message cuts it off.
func TestTheDesktopBridgeRelaysASessionAsJSONLines(t *testing.T) {
	t.Setenv("TEND_RUNTIME_DIR", t.TempDir())
	path, err := transport.SocketPath("bridgetest")
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

	out, outW := io.Pipe()
	in, inW := io.Pipe()
	w := &bridgeWriter{enc: json.NewEncoder(outW)}
	h := &bridgeHandler{w: w, closed: make(chan struct{})}
	c, err := connect("bridgetest", h)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	go func() { _ = serveBridge(c, in, w, h.closed) }()

	msgs := make(chan bridgeOut, 256)
	go func() {
		sc := bufio.NewScanner(out)
		sc.Buffer(make([]byte, 64*1024), 4<<20)
		for sc.Scan() {
			var m bridgeOut
			if json.Unmarshal(sc.Bytes(), &m) == nil {
				msgs <- m
			}
		}
	}()
	send := func(line string) {
		if _, err := io.WriteString(inW, line+"\n"); err != nil {
			t.Fatal(err)
		}
	}
	wait := func(what string, pred func(bridgeOut) bool) bridgeOut {
		deadline := time.After(10 * time.Second)
		for {
			select {
			case m := <-msgs:
				if pred(m) {
					return m
				}
			case <-deadline:
				t.Fatalf("timed out waiting for %s", what)
			}
		}
	}
	answer := func(id uint64) json.RawMessage {
		m := wait("the answer to call "+itoa(int(id)), func(m bridgeOut) bool { return m.T == "res" && m.ID == id })
		if m.Error != "" {
			t.Fatalf("call %d: %s", id, m.Error)
		}
		return m.Result
	}

	send(`this is not json`)
	wait("the error for a broken line", func(m bridgeOut) bool { return m.T == "res" && strings.Contains(m.Error, "not a message") })

	send(`{"t":"call","id":1,"method":"workspace.new","params":{"name":"gui"}}`)
	var ws struct{ Workspace uint64 }
	_ = json.Unmarshal(answer(1), &ws)
	send(`{"t":"call","id":2,"method":"tab.new","params":{"workspace":` + itoa(int(ws.Workspace)) + `,"name":"t","pane":{"command":["/bin/sh","-c","echo started-by-the-page; exec /bin/sh"]}}}`)
	var tab struct{ Pane uint64 }
	_ = json.Unmarshal(answer(2), &tab)
	send(`{"t":"call","id":3,"method":"pane.subscribe","params":{"panes":[` + itoa(int(tab.Pane)) + `]}}`)
	answer(3)
	// The screen as it is now, as the page asks for it when it shows a pane:
	// the command asked for ran, not a shell in its place.
	deadline := time.Now().Add(10 * time.Second)
	for i := uint64(10); ; i++ {
		send(`{"t":"call","id":` + itoa(int(i)) + `,"method":"pane.screen","params":{"pane":` + itoa(int(tab.Pane)) + `}}`)
		var screen struct{ Text string }
		_ = json.Unmarshal(answer(i), &screen)
		if strings.Contains(screen.Text, "started-by-the-page") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the pane is not running the command asked for:\n%s", screen.Text)
		}
		time.Sleep(100 * time.Millisecond)
	}
	send(`{"t":"input","pane":` + itoa(int(tab.Pane)) + `,"data":"echo from-the-page-$((6*7))\r"}`)
	wait("the pane's screen with the command's output", func(m bridgeOut) bool {
		return m.T == "out" && m.Pane == tab.Pane && strings.Contains(m.Data, "from-the-page-42")
	})
}
