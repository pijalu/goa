// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package bm25

import (
	"path/filepath"
	"strings"
)

// commentSyntax maps file extensions to their comment syntax.
// Each entry stores (line prefixes, block open delimiter, block close delimiter).
type commentRule struct {
	linePrefixes []string
	blockOpen    string
	blockClose   string
}

// extRules maps file extensions to comment rules.
// Base-name rules are handled separately (see baseRules).
var extRules = map[string]commentRule{
	".go":                      {[]string{"//"}, "/*", "*/"},
	".rs":                      {[]string{"//"}, "/*", "*/"},
	".py":                      {[]string{"#"}, "", ""},
	".rb":                      {[]string{"#"}, "", ""},
	".r":                       {[]string{"#"}, "", ""},
	".js":                      {[]string{"//"}, "/*", "*/"},
	".ts":                      {[]string{"//"}, "/*", "*/"},
	".jsx":                     {[]string{"//"}, "/*", "*/"},
	".tsx":                     {[]string{"//"}, "/*", "*/"},
	".java":                    {[]string{"//"}, "/*", "*/"},
	".kt":                      {[]string{"//"}, "/*", "*/"},
	".scala":                   {[]string{"//"}, "/*", "*/"},
	".swift":                   {[]string{"//"}, "/*", "*/"},
	".c":                       {[]string{"//"}, "/*", "*/"},
	".h":                       {[]string{"//"}, "/*", "*/"},
	".cpp":                     {[]string{"//"}, "/*", "*/"},
	".hpp":                     {[]string{"//"}, "/*", "*/"},
	".cs":                      {[]string{"//"}, "/*", "*/"},
	".dart":                    {[]string{"//"}, "/*", "*/"},
	".zig":                     {[]string{"//"}, "/*", "*/"},
	".sh":                      {[]string{"#"}, "", ""},
	".bash":                    {[]string{"#"}, "", ""},
	".zsh":                     {[]string{"#"}, "", ""},
	".fish":                    {[]string{"#"}, "", ""},
	".pl":                      {[]string{"#"}, "", ""},
	".pm":                      {[]string{"#"}, "", ""},
	".lua":                     {[]string{"--"}, "--[[", "]]"},
	".sql":                     {[]string{"--"}, "/*", "*/"},
	".elm":                     {[]string{"--"}, "{-", "-}"},
	".hs":                      {[]string{"--"}, "{-", "-}"},
	".nim":                     {[]string{"#"}, "", ""},
	".cr":                      {[]string{"#"}, "", ""},
	".vue":                     {[]string{"//"}, "<!--", "-->"},
	".svelte":                  {[]string{"//"}, "<!--", "-->"},
	".html":                    {nil, "<!--", "-->"},
	".htm":                     {nil, "<!--", "-->"},
	".xml":                     {nil, "<!--", "-->"},
	".css":                     {[]string{"//"}, "/*", "*/"},
	".scss":                    {[]string{"//"}, "/*", "*/"},
	".less":                    {[]string{"//"}, "/*", "*/"},
	".yaml":                    {[]string{"#"}, "", ""},
	".yml":                     {[]string{"#"}, "", ""},
	".toml":                    {[]string{"#"}, "", ""},
	".ini":                     {[]string{"#", ";"}, "", ""},
	".cfg":                     {[]string{"#", ";"}, "", ""},
	".conf":                    {[]string{"#", ";"}, "", ""},
	".env":                     {[]string{"#"}, "", ""},
	".makefile":                {[]string{"#"}, "", ""},
	".dockerfile":              {[]string{"#"}, "", ""},
	".gradle":                  {[]string{"//"}, "/*", "*/"},
	".proto":                   {[]string{"//"}, "/*", "*/"},
}

// baseRules maps extension-less file base names to comment rules.
var baseRules = map[string]commentRule{
	"makefile":       {[]string{"#"}, "", ""},
	"gemfile":        {[]string{"#"}, "", ""},
	"rakefile":       {[]string{"#"}, "", ""},
	"dockerfile":     {[]string{"#"}, "", ""},
	"cmakelists.txt": {[]string{"#"}, "", ""},
	"justfile":       {[]string{"#"}, "", ""},
}

// stripComments removes comments from source code text based on the file's
// extension. It handles common single-line and block comment syntaxes.
// Comment text is documentation for humans, not meaningful search tokens.
func stripComments(path, text string) string {
	ext := strings.ToLower(filepath.Ext(path))
	base := strings.ToLower(filepath.Base(path))

	rule, ok := extRules[ext]
	if !ok {
		rule, ok = baseRules[base]
	}
	if !ok {
		return text
	}

	result := text
	if rule.blockOpen != "" {
		result = stripBlockComments(result, rule.blockOpen, rule.blockClose)
	}
	if len(rule.linePrefixes) > 0 {
		result = stripLineComments(result, rule.linePrefixes)
	}
	return result
}

// stripLineComments removes all single-line comments from text using the given
// prefix list. It removes from the prefix to the end of the line.
func stripLineComments(text string, prefixes []string) string {
	if len(prefixes) == 0 {
		return text
	}
	var b strings.Builder
	b.Grow(len(text))
	lines := strings.Split(text, "\x0a")
	for i, line := range lines {
		if i > 0 {
			b.WriteByte('\x0a')
		}
		stripped := stripLineComment(line, prefixes)
		b.WriteString(stripped)
	}
	return b.String()
}

// stripLineComment removes the first comment prefix found outside a string
// literal and everything after it from a single line. It scans character by
// character to avoid matching comment prefixes inside URLs or string content.
func stripLineComment(line string, prefixes []string) string {
	i := 0
	for i < len(line) {
		ch := line[i]

		// Skip escaped characters and string literal boundaries
		if ch == '\x5c' {
			i += 2
			continue
		}
		if ch == '\x27' || ch == '\x22' {
			i++
			continue
		}

		// Outside strings: check for comment prefix match
		if !isInString(line, i) && isCommentStart(line[i:], prefixes) {
			return line[:i]
		}
		i++
	}
	return line
}

// isInString reports whether position i in line is inside a quoted string.
func isInString(line string, i int) bool {
	inSingle := false
	inDouble := false
	for j := 0; j < i; j++ {
		ch := line[j]
		if ch == '\x5c' {
			j++
			continue
		}
		if ch == '\x27' && !inDouble {
			inSingle = !inSingle
		}
		if ch == '\x22' && !inSingle {
			inDouble = !inDouble
		}
	}
	return inSingle || inDouble
}

// isCommentStart reports whether text starts with any of the given prefixes.
func isCommentStart(text string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(text, p) {
			return true
		}
	}
	return false
}

// blockAfterClose writes trailing space after a block comment to avoid
// merging surrounding tokens. Only called when depth reaches 0 after close.
func blockAfterClose(b *strings.Builder) {
	if b.Len() == 0 {
		return
	}
	last := b.String()[b.Len()-1]
	if last != ' ' && last != '\x0a' && last != '\x09' {
		b.WriteByte(' ')
	}
}

// stripBlockComments removes all block comments from text, handling
// non-nesting delimiters (the common case). It tracks depth so nested
// delimiters like <!-- <!-- --> --> work correctly.
func stripBlockComments(text, open, close string) string {
	if open == "" || close == "" {
		return text
	}
	var b strings.Builder
	b.Grow(len(text))
	depth := 0
	i := 0
	for i < len(text) {
		switch {
		case depth == 0 && strings.HasPrefix(text[i:], open):
			depth++
			i += len(open)
		case depth > 0 && strings.HasPrefix(text[i:], close):
			depth--
			i += len(close)
			if depth == 0 {
				blockAfterClose(&b)
			}
		case depth == 0:
			b.WriteByte(text[i])
			i++
		default:
			i++
		}
	}
	return b.String()
}
