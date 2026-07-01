// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package internal

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// WorktreeManager manages git worktrees for sandboxed agent operations.
// It wraps the `git worktree` CLI with Create, Apply, Discard, and path
// resolution methods.
type WorktreeManager struct {
	mainDir string
	mode    WorktreeMode
	// activeWorktree tracks the currently active worktree per session.
	activeWorktree string
}

// NewWorktreeManager creates a new WorktreeManager for the given main tree directory.
func NewWorktreeManager(mainDir string, mode WorktreeMode) *WorktreeManager {
	return &WorktreeManager{
		mainDir: mainDir,
		mode:    mode,
	}
}

// ProjectDir returns the main project directory managed by this worktree manager.
func (w *WorktreeManager) ProjectDir() string {
	return w.mainDir
}

// WorktreeDir returns the directory where worktrees are created.
func (w *WorktreeManager) WorktreeDir() string {
	return filepath.Join(w.mainDir, ".goa", "worktrees")
}

// Create creates a new git worktree at .goa/worktrees/<name>.
// Returns the path to the worktree root.
func (w *WorktreeManager) Create(name string) (string, error) {
	worktreePath := filepath.Join(w.WorktreeDir(), name)

	// Check if worktree already exists
	if _, err := os.Stat(worktreePath); err == nil {
		return "", fmt.Errorf("worktree %q already exists at %s", name, worktreePath)
	}

	// Ensure parent directory exists
	if err := os.MkdirAll(w.WorktreeDir(), 0755); err != nil {
		return "", fmt.Errorf("create worktree dir: %w", err)
	}

	// git worktree add <path> -b goa-<name>
	branch := "goa-" + name
	cmd := exec.Command("git", "worktree", "add", "-b", branch, worktreePath)
	cmd.Dir = w.mainDir
	if output, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("git worktree add: %w\nOutput: %s", err, string(output))
	}

	w.activeWorktree = worktreePath
	return worktreePath, nil
}

// Apply syncs changes from the worktree back to the main tree using a diff.
func (w *WorktreeManager) Apply(worktreePath string) error {
	// git -C <worktree> diff HEAD | git -C <main> apply
	diffCmd := exec.Command("git", "-C", worktreePath, "diff", "HEAD")
	diffOutput, err := diffCmd.Output()
	if err != nil {
		return fmt.Errorf("generate diff from worktree: %w", err)
	}

	if len(diffOutput) == 0 {
		// No changes to apply
		return nil
	}

	applyCmd := exec.Command("git", "-C", w.mainDir, "apply")
	applyCmd.Stdin = strings.NewReader(string(diffOutput))
	if output, err := applyCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("apply worktree diff: %w\nOutput: %s", err, string(output))
	}

	return nil
}

// Discard removes the worktree from the filesystem and git.
func (w *WorktreeManager) Discard(worktreePath string) error {
	cmd := exec.Command("git", "worktree", "remove", "--force", worktreePath)
	cmd.Dir = w.mainDir
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git worktree remove: %w\nOutput: %s", err, string(output))
	}

	if w.activeWorktree == worktreePath {
		w.activeWorktree = ""
	}
	return nil
}

// ShouldUseWorktree determines whether a worktree should be used based on
// the configured mode and the isMultiAgent flag.
func (w *WorktreeManager) ShouldUseWorktree(isMultiAgent bool) bool {
	switch w.mode {
	case WorktreeAlways:
		return true
	case WorktreeMultiAgent:
		return isMultiAgent
	default:
		return false
	}
}

// ResolvePath translates a user-provided path to the appropriate worktree
// path when a worktree is active. If the worktree is not active, returns
// the original path unchanged.
func (w *WorktreeManager) ResolvePath(worktreePath, userPath string) string {
	if worktreePath == "" {
		return userPath
	}

	// If the path is relative, prepend the worktree path
	if !filepath.IsAbs(userPath) {
		return filepath.Join(worktreePath, userPath)
	}

	return userPath
}

// List returns all active worktrees managed by Goa.
func (w *WorktreeManager) List() ([]string, error) {
	worktreeDir := w.WorktreeDir()
	entries, err := os.ReadDir(worktreeDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var names []string
	for _, entry := range entries {
		if entry.IsDir() {
			names = append(names, entry.Name())
		}
	}
	return names, nil
}

// Cleanup removes all Goa-managed worktrees.
func (w *WorktreeManager) Cleanup() error {
	worktrees, err := w.List()
	if err != nil {
		return err
	}
	for _, name := range worktrees {
		path := filepath.Join(w.WorktreeDir(), name)
		if err := w.Discard(path); err != nil {
			return fmt.Errorf("cleanup worktree %q: %w", name, err)
		}
	}
	return nil
}

// CurrentWorktree returns the active worktree path, or empty string if
// no worktree is active.
func (w *WorktreeManager) CurrentWorktree() string {
	// Check if active worktree still exists
	if w.activeWorktree != "" {
		if _, err := os.Stat(w.activeWorktree); err == nil {
			return w.activeWorktree
		}
		w.activeWorktree = ""
	}
	return ""
}

// Ensure reader interface compliance
var _ io.Reader = (*strings.Reader)(nil)
