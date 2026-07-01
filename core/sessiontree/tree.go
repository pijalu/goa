// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package sessiontree

import (
	"fmt"
	"time"
)

// Node represents a single point in the session tree.
type Node struct {
	ID        string    `json:"id"`
	ParentID  string    `json:"parent_id,omitempty"`
	Message   string    `json:"message,omitempty"`
	Summary   string    `json:"summary,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// Tree manages session branch nodes.
type Tree struct {
	nodes  map[string]*Node
	rootID string
	nextID int
}

// NewTree creates a tree with a root node.
func NewTree(rootSummary string) *Tree {
	root := &Node{
		ID:        "node-1",
		Summary:   rootSummary,
		CreatedAt: time.Now(),
	}
	return &Tree{
		nodes:  map[string]*Node{root.ID: root},
		rootID: root.ID,
		nextID: 2,
	}
}

// Root returns the root node.
func (t *Tree) Root() *Node { return t.nodes[t.rootID] }

// Node returns a node by ID.
func (t *Tree) Node(id string) (*Node, bool) {
	n, ok := t.nodes[id]
	return n, ok
}

// Children returns direct children of a node.
func (t *Tree) Children(parentID string) []*Node {
	var out []*Node
	for _, n := range t.nodes {
		if n.ParentID == parentID {
			out = append(out, n)
		}
	}
	return out
}

// AddChild adds a child node under parentID.
func (t *Tree) AddChild(parentID, message, summary string) (*Node, error) {
	if _, ok := t.nodes[parentID]; !ok {
		return nil, fmt.Errorf("parent %q not found", parentID)
	}
	id := fmt.Sprintf("node-%d", t.nextID)
	t.nextID++
	n := &Node{
		ID:        id,
		ParentID:  parentID,
		Message:   message,
		Summary:   summary,
		CreatedAt: time.Now(),
	}
	t.nodes[id] = n
	return n, nil
}

// Clone copies the subtree rooted at sourceID under parentID.
func (t *Tree) Clone(sourceID, parentID string) (*Node, error) {
	src, ok := t.nodes[sourceID]
	if !ok {
		return nil, fmt.Errorf("source %q not found", sourceID)
	}
	if _, ok := t.nodes[parentID]; !ok {
		return nil, fmt.Errorf("parent %q not found", parentID)
	}
	return t.cloneNode(src, parentID)
}

func (t *Tree) cloneNode(src *Node, parentID string) (*Node, error) {
	id := fmt.Sprintf("node-%d", t.nextID)
	t.nextID++
	n := &Node{
		ID:        id,
		ParentID:  parentID,
		Message:   src.Message,
		Summary:   src.Summary,
		CreatedAt: time.Now(),
	}
	t.nodes[id] = n
	for _, child := range t.Children(src.ID) {
		if _, err := t.cloneNode(child, n.ID); err != nil {
			return nil, err
		}
	}
	return n, nil
}

// Path returns ancestor IDs from root to the given node (inclusive).
func (t *Tree) Path(id string) ([]*Node, error) {
	var path []*Node
	for id != "" {
		n, ok := t.nodes[id]
		if !ok {
			return nil, fmt.Errorf("node %q not found", id)
		}
		path = append([]*Node{n}, path...)
		id = n.ParentID
	}
	return path, nil
}

// All returns all nodes.
func (t *Tree) All() []*Node {
	out := make([]*Node, 0, len(t.nodes))
	for _, n := range t.nodes {
		out = append(out, n)
	}
	return out
}
