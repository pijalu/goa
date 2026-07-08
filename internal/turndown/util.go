// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package turndown

import "strings"

// blockElements lists HTML elements that are rendered as Markdown blocks.
var blockElements = map[string]bool{
	"ADDRESS": true, "ARTICLE": true, "ASIDE": true, "AUDIO": true,
	"BLOCKQUOTE": true, "BODY": true, "CANVAS": true, "CENTER": true,
	"DD": true, "DIR": true, "DIV": true, "DL": true, "DT": true,
	"FIELDSET": true, "FIGCAPTION": true, "FIGURE": true, "FOOTER": true,
	"FORM": true, "FRAMESET": true, "H1": true, "H2": true, "H3": true,
	"H4": true, "H5": true, "H6": true, "HEADER": true, "HGROUP": true,
	"HR": true, "HTML": true, "ISINDEX": true, "LI": true, "MAIN": true,
	"MENU": true, "NAV": true, "NOFRAMES": true, "NOSCRIPT": true,
	"OL": true, "OUTPUT": true, "P": true, "PRE": true, "SECTION": true,
	"TABLE": true, "TBODY": true, "TD": true, "TFOOT": true, "TH": true,
	"THEAD": true, "TR": true, "UL": true,
}

// voidElements lists HTML void elements.
var voidElements = map[string]bool{
	"AREA": true, "BASE": true, "BR": true, "COL": true, "COMMAND": true,
	"EMBED": true, "HR": true, "IMG": true, "INPUT": true, "KEYGEN": true,
	"LINK": true, "META": true, "PARAM": true, "SOURCE": true,
	"TRACK": true, "WBR": true,
}

func isBlock(tag string) bool { return blockElements[strings.ToUpper(tag)] }
func isVoid(tag string) bool  { return voidElements[strings.ToUpper(tag)] }

func trimLeadingNewlines(s string) string {
	i := 0
	for i < len(s) && s[i] == '\n' {
		i++
	}
	return s[i:]
}

func trimTrailingNewlines(s string) string {
	i := len(s)
	for i > 0 && s[i-1] == '\n' {
		i--
	}
	return s[:i]
}

// escapeMarkdown escapes Markdown-special characters.
func escapeMarkdown(s string) string {
	replacements := []struct {
		old, new string
	}{
		{`\`, `\\`},
		{`*`, `\*`},
		{"`", "\\`"},
		{"[", "\\["},
		{"]", "\\]"},
		{"_", "\\_"},
	}
	for _, r := range replacements {
		s = strings.ReplaceAll(s, r.old, r.new)
	}
	return s
}

// escapeLinkDestination escapes characters that break Markdown link syntax.
func escapeLinkDestination(s string) string {
	s = strings.ReplaceAll(s, "<", "\\<")
	s = strings.ReplaceAll(s, ">", "\\>")
	s = strings.ReplaceAll(s, "(", "\\(")
	s = strings.ReplaceAll(s, ")", "\\)")
	if strings.Contains(s, " ") {
		return "<" + s + ">"
	}
	return s
}

// escapeLinkTitle escapes double quotes in a link title.
func escapeLinkTitle(s string) string {
	return strings.ReplaceAll(s, `"`, `\\"`)
}
