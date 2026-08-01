// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package embeddoc

import (
	"embed"
	"testing"
)

//go:embed testdata/*.md
var testFS embed.FS

func TestStripHTMLComments(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"no comments", "hello\nworld", "hello\nworld"},
		{"leading SPDX block", "<!--\nSPDX-License-Identifier: GPL-3.0-or-later\n-->\n\n# Title\nbody", "# Title\nbody"},
		{"multiple leading comments", "<!-- a -->\n<!-- b -->\ntext", "text"},
		{"mid-document comment", "before\n<!-- note -->\nafter", "before\n\nafter"},
		{"inline comment", "a <!-- hidden --> b", "a  b"},
		{"multi-line comment keeps line structure", "start <!-- one\ntwo\nthree --> end", "start \n\n end"},
		{"unterminated drops to EOF", "keep <!-- never closed\ngone\ngone", "keep"},
		{"fenced code preserved", "text\n```html\n<!-- in code -->\n```\nafter", "text\n```html\n<!-- in code -->\n```\nafter"},
		{"tilde fence preserved", "~~~\n<!-- in code -->\n~~~\n<!-- real -->", "~~~\n<!-- in code -->\n~~~"},
		{"comment after closed fence", "```\ncode\n```\n<!-- trailing -->\nend", "```\ncode\n```\n\nend"},
		{"fence with language then comment", "```go\n// <!-- not a comment -->\n```\nvisible <!-- gone -->", "```go\n// <!-- not a comment -->\n```\nvisible"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := string(StripHTMLComments([]byte(tt.input))); got != tt.want {
				t.Errorf("StripHTMLComments(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestLoadText(t *testing.T) {
	tests := []struct {
		name     string
		file     string
		expected string
	}{
		{"existing file", "testdata/plain.md", "plain text"},
		{"missing file", "testdata/missing.md", ""},
		{"trimmed whitespace", "testdata/spaces.md", "trimmed"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := LoadText(testFS, tt.file)
			if got != tt.expected {
				t.Errorf("LoadText(%q) = %q, want %q", tt.file, got, tt.expected)
			}
		})
	}
}

func TestParseDocument_WithFrontmatter(t *testing.T) {
	doc, err := LoadDocument(testFS, "testdata/frontmatter.md")
	if err != nil {
		t.Fatalf("LoadDocument failed: %v", err)
	}

	if got := doc.Body; got != "body content" {
		t.Errorf("Body = %q, want %q", got, "body content")
	}

	if got, ok := doc.Frontmatter["name"].(string); !ok || got != "test" {
		t.Errorf("Frontmatter[name] = %v, want %q", doc.Frontmatter["name"], "test")
	}

	if got, ok := doc.Frontmatter["count"].(int); !ok || got != 42 {
		t.Errorf("Frontmatter[count] = %v, want %d", doc.Frontmatter["count"], 42)
	}
}

func TestParseDocument_WithoutFrontmatter(t *testing.T) {
	doc, err := LoadDocument(testFS, "testdata/plain.md")
	if err != nil {
		t.Fatalf("LoadDocument failed: %v", err)
	}
	if len(doc.Frontmatter) != 0 {
		t.Errorf("expected empty frontmatter, got %v", doc.Frontmatter)
	}
	if doc.Body != "plain text" {
		t.Errorf("Body = %q, want %q", doc.Body, "plain text")
	}
}

func TestParseDocument_MissingFile(t *testing.T) {
	_, err := LoadDocument(testFS, "testdata/nope.md")
	if err == nil {
		t.Error("expected error for missing file")
	}
}
