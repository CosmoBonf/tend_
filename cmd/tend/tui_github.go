package main

import (
	"github.com/auth-com-br/tend/internal/proto"
)

// The GitHub account, tend's own and the owner's: everything tend does on
// GitHub — the issues, the pull requests, the files panel's CI — goes
// through the GitHub CLI and its login, so connecting GitHub is gh's own
// login, which opens the browser to sign in. "GitHub account..." on the
// sidebar's menu runs it in a tab, for the owner to see what it asks:
// signed in, it says as whom and offers another account or a switch
// between them (gh keeps several); not, it signs in. tend keeps no token.

// githubScript is what the tab runs, on the machine the session is on.
//
// gh's own login shows a code, copies it, and then waits for a second
// enter before it opens the browser: the owner stopped at the first and
// saw no browser. The tab says so before gh starts, with the address to
// ctrl+click should the browser still not come.
const githubScript = `say_login() {
  echo
  echo "  gh shows a code (already copied) and asks you to press Enter:"
  echo "  press Enter AGAIN there, and the browser opens on GitHub."
  echo "  Paste the code there and confirm. No browser? ctrl+click:"
  echo "  https://github.com/login/device"
  echo
}
if ! command -v gh >/dev/null 2>&1; then
  echo "tend uses the GitHub CLI for GitHub. Install it: https://cli.github.com"
elif gh auth status --hostname github.com >/dev/null 2>&1; then
  gh auth status --hostname github.com
  echo
  printf 'enter: add another account (opens the browser) · s: switch account · q: leave  '
  read -r answer
  case "$answer" in
  q*) ;;
  s*) gh auth switch --hostname github.com ;;
  *) say_login; gh auth login --web --clipboard --hostname github.com --git-protocol https ;;
  esac
else
  say_login
  gh auth login --web --clipboard --hostname github.com --git-protocol https
fi
echo
exec "${SHELL:-/bin/sh}"`

// openGitHubAccount runs the GitHub login in a tab of its own.
func (t *tui) openGitHubAccount() error {
	t.mu.Lock()
	ws := t.workspace
	t.mu.Unlock()
	if ws == 0 {
		return nil
	}
	tab, _, err := t.client.NewTab(ws, "github", proto.PaneSpec{
		Command: []string{"/bin/sh", "-c", githubScript},
		Title:   "github",
	})
	if err != nil {
		t.setMessage("could not open the GitHub login: "+refusalText(err), true)
		return nil
	}
	t.mu.Lock()
	t.rememberFocusLocked()
	t.tab, t.focus, t.zoom = tab, 0, false
	t.mu.Unlock()
	return t.refresh()
}
