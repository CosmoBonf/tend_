//go:build linux

package pty

import (
	"os"
	"strconv"
	"strings"
	"syscall"
	"unsafe"
)

// Foreground returns the name of the program currently in charge of the
// terminal, which is not the same as the command the pane was started with.
//
// Almost nobody opens a pane by naming an agent. They open a shell and type
// its name, so the pane's command is "zsh" forever while the thing on screen
// is Claude Code. The terminal itself knows which process group is in the
// foreground — that is how it decides where Ctrl+C goes — and asking it is
// the only answer that stays right as the user starts and quits programs.
//
// An empty name is not an error: the process may have just exited, or be one
// this process is not allowed to look at.
func (p *Pty) Foreground() string {
	if p.f == nil {
		return ""
	}
	pgrp, err := foregroundGroup(p.f)
	if err != nil || pgrp <= 0 {
		return ""
	}
	name, err := os.ReadFile("/proc/" + strconv.Itoa(pgrp) + "/comm")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(name))
}

// ForegroundArgv is the arguments of the program in charge of the terminal,
// for a runtime whose name says nothing (node, python, a shell) — the agent
// it runs is its script (internal/agent's WrappedAgentName). Nil when the
// process cannot be read.
func (p *Pty) ForegroundArgv() []string {
	if p.f == nil {
		return nil
	}
	pgrp, err := foregroundGroup(p.f)
	if err != nil || pgrp <= 0 {
		return nil
	}
	cmdline, err := os.ReadFile("/proc/" + strconv.Itoa(pgrp) + "/cmdline")
	if err != nil {
		return nil
	}
	var argv []string
	for _, arg := range strings.Split(strings.TrimRight(string(cmdline), "\x00"), "\x00") {
		if arg != "" {
			argv = append(argv, arg)
		}
	}
	return argv
}

// foregroundGroup asks the terminal which process group has it.
//
// The descriptor is borrowed through SyscallConn rather than taken with Fd().
// This runs on a timer while another goroutine is reading the same pty, and
// Fd() neither holds the file open for the duration nor leaves the descriptor
// as it found it — it detaches it from the runtime poller, which would turn
// every later read on it into a blocking one.
func foregroundGroup(f *os.File) (int, error) {
	raw, err := f.SyscallConn()
	if err != nil {
		return 0, err
	}

	var pgrp int32
	var errno syscall.Errno
	if err := raw.Control(func(fd uintptr) {
		_, _, errno = syscall.Syscall(
			syscall.SYS_IOCTL,
			fd,
			uintptr(syscall.TIOCGPGRP),
			uintptr(unsafe.Pointer(&pgrp)),
		)
	}); err != nil {
		return 0, err
	}
	if errno != 0 {
		return 0, errno
	}
	return int(pgrp), nil
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

// maxForegroundProcesses bounds the walk through /proc: a group of thousands is
// a build system mid-flight, and the first few are what anybody looks at.
const maxForegroundProcesses = 64

// Processes reads the terminal's foreground group from /proc.
func (p *Pty) Processes() ProcessInfo {
	info := ProcessInfo{ShellPid: p.pid}
	if p.f == nil {
		return info
	}
	if pgrp, err := foregroundGroup(p.f); err == nil && pgrp > 0 {
		info.ForegroundGroup = pgrp
	}
	if link, err := os.Readlink("/proc/" + strconv.Itoa(p.pid) + "/fd/0"); err == nil {
		info.TTY = link
	}
	if info.ForegroundGroup == 0 {
		return info
	}

	entries, err := os.ReadDir("/proc")
	if err != nil {
		return info
	}
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		if processGroup(pid) != info.ForegroundGroup {
			continue
		}
		proc := Process{Pid: pid}
		if name, err := os.ReadFile("/proc/" + e.Name() + "/comm"); err == nil {
			proc.Name = strings.TrimSpace(string(name))
		}
		if cmdline, err := os.ReadFile("/proc/" + e.Name() + "/cmdline"); err == nil {
			for _, arg := range strings.Split(strings.TrimRight(string(cmdline), "\x00"), "\x00") {
				if arg != "" {
					proc.Command = append(proc.Command, arg)
				}
			}
		}
		info.Foreground = append(info.Foreground, proc)
		if len(info.Foreground) >= maxForegroundProcesses {
			break
		}
	}
	return info
}

// processGroup reads a process's group from /proc/<pid>/stat. The name field
// is in parentheses and may itself contain spaces and parentheses, so the
// fields are counted from the last ")".
func processGroup(pid int) int {
	stat, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return 0
	}
	s := string(stat)
	end := strings.LastIndexByte(s, ')')
	if end < 0 || end+2 >= len(s) {
		return 0
	}
	fields := strings.Fields(s[end+2:])
	// state ppid pgrp ...
	if len(fields) < 3 {
		return 0
	}
	pgrp, err := strconv.Atoi(fields[2])
	if err != nil {
		return 0
	}
	return pgrp
}

// ChildForeground is the process group in charge of the terminal when it is
// not the pane's own process: a program its shell started — an agent typed
// at the prompt. It is 0 when the pane's process has the terminal itself, or
// when it cannot be told.
func (p *Pty) ChildForeground() int {
	if p.f == nil {
		return 0
	}
	pgrp, err := foregroundGroup(p.f)
	if err != nil || pgrp <= 0 || pgrp == p.pid {
		return 0
	}
	return pgrp
}

// SignalGroup sends a signal to every process of a group.
func SignalGroup(pgrp int, sig syscall.Signal) error {
	if pgrp <= 0 {
		return syscall.ESRCH
	}
	return syscall.Kill(-pgrp, sig)
}

// GroupMemory is the resident memory the processes of a group hold, in
// bytes: what ending the group gives back. An agent's group holds the
// helpers it started too — its MCP servers, a language server — and they go
// with it.
func GroupMemory(pgrp int) int64 {
	if pgrp <= 0 {
		return 0
	}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return 0
	}
	page := int64(os.Getpagesize())
	var total int64
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || processGroup(pid) != pgrp {
			continue
		}
		statm, err := os.ReadFile("/proc/" + e.Name() + "/statm")
		if err != nil {
			continue
		}
		// size resident shared ...: the second is the resident pages.
		fields := strings.Fields(string(statm))
		if len(fields) < 2 {
			continue
		}
		if pages, err := strconv.ParseInt(fields[1], 10, 64); err == nil {
			total += pages * page
		}
	}
	return total
}
