// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package swarm

import "sync"

// Trigger identifies what activated swarm mode. It controls auto-exit
// semantics, mirroring the kimi-code SwarmModeTrigger design:
//
//   - ManualTrigger: persistent toggle set by `/swarm on` — stays on across
//     turns until `/swarm off`.
//   - TaskTrigger: one-shot set by `/swarm <prompt>` — auto-exits after the
//     turn that consumed the prompt completes.
//   - ToolTrigger: set by the agent_swarm tool itself — auto-exits after the
//     turn that issued the swarm call completes.
type Trigger int

const (
	NoTrigger Trigger = iota
	ManualTrigger
	TaskTrigger
	ToolTrigger
)

// State tracks whether the agent is currently operating in swarm mode and, if
// so, what activated it. The trigger decides whether swarm mode auto-exits at
// the end of a turn (task/tool) or persists until disabled (manual).
type State struct {
	mu      sync.RWMutex
	trigger Trigger
	task    string
}

// NewState creates a new inactive swarm state.
func NewState() *State { return &State{} }

// Enter activates swarm mode with the given trigger, recording the task
// description for status display. It is idempotent: a second Enter while
// already active is a no-op (the first trigger wins), matching kimi-code's
// SwarmMode.enter guard.
func (s *State) Enter(trigger Trigger, task string) {
	if trigger == NoTrigger {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.trigger != NoTrigger {
		return
	}
	s.trigger = trigger
	s.task = task
}

// Exit deactivates swarm mode regardless of the active trigger.
func (s *State) Exit() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.trigger = NoTrigger
	s.task = ""
}

// IsActive reports whether swarm mode is currently active.
func (s *State) IsActive() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.trigger != NoTrigger
}

// Trigger returns the trigger that activated swarm mode, or NoTrigger.
func (s *State) Trigger() Trigger {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.trigger
}

// Task returns the current swarm task description, if any.
func (s *State) Task() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.task
}

// ShouldAutoExit reports whether swarm mode should exit at the end of the
// current turn. Only one-shot triggers (task/tool) auto-exit; the manual
// toggle persists until explicitly disabled.
func (s *State) ShouldAutoExit() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.trigger == TaskTrigger || s.trigger == ToolTrigger
}

// MaybeAutoExit exits swarm mode iff ShouldAutoExit is true. Returns true if
// it exited. This is the hook the agent turn loop calls at end-of-turn.
func (s *State) MaybeAutoExit() bool {
	if !s.ShouldAutoExit() {
		return false
	}
	s.Exit()
	return true
}
