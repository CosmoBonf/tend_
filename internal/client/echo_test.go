//go:build unix

package client

import (
	"strings"
	"testing"
	"time"

	"github.com/auth-com-br/tend/internal/server"
)

// TestAKeyTypedIntoAQuietPaneIsEchoedWithoutWaitingForTheFrame: the screen
// a key's echo makes is sent as soon as the pane prints it, not on the
// server's next frame tick. With frames a second apart, every echo comes
// back in a fraction of one. If it regresses, each key waits for the tick
// before it can be seen — up to 33 ms on top of the network's, which over
// the owner's VPN made typing lag behind the fingers.
func TestAKeyTypedIntoAQuietPaneIsEchoedWithoutWaitingForTheFrame(t *testing.T) {
	h := newHarnessWith(t, server.Config{FrameInterval: time.Second})
	ws, err := h.client.NewWorkspace("main")
	if err != nil {
		t.Fatal(err)
	}
	_, pane, err := h.client.NewTab(ws, "t", shellSpec("stty -echo; exec cat"))
	if err != nil {
		t.Fatal(err)
	}
	if err := h.client.SubscribePanes([]uint64{pane}); err != nil {
		t.Fatal(err)
	}

	for _, word := range []string{"alpha", "bravo", "charlie"} {
		// Quiet for longer than a frame, as a pane between keys is.
		time.Sleep(1200 * time.Millisecond)
		start := time.Now()
		if err := h.client.SendInput(pane, []byte(word+"\n")); err != nil {
			t.Fatal(err)
		}
		waitFor(t, word+" echoed", func() bool { return strings.Contains(h.rec.screen(pane), word) })
		if took := time.Since(start); took > 400*time.Millisecond {
			t.Errorf("%s came back after %v, as if it waited for the frame", word, took)
		}
	}
}
