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
type Comment struct {
	ID        string    `json:"id"`
	File      string    `json:"file"`
	LineNum   int       `json:"line_num"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Session tracks an in-progress code review.
type Session struct {
	ID         string    `json:"id"`
	ProjectDir string    `json:"project_dir"`
	BaseRef    string    `json:"base_ref"`
	HeadRef    string    `json:"head_ref"`
	Dirty      bool      `json:"dirty"`
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

// AddComment appends a new comment to the session.
func (s *Session) AddComment(file string, lineNum int, content string) Comment {
	c := Comment{
		ID:        generateID(),
		File:      file,
		LineNum:   lineNum,
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

// CommentsFor returns comments attached to a specific file and line.
func (s *Session) CommentsFor(file string, lineNum int) []Comment {
	var out []Comment
	for _, c := range s.Comments {
		if c.File == file && c.LineNum == lineNum {
			out = append(out, c)
		}
	}
	return out
}

// MarkdownSummary returns a Markdown formatted review summary intended for
// the LLM and for human readers. It includes the base/head refs and only the
// diff hunks that contain comments, so the result is focused on the reviewed
// changes rather than the complete raw diff.
func (s *Session) MarkdownSummary(diff string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Code Review\n\n")
	fmt.Fprintf(&b, "- **Base:** %s\n", s.BaseRef)
	fmt.Fprintf(&b, "- **Head:** %s\n", s.HeadRef)
	fmt.Fprintf(&b, "- **Dirty:** %v\n\n", s.Dirty)

	if len(s.Comments) == 0 {
		b.WriteString("No comments yet.\n")
		return b.String()
	}

	b.WriteString("## Comments\n\n")
	for _, c := range s.Comments {
		fmt.Fprintf(&b, "- `%s:%d`: %s\n", c.File, c.LineNum, c.Content)
	}

	commented := s.commentedHunks(diff)
	if len(commented) > 0 {
		b.WriteString("\n## Diff\n\n")
		b.WriteString("```diff\n")
		for _, hunk := range commented {
			b.WriteString(hunk)
			if !strings.HasSuffix(hunk, "\n") {
				b.WriteByte('\n')
			}
		}
		b.WriteString("```\n")
	}

	return b.String()
}

// commentedHunks returns the diff hunks that contain at least one commented
// line. Each returned string includes the file header (diff --git, ---, +++)
// and the hunk header and body, so it is a valid unified-diff fragment.
func (s *Session) commentedHunks(diff string) []string {
	hunks := collectHunks(ParseDiff(diff))
	if len(hunks) == 0 || len(s.Comments) == 0 {
		return nil
	}

	for i := range hunks {
		hunks[i].comment = hunkHasComment(hunks[i], s.Comments)
	}

	var out []string
	for _, h := range hunks {
		if h.comment {
			out = append(out, strings.Join(h.raw, "\n"))
		}
	}
	return out
}

type diffHunk struct {
	raw     []string
	lines   []DiffLine
	comment bool
}

func collectHunks(lines []DiffLine) []diffHunk {
	var hunks []diffHunk
	var current *diffHunk
	var headerBuf []string // file header lines for the current file

	flush := func() {
		if current != nil {
			hunks = append(hunks, *current)
			current = nil
		}
	}

	for _, line := range lines {
		switch line.Kind {
		case DiffHeader:
			flush()
			headerBuf = []string{line.Raw}
		case DiffFileMeta:
			headerBuf = append(headerBuf, line.Raw)
		case DiffHunkHeader:
			flush()
			current = &diffHunk{raw: append([]string(nil), headerBuf...)}
			current.raw = append(current.raw, line.Raw)
		case DiffContext, DiffAdded, DiffRemoved:
			if current == nil {
				continue
			}
			current.raw = append(current.raw, line.Raw)
			current.lines = append(current.lines, line)
		}
	}
	flush()
	return hunks
}

func hunkHasComment(h diffHunk, comments []Comment) bool {
	for _, line := range h.lines {
		file, lineNum := lineFileAndNumber(line)
		if lineNum <= 0 {
			continue
		}
		for _, c := range comments {
			if c.File == file && c.LineNum == lineNum {
				return true
			}
		}
	}
	return false
}

// lineFileAndNumber returns the file and line number a parsed diff line
// represents, using the new line number for context/added lines and the old
// line number for removed lines.
func lineFileAndNumber(line DiffLine) (string, int) {
	if line.File == "" {
		return "", 0
	}
	switch line.Kind {
	case DiffAdded, DiffContext:
		return line.File, line.NewLineNum
	case DiffRemoved:
		return line.File, line.OldLineNum
	}
	return "", 0
}

// Export writes the Markdown review summary to path.
func (s *Session) Export(diff, path string) error {
	if err := EnsureDir(path); err != nil {
		return fmt.Errorf("create export directory: %w", err)
	}
	return os.WriteFile(path, []byte(s.MarkdownSummary(diff)), 0644)
}

// ExportPath returns a default export filename under projectDir using the
// resolved base SHA and current timestamp.
func (s *Session) ExportPath(projectDir string) (string, error) {
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
	ts := time.Now().UTC().Format("2006-01-02T15-04-05")
	return filepath.Join(projectDir, fmt.Sprintf("review_%s_%s.md", baseShort, ts)), nil
}

func sanitizeRef(r rune) rune {
	if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
		return r
	}
	return '-'
}

// Summary is a deprecated alias for MarkdownSummary. New code should use
// MarkdownSummary.
func (s *Session) Summary(diff string) string {
	return s.MarkdownSummary(diff)
}

func generateID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
