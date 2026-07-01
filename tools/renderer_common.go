// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pijalu/goa/internal/ansi"
)

// renderColor helpers produce ANSI-styled fragments. These are intentionally
// self-contained so tool renderers do not depend on the tui package.

func rToolTitle(text string) string {
	return ansi.Bold + ansi.Fg(themeHex("toolTitle")) + text + ansi.BoldReset + ansi.FgReset
}

func rBashPrompt(text string) string {
	return ansi.Bold + ansi.Fg(themeHex("bash_prompt")) + text + ansi.BoldReset + ansi.FgReset
}

func rToolOutput(text string) string {
	return ansi.Fg(themeHex("toolOutput")) + text + ansi.FgReset
}

func rMuted(text string) string {
	return ansi.Fg(themeHex("system_msg")) + text + ansi.FgReset
}

func rWarning(text string) string {
	return ansi.Fg(themeHex("warning")) + text + ansi.FgReset
}

func rError(text string) string {
	return ansi.Fg(themeHex("error")) + text + ansi.FgReset
}

func rAccent(text string) string {
	return ansi.Fg(themeHex("token_prompt")) + text + ansi.FgReset
}

func rDiffAdded(text string) string {
	return ansi.Fg(themeHex("toolDiffAdded")) + text + ansi.FgReset
}

func rDiffRemoved(text string) string {
	return ansi.Fg(themeHex("toolDiffRemoved")) + text + ansi.FgReset
}

func rDiffContext(text string) string {
	return ansi.Fg(themeHex("toolDiffContext")) + text + ansi.FgReset
}

func rInverse(text string) string {
	return ansi.Reverse + text + ansi.ReverseReset
}

func themeHex(token string) string {
	if Themer != nil {
		return Themer.ColorHex(token)
	}
	// Fallbacks that match the dark theme.
	switch token {
	case "toolTitle":
		return "#ffffff"
	case "bash_prompt":
		return "#7dd3fc"
	case "toolOutput":
		return "#8b949e"
	case "system_msg":
		return "#8b949e"
	case "warning":
		return "#d29922"
	case "error":
		return "#f85149"
	case "token_prompt":
		return "#1f6feb"
	case "toolDiffAdded":
		return "#b5bd68"
	case "toolDiffRemoved":
		return "#cc6666"
	case "toolDiffContext":
		return "#808080"
	}
	return "#888888"
}

// ThemeProvider is satisfied by tui.Theme so tool renderers can query theme
// colors without importing the tui package.
type ThemeProvider interface {
	ColorHex(name string) string
}

// Themer is the active theme provider. The tui package sets this at startup.
var Themer ThemeProvider

// shortenHome replaces the home directory prefix with ~ for display.
func shortenHome(path string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	if strings.HasPrefix(path, home) {
		return "~" + path[len(home):]
	}
	return path
}

// formatPathRelativeToCwdOrAbsolute returns a path relative to cwd when
// possible, otherwise the absolute path.
func formatPathRelativeToCwdOrAbsolute(path, cwd string) string {
	if cwd == "" {
		return shortenHome(path)
	}
	rel, err := filepath.Rel(cwd, path)
	if err != nil || strings.HasPrefix(rel, "..") {
		return shortenHome(path)
	}
	return rel
}

// getLanguageFromPath returns a language identifier from a file extension.
func getLanguageFromPath(path string) string {
	ext := strings.TrimPrefix(filepath.Ext(path), ".")
	return extToLang[strings.ToLower(ext)]
}

var extToLang = map[string]string{
	"go": "go", "py": "python", "js": "javascript", "ts": "typescript",
	"tsx": "typescript", "jsx": "javascript", "json": "json", "yaml": "yaml",
	"yml": "yaml", "md": "markdown", "sh": "bash", "bash": "bash",
	"zsh": "bash", "fish": "fish", "dockerfile": "dockerfile", "makefile": "makefile",
	"c": "c", "h": "c", "cpp": "cpp", "hpp": "cpp", "cs": "csharp",
	"java": "java", "kt": "kotlin", "rs": "rust", "swift": "swift",
	"rb": "ruby", "php": "php", "sql": "sql", "html": "html", "css": "css",
	"scss": "scss", "xml": "xml", "toml": "toml", "lua": "lua", "vim": "vim",
}

// HighlightCode applies basic syntax coloring to a block of code based on the
// language. It is a thin wrapper around tui/markdown-style tokenizers to avoid
// importing the tui package from tools.
func HighlightCode(code, lang string) []string {
	lang = strings.ToLower(strings.TrimSpace(lang))
	lines := strings.Split(code, "\n")
	out := make([]string, len(lines))
	for i, line := range lines {
		out[i] = HighlightLine(line, lang)
	}
	return out
}

// HighlightLine applies basic syntax coloring to a single line.
func HighlightLine(line, lang string) string {
	switch lang {
	case "bash", "sh", "shell", "zsh":
		return highlightBash(line)
	case "go", "golang":
		return highlightGo(line)
	case "python", "py":
		return highlightPython(line)
	case "json":
		return highlightJSON(line)
	case "yaml", "yml":
		return highlightYAML(line)
	default:
		return line
	}
}

func highlightBash(line string) string {
	if idx := strings.IndexByte(line, '#'); idx >= 0 {
		comment := ansi.Faint + line[idx:] + ansi.BoldReset
		before := line[:idx]
		return before + comment
	}
	words := strings.Fields(line)
	if len(words) > 0 {
		cmdColor := ansi.Fg("#58a6ff")
		switch words[0] {
		case "cd", "go", "make", "ls", "cat", "echo", "git", "rm", "mv", "cp",
			"mkdir", "grep", "find", "docker", "npm", "yarn", "pnpm", "curl",
			"wget", "export", "source", "alias":
			return cmdColor + words[0] + ansi.FgReset + ansi.Fg("#8b949e") + " " + strings.Join(words[1:], " ") + ansi.FgReset
		}
	}
	return line
}

func highlightGo(line string) string {
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
	c := &hlColors{
		kw: ansi.Fg("#d29922"), typ: ansi.Fg("#58a6ff"), fn: ansi.Fg("#3fb950"),
		str: ansi.Fg("#a5d6ff"), num: ansi.Fg("#79c0ff"), comm: ansi.Faint,
		reset: ansi.BoldReset + ansi.FgReset, fg: ansi.Fg("#8b949e"),
	}
	return tokenizeGo(line, keywords, types, c)
}

func highlightPython(line string) string { return line }
func highlightJSON(line string) string   { return line }
func highlightYAML(line string) string   { return line }

type hlColors struct {
	kw, typ, fn, str, num, comm, reset, fg string
}

func tokenizeGo(line string, keywords, types map[string]bool, c *hlColors) string {
	var out strings.Builder
	i := 0
	for i < len(line) {
		if writeGoComment(line, &i, &out, c) {
			continue
		}
		if writeGoString(line, &i, &out, c) {
			continue
		}
		if writeGoNumber(line, &i, &out, c) {
			continue
		}
		if writeGoIdent(line, &i, &out, keywords, types, c) {
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

func writeGoComment(line string, i *int, out *strings.Builder, c *hlColors) bool {
	if *i+1 < len(line) && line[*i] == '/' && line[*i+1] == '/' {
		out.WriteString(c.comm + line[*i:] + c.reset)
		*i = len(line)
		return true
	}
	return false
}

func writeGoString(line string, i *int, out *strings.Builder, c *hlColors) bool {
	var delim byte
	switch line[*i] {
	case '"':
		delim = '"'
	case '`':
		delim = '`'
	default:
		return false
	}
	start := *i
	*i++
	for *i < len(line) {
		if line[*i] == '\\' && *i+1 < len(line) {
			*i += 2
			continue
		}
		if line[*i] == delim {
			*i++
			break
		}
		*i++
	}
	out.WriteString(c.str + line[start:*i] + c.reset)
	return true
}

func writeGoNumber(line string, i *int, out *strings.Builder, c *hlColors) bool {
	if !isDigit(line[*i]) {
		return false
	}
	start := *i
	for *i < len(line) && (isDigit(line[*i]) || line[*i] == '.' || line[*i] == 'x' || isHexDigit(line[*i])) {
		*i++
	}
	out.WriteString(c.num + line[start:*i] + c.reset)
	return true
}

func writeGoIdent(line string, i *int, out *strings.Builder, keywords, types map[string]bool, c *hlColors) bool {
	if !isIdentStart(line[*i]) {
		return false
	}
	start := *i
	for *i < len(line) && isIdentChar(line[*i]) {
		*i++
	}
	ident := line[start:*i]
	colored := ident
	if keywords[ident] {
		colored = c.kw + ident + c.reset
	} else if types[ident] {
		colored = c.typ + ident + c.reset
	} else if *i < len(line) && line[*i] == '(' {
		colored = c.fn + ident + c.reset
	}
	out.WriteString(colored)
	return true
}

func isDigit(b byte) bool    { return b >= '0' && b <= '9' }
func isHexDigit(b byte) bool { return isDigit(b) || (b >= 'a' && b <= 'f') || (b >= 'A' && b <= 'F') }
func isIdentStart(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || b == '_'
}
func isIdentChar(b byte) bool {
	return isIdentStart(b) || isDigit(b)
}

// keyHint returns a styled keybinding hint.
func keyHint(keys, action string) string {
	return ansi.Fg(themeHex("toolOutput")) + keys + ansi.FgReset + rMuted(" "+action)
}

// expandHint returns the standard "... (N more lines, <key> to expand)" hint.
func expandHint(hiddenCount int, key string) string {
	if hiddenCount <= 0 {
		return ""
	}
	return rMuted(fmt.Sprintf("… %d more lines (%s to expand)", hiddenCount, key))
}

// trimTrailingEmptyLines removes empty lines from the end of a slice.
func trimTrailingEmptyLines(lines []string) []string {
	end := len(lines)
	for end > 0 && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	return lines[:end]
}
