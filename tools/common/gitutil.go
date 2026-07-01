// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package common

import (
	"os"
	"os/exec"
	"path/filepath"
)

// GitStager handles git staging as a recovery point before file edits.
type GitStager struct {
	backupDir string
}

// NewGitStager creates a stager that stores backups in .goa/backups/.
func NewGitStager(projectDir string) *GitStager {
	return &GitStager{
		backupDir: filepath.Join(projectDir, ".goa", "backups"),
	}
}

// StageBeforeEdit creates a git recovery point by staging the file.
// If git-tracked: runs `git add <path>`.
// If not git-tracked: copies to .goa/backups/<path>.bak.
func (gs *GitStager) StageBeforeEdit(absPath, projectDir string) error {
	// Check if file is git-tracked
	if isGitTracked(absPath) {
		cmd := exec.Command("git", "add", absPath)
		cmd.Dir = projectDir
		return cmd.Run()
	}

	// Fallback: copy to backups
	relPath, err := filepath.Rel(projectDir, absPath)
	if err != nil {
		return err
	}
	backupPath := filepath.Join(gs.backupDir, relPath+".bak")
	if err := os.MkdirAll(filepath.Dir(backupPath), 0755); err != nil {
		return err
	}
	data, err := os.ReadFile(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // new file, no backup needed
		}
		return err
	}
	return os.WriteFile(backupPath, data, 0644)
}

// isGitTracked checks if a file is tracked by git.
func isGitTracked(absPath string) bool {
	cmd := exec.Command("git", "ls-files", "--error-unmatch", absPath)
	return cmd.Run() == nil
}
