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
	"github.com/pijalu/goa/tui"
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

// TestReviewCommand_StartReviewWithRef verifies that /review:<ref> creates a
// session whose BaseRef is the given tag/branch/ancestor, so the diff and the
// submit pointer both target that checkpoint.
func TestReviewCommand_StartReviewWithRef(t *testing.T) {
	dir := setupReviewExportRepo(t)
	exec.Command("git", "-C", dir, "tag", "v1.0").Run()

	events := event.MakeBus(1, 1, 10, 1)
	ctx := core.Context{ProjectDir: dir, EventBus: events}
	cmd := &ReviewCommand{}
	if err := cmd.Run(ctx, []string{"v1.0"}); err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	select {
	case ev := <-events.Chat:
		if ev.ShowReviewPager == nil {
			t.Fatal("expected ShowReviewPager event")
		}
		pager, ok := ev.ShowReviewPager.Pager.(*tui.ReviewPager)
		if !ok {
			t.Fatalf("pager type = %T, want *tui.ReviewPager", ev.ShowReviewPager.Pager)
		}
		if got := pager.Session.BaseRef; got != "v1.0" {
			t.Errorf("BaseRef = %q, want %q", got, "v1.0")
		}
	default:
		t.Fatal("expected chat event")
	}

	// The persisted session must carry the ref so /review submit points at it.
	store := review.NewStore(dir)
	ids, err := store.List()
	if err != nil || len(ids) != 1 {
		t.Fatalf("expected 1 session, ids=%v err=%v", ids, err)
	}
	s, err := store.Load(ids[0])
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if s.BaseRef != "v1.0" {
		t.Errorf("persisted BaseRef = %q, want %q", s.BaseRef, "v1.0")
	}
	if !strings.Contains(s.MarkdownSummary(), "git diff v1.0..HEAD") {
		t.Errorf("summary should point at the tag diff command, got:\n%s", s.MarkdownSummary())
	}
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
	session.AddComment("a.txt", 1, review.SideNew, "why change?")
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
	// The export must point to the diff command, not embed diff content.
	if !strings.Contains(content, "git diff HEAD^1..HEAD") {
		t.Errorf("expected diff command pointer in export, got:\n%s", content)
	}
	if strings.Contains(content, "```diff") {
		t.Errorf("export must not embed a diff code block, got:\n%s", content)
	}
}

func TestReviewCommand_CompleteArgs_DefaultFirst(t *testing.T) {
	dir := setupReviewExportRepo(t) // two commits on the default branch
	exec.Command("git", "-C", dir, "tag", "v1.0").Run()
	exec.Command("git", "-C", dir, "branch", "feature-x").Run()

	cmd := &ReviewCommand{}
	ctx := core.Context{ProjectDir: dir}

	// Empty prefix: ^1 default must lead, followed by ancestry and recent refs.
	comps := cmd.CompleteArgs(ctx, "")
	if len(comps) == 0 {
		t.Fatal("expected completions")
	}
	if comps[0].Value != "^1" {
		t.Errorf("first completion = %q, want default %q", comps[0].Value, "^1")
	}
	var values []string
	for _, c := range comps {
		values = append(values, c.Value)
	}
	if !strings.Contains(strings.Join(values, " "), "v1.0") {
		t.Errorf("expected tag v1.0 in completions: %v", values)
	}
	if !strings.Contains(strings.Join(values, " "), "feature-x") {
		t.Errorf("expected branch feature-x in completions: %v", values)
	}
}

func TestReviewCommand_CompleteArgs_PrefixFilters(t *testing.T) {
	dir := setupReviewExportRepo(t)
	exec.Command("git", "-C", dir, "tag", "v1.0").Run()
	exec.Command("git", "-C", dir, "branch", "feature-x").Run()

	cmd := &ReviewCommand{}
	ctx := core.Context{ProjectDir: dir}

	// Typing "v" filters to prefix-matching refs only (drops ancestry + branch).
	comps := cmd.CompleteArgs(ctx, "v")
	var values []string
	for _, c := range comps {
		values = append(values, c.Value)
	}
	if !strings.Contains(strings.Join(values, " "), "v1.0") {
		t.Errorf("expected v1.0 for prefix 'v': %v", values)
	}
	for _, v := range values {
		if v == "^1" || v == "feature-x" {
			t.Errorf("prefix 'v' should filter out %q: %v", v, values)
		}
	}

	// Typing "^" keeps only ancestry suggestions.
	comps = cmd.CompleteArgs(ctx, "^")
	if len(comps) == 0 {
		t.Fatal("expected ancestry completions for prefix '^'")
	}
	for _, c := range comps {
		if !strings.HasPrefix(c.Value, "^") {
			t.Errorf("prefix '^' should only yield ancestry, got %q", c.Value)
		}
	}
}

// TestReviewCommand_CompletionShapeForTUI verifies the values plug into the
// editor's "/cmd:arg" expansion: after the user types "/review:", the first
// candidate must be "/review:^1" so the default base is the leading ghost.
func TestReviewCommand_CompletionShapeForTUI(t *testing.T) {
	dir := setupReviewExportRepo(t)
	cmd := &ReviewCommand{}
	ctx := core.Context{ProjectDir: dir}

	comps := cmd.CompleteArgs(ctx, "")
	if len(comps) == 0 {
		t.Fatal("expected completions")
	}
	// Mirror tui/autocomplete.go: Value = cmdName + ":" + completion.Value.
	first := "/review:" + comps[0].Value
	if first != "/review:^1" {
		t.Errorf("first expanded completion = %q, want %q", first, "/review:^1")
	}
}

func TestReviewCommand_CompleteArgs_NonGit(t *testing.T) {
	cmd := &ReviewCommand{}
	// Non-git dir: ancestry defaults still offered, no refs, no crash.
	comps := cmd.CompleteArgs(core.Context{ProjectDir: t.TempDir()}, "")
	if len(comps) == 0 || comps[0].Value != "^1" {
		t.Errorf("expected ^1 default even outside git, got %v", comps)
	}
}

// TestReviewCommand_CompleteArgs_NestedNonFileIsEmpty is the perf regression
// test for slow /review typing: the TUI's expandArg re-queries CompleteArgs
// with "<value>:" appended for every level-1 value (^1:, v1.0:, ...).
// /review has no nested scopes outside file:, so those probes must answer
// empty — and, critically, without spawning git (the old code ran IsGitRepo
// + for-each-ref per probe, ~40 processes per keystroke while typing /review).
// A fake git on PATH counts spawns; nested prefixes must produce zero.
func TestReviewCommand_CompleteArgs_NestedNonFileIsEmpty(t *testing.T) {
	dir := setupReviewExportRepo(t)
	exec.Command("git", "-C", dir, "tag", "v1.0").Run()
	log := installGitSpy(t)

	cmd := &ReviewCommand{}
	ctx := core.Context{ProjectDir: dir}

	for _, prefix := range []string{"^1:", "^2:", "v1.0:", "file:x:"} {
		if comps := cmd.CompleteArgs(ctx, prefix); len(comps) != 0 {
			t.Errorf("CompleteArgs(%q) = %v, want empty (no nested scope)", prefix, comps)
		}
	}
	if n := countGitSpawns(t, log); n != 0 {
		t.Errorf("nested CompleteArgs spawned git %d times, want 0", n)
	}

	// file: scope still completes paths, not empty.
	clearGitSpy(t, log)
	if comps := cmd.CompleteArgs(ctx, "file:"); len(comps) == 0 {
		t.Error("CompleteArgs(file:) should still propose file paths")
	}
}

// installGitSpy shadows git with a logging shim that always fails, so any
// subprocess spawn is recorded without needing a real repository. Returns
// the path of the spawn log. Callers must set up real git state BEFORE
// installing the spy.
func installGitSpy(t *testing.T) string {
	t.Helper()
	shimDir := t.TempDir()
	log := shimDir + "/spawns.log"
	shim := "#!/bin/sh\necho \"$@\" >> \"" + log + "\"\nexit 1\n"
	if err := os.WriteFile(shimDir+"/git", []byte(shim), 0755); err != nil {
		t.Fatalf("write git shim: %v", err)
	}
	t.Setenv("PATH", shimDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return log
}

func countGitSpawns(t *testing.T, log string) int {
	t.Helper()
	data, err := os.ReadFile(log)
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatalf("read spawn log: %v", err)
	}
	n := 0
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) != "" {
			n++
		}
	}
	return n
}

func clearGitSpy(t *testing.T, log string) {
	t.Helper()
	if err := os.Remove(log); err != nil && !os.IsNotExist(err) {
		t.Fatalf("clear spawn log: %v", err)
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
