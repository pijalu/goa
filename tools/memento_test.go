// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pijalu/goa/internal"
)

func TestMementoTool_Schema(t *testing.T) {
	tool := &MementoTool{}
	schema := tool.Schema()
	if schema.Name != "memento" {
		t.Errorf("expected 'memento', got %q", schema.Name)
	}
	if schema.Description == "" {
		t.Error("expected non-empty Description")
	}
	if schema.Schema == nil {
		t.Fatal("expected non-nil Schema")
	}
	props, ok := schema.Schema["properties"].(map[string]any)
	if !ok {
		t.Fatal("expected properties map")
	}
	action, ok := props["action"]
	if !ok {
		t.Fatal("expected action property")
	}
	actionMap, ok := action.(map[string]any)
	if !ok {
		t.Fatal("expected action map")
	}
	if actionMap["type"] != "string" {
		t.Errorf("expected action type string, got %v", actionMap["type"])
	}
}

func TestMementoTool_ShortDoc(t *testing.T) {
	tool := &MementoTool{}
	if tool.ShortDoc() == "" {
		t.Error("expected non-empty ShortDoc")
	}
}

func TestMementoTool_LongDoc(t *testing.T) {
	tool := &MementoTool{}
	if tool.LongDoc() == "" {
		t.Error("expected non-empty LongDoc")
	}
}

func TestMementoTool_Examples(t *testing.T) {
	tool := &MementoTool{}
	examples := tool.Examples()
	if len(examples) == 0 {
		t.Fatal("expected at least one example")
	}
	if !strings.Contains(examples[0], "list") {
		t.Errorf("expected list example, got: %s", examples[0])
	}
}

func TestMementoTool_IsRetryable(t *testing.T) {
	tool := &MementoTool{}
	if tool.IsRetryable(nil) {
		t.Error("expected IsRetryable to be false")
	}
}

func TestValidateName_Valid(t *testing.T) {
	tool := &MementoTool{}
	for _, name := range []string{"simple", "with-hyphens", "v2", "a"} {
		if err := tool.validateName(name); err != nil {
			t.Errorf("expected valid name %q, got error: %v", name, err)
		}
	}
}

func TestValidateName_Invalid(t *testing.T) {
	tool := &MementoTool{}
	for _, name := range []string{"UPPERCASE", "with spaces", "has.dot", ""} {
		err := tool.validateName(name)
		if err == nil {
			t.Errorf("expected error for name %q", name)
			continue
		}
		var toolErr *internal.ToolError
		if !errors.As(err, &toolErr) {
			t.Errorf("expected ToolError for %q, got %T: %v", name, err, err)
		}
	}
}

func TestFormatMemoryRead(t *testing.T) {
	result := formatMemoryRead("my-file", "content", "project")
	if !strings.Contains(result, "my-file") {
		t.Errorf("expected file name, got: %s", result)
	}
	if !strings.Contains(result, "from project") {
		t.Errorf("expected source, got: %s", result)
	}
	if !strings.Contains(result, "content") {
		t.Errorf("expected content, got: %s", result)
	}
}

func TestReadOrCreateMemory_Existing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.md")
	os.WriteFile(path, []byte("existing content"), 0644)

	tool := &MementoTool{}
	content := tool.readOrCreateMemory(path)
	if content != "existing content" {
		t.Errorf("expected existing content, got %q", content)
	}
}

func TestReadOrCreateMemory_New(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "new.md")

	tool := &MementoTool{}
	content := tool.readOrCreateMemory(path)
	if !strings.Contains(content, "created:") {
		t.Errorf("expected frontmatter with created date, got: %s", content)
	}
}

func TestFindSectionEnd_MidFile(t *testing.T) {
	tool := &MementoTool{}
	lines := []string{
		"## section1",
		"content",
		"## section2",
		"more content",
	}
	end := tool.findSectionEnd(lines, 1)
	if end != 2 {
		t.Errorf("expected end at index 2 (start of section2), got %d", end)
	}
}

func TestFindSectionEnd_LastSection(t *testing.T) {
	tool := &MementoTool{}
	lines := []string{
		"## section1",
		"content",
		"more",
	}
	end := tool.findSectionEnd(lines, 1)
	if end != 3 {
		t.Errorf("expected end at index 3 (end of file), got %d", end)
	}
}

func TestAppendToSection_NewSection(t *testing.T) {
	tool := &MementoTool{}
	existing := "## existing\n\ncontent\n"
	result := tool.appendToSection(existing, "new-section", "new content")
	if !strings.Contains(result, "## new-section") {
		t.Errorf("expected new section header, got: %s", result)
	}
	if !strings.Contains(result, "new content") {
		t.Errorf("expected new content, got: %s", result)
	}
}

func TestAppendToSection_ExistingSection(t *testing.T) {
	tool := &MementoTool{}
	existing := "## decisions\n\nold content\n## other\n\nother content\n"
	result := tool.appendToSection(existing, "decisions", "new decision")
	if !strings.Contains(result, "new decision") {
		t.Errorf("expected new content appended, got: %s", result)
	}
	// Should not duplicate the section header
	count := strings.Count(result, "## decisions")
	if count != 1 {
		t.Errorf("expected exactly 1 'decisions' header, got %d", count)
	}
}

func TestReadPreview_Empty(t *testing.T) {
	tool := &MementoTool{}
	dir := t.TempDir()
	path := filepath.Join(dir, "test.md")
	os.WriteFile(path, []byte(""), 0644)

	preview := tool.readPreview(path)
	if preview != "" {
		t.Errorf("expected empty preview, got %q", preview)
	}
}

func TestReadPreview_FirstLine(t *testing.T) {
	tool := &MementoTool{}
	dir := t.TempDir()
	path := filepath.Join(dir, "test.md")
	os.WriteFile(path, []byte("first line\nsecond line"), 0644)

	preview := tool.readPreview(path)
	if preview != "first line" {
		t.Errorf("expected 'first line', got %q", preview)
	}
}

func TestReadPreview_SkipsFrontmatter(t *testing.T) {
	tool := &MementoTool{}
	dir := t.TempDir()
	path := filepath.Join(dir, "test.md")
	os.WriteFile(path, []byte("---\ntitle: test\n---\n\ntitle: test"), 0644)

	preview := tool.readPreview(path)
	if !strings.Contains(preview, "title: test") {
		t.Errorf("expected title line, got %q", preview)
	}
}

func TestReadPreview_Truncates(t *testing.T) {
	tool := &MementoTool{}
	dir := t.TempDir()
	longLine := strings.Repeat("x", 200)
	path := filepath.Join(dir, "test.md")
	os.WriteFile(path, []byte(longLine), 0644)

	preview := tool.readPreview(path)
	if len(preview) > 85 {
		t.Errorf("expected truncated preview (<=85 chars), got %d: %q", len(preview), preview)
	}
}

func TestScanDir_Empty(t *testing.T) {
	tool := &MementoTool{}
	dir := t.TempDir()

	files := tool.scanDir(dir, false)
	if len(files) != 0 {
		t.Errorf("expected no files, got %d", len(files))
	}
}

func TestScanDir_WithFiles(t *testing.T) {
	tool := &MementoTool{}
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "test.md"), []byte("hello"), 0644)
	os.WriteFile(filepath.Join(dir, "other.md"), []byte("world"), 0644)
	os.WriteFile(filepath.Join(dir, "not-memory.txt"), []byte("skip"), 0644)

	files := tool.scanDir(dir, false)
	if len(files) != 2 {
		t.Errorf("expected 2 .md files, got %d", len(files))
	}
}

func TestScanDir_Global(t *testing.T) {
	tool := &MementoTool{}
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "global-note.md"), []byte("global"), 0644)

	files := tool.scanDir(dir, true)
	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(files))
	}
	if !strings.Contains(files[0].Name, "(global)") {
		t.Errorf("expected '(global)' suffix, got %q", files[0].Name)
	}
}

func TestExecute_MementoTool_InvalidJSON(t *testing.T) {
	tool := &MementoTool{}
	result, err := tool.Execute("{bad json")
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
	if result != "" {
		t.Errorf("expected empty result on error, got %q", result)
	}
	var toolErr *internal.ToolError
	if !errors.As(err, &toolErr) {
		t.Errorf("expected ToolError, got %T: %v", err, err)
	}
}

func TestExecute_MementoTool_UnknownAction(t *testing.T) {
	tool := &MementoTool{}
	result, err := tool.Execute(`{"action": "unknown"}`)
	if err == nil {
		t.Fatal("expected error for unknown action")
	}
	if result != "" {
		t.Errorf("expected empty result on error, got %q", result)
	}
	var toolErr *internal.ToolError
	if !errors.As(err, &toolErr) {
		t.Errorf("expected ToolError, got %T: %v", err, err)
	}
}

func TestExecute_MementoTool_List(t *testing.T) {
	dir := t.TempDir()
	tool := &MementoTool{ProjectDir: dir, GlobalDir: t.TempDir()}

	result, err := tool.Execute(`{"action": "list"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "No memory files found") {
		t.Errorf("expected empty list message, got: %s", result)
	}
}

func TestExecute_MementoTool_WriteRead(t *testing.T) {
	dir := t.TempDir()
	tool := &MementoTool{ProjectDir: dir, GlobalDir: t.TempDir()}

	// Write a memory file
	result, err := tool.Execute(`{"action": "write", "name": "test-note", "content": "hello world"}`)
	if err != nil {
		t.Fatalf("unexpected error on write: %v", err)
	}
	if !strings.Contains(result, "written to project memory") {
		t.Errorf("expected write confirmation, got: %s", result)
	}

	// Read it back
	result, err = tool.Execute(`{"action": "read", "name": "test-note"}`)
	if err != nil {
		t.Fatalf("unexpected error on read: %v", err)
	}
	if !strings.Contains(result, "hello world") {
		t.Errorf("expected 'hello world' in read output, got: %s", result)
	}
}

func TestExecute_MementoTool_Append(t *testing.T) {
	dir := t.TempDir()
	tool := &MementoTool{ProjectDir: dir, GlobalDir: t.TempDir()}

	// Write initial file
	tool.Execute(`{"action": "write", "name": "notes", "content": "initial"}`)

	// Append without section
	result, err := tool.Execute(`{"action": "append", "name": "notes", "content": "more text"}`)
	if err != nil {
		t.Fatalf("unexpected error on append: %v", err)
	}
	if !strings.Contains(result, "content appended") {
		t.Errorf("expected append confirmation, got: %s", result)
	}

	// Read to verify both contents
	result, _ = tool.Execute(`{"action": "read", "name": "notes"}`)
	if !strings.Contains(result, "initial") || !strings.Contains(result, "more text") {
		t.Errorf("expected both contents, got: %s", result)
	}
}

func TestExecute_MementoTool_Delete(t *testing.T) {
	dir := t.TempDir()
	tool := &MementoTool{ProjectDir: dir, GlobalDir: t.TempDir()}

	// Write then delete
	tool.Execute(`{"action": "write", "name": "temp-file", "content": "temporary"}`)
	result, err := tool.Execute(`{"action": "delete", "name": "temp-file"}`)
	if err != nil {
		t.Fatalf("unexpected error on delete: %v", err)
	}
	if !strings.Contains(result, "deleted") {
		t.Errorf("expected delete confirmation, got: %s", result)
	}

	// Verify file is gone
	_, err = tool.Execute(`{"action": "read", "name": "temp-file"}`)
	if err == nil {
		t.Error("expected error reading deleted file")
	}
}

func TestExecute_MementoTool_DeleteNotFound(t *testing.T) {
	tool := &MementoTool{ProjectDir: t.TempDir(), GlobalDir: t.TempDir()}

	_, err := tool.Execute(`{"action": "delete", "name": "nonexistent"}`)
	if err == nil {
		t.Fatal("expected error for deleting nonexistent file")
	}
	var toolErr *internal.ToolError
	if !errors.As(err, &toolErr) {
		t.Errorf("expected ToolError, got %T: %v", err, err)
	}
}

func TestExecute_MementoTool_ReadNotFound(t *testing.T) {
	tool := &MementoTool{ProjectDir: t.TempDir(), GlobalDir: t.TempDir()}

	_, err := tool.Execute(`{"action": "read", "name": "nonexistent"}`)
	if err == nil {
		t.Fatal("expected error for reading nonexistent file")
	}
	var toolErr *internal.ToolError
	if !errors.As(err, &toolErr) {
		t.Errorf("expected ToolError, got %T: %v", err, err)
	}
}

func TestExecute_MementoTool_WriteInvalidName(t *testing.T) {
	tool := &MementoTool{ProjectDir: t.TempDir(), GlobalDir: t.TempDir()}

	_, err := tool.Execute(`{"action": "write", "name": "UPPERCASE", "content": "test"}`)
	if err == nil {
		t.Fatal("expected error for invalid name")
	}
	var toolErr *internal.ToolError
	if !errors.As(err, &toolErr) {
		t.Errorf("expected ToolError, got %T: %v", err, err)
	}
}

func TestExecute_MementoTool_ReadGlobalFallback(t *testing.T) {
	projectDir := t.TempDir()
	globalDir := t.TempDir()
	tool := &MementoTool{ProjectDir: projectDir, GlobalDir: globalDir}

	// Write to global dir
	globalMemDir := filepath.Join(globalDir, "memory")
	os.MkdirAll(globalMemDir, 0755)
	os.WriteFile(filepath.Join(globalMemDir, "global-note.md"), []byte("global content"), 0644)

	// Read should find it in global (project doesn't have it)
	result, err := tool.Execute(`{"action": "read", "name": "global-note"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "(from global)") {
		t.Errorf("expected 'from global' in result, got: %s", result)
	}
}

func TestExecute_MementoTool_WriteDirCreation(t *testing.T) {
	// Use a non-existent subdirectory to test MkdirAll
	dir := filepath.Join(t.TempDir(), "deep", "path")
	tool := &MementoTool{ProjectDir: dir, GlobalDir: t.TempDir()}

	result, err := tool.Execute(`{"action": "write", "name": "new-file", "content": "test"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "written to project memory") {
		t.Errorf("expected write confirmation, got: %s", result)
	}
}
