// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package perms

import (
	"encoding/json"
	"path/filepath"
	"strings"
)

// extractPath returns a file path from a tool input. It prefers a JSON "path"
// field and falls back to a non-JSON string.
func extractPath(input string) string {
	var payload struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal([]byte(input), &payload); err == nil && payload.Path != "" {
		return payload.Path
	}
	trimmed := strings.TrimSpace(input)
	if trimmed == "" || strings.HasPrefix(trimmed, "{") {
		return ""
	}
	return trimmed
}

// stripQuoted removes content inside single and double quotes from a command
// string, replacing it with spaces to preserve token boundaries. Text inside
// quotes is never a real filesystem path reference — it's a string literal
// (commit message, echo text, etc.).
func stripQuoted(cmd string) string {
	var b strings.Builder
	b.Grow(len(cmd))
	inSingle := false
	inDouble := false
	for i := 0; i < len(cmd); i++ {
		ch := cmd[i]
		switch {
		case ch == '\'' && !inDouble:
			inSingle = !inSingle
			b.WriteByte(' ')
		case ch == '"' && !inSingle:
			inDouble = !inDouble
			b.WriteByte(' ')
		case inSingle || inDouble:
			b.WriteByte(' ')
		default:
			b.WriteByte(ch)
		}
	}
	return b.String()
}

// looksLikePath returns the cleaned token when it appears to be a filesystem
// path, otherwise an empty string.
func looksLikePath(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if s == "." || s == ".." {
		return s
	}
	if strings.HasPrefix(s, "/") || strings.HasPrefix(s, "./") || strings.HasPrefix(s, "../") {
		return s
	}
	return ""
}

// underDir reports whether path is inside base. Empty or non-path inputs are
// treated as under base. Both paths are resolved to absolute form.
func underDir(path, base string) bool {
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

// referencesOutsidePath performs a best-effort check that a bash command does
// not reference paths outside base. It also rejects `cd` targets outside base.
// Quoted strings are stripped before scanning since they are never real paths.
func referencesOutsidePath(cmd, base string) bool {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return false
	}
	if after, ok := strings.CutPrefix(cmd, "cd "); ok {
		target := strings.TrimSpace(after)
		if target != "" && !underDir(target, base) {
			return true
		}
	}
	// Strip quoted strings so text in commit messages, echo arguments, etc.
	// is not mistaken for filesystem paths (e.g. /skill:run in a git -m msg).
	flat := stripQuoted(cmd)
	for _, tok := range strings.Fields(flat) {
		if looksLikePath(tok) == "" {
			continue
		}
		if !underDir(tok, base) {
			return true
		}
	}
	return false
}
