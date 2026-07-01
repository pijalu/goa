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
	c := s.AddComment("main.go", 10, "fix this")
	if c.ID == "" {
		t.Error("expected comment ID")
	}
	if len(s.Comments) != 1 {
		t.Fatalf("expected 1 comment, got %d", len(s.Comments))
	}

	got := s.CommentsFor("main.go", 10)
	if len(got) != 1 || got[0].Content != "fix this" {
		t.Errorf("unexpected comments: %+v", got)
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
	diff := `diff --git a/main.go b/main.go
--- a/main.go
+++ b/main.go
@@ -3,3 +3,3 @@
 package main
-func old() {}
+func new() {}
`
	s := &Session{ID: "abc", BaseRef: "HEAD^1", HeadRef: "def123"}
	s.AddComment("main.go", 4, "rename variable")
	summary := s.MarkdownSummary(diff)
	if !containsOne(summary, "main.go:4") {
		t.Errorf("expected file/line in summary, got:\n%s", summary)
	}
	if !containsOne(summary, "rename variable") {
		t.Errorf("expected comment in summary, got:\n%s", summary)
	}
	if !containsOne(summary, "```diff") {
		t.Errorf("expected diff code block in summary, got:\n%s", summary)
	}
	if !containsOne(summary, "-func old") {
		t.Errorf("expected commented hunk in summary, got:\n%s", summary)
	}
}

func TestSession_MarkdownSummary_NoComments(t *testing.T) {
	s := &Session{ID: "abc", BaseRef: "HEAD^1", HeadRef: "def123"}
	summary := s.MarkdownSummary("diff text")
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
	diff := `diff --git a/a.txt b/a.txt
--- a/a.txt
+++ b/a.txt
@@ -1 +1 @@
-hello
+world
`
	s.AddComment("a.txt", 1, "why change?")

	path, err := s.ExportPath(dir)
	if err != nil {
		t.Fatalf("ExportPath failed: %v", err)
	}
	if err := s.Export(diff, path); err != nil {
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
	if !containsOne(content, "```diff") {
		t.Errorf("expected diff code block in export, got:\n%s", content)
	}
	if !containsOne(content, "-hello") {
		t.Errorf("expected commented hunk in export, got:\n%s", content)
	}
}
