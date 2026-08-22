// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package review

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Comment is a user note attached to a specific diff line.
//
// The attachment point is (File, LineNum, Side): a bare line number is
// ambiguous because old and new files use different coordinate spaces, so
// Side records which numbering LineNum belongs to. Comments persisted before
// Side existed have Side == "" and are treated as SideNew (the common case:
// notes on added/context lines).
type Comment struct {
	ID        string    `json:"id"`
	File      string    `json:"file"`
	LineNum   int       `json:"line_num"`
	Side      Side      `json:"side"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// anchor returns the comment's attachment point with the legacy empty side
// normalized to SideNew.
func (c Comment) anchor() LineAnchor {
	side := c.Side
	if side == "" {
		side = SideNew
	}
	return LineAnchor{File: c.File, LineNum: c.LineNum, Side: side}
}

// AnchorLabel returns a human-readable position such as "main.go:12" or,
// for comments on removed lines, "main.go:12 (removed)".
func (c Comment) AnchorLabel() string {
	a := c.anchor()
	label := fmt.Sprintf("%s:%d", a.File, a.LineNum)
	if a.Side == SideOld {
		label += " (removed)"
	}
	return label
}

// Kind distinguishes what a Session reviews. The zero value is KindDiff so
// every stored legacy session (whose JSON has no "kind" field) decodes as a
// diff review unchanged.
type Kind string

const (
	// KindDiff is a git diff review (base..head). It is the zero value of
	// Kind, which keeps stored sessions JSON-compatible.
	KindDiff Kind = ""
	// KindFile is a single-file review; it does not involve git at all.
	KindFile Kind = "file"
)

// Session tracks an in-progress code review.
type Session struct {
	ID         string    `json:"id"`
	ProjectDir string    `json:"project_dir"`
	BaseRef    string    `json:"base_ref"`
	HeadRef    string    `json:"head_ref"`
	Dirty      bool      `json:"dirty"`
	Kind       Kind      `json:"kind,omitempty"`      // zero value = diff review
	FilePath   string    `json:"file_path,omitempty"` // anchor path: project-relative when inside the project, else absolute
	CreatedAt  time.Time `json:"created_at"`
	Comments   []Comment `json:"comments"`
}

// NewSession creates a review session after resolving the default base ref
// and current HEAD.
func NewSession(projectDir string) (*Session, error) {
	if !IsGitRepo(projectDir) {
		return nil, fmt.Errorf("not a git repository: %s", projectDir)
	}
	root, err := ProjectRoot(projectDir)
	if err != nil {
		return nil, err
	}
	baseRef, err := DefaultBase(root)
	if err != nil {
		return nil, err
	}
	headRef, err := HeadSHA(root)
	if err != nil {
		return nil, err
	}
	dirty, err := HasUncommittedChanges(root)
	if err != nil {
		return nil, err
	}
	return &Session{
		ID:         generateID(),
		ProjectDir: root,
		BaseRef:    baseRef,
		HeadRef:    headRef,
		Dirty:      dirty,
		CreatedAt:  time.Now(),
		Comments:   nil,
	}, nil
}

// NewFileSession creates a single-file review session. Unlike NewSession it
// requires no git repository: the file itself is the review subject. It does
// not read the file — loading and validation are LoadReviewFile's job (the
// caller needs that content for the pager anyway). filePath may be absolute
// or project-relative; FilePath stores the anchor path (project-relative when
// the file lives inside projectDir, else absolute) so comment anchors stay
// stable regardless of how the path was spelled.
func NewFileSession(projectDir, filePath string) (*Session, error) {
	if projectDir == "" {
		return nil, fmt.Errorf("project dir is required")
	}
	if filePath == "" {
		return nil, fmt.Errorf("file path is required")
	}
	return &Session{
		ID:         generateID(),
		ProjectDir: projectDir,
		Kind:       KindFile,
		FilePath:   anchorPath(projectDir, filePath),
		CreatedAt:  time.Now(),
	}, nil
}

// anchorPath returns the canonical anchor path for a reviewed file: the
// path relative to projectDir when it resolves inside the project, else the
// cleaned absolute path.
func anchorPath(projectDir, path string) string {
	if filepath.IsAbs(path) {
		if rel, err := filepath.Rel(projectDir, path); err == nil &&
			rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return rel
		}
		return filepath.Clean(path)
	}
	return filepath.Clean(path)
}

// fileAbsPath returns the absolute location of the reviewed file: an
// already-absolute FilePath passes through; a relative one resolves against
// ProjectDir.
func (s *Session) fileAbsPath() string {
	if filepath.IsAbs(s.FilePath) {
		return s.FilePath
	}
	return filepath.Join(s.ProjectDir, s.FilePath)
}

// AddComment appends a new comment to the session. The side identifies
// which diff coordinate space lineNum belongs to (SideNew for added/context
// lines, SideOld for removed lines). File-kind sessions have a single
// coordinate space, so their comments always attach to SideNew regardless of
// the side passed in (D3).
func (s *Session) AddComment(file string, lineNum int, side Side, content string) Comment {
	if s.Kind == KindFile {
		side = SideNew
	}
	c := Comment{
		ID:        generateID(),
		File:      file,
		LineNum:   lineNum,
		Side:      side,
		Content:   content,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	s.Comments = append(s.Comments, c)
	return c
}

// UpdateComment updates an existing comment by ID.
func (s *Session) UpdateComment(id, content string) (Comment, bool) {
	for i := range s.Comments {
		if s.Comments[i].ID == id {
			s.Comments[i].Content = content
			s.Comments[i].UpdatedAt = time.Now()
			return s.Comments[i], true
		}
	}
	return Comment{}, false
}

// RemoveComment deletes a comment by ID.
func (s *Session) RemoveComment(id string) bool {
	for i := range s.Comments {
		if s.Comments[i].ID == id {
			s.Comments = append(s.Comments[:i], s.Comments[i+1:]...)
			return true
		}
	}
	return false
}

// CommentsFor returns comments attached to a specific file, line, and diff
// side. The side must match exactly (after legacy normalization): a comment
// on new line N never matches a removed line whose old number happens to be
// N, and vice versa.
func (s *Session) CommentsFor(file string, lineNum int, side Side) []Comment {
	want := LineAnchor{File: file, LineNum: lineNum, Side: side}
	var out []Comment
	for _, c := range s.Comments {
		if c.anchor() == want {
			out = append(out, c)
		}
	}
	return out
}

// MarkdownSummary returns a Markdown formatted review summary intended for
// the LLM and for human readers. For diff sessions it contains the base/head
// refs and the review comments. It deliberately does NOT embed any diff
// content: diffs can be huge and would bloat the agent's context. Instead it
// points to the exact command that produces the diff under review, so the
// agent can inspect the changes itself (run the command, or read the
// referenced files at the comment anchors).
func (s *Session) MarkdownSummary() string {
	if s.Kind == KindFile {
		return s.fileMarkdownSummary()
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# Code Review\n\n")
	fmt.Fprintf(&b, "- **Base:** %s\n", s.BaseRef)
	fmt.Fprintf(&b, "- **Head:** %s\n", s.HeadRef)
	fmt.Fprintf(&b, "- **Dirty:** %v\n\n", s.Dirty)

	// Point to the diff source instead of embedding the diff. DiffCommand
	// mirrors DiffArgs exactly, so this command reproduces the reviewed diff.
	fmt.Fprintf(&b, "The changes under review are produced by `%s` (run from the project root). "+
		"Do not expect the diff inline; run that command or read the referenced files to see them.\n\n",
		DiffCommand(s.BaseRef))

	return s.appendComments(&b)
}

// fileMarkdownSummary renders the single-file review summary: the absolute
// path (the "link" — read tools resolve absolute paths directly), how much of
// the file was reviewed, and the comments anchored to project-relative paths.
func (s *Session) fileMarkdownSummary() string {
	var b strings.Builder
	fmt.Fprintf(&b, "# File Review\n\n")
	fmt.Fprintf(&b, "- **File:** %s\n", s.fileAbsPath())

	// Reload to report how much was reviewed. If the file disappeared since
	// the review started, degrade gracefully and keep the summary usable.
	if content, err := LoadReviewFile(s.ProjectDir, s.FilePath); err == nil {
		lineCount := fmt.Sprintf("- **Lines reviewed:** %d", len(content.Lines))
		if content.Truncated {
			lineCount += " (truncated)"
		}
		fmt.Fprintf(&b, "%s\n", lineCount)
	}

	fmt.Fprintf(&b, "\nRead the file to see each comment in context. Comments are anchored to the "+
		"line numbers of that file.\n\n")

	return s.appendComments(&b)
}

// appendComments writes the shared comment block: either "No comments yet."
// or a "## Comments" section with one bullet per comment. The bullets use
// AnchorLabel() so removed-line comments stay distinguishable from new-file
// numbering in diff reviews; in file reviews SideNew labels are plain
// "file:line".
func (s *Session) appendComments(b *strings.Builder) string {
	if len(s.Comments) == 0 {
		b.WriteString("No comments yet.\n")
		return b.String()
	}

	b.WriteString("## Comments\n\n")
	for _, c := range s.Comments {
		fmt.Fprintf(b, "- `%s`: %s\n", c.AnchorLabel(), c.Content)
	}
	return b.String()
}

// Export writes the Markdown review summary to path.
func (s *Session) Export(path string) error {
	if err := EnsureDir(path); err != nil {
		return fmt.Errorf("create export directory: %w", err)
	}
	return os.WriteFile(path, []byte(s.MarkdownSummary()), 0644)
}

// ExportPath returns a default export filename under projectDir. Diff
// sessions use the resolved base SHA; file sessions use the sanitized file
// base name (capped to keep filesystems happy).
func (s *Session) ExportPath(projectDir string) (string, error) {
	ts := time.Now().UTC().Format("2006-01-02T15-04-05")
	if s.Kind == KindFile {
		base := filepath.Base(s.FilePath)
		sanitized := strings.Map(sanitizeRef, base)
		if len(sanitized) > maxExportNamePart {
			sanitized = sanitized[:maxExportNamePart]
		}
		return filepath.Join(projectDir, fmt.Sprintf("review_file_%s_%s.md", sanitized, ts)), nil
	}
	baseSHA, err := ResolveSHA(projectDir, s.BaseRef)
	if err != nil {
		baseSHA = strings.Map(sanitizeRef, s.BaseRef)
		if len(baseSHA) > 20 {
			baseSHA = baseSHA[:20]
		}
	}
	baseShort := baseSHA
	if len(baseShort) > 7 {
		baseShort = baseShort[:7]
	}
	return filepath.Join(projectDir, fmt.Sprintf("review_%s_%s.md", baseShort, ts)), nil
}

// maxExportNamePart caps the variable part of an export filename so hostile
// or pathological names cannot exceed filesystem limits.
const maxExportNamePart = 40

func sanitizeRef(r rune) rune {
	if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
		return r
	}
	return '-'
}

// Summary is a deprecated alias for MarkdownSummary. New code should use
// MarkdownSummary.
func (s *Session) Summary() string {
	return s.MarkdownSummary()
}

func generateID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
