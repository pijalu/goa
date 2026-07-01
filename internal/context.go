// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

// Package internal provides shared types, utilities, and infrastructure for Goa.
package internal

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ContextFile represents a loaded AGENTS.md or CLAUDE.md document.
type ContextFile struct {
	Path    string // absolute path to the file
	Content string // raw file content
	Source  string // "home" (~/.goa/AGENTS.md) or "project" (ancestor walk)
}

// LoadProjectContextFiles walks ancestor directories from projectDir up to the
// filesystem root, finding AGENTS.md (or CLAUDE.md) in each directory. It also
// checks ~/.goa/AGENTS.md as a global override.
//
// Returns all found files in order: home global → farthest ancestor → cwd.
// Closer-to-cwd files have higher priority (override earlier matches on
// collision). Each directory is searched at most once — the first match wins.
func LoadProjectContextFiles(projectDir, goaHomeDir string) []ContextFile {
	var result []ContextFile
	seen := make(map[string]bool) // track paths to avoid duplicates

	// 1. Check ~/.goa/AGENTS.md (global/home override)
	if goaHomeDir != "" {
		if cf := findContextFile(goaHomeDir); cf != nil {
			cf.Source = "home"
			result = append(result, *cf)
			seen[cf.Path] = true
		}
	}

	// 2. Walk ancestors from projectDir up to root, collecting farthest first
	var ancestors []ContextFile
	currentDir := projectDir
	root := "/"

	for {
		cf := findContextFile(currentDir)
		if cf != nil && !seen[cf.Path] {
			cf.Source = "project"
			ancestors = append(ancestors, *cf)
			seen[cf.Path] = true
		}

		if currentDir == root {
			break
		}
		parent := filepath.Dir(currentDir)
		if parent == currentDir {
			break
		}
		currentDir = parent
	}

	// Reverse ancestors so they appear in order: farthest (root-adjacent) → closest (cwd)
	// This means closer-to-cwd files override farther ones on name collision.
	for i := len(ancestors) - 1; i >= 0; i-- {
		result = append(result, ancestors[i])
	}

	return result
}

// findContextFile checks a single directory for AGENTS.md or CLAUDE.md.
// It looks for candidates in order: AGENTS.md, AGENTS.MD, CLAUDE.md, CLAUDE.MD.
// Returns the first match, or nil if none found.
func findContextFile(dir string) *ContextFile {
	candidates := []string{"AGENTS.md", "AGENTS.MD", "CLAUDE.md", "CLAUDE.MD"}
	for _, name := range candidates {
		path := filepath.Join(dir, name)
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			data, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			absPath, _ := filepath.Abs(path)
			return &ContextFile{
				Path:    absPath,
				Content: string(data),
			}
		}
	}
	return nil
}

// FindContextFile is the public equivalent of findContextFile.
// It checks a single directory for context file candidates.
func FindContextFile(dir string) (*ContextFile, error) {
	cf := findContextFile(dir)
	if cf == nil {
		return nil, fmt.Errorf("no context file found in %s", dir)
	}
	return cf, nil
}

// SortContextFilesByProximity sorts context files so closer-to-cwd files
// come last (highest priority). The last file in the result is the one
// closest to the working directory.
func SortContextFilesByProximity(files []ContextFile, projectDir string) []ContextFile {
	sorted := make([]ContextFile, len(files))
	copy(sorted, files)
	sort.SliceStable(sorted, func(i, j int) bool {
		// "home" source always comes first
		if sorted[i].Source != sorted[j].Source {
			return sorted[i].Source == "home"
		}
		// Otherwise, sort by depth (deeper = closer to cwd = higher priority)
		depthI := dirDepth(sorted[i].Path, projectDir)
		depthJ := dirDepth(sorted[j].Path, projectDir)
		return depthI < depthJ
	})
	return sorted
}

// dirDepth returns the number of directory components between base and path.
// Used to determine proximity: more components = deeper = closer to cwd.
func dirDepth(path, base string) int {
	rel, err := filepath.Rel(base, filepath.Dir(path))
	if err != nil {
		return 0
	}
	if rel == "." {
		return 0
	}
	return strings.Count(rel, string(filepath.Separator)) + 1
}
