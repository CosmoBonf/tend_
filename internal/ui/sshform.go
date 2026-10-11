package ui

import (
	"strconv"
	"strings"

	"github.com/auth-com-br/tend/internal/vt"
	"github.com/mattn/go-runewidth"
)

// The SSH connection form: what a saved connection is made of, typed into a
// box over the screen, as the errors panel's connect box takes a server.
// tend's own, after Ásbrú's connection editor.

// The form's fields, in the order tab walks them.
const (
	SSHFieldName = iota
	SSHFieldGroup
	SSHFieldHost
	SSHFieldPort
	SSHFieldUser
	SSHFieldPassword
	SSHFieldCount
)

var sshFieldLabels = [SSHFieldCount]string{"name", "group", "host", "port", "user", "password"}

// SSHFormSave and SSHFormCancel are the form's buttons, as SSHFormAt names
// them.
const (
	SSHFormSave   = "save"
	SSHFormCancel = "cancel"
)

// SSHForm is the form while it is up.
type SSHForm struct {
	// Title heads the box: a new connection, or the one being edited.
	Title  string
	Fields [SSHFieldCount]string
	// Focus is the field typing goes to.
	Focus int
	// Error is why the last save was refused.
	Error string
	// Groups are the groups there are, offered while the group field has
	// the keyboard: a click on one, or ← and →, puts it in the field, so
	// joining a group is not typing its name again exactly.
	Groups []string
}

// SSHFormGeometry is where the form's parts are, for drawing and clicking.
type SSHFormGeometry struct {
	Box     Rect
	Fields  [SSHFieldCount]Rect
	Buttons []IssueButton
	// GroupChips are the groups offered, on the message line, while the
	// group field has the keyboard; ID is the group's index.
	GroupChips []IssueButton
}

const sshFormRows = 13

// SSHFormLayout places the form in the middle of the screen.
func SSHFormLayout(f *SSHForm, cols, rows int) SSHFormGeometry {
	w, h := min(60, cols-4), min(sshFormRows, rows)
	box := Rect{X: max((cols-w)/2, 0), Y: max((rows-h)/2, 0), Cols: max(w, 0), Rows: h}
	g := SSHFormGeometry{Box: box}
	for i := range g.Fields {
		g.Fields[i] = Rect{X: box.X + 12, Y: box.Y + 3 + i, Cols: max(box.Cols-15, 0), Rows: 1}
	}
	bx := box.X + 2
	for _, b := range []IssueButton{{ID: SSHFormSave, Label: "[ Save ]"}, {ID: SSHFormCancel, Label: "[ Cancel ]"}} {
		b.Rect = Rect{X: bx, Y: box.Y + box.Rows - 2, Cols: runewidth.StringWidth(b.Label), Rows: 1}
		bx += b.Cols + 1
		g.Buttons = append(g.Buttons, b)
	}
	if f != nil && f.Focus == SSHFieldGroup && f.Error == "" {
		x, right := box.X+2+len("groups: "), box.X+box.Cols-2
		for i, name := range f.Groups {
			label := "[" + name + "]"
			w := runewidth.StringWidth(label)
			if x+w > right {
				break
			}
			g.GroupChips = append(g.GroupChips, IssueButton{ID: strconv.Itoa(i), Label: label,
				Rect: Rect{X: x, Y: box.Y + 10, Cols: w, Rows: 1}})
			x += w + 1
		}
	}
	return g
}

// SSHFormAt is what a click at a point is on: a button's ID, "field:N",
// "group:N" for an offered group, or "inside" for the rest of the box. ok
// is false outside it.
func SSHFormAt(f *SSHForm, cols, rows, x, y int) (string, bool) {
	g := SSHFormLayout(f, cols, rows)
	in := func(r Rect) bool { return y >= r.Y && y < r.Y+r.Rows && x >= r.X && x < r.X+r.Cols }
	if OnCloseMark(g.Box, x, y) {
		return SSHFormCancel, true
	}
	for _, b := range g.Buttons {
		if in(b.Rect) {
			return b.ID, true
		}
	}
	for _, b := range g.GroupChips {
		if in(b.Rect) {
			return "group:" + b.ID, true
		}
	}
	for i, r := range g.Fields {
		// The label is part of the field: clicking "host" means the host.
		label := Rect{X: g.Box.X + 2, Y: r.Y, Cols: r.X + r.Cols - g.Box.X - 2, Rows: 1}
		if in(label) {
			return "field:" + strconv.Itoa(i), true
		}
	}
	if in(g.Box) {
		return "inside", true
	}
	return "", false
}

// drawSSHForm draws the form: each field with its label, the password
// masked, what went wrong or the keys, and the buttons.
func drawSSHForm(dst *vt.Grid, f *SSHForm, theme Theme) {
	g := SSHFormLayout(f, dst.Cols(), dst.Rows())
	box := g.Box
	if box.Cols < 20 || box.Rows < sshFormRows {
		return
	}
	for y := box.Y; y < box.Y+box.Rows; y++ {
		for x := box.X; x < box.X+box.Cols; x++ {
			setCell(dst, x, y, ' ', theme.Notes)
		}
	}
	drawBox(dst, box, theme.NotesAccent)
	drawCloseMark(dst, box, withBold(theme.NotesAccent))
	right := box.X + box.Cols - 1
	writeString(dst, box.X+2, box.Y, " "+truncate(f.Title, box.Cols-12)+" ", withBold(theme.NotesAccent), right)
	writeString(dst, box.X+2, box.Y+1, truncate("password only for a machine with no key; empty port is 22", box.Cols-4), theme.NotesSub, right)
	for i, r := range g.Fields {
		on := i == f.Focus
		writeString(dst, box.X+2, r.Y, sshFieldLabels[i], theme.NotesSub, right)
		style := theme.NotesButton
		if !on {
			style = theme.NotesFence
		}
		for x := r.X; x < r.X+r.Cols; x++ {
			setCell(dst, x, r.Y, ' ', style)
		}
		value := f.Fields[i]
		if i == SSHFieldPassword {
			value = strings.Repeat("•", min(len([]rune(value)), 48))
		}
		x := writeString(dst, r.X+1, r.Y, truncateLeft(value, r.Cols-3), style, r.X+r.Cols)
		if on {
			setCell(dst, x, r.Y, '▏', style)
		}
	}
	msg, style := "tab next field · enter save · esc cancel", theme.NotesSub
	switch {
	case f.Error != "":
		msg, style = f.Error, withBold(theme.Notes)
	case f.Focus == SSHFieldGroup && len(f.Groups) == 0:
		msg = "type a name to make a group"
	case f.Focus == SSHFieldGroup:
		msg = "groups: "
	}
	writeString(dst, box.X+2, box.Y+10, truncate(msg, box.Cols-4), style, right)
	for _, b := range g.GroupChips {
		chip := theme.NotesAccent
		if i, _ := strconv.Atoi(b.ID); f.Groups[i] == f.Fields[SSHFieldGroup] {
			chip = withBold(theme.NotesButton)
		}
		writeString(dst, b.X, b.Y, b.Label, chip, right)
	}
	for _, b := range g.Buttons {
		writeString(dst, b.X, b.Y, b.Label, theme.NotesAccent, right)
	}
}
