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

func TestNewReadFileTool(t *testing.T) {
	workDir := t.TempDir()
	logger := agentic.NewLogger(agentic.Error)

	tool := NewReadFileTool(workDir, logger)
	if tool == nil {
		t.Fatal("NewReadFileTool returned nil")
	}

	_ = tool
}

func TestReadFileToolSchema(t *testing.T) {
	workDir := t.TempDir()
	tool := NewReadFileTool(workDir, nil)

	schema := tool.Schema()
	if schema.Name != "read" {
		t.Errorf("Schema.Name = %q, want %q", schema.Name, "read")
	}
	if schema.Description == "" {
		t.Error("Schema.Description should not be empty")
	}
	if schema.Schema == nil {
		t.Error("Schema.Schema should not be nil")
	}

	// Check required fields
	schemaMap, ok := schema.Schema["properties"].(map[string]interface{})
	if !ok {
		t.Fatal("Schema properties not found or wrong type")
	}

	// Check that path is required
	required, ok := schema.Schema["required"].([]string)
	if !ok {
		t.Fatal("required field not found or wrong type")
	}
	if len(required) != 1 || required[0] != "path" {
		t.Errorf("required = %v, want [path]", required)
	}

	// Check path property
	pathProp, ok := schemaMap["path"].(map[string]interface{})
	if !ok {
		t.Fatal("path property not found")
	}
	if pathProp["type"] != "string" {
		t.Errorf("path type = %v, want %q", pathProp["type"], "string")
	}
}

func TestReadFileToolExecute(t *testing.T) {
	workDir := t.TempDir()

	// Create a test file
	testFile := filepath.Join(workDir, "test.txt")
	testContent := "Line 1\nLine 2\nLine 3\nLine 4\nLine 5"
	if err := os.WriteFile(testFile, []byte(testContent), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	tool := NewReadFileTool(workDir, nil)

	tests := []struct {
		name      string
		input     string
		want      string
		wantError bool
	}{
		{
			name:  "read_entire_file",
			input: `{"path": "test.txt"}`,
			want:  testContent,
		},
		{
			name:  "read_with_offset",
			input: `{"path": "test.txt", "offset": 2}`,
			want:  "Line 2\nLine 3\nLine 4\nLine 5",
		},
		{
			name:  "read_with_limit",
			input: `{"path": "test.txt", "limit": 2}`,
			want:  "Line 1\nLine 2",
		},
		{
			name:  "read_with_offset_and_limit",
			input: `{"path": "test.txt", "offset": 2, "limit": 2}`,
			want:  "Line 2\nLine 3",
		},
		{
			name:      "nonexistent_file",
			input:     `{"path": "nonexistent.txt"}`,
			wantError: true,
		},
		{
			name:      "invalid_json",
			input:     `not json`,
			wantError: true,
		},
		{
			name:      "missing_path",
			input:     `{"offset": 1}`,
			wantError: true,
		},
		{
			name:      "path_traversal",
			input:     `{"path": "../../../etc/passwd"}`,
			wantError: true,
		},
		{
			name:      "offset_exceeds_lines",
			input:     `{"path": "test.txt", "offset": 100}`,
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := tool.Execute(tt.input)
			if tt.wantError {
				if err == nil {
					t.Error("Expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}
			if result != tt.want {
				t.Errorf("Execute() = %q, want %q", result, tt.want)
			}
		})
	}
}

func TestReadFileToolPathTraversal(t *testing.T) {
	workDir := t.TempDir()
	tool := NewReadFileTool(workDir, nil)

	traversalAttempts := []string{
		`{"path": "../outside.txt"}`,
		`{"path": "../../etc/passwd"}`,
		`{"path": "/etc/passwd"}`,
		`{"path": "....//....//etc/passwd"}`,
	}

	for _, input := range traversalAttempts {
		t.Run(input, func(t *testing.T) {
			_, err := tool.Execute(input)
			if err == nil {
				t.Errorf("Expected path traversal error for %q", input)
			}
		})
	}
}

func TestReadFileToolAbsolutePath(t *testing.T) {
	workDir := t.TempDir()

	// Create a test file
	testFile := filepath.Join(workDir, "test.txt")
	if err := os.WriteFile(testFile, []byte("content"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	tool := NewReadFileTool(workDir, nil)

	// Try to read with absolute path outside workDir
	outsideFile := "/etc/hosts"
	input := `{"path": "` + outsideFile + `"}`
	_, err := tool.Execute(input)
	if err == nil {
		t.Error("Should not be able to read files outside workDir using absolute paths")
	}
}
