// SPDX-License-Identifier: GPL-3.0-or-later
//
// Category-aware semantic output budgeting (P3, dsh output-policies parity).
//
// Uniform head/tail truncation keeps both ends of an oversized tool result but
// discards the semantically important middle: test failures, diagnostics, and
// per-file search matches all sit between the first and last lines. This file
// replaces the uniform cut with per-category retention — the result is still
// byte-bounded, still carries an omission marker, and still lets the beginning
// and end survive, but the middle is selected by what the category values:
//
//   - read            → file window: header + alternating start/end lines
//   - bash/bg_exec    → process: first lines + diagnostics + tail, repeats collapsed
//   - verify/tests    → test: failure lines + ±3 context + tail summaries
//   - search/lsp      → search: per-file representative + summary lines
//   - everything else → default head/tail (webfetch, unknown tools)
//
// Classification is by tool name plus a content heuristic: a bash result that
// looks like test-run output is budgeted as a test result, so `go test` failures
// piped through bash keep their failure lines too.

package agentic

import (
	"fmt"
	"strings"
)

// outputCategory is the semantic budget family of a tool result.
type outputCategory int

const (
	categoryDefault outputCategory = iota
	categoryFile
	categoryProcess
	categoryTest
	categorySearch
)

// semanticMarkerFmt is the omission marker shared by the semantic categories.
// It reports the byte budget, the original size, and how many lines were
// retained so the model knows content was dropped and can re-read narrowly.
const semanticMarkerFmt = "\n[goa-system] Tool result truncated to ~%d bytes (original %d bytes); %d of %d lines retained to preserve the most relevant content.\n"

// defaultMarkerFmt is the legacy head/tail omission marker (kept verbatim for
// the default category so existing consumers and tests keep working).
const defaultMarkerFmt = "\n[goa-system] Tool result was truncated to ~%d bytes (original %d bytes); the middle was elided, the beginning and end are preserved.\n"

// compactMarker is the omission marker used when the cap is too small to carry
// the full detailed marker. It keeps the byte-budget guarantee (result ≤ limit)
// while still reporting that content was dropped.
const compactMarker = "\n[goa-system] truncated\n"

// truncateToolResult caps a tool result to roughly limit bytes while preserving
// the category-relevant content (P3). The beginning and end survive for every
// category; the middle is selected by the tool's semantic budget instead of a
// uniform cut. The returned replacement never exceeds limit bytes and always
// carries an omission marker when content was dropped. A result at or under
// the limit is returned verbatim.
func truncateToolResult(toolName, output string, limit int) string {
	return semanticBudgetResult(toolName, output, limit)
}

// SemanticBudgetResult is the exported seam for the tools package (the spill
// policy preview): the category-aware bounded replacement for a plain-text tool
// result. It never exceeds limit bytes. A result at or under the limit is
// returned verbatim.
func SemanticBudgetResult(toolName, output string, limit int) string {
	return semanticBudgetResult(toolName, output, limit)
}

// semanticBudgetResult dispatches an over-budget result to its category budget.
func semanticBudgetResult(toolName, output string, limit int) string {
	if limit <= 0 || len(output) <= limit {
		return output
	}
	if len(compactMarker) >= limit {
		// Cap too small to carry any omission marker: fall back to a plain
		// rune-safe head/tail slice so the result is still byte-bounded (the
		// caller's own notice, when present, reports the omission).
		return cutRuneHead(output, (limit+1)/2) + cutRuneTail(output, limit/2)
	}
	switch classifyToolResult(toolName, output) {
	case categoryFile:
		return budgetFileResult(output, limit)
	case categoryProcess:
		return budgetProcessResult(output, limit)
	case categoryTest:
		return budgetTestResult(output, limit)
	case categorySearch:
		return budgetSearchResult(output, limit)
	default:
		return budgetDefaultResult(output, limit)
	}
}

// classifyToolResult maps a tool result to its semantic budget category by
// tool name plus a content heuristic (a process-looking result that carries
// test-run markers is budgeted as a test result).
func classifyToolResult(toolName, content string) outputCategory {
	switch toolName {
	case "read":
		return categoryFile
	case "bash", "bg_exec", "ssh_bash":
		if looksLikeTestOutput(content) {
			return categoryTest
		}
		return categoryProcess
	case "verify":
		return categoryTest
	case "search", "smartsearch", "lsp":
		return categorySearch
	default:
		return categoryDefault
	}
}

// testOutputMarkers are content heuristics that mark a result as test-run
// output (go test, pytest, jest, cargo test, ...) even when it arrived through
// a process-classified tool (bash running the suite).
var testOutputMarkers = []string{
	"--- FAIL:",
	"--- PASS:",
	"=== RUN",
	"FAIL\t",
	"ok  \t",
	"FAILED",
	"= FAILURES =",
	"Traceback (most recent call last)",
	"AssertionError",
	"test failed",
	"Tests failed",
	"tests failed",
}

// looksLikeTestOutput reports whether the content carries test-run markers.
func looksLikeTestOutput(content string) bool {
	for _, m := range testOutputMarkers {
		if strings.Contains(content, m) {
			return true
		}
	}
	return false
}

// --- default category: head/tail ---

// budgetDefaultResult is the legacy uniform head/tail cut (byte-bounded,
// rune-safe, marker reserved out of the cap).
func budgetDefaultResult(output string, limit int) string {
	marker := fmt.Sprintf(defaultMarkerFmt, limit, len(output))
	if len(marker) >= limit {
		marker = compactMarker
	}
	half := (limit - len(marker)) / 2
	if len(output) <= half*2+len(marker) {
		return output
	}
	return cutRuneHead(output, half) + marker + cutRuneTail(output, half)
}

// --- shared line-selection engine ---

// lineWindow is the line-oriented view of an oversized result being budgeted.
type lineWindow struct {
	lines    []string // original lines (split on "\n")
	original int      // byte size of the original output (for the marker)
	marker   string   // omission marker, reserved at its worst-case length
	compact  bool     // marker is the compact form (full marker did not fit)
}

// newLineWindow builds the line view for an oversized result, reserving the
// marker cost out of the cap at its WORST-CASE length (retained = total lines,
// so the digit count is maximal) — the real marker written later is never
// longer, which keeps the final replacement within limit. When even the
// worst-case full marker does not fit the cap, the compact marker is used.
func newLineWindow(output string, limit int) lineWindow {
	lines := strings.Split(output, "\n")
	full := fmt.Sprintf(semanticMarkerFmt, limit, len(output), len(lines), len(lines))
	if len(full) <= limit {
		return lineWindow{lines: lines, original: len(output), marker: full}
	}
	return lineWindow{lines: lines, original: len(output), marker: compactMarker, compact: true}
}

// budgetForLines returns the byte budget available for selected lines after
// the marker is reserved.
func (w lineWindow) budgetForLines(limit int) int {
	b := limit - len(w.marker)
	if b < 0 {
		b = 0
	}
	return b
}

// build joins the selected line indexes in original order, appends the marker
// (with the real retained/total line counts), and slices any over-budget line
// rune-safely. The result is guaranteed ≤ limit.
func (w lineWindow) build(limit int, selected []int) string {
	var kept []string
	remaining := w.budgetForLines(limit)
	for _, i := range selected {
		if i < 0 || i >= len(w.lines) {
			continue
		}
		line := w.lines[i]
		cost := len(line) + 1 // newline
		if cost <= remaining {
			kept = append(kept, line)
			remaining -= cost
			continue
		}
		// A single line too large for the remaining budget: keep a rune-safe
		// head/tail slice so no signal is lost entirely.
		if remaining > 0 {
			kept = append(kept, cutRuneHead(line, (remaining+1)/2)+cutRuneTail(line, remaining/2))
		}
		remaining = 0
		break
	}
	marker := w.marker
	if !w.compact {
		marker = fmt.Sprintf(semanticMarkerFmt, limit, w.original, len(kept), len(w.lines))
	}
	body := strings.Join(kept, "\n")
	if body == "" {
		return marker
	}
	return body + marker
}

// cutRuneHead keeps the first maxBytes bytes of s, backing off to a rune
// boundary so the result is always valid UTF-8.
func cutRuneHead(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	end := maxBytes
	for end > 0 && s[end]&0xC0 == 0x80 {
		end--
	}
	return s[:end]
}

// cutRuneTail keeps the last maxBytes bytes of s, advancing to a rune
// boundary so the result is always valid UTF-8.
func cutRuneTail(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	start := len(s) - maxBytes
	for start < len(s) && s[start]&0xC0 == 0x80 {
		start++
	}
	return s[start:]
}
