// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

// Package toolaccess defines shared types for tool resource access declarations.
// It is imported by both the tools package (for tool implementations) and the
// agentic package (for the concurrent tool scheduler), avoiding a circular
// dependency between those two packages.
package toolaccess

// Access describes the resources a tool accesses during execution.
// The scheduler uses this to determine which tools can run in parallel
// and which must be serialized to avoid conflicts.
type Access struct {
	// ReadPaths are file paths this tool reads.
	ReadPaths []string

	// WritePaths are file paths this tool writes to.
	WritePaths []string

	// Category groups tools by broad resource type.
	// Tools in the same category always conflict (serialized).
	// Empty string means no category-level conflict.
	// Known categories: "shell", "network", "memory".
	Category string
}

// Accessor is the interface tools implement to declare their resource access.
type Accessor interface {
	// Access returns the resources this tool accesses for the given input.
	// Returns zero-value Access if the tool cannot determine access.
	Access(input string) Access
}

// Conflict reports whether a and b access overlapping resources and thus
// cannot safely execute in parallel. Concurrent reads on the same path
// are always safe.
func Conflict(a, b Access) bool {
	if categoryConflict(a, b) {
		return true
	}
	if pathConflict(a, b) {
		return true
	}
	return false
}

// categoryConflict returns true when both accesses have the same non-empty
// category (e.g., two shell commands always conflict).
func categoryConflict(a, b Access) bool {
	return a.Category != "" && b.Category != "" && a.Category == b.Category
}

// pathConflict returns true when any write path of one access overlaps with
// any path (read or write) of the other. Pure read-read has no conflict.
func pathConflict(a, b Access) bool {
	switch {
	case intersects(a.WritePaths, b.WritePaths):
		return true
	case intersects(a.ReadPaths, b.WritePaths):
		return true
	case intersects(a.WritePaths, b.ReadPaths):
		return true
	default:
		return false
	}
}

// intersects returns true if any string in a also appears in b.
func intersects(a, b []string) bool {
	set := make(map[string]struct{}, len(a))
	for _, s := range a {
		set[s] = struct{}{}
	}
	for _, s := range b {
		if _, ok := set[s]; ok {
			return true
		}
	}
	return false
}
