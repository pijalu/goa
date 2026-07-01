// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/internal/event"
	"github.com/pijalu/goa/internal/review"
)

func initReviewGitRepo(t *testing.T, dir string) {
	t.Helper()
	cmd := exec.Command("git", "init")
	cmd.Dir = dir
	if err := cmd.Run(); err != nil {
		t.Fatalf("git init: %v", err)
	}
	exec.Command("git", "-C", dir, "config", "user.email", "t@t.com").Run()
	exec.Command("git", "-C", dir, "config", "user.name", "T").Run()
}

func TestReviewCommand_NonGit(t *testing.T) {
	ctx := core.Context{ProjectDir: t.TempDir()}
	cmd := &ReviewCommand{}
	if err := cmd.Run(ctx, nil); err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	// No crash is enough; output is written via writeStr which needs a writer.
}

func TestReviewCommand_ListCommits(t *testing.T) {
	dir := t.TempDir()
	initReviewGitRepo(t, dir)
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello\n"), 0644)
	exec.Command("git", "-C", dir, "add", ".").Run()
	exec.Command("git", "-C", dir, "commit", "-m", "first").Run()

	ctx := core.Context{ProjectDir: dir}
	cmd := &ReviewCommand{}
	if err := cmd.Run(ctx, []string{"list"}); err != nil {
		t.Fatalf("Run failed: %v", err)
	}
}

func TestReviewCommand_StartReview(t *testing.T) {
	dir := t.TempDir()
	initReviewGitRepo(t, dir)
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello\n"), 0644)
	exec.Command("git", "-C", dir, "add", ".").Run()
	exec.Command("git", "-C", dir, "commit", "-m", "first").Run()

	var submitted string
	events := event.MakeBus(1, 1, 10, 1)
	ctx := core.Context{
		ProjectDir: dir,
		EventBus:   events,
		RequestMainInput: func(prompt string, cb func(string)) {
			cb("ok")
		},
		SubmitToAgent: func(text string) {
			submitted = text
		},
	}
	cmd := &ReviewCommand{}
	if err := cmd.Run(ctx, nil); err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	select {
	case ev := <-events.Chat:
		if ev.ShowReviewPager == nil {
			t.Fatal("expected ShowReviewPager event")
		}
	default:
		t.Fatal("expected chat event")
	}

	// Verify session persisted.
	store := review.NewStore(dir)
	ids, err := store.List()
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(ids) != 1 {
		t.Fatalf("expected 1 review session, got %d", len(ids))
	}

	// Submit should send the review to the agent.
	if err := cmd.Run(ctx, []string{"submit"}); err != nil {
		t.Fatalf("submit failed: %v", err)
	}
	if submitted == "" {
		t.Error("expected review submitted to agent")
	}
	if !strings.Contains(submitted, "# Code Review") {
		t.Errorf("unexpected submitted text: %s", submitted)
	}
}

func TestReviewCommand_Export(t *testing.T) {
	dir := setupReviewExportRepo(t)
	store := review.NewStore(dir)
	session := &review.Session{ID: "abc12345", BaseRef: "HEAD^1", HeadRef: "def", ProjectDir: dir}
	session.AddComment("a.txt", 1, "why change?")
	if err := store.Save(session); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	ctx := core.Context{ProjectDir: dir}
	cmd := &ReviewCommand{}
	if err := cmd.Run(ctx, []string{"export"}); err != nil {
		t.Fatalf("export failed: %v", err)
	}

	path := findReviewExportFile(t, dir)
	content := readFileString(t, path)
	if !strings.Contains(content, "why change?") {
		t.Errorf("expected comment in export, got:\n%s", content)
	}
	if !strings.Contains(content, "```diff") {
		t.Errorf("expected diff block in export, got:\n%s", content)
	}
}

func setupReviewExportRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	initReviewGitRepo(t, dir)
	writeCommit(t, dir, "a.txt", "hello\n", "first")
	writeCommit(t, dir, "a.txt", "world\n", "second")
	return dir
}

func writeCommit(t *testing.T, dir, file, content, msg string) {
	t.Helper()
	os.WriteFile(filepath.Join(dir, file), []byte(content), 0644)
	exec.Command("git", "-C", dir, "add", ".").Run()
	exec.Command("git", "-C", dir, "commit", "-m", msg).Run()
}

func findReviewExportFile(t *testing.T, dir string) string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir failed: %v", err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "review_") && strings.HasSuffix(e.Name(), ".md") {
			return filepath.Join(dir, e.Name())
		}
	}
	t.Fatal("expected export file not created")
	return ""
}

func readFileString(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	return string(data)
}
