// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package core

import (
	"sync"

	"github.com/pijalu/goa/internal"
)

// SessionState tracks the live mode state, separate from Config defaults.
// This is what changes when the user runs /mode, a skill runs, or a workflow
// pushes a temporary mode.
type SessionState struct {
	mu          sync.RWMutex
	current     internal.ModeState
	modeStack   []internal.ModeState
	modeSource  string
	skillSource string
}

// NewSessionState creates a session state from config defaults.
func NewSessionState(defaults internal.ModeState) *SessionState {
	return &SessionState{
		current: defaults,
	}
}

// Current returns the current ModeState (thread-safe).
func (s *SessionState) Current() internal.ModeState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.current
}

// SetMode replaces the current mode (not push — for direct user changes).
func (s *SessionState) SetMode(ms internal.ModeState) internal.ModeState {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.current = ms
	s.modeSource = ""
	s.skillSource = ""
	return s.current
}

// PushMode saves current and activates new mode (for skills or temporary
// mode changes). The source string records why the mode was pushed.
func (s *SessionState) PushMode(ms internal.ModeState, source string) internal.ModeState {
	s.mu.Lock()
	defer s.mu.Unlock()
	prev := s.current
	s.modeStack = append(s.modeStack, prev)
	s.current = ms
	s.modeSource = source
	return prev
}

// PopMode restores the mode from the top of the stack.
func (s *SessionState) PopMode() internal.ModeState {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.modeStack) == 0 {
		return s.current
	}
	prev := s.modeStack[len(s.modeStack)-1]
	s.modeStack = s.modeStack[:len(s.modeStack)-1]
	s.current = prev
	s.modeSource = ""
	return s.current
}

// Source returns the source of the current pushed mode.
func (s *SessionState) Source() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.modeSource
}

// SetSource records the source of the current mode change.
func (s *SessionState) SetSource(source string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.modeSource = source
}

// SetSkillSource records what skill activated the current stack.
func (s *SessionState) SetSkillSource(skill string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.skillSource = skill
}

// SkillSource returns the skill that activated the current stack.
func (s *SessionState) SkillSource() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.skillSource
}

// PreviousMode returns the mode before the last push (if any).
func (s *SessionState) PreviousMode() *internal.ModeState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.modeStack) == 0 {
		return nil
	}
	prev := s.modeStack[len(s.modeStack)-1]
	return &prev
}
