// SPDX-License-Identifier: GPL-3.0-or-later
package tools

import (
	"fmt"
	"strings"
)

// truncateStr returns s truncated to n runes with "..." suffix.
func truncateStr(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "..."
}

// generateUnifiedDiff produces a unified-diff hunk comparing oldLines and newLines.
func generateUnifiedDiff(oldLines, newLines []string) string {
	start := firstDiff(oldLines, newLines)
	if start == len(oldLines) && start == len(newLines) {
		return ""
	}
	oldEnd, newEnd := diffEnds(oldLines, newLines, start)
	const context = 3
	ctxStart := maxInt(0, start-context)
	ctxOldEnd := minInt(len(oldLines), oldEnd+context)
	before, after := start-ctxStart, ctxOldEnd-oldEnd
	var b strings.Builder
	fmt.Fprintf(&b, "@@ -%d,%d +%d,%d @@\n", ctxStart+1, oldEnd-start+before+after, ctxStart+1, newEnd-start+before+after)
	writeDiffRange(&b, " ", oldLines, ctxStart, start)
	writeDiffRange(&b, "-", oldLines, start, oldEnd)
	writeDiffRange(&b, "+", newLines, start, newEnd)
	writeDiffRange(&b, " ", oldLines, oldEnd, ctxOldEnd)
	return strings.TrimSuffix(b.String(), "\n")
}

func firstDiff(oldLines, newLines []string) int {
	i := 0
	for i < len(oldLines) && i < len(newLines) && oldLines[i] == newLines[i] {
		i++
	}
	return i
}
func diffEnds(oldLines, newLines []string, start int) (int, int) {
	o, n := len(oldLines), len(newLines)
	for o > start && n > start && oldLines[o-1] == newLines[n-1] {
		o--
		n--
	}
	return o, n
}
func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
func writeDiffRange(b *strings.Builder, prefix string, lines []string, from, to int) {
	for i := from; i < to; i++ {
		fmt.Fprintf(b, "%s%s\n", prefix, lines[i])
	}
}
