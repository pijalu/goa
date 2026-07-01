// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package common

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/internal/perms"
)

// IsProtectedPath checks if the given path is in a protected directory.
// Delegates to the perms package so the policy definition lives in one place.
func IsProtectedPath(path string) bool {
	return perms.IsProtectedPath(path)
}

// ResolveToolPath resolves a user-provided path through the WorktreeManager.
// If worktree is active, relative paths are resolved to the worktree directory.
// Path protection (.goa/.git and project-root containment) is intentionally
// NOT enforced here; it is the responsibility of the autonomy/policy layer
// (e.g. SoloGuard for SOLO mode). This lets YOLO mode access any path and
// confirm/review modes ask approval through the normal confirmation flow.
func ResolveToolPath(wm *internal.WorktreeManager, userPath string) (string, error) {
	worktreePath := ""
	if wm != nil {
		worktreePath = wm.CurrentWorktree()
	}

	resolved := wm.ResolvePath(worktreePath, userPath)

	// Ensure the resolved path is absolute for consistent handling
	if !filepath.IsAbs(resolved) {
		abs, err := filepath.Abs(resolved)
		if err != nil {
			return "", fmt.Errorf("resolve path %q: %w", userPath, err)
		}
		resolved = abs
	}

	return resolved, nil
}

// fuzzySkipDirs are directories FuzzyFindFile must never descend into.
var fuzzySkipDirs = map[string]bool{
	".git": true,
	".goa": true,
}

// FuzzyFindFile searches for the closest matching file in projectDir.
// Returns the matching path and a confidence score (0.0-1.0).
// Uses substring match, prefix match, and Levenshtein distance.
func FuzzyFindFile(projectDir, target string) (string, float64) {
	targetLower := strings.ToLower(filepath.Base(target))

	var best string
	bestScore := 0.0

	filepath.WalkDir(projectDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if fuzzySkipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		// Skip dotfiles (hidden) but allow descending into non-skipped dirs.
		if strings.HasPrefix(d.Name(), ".") {
			return nil
		}
		baseLower := strings.ToLower(d.Name())

		if baseLower == targetLower {
			best = path
			bestScore = 1.0
			return filepath.SkipAll
		}

		score := scoreFile(targetLower, baseLower)
		if score > bestScore {
			bestScore = score
			best = path
		}
		return nil
	})

	return best, bestScore
}

func scoreFile(targetLower, baseLower string) float64 {
	score := baseMatchScore(targetLower, baseLower)
	if score >= 0.9 {
		return score
	}
	sim := levenshteinSimilarity(targetLower, baseLower)
	if sim > score {
		return sim
	}
	return score
}

func baseMatchScore(targetLower, baseLower string) float64 {
	switch {
	case strings.HasPrefix(baseLower, targetLower):
		return 0.8
	case strings.HasPrefix(targetLower, baseLower):
		return 0.7
	case strings.Contains(baseLower, targetLower):
		return 0.5
	default:
		return 0.0
	}
}

func levenshteinSimilarity(a, b string) float64 {
	maxLen := len(a)
	if len(b) > maxLen {
		maxLen = len(b)
	}
	if maxLen == 0 {
		return 0.0
	}
	dist := LevenshteinDistance(a, b)
	return 1.0 - float64(dist)/float64(maxLen)
}

// LevenshteinDistance returns the edit distance between two strings.
func LevenshteinDistance(a, b string) int {
	if len(a) == 0 {
		return len(b)
	}
	if len(b) == 0 {
		return len(a)
	}
	// Use single-row optimization
	prev := make([]int, len(b)+1)
	curr := make([]int, len(b)+1)
	for j := 0; j <= len(b); j++ {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		curr[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			curr[j] = min(curr[j-1]+1, min(prev[j]+1, prev[j-1]+cost))
		}
		prev, curr = curr, prev
	}
	return prev[len(b)]
}

// LazySyncFromMain copies a file from the main tree to the worktree if it
// exists in the main tree but not in the worktree. This enables reading files
// that haven't been modified in the worktree yet.
func LazySyncFromMain(wm *internal.WorktreeManager, worktreePath, resolvedPath string) error {
	if wm == nil || worktreePath == "" {
		return nil
	}

	// Check if file exists in worktree
	if _, err := os.Stat(resolvedPath); err == nil {
		return nil // already exists
	}

	// Use filepath.Rel to get relative path from worktree root
	relPath, err := filepath.Rel(worktreePath, resolvedPath)
	if err != nil {
		return fmt.Errorf("lazy sync: get relative path: %w", err)
	}
	mainPath := filepath.Join(wm.WorktreeDir(), relPath)

	// Check if file exists in main tree
	if _, err := os.Stat(mainPath); err != nil {
		return nil // doesn't exist in main tree either
	}

	// Copy from main to worktree
	data, err := os.ReadFile(mainPath)
	if err != nil {
		return fmt.Errorf("lazy sync from main: %w", err)
	}

	// Ensure parent directory exists in worktree
	if err := os.MkdirAll(filepath.Dir(resolvedPath), 0755); err != nil {
		return fmt.Errorf("create worktree parent dir: %w", err)
	}

	return os.WriteFile(resolvedPath, data, 0644)
}
