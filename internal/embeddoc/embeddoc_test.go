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
