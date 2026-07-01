// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package review

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func initGitRepo(t *testing.T, dir string) {
	t.Helper()
	run(t, dir, "git", "init")
	run(t, dir, "git", "config", "user.email", "test@example.com")
	run(t, dir, "git", "config", "user.name", "Test")
}

func run(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = dir
	if err := cmd.Run(); err != nil {
		t.Fatalf("%v: %v", args, err)
	}
}

func TestIsGitRepo(t *testing.T) {
	dir := t.TempDir()
	if IsGitRepo(dir) {
		t.Error("expected non-git dir to be false")
	}
	initGitRepo(t, dir)
	if !IsGitRepo(dir) {
		t.Error("expected git dir to be true")
	}
}

func TestHeadSHA(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello"), 0644)
	run(t, dir, "git", "add", ".")
	run(t, dir, "git", "commit", "-m", "first")

	sha, err := HeadSHA(dir)
	if err != nil {
		t.Fatalf("HeadSHA failed: %v", err)
	}
	if len(sha) != 40 {
		t.Errorf("expected full SHA, got %q", sha)
	}
}

func TestHasUncommittedChanges(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello"), 0644)
	run(t, dir, "git", "add", ".")
	run(t, dir, "git", "commit", "-m", "first")

	dirty, err := HasUncommittedChanges(dir)
	if err != nil {
		t.Fatalf("HasUncommittedChanges failed: %v", err)
	}
	if dirty {
		t.Error("expected clean tree after commit")
	}

	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("world"), 0644)
	dirty, err = HasUncommittedChanges(dir)
	if err != nil {
		t.Fatalf("HasUncommittedChanges failed: %v", err)
	}
	if !dirty {
		t.Error("expected dirty tree after modification")
	}
}

func TestDefaultBase(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello"), 0644)
	run(t, dir, "git", "add", ".")
	run(t, dir, "git", "commit", "-m", "first")

	base, err := DefaultBase(dir)
	if err != nil {
		t.Fatalf("DefaultBase failed: %v", err)
	}
	if base != "HEAD" {
		t.Errorf("single-commit tree should default to HEAD, got %q", base)
	}

	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("world"), 0644)
	run(t, dir, "git", "add", ".")
	run(t, dir, "git", "commit", "-m", "second")

	base, err = DefaultBase(dir)
	if err != nil {
		t.Fatalf("DefaultBase failed: %v", err)
	}
	if base != "HEAD^1" {
		t.Errorf("clean multi-commit tree should default to HEAD^1, got %q", base)
	}

	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("dirty"), 0644)
	base, err = DefaultBase(dir)
	if err != nil {
		t.Fatalf("DefaultBase failed: %v", err)
	}
	if base != "HEAD" {
		t.Errorf("dirty tree should default to HEAD, got %q", base)
	}
}

func TestDiff(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello\n"), 0644)
	run(t, dir, "git", "add", ".")
	run(t, dir, "git", "commit", "-m", "first")

	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("world\n"), 0644)
	diff, err := Diff(dir, "HEAD")
	if err != nil {
		t.Fatalf("Diff failed: %v", err)
	}
	if !contains(diff, "-hello", "+world") {
		t.Errorf("expected working-tree diff, got:\n%s", diff)
	}

	os.WriteFile(filepath.Join(dir, "b.txt"), []byte("extra\n"), 0644)
	run(t, dir, "git", "add", ".")
	run(t, dir, "git", "commit", "-m", "second")

	diff, err = Diff(dir, "HEAD^1")
	if err != nil {
		t.Fatalf("Diff failed: %v", err)
	}
	if !contains(diff, "+world", "b.txt") {
		t.Errorf("expected last-commit diff, got:\n%s", diff)
	}
}

func TestRecentCommits(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("1"), 0644)
	run(t, dir, "git", "add", ".")
	run(t, dir, "git", "commit", "-m", "first commit message")

	commits, err := RecentCommits(dir, 10, 80)
	if err != nil {
		t.Fatalf("RecentCommits failed: %v", err)
	}
	if len(commits) != 1 {
		t.Fatalf("expected 1 commit, got %d", len(commits))
	}
	if commits[0].Subject != "first commit message" {
		t.Errorf("unexpected subject %q", commits[0].Subject)
	}

	// Long subject should be truncated.
	commits, err = RecentCommits(dir, 10, 5)
	if err != nil {
		t.Fatalf("RecentCommits failed: %v", err)
	}
	if len([]rune(commits[0].Subject)) > 5 {
		t.Errorf("expected truncated subject, got %q", commits[0].Subject)
	}
}

// TestRecentCommits_MultiCommitNoNewlineLeak is a regression test for the
// selector corruption bug: git separates --pretty=format records with a '\n',
// so every SHA after the first used to acquire a leading newline that then
// leaked into the selector label and split it across rows. SHAs and subjects
// must be clean (no embedded newlines, no leading/trailing whitespace).
func TestRecentCommits_MultiCommitNoNewlineLeak(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	for _, msg := range []string{"first", "second commit", "third"} {
		os.WriteFile(filepath.Join(dir, "a.txt"), []byte(msg), 0644)
		run(t, dir, "git", "add", ".")
		run(t, dir, "git", "commit", "-m", msg)
	}

	commits, err := RecentCommits(dir, 10, 80)
	if err != nil {
		t.Fatalf("RecentCommits failed: %v", err)
	}
	if len(commits) != 3 {
		t.Fatalf("expected 3 commits, got %d", len(commits))
	}
	wantSubjects := []string{"third", "second commit", "first"}
	for i, c := range commits {
		if strings.ContainsAny(c.SHA, "\n\r") {
			t.Errorf("commit[%d] SHA has embedded newline: %q", i, c.SHA)
		}
		if strings.ContainsAny(c.Subject, "\n\r") {
			t.Errorf("commit[%d] subject has embedded newline: %q", i, c.Subject)
		}
		if c.SHA != strings.TrimSpace(c.SHA) {
			t.Errorf("commit[%d] SHA has surrounding whitespace: %q", i, c.SHA)
		}
		if c.Subject != wantSubjects[i] {
			t.Errorf("commit[%d] subject = %q, want %q", i, c.Subject, wantSubjects[i])
		}
	}
}

func contains(s string, subs ...string) bool {
	for _, sub := range subs {
		if !containsOne(s, sub) {
			return false
		}
	}
	return true
}

func containsOne(s, sub string) bool {
	return len(sub) == 0 || func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	}()
}
