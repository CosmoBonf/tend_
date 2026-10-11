package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/auth-com-br/tend/internal/client"
	"github.com/auth-com-br/tend/internal/proto"
)

// `tend gui-bridge`: a session for the desktop app (issue #40), spoken as
// JSON, one message a line, on stdin and stdout. The app is a web page in
// Neutralino, which starts a process and talks to it through its stdin and
// stdout as text (os.spawnProcess) — so this is the session's own protocol,
// relayed, with nothing new on the server's side and nothing listening on a
// network.
//
// From the app, one a line:
//
//	{"t":"call","id":1,"method":"session.snapshot","params":{...}}
//	{"t":"input","pane":3,"data":"ls\r"}
//
// To the app, one a line:
//
//	{"t":"ready","build":"v0.14.0"}
//	{"t":"res","id":1,"result":{...}}   or   {"t":"res","id":1,"error":"..."}
//	{"t":"event","event":{...}}
//	{"t":"out","pane":3,"data":"<the pane's whole screen, as ANSI>"}
//	{"t":"closed"}
//
// A pane's screen comes whole each time (the server sends states, not
// changes), so the app draws it over what it had and can never fall out of
// step with it.

// bridgeIn is a message from the app.
type bridgeIn struct {
	T      string          `json:"t"`
	ID     uint64          `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Pane   uint64          `json:"pane,omitempty"`
	Data   string          `json:"data,omitempty"`
}

// bridgeOut is a message to the app.
type bridgeOut struct {
	T      string          `json:"t"`
	ID     uint64          `json:"id,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
	Event  *proto.Event    `json:"event,omitempty"`
	Pane   uint64          `json:"pane,omitempty"`
	Data   string          `json:"data,omitempty"`
	Build  string          `json:"build,omitempty"`
}

// bridgeWriter writes the app's messages one whole line at a time: the
// screens, the events and the answers come from different goroutines.
type bridgeWriter struct {
	mu  sync.Mutex
	enc *json.Encoder
}

func (w *bridgeWriter) send(m bridgeOut) {
	w.mu.Lock()
	defer w.mu.Unlock()
	_ = w.enc.Encode(m)
}

// bridgeHandler hands what the server sends to the app.
type bridgeHandler struct {
	w      *bridgeWriter
	closed chan struct{}
	once   sync.Once
}

func (h *bridgeHandler) Disconnected() {
	h.once.Do(func() {
		h.w.send(bridgeOut{T: "closed"})
		close(h.closed)
	})
}

func (h *bridgeHandler) Event(ev proto.Event) {
	h.w.send(bridgeOut{T: "event", Event: &ev})
}

func (h *bridgeHandler) PaneOutput(pane uint64, data []byte) {
	h.w.send(bridgeOut{T: "out", Pane: pane, Data: string(data)})
}

func runGUIBridge(args []string) error {
	fs := flag.NewFlagSet("gui-bridge", flag.ExitOnError)
	name := sessionFlag(fs)
	fs.Usage = func() {
		fs.Output().Write([]byte("usage: tend gui-bridge [-s session]\n\n" +
			"A session for the desktop app, as JSON lines on stdin and stdout.\n\n"))
		fs.PrintDefaults()
	}
	_ = fs.Parse(args)

	w := &bridgeWriter{enc: json.NewEncoder(os.Stdout)}
	w.enc.SetEscapeHTML(false)
	h := &bridgeHandler{w: w, closed: make(chan struct{})}
	c, err := connect(*name, h)
	if err != nil {
		return err
	}
	defer c.Close()
	w.send(bridgeOut{T: "ready", Build: version})
	return serveBridge(c, os.Stdin, w, h.closed)
}

// serveBridge relays the app's messages to the session until the app's
// stdin ends or the session goes.
func serveBridge(c *client.Client, in io.Reader, w *bridgeWriter, closed <-chan struct{}) error {
	lines := make(chan []byte)
	go func() {
		defer close(lines)
		r := bufio.NewReaderSize(in, 64*1024)
		for {
			line, err := r.ReadBytes('\n')
			if len(line) > 0 {
				lines <- line
			}
			if err != nil {
				return
			}
		}
	}()
	for {
		select {
		case <-closed:
			return errors.New("the session ended")
		case line, ok := <-lines:
			if !ok {
				return nil // the app went away
			}
			var m bridgeIn
			if err := json.Unmarshal(line, &m); err != nil {
				w.send(bridgeOut{T: "res", Error: "not a message: " + err.Error()})
				continue
			}
			switch m.T {
			case "input":
				if err := c.SendInput(m.Pane, []byte(m.Data)); err != nil {
					w.send(bridgeOut{T: "res", Error: err.Error()})
				}
			case "call":
				// Each call on its own, so a slow one (an MCP test) does
				// not hold the keystrokes behind it.
				go func(m bridgeIn) {
					var params any
					if len(m.Params) > 0 {
						params = m.Params
					}
					var result json.RawMessage
					err := c.Call(m.Method, params, &result)
					out := bridgeOut{T: "res", ID: m.ID, Result: result}
					if err != nil {
						out.Result, out.Error = nil, refusalText(err)
					}
					w.send(out)
				}(m)
			default:
				w.send(bridgeOut{T: "res", ID: m.ID, Error: fmt.Sprintf("unknown message %q", m.T)})
			}
		}
	}
}
