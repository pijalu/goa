// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/tools/common"
)

// TestUndoUnstaged_RestoresFromBackup verifies agent edits are recovered from
// .goa/backups snapshots — the pre-edit content comes back WITHOUT touching
// the git index (the unexpected-staging bug's fix).
func TestUndoUnstaged_RestoresFromBackup(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "f.txt")
	stager := common.NewBackupStager(dir)

	// Pre-edit state, snapshotted before the agent edit.
	if err := os.WriteFile(file, []byte("pre-edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := stager.StageBeforeEdit(file, dir); err != nil {
		t.Fatalf("StageBeforeEdit: %v", err)
	}
	// Agent edit.
	if err := os.WriteFile(file, []byte("agent edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var buf strings.Builder
	ctx := core.Context{ProjectDir: dir, OutputBuffer: &buf}
	if err := undoUnstaged(ctx, []string{"f.txt"}, 1); err != nil {
		t.Fatalf("undoUnstaged: %v", err)
	}

	data, _ := os.ReadFile(file)
	if string(data) != "pre-edit\n" {
		t.Errorf("content = %q, want pre-edit snapshot", data)
	}
	if !strings.Contains(buf.String(), "(restored from backup)") {
		t.Errorf("output %q missing backup restore marker", buf.String())
	}
}

// TestUndoUnstaged_GitCheckoutFallback verifies files WITHOUT a backup
// snapshot (the user's own changes) still revert via git checkout.
func TestUndoUnstaged_GitCheckoutFallback(t *testing.T) {
	dir := t.TempDir()
	gitCmd := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	gitCmd("init")
	gitCmd("config", "user.email", "t@t.com")
	gitCmd("config", "user.name", "t")
	file := filepath.Join(dir, "own.txt")
	if err := os.WriteFile(file, []byte("committed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCmd("add", "own.txt")
	gitCmd("commit", "-m", "seed")

	// User's own worktree change, no agent backup.
	if err := os.WriteFile(file, []byte("user change\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var buf strings.Builder
	ctx := core.Context{ProjectDir: dir, OutputBuffer: &buf}
	if err := undoUnstaged(ctx, []string{"own.txt"}, 1); err != nil {
		t.Fatalf("undoUnstaged: %v", err)
	}

	data, _ := os.ReadFile(file)
	if string(data) != "committed\n" {
		t.Errorf("content = %q, want committed state via git checkout", data)
	}
	if strings.Contains(buf.String(), "(restored from backup)") {
		t.Errorf("fallback path must not claim a backup restore: %q", buf.String())
	}
}
