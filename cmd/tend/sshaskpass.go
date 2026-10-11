package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/auth-com-br/tend/internal/sshconn"
)

// runSSHAskpass answers ssh for a saved connection: ssh, started from the
// sidebar's ssh list with SSH_ASKPASS pointing here, runs tend with its
// question and reads the answer from its output. The password is read from
// the connections file at that moment, so it is never in the command line,
// the pane's environment or the session that is written down.
//
// A question that is not for the password is refused (exit 1), and ssh
// gives up on that way in. It returns the exit status.
func runSSHAskpass(id string, args []string) int {
	path, err := sshconn.Path()
	if err != nil {
		fmt.Fprintln(os.Stderr, "tend:", err)
		return 1
	}
	store, err := sshconn.Load(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "tend:", err)
		return 1
	}
	c, ok := store.Find(id)
	if !ok {
		fmt.Fprintln(os.Stderr, "tend: that ssh connection is no longer saved")
		return 1
	}
	answer, ok := sshconn.Answer(c, strings.Join(args, " "))
	if !ok {
		return 1
	}
	fmt.Println(answer)
	return 0
}
