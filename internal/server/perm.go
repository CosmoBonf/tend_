package server

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/auth-com-br/tend/internal/config"
	"github.com/auth-com-br/tend/internal/perm"
	"github.com/auth-com-br/tend/internal/proto"
	"github.com/auth-com-br/tend/internal/session"
	"github.com/auth-com-br/tend/internal/transport"
)

// The permissions manager's methods: what each agent may do without asking,
// read and written on the machine the panes run on, where the agents and
// their files are, for the project of the pane the panel was opened over.
// The project is found here, from the pane's working directory, for the
// same reason. Like the MCP manager's, none of it takes the server's lock
// for long: it is I/O, and the manager serialises its own writes.

// permManager is made on first use, with the extra agents the config names
// read again on each use: the config can change while the server runs.
func (s *Server) permManager() (*perm.Manager, config.Permissions, error) {
	s.permOnce.Do(func() {
		dir, err := transport.StateDir()
		if err != nil {
			s.permErr = err
			return
		}
		s.perm = perm.NewManager(dir, nil)
	})
	if s.permErr != nil {
		return nil, config.Permissions{}, s.permErr
	}
	cfg, err := config.Load()
	if err != nil {
		// A config that does not read is no reason to hide the agents.
		cfg = config.Config{}
	}
	var extras []perm.Extra
	for _, f := range cfg.Permissions.Files {
		extras = append(extras, perm.Extra{Agent: f.Agent, Path: f.Path, Scope: perm.Scope(f.Scope)})
	}
	s.perm.SetExtras(extras)
	return s.perm, cfg.Permissions, nil
}

// permPlace is the project of a pane: its program's working directory, as
// a new pane beside it would start in (followDir), and that folder's
// repository. No pane, or one gone, is no project.
func (s *Server) permPlace(pane uint64) perm.Place {
	if pane == 0 {
		return perm.Place{}
	}
	var spec PaneSpec
	s.followDir(&spec, session.PaneID(pane))
	if spec.Dir == "" {
		if ctx, err := s.PaneContext(session.PaneID(pane)); err == nil {
			spec.Dir = ctx.Dir
		}
	}
	return perm.PlaceOf(spec.Dir)
}

// PermList is every agent's permissions in the project of a pane.
func (s *Server) PermList(p proto.PermListParams) proto.PermListResult {
	m, _, err := s.permManager()
	if err != nil {
		return proto.PermListResult{Agents: []proto.PermAgent{{ID: "tend", Name: "tend", Error: err.Error()}}}
	}
	place := s.permPlace(p.Pane)
	if p.Dir != "" {
		place = perm.PlaceOf(p.Dir)
	}
	snap := m.List(place)
	out := proto.PermListResult{Project: snap.Project, Dir: place.Dir, Agents: []proto.PermAgent{}}
	for _, a := range snap.Agents {
		pa := proto.PermAgent{ID: a.ID, Name: a.Name, Warnings: a.Warnings, Effect: a.Effect, Error: a.Error}
		for _, d := range a.Decisions {
			pa.Decisions = append(pa.Decisions, string(d))
		}
		for _, f := range a.Files {
			pa.Files = append(pa.Files, proto.PermFile{Path: f.Path, Scope: string(f.Scope), Exists: f.Exists, ReadOnly: f.ReadOnly, Note: f.Note, ModesOnly: f.ModesOnly})
		}
		for _, r := range a.Rules {
			pa.Rules = append(pa.Rules, proto.PermRule{File: r.File, Scope: string(r.Scope), Decision: string(r.Decision), Text: r.Text, ReadOnly: r.ReadOnly, Why: r.Why, Elsewhere: r.Elsewhere})
		}
		for _, md := range a.Modes {
			pa.Modes = append(pa.Modes, proto.PermMode{File: md.File, Scope: string(md.Scope), Key: md.Key, Value: md.Value, Choices: md.Choices, Help: md.Help})
		}
		out.Agents = append(out.Agents, pa)
	}
	return out
}

// PermWrite is one write, by method.
func (s *Server) PermWrite(method string, p proto.PermWriteParams) error {
	m, _, err := s.permManager()
	if err != nil {
		return err
	}
	place := s.permPlace(p.Pane)
	d := perm.Decision(p.Decision)
	switch method {
	case proto.MethodPermAdd:
		return m.Add(place, p.Agent, p.File, d, p.Text)
	case proto.MethodPermRemove:
		return m.Remove(place, p.Agent, p.File, d, p.Text)
	case proto.MethodPermSetDecision:
		return m.SetDecision(place, p.Agent, p.File, p.Text, d, perm.Decision(p.ToDecision))
	case proto.MethodPermMove:
		return m.Move(place, p.Agent, p.File, p.To, d, p.Text)
	case proto.MethodPermSetMode:
		return m.SetMode(place, p.Agent, p.File, p.Key, p.Value)
	}
	return fmt.Errorf("%w: %s", proto.ErrUnknownMethod, method)
}

// The editors looked for, terminal ones first. emacs opens in the terminal
// with -nw; the graphical ones are offered only where there is a display.
var (
	terminalEditors = []struct{ name, command string }{
		{"nano", "nano"}, {"micro", "micro"}, {"vim", "vim"}, {"nvim", "nvim"},
		{"hx", "hx"}, {"emacs", "emacs -nw"}, {"kak", "kak"}, {"vi", "vi"},
	}
	guiEditors = []string{"gedit", "gnome-text-editor", "kate", "mousepad", "xed", "pluma", "code", "codium", "zed", "subl"}
)

// hasDisplay is whether a window can be opened from the server.
func hasDisplay() bool {
	return os.Getenv("DISPLAY") != "" || os.Getenv("WAYLAND_DISPLAY") != ""
}

// isGUIEditor is whether a command opens a window: its program is one of
// the graphical editors.
func isGUIEditor(command string) bool {
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return false
	}
	return contains(guiEditors, filepath.Base(fields[0]))
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// defaultEditor is the editor a file opens in: the config's, then $VISUAL,
// then $EDITOR, then nano — not vi, which strands whoever does not know it.
func defaultEditor(cfg config.Permissions) string {
	if e := strings.TrimSpace(cfg.Editor); e != "" {
		return e
	}
	for _, name := range []string{"VISUAL", "EDITOR"} {
		if e := strings.TrimSpace(os.Getenv(name)); e != "" {
			return e
		}
	}
	if _, err := exec.LookPath("nano"); err == nil {
		return "nano"
	}
	return "vi"
}

// PermEditors is the editors this machine has, the default one first.
func (s *Server) PermEditors() proto.PermEditorsResult {
	_, cfg, _ := s.permManager()
	def := defaultEditor(cfg)
	out := proto.PermEditorsResult{Editors: []proto.PermEditor{{Name: def, Command: def, GUI: isGUIEditor(def), Default: true}}}
	seen := map[string]bool{def: true}
	add := func(name, command string, gui bool) {
		if seen[command] {
			return
		}
		if _, err := exec.LookPath(strings.Fields(command)[0]); err != nil {
			return
		}
		seen[command] = true
		out.Editors = append(out.Editors, proto.PermEditor{Name: name, Command: command, GUI: gui})
	}
	for _, e := range terminalEditors {
		add(e.name, e.command, false)
	}
	if hasDisplay() {
		for _, e := range guiEditors {
			add(e, e, true)
		}
	}
	return out
}

// errNotAnAgentFile is a path perm.open was asked for that is none of the
// agents' files: the method opens what the panel lists, not anything.
var errNotAnAgentFile = errors.New("that is not one of the agents' permission files")

// PermOpen opens one of the agents' files in an editor: a terminal editor
// in a pane beside the one the panel was opened over, closed with it; a
// graphical one in a window of its own.
func (s *Server) PermOpen(p proto.PermOpenParams) (proto.PermOpenResult, error) {
	m, cfg, err := s.permManager()
	if err != nil {
		return proto.PermOpenResult{}, err
	}
	place := s.permPlace(p.Pane)
	listed := false
	for _, a := range m.List(place).Agents {
		for _, f := range a.Files {
			if f.Path == p.Path {
				listed = true
			}
		}
	}
	if !listed {
		return proto.PermOpenResult{}, errNotAnAgentFile
	}
	editor := strings.TrimSpace(p.Editor)
	if editor == "" {
		editor = defaultEditor(cfg)
	}
	// A file not there yet is opened all the same, to be written: its
	// folder is made so the editor can save it (.claude in a project).
	if err := os.MkdirAll(filepath.Dir(p.Path), 0o755); err != nil {
		return proto.PermOpenResult{}, err
	}
	// The editor through a shell, so one with flags works; the path as an
	// argument, so one with spaces reaches it as it is.
	command := []string{"/bin/sh", "-c", editor + ` "$1"`, "tend-edit", p.Path}
	if isGUIEditor(editor) {
		if !hasDisplay() {
			return proto.PermOpenResult{}, fmt.Errorf("%s needs a display, and this server has none; pick a terminal editor", editor)
		}
		cmd := exec.Command(command[0], command[1:]...)
		cmd.Dir = filepath.Dir(p.Path)
		detach(cmd)
		if err := cmd.Start(); err != nil {
			return proto.PermOpenResult{}, err
		}
		go func() { _ = cmd.Wait() }()
		return proto.PermOpenResult{Window: true}, nil
	}
	if p.Pane == 0 {
		return proto.PermOpenResult{}, errors.New("no pane to open the editor beside")
	}
	pane, err := s.SplitPane(session.PaneID(p.Pane), session.Rows, PaneSpec{
		Command: command, Dir: filepath.Dir(p.Path),
		Title: filepath.Base(p.Path), Named: true,
		// Gone with the editor, as the scrollback's editor is.
		CloseOnExit: true, NoDetect: true,
	})
	if err != nil {
		return proto.PermOpenResult{}, err
	}
	return proto.PermOpenResult{Pane: uint64(pane)}, nil
}
