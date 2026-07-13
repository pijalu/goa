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

// levenshteinDistance tests

func TestLevenshteinDistance_Empty(t *testing.T) {
	if d := levenshteinDistance("", ""); d != 0 {
		t.Errorf("expected 0, got %d", d)
	}
	if d := levenshteinDistance("abc", ""); d != 3 {
		t.Errorf("expected 3, got %d", d)
	}
	if d := levenshteinDistance("", "xyz"); d != 3 {
		t.Errorf("expected 3, got %d", d)
	}
}

func TestLevenshteinDistance_Exact(t *testing.T) {
	if d := levenshteinDistance("hello", "hello"); d != 0 {
		t.Errorf("expected 0, got %d", d)
	}
}

func TestLevenshteinDistance_Insert(t *testing.T) {
	if d := levenshteinDistance("cat", "cats"); d != 1 {
		t.Errorf("expected 1, got %d", d)
	}
}

func TestLevenshteinDistance_Delete(t *testing.T) {
	if d := levenshteinDistance("books", "book"); d != 1 {
		t.Errorf("expected 1, got %d", d)
	}
}

func TestLevenshteinDistance_Substitute(t *testing.T) {
	if d := levenshteinDistance("cat", "car"); d != 1 {
		t.Errorf("expected 1, got %d", d)
	}
}

func TestLevenshteinDistance_CompletelyDifferent(t *testing.T) {
	if d := levenshteinDistance("abc", "xyz"); d != 3 {
		t.Errorf("expected 3, got %d", d)
	}
}

// FuzzyFindFile tests

func TestFuzzyFindFile_ExactMatch(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main"), 0644)

	path, score := FuzzyFindFile(dir, "main.go")
	if path == "" {
		t.Fatal("expected a match")
	}
	if score != 1.0 {
		t.Errorf("expected score 1.0 for exact match, got %f", score)
	}
}

func TestFuzzyFindFile_ExactMatchCaseInsensitive(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "Main.go"), []byte("package main"), 0644)

	path, score := FuzzyFindFile(dir, "main.go")
	if path == "" {
		t.Fatal("expected a match")
	}
	if score != 1.0 {
		t.Errorf("expected score 1.0 for case-insensitive exact match, got %f", score)
	}
}

func TestFuzzyFindFile_StartsWith(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "main_test.go"), []byte("test"), 0644)

	path, score := FuzzyFindFile(dir, "main")
	if path == "" {
		t.Fatal("expected a match")
	}
	if score < 0.7 || score > 0.9 {
		t.Errorf("expected score ~0.8 for starts-with match, got %f", score)
	}
}

func TestFuzzyFindFile_Contains(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "utils_helper.go"), []byte("helper"), 0644)

	path, score := FuzzyFindFile(dir, "helper")
	if path == "" {
		t.Fatal("expected a match")
	}
	if score < 0.4 || score > 0.6 {
		t.Errorf("expected score ~0.5 for contains match, got %f", score)
	}
}

func TestFuzzyFindFile_NoMatch_Threshold(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main"), 0644)

	path, score := FuzzyFindFile(dir, "nonexistent_file_xyz")
	if score > 0.3 {
		t.Errorf("expected low score for poor match, got %f", score)
	}
	if path == "" {
		t.Error("expected some path (best-effort match)")
	}
}

func TestFuzzyFindFile_EmptyDir(t *testing.T) {
	dir := t.TempDir()

	path, score := FuzzyFindFile(dir, "anything")
	if path != "" {
		t.Errorf("expected empty path for empty dir, got %q", path)
	}
	if score != 0.0 {
		t.Errorf("expected score 0.0, got %f", score)
	}
}

func TestFuzzyFindFile_IgnoresDotFiles(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, ".hidden.go"), []byte("hidden"), 0644)

	path, _ := FuzzyFindFile(dir, "hidden")
	if path != "" {
		t.Errorf("expected no match for dotfile, got %q", path)
	}
}

func TestFuzzyFindFile_Subdirectory(t *testing.T) {
	dir := t.TempDir()
	subdir := filepath.Join(dir, "sub")
	os.MkdirAll(subdir, 0755)
	os.WriteFile(filepath.Join(subdir, "deep.go"), []byte("deep"), 0644)

	path, score := FuzzyFindFile(dir, "deep.go")
	if path == "" {
		t.Fatal("expected to find file in subdirectory")
	}
	if score != 1.0 {
		t.Errorf("expected score 1.0 for exact match in subdir, got %f", score)
	}
	if !strings.Contains(path, "sub") {
		t.Errorf("expected path to contain subdirectory, got %q", path)
	}
}

// bash helper tests

func TestMatchEnvKey_Exact(t *testing.T) {
	if !matchEnvKey("API_KEY", "API_KEY") {
		t.Error("expected exact match to pass")
	}
}

func TestMatchEnvKey_Wildcard(t *testing.T) {
	if !matchEnvKey("GITHUB_TOKEN", "GITHUB_*") {
		t.Error("expected wildcard match to pass")
	}
}

func TestMatchEnvKey_NoMatch(t *testing.T) {
	if matchEnvKey("HOME", "API_*") {
		t.Error("expected no match")
	}
}

func TestTruncateCommand_Short(t *testing.T) {
	result := truncateCommand("echo hello", 100)
	if result != "echo hello" {
		t.Errorf("expected unchanged, got %q", result)
	}
}

func TestTruncateCommand_Long(t *testing.T) {
	cmd := "echo " + strings.Repeat("x", 100)
	result := truncateCommand(cmd, 20)
	if len(result) > 25 {
		t.Errorf("expected truncated, got %d chars: %q", len(result), result)
	}
	if !strings.HasSuffix(result, "...") {
		t.Errorf("expected trailing ellipsis, got %q", result)
	}
}

func TestTruncateCommand_ZeroMax(t *testing.T) {
	result := truncateCommand("hello", 0)
	if result != "..." {
		t.Errorf("expected just ellipsis, got %q", result)
	}
}
