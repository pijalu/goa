// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/pijalu/goa/internal/ansi"
	"github.com/pijalu/goa/internal/tuirender"
)

// EditFileRenderer renders the edit tool call and its result diff.
type EditFileRenderer struct {
	KeyExpand string
}

// editDiffPreviewLines is the default number of diff lines shown before
// collapsing. It is large enough that ordinary edits are fully visible, while
// still offering the expand action for exceptionally large diffs.
const editDiffPreviewLines = 1000

var (
	_ tuirender.ToolRenderer        = (*EditFileRenderer)(nil)
	_ tuirender.StreamingRenderer   = (*EditFileRenderer)(nil)
)

func NewEditFileRenderer() *EditFileRenderer {
	return &EditFileRenderer{KeyExpand: KeyExpandLabel}
}

var (
	editDiffHunkRe = regexp.MustCompile(`^@@\s+-(\d+)(?:,(\d+))?\s+\+(\d+)(?:,(\d+))?\s+@@`)
)

// RenderCall displays "edit <path>" with the path relative to cwd.
func (r *EditFileRenderer) RenderCall(args map[string]any, ctx tuirender.RenderContext) string {
	path := stringArg(args, "path")
	if path == "" {
		path = "..."
	}
	return rToolTitle("edit") + " " + rAccent(formatPathRelativeToCwdOrAbsolute(path, ctx.Cwd))
}

// RenderPartial implements tuirender.StreamingRenderer. While the edit tool
// arguments are still streaming, it shows a compact diffstat preview so the
// user sees the edit's scope as it arrives: line counts for the old and new
// content, or the operation name when the full content is not yet available.
func (r *EditFileRenderer) RenderPartial(args map[string]any, ctx tuirender.RenderContext) string {
	oldStr := stringArg(args, "old_string")
	newStr := stringArg(args, "new_string")
	op := stringArg(args, "operation")

	var parts []string
	if oldStr != "" {
		oldLines := strings.Count(oldStr, "\n") + 1
		parts = append(parts, fmt.Sprintf("-%d lines", oldLines))
	}
	if newStr != "" {
		newLines := strings.Count(newStr, "\n") + 1
		parts = append(parts, fmt.Sprintf("+%d lines", newLines))
	}
	if len(parts) == 0 && op != "" {
		parts = append(parts, fmt.Sprintf("operation: %s", op))
	}
	if len(parts) == 0 {
		return ""
	}
	return rMuted("  " + strings.Join(parts, ", "))
}

// RenderResult renders the edit output as a colored, line-numbered diff.
func (r *EditFileRenderer) RenderResult(output string, ctx tuirender.RenderContext) string {
	if output == "" {
		return ""
	}
	// Try to locate a unified-diff hunk in the output.
	diff, err := extractDiff(output)
	if err != nil {
		// Not a diff-style result; fall back to plain output (errors are already red by the caller).
		return rToolOutput(output)
	}

	// Cap the diff lines we colorize to what will actually be displayed. On a
	// collapsed view that is PreviewLines (or ctx override); on an expanded
	// view it is the whole diff. renderDiffLines is O(n) in time AND memory
	// (per-line expandTabs + padding + colorize + intra-line LCS for single
	// -/+ pairs); rendering a 10k-line diff to show 1k wasted ~130ms and
	// ~135MB of garbage on the commandLoop, hitching the TUI on every large
	// edit. Line-number width must be computed over the FULL diff so numbers
	// stay correctly aligned after truncation.
	maxLines := previewLinesFromCtx(ctx, r.PreviewLines())
	if ctx.Expanded {
		maxLines = len(diff.lines)
	}
	width := diffLineNumberWidth(diff.lines, diff.oldStart, diff.newStart)
	toRender := diff.lines
	if len(toRender) > maxLines {
		toRender = toRender[:maxLines]
	}
	rendered := renderDiffLinesWithWidth(toRender, diff.oldStart, diff.newStart, width)
	if len(rendered) == 0 {
		return ""
	}
	remaining := len(diff.lines) - len(toRender)

	return r.formatDiffOutput(rendered, ctx.Expanded, maxLines, r.KeyExpand, remaining)
}

func (r *EditFileRenderer) formatDiffOutput(rendered []string, expanded bool, maxLines int, key string, remaining int) string {
	if expanded {
		maxLines = len(rendered)
	}
	display := rendered
	if len(rendered) > maxLines {
		display = rendered[:maxLines]
		remaining = len(rendered) - len(display)
	}

	var b strings.Builder
	for _, line := range display {
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(line)
	}
	if remaining > 0 {
		b.WriteString("\n")
		b.WriteString(expandHint(remaining, key))
	}
	return b.String()
}

func (r *EditFileRenderer) PreviewLines() int             { return editDiffPreviewLines }
func (r *EditFileRenderer) HideResultWhenCollapsed() bool { return false }

// editDiffPreviewLines is the default number of diff lines shown before
// collapsing. It is large enough that ordinary edits are fully visible, while
// still offering the expand action for exceptionally large diffs.

// diffInfo holds the parsed unified-diff hunk.
type diffInfo struct {
	lines    []string
	oldStart int
	oldCount int
	newStart int
	newCount int
}

// extractDiff finds the first unified-diff hunk in output and parses its header.
func extractDiff(output string) (diffInfo, error) {
	lines := strings.Split(output, "\n")
	for i, line := range lines {
		matches := editDiffHunkRe.FindStringSubmatch(line)
		if len(matches) == 0 {
			continue
		}
		diff := parseHunkHeader(matches, lines, i)
		return diff, nil
	}
	return diffInfo{}, fmt.Errorf("no diff hunk found")
}

func parseHunkHeader(matches []string, lines []string, hunkIndex int) diffInfo {
	oldStart, _ := strconv.Atoi(matches[1])
	oldCount := parseOptionalInt(matches[2], 1)
	newStart, _ := strconv.Atoi(matches[3])
	newCount := parseOptionalInt(matches[4], 1)

	body := make([]string, 0, len(lines)-hunkIndex-1)
	for j := hunkIndex + 1; j < len(lines); j++ {
		if strings.TrimSpace(lines[j]) == "" {
			continue
		}
		body = append(body, lines[j])
	}
	return diffInfo{
		lines:    body,
		oldStart: oldStart,
		oldCount: oldCount,
		newStart: newStart,
		newCount: newCount,
	}
}

func parseOptionalInt(s string, fallback int) int {
	if s == "" {
		return fallback
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return fallback
	}
	return v
}

// renderDiffLines turns unified-diff lines into colored, numbered lines.
func renderDiffLines(lines []string, oldStart, newStart int) []string {
	return renderDiffLinesWithWidth(lines, oldStart, newStart, diffLineNumberWidth(lines, oldStart, newStart))
}

// renderDiffLinesWithWidth renders diff lines using a pre-computed line-number
// width, so callers can cap the slice to the display size while keeping
// line-number alignment consistent with the full diff.
func renderDiffLinesWithWidth(lines []string, oldStart, newStart, width int) []string {
	state := diffState{oldLine: oldStart, newLine: newStart}
	var result []string
	for state.i < len(lines) {
		line := lines[state.i]
		prefix := ""
		if len(line) > 0 {
			prefix = string(line[0])
		}
		content := expandTabs(line[1:])

		switch prefix {
		case "-":
			result = append(result, renderRemovedBlock(lines, &state, width)...)
		case "+":
			result = append(result, formatDiffLine("+", state.newLine, content, width, rDiffAdded))
			state.newLine++
			state.i++
		default:
			result = append(result, formatDiffLine(" ", state.oldLine, content, width, rDiffContext))
			state.oldLine++
			state.newLine++
			state.i++
		}
	}
	return result
}

type diffState struct {
	oldLine int
	newLine int
	i       int
}

func renderRemovedBlock(lines []string, state *diffState, width int) []string {
	removed, removedLineNums, added, addedLineNums := collectChange(lines, state)
	if len(removed) == 1 && len(added) == 1 {
		remLine, addLine := intraLineDiff(removed[0], added[0])
		return []string{
			formatDiffLine("-", removedLineNums[0], remLine, width, rDiffRemoved),
			formatDiffLine("+", addedLineNums[0], addLine, width, rDiffAdded),
		}
	}
	var result []string
	for idx, rem := range removed {
		result = append(result, formatDiffLine("-", removedLineNums[idx], rem, width, rDiffRemoved))
	}
	for idx, add := range added {
		result = append(result, formatDiffLine("+", addedLineNums[idx], add, width, rDiffAdded))
	}
	return result
}

func diffLineNumberWidth(lines []string, oldStart, newStart int) int {
	state := lineCounter{oldLine: oldStart, newLine: newStart, maxLine: oldStart}
	for _, l := range lines {
		updateLineCounter(&state, l)
	}
	return len(strconv.Itoa(state.maxLine))
}

type lineCounter struct {
	oldLine int
	newLine int
	maxLine int
}

func updateLineCounter(state *lineCounter, l string) {
	switch {
	case strings.HasPrefix(l, "+"):
		state.newLine++
		if state.newLine > state.maxLine {
			state.maxLine = state.newLine
		}
	case strings.HasPrefix(l, "-"):
		state.oldLine++
		if state.oldLine > state.maxLine {
			state.maxLine = state.oldLine
		}
	default:
		state.oldLine++
		state.newLine++
		if state.oldLine > state.maxLine {
			state.maxLine = state.oldLine
		}
	}
}

func collectChange(lines []string, state *diffState) (removed []string, removedLineNums []int, added []string, addedLineNums []int) {
	for state.i < len(lines) && len(lines[state.i]) > 0 && lines[state.i][0] == '-' {
		removed = append(removed, expandTabs(lines[state.i][1:]))
		removedLineNums = append(removedLineNums, state.oldLine)
		state.oldLine++
		state.i++
	}
	for state.i < len(lines) && len(lines[state.i]) > 0 && lines[state.i][0] == '+' {
		added = append(added, expandTabs(lines[state.i][1:]))
		addedLineNums = append(addedLineNums, state.newLine)
		state.newLine++
		state.i++
	}
	return removed, removedLineNums, added, addedLineNums
}

// formatDiffLine formats a numbered diff line with the given colorizer.
func formatDiffLine(prefix string, lineNum int, content string, width int, colorize func(string) string) string {
	numStr := strconv.Itoa(lineNum)
	for len(numStr) < width {
		numStr = " " + numStr
	}
	return colorize(prefix + numStr + " " + content)
}

// expandTabs replaces tabs with spaces for aligned rendering.
// expandTabs expands tabs for display and sanitizes control bytes: diff
// content is raw file text, and a stray ESC byte must render as visible
// text, never reach the terminal as a command.
func expandTabs(s string) string {
	return strings.ReplaceAll(ansi.Sanitize(s), "\t", "   ")
}

// intraLineDiff computes a lightweight word/whitespace-level diff between two
// lines and returns colored strings with inverse video on changed tokens.
func intraLineDiff(oldLine, newLine string) (string, string) {
	oldTokens := tokenizeForDiff(oldLine)
	newTokens := tokenizeForDiff(newLine)

	// Find longest common subsequence indexes in oldTokens.
	lcs := longestCommonSubsequence(oldTokens, newTokens)
	oldInLCS := make([]bool, len(oldTokens))
	for _, idx := range lcs.oldIdxs {
		oldInLCS[idx] = true
	}
	newInLCS := make([]bool, len(newTokens))
	for _, idx := range lcs.newIdxs {
		newInLCS[idx] = true
	}

	removed := buildIntraLine(oldTokens, oldInLCS, rDiffRemoved, rInverse)
	added := buildIntraLine(newTokens, newInLCS, rDiffAdded, rInverse)
	return removed, added
}

// tokenizeForDiff splits a line into whitespace and non-whitespace tokens.
func tokenizeForDiff(line string) []string {
	var tokens []string
	var cur strings.Builder
	inSpace := false
	for _, r := range line {
		isSpace := r == ' ' || r == '\t'
		if cur.Len() == 0 || isSpace == inSpace {
			cur.WriteRune(r)
			inSpace = isSpace
			continue
		}
		tokens = append(tokens, cur.String())
		cur.Reset()
		cur.WriteRune(r)
		inSpace = isSpace
	}
	if cur.Len() > 0 {
		tokens = append(tokens, cur.String())
	}
	return tokens
}

// lcsResult holds the indexes of the longest common subsequence.
type lcsResult struct {
	oldIdxs []int
	newIdxs []int
}

// longestCommonSubsequence returns the indexes of an LCS between a and b.
func longestCommonSubsequence(a, b []string) lcsResult {
	m := len(a)
	n := len(b)
	if m == 0 || n == 0 {
		return lcsResult{}
	}
	dp := lcsTable(a, b, m, n)
	return lcsBacktrack(dp, a, b, m, n)
}

func lcsTable(a, b []string, m, n int) [][]int {
	dp := make([][]int, m+1)
	for i := range dp {
		dp[i] = make([]int, n+1)
	}
	for i := 1; i <= m; i++ {
		for j := 1; j <= n; j++ {
			dp[i][j] = lcsCell(dp, a, b, i, j)
		}
	}
	return dp
}

func lcsCell(dp [][]int, a, b []string, i, j int) int {
	if a[i-1] == b[j-1] {
		return dp[i-1][j-1] + 1
	}
	if dp[i-1][j] > dp[i][j-1] {
		return dp[i-1][j]
	}
	return dp[i][j-1]
}

func lcsBacktrack(dp [][]int, a, b []string, m, n int) lcsResult {
	var oldIdxs, newIdxs []int
	i, j := m, n
	for i > 0 && j > 0 {
		if a[i-1] == b[j-1] {
			oldIdxs = append([]int{i - 1}, oldIdxs...)
			newIdxs = append([]int{j - 1}, newIdxs...)
			i--
			j--
			continue
		}
		if dp[i-1][j] > dp[i][j-1] {
			i--
		} else {
			j--
		}
	}
	return lcsResult{oldIdxs: oldIdxs, newIdxs: newIdxs}
}

// buildIntraLine rebuilds a line, applying inverse video to tokens not in the LCS.
func buildIntraLine(tokens []string, inLCS []bool, colorize, inverse func(string) string) string {
	var b strings.Builder
	for i, tok := range tokens {
		if inLCS[i] {
			b.WriteString(colorize(tok))
		} else {
			b.WriteString(colorize(inverse(tok)))
		}
	}
	return b.String()
}
