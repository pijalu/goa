// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/pijalu/goa/internal"
)

// ReadFileTool method tests

func TestReadFileTool_WorktreeSyncError(t *testing.T) {
	tool := &ReadFileTool{}
	err := tool.worktreeSyncError(errors.New("sync failed"))
	if err == nil {
		t.Fatal("expected non-nil error")
	}
	if !strings.Contains(err.Error(), "Worktree sync") {
		t.Errorf("expected Worktree sync message, got: %v", err)
	}
}

func TestReadFileTool_ReadFileError_NotFound(t *testing.T) {
	tool := &ReadFileTool{}
	err := tool.readFileError("/missing/path", os.ErrNotExist)
	if err == nil {
		t.Fatal("expected non-nil error")
	}
	var toolErr *internal.ToolError
	if !errors.As(err, &toolErr) {
		t.Errorf("expected ToolError, got %T", err)
	}
	if toolErr.Type != "file_not_found" {
		t.Errorf("expected type 'file_not_found', got %q", toolErr.Type)
	}
}

func TestReadFileTool_ReadFileError_Permission(t *testing.T) {
	tool := &ReadFileTool{}
	err := tool.readFileError("/protected/path", errors.New("permission denied"))
	if err == nil {
		t.Fatal("expected non-nil error")
	}
	var toolErr *internal.ToolError
	if !errors.As(err, &toolErr) {
		t.Errorf("expected ToolError, got %T", err)
	}
	_ = toolErr
}
