//go:build unix

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestTheLogButtonOpensAndHidesTheLivePane: "▴ log" on the status bar opens
// a pane following ~/tend-dev.log under the agent, the button turns to
// "▾ log", and a second click puts the pane away. If it regresses, the
// owner is back to typing a tail command to watch what an agent runs.
func TestTheLogButtonOpensAndHidesTheLivePane(t *testing.T) {
	home := t.TempDir()
	for _, v := range []string{"GOPATH", "GOMODCACHE", "GOCACHE"} {
		if out, err := exec.Command("go", "env", v).Output(); err == nil {
			t.Setenv(v, strings.TrimSpace(string(out)))
		}
	}
	t.Setenv("HOME", home)
	if err := os.WriteFile(filepath.Join(home, "tend-dev.log"), []byte("==== 10:00:00 · make check ====\nok  all green\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	a := startSession(t, 160, 40)
	bar := func() string { return a.lines()[len(a.lines())-1] }
	a.waitForScreen(t, "the log button", func(string) bool { return strings.Contains(bar(), "▴ log") })
	a.clickAt(t, columnOfString(bar(), "▴ log")+2, len(a.lines()))
	a.waitForScreen(t, "the log in a pane", func(s string) bool {
		return strings.Contains(s, "ok  all green") && strings.Contains(bar(), "▾ log")
	})

	a.clickAt(t, columnOfString(bar(), "▾ log")+2, len(a.lines()))
	a.waitForScreen(t, "the log put away", func(s string) bool {
		return !strings.Contains(s, "ok  all green") && strings.Contains(bar(), "▴ log")
	})
}

// TestALogPaneOpenedByHandIsTheOneTheButtonHides: a pane titled
// "claude · log", as the owner's was from before the button, is the log
// pane — if it regresses, the bar says "▴ log" with a log showing, and
// each click adds another pane beside the one it cannot see.
func TestALogPaneOpenedByHandIsTheOneTheButtonHides(t *testing.T) {
	for _, title := range []string{"log", "claude · log"} {
		if !isLogTitle(title) {
			t.Errorf("%q is not taken for the log pane", title)
		}
	}
	for _, title := range []string{"files", "catalog", "✳ log in flow"} {
		if isLogTitle(title) {
			t.Errorf("%q is taken for the log pane", title)
		}
	}
}

// TestTheStatusBarSaysHowToReachTheSessionAndCopiesIt: the bar shows the
// ssh target another machine reaches this one by, and a click copies the
// command to type there. If it regresses, the owner on the laptop has to
// remember the address and the flag.
func TestTheStatusBarSaysHowToReachTheSessionAndCopiesIt(t *testing.T) {
	target := localTarget()
	if target == "" {
		t.Skip("this machine has no address another could reach it at")
	}
	a := startSession(t, 200, 30)
	bar := func() string { return a.lines()[len(a.lines())-1] }
	a.waitForScreen(t, "the remote shortcut", func(string) bool { return strings.Contains(bar(), "⇄ "+target) })
	a.clickAt(t, columnOfString(bar(), "⇄ "+target)+3, len(a.lines()))
	a.waitForScreen(t, "the command copied", func(s string) bool {
		return strings.Contains(s, "copied: tend --remote "+target)
	})
}

// TestTheAddressGivenIsAVPNsBeforeTheLANsAndNeverAContainersBridge: the
// owner's machine has wg1 at 10.8.0.2, which the laptop reaches it by, a
// LAN address, and docker's bridges. If it regresses, the bar names an
// address the laptop cannot reach.
func TestTheAddressGivenIsAVPNsBeforeTheLANsAndNeverAContainersBridge(t *testing.T) {
	for name, want := range map[string]string{
		"wg1": "vpn", "tun0": "vpn", "utun3": "vpn", "tailscale0": "vpn",
		"enp3s0": "lan", "wlp2s0": "lan", "en0": "lan",
		"docker0": "", "br-1a2b": "", "veth12": "", "lxdbr0": "", "virbr0": "",
	} {
		if got := interfaceKind(name); got != want {
			t.Errorf("%s is %q, want %q", name, got, want)
		}
	}
}
