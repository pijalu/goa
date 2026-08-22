// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package review

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
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

// TestNewFileSession_Gitless verifies the file-session constructor needs no
// git repository and normalizes FilePath to the anchor path: project-relative
// when inside the project, absolute otherwise.
func TestNewFileSession_Gitless(t *testing.T) {
	dir := t.TempDir() // deliberately not a git repo

	s := newFileSessionMust(t, dir, filepath.Join("src", "main.go"))
	if s.Kind != KindFile {
		t.Errorf("Kind = %q, want %q", s.Kind, KindFile)
	}
	if s.ProjectDir != dir {
		t.Errorf("ProjectDir = %q, want %q", s.ProjectDir, dir)
	}
	if want := filepath.Join("src", "main.go"); s.FilePath != want {
		t.Errorf("FilePath = %q, want %q", s.FilePath, want)
	}
	if s.ID == "" || s.CreatedAt.IsZero() {
		t.Error("expected ID and CreatedAt to be set")
	}

	// Absolute path inside the project collapses to project-relative.
	absInside := filepath.Join(dir, "src", "main.go")
	s2 := newFileSessionMust(t, dir, absInside)
	if want := filepath.Join("src", "main.go"); s2.FilePath != want {
		t.Errorf("abs-inside FilePath = %q, want %q", s2.FilePath, want)
	}

	// Absolute path outside the project stays absolute.
	outside := filepath.Join(t.TempDir(), "elsewhere.txt")
	s3 := newFileSessionMust(t, dir, outside)
	if s3.FilePath != outside {
		t.Errorf("abs-outside FilePath = %q, want %q", s3.FilePath, outside)
	}
}

// TestNewFileSession_ArgValidation pins the constructor's argument checks:
// both an empty file path and an empty project dir must be rejected.
func TestNewFileSession_ArgValidation(t *testing.T) {
	if _, err := NewFileSession(t.TempDir(), ""); err == nil {
		t.Error("expected error for empty file path")
	}
	if _, err := NewFileSession("", "a.txt"); err == nil {
		t.Error("expected error for empty project dir")
	}
}

// newFileSessionMust constructs a file session, failing the test on error.
func newFileSessionMust(t *testing.T, projectDir, path string) *Session {
	t.Helper()
	s, err := NewFileSession(projectDir, path)
	if err != nil {
		t.Fatalf("NewFileSession(%q, %q) failed: %v", projectDir, path, err)
	}
	return s
}

// TestSessionJSON_LegacyLoadsAsDiff pins the D3 JSON contract: sessions
// stored before Kind existed have no "kind" field and must decode as diff
// reviews; file sessions round-trip their kind and anchor path.
func TestSessionJSON_LegacyLoadsAsDiff(t *testing.T) {
	legacy := `{"id":"ab12","project_dir":"/p","base_ref":"HEAD^1","head_ref":"cafe","dirty":true,` +
		`"created_at":"2026-01-02T03:04:05Z","comments":[{"id":"c1","file":"a.go","line_num":7,` +
		`"side":"","content":"hi","created_at":"2026-01-02T03:04:05Z","updated_at":"2026-01-02T03:04:05Z"}]}`
	var s Session
	if err := json.Unmarshal([]byte(legacy), &s); err != nil {
		t.Fatalf("unmarshal legacy session JSON: %v", err)
	}
	if s.Kind != KindDiff {
		t.Errorf("legacy Kind = %q, want zero value %q", s.Kind, KindDiff)
	}
	if s.FilePath != "" {
		t.Errorf("legacy FilePath = %q, want empty", s.FilePath)
	}
	if s.BaseRef != "HEAD^1" || !s.Dirty || len(s.Comments) != 1 {
		t.Error("legacy fields not decoded as before")
	}

	// File session JSON round-trip keeps kind and anchor path.
	f := &Session{
		ID: "f1", ProjectDir: "/p", Kind: KindFile, FilePath: "src/main.go",
		CreatedAt: time.Unix(0, 0).UTC(),
		Comments: []Comment{{ID: "c9", File: "src/main.go", LineNum: 3,
			Side: SideNew, Content: "note"}},
	}
	data, err := json.Marshal(f)
	if err != nil {
		t.Fatalf("marshal file session: %v", err)
	}
	if !strings.Contains(string(data), `"kind":"file"`) {
		t.Errorf("expected kind field in JSON, got %s", data)
	}
	var back Session
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatalf("round-trip unmarshal: %v", err)
	}
	if back.Kind != KindFile || back.FilePath != "src/main.go" {
		t.Errorf("round-trip Kind/FilePath = %q/%q, want file/src/main.go", back.Kind, back.FilePath)
	}
}

// TestStore_FileSessionRoundTrip pins that the real store persists the new
// fields transparently.
func TestStore_FileSessionRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s, err := NewFileSession(dir, "app.go")
	if err != nil {
		t.Fatalf("NewFileSession failed: %v", err)
	}
	s.AddComment("app.go", 2, SideOld, "coerced to new side")

	st := NewStore(dir)
	if err := st.Save(s); err != nil {
		t.Fatalf("Save failed: %v", err)
	}
	got, err := st.Load(s.ID)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if got.Kind != KindFile || got.FilePath != "app.go" {
		t.Errorf("stored Kind/FilePath = %q/%q, want file/app.go", got.Kind, got.FilePath)
	}
	if len(got.Comments) != 1 || got.Comments[0].Side != SideNew {
		t.Errorf("stored comments = %+v, want one SideNew comment", got.Comments)
	}
}

// TestSession_AddComment_FileForcesSideNew verifies file-kind comments always
// attach SideNew regardless of the side passed in: a reviewed file has one
// coordinate space (D3).
func TestSession_AddComment_FileForcesSideNew(t *testing.T) {
	s := &Session{ID: "f", ProjectDir: "/p", Kind: KindFile, FilePath: "a.md"}
	c := s.AddComment("a.md", 5, SideOld, "typo")
	if c.Side != SideNew {
		t.Errorf("Side = %q, want %q", c.Side, SideNew)
	}
	if got := s.CommentsFor("a.md", 5, SideNew); len(got) != 1 {
		t.Fatalf("CommentsFor(SideNew) = %d, want 1", len(got))
	}
	if got := s.CommentsFor("a.md", 5, SideOld); len(got) != 0 {
		t.Errorf("CommentsFor(SideOld) = %d, want 0", len(got))
	}
}

// TestMarkdownSummary_FileKind checks the UX-spec shape: title, absolute file
// bullet, lines-reviewed count with truncation marker, guidance paragraph,
// and no diff-review leftovers.
func TestMarkdownSummary_FileKind(t *testing.T) {
	dir := t.TempDir()
	body := "package main\n\nfunc main() {}\n"
	if err := os.WriteFile(filepath.Join(dir, "app.go"), []byte(body), 0644); err != nil {
		t.Fatal(err)
	}

	s := newFileSessionMust(t, dir, "app.go")
	out := s.MarkdownSummary()
	if !strings.Contains(out, "# File Review\n") {
		t.Errorf("expected file title, got:\n%s", out)
	}
	wantAbs := filepath.Join(dir, "app.go")
	if !strings.Contains(out, "**File:** "+wantAbs+"\n") {
		t.Errorf("expected absolute path bullet %q, got:\n%s", wantAbs, out)
	}
	if !strings.Contains(out, "**Lines reviewed:** 3\n") {
		t.Errorf("expected lines-reviewed bullet, got:\n%s", out)
	}
	if strings.Contains(out, "(truncated)") {
		t.Errorf("unexpected truncation marker:\n%s", out)
	}
	if !strings.Contains(out, "Read the file to see each comment in context") {
		t.Errorf("expected guidance paragraph, got:\n%s", out)
	}
	if !strings.Contains(out, "No comments yet.") {
		t.Errorf("expected empty-comment note, got:\n%s", out)
	}
	// File variant must not leak diff-review content.
	if strings.Contains(out, "git diff") || strings.Contains(out, "**Base:**") {
		t.Errorf("file summary embeds diff review fields:\n%s", out)
	}
}

// TestMarkdownSummary_FileKind_WithComments checks the commented shape:
// anchors are project-relative file:line; passing SideOld must not leak into
// the label (coerced to SideNew).
func TestMarkdownSummary_FileKind_WithComments(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "app.go"), []byte("package main\n"), 0644); err != nil {
		t.Fatal(err)
	}

	s := newFileSessionMust(t, dir, "app.go")
	s.AddComment("app.go", 3, SideOld, "check error handling")
	out := s.MarkdownSummary()
	if !strings.Contains(out, "`app.go:3`: check error handling") {
		t.Errorf("expected rel:line anchor bullet, got:\n%s", out)
	}
	if strings.Contains(out, "(removed)") {
		t.Errorf("file comment must not carry removed marker:\n%s", out)
	}
	if strings.Contains(out, "No comments yet.") {
		t.Errorf("stale empty-comment note:\n%s", out)
	}
}

// The lines-reviewed bullet gains the "(truncated)" marker when either cap
// was hit at load time (here: the line cap).
func TestMarkdownSummary_FileKind_Truncated(t *testing.T) {
	dir := t.TempDir()
	var b strings.Builder
	for i := 0; i < maxReviewFileLines+1; i++ {
		b.WriteString("line\n")
	}
	if err := os.WriteFile(filepath.Join(dir, "big.txt"), []byte(b.String()), 0644); err != nil {
		t.Fatal(err)
	}

	s, err := NewFileSession(dir, "big.txt")
	if err != nil {
		t.Fatalf("NewFileSession failed: %v", err)
	}
	out := s.MarkdownSummary()
	want := "**Lines reviewed:** " + strconv.Itoa(maxReviewFileLines) + " (truncated)\n"
	if !strings.Contains(out, want) {
		t.Errorf("expected %q bullet, got:\n%s", want, out)
	}
}

// If the file disappears after the review started, submit/export still work:
// the summary degrades gracefully instead of failing.
func TestMarkdownSummary_FileKind_FileVanished(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gone.txt")
	if err := os.WriteFile(path, []byte("x\n"), 0644); err != nil {
		t.Fatal(err)
	}
	s, err := NewFileSession(dir, "gone.txt")
	if err != nil {
		t.Fatalf("NewFileSession failed: %v", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	out := s.MarkdownSummary()
	if !strings.Contains(out, "# File Review") || !strings.Contains(out, "**File:** "+path) {
		t.Errorf("summary should survive vanished file, got:\n%s", out)
	}
	if strings.Contains(out, "Lines reviewed") {
		t.Errorf("lines bullet should be dropped for vanished file:\n%s", out)
	}
}

// ExportPath for file sessions names exports
// review_file_<sanitized-base>_<UTC-ts>.md under projectDir.
func TestExportPath_FileKind(t *testing.T) {
	dir := t.TempDir()
	s := &Session{ID: "f", ProjectDir: dir, Kind: KindFile, FilePath: filepath.Join("src", "main.go")}

	path, err := s.ExportPath(dir)
	if err != nil {
		t.Fatalf("ExportPath failed: %v", err)
	}
	name := filepath.Base(path)
	re := regexp.MustCompile(`^review_file_main-go_\d{4}-\d{2}-\d{2}T\d{2}-\d{2}-\d{2}\.md$`)
	if !re.MatchString(name) {
		t.Errorf("export name = %q, want review_file_main-go_<UTC-ts>.md", name)
	}
	if filepath.Dir(path) != dir {
		t.Errorf("export dir = %q, want %q", filepath.Dir(path), dir)
	}

	// Hostile/pathological base names stay sanitized and capped.
	s.FilePath = strings.Repeat("a", 60) + ".go"
	path, err = s.ExportPath(dir)
	if err != nil {
		t.Fatalf("ExportPath(long) failed: %v", err)
	}
	if n := len(filepath.Base(path)); n > 80 {
		t.Errorf("long export name length = %d, want <= 80 (%q)", n, filepath.Base(path))
	}
}
