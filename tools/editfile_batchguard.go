// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"fmt"
	"strings"

	"github.com/pijalu/goa/internal"
)

// checkBatchLineDrift rejects a batch in which an edit that addresses the file
// by ABSOLUTE line number follows an edit that may change the line count
// (Issue 2: "delete_lines(2) + replace_lines(3)" silently overwrote the wrong
// line).
//
// Content anchors re-resolve against the in-memory content before every edit,
// but line numbers are harvested by the model before the call and cannot be
// re-anchored: once an earlier entry inserts or deletes a line, every later
// start_line/end_line points one position off (usually a silent, plausible
// wrong-line edit). The batch is therefore rejected up-front — nothing has been
// written at that point, so the file stays byte-identical — and the hint tells
// the caller to split the batch in two.
//
// The check is pure: it inspects only the parsed edits, never the file.
func checkBatchLineDrift(edits []editFileParams) error {
	shift := firstShiftingIndex(edits)
	if shift < 0 {
		return nil
	}
	// Only entries AFTER the first line-shifting entry can have stale absolute
	// coordinates: the entries before it are line-count stable, and the shifting
	// entry itself is applied against coordinates that are still valid.
	for i := shift + 1; i < len(edits); i++ {
		if usesAbsoluteLines(edits[i]) {
			return staleLineNumbersError(edits, i, shift)
		}
	}
	return nil
}

// firstShiftingIndex returns the index of the earliest entry whose line count
// may change, or -1 when no entry can shift lines.
func firstShiftingIndex(edits []editFileParams) int {
	for i := range edits {
		if mayShiftLines(edits[i]) {
			return i
		}
	}
	return -1
}

// usesAbsoluteLines reports whether the edit addresses the file by absolute
// line number — the coordinates that go stale when an earlier entry changes
// the line count. replace_lines and delete_lines always do; insert_after and
// insert_before do when anchored by start_line (a pattern-addressed insert
// re-resolves against the current content, so it stays valid).
func usesAbsoluteLines(e editFileParams) bool {
	switch effectiveOperation(e) {
	case OpReplaceLines, OpDeleteLines:
		return true
	case OpInsertAfter, OpInsertBefore:
		return e.StartLine > 0
	}
	return false
}

// mayShiftLines reports whether the edit can change the NUMBER of lines in the
// file. It is deliberately conservative: a false positive only makes the
// caller split an otherwise-working batch, while a false negative restores the
// silent corruption this guard exists to prevent. Edits that cannot apply at
// all (invalid range, missing content) report false so their own, more precise
// error is the one the caller sees.
func mayShiftLines(e editFileParams) bool {
	switch effectiveOperation(e) {
	case OpInsertAfter, OpInsertBefore, OpDeleteLines:
		return true
	case OpReplaceLines:
		return replaceLinesShifts(e)
	case OpReplacePattern:
		return replacePatternShifts(e)
	case OpReplace:
		return replaceShifts(e)
	}
	return false
}

// replaceLinesShifts reports whether a replace_lines entry changes the line
// count: it does whenever the target range is open-ended (end_line <= 0 means
// "to end of file") or the replacement does not occupy exactly the range it
// replaces.
func replaceLinesShifts(e editFileParams) bool {
	if e.EndLine <= 0 {
		return true
	}
	if e.StartLine < 1 || e.EndLine < e.StartLine {
		return false // invalid range: replace_lines reports invalid_range itself
	}
	replacement, ok := batchReplacementLines(e)
	if !ok {
		return false // missing content: the op reports missing_parameter itself
	}
	return len(replacement) != e.EndLine-e.StartLine+1
}

// replaceShifts reports whether a classic search/replace changes the line
// count. The counts mirror fuzzyEdit, which trims one trailing newline from
// each side (a trailing newline delimits the block, it does not add a line)
// before splitting on "\n".
func replaceShifts(e editFileParams) bool {
	return trimmedLineCount(e.OldString) != trimmedLineCount(e.NewString)
}

// replacePatternShifts reports whether a replace_pattern entry can change the
// line count. A pattern containing a newline (literal or the `\n` escape) is
// matched as a fuzzy block against the whole file, and a replacement that
// spans lines splices extra lines in — both shift. A single-line pattern whose
// replacement is also single-line rewrites text inside one line and cannot
// change the count.
func replacePatternShifts(e editFileParams) bool {
	return patternSpansLines(e.Pattern) ||
		patternSpansLines(e.NewContent) ||
		patternSpansLines(e.NewString)
}

// patternSpansLines reports whether s can introduce or match across a line
// break, either as a literal newline or as the two-character `\n` escape the
// regex engine reads as one.
func patternSpansLines(s string) bool {
	return strings.Contains(s, "\n") || strings.Contains(s, `\n`)
}

// batchReplacementLines resolves the replacement lines of a line/pattern entry
// the way resolveOpContent does, minus its error paths: new_string is the
// documented fallback when new_content is empty, and an explicitly empty
// new_content is a deliberate single empty line (Issue 5). ok is false when
// neither field carries content, i.e. when the op itself fails.
func batchReplacementLines(e editFileParams) (lines []string, ok bool) {
	if e.NewContent != "" {
		return splitLines(e.NewContent), true
	}
	if e.NewString != "" {
		return splitLines(e.NewString), true
	}
	if e.NewContentSet {
		return []string{""}, true
	}
	return nil, false
}

// trimmedLineCount counts the lines a fuzzyEdit operand occupies after the
// single trailing newline it ignores has been trimmed, CRLF-normalized like
// the matcher does.
func trimmedLineCount(s string) int {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.TrimSuffix(s, "\n")
	return strings.Count(s, "\n") + 1
}

// effectiveOperation mirrors applySingleEdit's routing: an entry without an
// explicit operation but with old_string is the classic search/replace.
func effectiveOperation(e editFileParams) EditOperation {
	if e.Operation == "" && e.OldString != "" {
		return OpReplace
	}
	return EditOperation(e.Operation)
}

// staleLineNumbersError is the actionable rejection for a drifting batch: it
// names both the offending entry and the earlier entry that invalidated its
// coordinates, states that the file was left untouched, and prescribes the
// split-in-two workflow.
func staleLineNumbersError(edits []editFileParams, at, shift int) error {
	return &internal.ToolError{
		Tool: "edit",
		Type: "stale_line_numbers",
		Detail: fmt.Sprintf(
			"edit %d/%d (%s) addresses lines by absolute number, but edit %d (%s) earlier in the batch changes the file's line count — the harvested line numbers would target stale positions; no changes were written",
			at+1, len(edits), effectiveOperation(edits[at]),
			shift+1, effectiveOperation(edits[shift]),
		),
		HintText: "Split the batch: apply the line-shifting edits in one call, re-harvest line numbers (grep -n / read), then apply the line-addressed edits in a second call. Content ops (replace) and pattern-addressed inserts re-anchor automatically and can stay in either call.",
	}
}
