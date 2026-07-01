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

// readFileTool implements agentic.Tool for reading files.
type readFileTool struct {
	workDir string
	logger  *agentic.Logger
}

// NewReadFileTool creates a new readFileTool.
func NewReadFileTool(workDir string, logger *agentic.Logger) agentic.Tool {
	return &readFileTool{
		workDir: workDir,
		logger:  logger,
	}
}

func (t *readFileTool) IsRetryable(err error) bool { return false }

func (t *readFileTool) Schema() agentic.ToolSchema {
	return agentic.ToolSchema{
		Name:        "read",
		Description: "Read the contents of a file. Supports reading specific lines with offset and limit.",
		Schema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"path": map[string]interface{}{
					"type":        "string",
					"description": "Path to the file to read (relative to work directory)",
				},
				"offset": map[string]interface{}{
					"type":        "integer",
					"description": "Line number to start reading from (1-indexed, optional)",
				},
				"limit": map[string]interface{}{
					"type":        "integer",
					"description": "Maximum number of lines to read (optional)",
				},
			},
			"required": []string{"path"},
		},
	}
}

func (t *readFileTool) Execute(input string) (string, error) {
	var params struct {
		Path   string `json:"path"`
		Offset int    `json:"offset"`
		Limit  int    `json:"limit"`
	}
	if err := json.Unmarshal([]byte(input), &params); err != nil {
		return "", fmt.Errorf("parse input: %w", err)
	}

	// Sanitize path to prevent traversal - convert to absolute paths for comparison
	cleanWorkDir, err := filepath.Abs(t.workDir)
	if err != nil {
		return "", fmt.Errorf("invalid work dir: %w", err)
	}
	cleanPath := filepath.Join(cleanWorkDir, params.Path)
	cleanPath = filepath.Clean(cleanPath)

	if !strings.HasPrefix(cleanPath, cleanWorkDir+string(filepath.Separator)) && cleanPath != cleanWorkDir {
		return "", fmt.Errorf("path traversal detected: %s", params.Path)
	}

	// Read file
	content, err := os.ReadFile(cleanPath)
	if err != nil {
		return "", fmt.Errorf("read file: %w", err)
	}

	lines := strings.Split(string(content), "\n")
	totalLines := len(lines)

	// Apply offset
	if params.Offset > 0 {
		if params.Offset > totalLines {
			return "", fmt.Errorf("offset %d exceeds total lines %d", params.Offset, totalLines)
		}
		lines = lines[params.Offset-1:] // 1-indexed
	}

	// Apply limit
	if params.Limit > 0 && params.Limit < len(lines) {
		lines = lines[:params.Limit]
	}

	return strings.Join(lines, "\n"), nil
}
