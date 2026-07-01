// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"strings"

	"github.com/pijalu/goa/internal/ansi"
)

// ── Syntax highlighting ────────────────────────────────────────

// highlightLine applies basic syntax coloring to a single code line
// based on the declared language. Uses simple keyword/keyword matching.
func highlightLine(line, lang, fg string) string {
	lang = strings.ToLower(strings.TrimSpace(lang))
	switch lang {
	case "bash", "sh", "shell", "zsh":
		return highlightBash(line, fg)
	case "go", "golang":
		return highlightGo(line, fg)
	case "python", "py":
		return highlightPython(line, fg)
	case "json":
		return highlightJSON(line, fg)
	case "yaml", "yml":
		return highlightYAML(line, fg)
	default:
		return line
	}
}

// resetToCode returns an ANSI sequence that resets only the foreground color
// and intensity while preserving an active background color. It then restores
// the code block's foreground color so that syntax highlighting does not
// strip the outer background.
func resetToCode(fg string) string {
	return ansi.FgReset + ansi.BoldReset + fg
}

// highlightBash colors shell commands, variables, and comments.
func highlightBash(line, fg string) string {
	// Comments: # ...
	if idx := strings.IndexByte(line, '#'); idx >= 0 {
		comment := ansi.Faint + line[idx:] + resetToCode(fg)
		before := line[:idx]
		// Color known commands blue
		cmds := []string{"cd ", "go ", "make ", "ls ", "cat ", "echo ", "git ", "rm ", "mv ", "cp ", "mkdir ", "grep ", "find ", "docker ", "npm ", "yarn ", "pnpm ", "curl ", "wget ", "export "}
		for _, cmd := range cmds {
			if strings.HasPrefix(strings.TrimSpace(before), strings.TrimSpace(cmd)) {
				before = ansi.Fg("#58a6ff") + cmd + resetToCode(fg) + before[len(cmd):]
				break
			}
		}
		return before + comment
	}
	// Variables: $VAR or ${VAR}
	result := line
	// Simple command detection
	words := strings.Fields(line)
	if len(words) > 0 {
		cmdColor := ansi.Fg("#58a6ff")
		switch words[0] {
		case "cd", "go", "make", "ls", "cat", "echo", "git", "rm", "mv", "cp",
			"mkdir", "grep", "find", "docker", "npm", "yarn", "pnpm", "curl",
			"wget", "export", "source", "alias":
			result = cmdColor + words[0] + resetToCode(fg)
			if len(words) > 1 {
				result += " " + strings.Join(words[1:], " ")
			}
		}
	}
	return result
}

// highlightGo colors Go keywords, types, functions, strings, and comments.
// Uses a simple tokenizer approach to avoid ANSI code interference.
func highlightGo(line, fg string) string {
	keywords := map[string]bool{
		"func": true, "if": true, "else": true, "for": true, "range": true,
		"return": true, "var": true, "const": true, "type": true, "struct": true,
		"interface": true, "map": true, "chan": true, "go": true, "defer": true,
		"select": true, "case": true, "switch": true, "break": true, "continue": true,
		"package": true, "import": true, "nil": true, "true": true, "false": true,
	}
	types := map[string]bool{
		"string": true, "int": true, "int8": true, "int16": true, "int32": true, "int64": true,
		"uint": true, "uint8": true, "uint16": true, "uint32": true, "uint64": true,
		"float32": true, "float64": true, "bool": true, "byte": true, "rune": true,
		"error": true, "any": true, "comparable": true,
	}
	colors := &hlColors{
		kw:    ansi.Fg("#d29922"),
		typ:   ansi.Fg("#58a6ff"),
		fn:    ansi.Fg("#3fb950"),
		str:   ansi.Fg("#a5d6ff"),
		num:   ansi.Fg("#79c0ff"),
		comm:  ansi.Faint,
		reset: resetToCode(fg),
	}
	return highlightTokenizer(line, keywords, types, colors)
}

// hlColors holds ANSI color strings for syntax highlighting.
type hlColors struct {
	kw, typ, fn, str, num, comm, reset string
}

// highlightTokenizer tokenizes a code line and applies syntax coloring.
func highlightTokenizer(line string, keywords, types map[string]bool, c *hlColors) string {
	var out strings.Builder
	i := 0
	for i < len(line) {
		if writeGoToken(line, &i, &out, keywords, types, c) {
			continue
		}
		out.WriteByte(line[i])
		i++
	}
	if out.Len() == 0 {
		return line
	}
	return out.String()
}

// writeGoToken detects and writes one syntax-colored token from line[i:].
func writeGoToken(line string, i *int, out *strings.Builder, keywords, types map[string]bool, c *hlColors) bool {
	if writeGoComment(line, i, out, c) {
		return true
	}
	if writeGoString(line, i, out, c) {
		return true
	}
	if writeGoNumber(line, i, out, c) {
		return true
	}
	if writeGoIdent(line, i, out, keywords, types, c) {
		return true
	}
	return false
}

// writeGoComment writes a //-style comment.
func writeGoComment(line string, i *int, out *strings.Builder, c *hlColors) bool {
	if *i+1 < len(line) && line[*i] == '/' && line[*i+1] == '/' {
		out.WriteString(c.comm + line[*i:] + c.reset)
		*i = len(line)
		return true
	}
	return false
}

// writeGoString writes a string, raw string, or char literal.
func writeGoString(line string, i *int, out *strings.Builder, c *hlColors) bool {
	var delim byte
	switch line[*i] {
	case '"':
		delim = '"'
	case '`':
		delim = '`'
	case '\'':
		delim = '\''
	default:
		return false
	}

	if delim == '`' {
		end := *i + 1
		for end < len(line) && line[end] != '`' {
			end++
		}
		if end < len(line) {
			end++
		}
		out.WriteString(c.str + line[*i:end] + c.reset)
		*i = end
		return true
	}

	end := findStringEnd(line, *i+1, delim, '\\')
	out.WriteString(c.str + line[*i:end] + c.reset)
	*i = end
	return true
}

// writeGoNumber writes a numeric literal.
func writeGoNumber(line string, i *int, out *strings.Builder, c *hlColors) bool {
	if !isDigit(line[*i]) {
		return false
	}
	start := *i
	*i++
	for *i < len(line) && isNumberChar(line[*i]) {
		*i++
	}
	out.WriteString(c.num + line[start:*i] + c.reset)
	return true
}

func isDigit(b byte) bool { return b >= '0' && b <= '9' }

func isNumberChar(ch byte) bool {
	return isDigit(ch) || ch == '.' || ch == 'x' || ch == 'X' ||
		ch >= 'a' && ch <= 'f' || ch >= 'A' && ch <= 'F'
}

// writeGoIdent writes an identifier, keyword, type, or function call.
func writeGoIdent(line string, i *int, out *strings.Builder, keywords, types map[string]bool, c *hlColors) bool {
	b := line[*i]
	if !(b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b == '_') {
		return false
	}
	start := *i
	for *i < len(line) && isGoIdent(line[*i]) {
		*i++
	}
	word := line[start:*i]
	switch {
	case keywords[word]:
		out.WriteString(c.kw + word + c.reset)
	case types[word]:
		out.WriteString(c.typ + word + c.reset)
	case isGoFuncCall(line, *i):
		out.WriteString(c.fn + word + c.reset)
	default:
		out.WriteString(word)
	}
	return true
}

// findStringEnd finds the closing delimiter, handling escape sequences.
func findStringEnd(line string, start int, closeDelim, escape byte) int {
	for start < len(line) {
		if line[start] == escape {
			start += 2
			if start > len(line) {
				start = len(line)
			}
			continue
		}
		if line[start] == closeDelim {
			return start + 1
		}
		start++
	}
	return start
}

// isGoIdent returns true if the byte can appear in a Go identifier.
func isGoIdent(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '_'
}

// isGoFuncCall checks if the identifier at position i is a function call
// (next non-space character is '(').
func isGoFuncCall(line string, i int) bool {
	for j := i; j < len(line); j++ {
		if line[j] == ' ' || line[j] == '\t' {
			continue
		}
		return line[j] == '('
	}
	return false
}

// highlightPython colors Python keywords and strings.
func highlightPython(line, fg string) string {
	keywords := []string{"def", "class", "if", "elif", "else", "for", "while", "return", "import", "from",
		"as", "with", "try", "except", "finally", "raise", "in", "not", "and", "or", "is", "None",
		"True", "False", "self", "lambda", "yield", "async", "await"}
	kwColor := ansi.Fg("#d29922")
	fnColor := ansi.Fg("#58a6ff")
	strColor := ansi.Fg("#a5d6ff")
	result := line

	for _, kw := range keywords {
		pattern := kw + " "
		if strings.Contains(result, pattern) {
			result = strings.ReplaceAll(result, pattern, kwColor+kw+resetToCode(fg)+" ")
		}
	}
	// Function definition
	if strings.HasPrefix(strings.TrimSpace(line), "def ") || strings.HasPrefix(strings.TrimSpace(line), "class ") {
		parts := strings.SplitN(line, " ", 3)
		if len(parts) >= 2 {
			fnName := strings.SplitN(parts[1], "(", 2)[0]
			line = parts[0] + " " + fnColor + fnName + resetToCode(fg)
			if len(parts) > 2 {
				line += " " + parts[2]
			}
			result = line
		}
	}
	// Strings
	result = highlightStrings(result, strColor, fg)
	return result
}

// highlightJSON colors JSON keys and string values.
func highlightJSON(line, fg string) string {
	strColor := ansi.Fg("#a5d6ff")
	return highlightStrings(line, strColor, fg)
}

// highlightYAML colors YAML keys.
func highlightYAML(line, fg string) string {
	keyColor := ansi.Fg("#58a6ff")
	if idx := strings.Index(line, ":"); idx >= 0 && idx < len(line)-1 && line[idx+1] == ' ' {
		key := line[:idx]
		rest := line[idx:]
		return keyColor + key + resetToCode(fg) + rest
	}
	return line
}

// highlightStrings replaces quoted strings with colored versions.
// Advances past the replacement to avoid re-processing the same delimiters.
func highlightStrings(line, strColor, fg string) string {
	result := line
	suffix := resetToCode(fg)
	delimiters := []string{`"`, `'`, "`"}
	for _, delim := range delimiters {
		pos := 0
		for {
			start := strings.Index(result[pos:], delim)
			if start < 0 {
				break
			}
			start += pos
			end := strings.Index(result[start+len(delim):], delim)
			if end < 0 {
				break
			}
			end += start + len(delim)
			before := result[:start]
			content := result[start : end+len(delim)]
			after := result[end+len(delim):]
			result = before + strColor + content + suffix + after
			pos = len(before) + len(strColor) + len(content) + len(suffix)
		}
	}
	return result
}
