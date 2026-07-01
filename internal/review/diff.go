// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package review

import (
	"fmt"
	"regexp"
	"strings"
)

// DiffLineKind classifies a line in a unified diff.
type DiffLineKind int

const (
	DiffHeader DiffLineKind = iota
	DiffFileMeta
	DiffHunkHeader
	DiffContext
	DiffAdded
	DiffRemoved
)

// DiffLine is a single renderable line of a unified diff.
type DiffLine struct {
	Kind       DiffLineKind
	File       string // current file path
	Raw        string // original line text including +/- prefix
	NewLineNum int    // line number in the new (current) file
	OldLineNum int    // line number in the old (base) file
}

// ParseDiff parses a unified diff into renderable lines with file and line
// number metadata. It is intentionally minimal: it tracks the current file
// and hunk line numbers so the pager can attach comments to the right place.
func ParseDiff(diff string) []DiffLine {
	var lines []DiffLine
	var currentFile string
	var oldLine, newLine int
	state := parseStateHeader

	for _, raw := range strings.Split(diff, "\n") {
		switch {
		case strings.HasPrefix(raw, "diff --git "):
			state = parseStateHeader
			currentFile = ""
			lines = append(lines, DiffLine{Kind: DiffHeader, Raw: raw})
		case strings.HasPrefix(raw, "--- ") || strings.HasPrefix(raw, "+++ "):
			if strings.HasPrefix(raw, "+++ ") && !strings.HasPrefix(raw, "+++ /dev/null") {
				currentFile = parseFilePath(raw[4:])
			}
			lines = append(lines, DiffLine{Kind: DiffFileMeta, File: currentFile, Raw: raw})
		case strings.HasPrefix(raw, "@@"):
			oldLine, newLine = parseHunkHeader(raw)
			state = parseStateHunk
			lines = append(lines, DiffLine{Kind: DiffHunkHeader, File: currentFile, Raw: raw, OldLineNum: oldLine, NewLineNum: newLine})
		case state == parseStateHunk:
			if len(raw) == 0 {
				// Empty line inside a hunk: treat as context with a single space prefix if the diff omitted it.
				lines = append(lines, DiffLine{Kind: DiffContext, File: currentFile, Raw: " ", OldLineNum: oldLine, NewLineNum: newLine})
				oldLine++
				newLine++
				continue
			}
			switch raw[0] {
			case '+':
				lines = append(lines, DiffLine{Kind: DiffAdded, File: currentFile, Raw: raw, NewLineNum: newLine})
				newLine++
			case '-':
				lines = append(lines, DiffLine{Kind: DiffRemoved, File: currentFile, Raw: raw, OldLineNum: oldLine})
				oldLine++
			default:
				lines = append(lines, DiffLine{Kind: DiffContext, File: currentFile, Raw: raw, OldLineNum: oldLine, NewLineNum: newLine})
				oldLine++
				newLine++
			}
		default:
			lines = append(lines, DiffLine{Kind: DiffHeader, Raw: raw})
		}
	}
	return lines
}

type parseState int

const (
	parseStateHeader parseState = iota
	parseStateHunk
)

var hunkHeaderRe = regexp.MustCompile(`@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@`)

func parseHunkHeader(line string) (oldLine, newLine int) {
	m := hunkHeaderRe.FindStringSubmatch(line)
	if len(m) < 3 {
		return 0, 0
	}
	fmt.Sscanf(m[1], "%d", &oldLine)
	fmt.Sscanf(m[2], "%d", &newLine)
	return oldLine, newLine
}

func parseFilePath(raw string) string {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "a/") || strings.HasPrefix(raw, "b/") {
		return raw[2:]
	}
	return raw
}
