// SPDX-License-Identifier: GPL-3.0-or-later

package tools

import (
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestReadFileDedupRepeatReturnsHint covers the E1.3 enhancement (ENHANCE.md):
// re-reading byte-identical content returns a short dedup hint instead of the
// full content, so an append-only context is not bloated with redundant file
// copies (session forensics: registry.go read 10×, tui.go 6× — ~5–6M wasted
// cache-read tokens). The dedup is content-keyed (sha256 of the rendered
// result), so a changed file or a different range is NOT deduped.
func TestReadFileDedupRepeatReturnsHint(t *testing.T) {
	dir, cleanup := tempDir(t)
	defer cleanup()
	filePath := filepath.Join(dir, "f.go")
	writeFile(t, filePath, "package a\n\nfunc A() {}\n")

	tool := &ReadFileTool{}
	input := `{"path": "` + filePath + `"}`

	first, err := tool.Execute(input)
	if err != nil {
		t.Fatalf("first read: %v", err)
	}
	if !strings.Contains(first, "func A()") {
		t.Fatalf("first read must return content: %s", first)
	}

	second, err := tool.Execute(input)
	if err != nil {
		t.Fatalf("second read: %v", err)
	}
	if strings.Contains(second, "func A()") {
		t.Errorf("repeat read of unchanged content must NOT return the body again:\n%s", second)
	}
	if !strings.Contains(second, "dedup") && !strings.Contains(second, "unchanged") {
		t.Errorf("repeat read must return a dedup hint:\n%s", second)
	}
}

// TestReadFileDedupChangedFileReturnsFresh verifies a modified file is not
// deduped: the content hash changes, so the read returns the new content.
func TestReadFileDedupChangedFileReturnsFresh(t *testing.T) {
	dir, cleanup := tempDir(t)
	defer cleanup()
	filePath := filepath.Join(dir, "f.go")
	writeFile(t, filePath, "package a\n\nfunc A() {}\n")

	tool := &ReadFileTool{}
	input := `{"path": "` + filePath + `"}`
	if _, err := tool.Execute(input); err != nil {
		t.Fatalf("first read: %v", err)
	}

	writeFile(t, filePath, "package a\n\nfunc A() {}\nfunc B() {}\n") // file grows
	got, err := tool.Execute(input)
	if err != nil {
		t.Fatalf("read after change: %v", err)
	}
	if !strings.Contains(got, "func B()") {
		t.Errorf("changed file must return fresh content (not deduped):\n%s", got)
	}
}

// TestReadFileDedupDifferentRangeNotDeduped verifies a read with different
// line-range params is not deduped against an earlier different-range read of
// the same file (different rendered content → different hash).
func TestReadFileDedupDifferentRangeNotDeduped(t *testing.T) {
	dir, cleanup := tempDir(t)
	defer cleanup()
	filePath := filepath.Join(dir, "f.go")
	writeFile(t, filePath, "l1\nl2\nl3\nl4\nl5\nl6\nl7\nl8\n")

	tool := &ReadFileTool{}
	if _, err := tool.Execute(`{"path": "` + filePath + `", "start_line": 1, "end_line": 4}`); err != nil {
		t.Fatalf("range read 1: %v", err)
	}
	got, err := tool.Execute(`{"path": "` + filePath + `", "start_line": 5, "end_line": 8}`)
	if err != nil {
		t.Fatalf("range read 2: %v", err)
	}
	if !strings.Contains(got, "l8") {
		t.Errorf("a different range must return its content (not deduped):\n%s", got)
	}
}

// TestReadFileDedupDisabledByConfig verifies the dedup can be turned off via
// config (tools.read_file.dedup: false) — every read returns full content.
func TestReadFileDedupDisabledByConfig(t *testing.T) {
	dir, cleanup := tempDir(t)
	defer cleanup()
	filePath := filepath.Join(dir, "f.go")
	writeFile(t, filePath, "package a\nfunc A() {}\n")

	off := false
	tool := &ReadFileTool{Config: FileToolConfig{Dedup: &off}}
	input := `{"path": "` + filePath + `"}`
	if _, err := tool.Execute(input); err != nil {
		t.Fatalf("first read: %v", err)
	}
	got, err := tool.Execute(input)
	if err != nil {
		t.Fatalf("second read: %v", err)
	}
	if !strings.Contains(got, "func A()") {
		t.Errorf("dedup disabled: repeat read must return full content:\n%s", got)
	}
}

// TestReadFileDedupCircularBufferEvictsOldest verifies the buffer is
// circular/bounded: once full, the oldest hash is evicted, so a file whose
// hash was evicted is returned in full again (not a hint).
func TestReadFileDedupCircularBufferEvictsOldest(t *testing.T) {
	dir, cleanup := tempDir(t)
	defer cleanup()

	tool := &ReadFileTool{}
	// Read cap+1 distinct files to overflow the buffer.
	firstPath := filepath.Join(dir, "f0.go")
	writeFile(t, firstPath, "package a\nfunc F0() {}\n")
	if _, err := tool.Execute(`{"path": "` + firstPath + `"}`); err != nil {
		t.Fatalf("seed f0: %v", err)
	}
	for i := 1; i <= readDedupCapacity; i++ {
		p := filepath.Join(dir, "g"+strconv.Itoa(i)+".go")
		writeFile(t, p, "package a\nfunc G"+strconv.Itoa(i)+"() {}\n")
		if _, err := tool.Execute(`{"path": "` + p + `"}`); err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
	}
	// f0's hash is now evicted; re-reading it must return full content again.
	got, err := tool.Execute(`{"path": "` + firstPath + `"}`)
	if err != nil {
		t.Fatalf("re-read f0: %v", err)
	}
	if !strings.Contains(got, "func F0()") {
		t.Errorf("evicted hash: re-read must return full content (not a hint):\n%s", got)
	}
}
