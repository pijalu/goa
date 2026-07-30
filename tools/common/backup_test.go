// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package common

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// initBackupTestRepo creates a git repo with one committed file.
func initBackupTestRepo(t *testing.T) (dir, file string) {
	t.Helper()
	dir = t.TempDir()
	gitCmd(t, dir, "init")
	gitCmd(t, dir, "config", "user.email", "t@t.com")
	gitCmd(t, dir, "config", "user.name", "t")
	file = filepath.Join(dir, "tracked.txt")
	if err := os.WriteFile(file, []byte("original\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCmd(t, dir, "add", "tracked.txt")
	gitCmd(t, dir, "commit", "-m", "seed")
	return dir, file
}

func gitCmd(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

// TestBackupStager_LeavesGitIndexAlone is the regression test for the
// unexpected-staging bug: snapshotting a tracked file before an agent edit
// must NOT touch the git index — previously `git status` showed the file
// staged (pre-edit snapshot) without any user action.
func TestBackupStager_LeavesGitIndexAlone(t *testing.T) {
	dir, file := initBackupTestRepo(t)
	bs := NewBackupStager(dir)

	if err := bs.StageBeforeEdit(file, dir); err != nil {
		t.Fatalf("StageBeforeEdit: %v", err)
	}

	// The index must be untouched: git status stays clean.
	cmd := exec.Command("git", "status", "--porcelain")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git status: %v", err)
	}
	if len(out) != 0 {
		t.Errorf("git index mutated by StageBeforeEdit, status: %s", out)
	}

	// The recovery point exists with the pre-edit content.
	if !bs.HasBackup(file, dir) {
		t.Fatal("expected a backup snapshot")
	}
	backupPath, _ := bs.BackupPath(file, dir)
	data, err := os.ReadFile(backupPath)
	if err != nil {
		t.Fatalf("read backup: %v", err)
	}
	if string(data) != "original\n" {
		t.Errorf("backup content = %q, want pre-edit content", data)
	}
}

// TestBackupStager_LatestSnapshotWins mirrors the old staging semantics: the
// latest pre-edit state is the recovery point.
func TestBackupStager_LatestSnapshotWins(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "f.txt")
	bs := NewBackupStager(dir)

	for _, content := range []string{"v1", "v2"} {
		if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := bs.StageBeforeEdit(file, dir); err != nil {
			t.Fatalf("StageBeforeEdit: %v", err)
		}
	}
	backupPath, _ := bs.BackupPath(file, dir)
	data, _ := os.ReadFile(backupPath)
	if string(data) != "v2" {
		t.Errorf("backup = %q, want latest pre-edit content v2", data)
	}
}

// TestBackupStager_NewFileNoBackup: nothing to recover for files the agent
// is about to create.
func TestBackupStager_NewFileNoBackup(t *testing.T) {
	dir := t.TempDir()
	bs := NewBackupStager(dir)
	missing := filepath.Join(dir, "new.txt")
	if err := bs.StageBeforeEdit(missing, dir); err != nil {
		t.Fatalf("StageBeforeEdit on missing file: %v", err)
	}
	if bs.HasBackup(missing, dir) {
		t.Error("no backup expected for a nonexistent file")
	}
}

// TestBackupStager_RestoreBackup verifies /undo's restore path: content and
// mode come back from the snapshot; restoring without one is a no-op.
func TestBackupStager_RestoreBackup(t *testing.T) {
	dir, file := initBackupTestRepo(t)
	bs := NewBackupStager(dir)
	if err := os.Chmod(file, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := bs.StageBeforeEdit(file, dir); err != nil {
		t.Fatalf("StageBeforeEdit: %v", err)
	}

	// Agent edit clobbers the file.
	if err := os.WriteFile(file, []byte("agent edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	restored, err := bs.RestoreBackup(file, dir)
	if err != nil || !restored {
		t.Fatalf("RestoreBackup = %v, %v", restored, err)
	}
	data, _ := os.ReadFile(file)
	if string(data) != "original\n" {
		t.Errorf("restored content = %q, want original", data)
	}
	info, _ := os.Stat(file)
	if info.Mode().Perm() != 0o600 {
		t.Errorf("restored mode = %o, want 600 (snapshot mode)", info.Mode().Perm())
	}

	// No snapshot → (false, nil).
	restored, err = bs.RestoreBackup(filepath.Join(dir, "other.txt"), dir)
	if err != nil || restored {
		t.Errorf("RestoreBackup without snapshot = %v, %v", restored, err)
	}
}
