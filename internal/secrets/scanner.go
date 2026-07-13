// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

// Package secrets provides pattern-based detection and redaction of
// sensitive material that might appear in tool outputs before they are sent
// to an LLM.
package secrets

import (
	"regexp"
	"sort"
	"sync"
)

// Match represents a detected secret occurrence in a string.
type Match struct {
	// SecretType is the category name (e.g. "aws_secret_key").
	SecretType string
	// Value is the detected secret string.
	Value string
	// Start is the byte offset of the match.
	Start int
	// End is the byte offset immediately after the match.
	End int
}

// Pattern defines a single secret detector. The secret value is taken from
// the Regexp submatch at index Group (default 0, the full match). When
// Group is non-zero the Start/End indices are adjusted to the submatch.
type Pattern struct {
	Name   string
	Regexp *regexp.Regexp
	Group  int
	// MinLen, when positive, filters out matches shorter than the threshold.
	MinLen int
}

// Default patterns. These are intentionally conservative: false positives are
// preferred over leaking credentials into the model context.
//
// Boundaries use explicit non-alphanumeric separators (including start/end of
// string) instead of \b because Go's \b treats underscore as a word
// character, which prevents matching secrets that follow underscores or other
// common separators.
var (
	awsAccessKeyID = Pattern{
		Name:   "aws_access_key_id",
		Regexp: regexp.MustCompile(`(?:^|[^A-Za-z0-9])(AKIA[0-9A-Z]{16})(?:$|[^A-Za-z0-9])`),
		Group:  1,
	}
	awsSecretAccessKey = Pattern{
		Name:   "aws_secret_access_key",
		Regexp: regexp.MustCompile(`(?i)(?:aws_secret_access_key|secret_access_key|aws_secret)\s*[:=]\s*["']?([A-Za-z0-9/+=]{40})["']?`),
		Group:  1,
		MinLen: 40,
	}
	githubToken = Pattern{
		Name:   "github_token",
		Regexp: regexp.MustCompile(`(?:^|[^A-Za-z0-9])((ghp|gho|ghu|ghs|ghr|github_pat)_[A-Za-z0-9_]{36,})(?:$|[^A-Za-z0-9])`),
		Group:  1,
		MinLen: 40,
	}
	openAIKey = Pattern{
		Name:   "openai_api_key",
		Regexp: regexp.MustCompile(`(?:^|[^A-Za-z0-9])(sk-[A-Za-z0-9]{48})(?:$|[^A-Za-z0-9])`),
		Group:  1,
		MinLen: 50,
	}
	slackToken = Pattern{
		Name:   "slack_token",
		Regexp: regexp.MustCompile(`(?:^|[^A-Za-z0-9])(xox[baprs]-[0-9]{10,13}-[0-9]{10,13}(-[A-Za-z0-9]{24})?)(?:$|[^A-Za-z0-9])`),
		Group:  1,
	}
	googleAPIKey = Pattern{
		Name:   "google_api_key",
		Regexp: regexp.MustCompile(`(?:^|[^A-Za-z0-9])(AIza[0-9A-Za-z_-]{35})(?:$|[^A-Za-z0-9])`),
		Group:  1,
		MinLen: 38,
	}
	privateKey = Pattern{
		Name:   "private_key",
		Regexp: regexp.MustCompile(`-----BEGIN (RSA |EC |DSA |OPENSSH )?PRIVATE KEY-----[\s\S]*?-----END (RSA |EC |DSA |OPENSSH )?PRIVATE KEY-----`),
	}
	jwt = Pattern{
		Name:   "jwt",
		Regexp: regexp.MustCompile(`(?:^|[^A-Za-z0-9])(eyJ[A-Za-z0-9_-]*\.eyJ[A-Za-z0-9_-]*\.[A-Za-z0-9_-]*)(?:$|[^A-Za-z0-9])`),
		Group:  1,
	}
	genericSecret = Pattern{
		Name:   "generic_secret",
		Regexp: regexp.MustCompile(`(?:^|[^A-Za-z0-9])(api[_-]?key|apikey|token|secret|password|passwd)\s*[:=]\s*["']?([A-Za-z0-9/+=!@#$%^&*]{16,})["']?(?:$|[^A-Za-z0-9])`),
		Group:  2,
		MinLen: 16,
	}
)

// DefaultPatterns is the list of patterns used by DefaultScanner.
func DefaultPatterns() []Pattern {
	return []Pattern{
		awsAccessKeyID,
		awsSecretAccessKey,
		githubToken,
		openAIKey,
		slackToken,
		googleAPIKey,
		privateKey,
		jwt,
		genericSecret,
	}
}

// Scanner scans text for secrets using a set of patterns.
type Scanner struct {
	patterns []Pattern
	mu       sync.RWMutex // protects patterns
}

// NewScanner creates a scanner with the given patterns. If patterns is nil,
// DefaultPatterns() is used.
func NewScanner(patterns []Pattern) *Scanner {
	if patterns == nil {
		patterns = DefaultPatterns()
	}
	return &Scanner{patterns: patterns}
}

// DefaultScanner returns a scanner configured with the built-in patterns.
func DefaultScanner() *Scanner {
	return NewScanner(DefaultPatterns())
}

// AddPattern appends a custom pattern to the scanner.
func (s *Scanner) AddPattern(p Pattern) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.patterns = append(s.patterns, p)
}

// Scan returns all secret matches found in text. Matches are sorted by Start
// and do not overlap.
func (s *Scanner) Scan(text string) []Match {
	s.mu.RLock()
	patterns := make([]Pattern, len(s.patterns))
	copy(patterns, s.patterns)
	s.mu.RUnlock()

	var matches []Match
	for _, p := range patterns {
		all := p.Regexp.FindAllStringSubmatchIndex(text, -1)
		for _, loc := range all {
			g := p.Group * 2
			if g+1 >= len(loc) {
				g = 0
			}
			if loc[g] < 0 || loc[g+1] < 0 {
				continue
			}
			value := text[loc[g]:loc[g+1]]
			if p.MinLen > 0 && len(value) < p.MinLen {
				continue
			}
			matches = append(matches, Match{
				SecretType: p.Name,
				Value:      value,
				Start:      loc[g],
				End:        loc[g+1],
			})
		}
	}
	return dedupeAndSort(matches)
}

// HasSecrets reports whether any secrets were detected in text.
func (s *Scanner) HasSecrets(text string) bool {
	return len(s.Scan(text)) > 0
}

// dedupeAndSort removes overlapping matches, keeping the longer match when
// two patterns overlap. The result is sorted by Start.
func dedupeAndSort(matches []Match) []Match {
	if len(matches) <= 1 {
		return matches
	}
	sortMatches(matches)
	return mergeOverlapping(matches)
}

func sortMatches(matches []Match) {
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].Start != matches[j].Start {
			return matches[i].Start < matches[j].Start
		}
		return matches[i].End-matches[i].Start > matches[j].End-matches[j].Start
	})
}

func mergeOverlapping(matches []Match) []Match {
	out := []Match{matches[0]}
	for _, m := range matches[1:] {
		last := &out[len(out)-1]
		if !overlaps(m, *last) {
			out = append(out, m)
			continue
		}
		if longer(m, *last) {
			*last = m
		}
	}
	return out
}

func overlaps(a, b Match) bool { return a.Start < b.End }
func longer(a, b Match) bool   { return a.End-a.Start > b.End-b.Start }
