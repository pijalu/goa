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

func TestWriteFileFuzzyFilename_OverwritesClosestFile(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "target.txt")
	if err := os.WriteFile(filePath, []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}

	t.Chdir(dir)

	tool := &WriteFileTool{}
	result, err := tool.Execute(`{"path": "targt.txt", "content": "new"}`)
	if err != nil {
		t.Fatalf("Execute with fuzzy filename should succeed: %v", err)
	}
	if !strings.Contains(result, "used closest match") {
		t.Errorf("Expected fuzzy-match note, got: %q", result)
	}
	data, _ := os.ReadFile(filePath)
	if string(data) != "new" {
		t.Errorf("Content = %q, want %q", string(data), "new")
	}
}

func TestWriteFileFuzzyFilename_Disabled(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "target.txt")
	if err := os.WriteFile(filePath, []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}

	t.Chdir(dir)

	off := false
	tool := &WriteFileTool{Config: FileToolConfig{FuzzyMatch: &off}}
	_, err := tool.Execute(`{"path": "targt.txt", "content": "new"}`)
	if err != nil {
		t.Fatalf("Execute with fuzzy disabled should create new file: %v", err)
	}
	// A new file named targt.txt should be created instead of overwriting target.txt
	if _, err := os.Stat(filepath.Join(dir, "targt.txt")); err != nil {
		t.Errorf("Expected targt.txt to be created when fuzzy matching is disabled")
	}
	data, _ := os.ReadFile(filePath)
	if string(data) != "old" {
		t.Errorf("target.txt should be untouched, got: %q", string(data))
	}
}
