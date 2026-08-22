// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package review

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// loaderCase is one LoadReviewFile scenario: setup writes the fixture and
// returns the path argument (absolute or project-relative); wantErr is a
// required error substring (empty = expect success); check asserts content.
type loaderCase struct {
	name    string
	setup   func(t *testing.T, dir string) string
	wantErr string
	check   func(t *testing.T, dir string, c *FileReviewContent)
}

// runLoaderCases executes cases against a fresh temp project each.
func runLoaderCases(t *testing.T, cases []loaderCase) {
	t.Helper()
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			arg := tt.setup(t, dir)
			c, err := LoadReviewFile(dir, arg)
			if !checkLoaderResult(t, tt, arg, c, err) {
				return
			}
			if tt.check != nil {
				tt.check(t, dir, c)
			}
		})
	}
}

// checkLoaderResult enforces the case's wantErr expectation and reports
// whether the success-path check may run.
func checkLoaderResult(t *testing.T, c loaderCase, arg string, got *FileReviewContent, err error) bool {
	t.Helper()
	if c.wantErr != "" {
		if err == nil {
			t.Fatalf("LoadReviewFile(%q) succeeded, want error containing %q", arg, c.wantErr)
		}
		if !strings.Contains(err.Error(), c.wantErr) {
			t.Fatalf("error = %q, want containing %q", err.Error(), c.wantErr)
		}
		return false
	}
	if err != nil {
		t.Fatalf("LoadReviewFile(%q) failed: %v", arg, err)
	}
	if got == nil {
		t.Fatal("content is nil without error")
	}
	return true
}

// TestLoadReviewFile_Text covers happy-path line handling: plain text, CRLF,
// missing trailing newline, and empty files. Per-scenario setup/check bodies
// live in named helpers to keep each function inside the complexity budget.
func TestLoadReviewFile_Text(t *testing.T) {
	runLoaderCases(t, []loaderCase{
		{name: "plain text", setup: writeNotesTxt, check: checkPlainNotes},
		{name: "crlf stripped", setup: writeCrlfTxt, check: checkCrlfStripped},
		{name: "no trailing newline", setup: writeNoTrailingNewline, check: checkNoTrailingNewline},
		{name: "empty file", setup: writeEmptyTxt, check: checkEmptyFile},
	})
}

func writeNotesTxt(t *testing.T, dir string) string {
	write(t, filepath.Join(dir, "notes.txt"), "first\nsecond\nthird\n")
	return "notes.txt"
}

func checkPlainNotes(t *testing.T, dir string, c *FileReviewContent) {
	assertLinesJoined(t, c, "first|second|third")
	if c.Truncated {
		t.Error("Truncated = true, want false")
	}
	if c.Ext != "txt" || c.IsMarkdown {
		t.Errorf("ext/markdown = %q/%v, want txt/false", c.Ext, c.IsMarkdown)
	}
	if c.Path != "notes.txt" {
		t.Errorf("anchor Path = %q", c.Path)
	}
	if want := filepath.Join(dir, "notes.txt"); c.AbsPath != want {
		t.Errorf("AbsPath = %q, want %q", c.AbsPath, want)
	}
}

// assertLinesJoined compares "|"-joined loaded lines against want.
func assertLinesJoined(t *testing.T, c *FileReviewContent, want string) {
	t.Helper()
	if got := strings.Join(c.Lines, "|"); got != want {
		t.Errorf("lines = %q, want %q", got, want)
	}
}

func writeCrlfTxt(t *testing.T, dir string) string {
	write(t, filepath.Join(dir, "crlf.txt"), "alpha\r\nbeta\r\n")
	return "crlf.txt"
}

func checkCrlfStripped(t *testing.T, _ string, c *FileReviewContent) {
	assertLinesJoined(t, c, "alpha|beta")
	// Bytes counts loaded bytes before normalization: \r chars are
	// still on disk and were loaded.
	if c.Bytes != len("alpha\r\nbeta\r\n") {
		t.Errorf("Bytes = %d", c.Bytes)
	}
}

func writeNoTrailingNewline(t *testing.T, dir string) string {
	write(t, filepath.Join(dir, "x.txt"), "a\nb")
	return "x.txt"
}

func checkNoTrailingNewline(t *testing.T, _ string, c *FileReviewContent) {
	assertLinesJoined(t, c, "a|b")
}

func writeEmptyTxt(t *testing.T, dir string) string {
	write(t, filepath.Join(dir, "empty.txt"), "")
	return "empty.txt"
}

func checkEmptyFile(t *testing.T, _ string, c *FileReviewContent) {
	if len(c.Lines) != 0 {
		t.Errorf("lines = %v, want none", c.Lines)
	}
	if c.Truncated || c.Bytes != 0 {
		t.Errorf("Truncated/Bytes = %v/%d, want false/0", c.Truncated, c.Bytes)
	}
}

// TestLoadReviewFile_Errors covers rejection paths with descriptive errors:
// missing files, directories, binary content, and invalid arguments.
func TestLoadReviewFile_Errors(t *testing.T) {
	runLoaderCases(t, []loaderCase{
		{
			name: "missing file",
			setup: func(t *testing.T, dir string) string {
				return "ghost.txt"
			},
			wantErr: "file not found",
		},
		{
			name: "directory",
			setup: func(t *testing.T, dir string) string {
				mkdir(t, filepath.Join(dir, "adir"))
				return "adir"
			},
			wantErr: "directory",
		},
		{
			name: "binary NUL within sniff window",
			setup: func(t *testing.T, dir string) string {
				data := append(bytes.Repeat([]byte("A"), 100), 0)
				data = append(data, bytes.Repeat([]byte("B"), 100)...)
				writeBytes(t, filepath.Join(dir, "blob.bin"), data)
				return "blob.bin"
			},
			wantErr: "binary",
		},
		{
			name:    "empty path rejected",
			setup:   func(t *testing.T, dir string) string { return "" },
			wantErr: "required",
		},
	})

	if _, err := LoadReviewFile("", "a.txt"); err == nil {
		t.Error("expected error for empty project dir")
	}
}

// The NUL-sniff heuristic only inspects the first 8 KiB (same as
// tools/readfile.go): a NUL past that window loads as text — render-time
// sanitizing owns control characters from there on.
func TestLoadReviewFile_NULBeyondSniffWindowIsText(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "late-nul.bin")
	data := append(bytes.Repeat([]byte("A"), binarySniffLen+800), 0)
	data = append(data, []byte("tail\n")...)
	writeBytes(t, path, data)

	c, err := LoadReviewFile(dir, "late-nul.bin")
	if err != nil {
		t.Fatalf("LoadReviewFile failed: %v", err)
	}
	if joined := strings.Join(c.Lines, "\n"); !strings.HasSuffix(joined, "\x00tail") {
		t.Errorf("content = %q, want suffix %q", joined, "\x00tail")
	}
}

// TestLoadReviewFile_ByteCaps pins the 2 MiB cap and its exact boundary:
// over-cap truncates, at-cap does not.
func TestLoadReviewFile_ByteCaps(t *testing.T) {
	runLoaderCases(t, []loaderCase{
		{
			name: "over byte cap truncates",
			setup: func(t *testing.T, dir string) string {
				// Single line longer than 2 MiB, no trailing newline.
				write(t, filepath.Join(dir, "big.txt"), strings.Repeat("x", maxReviewFileBytes+1))
				return "big.txt"
			},
			check: func(t *testing.T, dir string, c *FileReviewContent) {
				if !c.Truncated {
					t.Error("Truncated = false, want true over byte cap")
				}
				if c.Bytes != maxReviewFileBytes {
					t.Errorf("Bytes = %d, want capped %d", c.Bytes, maxReviewFileBytes)
				}
				if len(c.Lines) != 1 || len(c.Lines[0]) != maxReviewFileBytes {
					t.Errorf("lines/len = %d/%d, want 1 line of %d", len(c.Lines), len(c.Lines[0]), maxReviewFileBytes)
				}
			},
		},
		{
			name: "exactly byte cap not truncated",
			setup: func(t *testing.T, dir string) string {
				write(t, filepath.Join(dir, "exact.txt"), strings.Repeat("y", maxReviewFileBytes))
				return "exact.txt"
			},
			check: func(t *testing.T, dir string, c *FileReviewContent) {
				if c.Truncated || c.Bytes != maxReviewFileBytes {
					t.Errorf("Truncated/Bytes = %v/%d, want false/%d", c.Truncated, c.Bytes, maxReviewFileBytes)
				}
			},
		},
	})
}

// TestLoadReviewFile_LineCaps pins the 20k-line cap and its exact boundary:
// over-cap truncates keeping the first 20k lines in order, at-cap does not.
func TestLoadReviewFile_LineCaps(t *testing.T) {
	runLoaderCases(t, []loaderCase{
		{
			name: "over line cap truncates",
			setup: func(t *testing.T, dir string) string {
				var b strings.Builder
				for i := 0; i < maxReviewFileLines+1; i++ {
					b.WriteString("line-")
					b.WriteString(strconv.Itoa(i))
					b.WriteByte('\n')
				}
				write(t, filepath.Join(dir, "many.txt"), b.String())
				return "many.txt"
			},
			check: func(t *testing.T, dir string, c *FileReviewContent) {
				if !c.Truncated {
					t.Error("Truncated = false, want true over line cap")
				}
				if len(c.Lines) != maxReviewFileLines {
					t.Fatalf("lines = %d, want capped %d", len(c.Lines), maxReviewFileLines)
				}
				want := "line-" + strconv.Itoa(maxReviewFileLines-1)
				if c.Lines[maxReviewFileLines-1] != want {
					t.Errorf("last kept line = %q, want %q", c.Lines[maxReviewFileLines-1], want)
				}
			},
		},
		{
			name: "exactly line cap not truncated",
			setup: func(t *testing.T, dir string) string {
				var b strings.Builder
				for i := 0; i < maxReviewFileLines; i++ {
					b.WriteString("l\n")
				}
				write(t, filepath.Join(dir, "edge.txt"), b.String())
				return "edge.txt"
			},
			check: func(t *testing.T, dir string, c *FileReviewContent) {
				if c.Truncated {
					t.Error("Truncated = true at exactly the line cap, want false")
				}
				if len(c.Lines) != maxReviewFileLines {
					t.Errorf("lines = %d, want %d", len(c.Lines), maxReviewFileLines)
				}
			},
		},
	})
}

// TestLoadReviewFile_MarkdownDetection checks extension classification is
// case-insensitive and limited to md/markdown.
func TestLoadReviewFile_MarkdownDetection(t *testing.T) {
	runLoaderCases(t, []loaderCase{
		{
			name: "markdown lowercase",
			setup: func(t *testing.T, dir string) string {
				write(t, filepath.Join(dir, "readme.md"), "# Hi\n")
				return "readme.md"
			},
			check: func(t *testing.T, dir string, c *FileReviewContent) {
				if c.Ext != "md" || !c.IsMarkdown {
					t.Errorf("ext/markdown = %q/%v, want md/true", c.Ext, c.IsMarkdown)
				}
			},
		},
		{
			name: "markdown uppercase",
			setup: func(t *testing.T, dir string) string {
				write(t, filepath.Join(dir, "NOTES.MARKDOWN"), "# Notes\n")
				return "NOTES.MARKDOWN"
			},
			check: func(t *testing.T, dir string, c *FileReviewContent) {
				if c.Ext != "markdown" || !c.IsMarkdown {
					t.Errorf("ext/markdown = %q/%v, want markdown/true", c.Ext, c.IsMarkdown)
				}
			},
		},
		{
			name: "non-markdown extension",
			setup: func(t *testing.T, dir string) string {
				write(t, filepath.Join(dir, "main.go"), "package main\n")
				return "main.go"
			},
			check: func(t *testing.T, dir string, c *FileReviewContent) {
				if c.Ext != "go" || c.IsMarkdown {
					t.Errorf("ext/markdown = %q/%v, want go/false", c.Ext, c.IsMarkdown)
				}
			},
		},
	})
}

// TestLoadReviewFile_AnchorPaths pins anchor-path normalization: relative
// paths stay relative, absolute-inside-project collapses to relative, and
// out-of-project paths stay absolute.
func TestLoadReviewFile_AnchorPaths(t *testing.T) {
	runLoaderCases(t, []loaderCase{
		{
			name: "nested relative path kept",
			setup: func(t *testing.T, dir string) string {
				mkdir(t, filepath.Join(dir, "sub"))
				write(t, filepath.Join(dir, "sub", "b.txt"), "hi\n")
				return filepath.Join("sub", "b.txt")
			},
			check: func(t *testing.T, dir string, c *FileReviewContent) {
				if want := filepath.Join("sub", "b.txt"); c.Path != want {
					t.Errorf("Path = %q, want %q", c.Path, want)
				}
			},
		},
		{
			name: "absolute inside project becomes relative",
			setup: func(t *testing.T, dir string) string {
				mkdir(t, filepath.Join(dir, "sub"))
				write(t, filepath.Join(dir, "sub", "a.go"), "package sub\n")
				return filepath.Join(dir, "sub", "a.go")
			},
			check: func(t *testing.T, dir string, c *FileReviewContent) {
				if wantRel := filepath.Join("sub", "a.go"); c.Path != wantRel {
					t.Errorf("Path = %q, want relative %q", c.Path, wantRel)
				}
				if !filepath.IsAbs(c.AbsPath) {
					t.Errorf("AbsPath = %q, want absolute", c.AbsPath)
				}
			},
		},
		{
			name: "path outside project stays absolute",
			setup: func(t *testing.T, dir string) string {
				outside := t.TempDir()
				write(t, filepath.Join(outside, "elsewhere.txt"), "far away\n")
				return filepath.Join(outside, "elsewhere.txt")
			},
			check: func(t *testing.T, dir string, c *FileReviewContent) {
				if !filepath.IsAbs(c.Path) {
					t.Errorf("Path = %q, want absolute for out-of-project file", c.Path)
				}
				if c.Path != c.AbsPath {
					t.Errorf("Path/AbsPath = %q/%q, want equal", c.Path, c.AbsPath)
				}
			},
		},
	})
}

// write writes a text file, failing the test on error.
func write(t *testing.T, path, content string) {
	t.Helper()
	writeBytes(t, path, []byte(content))
}

func writeBytes(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func mkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
}
