// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

//go:build !windows

package internal

import (
	"fmt"
	"os/exec"
	"syscall"
)

// Signal delivers sig to the session's foreground process group. Sessions are
// started with Setsid (pty.StartWithSize), making the child a session and
// process-group leader, so the negative PID targets the whole foreground
// process group (the shell plus its current job).
func (pm *PTYManager) Signal(id string, sig syscall.Signal) error {
	s, err := pm.getSession(id)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.running {
		return fmt.Errorf("session %q is not running", id)
	}
	if s.Cmd == nil || s.Cmd.Process == nil {
		return fmt.Errorf("session %q has no process", id)
	}
	pid := s.Cmd.Process.Pid
	if err := syscall.Kill(-pid, sig); err != nil {
		return fmt.Errorf("signal %v to session %q: %w", sig, id, err)
	}
	return nil
}

// killSessionTree terminates a session's captured process tree: the session
// leader (the shell) plus every process in its group. Sessions are Setsid
// leaders, so the negative PID targets the whole tree (including background
// jobs that would survive a PTY hangup).
func killSessionTree(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	pid := cmd.Process.Pid
	_ = syscall.Kill(-pid, syscall.SIGKILL)
	_ = cmd.Process.Kill()
}
