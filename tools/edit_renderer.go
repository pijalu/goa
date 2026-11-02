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
	_ tuirender.ToolRenderer      = (*EditFileRenderer)(nil)
	_ tuirender.StreamingRenderer = (*EditFileRenderer)(nil)
)

func NewEditFileRenderer() *EditFileRenderer {
	return &EditFileRenderer{KeyExpand: KeyExpandLabel}
}

var (
	editDiffHunkRe = regexp.MustCompile(`^@@\s+-(\d+)(?:,(\d+))?\s+\+(\d+)(?:,(\d+))?\s+@@`)
)

// RenderCall displays "edit <path>" with the path relative to cwd. Batch
// calls carry the target inside edits[] (the tool accepts both shapes — and
// models commonly use the nested one), so fall back to the first entry and
// say how many more edits are queued: the file name is the single most
// important piece of context on the call line (bugs.md BUG-4). Multi-file
// batch calls (bugs.md B40) name the first target plus the remaining file
// count, or the glob pattern itself.
func (r *EditFileRenderer) RenderCall(args map[string]any, ctx tuirender.RenderContext) string {
	if files := editTargetFiles(args); len(files) > 0 {
		title := rToolTitle("edit") + " " + rAccent(formatPathRelativeToCwdOrAbsolute(files[0], ctx.Cwd))
		if len(files) > 1 {
			title += rMuted(fmt.Sprintf(" (+%d more files)", len(files)-1))
		}
		return title
	}
	if glob := stringArg(args, "glob"); glob != "" {
		return rToolTitle("edit") + " " + rAccent("glob:"+glob)
	}
	path, count := editTargetPath(args)
	if path == "" {
		path = "..."
	}
	title := rToolTitle("edit") + " " + rAccent(formatPathRelativeToCwdOrAbsolute(path, ctx.Cwd))
	if count > 1 {
		title += rMuted(fmt.Sprintf(" (+%d more edits)", count-1))
	}
	return title
}

// editTargetFiles lists the per-file paths of a multi-file batch call.
func editTargetFiles(args map[string]any) []string {
	files, ok := args["files"].([]any)
	if !ok {
		return nil
	}
	var paths []string
	for _, f := range files {
		m, ok := f.(map[string]any)
		if !ok {
			continue
		}
		if p := stringArg(m, "path"); p != "" {
			paths = append(paths, p)
		}
	}
	return paths
}

// renderPartialMultiFile renders the streaming preview for the multi-file
// forms: the file count for 'files', the pattern plus edit count for 'glob'.
// Empty result: the call is not a multi-file form.
func (r *EditFileRenderer) renderPartialMultiFile(args map[string]any) string {
	if files := editTargetFiles(args); len(files) > 0 {
		return rMuted(fmt.Sprintf("  %d files", len(files)))
	}
	glob := stringArg(args, "glob")
	if glob == "" {
		return ""
	}
	if edits, ok := args["edits"].([]any); ok && len(edits) > 0 {
		return rMuted(fmt.Sprintf("  glob:%s — %d edits", glob, len(edits)))
	}
	return rMuted("  glob:" + glob)
}

// editTargetPath resolves the file an edit targets: top-level "path" first,
// then the per-entry path models commonly nest inside edits[] (the same
// precedence the tool itself applies). The second return is the number of
// queued edits (1 for flat calls).
func editTargetPath(args map[string]any) (string, int) {
	if p := stringArg(args, "path"); p != "" {
		return p, 1
	}
	edits, ok := args["edits"].([]any)
	if !ok || len(edits) == 0 {
		return "", 0
	}
	for _, e := range edits {
		m, ok := e.(map[string]any)
		if !ok {
			continue
		}
		if p := stringArg(m, "path"); p != "" {
			return p, len(edits)
		}
	}
	return "", len(edits)
}

// RenderPartial implements tuirender.StreamingRenderer. While the edit tool
// arguments are still streaming, it shows a compact diffstat preview so the
// user sees the edit's scope as it arrives: line counts for the old and new
// content, or the operation name when the full content is not yet available.
func (r *EditFileRenderer) RenderPartial(args map[string]any, ctx tuirender.RenderContext) string {
	if out := r.renderPartialMultiFile(args); out != "" {
		return out
	}
	if edits, ok := args["edits"].([]any); ok && len(edits) > 0 {
		if p, _ := editTargetPath(args); p != "" {
			return rMuted(fmt.Sprintf("  %s — %d edits", formatPathRelativeToCwdOrAbsolute(p, ctx.Cwd), len(edits)))
		}
		return rMuted(fmt.Sprintf("  %d edits", len(edits)))
	}
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

// RenderResult renders the edit output as colored, line-numbered diffs. A
// multi-file batch output (bugs.md B40) carries one hunk per file with the
// per-file summary above it; each section renders independently and is
// separated from the next by a blank line.
func (r *EditFileRenderer) RenderResult(output string, ctx tuirender.RenderContext) string {
	if output == "" {
		return ""
	}
	sections, err := extractDiffSections(output)
	if err != nil {
		// Not a diff-style result; fall back to plain output (errors are already red by the caller).
		return rToolOutput(output)
	}
	var rendered []string
	for _, sec := range sections {
		out := r.renderSection(sec, ctx)
		if out == "" {
			continue
		}
		rendered = append(rendered, out)
	}
	if len(rendered) == 0 {
		return ""
	}
	return strings.Join(rendered, "\n")
}

// renderSection renders one diff section: the muted header lines (the tool's
// per-file summary), then the capped, line-numbered hunk.
func (r *EditFileRenderer) renderSection(sec diffSection, ctx tuirender.RenderContext) string {
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
		maxLines = len(sec.info.lines)
	}
	width := diffLineNumberWidth(sec.info.lines, sec.info.oldStart, sec.info.newStart)
	toRender := sec.info.lines
	if len(toRender) > maxLines {
		toRender = toRender[:maxLines]
	}
	rendered := renderDiffLinesWithWidth(toRender, sec.info.oldStart, sec.info.newStart, width)
	if len(rendered) == 0 && len(sec.header) == 0 {
		return ""
	}
	remaining := len(sec.info.lines) - len(toRender)
	return r.formatDiffOutput(sec.header, rendered, ctx.Expanded, maxLines, r.KeyExpand, remaining)
}

func (r *EditFileRenderer) formatDiffOutput(header, rendered []string, expanded bool, maxLines int, key string, remaining int) string {
	if expanded {
		maxLines = len(rendered)
	}
	display := rendered
	if len(rendered) > maxLines {
		display = rendered[:maxLines]
		remaining = len(rendered) - len(display)
	}

	var b strings.Builder
	// The tool's summary ("[edit: <path>] N edits applied — …", plus any
	// fuzzy-match note) sits above the first hunk; keep it — muted — so the
	// rendered diff still names the file it edits (bugs.md BUG-4).
	for _, h := range header {
		b.WriteString(rMuted(h))
		b.WriteByte('\n')
	}
	for i, line := range display {
		if i > 0 {
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

// diffSection is one rendered diff block: the preamble lines above the hunk
// (the tool's "[edit: <path>] …" summary) and the hunk itself. A multi-file
// batch output yields one section per file.
type diffSection struct {
	header []string
	info   diffInfo
}

// extractDiffSections splits an edit-tool output into its diff sections, one
// per "@@" hunk. Non-empty lines before the first hunk become the first
// section's header; non-empty lines between two hunks become the next
// section's header (the per-file summaries a multi-file batch emits). An
// output without any hunk is an error — callers fall back to plain text.
func extractDiffSections(output string) ([]diffSection, error) {
	p := &diffSectionParser{}
	for _, line := range strings.Split(output, "\n") {
		if matches := editDiffHunkRe.FindStringSubmatch(line); len(matches) > 0 {
			p.startHunk(matches)
			continue
		}
		p.addLine(line)
	}
	return p.finish()
}

// diffSectionParser assembles diffSections from an edit-tool output, one
// section per "@@" hunk.
type diffSectionParser struct {
	sections []diffSection
	cur      *diffSection
	pending  []string // header lines collected before the next hunk
}

// addLine routes one non-hunk output line. Blank lines are dropped.
// Unified-diff bodies only contain ' ', '+', '-' and '\\' marker lines, so a
// non-diff line between hunks is the next file's summary (a multi-file batch
// emits one per file): it starts a new pending header. Everything else lands
// in the open section's body or the pending first header.
func (p *diffSectionParser) addLine(line string) {
	if strings.TrimSpace(line) == "" {
		return
	}
	if p.cur != nil && !isDiffBodyLine(line) {
		p.flush()
		p.pending = []string{line}
		return
	}
	if p.cur == nil {
		p.pending = append(p.pending, line)
		return
	}
	p.cur.info.lines = append(p.cur.info.lines, line)
}

// startHunk closes the open section and opens a new one for the "@@" match.
func (p *diffSectionParser) startHunk(matches []string) {
	p.flush()
	p.cur = &diffSection{
		header: p.pending,
		info:   parseHunkHeader(matches),
	}
	p.pending = nil
}

// flush appends the open section, if any, to the result.
func (p *diffSectionParser) flush() {
	if p.cur != nil {
		p.sections = append(p.sections, *p.cur)
		p.cur = nil
	}
}

// finish closes the trailing section and requires at least one hunk.
func (p *diffSectionParser) finish() ([]diffSection, error) {
	p.flush()
	if len(p.sections) == 0 {
		return nil, fmt.Errorf("no diff hunk found")
	}
	return p.sections, nil
}

func parseHunkHeader(matches []string) diffInfo {
	return diffInfo{
		oldStart: parseIntField(matches[1]),
		oldCount: parseOptionalInt(matches[2], 1),
		newStart: parseIntField(matches[3]),
		newCount: parseOptionalInt(matches[4], 1),
	}
}

// isDiffBodyLine reports whether a line belongs to a unified-diff body:
// context (' '), added ('+'), removed ('-') or the "\ No newline" marker.
func isDiffBodyLine(line string) bool {
	if line == "" {
		return false
	}
	switch line[0] {
	case ' ', '+', '-', '\\':
		return true
	}
	return false
}

func parseIntField(s string) int {
	v, err := strconv.Atoi(s)
	if err != nil {
		return 1
	}
	return v
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
