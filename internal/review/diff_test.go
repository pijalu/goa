// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package review

import (
	"testing"
)

func TestParseDiff(t *testing.T) {
	diff := `diff --git a/main.go b/main.go
--- a/main.go
+++ b/main.go
@@ -1,3 +1,3 @@
 package main
-func old() {}
+func new() {}
 // end
`
	lines := ParseDiff(diff)
	if len(lines) == 0 {
		t.Fatal("expected parsed lines")
	}

	if !hasDiffKind(lines, DiffFileMeta, func(l DiffLine) bool { return l.File == "main.go" }) {
		t.Error("expected main.go file meta")
	}
	if !hasDiffKind(lines, DiffHunkHeader, func(l DiffLine) bool { return l.NewLineNum == 1 }) {
		t.Error("expected hunk header")
	}
	if !hasDiffKind(lines, DiffRemoved, nil) {
		t.Error("expected removed line")
	}
	if !hasDiffKind(lines, DiffAdded, nil) {
		t.Error("expected added line")
	}
}

func hasDiffKind(lines []DiffLine, kind DiffLineKind, match func(DiffLine) bool) bool {
	for _, l := range lines {
		if l.Kind != kind {
			continue
		}
		if match == nil || match(l) {
			return true
		}
	}
	return false
}

func TestParseHunkHeader(t *testing.T) {
	oldLine, newLine := parseHunkHeader("@@ -10,5 +20,7 @@ context")
	if oldLine != 10 {
		t.Errorf("oldLine = %d, want 10", oldLine)
	}
	if newLine != 20 {
		t.Errorf("newLine = %d, want 20", newLine)
	}

	oldLine, newLine = parseHunkHeader("@@ -1 +1 @@")
	if oldLine != 1 || newLine != 1 {
		t.Errorf("unexpected header parse: %d, %d", oldLine, newLine)
	}
}

func TestParseFilePath(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"b/main.go", "main.go"},
		{"a/main.go", "main.go"},
		{"main.go", "main.go"},
	}
	for _, c := range cases {
		got := parseFilePath(c.input)
		if got != c.want {
			t.Errorf("parseFilePath(%q) = %q, want %q", c.input, got, c.want)
		}
	}
}
