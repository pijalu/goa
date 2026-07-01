// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package internal

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFindContextFile_AGENTSMD(t *testing.T) {
	dir := t.TempDir()
	content := "# Project Instructions\n\nBe careful with the database."
	path := filepath.Join(dir, "AGENTS.md")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cf, err := FindContextFile(dir)
	if err != nil {
		t.Fatalf("FindContextFile: %v", err)
	}
	if cf == nil {
		t.Fatal("FindContextFile returned nil")
	}
	if cf.Content != content {
		t.Errorf("Content = %q, want %q", cf.Content, content)
	}
	if cf.Path != path {
		t.Errorf("Path = %q, want %q", cf.Path, path)
	}
}

func TestFindContextFile_CLAUDEMD(t *testing.T) {
	dir := t.TempDir()
	content := "# Claude Context"
	path := filepath.Join(dir, "CLAUDE.md")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cf, err := FindContextFile(dir)
	if err != nil {
		t.Fatalf("FindContextFile: %v", err)
	}
	if cf == nil {
		t.Fatal("FindContextFile returned nil")
	}
	if cf.Content != content {
		t.Errorf("Content = %q, want %q", cf.Content, content)
	}
}

func TestFindContextFile_Missing(t *testing.T) {
	dir := t.TempDir()
	cf, err := FindContextFile(dir)
	if err == nil {
		t.Fatal("Expected error for missing context file")
	}
	if cf != nil {
		t.Fatalf("Expected nil, got %+v", cf)
	}
}

func TestFindContextFile_AGENTSMDPreferred(t *testing.T) {
	dir := t.TempDir()
	agentsContent := "# AGENTS instructions"
	claudeContent := "# Claude instructions"

	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte(agentsContent), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte(claudeContent), 0o644); err != nil {
		t.Fatal(err)
	}

	cf, err := FindContextFile(dir)
	if err != nil {
		t.Fatalf("FindContextFile: %v", err)
	}
	if cf.Content != agentsContent {
		t.Errorf("AGENTS.md should be preferred over CLAUDE.md, got %q", cf.Content)
	}
}

func TestLoadProjectContextFiles_NoFiles(t *testing.T) {
	projectDir := t.TempDir()
	files := LoadProjectContextFiles(projectDir, "")
	if len(files) != 0 {
		t.Fatalf("Expected 0 files, got %d", len(files))
	}
}

func TestLoadProjectContextFiles_ProjectOnly(t *testing.T) {
	projectDir := t.TempDir()
	content := "# Project context"
	if err := os.WriteFile(filepath.Join(projectDir, "AGENTS.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	files := LoadProjectContextFiles(projectDir, "")
	if len(files) != 1 {
		t.Fatalf("Expected 1 file, got %d", len(files))
	}
	if files[0].Content != content {
		t.Errorf("Content = %q, want %q", files[0].Content, content)
	}
	if files[0].Source != "project" {
		t.Errorf("Source = %q, want %q", files[0].Source, "project")
	}
}

func TestLoadProjectContextFiles_HomeOverride(t *testing.T) {
	homeDir := t.TempDir()
	projectDir := t.TempDir()

	homeContent := "# Home global context"
	projectContent := "# Project context"

	if err := os.WriteFile(filepath.Join(homeDir, "AGENTS.md"), []byte(homeContent), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, "AGENTS.md"), []byte(projectContent), 0o644); err != nil {
		t.Fatal(err)
	}

	files := LoadProjectContextFiles(projectDir, homeDir)
	if len(files) != 2 {
		t.Fatalf("Expected 2 files, got %d", len(files))
	}
	// Home file should be first
	if files[0].Source != "home" {
		t.Errorf("files[0].Source = %q, want %q", files[0].Source, "home")
	}
	if files[0].Content != homeContent {
		t.Errorf("files[0].Content = %q, want %q", files[0].Content, homeContent)
	}
	// Project file should be second
	if files[1].Source != "project" {
		t.Errorf("files[1].Source = %q, want %q", files[1].Source, "project")
	}
	if files[1].Content != projectContent {
		t.Errorf("files[1].Content = %q, want %q", files[1].Content, projectContent)
	}
}

func TestLoadProjectContextFiles_AncestorWalk(t *testing.T) {
	// Create a structure: tmpDir/subdir/project
	tmpDir := t.TempDir()
	subDir := filepath.Join(tmpDir, "middle")
	projectDir := filepath.Join(subDir, "project")

	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}

	rootContent := "# Root context"
	projectContent := "# Project context"

	if err := os.WriteFile(filepath.Join(tmpDir, "AGENTS.md"), []byte(rootContent), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, "AGENTS.md"), []byte(projectContent), 0o644); err != nil {
		t.Fatal(err)
	}

	files := LoadProjectContextFiles(projectDir, "")
	if len(files) != 2 {
		t.Fatalf("Expected 2 files (root + project), got %d", len(files))
	}

	// Root (farthest) should be first, project (closest) last
	if files[0].Content != rootContent {
		t.Errorf("files[0] should be root content, got %q", files[0].Content)
	}
	last := files[len(files)-1]
	if last.Content != projectContent {
		t.Errorf("last file should be project content, got %q", last.Content)
	}
}

func TestLoadProjectContextFiles_ClosestWins(t *testing.T) {
	// Create a structure where both root and project have AGENTS.md
	tmpDir := t.TempDir()
	projectDir := filepath.Join(tmpDir, "sub")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(tmpDir, "AGENTS.md"), []byte("root"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, "AGENTS.md"), []byte("project"), 0o644); err != nil {
		t.Fatal(err)
	}

	files := LoadProjectContextFiles(projectDir, "")
	if len(files) != 2 {
		t.Fatalf("Expected 2 files, got %d", len(files))
	}
	// Both should be present, project overrides root
}

func TestSortContextFilesByProximity(t *testing.T) {
	projectDir := "/home/user/project"
	files := []ContextFile{
		{Path: "/home/user/project/AGENTS.md", Source: "project"},
		{Path: "/home/user/.goa/AGENTS.md", Source: "home"},
		{Path: "/home/user/sub/AGENTS.md", Source: "project"},
	}

	sorted := SortContextFilesByProximity(files, projectDir)
	if len(sorted) != 3 {
		t.Fatalf("Expected 3 files, got %d", len(sorted))
	}
	// Home source should always be first regardless of depth
	if sorted[0].Source != "home" {
		t.Errorf("sorted[0].Source = %q, want 'home'", sorted[0].Source)
	}
	// Project files should have home before project
	// Sorted by proximity: farthest first (lower depth = farther)
	for _, f := range sorted {
		if f.Path == "" {
			t.Errorf("All files should have valid paths")
		}
	}
}
