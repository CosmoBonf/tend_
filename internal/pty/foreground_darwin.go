//go:build darwin

package pty

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/unix"
)

// Foreground returns the name of the program currently in charge of the
// terminal, as on Linux (see foreground_linux.go for why it is asked of the
// terminal rather than taken from the pane's command). macOS has no /proc:
// the process table is read through sysctl, which is a system call and
// needs no cgo.
//
// Without this every pane on a Mac was its shell to detection, and an agent
// typed at a prompt — which is how nearly everybody starts one — never
// reached the sidebar.
func (p *Pty) Foreground() string {
	if p.f == nil {
		return ""
	}
	pgrp, err := foregroundGroup(p.f)
	if err != nil || pgrp <= 0 {
		return ""
	}
	name := processName(pgrp)
	if interpreters[name] {
		if script := scriptName(processArgs(pgrp)); script != "" {
			return script
		}
	}
	return name
}

// interpreters are the programs a script runs under. Linux names a process
// after the script it executed; macOS after the interpreter, so an agent
// shipped as a script — codex is a node one — is "node" here, and would
// never be recognised. For these the script's own name is taken from the
// arguments, which is the name Linux would have given.
var interpreters = map[string]bool{
	"node": true, "bun": true, "deno": true,
	"python": true, "python3": true, "ruby": true, "perl": true,
	"sh": true, "bash": true, "zsh": true, "dash": true,
}

// scriptName is the base name of the script an interpreter was given: its
// first argument that is not an option. An interactive shell has none, and
// keeps its own name.
func scriptName(argv []string) string {
	if len(argv) < 2 {
		return ""
	}
	for _, arg := range argv[1:] {
		if arg == "-c" {
			return "" // a command line, not a script
		}
		if strings.HasPrefix(arg, "-") {
			continue
		}
		return filepath.Base(arg)
	}
	return ""
}

// foregroundGroup asks the terminal which process group has it. The
// descriptor is borrowed through SyscallConn rather than taken with Fd(), for
// the reason foreground_linux.go gives: Fd() would turn the reads another
// goroutine is doing on it into blocking ones.
func foregroundGroup(f *os.File) (int, error) {
	raw, err := f.SyscallConn()
	if err != nil {
		return 0, err
	}
	var pgrp int
	var ioctlErr error
	if err := raw.Control(func(fd uintptr) {
		pgrp, ioctlErr = unix.IoctlGetInt(int(fd), unix.TIOCGPGRP)
	}); err != nil {
		return 0, err
	}
	return pgrp, ioctlErr
}

// processName is a process's command name, the kernel's p_comm: the
// executable's name cut at 16 bytes, as /proc's comm is cut at 15.
func processName(pid int) string {
	kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil || kp.Proc.P_pid != int32(pid) {
		return ""
	}
	return cString(kp.Proc.P_comm[:])
}

// Process is one process in the terminal's foreground group.
type Process struct {
	Pid     int      `json:"pid"`
	Name    string   `json:"name"`
	Command []string `json:"argv,omitempty"`
}

// ProcessInfo is what the terminal knows about who is running in it: the shell
// it was started with, the process group in charge of it now, and the
// processes in that group. herdr reports the same (`pane.process_info`).
type ProcessInfo struct {
	ShellPid        int       `json:"shell_pid"`
	ForegroundGroup int       `json:"foreground_process_group_id,omitempty"`
	TTY             string    `json:"tty,omitempty"`
	Foreground      []Process `json:"foreground_processes,omitempty"`
}

// maxForegroundProcesses bounds the list, as on Linux: a group of thousands
// is a build system mid-flight, and the first few are what anybody looks at.
const maxForegroundProcesses = 64

// Processes reads the terminal's foreground group from the kernel's process
// table.
func (p *Pty) Processes() ProcessInfo {
	info := ProcessInfo{ShellPid: p.pid}
	if p.f == nil {
		return info
	}
	if pgrp, err := foregroundGroup(p.f); err == nil && pgrp > 0 {
		info.ForegroundGroup = pgrp
	}
	if name, err := ttyName(p.f); err == nil {
		info.TTY = name
	}
	if info.ForegroundGroup == 0 {
		return info
	}
	procs, err := unix.SysctlKinfoProcSlice("kern.proc.pgrp", info.ForegroundGroup)
	if err != nil {
		return info
	}
	for _, kp := range procs {
		pid := int(kp.Proc.P_pid)
		info.Foreground = append(info.Foreground, Process{
			Pid:     pid,
			Name:    cString(kp.Proc.P_comm[:]),
			Command: processArgs(pid),
		})
		if len(info.Foreground) >= maxForegroundProcesses {
			break
		}
	}
	return info
}

// ttyName is the terminal's device path, which on macOS is the name of the
// slave side the pane's processes have open.
func ttyName(f *os.File) (string, error) {
	raw, err := f.SyscallConn()
	if err != nil {
		return "", err
	}
	var name string
	var nameErr error
	if err := raw.Control(func(fd uintptr) {
		name, nameErr = ptsname(int(fd))
	}); err != nil {
		return "", err
	}
	return name, nameErr
}

// ptsname is the slave device of a pty master, by TIOCPTYGNAME, the ioctl
// libc's ptsname uses on macOS.
func ptsname(fd int) (string, error) {
	var buf [128]byte
	_, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), uintptr(unix.TIOCPTYGNAME), uintptr(unsafe.Pointer(&buf[0])))
	if errno != 0 {
		return "", errno
	}
	return cString(buf[:]), nil
}

// processArgs is a process's argv, from kern.procargs2: the argument count
// as a 32-bit integer, the executable's path, padding NULs, then the
// arguments, each NUL-terminated, then the environment, which is left out.
// A process of another user's is not readable and has none.
func processArgs(pid int) []string {
	raw, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil || len(raw) < 4 {
		return nil
	}
	argc := int(int32(raw[0]) | int32(raw[1])<<8 | int32(raw[2])<<16 | int32(raw[3])<<24)
	rest := raw[4:]
	// The executable's path, then the NULs that pad it.
	if i := bytes.IndexByte(rest, 0); i >= 0 {
		rest = rest[i:]
	} else {
		return nil
	}
	rest = bytes.TrimLeft(rest, "\x00")
	var args []string
	for len(args) < argc && len(rest) > 0 {
		i := bytes.IndexByte(rest, 0)
		if i < 0 {
			args = append(args, string(rest))
			break
		}
		if i > 0 {
			args = append(args, string(rest[:i]))
		}
		rest = rest[i+1:]
	}
	return args
}

// cString is a NUL-terminated byte array as a string.
func cString(b []byte) string {
	if i := bytes.IndexByte(b, 0); i >= 0 {
		b = b[:i]
	}
	return string(b)
}
