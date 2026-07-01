// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	agentic "github.com/pijalu/goa/internal/agentic"
)

// runCommandTool implements agentic.Tool for running shell commands.
type runCommandTool struct {
	workDir string
	logger  *agentic.Logger
}

// NewRunCommandTool creates a new runCommandTool.
func NewRunCommandTool(workDir string, logger *agentic.Logger) agentic.Tool {
	return &runCommandTool{
		workDir: workDir,
		logger:  logger,
	}
}

func (t *runCommandTool) IsRetryable(err error) bool { return false }

func (t *runCommandTool) Schema() agentic.ToolSchema {
	return agentic.ToolSchema{
		Name:        "run_command",
		Description: "Run a shell command in the work directory.",
		Schema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"command": map[string]interface{}{
					"type":        "string",
					"description": "Shell command to execute",
				},
			},
			"required": []string{"command"},
		},
	}
}

func (t *runCommandTool) Execute(input string) (string, error) {
	var params struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal([]byte(input), &params); err != nil {
		return "", fmt.Errorf("parse input: %w", err)
	}

	// Create command with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Run command in work directory
	cmd := exec.CommandContext(ctx, "sh", "-c", params.Command)
	cmd.Dir = t.workDir

	// Capture output
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("command failed: %w\nOutput: %s", err, string(output))
	}

	return strings.TrimSpace(string(output)), nil
}
