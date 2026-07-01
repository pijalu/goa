// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"os"
	"path/filepath"
	"testing"

	agentic "github.com/pijalu/goa/internal/agentic"
)

func TestTools(t *testing.T) {
	workDir := t.TempDir()
	logger := agentic.NewLogger(agentic.Error)

	toolsList := Tools(workDir, logger)
	if toolsList == nil {
		t.Fatal("Tools() returned nil")
	}

	if len(toolsList) != 4 {
		t.Errorf("Tools() returned %d tools, want 4", len(toolsList))
	}

	// Check that all expected tools are present
	toolNames := make(map[string]bool)
	for _, tool := range toolsList {
		schema := tool.Schema()
		toolNames[schema.Name] = true
	}

	expectedTools := []string{"read", "edit", "run_command", "rest_api"}
	for _, expected := range expectedTools {
		if !toolNames[expected] {
			t.Errorf("Expected tool %q not found in tools list", expected)
		}
	}
}

func TestToolsDifferentWorkDirs(t *testing.T) {
	workDir1 := t.TempDir()
	workDir2 := t.TempDir()
	logger := agentic.NewLogger(agentic.Error)

	tools1 := Tools(workDir1, logger)
	tools2 := Tools(workDir2, logger)

	if len(tools1) != len(tools2) {
		t.Errorf("Tools() returned different number of tools for different work dirs")
	}

	assertReadToolInWorkDir(t, tools1, workDir1)
}

func assertReadToolInWorkDir(t *testing.T, tools []agentic.Tool, workDir string) {
	t.Helper()
	if len(tools) == 0 {
		return
	}
	for _, tool := range tools {
		if tool.Schema().Name != "read" {
			continue
		}
		testFile := filepath.Join(workDir, "test.txt")
		if err := os.WriteFile(testFile, []byte("content"), 0644); err != nil {
			t.Fatalf("Failed to create test file: %v", err)
		}
		result, err := tool.Execute(`{"path": "test.txt"}`)
		if err != nil {
			t.Fatalf("Unexpected error reading file in %s: %v", workDir, err)
		}
		if result != "content" {
			t.Errorf("Execute() = %q, want %q", result, "content")
		}
		return
	}
}

func TestToolsWithNilLogger(t *testing.T) {
	workDir := t.TempDir()

	toolsList := Tools(workDir, nil)
	if toolsList == nil {
		t.Fatal("Tools() returned nil with nil logger")
	}

	if len(toolsList) != 4 {
		t.Errorf("Tools() returned %d tools, want 4", len(toolsList))
	}
}
