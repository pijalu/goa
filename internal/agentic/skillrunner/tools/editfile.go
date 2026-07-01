// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	agentic "github.com/pijalu/goa/internal/agentic"
)

// editFileTool implements agentic.Tool for editing files.
type editFileTool struct {
	workDir string
	logger  *agentic.Logger
}

// NewEditFileTool creates a new editFileTool.
func NewEditFileTool(workDir string, logger *agentic.Logger) agentic.Tool {
	return &editFileTool{
		workDir: workDir,
		logger:  logger,
	}
}

func (t *editFileTool) IsRetryable(err error) bool { return false }

func (t *editFileTool) Schema() agentic.ToolSchema {
	return agentic.ToolSchema{
		Name:        "edit",
		Description: "Edit a file by replacing text, adding lines, or removing lines.",
		Schema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"path": map[string]interface{}{
					"type":        "string",
					"description": "Path to the file to edit (relative to work directory)",
				},
				"old_text": map[string]interface{}{
					"type":        "string",
					"description": "Text to replace (used with new_text)",
				},
				"new_text": map[string]interface{}{
					"type":        "string",
					"description": "Replacement text (used with old_text)",
				},
				"add_after_line": map[string]interface{}{
					"type":        "integer",
					"description": "Add content after this line (1-indexed, used with add_content)",
				},
				"add_content": map[string]interface{}{
					"type":        "string",
					"description": "Content to add (used with add_after_line)",
				},
				"remove_line": map[string]interface{}{
					"type":        "integer",
					"description": "Line to remove (1-indexed)",
				},
			},
			"required": []string{"path"},
		},
	}
}

func (t *editFileTool) Execute(input string) (string, error) {
	var params struct {
		Path         string `json:"path"`
		OldText      string `json:"old_text"`
		NewText      string `json:"new_text"`
		AddAfterLine int    `json:"add_after_line"`
		AddContent   string `json:"add_content"`
		RemoveLine   int    `json:"remove_line"`
	}
	if err := json.Unmarshal([]byte(input), &params); err != nil {
		return "", fmt.Errorf("parse input: %w", err)
	}

	// Sanitize path
	cleanPath := filepath.Clean(filepath.Join(t.workDir, params.Path))
	if !strings.HasPrefix(cleanPath, filepath.Clean(t.workDir)) {
		return "", fmt.Errorf("path traversal detected: %s", params.Path)
	}

	// Read existing content
	content, err := os.ReadFile(cleanPath)
	if err != nil {
		return "", fmt.Errorf("read file: %w", err)
	}
	lines := strings.Split(string(content), "\n")

	// Apply edits
	switch {
	case params.OldText != "":
		// Replace text (NewText can be empty to clear)
		oldContent := string(content)
		newContent := strings.Replace(oldContent, params.OldText, params.NewText, 1)
		if newContent == oldContent {
			return "", fmt.Errorf("old_text not found in file")
		}
		content = []byte(newContent)
	case params.AddAfterLine > 0 && params.AddContent != "":
		// Add content after line
		if params.AddAfterLine > len(lines) {
			return "", fmt.Errorf("add_after_line %d exceeds total lines %d", params.AddAfterLine, len(lines))
		}
		newLines := append(lines[:params.AddAfterLine], append([]string{params.AddContent}, lines[params.AddAfterLine:]...)...)
		content = []byte(strings.Join(newLines, "\n"))
	case params.RemoveLine > 0:
		// Remove line
		if params.RemoveLine > len(lines) {
			return "", fmt.Errorf("remove_line %d exceeds total lines %d", params.RemoveLine, len(lines))
		}
		newLines := append(lines[:params.RemoveLine-1], lines[params.RemoveLine:]...)
		content = []byte(strings.Join(newLines, "\n"))
	default:
		return "", fmt.Errorf("no valid edit operation specified")
	}

	// Write back
	if err := os.WriteFile(cleanPath, content, 0644); err != nil {
		return "", fmt.Errorf("write file: %w", err)
	}

	return "File edited successfully", nil
}
