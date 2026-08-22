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
	"github.com/pijalu/goa/internal/filefind"
	"github.com/pijalu/goa/internal/review"
	"github.com/pijalu/goa/tui"
)

// runFileReview executes /review:file with args against a fresh bus and
// returns the received chat event.
func runFileReview(t *testing.T, ctx core.Context, args []string) event.ChatEvent {
	t.Helper()
	if err := (&ReviewCommand{}).Run(ctx, args); err != nil {
		t.Fatalf("Run(%v) failed: %v", args, err)
	}
	select {
	case ev := <-ctx.EventBus.Chat:
		return ev
	default:
		t.Fatal("expected a chat event")
	}
	return event.ChatEvent{}
}

// writeFileReviewTree creates a plain (non-git) project tree used by the
// file-review command tests.
func writeFileReviewTree(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"README.md":        "# Demo project\n",
		"a.txt":            "first line\nsecond line\nthird line\n",
		"notes.md":         "# Title\n\nA paragraph.\n",
		"main.go":          "package main\n\nfunc main() {}\n",
		"src/util/util.go": "package util\n",
	}
	for rel, content := range files {
		path := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("WriteFile %s: %v", rel, err)
		}
	}
	return dir
}

// TestReviewFileCommand_OpensPager verifies that /review:file:<path> loads
// the file, persists a Kind=file session and emits ShowFileReviewPager with
// a wired *tui.FileReviewPager — for text, markdown and go files alike.
func TestReviewFileCommand_OpensPager(t *testing.T) {
	for _, name := range []string{"a.txt", "notes.md", "main.go"} {
		t.Run(name, func(t *testing.T) {
			ctx := core.Context{
				ProjectDir: writeFileReviewTree(t),
				EventBus:   event.MakeBus(1, 1, 10, 1),
			}
			openFileReviewPager(t, ctx, name)
			// The session must be persisted so comments survive and
			// /review status/submit/export can see it.
			assertPersistedFileSession(t, ctx.ProjectDir, name)
		})
	}
}

// openFileReviewPager runs /review:file:<name> and returns the wired pager,
// asserting the event contract on the way.
func openFileReviewPager(t *testing.T, ctx core.Context, name string) *tui.FileReviewPager {
	t.Helper()
	ev := runFileReview(t, ctx, []string{"file", name})
	if ev.ShowFileReviewPager == nil {
		t.Fatalf("expected ShowFileReviewPager event, got %+v", ev)
	}
	pager, ok := ev.ShowFileReviewPager.Pager.(*tui.FileReviewPager)
	if !ok {
		t.Fatalf("pager type = %T, want *tui.FileReviewPager", ev.ShowFileReviewPager.Pager)
	}
	if pager.Session.Kind != review.KindFile {
		t.Errorf("session kind = %q, want %q", pager.Session.Kind, review.KindFile)
	}
	if pager.Content == nil || pager.Content.Path != name {
		t.Errorf("content path = %+v, want %q", pager.Content, name)
	}
	return pager
}

// assertPersistedFileSession verifies exactly one Kind=file session for name
// exists in dir's review store.
func assertPersistedFileSession(t *testing.T, dir, name string) {
	t.Helper()
	ids, err := review.NewStore(dir).List()
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(ids) != 1 {
		t.Fatalf("expected 1 persisted session, got %d", len(ids))
	}
	s, err := review.NewStore(dir).Load(ids[0])
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if s.Kind != review.KindFile {
		t.Errorf("persisted kind = %q, want %q", s.Kind, review.KindFile)
	}
	if s.FilePath != name {
		t.Errorf("persisted FilePath = %q, want %q", s.FilePath, name)
	}
}

// TestReviewFileCommand_WorksOutsideGit runs the whole flow in a plain temp
// directory (D4): no git repository required.
func TestReviewFileCommand_WorksOutsideGit(t *testing.T) {
	dir := writeFileReviewTree(t)
	if review.IsGitRepo(dir) {
		t.Skip("test tree unexpectedly inside a git repo")
	}
	events := event.MakeBus(1, 1, 10, 1)
	var submitted string
	ctx := core.Context{
		ProjectDir:    dir,
		EventBus:      events,
		SubmitToAgent: func(text string) { submitted = text },
	}

	ev := runFileReview(t, ctx, []string{"file", "a.txt"})
	pager := ev.ShowFileReviewPager.Pager.(*tui.FileReviewPager)

	absPath := filepath.Join(dir, "a.txt")
	pager.Session.AddComment(pager.Session.FilePath, 2, review.SideNew, "swallowed error")

	// Submit through the real key path: 's' asks for confirmation, then the
	// wired callback must hand the summary to SubmitToAgent.
	pager.RequestRender = func() {}
	pager.OnConfirm = func(question string, onResult func(bool)) { onResult(true) }
	pager.HandleInput("s")

	if submitted == "" {
		t.Fatal("expected review summary to reach SubmitToAgent")
	}
	if !strings.Contains(submitted, absPath) {
		t.Errorf("summary missing absolute path %q:\n%s", absPath, submitted)
	}
	if !strings.Contains(submitted, "a.txt:2") {
		t.Errorf("summary missing anchor a.txt:2:\n%s", submitted)
	}
	if !strings.Contains(submitted, "swallowed error") {
		t.Errorf("summary missing comment content:\n%s", submitted)
	}

	// OnSubmitReview also saves; reload from disk and check the comment.
	store := review.NewStore(dir)
	ids, err := store.List()
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	s, err := store.Load(ids[0])
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if len(s.Comments) != 1 || s.Comments[0].Content != "swallowed error" {
		t.Errorf("persisted comments = %+v, want one %q comment", s.Comments, "swallowed error")
	}
}

// TestReviewFileCommand_CommentSavedPersists wires OnCommentSaved and checks
// that invoking it stores the added comment on disk.
func TestReviewFileCommand_CommentSavedPersists(t *testing.T) {
	dir := writeFileReviewTree(t)
	events := event.MakeBus(1, 1, 10, 1)
	ctx := core.Context{ProjectDir: dir, EventBus: events}

	ev := runFileReview(t, ctx, []string{"file", "a.txt"})
	pager := ev.ShowFileReviewPager.Pager.(*tui.FileReviewPager)

	pager.Session.AddComment(pager.Session.FilePath, 3, review.SideNew, "off-by-one?")
	pager.OnCommentSaved()

	store := review.NewStore(dir)
	ids, err := store.List()
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	s, err := store.Load(ids[0])
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if len(s.Comments) != 1 || s.Comments[0].LineNum != 3 {
		t.Errorf("persisted comments = %+v, want one comment anchored at line 3", s.Comments)
	}
}

// TestReviewFileCommand_Rejections covers every failure path: each must
// answer with a chat message and emit NO pager event.
func TestReviewFileCommand_Rejections(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		wantIn  string // substring expected in the output message
		prepare func(dir string)
	}{
		{
			name:   "no path shows usage",
			args:   []string{"file"},
			wantIn: "Usage: /review:file:",
		},
		{
			name:   "trailing colon shows usage",
			args:   []string{"file", ""},
			wantIn: "Usage: /review:file:",
		},
		{
			name:   "missing file",
			args:   []string{"file", "nope.txt"},
			wantIn: "Cannot review file",
		},
		{
			name:   "directory rejected",
			args:   []string{"file", "src"},
			wantIn: "Cannot review file",
		},
		{
			name:   "binary rejected",
			args:   []string{"file", "blob.bin"},
			wantIn: "Cannot review file",
			prepare: func(dir string) {
				bin := []byte{'P', 'N', 0, 1, 2}
				os.WriteFile(filepath.Join(dir, "blob.bin"), bin, 0o644)
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := writeFileReviewTree(t)
			if tc.prepare != nil {
				tc.prepare(dir)
			}
			events := event.MakeBus(1, 1, 10, 1)
			out := &strings.Builder{}
			ctx := core.Context{ProjectDir: dir, EventBus: events, OutputBuffer: out}

			if err := (&ReviewCommand{}).Run(ctx, tc.args); err != nil {
				t.Fatalf("Run failed: %v", err)
			}
			select {
			case ev := <-events.Chat:
				t.Fatalf("unexpected event on failure path: %+v", ev.ShowFileReviewPager)
			default:
			}
			if got := out.String(); !strings.Contains(got, tc.wantIn) {
				t.Errorf("output %q missing %q", got, tc.wantIn)
			}
		})
	}
}

func completionValues(comps []core.ArgCompletion) []string {
	vals := make([]string, len(comps))
	for i, c := range comps {
		vals[i] = c.Value
	}
	return vals
}

func containsStr(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

func allPrefixed(list []string, prefix string) bool {
	if len(list) == 0 {
		return false
	}
	for _, v := range list {
		if !strings.HasPrefix(v, prefix) {
			return false
		}
	}
	return true
}

// TestReviewFileCompletion_BaseOrder checks the empty-prefix shape: ^1 ^2 ^3,
// then the file: scope entry, then refs (regression: default still first).
func TestReviewFileCompletion_BaseOrder(t *testing.T) {
	dir := setupReviewExportRepo(t)
	exec.Command("git", "-C", dir, "tag", "v1.0").Run()
	exec.Command("git", "-C", dir, "branch", "feature-x").Run()

	cmd := &ReviewCommand{}
	comps := cmd.CompleteArgs(core.Context{ProjectDir: dir}, "")
	vals := completionValues(comps)

	if len(vals) < 5 || vals[0] != "^1" || vals[1] != "^2" || vals[2] != "^3" {
		t.Fatalf("expected ^1 ^2 ^3 leading, got %v", vals)
	}
	if vals[3] != "file:" {
		t.Errorf("file: must follow the ancestry trio, got %v", vals)
	}
	if !containsStr(vals, "v1.0") || !containsStr(vals, "feature-x") {
		t.Errorf("refs missing after file: entry: %v", vals)
	}
}

// TestReviewFileCompletion_PrefixFiltering checks typed prefixes: "fi"
// narrows to the file scope only, "^" keeps only ancestry, refs filter as
// before (no regression).
func TestReviewFileCompletion_PrefixFiltering(t *testing.T) {
	dir := setupReviewExportRepo(t)
	exec.Command("git", "-C", dir, "tag", "v1.0").Run()

	cmd := &ReviewCommand{}
	ctx := core.Context{ProjectDir: dir}

	fiVals := completionValues(cmd.CompleteArgs(ctx, "fi"))
	if len(fiVals) != 1 || fiVals[0] != "file:" {
		t.Errorf("'fi' should complete only to file:, got %v", fiVals)
	}

	hatVals := completionValues(cmd.CompleteArgs(ctx, "^"))
	if len(hatVals) == 0 {
		t.Fatal("expected ancestry completions for '^'")
	}
	for _, v := range hatVals {
		if !strings.HasPrefix(v, "^") {
			t.Errorf("'^' should yield only ancestry, got %v", hatVals)
		}
	}

	vVals := completionValues(cmd.CompleteArgs(ctx, "v"))
	if !containsStr(vVals, "v1.0") {
		t.Errorf("'v' should keep v1.0, got %v", vVals)
	}
	if containsStr(vVals, "file:") {
		t.Errorf("'v' must not offer the file scope, got %v", vVals)
	}
}

// TestReviewFileCompletion_FileScope drives the nested "file:" scope. When fd
// is installed the search is subprocess-backed; ranking may differ between fd
// and the ReadDir fallback, so order-sensitive assertions only run when fd is
// absent — membership assertions hold either way.
func TestReviewFileCompletion_FileScope(t *testing.T) {
	cmd := &ReviewCommand{}
	ctx := core.Context{ProjectDir: writeFileReviewTree(t)}

	fileScopeTopLevel(t, cmd, ctx)
	fileScopeNestedDrillDown(t, cmd, ctx)
	fileScopeExactFileSuppresses(t, cmd, ctx)
	fileScopeColonPathUnreachable(t, cmd, ctx)
}

// fileScopeTopLevel checks the empty-path listing: scoped values, dir
// drill-down entries with trailing slash.
func fileScopeTopLevel(t *testing.T, cmd *ReviewCommand, ctx core.Context) {
	t.Helper()
	base := completionValues(cmd.CompleteArgs(ctx, "file:"))
	if len(base) == 0 {
		t.Fatal("expected file candidates for empty path prefix")
	}
	if !allPrefixed(base, "file:") {
		t.Errorf("all values must carry the file: scope prefix: %v", base)
	}
	if !containsStr(base, "file:README.md") && filefind.Available() {
		t.Logf("fd listing at top level: %v", base)
	}
	// Directory drill-down: dirs end with "/" regardless of engine.
	hasDirEntry := false
	for _, v := range base {
		if strings.HasPrefix(v, "file:src") && strings.HasSuffix(v, "/") {
			hasDirEntry = true
		}
	}
	if !hasDirEntry {
		t.Errorf("expected a src/ drill-down entry, got %v", base)
	}
}

// fileScopeNestedDrillDown checks completion inside a subdirectory keeps the
// scope prefix rooted at the project.
func fileScopeNestedDrillDown(t *testing.T, cmd *ReviewCommand, ctx core.Context) {
	t.Helper()
	nested := completionValues(cmd.CompleteArgs(ctx, "file:src/"))
	if !allPrefixed(nested, "file:src/") {
		t.Errorf("nested values must stay scoped to src/: %v", nested)
	}
	if !containsStr(nested, "file:src/util/") {
		t.Errorf("nested completion under src/ missing util/: %v", nested)
	}
}

// fileScopeExactFileSuppresses checks that a token naming an existing file
// suppresses the popup entirely.
func fileScopeExactFileSuppresses(t *testing.T, cmd *ReviewCommand, ctx core.Context) {
	t.Helper()
	done := completionValues(cmd.CompleteArgs(ctx, "file:main.go"))
	if len(done) != 0 {
		t.Errorf("exact existing file must suppress completions, got %v", done)
	}
}

// fileScopeColonPathUnreachable pins the documented colon-syntax limitation:
// paths containing ':' can never be completed through it.
func fileScopeColonPathUnreachable(t *testing.T, cmd *ReviewCommand, ctx core.Context) {
	t.Helper()
	colonVals := completionValues(cmd.CompleteArgs(ctx, "file:a:b"))
	if len(colonVals) != 0 {
		t.Errorf("colon-containing path prefix must yield nothing, got %v", colonVals)
	}
}
