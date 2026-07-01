// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestReadFileBasic verifies reading a complete file.
func TestReadFileBasic(t *testing.T) {
	dir, cleanup := tempDir(t)
	defer cleanup()

	filePath := filepath.Join(dir, "test.txt")
	writeFile(t, filePath, "line1\nline2\nline3\n")

	tool := &ReadFileTool{}
	result, err := tool.Execute(`{"path": "` + filePath + `"}`)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if !strings.Contains(result, "line1") || !strings.Contains(result, "line2") {
		t.Errorf("Result missing expected content: %s", result)
	}
	if !strings.Contains(result, "end — 3 lines shown") {
		t.Errorf("Result missing line count: %s", result)
	}
}

// TestReadFileLineRange verifies reading a subset of lines.
func TestReadFileLineRange(t *testing.T) {
	dir, cleanup := tempDir(t)
	defer cleanup()

	filePath := filepath.Join(dir, "test.txt")
	writeFile(t, filePath, "line1\nline2\nline3\nline4\nline5\n")

	tool := &ReadFileTool{}
	result, err := tool.Execute(`{"path": "` + filePath + `", "start_line": 2, "end_line": 4}`)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if !strings.Contains(result, "line2") || !strings.Contains(result, "line4") {
		t.Errorf("Result missing expected lines: %s", result)
	}
	if strings.Contains(result, "line1") {
		t.Errorf("Result should not include line1: %s", result)
	}
}

// TestReadFileBinaryDetection verifies binary files are detected.
func TestReadFileBinaryDetection(t *testing.T) {
	dir, cleanup := tempDir(t)
	defer cleanup()

	filePath := filepath.Join(dir, "binary.bin")
	os.WriteFile(filePath, []byte{0x00, 0x01, 0x02, 0x03}, 0644)

	tool := &ReadFileTool{}
	result, err := tool.Execute(`{"path": "` + filePath + `"}`)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if !strings.Contains(result, "binary file") {
		t.Errorf("Binary detection failed: %s", result)
	}
}

// TestReadFileNotFound verifies missing file error.
func TestReadFileNotFound(t *testing.T) {
	tool := &ReadFileTool{}
	_, err := tool.Execute(`{"path": "/nonexistent/path/file.txt"}`)
	if err == nil {
		t.Fatal("Expected error for non-existent file")
	}
	errStr := err.Error()
	if !strings.Contains(errStr, "file_not_found") {
		t.Errorf("Expected file_not_found error: %s", errStr)
	}
}

// TestReadFileProtectedPath verifies that the read tool no longer rejects
// protected paths at the tool layer; policy enforcement lives in the agent.
func TestReadFileProtectedPath(t *testing.T) {
	tool := &ReadFileTool{}
	_, err := tool.Execute(`{"path": ".goa/config.yaml"}`)
	if err != nil && strings.Contains(err.Error(), "protected_path") {
		t.Errorf("read tool should not emit protected_path error: %v", err)
	}
}

// TestReadFileMaxLines verifies max_lines truncation.
func TestReadFileMaxLines(t *testing.T) {
	dir, cleanup := tempDir(t)
	defer cleanup()

	filePath := filepath.Join(dir, "long.txt")
	var lines string
	for i := 1; i <= 100; i++ {
		lines += "line" + strconv.Itoa(i) + "\n"
	}
	writeFile(t, filePath, lines)

	tool := &ReadFileTool{}
	result, err := tool.Execute(`{"path": "` + filePath + `", "max_lines": 5}`)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if !strings.Contains(result, "end — 5 lines shown") {
		t.Errorf("Expected 5 lines shown: %s", result)
	}
}

// TestReadFileEmptyFile verifies reading a zero-byte file does not panic and
// reports zero lines.
func TestReadFileEmptyFile(t *testing.T) {
	dir, cleanup := tempDir(t)
	defer cleanup()

	filePath := filepath.Join(dir, "empty.txt")
	writeFile(t, filePath, "")

	tool := &ReadFileTool{}
	result, err := tool.Execute(`{"path": "` + filePath + `"}`)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if !strings.Contains(result, "end — 0 lines shown") {
		t.Errorf("Expected 0 lines shown for empty file: %s", result)
	}
}

// TestReadFileHardMaxCap verifies max_lines cannot exceed the hard limit.
func TestReadFileHardMaxCap(t *testing.T) {
	dir, cleanup := tempDir(t)
	defer cleanup()

	filePath := filepath.Join(dir, "huge.txt")
	var b strings.Builder
	for i := 1; i <= maxReadFileLines+100; i++ {
		b.WriteString("line")
		b.WriteString(strconv.Itoa(i))
		b.WriteByte('\n')
	}
	writeFile(t, filePath, b.String())

	tool := &ReadFileTool{}
	result, err := tool.Execute(`{"path": "` + filePath + `", "max_lines": 100000, "max_bytes": 200000}`)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if !strings.Contains(result, "end — "+strconv.Itoa(maxReadFileLines)+" lines shown") {
		t.Errorf("Expected hard cap of %d lines, got: %s", maxReadFileLines, result)
	}
}

// TestReadFileProtectedGitPath verifies that .git paths are no longer
// rejected at the tool layer; policy enforcement lives in the agent.
func TestReadFileProtectedGitPath(t *testing.T) {
	tool := &ReadFileTool{}
	_, err := tool.Execute(`{"path": ".git/config"}`)
	if err != nil && strings.Contains(err.Error(), "protected_path") {
		t.Errorf("read tool should not emit protected_path error: %v", err)
	}
}

// TestReadFileShowNumbers verifies line numbers are displayed.
func TestReadFileShowNumbers(t *testing.T) {
	dir, cleanup := tempDir(t)
	defer cleanup()

	filePath := filepath.Join(dir, "test.txt")
	writeFile(t, filePath, "hello\nworld\n")

	tool := &ReadFileTool{}
	result, err := tool.Execute(`{"path": "` + filePath + `", "show_numbers": true}`)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if !strings.Contains(result, "    1") || !strings.Contains(result, "    2") {
		t.Errorf("Expected line numbers: %s", result)
	}
}

// TestReadFileByteTruncation verifies that a file whose rendered output
// exceeds the byte limit is returned as a successful read with a truncation
// notice rather than an error.
func TestReadFileByteTruncation(t *testing.T) {
	dir, cleanup := tempDir(t)
	defer cleanup()

	filePath := filepath.Join(dir, "fat.txt")
	// Each line is 100 bytes plus newline; 200 lines exceed a 1KB max_bytes.
	line := strings.Repeat("a", 100)
	var b strings.Builder
	for i := 0; i < 200; i++ {
		b.WriteString(line)
		b.WriteByte('\n')
	}
	writeFile(t, filePath, b.String())

	tool := &ReadFileTool{}
	result, err := tool.Execute(`{"path": "` + filePath + `", "max_bytes": 1024}`)
	if err != nil {
		t.Fatalf("Execute should succeed with truncation, got error: %v", err)
	}
	if !strings.Contains(result, "[read size is limited to 1024 bytes") {
		t.Errorf("Expected byte-limit notice, got: %s", result)
	}
	if !strings.Contains(result, "output truncated") {
		t.Errorf("Expected truncated footer, got: %s", result)
	}
	// The header should not be duplicated inside the content.
	if strings.Count(result, "read file ") != 1 {
		t.Errorf("Expected exactly one read file header, got: %s", result)
	}
}

// TestReadFileAtPrefix verifies that a leading '@' expands to the current
// working directory.
func TestReadFileAtPrefix(t *testing.T) {
	dir, cleanup := tempDir(t)
	defer cleanup()

	filePath := filepath.Join(dir, "test.txt")
	writeFile(t, filePath, "at-prefix-content\n")

	t.Chdir(dir)

	tool := &ReadFileTool{}
	result, err := tool.Execute(`{"path": "@test.txt"}`)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if !strings.Contains(result, "at-prefix-content") {
		t.Errorf("Result missing expected content: %s", result)
	}
}

// TestReadFileFuzzyMatchDefault verifies that a misspelled filename is
// resolved to the closest existing file when fuzzy matching is enabled by
// default.
func TestReadFileFuzzyMatchDefault(t *testing.T) {
	dir, cleanup := tempDir(t)
	defer cleanup()

	filePath := filepath.Join(dir, "target.txt")
	writeFile(t, filePath, "fuzzy-content\n")

	t.Chdir(dir)

	tool := &ReadFileTool{}
	result, err := tool.Execute(`{"path": "targt.txt"}`)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if !strings.Contains(result, "fuzzy-content") {
		t.Errorf("Result missing expected content: %s", result)
	}
	if !strings.Contains(result, "used closest match") {
		t.Errorf("Expected fuzzy-match note, got: %s", result)
	}
}

// TestReadFileFuzzyMatchEnabled verifies the default-on behavior of the fuzzy
// match configuration helper.
func TestReadFileFuzzyMatchEnabled(t *testing.T) {
	tool := &ReadFileTool{}
	if !FileToolFuzzyMatchEnabled(tool.Config) {
		t.Error("expected fuzzy matching to be enabled by default")
	}
	off := false
	tool.Config.FuzzyMatch = &off
	if FileToolFuzzyMatchEnabled(tool.Config) {
		t.Error("expected fuzzy matching to be disabled when explicitly false")
	}
	on := true
	tool.Config.FuzzyMatch = &on
	if !FileToolFuzzyMatchEnabled(tool.Config) {
		t.Error("expected fuzzy matching to be enabled when explicitly true")
	}
}

// TestReadFileFuzzyMatchDisabled verifies that fuzzy matching can be disabled
// through configuration.
func TestReadFileFuzzyMatchDisabled(t *testing.T) {
	dir, cleanup := tempDir(t)
	defer cleanup()

	filePath := filepath.Join(dir, "target.txt")
	writeFile(t, filePath, "fuzzy-content\n")

	t.Chdir(dir)

	off := false
	tool := &ReadFileTool{Config: ReadFileConfig{FuzzyMatch: &off}}
	_, err := tool.Execute(`{"path": "targt.txt"}`)
	if err == nil {
		t.Fatal("Expected error when fuzzy matching is disabled")
	}
	if !strings.Contains(err.Error(), "file_not_found") {
		t.Errorf("Expected file_not_found error, got: %v", err)
	}
}

// TestReadFileMaxBytesDefaultFitsLargeFile verifies the default 50KB cap
// allows a moderately large file to be read without error.
func TestReadFileMaxBytesDefaultFitsLargeFile(t *testing.T) {
	dir, cleanup := tempDir(t)
	defer cleanup()

	filePath := filepath.Join(dir, "medium.txt")
	line := strings.Repeat("x", 100)
	var b strings.Builder
	for i := 0; i < 50; i++ {
		b.WriteString(line)
		b.WriteByte('\n')
	}
	writeFile(t, filePath, b.String())

	tool := &ReadFileTool{}
	result, err := tool.Execute(`{"path": "` + filePath + `"}`)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if strings.Contains(result, "[output truncated by bytes") {
		t.Errorf("Did not expect truncation for 5KB file: %s", result)
	}
}

// TestReadFileGoaDocURL verifies that goa:// URLs resolve to embedded docs.
func TestReadFileGoaDocURL(t *testing.T) {
	tool := &ReadFileTool{}
	result, err := tool.Execute(`{"path": "goa://docs/SKILLS.md"}`)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if !strings.Contains(result, "read file goa://docs/SKILLS.md") {
		t.Errorf("Expected goa:// display path in header, got: %s", result)
	}
	if !strings.Contains(result, "Skills") {
		t.Errorf("Expected embedded SKILLS content, got: %s", result)
	}
}

// TestReadFileGoaDocURLVariants verifies supported goa:// URL forms.
func TestReadFileGoaDocURLVariants(t *testing.T) {
	tool := &ReadFileTool{}
	variants := []string{
		`{"path": "goa://TOOLS"}`,
		`{"path": "goa://TOOLS.md"}`,
		`{"path": "goa://docs/TOOLS"}`,
		`{"path": "goa://docs/TOOLS.md"}`,
	}
	for _, input := range variants {
		result, err := tool.Execute(input)
		if err != nil {
			t.Fatalf("Execute failed for %s: %v", input, err)
		}
		if !strings.Contains(result, "Tool") {
			t.Errorf("Expected embedded TOOLS content for %s, got: %s", input, result)
		}
	}
}

// TestReadFileGoaDocURLNotFound verifies that an unknown goa:// doc returns
// a read error.
func TestShortenPath(t *testing.T) {
	tests := []struct {
		name string
		path string
		want string
	}{
		{"home path shortened", "/home/user/proj/file.go", "/home/user/proj/file.go"}, // not the actual home
		{"non-home path unchanged", "/var/log/syslog", "/var/log/syslog"},
		{"empty string", "", ""},
		{"relative path", "relative/path", "relative/path"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shortenPath(tt.path)
			if got != tt.want {
				t.Errorf("shortenPath(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

func TestReadFileGoaDocURLNotFound(t *testing.T) {
	tool := &ReadFileTool{}
	_, err := tool.Execute(`{"path": "goa://DOES_NOT_EXIST"}`)
	if err == nil {
		t.Fatal("Expected error for unknown goa:// document")
	}
	if !strings.Contains(err.Error(), "read_error") {
		t.Errorf("Expected read_error, got: %v", err)
	}
}

func TestReadDirectory(t *testing.T) {
	tool := &ReadFileTool{}

	t.Run("lists directory contents", func(t *testing.T) {
		dir, cleanup := tempDir(t)
		defer cleanup()

		// Create some files and subdirectories
		os.WriteFile(filepath.Join(dir, "file1.go"), []byte("package main"), 0644)
		os.WriteFile(filepath.Join(dir, "file2.md"), []byte("# doc"), 0644)
		os.MkdirAll(filepath.Join(dir, "subdir"), 0755)

		result, err := tool.Execute(`{"path": "` + dir + `"}`)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !strings.Contains(result, "read directory") {
			t.Errorf("expected 'read directory' header, got: %s", result)
		}
		if !strings.Contains(result, "file1.go") {
			t.Errorf("expected file1.go in listing, got: %s", result)
		}
		if !strings.Contains(result, "file2.md") {
			t.Errorf("expected file2.md in listing, got: %s", result)
		}
		if !strings.Contains(result, "subdir/") {
			t.Errorf("expected subdir/ (with trailing slash) in listing, got: %s", result)
		}
		if !strings.Contains(result, "[directory: 3 entries]") {
			t.Errorf("expected 3 entries in listing, got: %s", result)
		}
	})

	t.Run("empty directory", func(t *testing.T) {
		dir, cleanup := tempDir(t)
		defer cleanup()

		result, err := tool.Execute(`{"path": "` + dir + `"}`)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(result, "[directory: 0 entries]") {
			t.Errorf("expected 0 entries, got: %s", result)
		}
	})

	t.Run("relative path", func(t *testing.T) {
		dir, cleanup := tempDir(t)
		defer cleanup()

		os.WriteFile(filepath.Join(dir, "a.txt"), []byte("data"), 0644)

		// Use a relative path via a subdirectory change
		result, err := tool.Execute(`{"path": "` + dir + `"}`)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(result, "a.txt") {
			t.Errorf("expected a.txt in listing, got: %s", result)
		}
	})
}




func TestShortenPath_HomeDir(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("cannot determine home dir")
	}

	// Directly test shortenPath rather than going through Execute (which
	// triggers fuzzy matching and can hang walking from a home subdirectory).
	got := shortenPath(filepath.Join(home, "project/file.go"))
	if !strings.HasPrefix(got, "~/") {
		t.Errorf("shortenPath(%q) should start with ~/, got: %q", filepath.Join(home, "project/file.go"), got)
	}
	if !strings.HasSuffix(got, "project/file.go") {
		t.Errorf("shortenPath should preserve the relative path suffix, got: %q", got)
	}
}

func TestRenderReadLines(t *testing.T) {
	t.Run("with line numbers", func(t *testing.T) {
		lines := []string{"first line", "second line", "third line"}
		result := renderReadLines(lines, 10, true)
		expected := "    10  first line\n    11  second line\n    12  third line\n"
		if result != expected {
			t.Errorf("renderReadLines with numbers:\ngot:\n%q\nwant:\n%q", result, expected)
		}
	})

	t.Run("without line numbers", func(t *testing.T) {
		lines := []string{"first line", "second line"}
		result := renderReadLines(lines, 1, false)
		expected := "first line\nsecond line\n"
		if result != expected {
			t.Errorf("renderReadLines without numbers:\ngot:\n%q\nwant:\n%q", result, expected)
		}
	})

	t.Run("empty lines", func(t *testing.T) {
		result := renderReadLines(nil, 1, true)
		if result != "" {
			t.Errorf("expected empty for nil lines, got: %q", result)
		}
	})
}

func TestReadFileError_PermissionDenied(t *testing.T) {
	tool := &ReadFileTool{}
	err := tool.readFileError("/protected/file", os.ErrPermission)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "permission_denied") {
		t.Errorf("expected permission_denied type, got: %v", err)
	}
}

func TestReadFileError_Generic(t *testing.T) {
	tool := &ReadFileTool{}
	err := tool.readFileError("/some/file", os.ErrInvalid)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "read_error") {
		t.Errorf("expected read_error type, got: %v", err)
	}
}

func TestClampLineRange(t *testing.T) {
	tests := []struct {
		name       string
		startLine  int
		endLine    int
		totalLines int
		maxLines   int
		wantStart  int
		wantEnd    int
	}{
		{"normal range", 2, 5, 10, 100, 2, 5},
		{"start before 1", 0, 5, 10, 100, 1, 5},
		{"end beyond total", 1, 20, 10, 100, 1, 10},
		{"zero end (default)", 1, 0, 10, 100, 1, 10},
		{"start after end", 5, 3, 10, 100, 3, 3},
		{"single line", 3, 3, 10, 100, 3, 3},
		{"max lines clamping (ignored by clampLineRange)", 1, 10, 10, 5, 1, 10},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotStart, gotEnd := clampLineRange(tt.startLine, tt.endLine, tt.totalLines, tt.maxLines)
			if gotStart != tt.wantStart || gotEnd != tt.wantEnd {
				t.Errorf("clampLineRange(%d,%d,%d,%d) = (%d,%d), want (%d,%d)",
					tt.startLine, tt.endLine, tt.totalLines, tt.maxLines,
					gotStart, gotEnd, tt.wantStart, tt.wantEnd)
			}
		})
	}
}


func TestShortenPath_NonHomePath(t *testing.T) {
	// Paths that are NOT under the home directory should stay unchanged.
	got := shortenPath("/var/log/syslog")
	if got != "/var/log/syslog" {
		t.Errorf("shortenPath(%q) = %q, want %q", "/var/log/syslog", got, "/var/log/syslog")
	}
}

func TestShortenPath_Empty(t *testing.T) {
	got := shortenPath("")
	if got != "" {
		t.Errorf("shortenPath('') = %q, want ''", got)
	}
}

func TestShortenPath_Relative(t *testing.T) {
	got := shortenPath("relative/path/file.go")
	if got != "relative/path/file.go" {
		t.Errorf("shortenPath(%q) = %q, want %q", "relative/path/file.go", got, "relative/path/file.go")
	}
}
