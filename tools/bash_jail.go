// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"path/filepath"
	"strings"
	"unicode"
)

// bashReferencesOutsidePath performs a best-effort check that a bash command
// does not reference paths outside base. It also rejects cd targets outside
// base. Empty commands or commands without path-like tokens are allowed.
func bashReferencesOutsidePath(cmd, base string) bool {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return false
	}
	// Check cd target: extract only the first argument to cd, not the entire
	// command remainder. Commands like "cd /project && find ." should only
	// check /project, not "/project && find ." as a path.
	if strings.HasPrefix(cmd, "cd ") {
		rest := strings.TrimSpace(cmd[3:])
		if fields := strings.Fields(rest); len(fields) > 0 {
			if !pathUnderDir(fields[0], base) {
				return true
			}
		}
		// Continue to check the remaining tokens for other path references.
	}
	for _, tok := range tokenizeQuoted(cmd) {
		if !isPathLike(tok) {
			continue
		}
		if !pathUnderDir(tok, base) {
			return true
		}
	}
	return false
}

// tokenizeQuoted splits cmd into tokens, respecting single and double quotes.
// Tokens inside quoted strings are excluded — they are likely grep patterns,
// echo strings, or other non-path arguments, not filesystem paths.
// isQuoteChar reports whether ch is a single or double quote character.
func isQuoteChar(ch byte) bool {
	return ch == '\'' || ch == '"'
}

// tokenizeQuoted splits cmd into tokens, respecting single and double quotes.
// Tokens inside quoted strings are excluded — they are likely grep patterns,
// echo strings, or other non-path arguments, not filesystem paths.
func tokenizeQuoted(cmd string) []string {
	var tokens []string
	var current strings.Builder
	inQuote := false
	quoteChar := byte(0)
	for _, r := range cmd {
		ch := byte(r)
		if !inQuote && isQuoteChar(ch) {
			inQuote = true
			quoteChar = ch
			continue
		}
		if inQuote && ch == quoteChar {
			inQuote = false
			quoteChar = 0
			continue
		}
		if inQuote {
			continue
		}
		if unicode.IsSpace(r) {
			if current.Len() > 0 {
				tokens = append(tokens, current.String())
				current.Reset()
			}
			continue
		}
		current.WriteRune(r)
	}
	if current.Len() > 0 {
		tokens = append(tokens, current.String())
	}
	return tokens
}

// pathUnderDir reports whether path is inside base. Empty or non-path inputs
// are treated as inside base so unrelated tokens do not trigger false
// positives.
func pathUnderDir(path, base string) bool {
	p := looksLikePath(path)
	if p == "" {
		return true
	}
	base = filepath.Clean(base)
	var abs string
	if filepath.IsAbs(p) {
		abs = filepath.Clean(p)
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
	if strings.HasPrefix(s, "/") {
		// A bare sequence of slashes (e.g. "//" comments in code snippets) is
		// not a meaningful filesystem path and causes false positives when
		// commands contain heredoc bodies. Ignore slash-only tokens.
		if strings.IndexFunc(s, func(r rune) bool { return r != '/' }) < 0 {
			return ""
		}
		return s
	}
	if strings.HasPrefix(s, "./") || strings.HasPrefix(s, "../") {
		return s
	}
	return ""
}

// isPathLike reports whether s looks like a filesystem path token.
func isPathLike(s string) bool {
	return looksLikePath(s) != ""
}
