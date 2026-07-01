// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"errors"
	"fmt"
	"strings"
)

// MatchType describes which matching strategy successfully located the text
// to replace.
type MatchType string

const (
	// MatchExact means the old text was found as a byte-for-byte substring
	// (after normalizing line endings).
	MatchExact MatchType = "exact"

	// MatchTrailingWhitespace means the old text was found after trimming
	// trailing spaces/tabs from the end of every line being compared.
	MatchTrailingWhitespace MatchType = "trailing_whitespace_normalized"

	// MatchFuzzy means the old text was found after trimming leading and
	// trailing whitespace and collapsing internal whitespace runs to a
	// single space on every line being compared. The replacement text was
	// automatically re-indented to match the file.
	MatchFuzzy MatchType = "fuzzy_whitespace_and_indentation"
)

// EditResult is returned by fuzzyEdit on success.
type EditResult struct {
	// NewContent is the full file content after the edit has been applied.
	NewContent string

	// Diff is a unified-diff-style ("@@ ... @@", "-", "+", " ") rendering of
	// the change, including a few lines of surrounding context.
	Diff string

	// MatchType reports which strategy located the text to replace.
	MatchType MatchType

	// StartLine and EndLine are the 1-indexed, inclusive line numbers of the
	// region that was replaced in the *original* file content.
	StartLine int
	EndLine   int
}

var (
	// ErrEmptyOldStr is returned when the text to replace is empty.
	ErrEmptyOldStr = errors.New("fuzzyedit: old string must not be empty")

	// ErrNotFound is returned when the text to replace cannot be located in
	// the file, even with fuzzy matching.
	ErrNotFound = errors.New("fuzzyedit: old string not found in file")

	// ErrAmbiguous is returned when the text to replace matches more than
	// one location in the file. The caller should provide more surrounding
	// context to make the match unique.
	ErrAmbiguous = errors.New("fuzzyedit: old string matches multiple locations in file")

	// ErrNoChange is returned when the located text and the replacement text
	// are identical, i.e. applying the edit would not change the file.
	ErrNoChange = errors.New("fuzzyedit: old and new string are equivalent, edit would be a no-op")
)

// diffContextLines is the number of unchanged lines shown before and after
// the changed region in the generated diff.
const diffContextLines = 3

// fuzzyEdit replaces the first (and required-to-be-unique) occurrence of oldStr in
// file with newStr, using fuzzy matching as described in the package docs if
// an exact match cannot be found.
//
// When allowFuzz is false, only exact matching (after CRLF normalization) is
// attempted. When true, the full 3-tier strategy is used.
//
// A single trailing newline on oldStr/newStr is ignored, so callers do not
// need to worry about whether their replacement text should end with a
// newline character.
func fuzzyEdit(file, oldStr, newStr string, allowFuzz bool) (*EditResult, error) {
	if oldStr == "" {
		return nil, ErrEmptyOldStr
	}

	// Normalize CRLF -> LF for comparison purposes; we restore CRLF in the
	// output at the end if the original file used it.
	useCRLF := strings.Contains(file, "\r\n")

	normFile := strings.ReplaceAll(file, "\r\n", "\n")
	normOld := strings.ReplaceAll(oldStr, "\r\n", "\n")
	normNew := strings.ReplaceAll(newStr, "\r\n", "\n")

	// A single trailing newline is treated as a line-block delimiter, not as
	// a request to match/insert an extra empty line.
	normOld = strings.TrimSuffix(normOld, "\n")
	normNew = strings.TrimSuffix(normNew, "\n")

	fileLines := strings.Split(normFile, "\n")
	oldLines := strings.Split(normOld, "\n")
	newLines := strings.Split(normNew, "\n")

	// Build strategy list based on allowFuzz setting
	type strategy struct {
		matchType MatchType
		normalize func(string) string
		reindent  bool
	}
	strategies := []strategy{
		{MatchExact, exactNormalize, false},
	}
	if allowFuzz {
		strategies = append(strategies,
			strategy{MatchTrailingWhitespace, trailingWhitespaceNormalize, false},
			strategy{MatchFuzzy, fuzzyNormalize, true},
		)
	}

	for _, strat := range strategies {
		matches := findMatches(fileLines, oldLines, strat.normalize)
		switch len(matches) {
		case 0:
			continue // try the next, fuzzier strategy
		case 1:
			start := matches[0]
			end := start + len(oldLines)

			replacement := newLines
			if strat.reindent {
				replacement = reindentReplacement(fileLines, oldLines, newLines, start)
			}

			if linesEqual(fileLines[start:end], replacement) {
				return nil, ErrNoChange
			}

			return buildEditResult(fileLines, start, end, replacement, strat.matchType, useCRLF), nil
		default:
			return nil, fmt.Errorf("%w: found %d possible matches for the given text", ErrAmbiguous, len(matches))
		}
	}

	return nil, ErrNotFound
}

// exactNormalize is the identity function: used for the exact-match
// strategy.
func exactNormalize(s string) string {
	return s
}

// trailingWhitespaceNormalize trims trailing spaces and tabs from a line.
func trailingWhitespaceNormalize(s string) string {
	return strings.TrimRight(s, " \t")
}

// fuzzyNormalize trims leading/trailing whitespace and collapses any internal
// run of whitespace to a single space.
func fuzzyNormalize(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// findMatches returns the starting line indices of every position in
// fileLines where, for every k in [0, len(oldLines)),
// normalize(fileLines[i+k]) == normalize(oldLines[k]).
func findMatches(fileLines, oldLines []string, normalize func(string) string) []int {
	var matches []int

	n, m := len(fileLines), len(oldLines)
	if m == 0 || m > n {
		return matches
	}

	normOld := make([]string, m)
	for i, l := range oldLines {
		normOld[i] = normalize(l)
	}

	for i := 0; i+m <= n; i++ {
		match := true
		for k := 0; k < m; k++ {
			if normalize(fileLines[i+k]) != normOld[k] {
				match = false
				break
			}
		}
		if match {
			matches = append(matches, i)
		}
	}

	return matches
}

// leadingWhitespace returns the leading run of spaces/tabs of s.
func leadingWhitespace(s string) string {
	i := 0
	for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	return s[:i]
}

// reindentReplacement re-indents newLines so that the indentation "shift"
// between the matched block in the file and the requested old text is
// applied uniformly to every non-blank line of the replacement.
//
// The reference line used to compute the shift is the first non-blank line
// of oldLines (falling back to the first line if every line is blank).
func reindentReplacement(fileLines, oldLines, newLines []string, start int) []string {
	refIdx := 0
	for refIdx < len(oldLines)-1 && strings.TrimSpace(oldLines[refIdx]) == "" {
		refIdx++
	}

	fileIndent := leadingWhitespace(fileLines[start+refIdx])
	oldIndent := leadingWhitespace(oldLines[refIdx])

	delta := len(fileIndent) - len(oldIndent)
	if delta == 0 {
		return newLines
	}

	indentChar := byte(' ')
	switch {
	case len(fileIndent) > 0:
		indentChar = fileIndent[0]
	case len(oldIndent) > 0:
		indentChar = oldIndent[0]
	}

	out := make([]string, len(newLines))
	for i, line := range newLines {
		if strings.TrimSpace(line) == "" {
			out[i] = line
			continue
		}

		ind := leadingWhitespace(line)
		newLen := len(ind) + delta
		if newLen < 0 {
			newLen = 0
		}

		out[i] = strings.Repeat(string(indentChar), newLen) + line[len(ind):]
	}

	return out
}

// linesEqual reports whether a and b contain the same lines in the same
// order.
func linesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// buildEditResult assembles the new file content and the diff for a replacement
// of fileLines[start:end] with replacement.
func buildEditResult(fileLines []string, start, end int, replacement []string, matchType MatchType, useCRLF bool) *EditResult {
	newFileLines := make([]string, 0, len(fileLines)-(end-start)+len(replacement))
	newFileLines = append(newFileLines, fileLines[:start]...)
	newFileLines = append(newFileLines, replacement...)
	newFileLines = append(newFileLines, fileLines[end:]...)

	newContent := strings.Join(newFileLines, "\n")
	diff := generateDiff(fileLines, start, end, replacement)

	if useCRLF {
		newContent = strings.ReplaceAll(newContent, "\n", "\r\n")
		diff = strings.ReplaceAll(diff, "\n", "\r\n")
	}

	return &EditResult{
		NewContent: newContent,
		Diff:       diff,
		MatchType:  matchType,
		StartLine:  start + 1,
		EndLine:    end,
	}
}

// generateDiff renders a unified-diff-style hunk describing the replacement
// of fileLines[start:end] with replacement, including up to
// diffContextLines lines of unchanged context before and after.
func generateDiff(fileLines []string, start, end int, replacement []string) string {
	ctxStart := start - diffContextLines
	if ctxStart < 0 {
		ctxStart = 0
	}
	ctxEnd := end + diffContextLines
	if ctxEnd > len(fileLines) {
		ctxEnd = len(fileLines)
	}

	leadingCtx := start - ctxStart
	trailingCtx := ctxEnd - end

	oldCount := (end - start) + leadingCtx + trailingCtx
	newCount := len(replacement) + leadingCtx + trailingCtx

	var b strings.Builder
	fmt.Fprintf(&b, "@@ -%d,%d +%d,%d @@\n", ctxStart+1, oldCount, ctxStart+1, newCount)

	for i := ctxStart; i < start; i++ {
		fmt.Fprintf(&b, " %s\n", fileLines[i])
	}
	for i := start; i < end; i++ {
		fmt.Fprintf(&b, "-%s\n", fileLines[i])
	}
	for _, l := range replacement {
		fmt.Fprintf(&b, "+%s\n", l)
	}
	for i := end; i < ctxEnd; i++ {
		fmt.Fprintf(&b, " %s\n", fileLines[i])
	}

	return strings.TrimSuffix(b.String(), "\n")
}
