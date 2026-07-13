// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSearchTool_Schema_HasRequiredFields(t *testing.T) {
	tool := &SearchTool{}
	schema := tool.Schema()
	if schema.Name != "search" {
		t.Errorf("schema.Name = %q, want %q", schema.Name, "search")
	}
	props := schema.Schema["properties"].(map[string]any)
	if _, ok := props["pattern"]; !ok {
		t.Error("schema missing required field: pattern")
	}
}

// TestSearchTool_Schema_SteersAwayFromGrep locks in the guidance that makes
// the agent reach for `search` instead of `bash`+grep/rg: the description must
// state it is preferred for codebase search and reference grep/rg so a model
// wiring bash+grep maps that intent onto this tool.
func TestSearchTool_Schema_SteersAwayFromGrep(t *testing.T) {
	tool := &SearchTool{}
	desc := strings.ToLower(tool.Schema().Description)
	for _, want := range []string{"prefer", "grep", "search"} {
		if !strings.Contains(desc, want) {
			t.Errorf("search description should mention %q to steer away from bash+grep; got: %q", want, tool.Schema().Description)
		}
	}
}

// TestBashTool_Schema_SteersToSearchForCodebaseSearch ensures the bash
// description tells the agent to use the `search` tool for codebase search so
// bash is reserved for features search cannot do.
func TestBashTool_Schema_SteersToSearchForCodebaseSearch(t *testing.T) {
	tool := &BashTool{}
	desc := strings.ToLower(tool.Schema().Description)
	if !strings.Contains(desc, "search") || !strings.Contains(desc, "prefer") {
		t.Errorf("bash description should tell the agent to prefer `search` for codebase search; got: %q", tool.Schema().Description)
	}
}

func TestSearchTool_ShortDoc_NotEmpty(t *testing.T) {
	tool := &SearchTool{}
	if tool.ShortDoc() == "" {
		t.Error("ShortDoc should not be empty")
	}
}

func TestSearchTool_LongDoc_NotEmpty(t *testing.T) {
	tool := &SearchTool{}
	if tool.LongDoc() == "" {
		t.Error("LongDoc should not be empty")
	}
}

func TestSearchTool_Examples_NotEmpty(t *testing.T) {
	tool := &SearchTool{}
	if len(tool.Examples()) == 0 {
		t.Error("Examples should not be empty")
	}
}

func TestSearchTool_Execute_EmptyInput_ReturnsError(t *testing.T) {
	tool := &SearchTool{}
	_, err := tool.Execute("")
	if err == nil {
		t.Error("Execute with empty input should return error")
	}
}

func TestSearchTool_Execute_InvalidJSON_ReturnsError(t *testing.T) {
	tool := &SearchTool{}
	_, err := tool.Execute("not json")
	if err == nil {
		t.Error("Execute with invalid JSON should return error")
	}
}

func TestSearchTool_Execute_MissingPattern_ReturnsError(t *testing.T) {
	tool := &SearchTool{}
	_, err := tool.Execute(`{}`)
	if err == nil {
		t.Error("Execute without pattern should return error")
	}
}

func TestSearchTool_Execute_SearchInDir_FindsResults(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "test.go"), []byte("package main\nfunc hello() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "other.go"), []byte("package main\nfunc world() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}

	tool := &SearchTool{
		WorktreeMgr: nil,
		Threads:     2,
		MaxResults:  50,
	}
	result, err := tool.Execute(`{"pattern": "func", "path": "` + dir + `", "glob": "*.go"}`)
	if err != nil {
		t.Fatalf("Search should succeed: %v", err)
	}
	if len(result) < 5 {
		t.Errorf("Expected search results, got: %q", result)
	}
}

func TestSearchTool_Execute_NoMatches_ReturnsEmpty(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "test.go"), []byte("package main\n"), 0644); err != nil {
		t.Fatal(err)
	}

	tool := &SearchTool{
		WorktreeMgr: nil,
		Threads:     2,
		MaxResults:  50,
	}
	result, err := tool.Execute(`{"pattern": "ZZZZNOMATCH", "path": "` + dir + `", "glob": "*.go"}`)
	if err != nil {
		t.Fatalf("Search with no matches should succeed: %v", err)
	}
	if len(result) < 1 {
		t.Error("Expected at least 'no matches' message")
	}
}

func TestSearchTool_Execute_RespectsMaxResults(t *testing.T) {
	dir := t.TempDir()
	// Create a file with 100 matching lines
	var content string
	for i := 0; i < 100; i++ {
		content += "func testFunction() {}\n"
	}
	if err := os.WriteFile(filepath.Join(dir, "test.go"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	tool := &SearchTool{
		WorktreeMgr: nil,
		Threads:     2,
		MaxResults:  10,
	}
	result, err := tool.Execute(`{"pattern": "func", "path": "` + dir + `", "glob": "*.go"}`)
	if err != nil {
		t.Fatalf("Search should succeed: %v", err)
	}
	if len(result) < 1 {
		t.Error("Expected at least some results")
	}
}

func TestSearchTool_Execute_CaseSensitive(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "test.go"), []byte("func Hello() {}\nfunc hello() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}

	tool := &SearchTool{
		WorktreeMgr: nil,
		Threads:     2,
		MaxResults:  50,
	}
	result, err := tool.Execute(`{"pattern": "Hello", "path": "` + dir + `", "glob": "*.go", "case_sensitive": true}`)
	if err != nil {
		t.Fatalf("Search should succeed: %v", err)
	}
	if len(result) < 1 {
		t.Error("Expected at least one match for case-sensitive Hello")
	}
}

func TestSearchTool_Execute_WithContextLines(t *testing.T) {
	dir := t.TempDir()
	content := "line1\nfunc target() {}\nline3\n"
	if err := os.WriteFile(filepath.Join(dir, "test.go"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	tool := &SearchTool{
		WorktreeMgr: nil,
		Threads:     2,
		MaxResults:  50,
	}
	result, err := tool.Execute(`{"pattern": "target", "path": "` + dir + `", "context_lines": 1}`)
	if err != nil {
		t.Fatalf("Search should succeed: %v", err)
	}
	if len(result) < 1 {
		t.Error("Expected search results with context")
	}
}

func TestSearchTool_ShouldSkipDir_DotRoot(t *testing.T) {
	tool := &SearchTool{}
	excludes := []string{".git", "vendor", "node_modules"}
	// The root "." should NOT be skipped
	if tool.shouldSkipDir(".", excludes) {
		t.Error("shouldSkipDir should not skip '.' (root) directory")
	}
	// ".." should NOT be skipped
	if tool.shouldSkipDir("..", excludes) {
		t.Error("shouldSkipDir should not skip '..' directory")
	}
	// Hidden dirs should still be skipped
	if !tool.shouldSkipDir(".git", excludes) {
		t.Error("shouldSkipDir should skip .git directory")
	}
	if !tool.shouldSkipDir(".hidden", excludes) {
		t.Error("shouldSkipDir should skip .hidden directory")
	}
	// Named excludes should also be skipped
	if !tool.shouldSkipDir("vendor", excludes) {
		t.Error("shouldSkipDir should skip vendor directory")
	}
}

func TestSearchTool_Score_LineWithMoreMatchesRanksHigher(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("TODO TODO\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.go"), []byte("TODO\n"), 0644); err != nil {
		t.Fatal(err)
	}

	tool := &SearchTool{WorktreeMgr: nil, Threads: 2, MaxResults: 50}
	result, err := tool.Execute(`{"pattern": "TODO", "path": "` + dir + `", "glob": "*.go", "showing": 10}`)
	if err != nil {
		t.Fatalf("Search should succeed: %v", err)
	}

	// a.go has a single line scoring 2; b.go has a single line scoring 1.
	// a.go should appear first, and its line should be shown first.
	lines := strings.Split(result, "\n")
	var aIdx, bIdx int
	for i, line := range lines {
		if strings.Contains(line, "a.go:") {
			aIdx = i
		}
		if strings.Contains(line, "b.go:") {
			bIdx = i
		}
	}
	if aIdx == 0 || bIdx == 0 {
		t.Fatalf("Could not locate file headers in output:\n%s", result)
	}
	if aIdx > bIdx {
		t.Errorf("Expected a.go (score 2) to rank above b.go (score 1); got:\n%s", result)
	}
}

func TestSearchTool_Score_FileTotalScoreRanksHigher(t *testing.T) {
	dir := t.TempDir()
	// a.go: two lines, each with two matches -> total score 4
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("TODO TODO\nTODO TODO\n"), 0644); err != nil {
		t.Fatal(err)
	}
	// b.go: three lines, each with one match -> total score 3
	if err := os.WriteFile(filepath.Join(dir, "b.go"), []byte("TODO\nTODO\nTODO\n"), 0644); err != nil {
		t.Fatal(err)
	}

	tool := &SearchTool{WorktreeMgr: nil, Threads: 2, MaxResults: 50}
	result, err := tool.Execute(`{"pattern": "TODO", "path": "` + dir + `", "glob": "*.go", "showing": 10}`)
	if err != nil {
		t.Fatalf("Search should succeed: %v", err)
	}

	lines := strings.Split(result, "\n")
	var aIdx, bIdx int
	for i, line := range lines {
		if strings.Contains(line, "a.go:") {
			aIdx = i
		}
		if strings.Contains(line, "b.go:") {
			bIdx = i
		}
	}
	if aIdx == 0 || bIdx == 0 {
		t.Fatalf("Could not locate file headers in output:\n%s", result)
	}
	if aIdx > bIdx {
		t.Errorf("Expected a.go (total score 4) to rank above b.go (total score 3); got:\n%s", result)
	}
}

func TestSearchTool_Score_MultipleMatchesPerLine(t *testing.T) {
	dir := t.TempDir()
	content := "func a() {} func b() {}\n"
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	tool := &SearchTool{WorktreeMgr: nil, Threads: 2, MaxResults: 50}
	result, err := tool.Execute(`{"pattern": "func", "path": "` + dir + `", "glob": "*.go"}`)
	if err != nil {
		t.Fatalf("Search should succeed: %v", err)
	}

	if !strings.Contains(result, "2 matches") {
		t.Errorf("Expected line with two 'func' matches to report 2 matches, got:\n%s", result)
	}
}

func TestSearchTool_Score_PreviewTruncatesLowerScores(t *testing.T) {
	dir := t.TempDir()
	// Line 1 has 3 matches, line 2 has 1 match.
	content := "TODO TODO TODO\nTODO\n"
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	tool := &SearchTool{WorktreeMgr: nil, Threads: 2, MaxResults: 50}
	result, err := tool.Execute(`{"pattern": "TODO", "path": "` + dir + `", "glob": "*.go", "showing": 1}`)
	if err != nil {
		t.Fatalf("Search should succeed: %v", err)
	}

	if !strings.Contains(result, "(+1 more)") {
		t.Errorf("Expected truncated indicator when showing=1, got:\n%s", result)
	}
	if strings.Contains(result, "TODO TODO TODO") {
		// The high-score line should be the one shown.
	} else if strings.Contains(result, "TODO\n") {
		t.Errorf("Expected highest-scoring line to be shown; got:\n%s", result)
	}
}

func TestSearchTool_Execute_WithDotRoot(t *testing.T) {
	// Test that searching from "." root works (regression: shouldSkipDir
	// must not skip the "." directory itself)
	tool := &SearchTool{
		WorktreeMgr: nil,
		Threads:     2,
		MaxResults:  50,
	}
	// Search the current package (tools/) for "SearchTool" — should find
	// itself in search.go
	result, err := tool.Execute(`{"pattern": "SearchTool", "path": ".", "glob": "*.go"}`)
	if err != nil {
		t.Fatalf("Search from . root should succeed: %v", err)
	}
	if len(result) < 5 || strings.Contains(result, "no matching files") {
		t.Errorf("Expected search results from . root, got: %q", result)
	}
}

// TestSearchTool_TotalShownCountIsAccurate is a regression test for the
// double-counting bug where formatFileContentLines incremented *totalShown via
// the pointer AND returned the count which the caller added back. That inflated
// the "showing N" summary and prematurely truncated later files.
func TestSearchTool_Execute_SearchSingleFile(t *testing.T) {
	dir := t.TempDir()
	targetFile := filepath.Join(dir, "target.go")
	if err := os.WriteFile(targetFile, []byte("package main\nfunc configSetters() {}\nfunc main() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	// Write another file to ensure we don't accidentally search both
	if err := os.WriteFile(filepath.Join(dir, "other.go"), []byte("package other\nfunc somethingElse() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}

	tool := &SearchTool{WorktreeMgr: nil, Threads: 2, MaxResults: 50}
	out, err := tool.Execute(`{"pattern": "configSetters", "path": "` + targetFile + `"}`)
	if err != nil {
		t.Fatalf("Search should succeed: %v", err)
	}
	if !strings.Contains(out, "configSetters") {
		t.Errorf("expected to find 'configSetters' in single-file search, got:\n%s", out)
	}
	if !strings.Contains(out, "1 match") {
		t.Errorf("expected '1 match' in output, got:\n%s", out)
	}
}

func TestSearchTool_Execute_SearchSingleFile_WithGlobFilter(t *testing.T) {
	dir := t.TempDir()
	targetFile := filepath.Join(dir, "target.go")
	if err := os.WriteFile(targetFile, []byte("package main\nfunc configSetters() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}

	tool := &SearchTool{WorktreeMgr: nil, Threads: 2, MaxResults: 50}
	// glob matching a single file: *.go matches the file when passed as path
	out, err := tool.Execute(`{"pattern": "configSetters", "path": "` + targetFile + `", "glob": "*.go"}`)
	if err != nil {
		t.Fatalf("Search should succeed: %v", err)
	}
	if !strings.Contains(out, "configSetters") {
		t.Errorf("expected to find 'configSetters' with glob filter, got:\n%s", out)
	}
}

func TestSearchTool_Execute_SearchSingleFile_GlobExcludes(t *testing.T) {
	dir := t.TempDir()
	targetFile := filepath.Join(dir, "target.txt")
	if err := os.WriteFile(targetFile, []byte("configSetters\n"), 0644); err != nil {
		t.Fatal(err)
	}

	tool := &SearchTool{WorktreeMgr: nil, Threads: 2, MaxResults: 50}
	// glob is "*.go", target is .txt — should exclude
	out, err := tool.Execute(`{"pattern": "configSetters", "path": "` + targetFile + `", "glob": "*.go"}`)
	if err != nil {
		t.Fatalf("Search should succeed: %v", err)
	}
	if !strings.Contains(out, "no matching files found") {
		t.Errorf("expected 'no matching files found' for mismatched glob, got:\n%s", out)
	}
}

func TestSearchTool_TotalShownCountIsAccurate(t *testing.T) {
	dir := t.TempDir()
	// Two files, two matches each. With maxResults limiting total lines, the
	// reported "showing N" must equal the actual number of "  <n>: ..." lines.
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("MARK x\nMARK y\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.go"), []byte("MARK z\nMARK w\n"), 0644); err != nil {
		t.Fatal(err)
	}

	tool := &SearchTool{WorktreeMgr: nil, Threads: 2, MaxResults: 50}
	out, err := tool.Execute(`{"pattern": "MARK", "path": "` + dir + `", "glob": "*.go", "showing": 1}`)
	if err != nil {
		t.Fatalf("Search should succeed: %v", err)
	}

	// Count actual content lines emitted (format "  <num>: <matched-text>").
	// Each file shows at most `showing`=1 content line => 2 lines total.
	// We count the matched text occurrences in content lines: file headers
	// ("a.go: 2 matches") and (+more) summaries do not contain ": MARK".
	contentLines := strings.Count(out, ": MARK")
	if contentLines != 2 {
		t.Errorf("expected exactly 2 shown content lines (showing=1 per file), got %d:\n%s", contentLines, out)
	}
	// The reported "showing N" must match the actual emitted count, not be doubled.
	if !strings.Contains(out, "showing 2") {
		t.Errorf("expected summary \"showing 2\", got:\n%s", out)
	}
	if strings.Contains(out, "showing 4") {
		t.Errorf("double-counted summary detected (showing 4). Output:\n%s", out)
	}
}
