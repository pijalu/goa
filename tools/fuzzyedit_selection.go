// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"errors"
	"fmt"
	"strings"
)

// ErrOccurrenceNotFound reports an occurrence selector beyond the available
// matches. The concrete error retains both numbers for actionable diagnostics.
var ErrOccurrenceNotFound = errors.New("fuzzyedit: requested occurrence not found")

type editSelection struct {
	occurrence  int
	replaceAll  bool
	deleteMatch bool
}

type ambiguousMatchError struct{ count int }

func (e *ambiguousMatchError) Error() string {
	return fmt.Sprintf("%s: found %d possible matches for the given text", ErrAmbiguous, e.count)
}
func (e *ambiguousMatchError) Unwrap() error { return ErrAmbiguous }

type occurrenceMatchError struct {
	count      int
	occurrence int
}

func (e *occurrenceMatchError) Error() string {
	return fmt.Sprintf("%s: matched %d location(s), occurrence %d requested", ErrOccurrenceNotFound, e.count, e.occurrence)
}
func (e *occurrenceMatchError) Unwrap() error { return ErrOccurrenceNotFound }

type fuzzyStrategy struct {
	matchType MatchType
	normalize func(string) string
	reindent  bool
}

// fuzzyEditSelected applies the normal fuzzy matching tiers with explicit
// occurrence/all-match selection. Matches are collected from the original
// content and spliced once, so replacement text is never matched recursively.
func fuzzyEditSelected(file, oldStr, newStr string, allowFuzz bool, selection editSelection) (*EditResult, error) {
	if oldStr == "" {
		return nil, ErrEmptyOldStr
	}
	useCRLF := strings.Contains(file, "\r\n")
	normFile := strings.ReplaceAll(file, "\r\n", "\n")
	normOld := strings.TrimSuffix(strings.ReplaceAll(oldStr, "\r\n", "\n"), "\n")
	normNew := strings.TrimSuffix(strings.ReplaceAll(newStr, "\r\n", "\n"), "\n")
	fileLines := strings.Split(normFile, "\n")
	oldLines := strings.Split(normOld, "\n")
	newLines := strings.Split(normNew, "\n")

	if selection.deleteMatch && normNew == "" {
		// Explicit string deletion removes whole-line matches including their
		// line. Other operations retain empty replacement as one empty line.
		newLines = nil
	}
	for _, strategy := range fuzzyStrategies(allowFuzz) {
		matches := nonOverlappingLineMatches(findMatches(fileLines, oldLines, strategy.normalize), len(oldLines))
		if len(matches) == 0 {
			continue
		}
		selected, err := selectMatchIndexes(matches, selection)
		if err != nil {
			return nil, err
		}
		replacements := lineReplacements(fileLines, oldLines, newLines, selected, strategy.reindent)
		return buildSelectedLineResult(fileLines, selected, len(oldLines), replacements, strategy.matchType, useCRLF)
	}
	if len(oldLines) == 1 {
		return substringEditSelected(fileLines, normFile, normOld, normNew, useCRLF, selection)
	}
	return nil, ErrNotFound
}

func fuzzyStrategies(allowFuzz bool) []fuzzyStrategy {
	strategies := []fuzzyStrategy{{matchType: MatchExact, normalize: exactNormalize}}
	if allowFuzz {
		strategies = append(strategies,
			fuzzyStrategy{matchType: MatchTrailingWhitespace, normalize: trailingWhitespaceNormalize},
			fuzzyStrategy{matchType: MatchFuzzy, normalize: fuzzyNormalize, reindent: true},
		)
	}
	return strategies
}

// nonOverlappingLineMatches selects leftmost matches and skips any candidate
// covered by an accepted match. This gives occurrence and replace_all the same
// deterministic, non-recursive coordinates.
func nonOverlappingLineMatches(matches []int, width int) []int {
	selected := make([]int, 0, len(matches))
	end := 0
	for _, start := range matches {
		if len(selected) > 0 && start < end {
			continue
		}
		selected = append(selected, start)
		end = start + width
	}
	return selected
}

func selectMatchIndexes(matches []int, selection editSelection) ([]int, error) {
	if selection.replaceAll {
		return matches, nil
	}
	if selection.occurrence > 0 {
		if selection.occurrence > len(matches) {
			return nil, &occurrenceMatchError{count: len(matches), occurrence: selection.occurrence}
		}
		return matches[selection.occurrence-1 : selection.occurrence], nil
	}
	if len(matches) > 1 {
		return nil, &ambiguousMatchError{count: len(matches)}
	}
	return matches, nil
}

func lineReplacements(fileLines, oldLines, newLines []string, selected []int, reindent bool) [][]string {
	replacements := make([][]string, len(selected))
	for i, start := range selected {
		replacements[i] = newLines
		if reindent {
			replacements[i] = reindentReplacement(fileLines, oldLines, newLines, start)
		}
	}
	return replacements
}

func buildSelectedLineResult(fileLines []string, selected []int, width int, replacements [][]string, matchType MatchType, useCRLF bool) (*EditResult, error) {
	if len(selected) == 1 {
		start := selected[0]
		if linesEqual(fileLines[start:start+width], replacements[0]) {
			return nil, ErrNoChange
		}
		result := buildEditResult(fileLines, start, start+width, replacements[0], matchType, useCRLF)
		result.MatchCount = 1
		return result, nil
	}
	newLines := make([]string, 0, len(fileLines))
	cursor := 0
	changed := false
	for i, start := range selected {
		newLines = append(newLines, fileLines[cursor:start]...)
		if !linesEqual(fileLines[start:start+width], replacements[i]) {
			changed = true
		}
		newLines = append(newLines, replacements[i]...)
		cursor = start + width
	}
	newLines = append(newLines, fileLines[cursor:]...)
	if !changed {
		return nil, ErrNoChange
	}
	return buildMultiMatchResult(fileLines, newLines, selected[0]+1, selected[len(selected)-1]+width, matchType, len(selected), useCRLF), nil
}

func substringEditSelected(fileLines []string, normFile, normOld, normNew string, useCRLF bool, selection editSelection) (*EditResult, error) {
	if normOld == "" {
		return nil, ErrNotFound
	}
	matches := substringMatchOffsets(normFile, normOld)
	selected, err := selectMatchIndexes(matches, selection)
	if err != nil {
		return nil, err
	}
	if len(selected) == 0 {
		return nil, ErrNotFound
	}
	if normOld == normNew {
		return nil, ErrNoChange
	}
	replaced := spliceSubstringMatches(normFile, normOld, normNew, selected)
	newFileLines := strings.Split(replaced, "\n")
	start := strings.Count(normFile[:selected[0]], "\n")
	end := strings.Count(normFile[:selected[len(selected)-1]], "\n") + 1
	if len(selected) == 1 {
		replLines := newFileLines[start : len(newFileLines)-(len(fileLines)-start-1)]
		result := buildEditResult(fileLines, start, start+1, replLines, MatchExactSubstring, useCRLF)
		result.MatchCount = 1
		return result, nil
	}
	return buildMultiMatchResult(fileLines, newFileLines, start+1, end, MatchExactSubstring, len(selected), useCRLF), nil
}

func substringMatchOffsets(content, oldText string) []int {
	var matches []int
	for offset := 0; offset <= len(content)-len(oldText); {
		relative := strings.Index(content[offset:], oldText)
		if relative < 0 {
			break
		}
		match := offset + relative
		matches = append(matches, match)
		offset = match + len(oldText)
	}
	return matches
}

func spliceSubstringMatches(content, oldText, newText string, selected []int) string {
	var b strings.Builder
	b.Grow(len(content))
	cursor := 0
	for _, match := range selected {
		b.WriteString(content[cursor:match])
		b.WriteString(newText)
		cursor = match + len(oldText)
	}
	b.WriteString(content[cursor:])
	return b.String()
}

func buildMultiMatchResult(oldLines, newLines []string, startLine, endLine int, matchType MatchType, count int, useCRLF bool) *EditResult {
	content := strings.Join(newLines, "\n")
	diff := generateUnifiedDiff(oldLines, newLines)
	if useCRLF {
		content = strings.ReplaceAll(content, "\n", "\r\n")
		diff = strings.ReplaceAll(diff, "\n", "\r\n")
	}
	return &EditResult{NewContent: content, Diff: diff, MatchType: matchType, MatchCount: count, StartLine: startLine, EndLine: endLine}
}
