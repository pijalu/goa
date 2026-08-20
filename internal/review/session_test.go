// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package review

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNewSession_NotGit(t *testing.T) {
	dir := t.TempDir()
	_, err := NewSession(dir)
	if err == nil {
		t.Fatal("expected error for non-git dir")
	}
}

func TestNewSession_Defaults(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello\n"), 0644)
	run(t, dir, "git", "add", ".")
	run(t, dir, "git", "commit", "-m", "first")

	s, err := NewSession(dir)
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	if s.ID == "" {
		t.Error("expected session ID")
	}
	// With a single commit, HEAD^1 does not exist so base falls back to HEAD.
	if s.BaseRef != "HEAD" {
		t.Errorf("expected single-commit base HEAD, got %q", s.BaseRef)
	}
	if len(s.HeadRef) != 40 {
		t.Errorf("expected head SHA, got %q", s.HeadRef)
	}

	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("world\n"), 0644)
	run(t, dir, "git", "add", ".")
	run(t, dir, "git", "commit", "-m", "second")

	s, err = NewSession(dir)
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	if s.BaseRef != "HEAD^1" {
		t.Errorf("expected clean multi-commit base HEAD^1, got %q", s.BaseRef)
	}
	if s.Dirty {
		t.Error("expected Dirty=false")
	}

	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("dirty\n"), 0644)
	s, err = NewSession(dir)
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	if s.BaseRef != "HEAD" {
		t.Errorf("expected dirty base HEAD, got %q", s.BaseRef)
	}
	if !s.Dirty {
		t.Error("expected Dirty=true")
	}
}

func TestSession_Comments(t *testing.T) {
	s := &Session{ID: "abc", ProjectDir: "/tmp"}
	c := s.AddComment("main.go", 10, SideNew, "fix this")
	if c.ID == "" {
		t.Error("expected comment ID")
	}
	if len(s.Comments) != 1 {
		t.Fatalf("expected 1 comment, got %d", len(s.Comments))
	}

	got := s.CommentsFor("main.go", 10, SideNew)
	if len(got) != 1 || got[0].Content != "fix this" {
		t.Errorf("unexpected comments: %+v", got)
	}

	// The same file+number on the other diff side must NOT match: old and
	// new line numbers are different coordinate spaces.
	if leaked := s.CommentsFor("main.go", 10, SideOld); len(leaked) != 0 {
		t.Errorf("comment leaked across diff sides: %+v", leaked)
	}

	updated, ok := s.UpdateComment(c.ID, "fix that")
	if !ok {
		t.Fatal("expected update to succeed")
	}
	if updated.Content != "fix that" {
		t.Errorf("expected updated content, got %q", updated.Content)
	}

	if !s.RemoveComment(c.ID) {
		t.Error("expected remove to succeed")
	}
	if len(s.Comments) != 0 {
		t.Errorf("expected 0 comments, got %d", len(s.Comments))
	}
}

func TestSession_MarkdownSummary(t *testing.T) {
	s := &Session{ID: "abc", BaseRef: "HEAD^1", HeadRef: "def123"}
	s.AddComment("main.go", 4, SideNew, "rename variable")
	summary := s.MarkdownSummary()
	if !containsOne(summary, "main.go:4") {
		t.Errorf("expected file/line in summary, got:\n%s", summary)
	}
	if !containsOne(summary, "rename variable") {
		t.Errorf("expected comment in summary, got:\n%s", summary)
	}
	// The summary must point to the diff command, not embed diff content.
	if !containsOne(summary, "git diff HEAD^1..HEAD") {
		t.Errorf("expected diff command pointer in summary, got:\n%s", summary)
	}
	if containsOne(summary, "```diff") {
		t.Errorf("summary must not embed a diff code block, got:\n%s", summary)
	}
}

// TestSession_MarkdownSummary_RemovedSideComment verifies that a comment on
// a removed line is labeled as old-side so the reader does not confuse its
// line number with new-file numbering.
func TestSession_MarkdownSummary_RemovedSideComment(t *testing.T) {
	s := &Session{ID: "abc", BaseRef: "HEAD^1", HeadRef: "def123"}
	s.AddComment("main.go", 4, SideOld, "why was this removed?")
	summary := s.MarkdownSummary()
	if !containsOne(summary, "main.go:4 (removed)") {
		t.Errorf("expected removed-side label in summary, got:\n%s", summary)
	}
}

// TestDiffCommand verifies the command shown in summaries matches what Diff
// executes, for both the working-tree and the range forms.
func TestDiffCommand(t *testing.T) {
	if got := DiffCommand("HEAD"); got != "git diff HEAD" {
		t.Errorf("DiffCommand(HEAD) = %q, want %q", got, "git diff HEAD")
	}
	if got := DiffCommand("HEAD^1"); got != "git diff HEAD^1..HEAD" {
		t.Errorf("DiffCommand(HEAD^1) = %q, want %q", got, "git diff HEAD^1..HEAD")
	}
}

// TestSession_CommentsFor_LegacySideNormalization verifies that comments
// persisted before the Side field existed (side == "") are treated as
// new-side comments, the common case for pre-existing review sessions.
func TestSession_CommentsFor_LegacySideNormalization(t *testing.T) {
	s := &Session{ID: "abc", ProjectDir: "/tmp"}
	s.Comments = append(s.Comments, Comment{ID: "legacy", File: "main.go", LineNum: 7, Content: "old note"})

	if got := s.CommentsFor("main.go", 7, SideNew); len(got) != 1 {
		t.Errorf("legacy comment should match new side, got %d", len(got))
	}
	if got := s.CommentsFor("main.go", 7, SideOld); len(got) != 0 {
		t.Errorf("legacy comment should not match old side, got %d", len(got))
	}
}

func TestSession_MarkdownSummary_NoComments(t *testing.T) {
	s := &Session{ID: "abc", BaseRef: "HEAD^1", HeadRef: "def123"}
	summary := s.MarkdownSummary()
	if !containsOne(summary, "# Code Review") {
		t.Errorf("expected heading, got:\n%s", summary)
	}
	if !containsOne(summary, "No comments yet") {
		t.Errorf("expected no-comments message, got:\n%s", summary)
	}
}

func TestSession_Export(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello\n"), 0644)
	run(t, dir, "git", "add", ".")
	run(t, dir, "git", "commit", "-m", "first")
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("world\n"), 0644)

	s := &Session{ID: "abc", BaseRef: "HEAD^1", HeadRef: "def", ProjectDir: dir}
	s.AddComment("a.txt", 1, SideNew, "why change?")

	path, err := s.ExportPath(dir)
	if err != nil {
		t.Fatalf("ExportPath failed: %v", err)
	}
	if err := s.Export(path); err != nil {
		t.Fatalf("Export failed: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read export: %v", err)
	}
	content := string(data)
	if !containsOne(content, "why change?") {
		t.Errorf("expected comment in export, got:\n%s", content)
	}
	// Export, like submit, must point to the diff command and not embed diffs.
	if !containsOne(content, "git diff HEAD^1..HEAD") {
		t.Errorf("expected diff command pointer in export, got:\n%s", content)
	}
	if containsOne(content, "```diff") {
		t.Errorf("export must not embed a diff code block, got:\n%s", content)
	}
}
