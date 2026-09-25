// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package common

import (
	"strings"
	"testing"
)

// --- routing ---

// TestCompressOutput_Routing pins which commands are compressed and which are
// passed through untouched.
func TestCompressOutput_Routing(t *testing.T) {
	cases := []struct {
		name        string
		command     string
		output      string
		wantApplied bool
		wantHeader  string
	}{
		{name: "empty output", command: "git diff", output: "", wantApplied: false},
		{name: "unrouted command", command: "go build ./...", output: "ok", wantApplied: false},
		{name: "git diff", command: "git diff HEAD", wantApplied: true, wantHeader: "[compress: git diff]",
			output: "diff --git a/a.go b/a.go\n@@ -1 +1 @@\n+added\n"},
		{name: "git show routes to diff", command: "git show abc123", wantApplied: true, wantHeader: "[compress: git diff]",
			output: "diff --git a/a.go b/a.go\n@@ -1 +1 @@\n+added\n"},
		{name: "git status", command: "git status --short", wantApplied: true, wantHeader: "[compress: git status]",
			output: " M tracked.go\n?? new.go\n"},
		{name: "git log", command: "git log --oneline", wantApplied: true, wantHeader: "[compress: git log]",
			output: "commit abc\n\n    fix the thing\n"},
		{name: "ls", command: "ls -la", wantApplied: true, wantHeader: "[compress: ls]",
			output: "-rw-r--r-- 1 u g 10 Jan 1 00:00 main.go\n"},
		{name: "grep", command: "grep -rn foo .", wantApplied: true, wantHeader: "[compress: grep]",
			output: "a.go:1:foo here\n"},
		{name: "rg", command: "rg foo", wantApplied: true, wantHeader: "[compress: grep]",
			output: "a.go:1:foo here\n"},
		{name: "cat", command: "cat main.go", wantApplied: true, wantHeader: "[compress: read]",
			output: "package main\n"},
		{name: "test", command: "go test ./...", wantApplied: true, wantHeader: "[compress: test]",
			output: "--- PASS: TestA\nFAIL\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, applied := CompressOutput(tc.command, tc.output)
			if applied != tc.wantApplied {
				t.Fatalf("applied = %v, want %v (output %q)", applied, tc.wantApplied, got)
			}
			if !tc.wantApplied {
				if got != tc.output {
					t.Errorf("uncompressed output changed: %q", got)
				}
				return
			}
			if !strings.Contains(got, tc.wantHeader) {
				t.Errorf("output missing header %q:\n%s", tc.wantHeader, got)
			}
		})
	}
}

// --- git diff ---

// TestCompressGitDiff_KeepsFirstFileHeaderAndHunk pins the regression the
// compressor documents: the summary header must be PREPENDED, never written
// over the first scanned line, and both the file header and the first hunk must
// survive.
func TestCompressGitDiff_KeepsFirstFileHeaderAndHunk(t *testing.T) {
	output := "diff --git a/foo.go b/foo.go\n" +
		"index 111..222 100644\n" +
		"--- a/foo.go\n" +
		"+++ b/foo.go\n" +
		"@@ -1,3 +1,4 @@\n" +
		" package foo\n" +
		"+added line\n" +
		"-removed line\n"

	got, applied := CompressOutput("git diff", output)
	if !applied {
		t.Fatal("git diff output was not compressed")
	}
	for _, want := range []string{"[compress: git diff]", "--- foo.go", "@@ -1,3 +1,4 @@", "+added line", "-removed line"} {
		if !strings.Contains(got, want) {
			t.Errorf("compressed diff missing %q:\n%s", want, got)
		}
	}
	if strings.Index(got, "--- foo.go") > strings.Index(got, "@@ -1,3 +1,4 @@") {
		t.Errorf("file header must precede its first hunk:\n%s", got)
	}
	if strings.Contains(got, "index 111..222") || strings.Contains(got, " package foo") {
		t.Errorf("metadata/context lines must be dropped:\n%s", got)
	}
}

// TestCompressGitDiff_NoHunksPassesThrough pins the fileCount==0 guard.
func TestCompressGitDiff_NoHunksPassesThrough(t *testing.T) {
	output := "diff --git a/x b/x\nindex 1..2 100644\n--- a/x\n+++ b/x\n"
	if got, applied := CompressOutput("git diff", output); applied {
		t.Errorf("hunk-less diff must not be compressed, got %q", got)
	}
}

// --- git status ---

// TestCompressGitStatus_CountsChangedAndUntracked pins the counters and the
// short status rendering.
func TestCompressGitStatus_CountsChangedAndUntracked(t *testing.T) {
	output := " M tracked.go\nA  added.go\n?? first.txt\n?? second.txt\n"
	got, applied := CompressOutput("git status --short", output)
	if !applied {
		t.Fatal("git status output was not compressed")
	}
	for _, want := range []string{"[compress: git status]", "Changed: 2 files", "Untracked: 2 files", "M tracked.go", "? first.txt"} {
		if !strings.Contains(got, want) {
			t.Errorf("compressed status missing %q:\n%s", want, got)
		}
	}
}

func TestCompressGitStatus_NoEntriesPassesThrough(t *testing.T) {
	if got, applied := CompressOutput("git status", "\n \n"); applied {
		t.Errorf("empty status must not be compressed, got %q", got)
	}
}

// --- git log ---

// TestCompressGitLog_DedupesAndStripsEmail pins log compaction.
func TestCompressGitLog_DedupesAndStripsEmail(t *testing.T) {
	output := "commit abc123\n" +
		"Author: Jane <jane@example.com>\n" +
		"Date: today\n" +
		"    fix the thing\n" +
		"    fix the thing\n" +
		"Jane <jane@example.com>\n"

	got, applied := CompressOutput("git log", output)
	if !applied {
		t.Fatal("git log output was not compressed")
	}
	if n := strings.Count(got, "fix the thing"); n != 1 {
		t.Errorf("duplicate log lines not deduped (%d copies):\n%s", n, got)
	}
	if strings.Contains(got, "jane@example.com") {
		t.Errorf("author email not stripped:\n%s", got)
	}
	if !strings.Contains(got, "Jane") {
		t.Errorf("author name lost:\n%s", got)
	}
}

func TestCompressGitLog_OnlyHeadersPassesThrough(t *testing.T) {
	if got, applied := CompressOutput("git log", "commit abc\nAuthor: x\nDate: y\n"); applied {
		t.Errorf("header-only log must not be compressed, got %q", got)
	}
}

// --- ls ---

// TestCompressLs_StripsMetadataAndCountsHidden pins the compact listing.
func TestCompressLs_StripsMetadataAndCountsHidden(t *testing.T) {
	output := "total 8\n" +
		"-rw-r--r-- 1 user staff 123 Jan  1 12:00 main.go\n" +
		"drwxr-xr-x 2 user staff  64 Jan  1 12:00 .hidden\n" +
		"short line\n"

	got, applied := CompressOutput("ls -la", output)
	if !applied {
		t.Fatal("ls output was not compressed")
	}
	for _, want := range []string{"[compress: ls]", "1 hidden file", "main.go", ".hidden", "short line"} {
		if !strings.Contains(got, want) {
			t.Errorf("compressed ls missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "total 8") || strings.Contains(got, "-rw-r--r--") || strings.Contains(got, "staff") {
		t.Errorf("ls metadata not stripped:\n%s", got)
	}
}

func TestCompressLs_OnlyTotalPassesThrough(t *testing.T) {
	if got, applied := CompressOutput("ls", "total 0\n"); applied {
		t.Errorf("empty listing must not be compressed, got %q", got)
	}
}

// --- grep ---

// TestCompressGrep_GroupsByFileAndTruncatesLongLines pins grouping, the long
// line clamp, and raw passthrough for non-matching lines.
func TestCompressGrep_GroupsByFileAndTruncatesLongLines(t *testing.T) {
	long := strings.Repeat("x", 250)
	output := "a.go:12:first match\n" +
		"a.go:13:second match\n" +
		"b.go:1:" + long + "\n" +
		"binary file matches\n"

	got, applied := CompressOutput("grep -rn match .", output)
	if !applied {
		t.Fatal("grep output was not compressed")
	}
	for _, want := range []string{"[compress: grep]", "a.go:", "b.go:", "2 files with 3 matches", "binary file matches"} {
		if !strings.Contains(got, want) {
			t.Errorf("compressed grep missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, long) {
		t.Error("over-long match line was not truncated")
	}
	if !strings.Contains(got, "...") {
		t.Error("truncation marker missing")
	}
	if n := strings.Count(got, "a.go:"); n != 1 {
		t.Errorf("file header repeated %d times, want 1:\n%s", n, got)
	}
}

func TestCompressGrep_NoMatchesPassesThrough(t *testing.T) {
	if got, applied := CompressOutput("grep foo .", "nothing matched\n"); applied {
		t.Errorf("match-less grep must not be compressed, got %q", got)
	}
}

// --- read (cat/head/tail) ---

// TestCompressRead_NumberedAndBlankCollapsed pins line numbering, the
// two-blank-line cap, and preservation of pre-numbered lines.
func TestCompressRead_NumberedAndBlankCollapsed(t *testing.T) {
	output := "package main\n\n\n\n\nfunc main() {}\n"
	got, applied := CompressOutput("cat main.go", output)
	if !applied {
		t.Fatal("cat output was not compressed")
	}
	if !strings.Contains(got, "[compress: read]") {
		t.Errorf("missing header:\n%s", got)
	}
	if !strings.Contains(got, "package main") || !strings.Contains(got, "func main() {}") {
		t.Errorf("content lost:\n%s", got)
	}
	if !strings.Contains(got, "    1  package main") {
		t.Errorf("first line not numbered:\n%s", got)
	}
	blanks := 0
	for _, line := range strings.Split(got, "\n") {
		if strings.TrimSpace(strings.TrimLeft(line, "0123456789")) == "" && strings.TrimSpace(line) != "[compress: read]" {
			blanks++
		}
	}
	if blanks > 2 {
		t.Errorf("blank lines not collapsed (found %d):\n%s", blanks, got)
	}

	preNumbered := "10  already numbered\n"
	out, applied := CompressOutput("head -1 x", preNumbered)
	if !applied {
		t.Fatal("pre-numbered output was not compressed")
	}
	if !strings.Contains(out, "10  already numbered") {
		t.Errorf("pre-numbered line was renumbered:\n%s", out)
	}
}

// TestCompressRead_EmptyPassesThrough pins the guard for content with no lines
// at all (the routing layer already rejects an empty output string).
func TestCompressRead_EmptyPassesThrough(t *testing.T) {
	if got, applied := compressRead(""); applied {
		t.Errorf("empty read must not be compressed, got %q", got)
	}
}

// --- test output ---

// TestCompressTestOutput_StripsPassesAndCompressesStack pins the test summary
// and the stack-trace compression.
func TestCompressTestOutput_StripsPassesAndCompressesStack(t *testing.T) {
	output := "=== RUN   TestA\n" +
		"--- PASS: TestA\n" +
		"--- FAIL: TestB\n" +
		"    b_test.go:10: first detail\n" +
		"    b_test.go:11: second detail\n" +
		"    b_test.go:12: third detail\n" +
		"FAIL\n"

	got, applied := CompressOutput("go test ./...", output)
	if !applied {
		t.Fatal("test output was not compressed")
	}
	for _, want := range []string{"[compress: test]", "1 passed, 1 failed", "--- FAIL: TestB", "first detail", "(stack trace compressed)"} {
		if !strings.Contains(got, want) {
			t.Errorf("compressed test output missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "--- PASS: TestA") {
		t.Errorf("PASS lines must be stripped:\n%s", got)
	}
	if strings.Contains(got, "second detail") || strings.Contains(got, "third detail") {
		t.Errorf("stack trace not compressed:\n%s", got)
	}
}

func TestCompressTestOutput_NoResultsPassesThrough(t *testing.T) {
	if got, applied := CompressOutput("go test ./...", "informational only\n"); applied {
		t.Errorf("result-less test output must not be compressed, got %q", got)
	}
}

// --- helpers ---

// TestCompressHelpers pins the shared formatting helpers.
func TestCompressHelpers(t *testing.T) {
	if got := formatCompressHeader("ls"); len(got) != 2 || got[0] != "" || got[1] != "[compress: ls]" {
		t.Errorf("formatCompressHeader = %#v", got)
	}
	cases := []struct {
		n    int
		word string
		want string
	}{
		{1, "file", "1 file"},
		{2, "file", "2 files"},
		{3, "match", "3 matches"},
		{0, "match", "0 matches"},
		{2, "hidden file", "2 hidden files"},
	}
	for _, tc := range cases {
		if got := pluralize(tc.n, tc.word); got != tc.want {
			t.Errorf("pluralize(%d, %q) = %q, want %q", tc.n, tc.word, got, tc.want)
		}
	}
	if got := fmtLineNum(7, "package x"); got != "    7  package x" {
		t.Errorf("fmtLineNum = %q", got)
	}
	if got := fmtLineNum(7, "42  already"); got != "42  already" {
		t.Errorf("fmtLineNum must preserve numbered lines, got %q", got)
	}
}
