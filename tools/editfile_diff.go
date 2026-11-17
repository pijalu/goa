// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"fmt"
	"strings"

	"github.com/pmezard/go-difflib/difflib"
)

// truncateStr returns s truncated to n runes with "..." suffix.
func truncateStr(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "..."
}

// generateUnifiedDiff produces independent, context-bounded unified-diff
// hunks. Grouping opcodes is essential: a single hunk from the first through
// the last edit reprints every unchanged line between distant batch edits.
func generateUnifiedDiff(oldLines, newLines []string) string {
	matcher := difflib.NewMatcherWithJunk(oldLines, newLines, false, nil)
	groups := matcher.GetGroupedOpCodes(diffContextLines)
	var b strings.Builder
	for i, group := range groups {
		if len(group) == 0 {
			continue
		}
		if i > 0 {
			b.WriteByte('\n')
		}
		writeDiffHunk(&b, group, oldLines, newLines)
	}
	return strings.TrimSuffix(b.String(), "\n")
}

func writeDiffHunk(b *strings.Builder, group []difflib.OpCode, oldLines, newLines []string) {
	first, last := group[0], group[len(group)-1]
	oldStart, oldCount := unifiedDiffRange(first.I1, last.I2)
	newStart, newCount := unifiedDiffRange(first.J1, last.J2)
	fmt.Fprintf(b, "@@ -%d,%d +%d,%d @@\n", oldStart, oldCount, newStart, newCount)
	for _, op := range group {
		writeDiffOpcode(b, op, oldLines, newLines)
	}
}

// unifiedDiffRange converts zero-based, end-exclusive offsets to unified-diff
// coordinates. Empty ranges point at the line before the insertion/deletion.
func unifiedDiffRange(start, stop int) (line, count int) {
	line = start + 1
	count = stop - start
	if count == 0 {
		line--
	}
	return line, count
}

func writeDiffOpcode(b *strings.Builder, op difflib.OpCode, oldLines, newLines []string) {
	switch op.Tag {
	case 'e':
		writeDiffRange(b, " ", oldLines, op.I1, op.I2)
	case 'd':
		writeDiffRange(b, "-", oldLines, op.I1, op.I2)
	case 'i':
		writeDiffRange(b, "+", newLines, op.J1, op.J2)
	case 'r':
		writeDiffRange(b, "-", oldLines, op.I1, op.I2)
		writeDiffRange(b, "+", newLines, op.J1, op.J2)
	}
}

func writeDiffRange(b *strings.Builder, prefix string, lines []string, from, to int) {
	for i := from; i < to; i++ {
		fmt.Fprintf(b, "%s%s\n", prefix, lines[i])
	}
}
