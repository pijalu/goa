// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package core

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/pijalu/goa/internal"
)

// SessionStateSnapshot holds the persisted runtime session state.
// This is auto-managed in .goa/state.json, separate from user config.
type SessionStateSnapshot struct {
	ModeState          internal.ModeState `json:"mode_state"`
	MinorMode          string             `json:"minor_mode"`           // "companion" or ""
	AgentDrivenEnabled bool               `json:"agent_driven_enabled"` // agent-driven tools enabled
	ThinkingLevel      string             `json:"thinking_level,omitempty"`
	CompanionHistory   []json.RawMessage  `json:"companion_history,omitempty"` // companion agent message history
	InputHistory       []string           `json:"input_history,omitempty"`     // user input history for readline
	ApprovedPaths      []string           `json:"approved_paths,omitempty"`    // persisted tool-path approvals
	DeniedPaths        []string           `json:"denied_paths,omitempty"`      // persisted tool-path denials
}

// StateStore persists runtime session state to disk.
type StateStore struct {
	projectDir string
	mu         sync.Mutex
}

// NewStateStore creates a StateStore for the given project directory.
func NewStateStore(projectDir string) *StateStore {
	return &StateStore{projectDir: projectDir}
}

func (s *StateStore) path() string {
	return filepath.Join(s.projectDir, ".goa", "state.json")
}

// Load reads the persisted state snapshot. Returns a zero snapshot if the
// file does not exist or is unreadable.
func (s *StateStore) Load() (SessionStateSnapshot, error) {
	var snap SessionStateSnapshot
	data, err := os.ReadFile(s.path())
	if err != nil {
		if os.IsNotExist(err) {
			return snap, nil
		}
		return snap, fmt.Errorf("read state file: %w", err)
	}
	if err := json.Unmarshal(data, &snap); err != nil {
		return snap, fmt.Errorf("unmarshal state file: %w", err)
	}
	return snap, nil
}

// Save writes the snapshot to disk.
func (s *StateStore) Save(snap SessionStateSnapshot) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	dir := filepath.Dir(s.path())
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create state dir: %w", err)
	}

	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal state: %w", err)
	}

	if err := os.WriteFile(s.path(), data, 0644); err != nil {
		return fmt.Errorf("write state file: %w", err)
	}
	return nil
}
