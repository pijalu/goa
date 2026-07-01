// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package perms

import (
	"encoding/json"
	"path/filepath"
	"strings"
)

// PathDecision is the result of evaluating a tool call against path protection.
type PathDecision string

const (
	// PathAllow permits the tool call without confirmation.
	PathAllow PathDecision = "allow"
	// PathDeny rejects the tool call immediately.
	PathDeny PathDecision = "deny"
	// PathAsk requires user confirmation before executing.
	PathAsk PathDecision = "ask"
)

// PathPolicy decides whether a tool call needs confirmation based on the
// current autonomy level and the paths it accesses.
type PathPolicy struct {
	ProjectDir string
	Autonomy   string // "solo", "yolo", "confirm", "review", etc.
}

// Decide returns the path-protection decision for a tool call.
//
//   - SOLO: allow only paths under the project directory; deny everything else.
//   - YOLO: allow all paths without confirmation.
//   - Any ask/confirm mode: allow reads under the project directory; ask for
//     bash, write, edit, and for any access outside the project directory.
func (p *PathPolicy) Decide(toolName, input string) PathDecision {
	switch p.Autonomy {
	case "solo":
		return p.decideSolo(toolName, input)
	case "yolo":
		return PathAllow
	default:
		return p.decideAsk(toolName, input)
	}
}

func (p *PathPolicy) decideSolo(toolName, input string) PathDecision {
	paths := extractToolPaths(toolName, input)
	if len(paths) == 0 {
		return PathAllow
	}
	for _, path := range paths {
		if IsProtectedPath(path) {
			return PathDeny
		}
		if p.ProjectDir != "" && !isUnderDir(path, p.ProjectDir) {
			return PathDeny
		}
	}
	return PathAllow
}

func (p *PathPolicy) decideAsk(toolName, input string) PathDecision {
	// Destructive or powerful tools always require confirmation in ask modes,
	// even when their arguments do not explicitly name a path.
	switch toolName {
	case "bash":
		return PathAsk
	case "write", "edit":
		return PathAsk
	}

	paths := extractToolPaths(toolName, input)
	if len(paths) == 0 {
		// Tools without paths (e.g. generic status commands) are allowed.
		return PathAllow
	}

	// Any access outside the working directory requires dedicated confirmation.
	for _, path := range paths {
		if p.ProjectDir != "" && !isUnderDir(path, p.ProjectDir) {
			return PathAsk
		}
	}

	// Inside the project: reads are allowed.
	return PathAllow
}

// IsProtectedPath reports whether path is inside .goa or .git.
func IsProtectedPath(path string) bool {
	clean := filepath.Clean(path)
	for _, protected := range protectedDirNames {
		if pathMatchesProtected(clean, protected) {
			return true
		}
	}
	return false
}

var protectedDirNames = []string{".goa", ".git"}

func pathMatchesProtected(clean, protected string) bool {
	sep := string(filepath.Separator)
	if clean == protected {
		return true
	}
	if strings.HasPrefix(clean, protected+sep) {
		return true
	}
	return strings.Contains(clean, sep+protected+sep)
}

// isUnderDir reports whether path is inside base. Both paths are resolved to
// absolute form. Non-path strings are treated as under base.
func isUnderDir(path, base string) bool {
	p := looksLikePath(path)
	if p == "" {
		return true
	}
	var abs string
	if filepath.IsAbs(p) {
		abs = p
	} else {
		joined, err := filepath.Abs(filepath.Join(base, p))
		if err != nil {
			return false
		}
		abs = joined
	}
	rel, err := filepath.Rel(base, abs)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, "../") && rel != ""
}

// extractToolPaths returns the filesystem paths referenced by a tool call.
func extractToolPaths(toolName, input string) []string {
	switch toolName {
	case "read", "write", "edit", "read_media_file":
		return extractJSONPaths(input, "path", "file_path", "target_path")
	case "bash":
		return extractBashPaths(input)
	}
	return nil
}

// extractJSONPaths extracts path-like string fields from JSON input.
func extractJSONPaths(input string, keys ...string) []string {
	var payload map[string]any
	if err := json.Unmarshal([]byte(input), &payload); err != nil {
		return nil
	}
	keySet := make(map[string]bool, len(keys))
	for _, k := range keys {
		keySet[k] = true
	}
	var paths []string
	for k, v := range payload {
		if !keySet[k] {
			continue
		}
		if s, ok := v.(string); ok && s != "" {
			paths = append(paths, s)
		}
	}
	return paths
}

// extractBashPaths performs a best-effort extraction of path tokens from a
// bash command JSON object.
func extractBashPaths(input string) []string {
	var payload struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal([]byte(input), &payload); err != nil {
		return nil
	}
	cmd := strings.TrimSpace(payload.Command)
	if cmd == "" {
		return nil
	}
	var paths []string
	for _, tok := range strings.Fields(cmd) {
		if looksLikePath(tok) != "" {
			paths = append(paths, tok)
		}
	}
	return paths
}
