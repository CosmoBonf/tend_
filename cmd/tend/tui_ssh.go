package main

import (
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/auth-com-br/tend/internal/proto"
	"github.com/auth-com-br/tend/internal/sshconn"
	"github.com/auth-com-br/tend/internal/ui"
)

// The SSH list, tend's own, after Ásbrú Connection Manager: the button
// beside the sidebar's "menu" turns the top list from the spaces to the
// saved SSH connections and back. A click on a connection picks it, and a
// second click on the one picked opens it, logged in, in a space of its own —
// or goes to it, when it is open already; a right-click edits, duplicates,
// copies or deletes it; "new" adds one.
//
// The list shows each connection by its name alone. The user and host are
// the owner's to keep off a screen that is shared or recorded, so they stay
// in the form and the ssh command, and out of the list and the tab.
//
// The connections are kept in a file of their own beside the settings
// (internal/sshconn), read by this client. Which list is shown, which groups
// are folded, and which space each connection is open in are this person's
// and are never sent to the server.

// sshGroup is the space group the connections' spaces are kept in. The
// spaces list leaves it out (spaceRowsFrom): those spaces are reached from
// the ssh list, and the spaces list is the work's.
const sshGroup = "ssh"

// sshState is the SSH list's client state.
type sshState struct {
	// list is the top list showing the connections rather than the spaces.
	list   bool
	folded map[string]bool
	store  sshconn.Store
	loaded bool
	// form is the connection form while it is up, and editing the
	// connection it edits, empty for a new one.
	form    *ui.SSHForm
	editing string
	// open is where each connection opened from here is: its space and the
	// pane ssh runs in, whose end closes the space.
	open map[string]sshOpen
	// picked is the connection the last click picked. Only a click on the
	// connection already picked opens it: the owner asked that a single
	// click, easily made by mistake, log in nowhere.
	picked string
	// drag is the connection pressed on, until the button comes up; moved
	// is whether the pointer left it, which makes the press a drag rather
	// than a click. dropOn is the row it would be dropped on ("group:name"
	// or "conn:id"), and dropGroup the group that drop files it under.
	drag      string
	moved     bool
	dropOn    string
	dropGroup string
}

type sshOpen struct{ workspace, pane uint64 }

// sshLoadLocked reads the connections the first time they are needed.
func (t *tui) sshLoadLocked() {
	if t.ssh.loaded {
		return
	}
	t.ssh.loaded = true
	path, err := sshconn.Path()
	if err == nil {
		t.ssh.store, err = sshconn.Load(path)
	}
	if err != nil {
		t.message, t.alert = "ssh connections: "+err.Error(), true
	}
}

// sshSaveLocked writes the connections down, saying so when it cannot.
func (t *tui) sshSaveLocked() bool {
	path, err := sshconn.Path()
	if err == nil {
		err = sshconn.Save(path, t.ssh.store)
	}
	if err != nil {
		t.message, t.alert = "ssh connections not saved: "+err.Error(), true
		return false
	}
	return true
}

// sshOpenLocked is where a connection is open, if it still is: a space
// closed by other means is forgotten here as well.
//
// A client started after the connection was opened has no record of it, so
// the session is asked: a space in the ssh group named after the connection
// is it, and its first pane is ssh's. Without that, reopening tend lost the
// ● and a second click opened the connection twice.
func (t *tui) sshOpenLocked(id string) (sshOpen, bool) {
	if o, ok := t.ssh.open[id]; ok {
		for _, w := range t.snap.Workspaces {
			if w.ID == o.workspace {
				return o, true
			}
		}
		delete(t.ssh.open, id)
	}
	c, ok := t.ssh.store.Find(id)
	if !ok {
		return sshOpen{}, false
	}
	for _, w := range t.snap.Workspaces {
		if w.Group != sshGroup || w.Name != c.Name || len(w.Tabs) == 0 || len(w.Tabs[0].Panes) == 0 {
			continue
		}
		o := sshOpen{workspace: w.ID, pane: w.Tabs[0].Panes[0]}
		if t.ssh.open == nil {
			t.ssh.open = make(map[string]sshOpen)
		}
		t.ssh.open[id] = o
		return o, true
	}
	return sshOpen{}, false
}

// listSwitch is the button beside "menu": where it takes the list.
func (t *tui) listSwitchLocked() string {
	if t.ssh.list {
		return "⇄ dev"
	}
	return "⇄ ssh"
}

// sshSectionLocked is the top list as the SSH connections: those in no
// group, then each group with its own under it, and the footer's "new".
func (t *tui) sshSectionLocked() ui.SidebarSection {
	t.sshLoadLocked()
	rows := []ui.SidebarRow{{Kind: ui.SidebarHeading, Label: "ssh"}}
	row := func(c sshconn.Connection, depth int) ui.SidebarRow {
		o, open := t.sshOpenLocked(c.ID)
		return ui.SidebarRow{
			Kind:     ui.SidebarSSH,
			Label:    c.Name,
			SSH:      c.ID,
			Group:    c.Group,
			Action:   ui.ActionSSHConnect,
			Running:  open,
			Active:   open && o.workspace == t.workspace,
			Depth:    depth,
			Selected: t.ssh.picked == c.ID,
			DropHere: t.ssh.dropOn == "conn:"+c.ID,
		}
	}
	for _, c := range t.ssh.store.Connections {
		if c.Group == "" {
			rows = append(rows, row(c, 0))
		}
	}
	for _, g := range t.ssh.store.Groups() {
		folded := t.ssh.folded[g]
		rows = append(rows, ui.SidebarRow{
			Kind: ui.SidebarSSHGroup, Label: g, Group: g, Folded: folded,
			Action:   ui.ActionSSHToggleGroup,
			DropHere: t.ssh.dropOn == "group:"+g,
		})
		if folded {
			continue
		}
		members := 0
		for _, c := range t.ssh.store.Connections {
			if c.Group == g {
				rows = append(rows, row(c, 1))
				members++
			}
		}
		if members == 0 {
			// Said, so a group just made does not look like nothing happened.
			rows = append(rows, ui.SidebarRow{Kind: ui.SidebarAction, Label: "   empty: right-click " + g})
		}
	}
	if len(t.ssh.store.Connections) == 0 {
		rows = append(rows, ui.SidebarRow{Kind: ui.SidebarAction, Label: "no connections: new adds one", Action: ui.ActionSSHNew})
	}
	return ui.SidebarSection{
		Rows: rows,
		Footer: []ui.SidebarRow{{
			Kind:           ui.SidebarAction,
			Label:          "new",
			Action:         ui.ActionSSHNew,
			Trailing:       "menu",
			TrailingAction: ui.ActionOpenMenu,
			Switch:         t.listSwitchLocked(),
			SwitchAction:   ui.ActionSwitchList,
		}},
	}
}

// switchList turns the top list between the spaces and the connections.
func (t *tui) switchList() {
	t.mu.Lock()
	t.ssh.list = !t.ssh.list
	t.dirty = true
	t.mu.Unlock()
	t.wakeUp()
}

// pressSSH takes hold of a connection: letting go where it was pressed
// opens it, and dragging it onto a group files it there (dropSSH). The
// click waits for the release because a press that opened the connection
// at once would leave nothing to drag.
func (t *tui) pressSSH(id string) {
	t.mu.Lock()
	t.ssh.drag, t.ssh.moved, t.ssh.dropOn, t.ssh.dropGroup = id, false, "", ""
	t.mu.Unlock()
}

// dragSSH follows a connection dragged down the list, marking the row it
// would be dropped on: a group's heading files it in that group, another
// connection in that one's group — none, for one in none. It reports
// whether a connection is being dragged.
func (t *tui) dragSSH(ev ui.MouseEvent) bool {
	t.mu.Lock()
	if t.ssh.drag == "" {
		t.mu.Unlock()
		return false
	}
	frame := t.buildFrame()
	c, _ := t.ssh.store.Find(t.ssh.drag)
	on, group := "", ""
	row, ok := ui.SidebarRowAt(frame, 1, ev.Y, t.rows)
	if ok && row.Kind == ui.SidebarSSH && row.SSH == c.ID && !t.ssh.moved {
		// A hand that shakes over the connection, its name or the line
		// under it, is still clicking; leaving it is what makes a drag.
		t.mu.Unlock()
		return true
	}
	t.ssh.moved = true
	if ok {
		switch {
		case row.Kind == ui.SidebarSSHGroup && row.Group != c.Group:
			on, group = "group:"+row.Group, row.Group
		case row.Kind == ui.SidebarSSH && row.SSH != c.ID && row.Group != c.Group:
			on, group = "conn:"+row.SSH, row.Group
		}
	}
	if on != t.ssh.dropOn {
		t.ssh.dropOn, t.ssh.dropGroup = on, group
		t.dirty = true
	}
	t.mu.Unlock()
	t.wakeUp()
	return true
}

// dropSSH ends a press on a connection: a click picks it, a click on the
// one picked opens it, a drag onto a group files it there, and a drag onto
// nothing does nothing. It reports whether there was one.
func (t *tui) dropSSH() (bool, error) {
	t.mu.Lock()
	id, moved, on, group := t.ssh.drag, t.ssh.moved, t.ssh.dropOn, t.ssh.dropGroup
	t.ssh.drag, t.ssh.moved, t.ssh.dropOn, t.ssh.dropGroup = "", false, "", ""
	t.dirty = true
	second := !moved && id != "" && t.ssh.picked == id
	if !moved && id != "" {
		t.ssh.picked = id
	}
	if second {
		// Picked again after it opens: a click on it once its space closes
		// picks it, rather than reconnecting at once.
		t.ssh.picked = ""
	}
	t.mu.Unlock()
	switch {
	case id == "":
		return false, nil
	case second:
		return true, t.connectSSH(id)
	case !moved:
		t.wakeUp()
		return true, nil
	case on != "":
		t.moveSSH(id, group)
	}
	t.wakeUp()
	return true, nil
}

// toggleSSHGroup folds or unfolds a group of connections.
func (t *tui) toggleSSHGroup(group string) {
	t.mu.Lock()
	if t.ssh.folded == nil {
		t.ssh.folded = make(map[string]bool)
	}
	t.ssh.folded[group] = !t.ssh.folded[group]
	t.dirty = true
	t.mu.Unlock()
	t.wakeUp()
}

// connectSSH opens a connection in a space of its own, named after it, or
// goes to the space it is open in.
//
// A password is given to ssh by tend itself (sshaskpass.go), which reads it
// from the connections file on the machine the pane runs on. Over --remote
// that is not this one, so a connection with a password is not opened
// there: it would ask for a password nobody can type.
func (t *tui) connectSSH(id string) error {
	t.mu.Lock()
	t.sshLoadLocked()
	c, ok := t.ssh.store.Find(id)
	o, open := t.sshOpenLocked(id)
	remote := t.host != ""
	t.mu.Unlock()
	if !ok {
		return nil
	}
	if open {
		return t.showWorkspace(o.workspace)
	}
	if c.Password != "" && remote {
		t.setMessage(c.Name+": a saved password works in a session on this machine only", true)
		return nil
	}

	ws, err := t.client.NewWorkspaceIn(c.Name, "")
	if err != nil {
		return err
	}
	if err := t.client.GroupWorkspace(ws, sshGroup); err != nil {
		_ = t.client.CloseWorkspace(ws)
		return err
	}
	tab, pane, err := t.client.NewTab(ws, c.Name, proto.PaneSpec{
		Command: c.PaneCommand(),
		Title:   c.Name,
	})
	if err != nil {
		_ = t.client.CloseWorkspace(ws)
		t.setMessage(c.Name+" did not open: "+refusalText(err), true)
		return nil
	}
	t.mu.Lock()
	if t.ssh.open == nil {
		t.ssh.open = make(map[string]sshOpen)
	}
	t.ssh.open[id] = sshOpen{workspace: ws, pane: pane}
	t.rememberFocusLocked()
	t.workspace, t.tab, t.focus, t.zoom = ws, tab, 0, false
	t.mu.Unlock()
	return t.refresh()
}

// sshPaneExited closes a connection's space when its pane ends, which is
// the user answering q to "connect again": the space was the connection,
// and an empty one left behind would be clutter to close by hand. It
// reports whether the pane was one.
func (t *tui) sshPaneExited(pane uint64) bool {
	t.mu.Lock()
	ws := uint64(0)
	for id, o := range t.ssh.open {
		if o.pane == pane {
			ws = o.workspace
			delete(t.ssh.open, id)
		}
	}
	if ws == 0 {
		// Opened before this client started: a space of the ssh group whose
		// first pane this is was opened as a connection.
		for _, w := range t.snap.Workspaces {
			if w.Group == sshGroup && len(w.Tabs) > 0 && len(w.Tabs[0].Panes) > 0 && w.Tabs[0].Panes[0] == pane {
				ws = w.ID
			}
		}
	}
	t.mu.Unlock()
	if ws == 0 {
		return false
	}
	if err := t.client.CloseWorkspace(ws); err != nil {
		t.setMessage(err.Error(), true)
	}
	return true
}

// --- the form --------------------------------------------------------------

func (t *tui) sshFormUp() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.ssh.form != nil
}

// openSSHForm puts the form up: for a new connection in a group, for the
// connection with an id, or for a copy of it.
func (t *tui) openSSHForm(id string, duplicate bool, group string) {
	t.openSSHFormAt(id, duplicate, group, -1)
}

// openSSHFormAt is openSSHForm with the keyboard on a field of choice, or
// on the usual one for -1.
func (t *tui) openSSHFormAt(id string, duplicate bool, group string, focus int) {
	t.mu.Lock()
	t.sshLoadLocked()
	f := &ui.SSHForm{Title: "new ssh connection", Focus: ui.SSHFieldHost, Groups: t.ssh.store.Groups()}
	f.Fields[ui.SSHFieldGroup] = group
	t.ssh.editing = ""
	if c, ok := t.ssh.store.Find(id); ok {
		f.Fields = [ui.SSHFieldCount]string{c.Name, c.Group, c.Host, "", c.User, c.Password}
		if c.Port != 0 {
			f.Fields[ui.SSHFieldPort] = strconv.Itoa(c.Port)
		}
		f.Focus = ui.SSHFieldName
		if duplicate {
			f.Title = "copy of " + c.Name
			f.Fields[ui.SSHFieldName] = c.Name + " (copy)"
		} else {
			f.Title = "edit " + c.Name
			t.ssh.editing = c.ID
		}
	}
	if focus >= 0 {
		f.Focus = focus
	}
	t.ssh.form = f
	t.dirty = true
	t.mu.Unlock()
	t.wakeUp()
}

func (t *tui) closeSSHForm() {
	t.mu.Lock()
	t.ssh.form = nil
	t.dirty = true
	t.mu.Unlock()
	t.wakeUp()
}

// saveSSHForm keeps what the form says, or says why it cannot.
func (t *tui) saveSSHForm() {
	t.mu.Lock()
	defer func() {
		t.dirty = true
		t.mu.Unlock()
		t.wakeUp()
	}()
	f := t.ssh.form
	if f == nil {
		return
	}
	port, err := sshconn.ParsePort(f.Fields[ui.SSHFieldPort])
	if err != nil {
		f.Error, f.Focus = err.Error(), ui.SSHFieldPort
		return
	}
	c, err := sshconn.Normalize(sshconn.Connection{
		ID:       t.ssh.editing,
		Name:     f.Fields[ui.SSHFieldName],
		Group:    f.Fields[ui.SSHFieldGroup],
		Host:     f.Fields[ui.SSHFieldHost],
		Port:     port,
		User:     f.Fields[ui.SSHFieldUser],
		Password: f.Fields[ui.SSHFieldPassword],
	})
	if err != nil {
		f.Error = err.Error()
		return
	}
	before := t.ssh.store.Clone()
	c = t.ssh.store.Put(c)
	if !t.sshSaveLocked() {
		t.ssh.store = before
		f.Error = t.message
		return
	}
	t.ssh.form = nil
	if c.Group != "" && t.ssh.folded[c.Group] {
		// Saved into a folded group, it would seem to have vanished.
		t.ssh.folded[c.Group] = false
	}
	t.message, t.alert = "saved "+c.Name, false
}

// moveSSH files a connection under another group, or under none.
func (t *tui) moveSSH(id, group string) {
	t.mu.Lock()
	if c, ok := t.ssh.store.Find(id); ok && c.Group != group {
		before := t.ssh.store.Clone()
		c.Group = group
		t.ssh.store.Put(c)
		if t.sshSaveLocked() {
			if group != "" && t.ssh.folded[group] {
				t.ssh.folded[group] = false // or it would seem to vanish
			}
			where := "out of its group"
			if group != "" {
				where = "to " + group
			}
			t.message, t.alert = "moved "+c.Name+" "+where, false
		} else {
			t.ssh.store = before
		}
	}
	t.dirty = true
	t.mu.Unlock()
	t.wakeUp()
}

// sshAddGroup makes a group, empty, for connections to be put in.
func (t *tui) sshAddGroup(name string) {
	t.sshChangeGroups(func(s *sshconn.Store) (string, error) {
		return "made group " + name + ": right-click it for a connection in it", s.AddGroup(name)
	})
}

// sshRenameGroup renames a group, its connections and its folding with it.
func (t *tui) sshRenameGroup(from, to string) {
	t.sshChangeGroups(func(s *sshconn.Store) (string, error) {
		if err := s.RenameGroup(from, to); err != nil {
			return "", err
		}
		if t.ssh.folded[from] {
			delete(t.ssh.folded, from)
			t.ssh.folded[to] = true
		}
		return "renamed " + from + " to " + to, nil
	})
}

// sshDeleteGroup forgets a group; its connections stay, in none.
func (t *tui) sshDeleteGroup(name string) {
	t.sshChangeGroups(func(s *sshconn.Store) (string, error) {
		s.RemoveGroup(name)
		delete(t.ssh.folded, name)
		return "deleted group " + name + "; its connections are kept", nil
	})
}

// sshChangeGroups makes a change to the groups and saves it, or puts it back
// and says why not.
func (t *tui) sshChangeGroups(change func(*sshconn.Store) (string, error)) {
	t.mu.Lock()
	t.sshLoadLocked()
	before := t.ssh.store.Clone()
	said, err := change(&t.ssh.store)
	switch {
	case err != nil:
		t.ssh.store = before
		t.message, t.alert = err.Error(), true
	case !t.sshSaveLocked():
		t.ssh.store = before
	default:
		t.message, t.alert = said, false
	}
	t.dirty = true
	t.mu.Unlock()
	t.wakeUp()
}

// deleteSSH forgets a connection and its password. A space it is open in
// stays: closing what somebody is working in is not what deleting the
// bookmark asked for.
func (t *tui) deleteSSH(id string) {
	t.mu.Lock()
	c, ok := t.ssh.store.Find(id)
	if ok {
		before := t.ssh.store.Clone()
		t.ssh.store.Remove(id)
		if t.sshSaveLocked() {
			t.message, t.alert = "deleted "+c.Name, false
		} else {
			t.ssh.store = before
		}
	}
	t.dirty = true
	t.mu.Unlock()
	t.wakeUp()
}

// sshFormInput is every key and click while the form is up: typing goes to
// the field, tab and the arrows move between them, enter saves, esc gives
// up; a click picks a field or a button, and one outside the box cancels.
func (t *tui) sshFormInput(data []byte) error {
	forward, _, mice := t.keys.FeedAll(data)
	for _, ev := range mice {
		if ev.Kind != ui.MousePress || ev.Button != 0 {
			continue
		}
		t.mu.Lock()
		cols, rows := t.cols, t.rows
		t.mu.Unlock()
		t.mu.Lock()
		form := t.ssh.form
		var at string
		var inside bool
		if form != nil {
			at, inside = ui.SSHFormAt(form, cols, rows, ev.X, ev.Y)
		}
		t.mu.Unlock()
		switch {
		case !inside, at == ui.SSHFormCancel:
			t.closeSSHForm()
			return nil
		case at == ui.SSHFormSave:
			t.saveSSHForm()
		case strings.HasPrefix(at, "group:"):
			n, _ := strconv.Atoi(strings.TrimPrefix(at, "group:"))
			t.mu.Lock()
			if f := t.ssh.form; f != nil && n < len(f.Groups) {
				f.Fields[ui.SSHFieldGroup] = f.Groups[n]
				t.dirty = true
			}
			t.mu.Unlock()
			t.wakeUp()
		case strings.HasPrefix(at, "field:"):
			n, _ := strconv.Atoi(strings.TrimPrefix(at, "field:"))
			t.mu.Lock()
			if t.ssh.form != nil {
				t.ssh.form.Focus = n
				t.dirty = true
			}
			t.mu.Unlock()
			t.wakeUp()
		}
	}
	for _, key := range splitKeys(forward) {
		if !t.sshFormKey(key) {
			return nil
		}
	}
	return nil
}

// sshFormKey is one key in the form. It reports whether the form is still
// up for the next.
func (t *tui) sshFormKey(key string) bool {
	t.mu.Lock()
	f := t.ssh.form
	if f == nil {
		t.mu.Unlock()
		return false
	}
	field := &f.Fields[f.Focus]
	save := false
	switch key {
	case "\x1b":
		t.ssh.form = nil
	case "\t", "\x1b[B":
		f.Focus = (f.Focus + 1) % ui.SSHFieldCount
	case "\x1b[Z", "\x1b[A":
		f.Focus = (f.Focus + ui.SSHFieldCount - 1) % ui.SSHFieldCount
	case "\x1b[C", "\x1b[D":
		// On the group field, the arrows walk the groups there are; the
		// field's text is the one shown, so where it is now is found by name.
		if f.Focus == ui.SSHFieldGroup && len(f.Groups) > 0 {
			n := len(f.Groups) + 1 // the groups, and none
			at := n - 1
			for i, g := range f.Groups {
				if g == f.Fields[ui.SSHFieldGroup] {
					at = i
				}
			}
			if key == "\x1b[C" {
				at = (at + 1) % n
			} else {
				at = (at + n - 1) % n
			}
			f.Fields[ui.SSHFieldGroup] = ""
			if at < len(f.Groups) {
				f.Fields[ui.SSHFieldGroup] = f.Groups[at]
			}
		}
	case "\r", "\n":
		save = true
	case "\x7f", "\x08":
		_, size := utf8.DecodeLastRuneInString(*field)
		*field = (*field)[:len(*field)-size]
	case "\x15":
		*field = ""
	default:
		if len(key) == 1 && key[0] >= 0x20 && key[0] != 0x7f {
			*field += key
		}
	}
	f.Error = ""
	up := t.ssh.form != nil
	t.dirty = true
	t.mu.Unlock()
	t.wakeUp()
	if save {
		t.saveSSHForm()
		return t.sshFormUp()
	}
	return up
}
