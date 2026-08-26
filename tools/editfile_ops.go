// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/pijalu/goa/internal"
)

func (t *EditFileTool) runOp(lines []string, op EditOperation, p editParams) ([]string, int, error) {
	switch op {
	case OpReplace:
		return nil, 0, &internal.ToolError{Tool: "edit", Type: "missing_parameter",
			Detail:   "operation 'replace' requires 'old_string' and 'new_string'",
			HintText: "Provide the text to search for in 'old_string' and the replacement in 'new_string'."}
	case OpReplaceLines:
		return t.replaceLines(lines, p.startLine, p.endLine, p.newLines, p.indentMode)
	case OpReplacePattern:
		return t.replacePattern(lines, p.pattern, p.patternFlags, p.occurrence, p.newLines, p.indentMode)
	case OpInsertAfter:
		return t.insertAfter(lines, p.startLine, p.pattern, p.newLines, p.indentMode)
	case OpInsertBefore:
		return t.insertBefore(lines, p.startLine, p.pattern, p.newLines, p.indentMode)
	case OpDeleteLines:
		return t.deleteLines(lines, p.startLine, p.endLine)
	default:
		return nil, 0, &internal.ToolError{Tool: "edit", Type: "unknown_operation",
			Detail:   fmt.Sprintf("Unknown operation: %s", op),
			HintText: "Use one of: replace, replace_lines, replace_pattern, insert_after, insert_before, delete_lines"}
	}
}

func (t *EditFileTool) replaceLines(lines []string, startLine, endLine int, newLines []string, indentMode IndentMode) ([]string, int, error) {
	start, end, err := t.checkLineRange(lines, startLine, endLine)
	if err != nil {
		return nil, 0, err
	}
	targetLines := lines[start-1 : end]
	adjusted := t.adjustIndent(targetLines, newLines, indentMode)
	result := make([]string, 0, len(lines)-len(targetLines)+len(adjusted))
	result = append(result, lines[:start-1]...)
	result = append(result, adjusted...)
	result = append(result, lines[end:]...)
	// Return the removed line count: the result message pairs it with the
	// inserted count ("replaced N lines with M") so a content-less edit is
	// immediately visible instead of a misleading "0 lines affected".
	return result, len(targetLines), nil
}

func (t *EditFileTool) replacePattern(lines []string, pattern, flags string, occurrence int, newLines []string, indentMode IndentMode) ([]string, int, error) {
	if pattern == "" {
		return nil, 0, &internal.ToolError{Tool: "edit", Type: "missing_pattern",
			Detail: "Pattern is required for replace_pattern", HintText: "Provide a 'pattern' to search for."}
	}
	if occurrence <= 0 {
		occurrence = 1
	}

	// Use the pattern verbatim. JSON unmarshalling already resolved any escape
	// sequence the model intended (a real newline for "\n", a literal backslash+n
	// for "\\n"). Re-interpreting escapes here is unsafe: it silently corrupts
	// patterns that legitimately contain backslash escapes (Go/Python string
	// literals, regex metacharacters such as \n, \d, \s) and can wrongly reroute
	// a single-line regex into block matching.

	// A pattern that spans multiple lines (after JSON decoding) cannot be matched
	// line-by-line, so match it as a fuzzy block against the whole file.
	if strings.Contains(pattern, "\n") {
		return t.replacePatternBlock(lines, pattern, occurrence, newLines, indentMode)
	}

	caseSensitive := !strings.Contains(flags, "i")
	found := 0
	result := make([]string, 0, len(lines))
	for _, line := range lines {
		if matchLine(line, pattern, caseSensitive) {
			found++
			if found == occurrence {
				adjusted := t.adjustIndent([]string{line}, newLines, indentMode)
				result = append(result, adjusted...)
				continue
			}
		}
		result = append(result, line)
	}
	if found == 0 {
		return nil, 0, &internal.ToolError{Tool: "edit", Type: "pattern_not_found",
			Detail:   fmt.Sprintf("Pattern %q not found in file", pattern),
			HintText: "Use 'read' to verify the file content and check the pattern for typos or try different flags."}
	}
	return result, len(newLines), nil
}

// replacePatternBlock replaces a multi-line pattern against the whole file
// using fuzzy block matching. This catches the common model mistake of calling
// replace_pattern with a double-escaped literal block.
func (t *EditFileTool) replacePatternBlock(lines []string, pattern string, occurrence int, newLines []string, indentMode IndentMode) ([]string, int, error) {
	if occurrence > 1 {
		return nil, 0, &internal.ToolError{Tool: "edit", Type: "unsupported_occurrence",
			Detail:   "Multi-line replace_pattern only supports occurrence=1",
			HintText: "Use 'old_string'/'new_string' search/replace or 'replace_lines' for specific occurrences."}
	}
	fileText := strings.Join(lines, "\n")
	newText := strings.Join(newLines, "\n")
	result, err := fuzzyEdit(fileText, pattern, newText, true)
	if err != nil {
		return nil, 0, t.mapBlockPatternError(pattern, err)
	}
	return splitLines(result.NewContent), len(newLines), nil
}

func (t *EditFileTool) mapBlockPatternError(pattern string, err error) error {
	switch {
	case errors.Is(err, ErrAmbiguous):
		return &internal.ToolError{Tool: "edit", Type: "ambiguous_match",
			Detail:   fmt.Sprintf("Pattern %q matches multiple locations in file", truncateStr(pattern, 40)),
			HintText: "Add more surrounding context to the pattern so only one location matches, or use 'old_string'/'new_string' search/replace."}
	case errors.Is(err, ErrNotFound):
		return &internal.ToolError{Tool: "edit", Type: "pattern_not_found",
			Detail:   fmt.Sprintf("Pattern %q not found in file (tried exact, trailing whitespace, and fuzzy matching)", truncateStr(pattern, 40)),
			HintText: "Use 'read' to verify the current file content (the file may have changed since your last read)."}
	case errors.Is(err, ErrNoChange):
		return &internal.ToolError{Tool: "edit", Type: "no_change",
			Detail:   "Pattern and replacement are identical",
			HintText: "Provide different 'new_content' content."}
	case errors.Is(err, ErrEmptyOldStr):
		return &internal.ToolError{Tool: "edit", Type: "empty_pattern",
			Detail:   "Pattern must not be empty",
			HintText: "Provide a non-empty pattern to search for."}
	default:
		return &internal.ToolError{Tool: "edit", Type: "edit_error",
			Detail:   fmt.Sprintf("Edit failed: %v", err),
			HintText: "Check the file content with 'read' and try again."}
	}
}

func (t *EditFileTool) insertAfter(lines []string, lineNum int, pattern string, newLines []string, indentMode IndentMode) ([]string, int, error) {
	if lineNum > 0 {
		return t.insertAtLine(lines, lineNum, newLines, indentMode, false)
	}
	if pattern == "" {
		return nil, 0, &internal.ToolError{Tool: "edit", Type: "missing_parameter",
			Detail:   "Provide either start_line or pattern for insert_after",
			HintText: "Specify which line or pattern to insert after."}
	}
	return t.insertAtPattern(lines, pattern, newLines, indentMode, false)
}

func (t *EditFileTool) insertBefore(lines []string, lineNum int, pattern string, newLines []string, indentMode IndentMode) ([]string, int, error) {
	if lineNum > 0 {
		return t.insertAtLine(lines, lineNum, newLines, indentMode, true)
	}
	if pattern == "" {
		return nil, 0, &internal.ToolError{Tool: "edit", Type: "missing_parameter",
			Detail:   "Provide either start_line or pattern for insert_before",
			HintText: "Specify which line or pattern to insert before."}
	}
	return t.insertAtPattern(lines, pattern, newLines, indentMode, true)
}

func (t *EditFileTool) deleteLines(lines []string, startLine, endLine int) ([]string, int, error) {
	start, end, err := t.checkLineRange(lines, startLine, endLine)
	if err != nil {
		return nil, 0, err
	}
	result := make([]string, 0, len(lines)-(end-start+1))
	result = append(result, lines[:start-1]...)
	result = append(result, lines[end:]...)
	return result, end - start + 1, nil
}

// checkLineRange validates and normalizes a 1-indexed line range.
// Returns the normalized (start, end). endLine <= 0 means "to end of file".
// endLine > len(lines) is rejected as invalid_range (no silent clamp) so the
// caller never slices with an out-of-bounds value.
func (t *EditFileTool) checkLineRange(lines []string, startLine, endLine int) (int, int, error) {
	n := len(lines)
	if startLine < 1 || startLine > n {
		return 0, 0, &internal.ToolError{Tool: "edit", Type: "invalid_range",
			Detail:   fmt.Sprintf("start_line %d is out of range (file has %d lines)", startLine, n),
			HintText: "Use 'read' to verify the file length and provide a valid start_line."}
	}
	if endLine <= 0 {
		endLine = n
	}
	if endLine > n {
		return 0, 0, &internal.ToolError{Tool: "edit", Type: "invalid_range",
			Detail:   fmt.Sprintf("end_line %d is out of range (file has %d lines)", endLine, n),
			HintText: "Use 'read' to verify the file length and provide a valid end_line."}
	}
	if startLine > endLine {
		return 0, 0, &internal.ToolError{Tool: "edit", Type: "invalid_range",
			Detail:   fmt.Sprintf("start_line %d > end_line %d", startLine, endLine),
			HintText: "start_line must be <= end_line."}
	}
	return startLine, endLine, nil
}

func (t *EditFileTool) insertAtLine(lines []string, lineNum int, newLines []string, indentMode IndentMode, before bool) ([]string, int, error) {
	if lineNum < 1 || lineNum > len(lines) {
		return nil, 0, &internal.ToolError{Tool: "edit", Type: "invalid_line",
			Detail:   fmt.Sprintf("Line %d is out of range (file has %d lines)", lineNum, len(lines)),
			HintText: "Use 'read' to check the file content and provide a valid line number."}
	}
	idx := lineNum - 1
	target := []string{lines[idx]}
	adjusted := t.adjustIndent(target, newLines, indentMode)
	result := make([]string, 0, len(lines)+len(adjusted))
	if before {
		result = append(result, lines[:idx]...)
		result = append(result, adjusted...)
		result = append(result, lines[idx:]...)
	} else {
		result = append(result, lines[:idx+1]...)
		result = append(result, adjusted...)
		result = append(result, lines[idx+1:]...)
	}
	return result, len(adjusted), nil
}

func (t *EditFileTool) insertAtPattern(lines []string, pattern string, newLines []string, indentMode IndentMode, before bool) ([]string, int, error) {
	for i, line := range lines {
		if strings.Contains(line, pattern) {
			adjusted := t.adjustIndent([]string{line}, newLines, indentMode)
			result := make([]string, 0, len(lines)+len(adjusted))
			if before {
				result = append(result, lines[:i]...)
				result = append(result, adjusted...)
				result = append(result, lines[i:]...)
			} else {
				result = append(result, lines[:i+1]...)
				result = append(result, adjusted...)
				result = append(result, lines[i+1:]...)
			}
			return result, len(adjusted), nil
		}
	}
	return nil, 0, &internal.ToolError{Tool: "edit", Type: "pattern_not_found",
		Detail:   fmt.Sprintf("Pattern %q not found in file", pattern),
		HintText: "Use 'read' to verify the file content and check the pattern for typos."}
}

func matchLine(line, pattern string, caseSensitive bool) bool {
	// Try as regex first
	re, err := regexp.Compile(pattern)
	if err == nil {
		if caseSensitive {
			return re.MatchString(line)
		}
		return re.MatchString(strings.ToLower(line))
	}
	// Fall back to substring match
	check := line
	match := pattern
	if !caseSensitive {
		check = strings.ToLower(line)
		match = strings.ToLower(pattern)
	}
	return strings.Contains(check, match)
}

func (t *EditFileTool) adjustIndent(targetLines, newLines []string, mode IndentMode) []string {
	switch mode {
	case IndentPreserve:
		return adjustPreserve(targetLines, newLines)
	case IndentNormalize:
		return adjustNormalize(targetLines, newLines)
	default:
		return newLines
	}
}

func adjustPreserve(targetLines, newLines []string) []string {
	if len(targetLines) == 0 || len(newLines) == 0 {
		return newLines
	}
	targetIndent := len(leadingWS(targetLines[0]))
	sourceIndent := len(leadingWS(newLines[0]))
	delta := targetIndent - sourceIndent
	result := make([]string, len(newLines))
	for i, line := range newLines {
		if delta >= 0 {
			result[i] = strings.Repeat(" ", delta) + line
		} else if len(line) >= -delta {
			result[i] = line[-delta:]
		} else {
			result[i] = ""
		}
	}
	return result
}

func adjustNormalize(targetLines, newLines []string) []string {
	var indents []int
	for _, line := range targetLines {
		indents = append(indents, len(leadingWS(line)))
	}
	sort.Ints(indents)
	target := 0
	if len(indents) > 0 && indents[len(indents)/2] > 0 {
		target = indents[len(indents)/2]
		if target > 4 {
			target = 4
		}
	}
	result := make([]string, len(newLines))
	for i, line := range newLines {
		stripped := strings.TrimLeft(line, " \t")
		if stripped == "" {
			result[i] = ""
		} else {
			result[i] = strings.Repeat(" ", target) + stripped
		}
	}
	return result
}

func leadingWS(line string) string {
	for i, ch := range line {
		if ch != ' ' && ch != '\t' {
			return line[:i]
		}
	}
	return line
}
