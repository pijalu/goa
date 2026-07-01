// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// fdAvailable checks if the `fd` CLI tool is on PATH.
// Results are cached after first check.
var fdAvailable bool
var fdPath string

func init() {
	path, err := exec.LookPath("fd")
	if err == nil {
		fdAvailable = true
		fdPath = path
	}
}

// fdSearchResult holds a single file/directory result from fd.
type fdSearchResult struct {
	Path        string
	IsDirectory bool
}

// fdSearch runs `fd` to search for files matching the query under baseDir.
// Supports AbortController pattern via context cancellation.
func fdSearch(ctx context.Context, baseDir, query string, maxResults int) ([]fdSearchResult, error) {
	if !fdAvailable {
		return nil, nil // fd not available, caller should fall back
	}

	args := []string{
		"--base-directory", baseDir,
		"--max-results", fmt.Sprintf("%d", maxResults),
		"--type", "f",
		"--type", "d",
		"--follow",
		"--hidden",
		"--exclude", ".git",
		"--exclude", ".git/*",
		"--exclude", ".git/**",
	}

	// Use full-path mode for multi-segment queries
	if strings.Contains(query, "/") {
		args = append(args, "--full-path")
	}

	if query != "" {
		args = append(args, query)
	}

	cmd := exec.CommandContext(ctx, fdPath, args...)
	output, err := cmd.Output()
	if err != nil {
		// Command may fail if context is cancelled or no matches
		return nil, err
	}

	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	results := make([]fdSearchResult, 0, len(lines))
	for _, line := range lines {
		if line == "" {
			continue
		}
		info, statErr := os.Stat(filepath.Join(baseDir, line))
		if statErr != nil {
			continue
		}
		results = append(results, fdSearchResult{
			Path:        line,
			IsDirectory: statErr == nil && info.IsDir(),
		})
	}
	return results, nil
}
