// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package perms

import (
	"fmt"
	"path/filepath"
	"strings"
)

// SoloGuard enforces the "solo" autonomy policy: tool calls are constrained
// to the codebase directory and git is restricted to commit/diff.
type SoloGuard struct {
	CodebaseDir string
}

// NewSoloGuard creates a guard for the given codebase directory.
func NewSoloGuard(codebaseDir string) *SoloGuard {
	return &SoloGuard{CodebaseDir: codebaseDir}
}

// Validate checks a tool call against the SOLO policy. It returns an error
// explaining the violation when the call should be rejected.
func (g *SoloGuard) Validate(toolName, input string) error {
	if g.CodebaseDir == "" {
		return nil
	}
	base, err := filepath.Abs(g.CodebaseDir)
	if err != nil {
		return fmt.Errorf("SOLO mode: unable to resolve codebase directory: %w", err)
	}
	switch toolName {
	case "read", "write", "edit":
		return g.validateFileTool(toolName, input, base)
	case "bash":
		return g.validateBash(input, base)
	case "git":
		return g.validateGit(input)
	}
	return nil
}

func (g *SoloGuard) validateFileTool(toolName, input, base string) error {
	path := extractPath(input)
	if path == "" {
		return nil
	}
	if IsProtectedPath(path) {
		return g.soloError("%s tool cannot access protected path %q", toolName, path)
	}
	if !underDir(path, base) {
		return g.soloError("%s tool can only access files under %s", toolName, base)
	}
	return nil
}

func (g *SoloGuard) validateBash(input, base string) error {
	if referencesOutsidePath(input, base) {
		return g.soloError("bash command references a path outside the codebase")
	}
	return nil
}

func (g *SoloGuard) validateGit(input string) error {
	args := strings.Fields(input)
	if len(args) == 0 {
		return nil
	}
	// Strip leading "git" if present.
	if args[0] == "git" {
		args = args[1:]
	}
	if len(args) == 0 {
		return nil
	}
	switch args[0] {
	case "commit", "diff":
		return nil
	}
	return g.soloError("git command %q is not allowed in SOLO mode (only commit and diff are permitted)", args[0])
}

func (g *SoloGuard) soloError(format string, args ...any) error {
	msg := fmt.Sprintf(format, args...)
	msg = strings.ReplaceAll(msg, "$CODEBASE_DIR", g.CodebaseDir)
	return fmt.Errorf("SOLO mode restriction: %s", msg)
}
