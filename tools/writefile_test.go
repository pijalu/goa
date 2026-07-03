// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestWriteFileNew verifies creating a new file.
func TestWriteFileNew(t *testing.T) {
	dir, cleanup := tempDir(t)
	defer cleanup()

	filePath := dir + "/newfile.txt"
	tool := &WriteFileTool{}
	result, err := tool.Execute(`{"path": "` + filePath + `", "content": "hello world"}`)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if !strings.Contains(result, "Written") {
		t.Errorf("Result missing success indicator: %s", result)
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("Read file: %v", err)
	}
	if string(data) != "hello world" {
		t.Errorf("Content = %q, want %q", string(data), "hello world")
	}
}

// TestWriteFileOverwrite verifies overwriting an existing file.
func TestWriteFileOverwrite(t *testing.T) {
	dir, cleanup := tempDir(t)
	defer cleanup()

	filePath := dir + "/overwrite.txt"
	writeFile(t, filePath, "old content")

	tool := &WriteFileTool{}
	_, err := tool.Execute(`{"path": "` + filePath + `", "content": "new content"}`)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	data, _ := os.ReadFile(filePath)
	if string(data) != "new content" {
		t.Errorf("Content = %q, want %q", string(data), "new content")
	}
}

// TestWriteFileCreateDirs verifies parent directory creation.
func TestWriteFileCreateDirs(t *testing.T) {
	dir, cleanup := tempDir(t)
	defer cleanup()

	filePath := dir + "/a/b/c/deep.txt"
	tool := &WriteFileTool{}
	_, err := tool.Execute(`{"path": "` + filePath + `", "content": "deep", "create_dirs": true}`)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		t.Error("File should exist with create_dirs=true")
	}
}

// TestWriteFileMissingPath verifies error when path is missing.
func TestWriteFileMissingPath(t *testing.T) {
	tool := &WriteFileTool{}
	_, err := tool.Execute(`{"content": "hello"}`)
	if err == nil {
		t.Fatal("Expected error for missing path")
	}
	errStr := err.Error()
	if !strings.Contains(errStr, "missing_path") {
		t.Errorf("Expected missing_path error: %s", errStr)
	}
}

func TestWriteFileAtPrefix_ResolvesToCurrentDir(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "test.txt")

	t.Chdir(dir)

	tool := &WriteFileTool{}
	_, err := tool.Execute(`{"path": "@test.txt", "content": "hello"}`)
	if err != nil {
		t.Fatalf("Execute with @ prefix should succeed: %v", err)
	}
	data, _ := os.ReadFile(filePath)
	if string(data) != "hello" {
		t.Errorf("Content = %q, want %q", string(data), "hello")
	}
}

func TestWriteFileFuzzyRejected_CreatesNewFileAsNamed(t *testing.T) {
	dir := t.TempDir()
	existingPath := filepath.Join(dir, "target.txt")
	if err := os.WriteFile(existingPath, []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}

	t.Chdir(dir)

	// Write should NOT fuzzy-match to target.txt — it must create the exact
	// path requested, even when a close match already exists.
	tool := &WriteFileTool{}
	result, err := tool.Execute(`{"path": "targt.txt", "content": "new"}`)
	if err != nil {
		t.Fatalf("Execute should succeed, creating file at exact requested path: %v", err)
	}
	if strings.Contains(result, "used closest match") {
		t.Errorf("Write MUST NOT use fuzzy filename matching, got fuzzy-match note: %q", result)
	}

	// A new file named exactly targt.txt should be created
	newFilePath := filepath.Join(dir, "targt.txt")
	if _, err := os.Stat(newFilePath); err != nil {
		t.Errorf("Expected targt.txt to be created at exact requested path")
	}
	data, _ := os.ReadFile(newFilePath)
	if string(data) != "new" {
		t.Errorf("targt.txt content = %q, want %q", string(data), "new")
	}

	// Existing target.txt must be untouched
	existingData, _ := os.ReadFile(existingPath)
	if string(existingData) != "old" {
		t.Errorf("target.txt should be untouched, got: %q", string(existingData))
	}
}

func TestWriteFileFuzzyFilename_Disabled(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "target.txt")
	if err := os.WriteFile(filePath, []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}

	t.Chdir(dir)

	// Config field is removed — write never fuzzy matches.
	// This test verifies the behavior is correct by default.
	tool := &WriteFileTool{}
	_, err := tool.Execute(`{"path": "targt.txt", "content": "new"}`)
	if err != nil {
		t.Fatalf("Execute should create new file at exact path: %v", err)
	}
	// A new file named targt.txt should be created instead of overwriting target.txt
	if _, err := os.Stat(filepath.Join(dir, "targt.txt")); err != nil {
		t.Errorf("Expected targt.txt to be created when fuzzy matching is not used")
	}
	data, _ := os.ReadFile(filePath)
	if string(data) != "old" {
		t.Errorf("target.txt should be untouched, got: %q", string(data))
	}
}
