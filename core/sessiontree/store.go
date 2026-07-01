// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package sessiontree

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Store persists session trees.
type Store interface {
	Save(tree *Tree) error
	Load() (*Tree, error)
}

// JSONStore persists a tree as JSON.
type JSONStore struct {
	path string
}

// NewJSONStore creates a JSON tree store.
func NewJSONStore(path string) *JSONStore {
	return &JSONStore{path: path}
}

type storedTree struct {
	Nodes  []*Node `json:"nodes"`
	RootID string  `json:"root_id"`
	NextID int     `json:"next_id"`
}

// Save writes the tree to disk.
func (s *JSONStore) Save(tree *Tree) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("mkdir session tree store: %w", err)
	}
	st := storedTree{
		Nodes:  tree.All(),
		RootID: tree.rootID,
		NextID: tree.nextID,
	}
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, data, 0o644)
}

// Load reads the tree from disk.
func (s *JSONStore) Load() (*Tree, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read session tree store: %w", err)
	}
	var st storedTree
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, fmt.Errorf("parse session tree store: %w", err)
	}
	t := &Tree{
		nodes:  make(map[string]*Node, len(st.Nodes)),
		rootID: st.RootID,
		nextID: st.NextID,
	}
	for _, n := range st.Nodes {
		t.nodes[n.ID] = n
	}
	return t, nil
}

// NopStore is a no-op store.
type NopStore struct{}

// Save implements Store.
func (NopStore) Save(tree *Tree) error { return nil }

// Load implements Store.
func (NopStore) Load() (*Tree, error) { return nil, nil }
