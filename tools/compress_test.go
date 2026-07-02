// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"strings"
	"testing"
)

func TestCompressOutput_EmptyOutput_ReturnsAsIs(t *testing.T) {
	out, ok := CompressOutput("ls", "")
	if ok {
		t.Error("Empty output should not be compressed")
	}
	if out != "" {
		t.Errorf("Output should be empty, got: %q", out)
	}
}

func TestCompressOutput_LsOutput_StripsPermissions(t *testing.T) {
	lsOutput := `total 100
-rw-r--r--  1 user  group  100 Jan 1 12:00 file1.go
-rw-r--r--  1 user  group  200 Jan 1 12:00 file2.go
drwxr-xr-x  2 user  group   64 Jan 1 12:00 subdir`
	out, ok := CompressOutput("ls -la", lsOutput)
	if !ok {
		t.Fatal("ls output should be compressed")
	}
	if strings.Contains(out, "-rw-r--r--") {
		t.Errorf("Compressed ls output should not contain permissions. Got: %q", out)
	}
	if !strings.Contains(out, "file1.go") {
		t.Errorf("Compressed ls output should contain file names. Got: %q", out)
	}
	if strings.Contains(out, "total") {
		t.Errorf("Compressed ls output should not contain 'total' line. Got: %q", out)
	}
}

func TestCompressOutput_GitDiff_ShowsChangedLines(t *testing.T) {
	diffOutput := `diff --git a/file.go b/file.go
--- a/file.go
+++ b/file.go
@@ -1,3 +1,4 @@
 line1
+new line
 line2
-line3`
	out, ok := CompressOutput("git diff", diffOutput)
	if !ok {
		t.Fatal("git diff output should be compressed")
	}
	if !strings.Contains(out, "+new line") {
		t.Errorf("Compressed diff should contain added lines. Got: %q", out)
	}
	if !strings.Contains(out, "-line3") {
		t.Errorf("Compressed diff should contain removed lines. Got: %q", out)
	}
	// Regression: the file path and hunk header must survive compression.
	// The old copy(result, summary) prepend silently dropped the first file's
	// path (--- file.go) and the first @@ hunk header.
	if !strings.Contains(out, "file.go") {
		t.Errorf("Compressed diff must retain the file path. Got: %q", out)
	}
	if !strings.Contains(out, "@@") {
		t.Errorf("Compressed diff must retain the hunk header. Got: %q", out)
	}
}

func TestCompressOutput_GitStatus_CompactFormat(t *testing.T) {
	statusOutput := " M modified.go\nA  newfile.go\n?? untracked.go"
	out, ok := CompressOutput("git status", statusOutput)
	if !ok {
		t.Fatal("git status output should be compressed")
	}
	// Output should reference compression
	if !strings.Contains(out, "git status") && !strings.Contains(out, "compress") {
		t.Errorf("Output should indicate compression. Got: %q", out)
	}
	if len(out) < 10 {
		t.Errorf("Compressed output too short: %q", out)
	}
}

func TestCompressOutput_GrepOutput_TruncatesLines(t *testing.T) {
	grepOutput := `file1.go:10:func hello() {}
file2.go:20:func world() {}`
	out, ok := CompressOutput("grep func", grepOutput)
	if !ok {
		t.Fatal("grep output should be compressed")
	}
	if !strings.Contains(out, "func hello") {
		t.Errorf("Compressed grep should contain match content. Got: %q", out)
	}
}

func TestCompressOutput_UnknownCommand_ReturnsAsIs(t *testing.T) {
	input := "some random output"
	out, ok := CompressOutput("some_unknown_command", input)
	if ok {
		t.Error("Unknown command output should not be compressed")
	}
	if out != input {
		t.Errorf("Output should match input, got: %q", out)
	}
}

func TestCompressOutput_LongLineGrep_Truncates(t *testing.T) {
	longLine := "file.go:1:" + strings.Repeat("x", 500)
	out, ok := CompressOutput("grep pattern", longLine)
	if !ok {
		t.Fatal("grep output should be compressed")
	}
	if len(out) > 300 {
		t.Errorf("Long grep lines should be truncated. Got %d chars", len(out))
	}
}

func TestCompressOutput_TestOutput_StripPasses(t *testing.T) {
	testOutput := `=== RUN   TestFoo
--- PASS: TestFoo (0.00s)
=== RUN   TestBar
--- FAIL: TestBar (0.00s)
    test.go:42: expected value, got other
FAIL`
	out, ok := CompressOutput("go test", testOutput)
	if !ok {
		t.Fatal("test output should be compressed")
	}
	if strings.Contains(out, "PASS: TestFoo") {
		t.Errorf("Compressed test output should strip PASS lines. Got: %q", out)
	}
	if !strings.Contains(out, "FAIL") {
		t.Errorf("Compressed test output should show FAIL lines. Got: %q", out)
	}
}

func TestCompressOutput_CatOutput_LineNumbers(t *testing.T) {
	catOutput := "hello\nworld\nfoo"
	out, ok := CompressOutput("cat file.go", catOutput)
	if !ok {
		t.Fatal("cat output should be compressed")
	}
	if !strings.Contains(out, "hello") || !strings.Contains(out, "world") {
		t.Errorf("Compressed cat should contain all lines. Got: %q", out)
	}
}

func TestCompressOutput_GitLog_Deduplicates(t *testing.T) {
	logOutput := `commit abc123
Author: User
Date:   Mon Jan 1 12:00:00 2024
    first commit

commit def456
Author: User
Date:   Mon Jan 1 13:00:00 2024
    second commit`
	out, ok := CompressOutput("git log", logOutput)
	if !ok {
		t.Fatal("git log output should be compressed")
	}
	if strings.Contains(out, "Author:") {
		t.Errorf("Compressed log should not contain Author lines. Got: %q", out)
	}
	if strings.Contains(out, "Date:") {
		t.Errorf("Compressed log should not contain Date lines. Got: %q", out)
	}
	if !strings.Contains(out, "commit") && !strings.Contains(out, "first") {
		t.Errorf("Compressed log should contain commit messages. Got: %q", out)
	}
}
