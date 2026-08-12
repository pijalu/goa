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
	// Strip shell redirect prefixes so the redirect TARGET is path-checked:
	// ">/tmp/x", ">>/tmp/x", "2>/tmp/x", "&>/tmp/x", "</etc/y". Without this a
	// redirect with no space before the target (`>/tmp/x`) produces a token
	// that starts with '>' and is never treated as a path — a jail escape
	// (SOLO allowed writes to /tmp via attached redirects).
	s = stripRedirectPrefix(s)
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

// stripRedirectPrefix removes leading shell redirect operators from a token,
// returning the path that follows. It handles ">", ">>", "<", "<<", and
// fd-qualified forms like "2>", "2>>", "&>". Returns "" when the token is a
// bare redirect operator with no target (nothing to path-check).
func stripRedirectPrefix(s string) string {
	i := 0
	// Optional leading file-descriptor digits (e.g. "2>").
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	// Optional '&' (as in "&>").
	if i < len(s) && s[i] == '&' {
		i++
	}
	// One or two redirect chars.
	if i < len(s) && (s[i] == '>' || s[i] == '<') {
		i++
		if i < len(s) && s[i] == s[i-1] {
			i++
		}
		// Only treat as a redirect when we consumed at least one '>'/'<';
		// leading digits alone (e.g. "2fast") are not redirects.
		return s[i:]
	}
	// No redirect operator found — return the original token unchanged.
	return s
}

// isPathLike reports whether s looks like a filesystem path token.
func isPathLike(s string) bool {
	return looksLikePath(s) != ""
}
