// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package sessiontree

import (
	"path/filepath"
	"testing"
)

func TestTreeFork(t *testing.T) {
	tree := NewTree("root")
	child, err := tree.AddChild(tree.Root().ID, "hello", "child")
	if err != nil {
		t.Fatalf("fork: %v", err)
	}
	if len(tree.Children(tree.Root().ID)) != 1 {
		t.Errorf("children = %d", len(tree.Children(tree.Root().ID)))
	}
	if path, err := tree.Path(child.ID); err != nil || len(path) != 2 {
		t.Errorf("path = %v, err = %v", path, err)
	}
}

func TestTreeClone(t *testing.T) {
	tree := NewTree("root")
	child, _ := tree.AddChild(tree.Root().ID, "a", "child")
	_, _ = tree.AddChild(child.ID, "b", "grandchild")
	clone, err := tree.Clone(child.ID, tree.Root().ID)
	if err != nil {
		t.Fatalf("clone: %v", err)
	}
	if len(tree.Children(clone.ID)) != 1 {
		t.Errorf("cloned children = %d", len(tree.Children(clone.ID)))
	}
}

func TestManagerPersistence(t *testing.T) {
	dir := t.TempDir()
	store := NewJSONStore(filepath.Join(dir, "tree.json"))
	m := NewManager(store)
	child, _ := m.Fork(m.Tree().Root().ID, "msg", "summary")

	m2 := NewManager(store)
	if _, ok := m2.Tree().Node(child.ID); !ok {
		t.Error("expected child persisted")
	}
}
