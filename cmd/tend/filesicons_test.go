//go:build unix

package main

import (
	"os/exec"
	"strings"
	"testing"
)

// TestTheFilesPanelTakesTheClientsIconsOnAMachineWithoutSettings: the
// command that starts the panel carries the client's icons to it, and the
// panel uses them when its own machine sets none, while a machine that does
// set them keeps its own. If it regresses, the files panel of a remote
// session draws no icons whatever the client chose — the remote machine has
// no settings file.
func TestTheFilesPanelTakesTheClientsIconsOnAMachineWithoutSettings(t *testing.T) {
	cmd := filesCommandFor("nerd")
	// The same shell line, with the program swapped for one that says what
	// it was given.
	line := strings.Replace(cmd[2], `"${TEND_BIN_PATH:-tend}" files`, `env`, 1)
	out, err := exec.Command(cmd[0], cmd[1], line).Output()
	if err != nil || !strings.Contains(string(out), filesIconsEnv+"=nerd\n") {
		t.Errorf("the panel was given %q (%v)", out, err)
	}
	if got := filesCommandFor(`x"; rm -rf ~; "`); got[2] != filesCommand[2] {
		t.Errorf("a value that is no icon setting went into the command: %q", got[2])
	}
	if got := panelIcons("", "nerd"); got != "nerd" {
		t.Errorf("a machine with no setting draws %q", got)
	}
	if got := panelIcons("emoji", "nerd"); got != "emoji" {
		t.Errorf("a machine's own setting was overridden: %q", got)
	}
}
