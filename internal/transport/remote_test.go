package transport

import (
	"strings"
	"testing"
)

// TestTheFarSideRunsTheTendAnAttachInstalled: over plain ssh the far side
// runs ~/.local/bin/tend when it is there, since an attach puts tend there
// and a non-interactive ssh often has no ~/.local/bin on its PATH; found on
// a real host, where every -ssh command then failed with "tend: command not
// found". A path an attach settled on wins, and a TEND_SSH wrapper gets the
// plain word.
func TestTheFarSideRunsTheTendAnAttachInstalled(t *testing.T) {
	t.Setenv(RemoteCommandEnv, "")
	argv := RemoteArgv("host", "s")
	tend := indexOf(argv, "host") + 1
	if argv[0] != "ssh" || tend == 0 || !strings.Contains(argv[tend], "$HOME/.local/bin/tend") || argv[tend+1] != "bridge" {
		t.Errorf("argv = %q", argv)
	}
	RemoteTend = "'/opt/tend'"
	defer func() { RemoteTend = "" }()
	if argv := RemoteArgv("host", "s"); argv[tend] != "'/opt/tend'" {
		t.Errorf("a settled path should win: %q", argv)
	}
	RemoteTend = ""
	t.Setenv(RemoteCommandEnv, "fake-ssh")
	if argv := RemoteArgv("host", "s"); argv[2] != "tend" {
		t.Errorf("a wrapper should get the plain word: %q", argv)
	}
}

func indexOf(argv []string, want string) int {
	for i, a := range argv {
		if a == want {
			return i
		}
	}
	return -1
}

// TestASessionOverSshIsCompressedAndMarkedInteractive: the ssh carrying a
// session compresses, and marks its packets as typing rather than bulk. If
// it regresses, a laptop on a VPN gets every screen whole — 47 KB/s from a
// working Claude where 0.6 KB/s does — in Wi-Fi's background queue, and
// typing lags (the owner's report). The non-interactive watch keeps them.
func TestASessionOverSshIsCompressedAndMarkedInteractive(t *testing.T) {
	t.Setenv(RemoteCommandEnv, "")
	for _, argv := range [][]string{RemoteArgv("host", "s"), WatchArgv("host", "s")} {
		joined := strings.Join(argv, " ")
		host := indexOf(argv, "host")
		for _, opt := range []string{"Compression=yes", "IPQoS=af21"} {
			if i := indexOf(argv, opt); i < 0 || i > host {
				t.Errorf("%s is not an option before the host: %s", opt, joined)
			}
		}
	}
}
