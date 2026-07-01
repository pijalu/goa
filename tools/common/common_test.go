// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package common

import (
	"embed"
	"os"
	"path/filepath"
	"testing"
)

//go:embed docloader.go
var docTestFS embed.FS

func TestReadDoc(t *testing.T) {
	got := ReadDoc(docTestFS, "docloader.go")
	if got == "" {
		t.Fatal("expected non-empty doc content")
	}
	if got != "package common" && len(got) < 10 {
		t.Errorf("unexpected content: %q", got)
	}
}

func TestReadDoc_Missing(t *testing.T) {
	if got := ReadDoc(docTestFS, "missing.md"); got != "" {
		t.Errorf("expected empty string for missing file, got %q", got)
	}
}

func TestNormalizeFileToolPath(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"hello.go", "hello.go"},
		{"@hello.go", "hello.go"},
		{"  spaced.go  ", "  spaced.go  "},
	}
	for _, tc := range tests {
		got := NormalizeFileToolPath(tc.input)
		if got != tc.want {
			t.Errorf("NormalizeFileToolPath(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestIsProtectedPath(t *testing.T) {
	if !IsProtectedPath(".goa/config.yaml") {
		t.Error("expected .goa/config.yaml to be protected")
	}
	if !IsProtectedPath(".git/config") {
		t.Error("expected .git/config to be protected")
	}
	if IsProtectedPath("foo.go") {
		t.Error("expected foo.go not to be protected")
	}
}

func TestTruncateTail(t *testing.T) {
	content := "line1\nline2\nline3"
	res := TruncateTail(content, 2, 1024)
	if !res.Truncated {
		t.Error("expected truncated result")
	}
	if res.OutputLines != 2 {
		t.Errorf("OutputLines = %d, want 2", res.OutputLines)
	}
}

func TestCompressOutput_GitStatus(t *testing.T) {
	out, ok := CompressOutput("git status", " M file.go\n?? new.go")
	if !ok {
		t.Fatal("expected compression to succeed")
	}
	if out == "" {
		t.Error("expected non-empty compressed output")
	}
}

func TestExtPriority(t *testing.T) {
	goPri := ExtPriority("main.go")
	mdPri := ExtPriority("README.md")
	if goPri >= mdPri {
		t.Errorf("expected .go priority (%d) < .md priority (%d)", goPri, mdPri)
	}
}

func TestLevenshteinDistance(t *testing.T) {
	if d := LevenshteinDistance("", ""); d != 0 {
		t.Errorf("distance(\"\",\"\") = %d, want 0", d)
	}
	if d := LevenshteinDistance("kitten", "sitting"); d != 3 {
		t.Errorf("distance(\"kitten\",\"sitting\") = %d, want 3", d)
	}
}

func TestFuzzyFindFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("x"), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	path, score := FuzzyFindFile(dir, "config")
	if path == "" {
		t.Fatalf("expected to find config.yaml, score=%f", score)
	}
}
