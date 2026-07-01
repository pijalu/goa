// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package plan

import (
	"path/filepath"
	"strings"
	"sync"
)

// State tracks plan mode.
type State struct {
	mu       sync.RWMutex
	active   bool
	planFile string
}

// NewState creates a plan-mode state.
func NewState() *State {
	return &State{planFile: "PLAN.md"}
}

// Enable activates plan mode.
func (s *State) Enable() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.active = true
}

// Disable deactivates plan mode.
func (s *State) Disable() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.active = false
}

// IsActive reports whether plan mode is active.
func (s *State) IsActive() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.active
}

// IsAllowedPath reports whether a write/edit is permitted in plan mode.
// Only the plan file may be written.
func (s *State) IsAllowedPath(path string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.active {
		return true
	}
	base := filepath.Base(path)
	return strings.EqualFold(base, s.planFile)
}

// PlanFile returns the plan file name.
func (s *State) PlanFile() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.planFile
}
