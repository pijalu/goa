// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package sessiontree

import "sync"

// Manager wraps a Tree with concurrency-safe access and persistence.
type Manager struct {
	mu    sync.RWMutex
	tree  *Tree
	store Store
}

// NewManager loads or creates a session tree.
func NewManager(store Store) *Manager {
	m := &Manager{store: store}
	if store != nil {
		t, _ := store.Load()
		m.tree = t
	}
	if m.tree == nil {
		m.tree = NewTree("root")
	}
	return m
}

// Tree returns the underlying tree.
func (m *Manager) Tree() *Tree {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.tree
}

// Fork creates a new child node.
func (m *Manager) Fork(parentID, message, summary string) (*Node, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n, err := m.tree.AddChild(parentID, message, summary)
	if err != nil {
		return nil, err
	}
	_ = m.saveLocked()
	return n, nil
}

// Clone copies a subtree.
func (m *Manager) Clone(sourceID, parentID string) (*Node, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n, err := m.tree.Clone(sourceID, parentID)
	if err != nil {
		return nil, err
	}
	_ = m.saveLocked()
	return n, nil
}

func (m *Manager) saveLocked() error {
	if m.store == nil {
		return nil
	}
	return m.store.Save(m.tree)
}
