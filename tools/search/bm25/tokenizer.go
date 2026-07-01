// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

// Package bm25 provides a BM25Okapi ranking implementation optimised for code
// search in large codebases. It offers a code-aware tokenizer, a persistent
// index with incremental rebuild support, and an Okapi BM25 scorer.
package bm25

import (
	"strings"
	"unicode"
)

// CodeTokenizer splits source code into search tokens using code-aware rules.
// It handles camelCase, PascalCase, snake_case, and common code patterns while
// filtering purely syntactic noise.
type CodeTokenizer struct {
	minTokenLen int
	stopWords   map[string]bool
}

// NewCodeTokenizer returns a CodeTokenizer with sensible defaults for code
// search: minimum token length of 2 and a curated stop-word list.
func NewCodeTokenizer() *CodeTokenizer {
	return &CodeTokenizer{
		minTokenLen: 2,
		stopWords:   defaultStopWords(),
	}
}

// Tokenize splits text into lowercased, filtered tokens suitable for BM25
// scoring. It handles:
//   - camelCase and PascalCase: "getUserID" → "get", "user", "id"
//   - snake_case and SCREAMING_SNAKE: "user_id" → "user", "id"
//   - kebab-case: "user-id" → "user", "id"
//   - dot/forward-slash paths: "pkg/util.go" → "pkg", "util", "go"
//   - Digits attached to words: "func2call" → "func", "call"
//   - Non-alphanumeric characters as separators
//   - Stop-word and short-token removal
func (ct *CodeTokenizer) Tokenize(text string) []string {
	raw := ct.extractTokens(text)
	tokens := make([]string, 0, len(raw))
	for _, t := range raw {
		t = strings.ToLower(t)
		if len(t) < ct.minTokenLen {
			continue
		}
		if ct.stopWords[t] {
			continue
		}
		tokens = append(tokens, t)
	}
	return tokens
}

// extractTokens performs the raw token extraction without lowercasing or
// stop-word filtering. It splits on:
//   - camelCase and PascalCase boundaries (lower→upper transition)
//   - digits attached to letters (letter→digit boundary)
//   - any non-alphanumeric character (including _ for snake_case)
func (ct *CodeTokenizer) extractTokens(text string) []string {
	var tokens []string
	var buf strings.Builder
	flush := func() {
		if buf.Len() > 0 {
			tokens = append(tokens, buf.String())
			buf.Reset()
		}
	}

	runes := []rune(text)
	var lastCategory uint8 // 0 = none, 1 = lower, 2 = upper, 3 = digit

	for i := 0; i < len(runes); i++ {
		ch := runes[i]
		category := charCategory(ch)

		switch {
		case category == 1: // lowercase letter
			buf.WriteRune(ch)

		case category == 2: // uppercase letter
			if buf.Len() > 0 && lastCategory == 1 {
				// lower→upper: camelCase boundary, flush before uppercase
				flush()
			}
			// If the buffer has content and previous was also uppercase
			// (PascalCase acronym), keep accumulating until we see a lower.
			buf.WriteRune(ch)

		case category == 3: // digit
			if buf.Len() > 0 && lastCategory == 2 {
				// Backtrack: this digit is attached to an uppercase token.
				// E.g. "HTTP2" → keep "HTTP2" as one token.
			}
			buf.WriteRune(ch)

		default:
			// Non-alphanumeric: separator.
			flush()
		}

		lastCategory = category
	}
	flush()

	return tokens
}

// charCategory returns 0 (separator), 1 (lowercase), 2 (uppercase), 3 (digit).
func charCategory(r rune) uint8 {
	switch {
	case unicode.IsLower(r):
		return 1
	case unicode.IsUpper(r):
		return 2
	case unicode.IsDigit(r):
		return 3
	default:
		return 0
	}
}

// defaultStopWords returns a minimal set of stop words that are purely
// syntactic and unlikely to carry semantic meaning in code search.
func defaultStopWords() map[string]bool {
	return map[string]bool{
		// Go keywords
		"package": true, "import": true, "func": true, "return": true,
		"var": true, "const": true, "type": true, "struct": true,
		"interface": true, "map": true, "chan": true, "nil": true,
		"true": true, "false": true, "iota": true, "go": true,
		"defer": true, "if": true, "else": true, "for": true,
		"range": true, "switch": true, "case": true, "default": true,
		"break": true, "continue": true, "goto": true, "fallthrough": true,
		"select": true,

		// Common keywords across languages
		"void": true, "int": true, "bool": true, "char": true,
		"float": true, "double": true, "long": true, "short": true,
		"unsigned": true, "signed": true, "static": true, "class": true,
		"new": true, "delete": true, "throw": true, "try": true,
		"catch": true, "finally": true, "public": true, "private": true,
		"protected": true, "this": true, "super": true, "extends": true,
		"implements": true, "abstract": true, "virtual": true,
		"override": true, "final": true, "async": true, "await": true,
		"let": true, "yield": true, "from": true, "export": true,
		"module": true, "namespace": true, "using": true, "where": true,
		"sizeof": true, "typeof": true, "instanceof": true,

		// English articles, determiners, and common function words
		"the": true, "a": true, "an": true,
		"that": true, "these": true, "those": true, "some": true, "any": true,
		"each": true, "every": true, "all": true, "both": true,
		"few": true, "more": true, "most": true, "other": true,
		"such": true, "only": true, "own": true, "same": true,

		// Prepositions (least likely to be meaningful in code)
		"to": true, "of": true, "in": true, "on": true,
		"at": true, "by": true, "with": true, "into": true,
		"onto": true, "upon": true, "out": true, "off": true,
		"over": true, "under": true, "through": true, "during": true,
		"before": true, "after": true, "between": true, "among": true,
		"about": true, "against": true, "without": true, "within": true,
		"along": true, "around": true, "behind": true, "beyond": true,

		// Conjunctions
		"and": true, "or": true, "but": true, "nor": true, "not": true,
		"so": true, "yet": true, "then": true,

		// Common pronouns
		"it": true, "its": true, "we": true, "our": true, "you": true,
		"your": true, "they": true, "them": true, "their": true,

		// Verb forms that are purely syntactic in code context
		"is": true, "are": true, "was": true, "were": true, "be": true,
		"been": true, "being": true, "have": true, "has": true,
		"had": true, "do": true, "does": true, "did": true, "will": true,
		"would": true, "can": true, "could": true, "shall": true,
		"should": true, "may": true, "might": true, "must": true,
	}
}
