// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import "strings"

// firstCommandToken extracts the first command name from a shell command.
// Handles simple quoting but does not fully lex — enough for security matching.
func firstCommandToken(cmd string) string {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return ""
	}
	// Skip leading variable assignments (e.g. "FOO=bar make install" → "make")
	// Skip leading redirects (e.g. ">/dev/null ls" → "ls")
	for i := 0; i < len(cmd); {
		start := i
		i = advanceShellWord(cmd, i)
		word := cmd[start:i]
		i = skipShellSpace(cmd, i)
		if isCommandWord(word) {
			return word
		}
	}
	return ""
}

// advanceShellWord advances i past one shell word, respecting simple quotes
// and escapes. It returns the index after the word.
func advanceShellWord(cmd string, i int) int {
	for i < len(cmd) && cmd[i] != ' ' && cmd[i] != '\t' {
		if q := cmd[i]; q == '\\' || q == '\'' || q == '"' {
			i = skipQuoted(cmd, i+1, q)
			continue
		}
		i++
	}
	return i
}

func skipQuoted(cmd string, i int, quote byte) int {
	for i < len(cmd) && cmd[i] != quote {
		if cmd[i] == '\\' && quote == '\'' {
			break
		}
		i++
	}
	if i < len(cmd) {
		i++ // consume closing quote
	}
	return i
}

// skipShellSpace advances i past spaces and tabs.
func skipShellSpace(cmd string, i int) int {
	for i < len(cmd) && (cmd[i] == ' ' || cmd[i] == '\t') {
		i++
	}
	return i
}

// isCommandWord reports whether word is a command token rather than an
// assignment, redirect, or empty string.
func isCommandWord(word string) bool {
	return word != "" && !strings.Contains(word, "=") && !strings.HasPrefix(word, ">")
}
