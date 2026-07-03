// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/internal/agentic"
)

// WriteFileTool creates or overwrites a file with the given content.
//
// IMPORTANT: write MUST NOT use fuzzy filename matching. Fuzzy path
// resolution can redirect writes to the wrong file, causing irreversible
// data loss. Unlike edit/read which handle existing files, write's
// destructive nature requires exact path fidelity.
type WriteFileTool struct {
	WorktreeMgr        *internal.WorktreeManager
	ProjectDir         string
	GitStager          *GitStager
	// FileChangeNotifier, when set, is called after every successful file
	// write with the resolved (absolute) path. Tools like SmartSearch use
	// this to trigger background index updates.
	FileChangeNotifier func(path string)
}

// Schema returns the tool schema for write.
func (t *WriteFileTool) Schema() agentic.ToolSchema {
	return agentic.ToolSchema{
		Name:        "write",
		Description: "Create a new file or completely overwrite an existing file with new content. Creates parent directories by default.",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "Path to the file to write",
				},
				"content": map[string]any{
					"type":        "string",
					"description": "Content to write to the file",
				},
			},
			"required": []string{"path", "content"},
		},
	}
}

// writeFileParams holds the parsed input for WriteFileTool.
type writeFileParams struct {
	Path       string `json:"path"`
	Content    string `json:"content"`
	CreateDirs *bool  `json:"create_dirs"`
}

// Execute writes content to a file.
func (t *WriteFileTool) Execute(input string) (string, error) {
	p, err := parseWriteFileParams(input)
	if err != nil {
		return "", err
	}

	resolvedPath, originalPath, err := ResolveFileToolPath(t.WorktreeMgr, p.Path)
	if err != nil {
		return "", &internal.ToolError{
			Tool: "write", Type: "protected_path",
			Detail:   fmt.Sprintf("Cannot write to %q", p.Path),
			HintText: "Choose a path outside .goa/ and .git/ directories.",
		}
	}

	processedContent := strings.ReplaceAll(p.Content, "\\n", "\n")
	if err := t.ensureParentDirs(resolvedPath, p.CreateDirs); err != nil {
		return "", err
	}

	t.stageIfExists(resolvedPath)

	if err := os.WriteFile(resolvedPath, []byte(processedContent), 0644); err != nil {
		return "", formatWriteError(originalPath, err)
	}

	if t.FileChangeNotifier != nil {
		t.FileChangeNotifier(resolvedPath)
	}

	return buildWritePreview(originalPath, processedContent), nil
}

func parseWriteFileParams(input string) (writeFileParams, error) {
	var p writeFileParams
	if err := json.Unmarshal([]byte(input), &p); err != nil {
		return writeFileParams{}, &internal.ToolError{
			Tool: "write", Type: "invalid_input",
			Detail:   fmt.Sprintf("Cannot parse parameters: %v", err),
			HintText: "Ensure your input is valid JSON with the required fields.",
		}
	}
	if p.Path == "" {
		return writeFileParams{}, &internal.ToolError{
			Tool: "write", Type: "missing_path",
			Detail: "No path provided", HintText: "Provide a file path in the 'path' field.",
		}
	}
	if p.CreateDirs == nil {
		p.CreateDirs = boolPtr(true)
	}
	return p, nil
}

func boolPtr(b bool) *bool {
	return &b
}

func (t *WriteFileTool) ensureParentDirs(resolvedPath string, createDirs *bool) error {
	if createDirs == nil || !*createDirs {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(resolvedPath), 0755); err != nil {
		return &internal.ToolError{
			Tool: "write", Type: "mkdir_failed",
			Detail:   fmt.Sprintf("Cannot create parent directories: %v", err),
			HintText: "Set create_dirs=false if you want to manually create the directory.",
		}
	}
	return nil
}

func (t *WriteFileTool) stageIfExists(resolvedPath string) {
	if _, err := os.Stat(resolvedPath); err == nil && t.GitStager != nil {
		t.GitStager.StageBeforeEdit(resolvedPath, t.ProjectDir)
	}
}

func formatWriteError(path string, err error) error {
	if os.IsPermission(err) {
		return &internal.ToolError{
			Tool: "write", Type: "permission_denied",
			Detail:   fmt.Sprintf("Permission denied: %s", path),
			HintText: "Check file permissions or use a different path.",
		}
	}
	return &internal.ToolError{
		Tool: "write", Type: "write_error",
		Detail:   fmt.Sprintf("Error writing %s: %v", path, err),
		HintText: "Ensure the path is valid and writable.",
	}
}

func buildWritePreview(path, content string) string {
	lines := strings.Split(content, "\n")
	lineCount := len(lines)
	preview := fmt.Sprintf("[write: %s]\n✓ Written — %d bytes, %d lines\n", path, len(content), lineCount)
	previewLines := 10
	if len(lines) < previewLines {
		previewLines = len(lines)
	}
	if previewLines == 0 {
		return preview
	}
	preview += "```\n"
	for i := 0; i < previewLines; i++ {
		preview += lines[i] + "\n"
	}
	preview += "```\n"
	if len(lines) > previewLines {
		preview += fmt.Sprintf("… %d more lines (Ctrl+O to expand)\n", len(lines)-previewLines)
	}
	return preview
}

// IsRetryable returns false.
func (t *WriteFileTool) IsRetryable(err error) bool { return false }

// Access returns WritePath for the file being written.
func (t *WriteFileTool) Access(input string) ToolAccess {
	var p writeFileParams
	if err := json.Unmarshal([]byte(input), &p); err != nil {
		return ToolAccess{}
	}
	return ToolAccess{WritePaths: []string{p.Path}}
}

//go:embed writefile.short.md writefile.long.md
var writefileDocs embed.FS

func (t *WriteFileTool) ShortDoc() string { return readDoc(writefileDocs, "writefile.short.md") }
func (t *WriteFileTool) LongDoc() string  { return readDoc(writefileDocs, "writefile.long.md") }

func (t *WriteFileTool) Examples() []string {
	return []string{
		`{"path": "src/main.go", "content": "package main\n\nfunc main() {\n\tprintln(\"hello\")\n}"}`,
		`{"path": "docs/notes.md", "content": "# Notes", "create_dirs": true}`,
	}
}
