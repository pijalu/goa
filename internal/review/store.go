// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package review

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Store persists review sessions under .goa/reviews/<id>/.
type Store struct {
	projectDir string
}

// NewStore creates a store rooted at projectDir.
func NewStore(projectDir string) *Store {
	return &Store{projectDir: projectDir}
}

// SessionDir returns the directory for a session ID.
func (st *Store) SessionDir(id string) string {
	return filepath.Join(st.projectDir, ".goa", "reviews", id)
}

// Save persists the session and its comments.
func (st *Store) Save(s *Session) error {
	dir := st.SessionDir(s.ID)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create review dir: %w", err)
	}
	path := filepath.Join(dir, "session.json")
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal session: %w", err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("write session: %w", err)
	}
	return nil
}

// Load reads a session from disk.
func (st *Store) Load(id string) (*Session, error) {
	path := filepath.Join(st.SessionDir(id), "session.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read session: %w", err)
	}
	var s Session
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("unmarshal session: %w", err)
	}
	return &s, nil
}

// List returns all stored review session IDs.
func (st *Store) List() ([]string, error) {
	dir := filepath.Join(st.projectDir, ".goa", "reviews")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var ids []string
	for _, e := range entries {
		if e.IsDir() {
			ids = append(ids, e.Name())
		}
	}
	return ids, nil
}
