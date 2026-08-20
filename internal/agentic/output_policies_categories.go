// SPDX-License-Identifier: GPL-3.0-or-later
//
// Category budgets for semantic output budgeting (P3, dsh output-policies
// parity). Each budget selects the lines that carry the most signal for its
// tool family within the shared byte cap (lineWindow in output_policies.go):
//
//   - file    (read)      → header + alternating start/end lines
//   - process (bash/bg_exec) → first lines + diagnostics + tail, repeats collapsed
//   - test    (verify/tests) → failure lines + ±3 context + tail summaries
//   - search  (search/smartsearch/lsp) → per-file representative + summary lines
//
// Kept lines are emitted in original order and the omission marker reports how
// many lines were retained, so the model can re-read a narrower range.

package agentic

import (
	"fmt"
	"regexp"
	"strings"
)

// --- file category (read): header + alternating ends ---

// budgetFileResult keeps the header line (read results start with the file
// path and line range), then alternates one line from the start and one from
// the end of the remaining lines so both the beginning and the end of the
// window survive. Kept lines are emitted in original order.
func budgetFileResult(output string, limit int) string {
	w := newLineWindow(output, limit)
	if len(w.lines) <= 3 {
		return budgetDefaultResult(output, limit)
	}
	var candidates []int
	candidates = append(candidates, 0) // header
	lo, hi := 1, len(w.lines)-1
	for lo <= hi {
		candidates = append(candidates, lo)
		lo++
		if lo <= hi {
			candidates = append(candidates, hi)
			hi--
		}
	}
	return w.build(limit, candidates)
}

// --- process category (bash/bg_exec): first lines + diagnostics + tail ---

// diagnosticMarkers mark lines that carry process diagnostics (errors,
// warnings, exit status) and get priority retention after the first lines.
var diagnosticMarkers = []string{
	"error", "Error", "ERROR",
	"warning", "Warning", "WARN",
	"failed", "Failed", "FAILED",
	"panic", "exit status", "Traceback",
	"cannot", "undefined", "fatal",
}

// isDiagnosticLine reports whether a line looks like a process diagnostic.
func isDiagnosticLine(line string) bool {
	for _, m := range diagnosticMarkers {
		if strings.Contains(line, m) {
			return true
		}
	}
	return false
}

// budgetProcessResult keeps the first up-to-10 lines, then diagnostic lines,
// then the last up-to-16 lines, with consecutive duplicate lines collapsed
// into a repeat marker. Selection priority follows the plan (first lines,
// diagnostics, tail); the output is emitted in original order.
func budgetProcessResult(output string, limit int) string {
	w := newLineWindow(output, limit)
	lines := w.lines
	if len(lines) <= 3 {
		return budgetDefaultResult(output, limit)
	}
	first := len(lines)
	if first > 10 {
		first = 10
	}
	lastStart := len(lines) - 16
	if lastStart < first {
		lastStart = first
	}
	selected := make([]bool, len(lines))
	var candidates []int
	// Bucket 1: first lines.
	for i := 0; i < first; i++ {
		if !selected[i] {
			selected[i] = true
			candidates = append(candidates, i)
		}
	}
	// Bucket 2: diagnostics anywhere.
	for i := range lines {
		if selected[i] {
			continue
		}
		if isDiagnosticLine(lines[i]) {
			selected[i] = true
			candidates = append(candidates, i)
		}
	}
	// Bucket 3: last lines.
	for i := lastStart; i < len(lines); i++ {
		if !selected[i] {
			selected[i] = true
			candidates = append(candidates, i)
		}
	}
	kept := w.build(limit, candidates)
	return collapseRepeats(kept)
}

// collapseRepeats collapses consecutive identical lines in a built result into
// a single line with a repeat count marker (dsh "duplicate lines collapsed
// with repeat marker"). It only runs on the retained body (before the marker).
func collapseRepeats(body string) string {
	idx := strings.Index(body, "[goa-system] Tool result truncated")
	if idx < 0 {
		return body
	}
	text := body[:idx]
	marker := body[idx:]
	lines := strings.Split(text, "\n")
	if len(lines) == 0 {
		return body
	}
	var out []string
	count := 1
	for i := 1; i < len(lines); i++ {
		if lines[i] == lines[i-1] {
			count++
			continue
		}
		out = append(out, repeatLine(lines[i-1], count))
		count = 1
	}
	out = append(out, repeatLine(lines[len(lines)-1], count))
	return strings.Join(out, "\n") + marker
}

// repeatLine renders a line, appending a repeat marker when it was seen
// count>1 consecutive times.
func repeatLine(line string, count int) string {
	if count <= 1 || line == "" {
		return line
	}
	return fmt.Sprintf("%s (×%d)", line, count)
}

// --- test category (verify/test): failures + context + tail summaries ---

// failureMarkers mark lines that carry test failures.
var failureMarkers = []string{
	"--- FAIL:",
	"FAIL\t",
	"FAILED",
	"= FAILURES =",
	"Error:", "error:",
	"Traceback (most recent call last)",
	"AssertionError",
	"not ok",
	"panic:",
	"Fatal",
	"failed",
}

// isFailureLine reports whether a line carries a test failure.
func isFailureLine(line string) bool {
	for _, m := range failureMarkers {
		if strings.Contains(line, m) {
			return true
		}
	}
	return false
}

// budgetTestResult keeps failure lines with ±3 lines of context, then the
// tail summary lines (exit code / PASS / FAIL totals). When the result carries
// no failure markers it is a passing run and the default head/tail cut is the
// right budget.
func budgetTestResult(output string, limit int) string {
	w := newLineWindow(output, limit)
	failures := failureLineIndexes(w.lines)
	if len(failures) == 0 {
		return budgetDefaultResult(output, limit)
	}
	selected := make([]bool, len(w.lines))
	var candidates []int
	for _, fi := range failures {
		candidates = appendFailureContext(candidates, selected, fi, len(w.lines))
	}
	// Tail summaries: last up-to-6 lines (exit code, PASS/FAIL totals).
	candidates = appendTailLines(candidates, selected, len(w.lines), 6)
	return w.build(limit, candidates)
}

// failureLineIndexes returns the indexes of lines carrying test-failure markers.
func failureLineIndexes(lines []string) []int {
	var idxs []int
	for i, l := range lines {
		if isFailureLine(l) {
			idxs = append(idxs, i)
		}
	}
	return idxs
}

// appendFailureContext appends the failure line at fi plus ±3 context lines,
// deduping against selected.
func appendFailureContext(candidates []int, selected []bool, fi, n int) []int {
	const context = 3
	lo, hi := fi-context, fi+context
	if lo < 0 {
		lo = 0
	}
	if hi >= n {
		hi = n - 1
	}
	return appendUnselected(candidates, selected, lo, hi)
}

// appendTailLines appends the last upTo lines (deduped against selected).
func appendTailLines(candidates []int, selected []bool, n, upTo int) []int {
	start := n - upTo
	if start < 0 {
		start = 0
	}
	return appendUnselected(candidates, selected, start, n-1)
}

// appendUnselected appends the indexes in [lo, hi] that are not already
// selected, marking them selected.
func appendUnselected(candidates []int, selected []bool, lo, hi int) []int {
	for i := lo; i <= hi; i++ {
		if !selected[i] {
			selected[i] = true
			candidates = append(candidates, i)
		}
	}
	return candidates
}

// --- search category (search/smartsearch/lsp): per-file representatives ---

// searchSummaryMarker marks lines that are global search summaries (the
// [search:/[smartsearch: headers, score ranges, truncation notes).
var searchSummaryMarker = []string{
	"[search:",
	"[smartsearch:",
	"matches across",
	"results from",
	"Score range",
	"truncated by max_lines",
	"(Index:",
	"Index was missing",
}

// searchFileEntryRe matches a file-entry line in the search formats:
//   - search:      `path/file.go: 12 matches`
//   - smartsearch: `1. [0.95] path/file.go  (12 lines)`
//   - lsp:         `Definitions (3):`
var (
	searchFileEntryRe = regexp.MustCompile(`^(\S+): \d+ matches?$`)
	smartFileEntryRe  = regexp.MustCompile(`^\d+\. \[.*\] .+`)
	lspFileEntryRe    = regexp.MustCompile(`^.+ \(\d+\):$`)
)

// isSummaryLine reports whether a line is a global search summary.
func isSummaryLine(line string) bool {
	for _, m := range searchSummaryMarker {
		if strings.Contains(line, m) {
			return true
		}
	}
	return false
}

// searchBlock is one file's entry line plus its indented content lines.
type searchBlock struct {
	entry   int   // index of the file-entry line
	content []int // indexes of the indented content lines
}

// parseSearchBlocks groups a search result into file blocks: a non-indented
// file-entry line (search/smartsearch/lsp) followed by its indented content
// lines. Returns the global summary line indexes and the ordered blocks.
func parseSearchBlocks(lines []string) (summaries []int, blocks []searchBlock) {
	var cur *searchBlock
	for i, l := range lines {
		if isSummaryLine(l) {
			summaries = append(summaries, i)
			cur = nil
			continue
		}
		if strings.HasPrefix(l, " ") || strings.HasPrefix(l, "\t") {
			if cur != nil {
				cur.content = append(cur.content, i)
			}
			continue
		}
		if isSearchFileEntry(l) {
			blocks = append(blocks, searchBlock{entry: i})
			cur = &blocks[len(blocks)-1]
			continue
		}
		cur = nil
	}
	return summaries, blocks
}

// isSearchFileEntry reports whether a non-indented line opens a file block in
// one of the search output formats.
func isSearchFileEntry(line string) bool {
	return searchFileEntryRe.MatchString(line) ||
		smartFileEntryRe.MatchString(line) ||
		lspFileEntryRe.MatchString(line)
}

// budgetSearchResult keeps global summary lines, then one entry line per file,
// then the first content line of each file (the per-file representative), then
// deeper content lines round-robin across files so no single file's deep lines
// crowd out every other file's representative. Unrecognized formats fall back
// to the default head/tail cut.
func budgetSearchResult(output string, limit int) string {
	w := newLineWindow(output, limit)
	if len(w.lines) <= 3 {
		return budgetDefaultResult(output, limit)
	}
	summaries, blocks := parseSearchBlocks(w.lines)
	if len(blocks) == 0 {
		return budgetDefaultResult(output, limit)
	}
	selected := make([]bool, len(w.lines))
	var candidates []int
	// Bucket 1: global summary lines.
	candidates = appendSearchLines(candidates, selected, summaries)
	// Bucket 2: one file-entry line per file.
	for _, b := range blocks {
		candidates = appendSearchLines(candidates, selected, []int{b.entry})
	}
	// Bucket 3: the first content line of each file (per-file representative).
	for _, b := range blocks {
		if len(b.content) > 0 {
			candidates = appendSearchLines(candidates, selected, b.content[:1])
		}
	}
	// Bucket 4: deeper content lines round-robin across files.
	candidates = appendSearchRoundRobin(candidates, selected, blocks)
	return w.build(limit, candidates)
}

// appendSearchLines appends the given line indexes that are not already
// selected, marking them selected.
func appendSearchLines(candidates []int, selected []bool, idxs []int) []int {
	for _, i := range idxs {
		if !selected[i] {
			selected[i] = true
			candidates = append(candidates, i)
		}
	}
	return candidates
}

// appendSearchRoundRobin appends each block's depth-th content line, repeating
// at increasing depths until no block has a deeper line, so every file gets a
// representative before any file's deep lines crowd out the rest.
func appendSearchRoundRobin(candidates []int, selected []bool, blocks []searchBlock) []int {
	for depth := 1; ; depth++ {
		added := 0
		for _, b := range blocks {
			if depth < len(b.content) && !selected[b.content[depth]] {
				selected[b.content[depth]] = true
				candidates = append(candidates, b.content[depth])
				added++
			}
		}
		if added == 0 {
			return candidates
		}
	}
}
