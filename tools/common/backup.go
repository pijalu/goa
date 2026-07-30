// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package common

import (
	"fmt"
	"os"
	"path/filepath"
)

// BackupStager snapshots files to .goa/backups/ before agent edits, providing
// a recovery point (/undo restores the pre-edit content).
//
// It deliberately uses shadow copies instead of `git add`: staging through
// the git index silently rewrote the user's index with pre-edit snapshots
// (files appeared staged without user action, the index diverged from the
// worktree, and a later `git commit` could commit the STALE pre-edit
// content). Shadow backups carry the same recovery semantics with zero git
// side effects — and also work outside git repositories.
type BackupStager struct {
	backupDir string
}

// NewBackupStager creates a stager that stores backups in .goa/backups/.
func NewBackupStager(projectDir string) *BackupStager {
	return &BackupStager{
		backupDir: filepath.Join(projectDir, ".goa", "backups"),
	}
}

// StageBeforeEdit snapshots absPath's current content (and mode) to
// .goa/backups/<relpath>.bak, overwriting any previous snapshot — the latest
// pre-edit state is the recovery point. A file that does not exist yet is
// not backed up (nothing to recover).
func (bs *BackupStager) StageBeforeEdit(absPath, projectDir string) error {
	data, info, err := readIfExists(absPath)
	if err != nil || info == nil {
		return err // nil when the file does not exist: new file, no backup needed
	}
	backupPath, err := bs.BackupPath(absPath, projectDir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(backupPath), 0o755); err != nil {
		return err
	}
	bs.ensureSelfIgnored()
	return os.WriteFile(backupPath, data, info.Mode())
}

// ensureSelfIgnored drops a `.gitignore: *` into the backup dir so snapshots
// never pollute the user's `git status` as untracked files. A worktree
// .gitignore applies even when untracked itself, so no repo-level setup is
// required. Best-effort: a write failure must not break editing.
func (bs *BackupStager) ensureSelfIgnored() {
	gitignore := filepath.Join(bs.backupDir, ".gitignore")
	if _, err := os.Stat(gitignore); err == nil {
		return
	}
	_ = os.WriteFile(gitignore, []byte("*\n"), 0o644)
}

// BackupPath returns the snapshot path for absPath, relative to projectDir.
func (bs *BackupStager) BackupPath(absPath, projectDir string) (string, error) {
	relPath, err := filepath.Rel(projectDir, absPath)
	if err != nil {
		return "", fmt.Errorf("backup path for %s: %w", absPath, err)
	}
	return filepath.Join(bs.backupDir, relPath+".bak"), nil
}

// HasBackup reports whether a snapshot exists for absPath.
func (bs *BackupStager) HasBackup(absPath, projectDir string) bool {
	backupPath, err := bs.BackupPath(absPath, projectDir)
	if err != nil {
		return false
	}
	_, err = os.Stat(backupPath)
	return err == nil
}

// RestoreBackup writes absPath's snapshot back over absPath, preserving the
// snapshot's mode. Returns false when no snapshot exists. The snapshot is
// kept: recovery points stay valid for repeated restores and later edits.
func (bs *BackupStager) RestoreBackup(absPath, projectDir string) (bool, error) {
	backupPath, err := bs.BackupPath(absPath, projectDir)
	if err != nil {
		return false, err
	}
	data, info, err := readIfExists(backupPath)
	if err != nil || info == nil {
		return false, err // nil when no snapshot exists
	}
	if err := os.WriteFile(absPath, data, info.Mode()); err != nil {
		return true, fmt.Errorf("restore %s: %w", absPath, err)
	}
	return true, nil
}

// readIfExists reads path and returns (data, info, nil); (nil, nil, nil)
// when path does not exist.
func readIfExists(path string) ([]byte, os.FileInfo, error) {
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	return data, info, nil
}
