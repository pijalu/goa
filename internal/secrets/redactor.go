// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package secrets

import (
	"strings"
)

// Redactor replaces detected secrets with a fixed placeholder.
type Redactor struct {
	scanner   *Scanner
	repl      string
	showTypes bool
}

// DefaultRedactor returns a redactor using the default scanner patterns and
// the placeholder "***".
func DefaultRedactor() *Redactor {
	return &Redactor{
		scanner: DefaultScanner(),
		repl:    "***",
	}
}

// NewRedactor creates a redactor with a custom scanner and replacement text.
func NewRedactor(scanner *Scanner, replacement string) *Redactor {
	if scanner == nil {
		scanner = DefaultScanner()
	}
	if replacement == "" {
		replacement = "***"
	}
	return &Redactor{scanner: scanner, repl: replacement}
}

// WithTypeLabels enables type labels in redacted output, e.g. "<aws_key:***>".
func (r *Redactor) WithTypeLabels(enabled bool) *Redactor {
	r.showTypes = enabled
	return r
}

// Redact scans text and replaces any detected secrets with the configured
// placeholder. It returns the cleaned text and the list of matches that were
// redacted.
func (r *Redactor) Redact(text string) (string, []Match) {
	matches := r.scanner.Scan(text)
	if len(matches) == 0 {
		return text, nil
	}
	var b strings.Builder
	b.Grow(len(text))
	last := 0
	for _, m := range matches {
		b.WriteString(text[last:m.Start])
		if r.showTypes {
			b.WriteString("<")
			b.WriteString(m.SecretType)
			b.WriteString(":")
			b.WriteString(r.repl)
			b.WriteString(">")
		} else {
			b.WriteString(r.repl)
		}
		last = m.End
	}
	b.WriteString(text[last:])
	return b.String(), matches
}

// HasSecrets reports whether the text contains detectable secrets.
func (r *Redactor) HasSecrets(text string) bool {
	return r.scanner.HasSecrets(text)
}

// RedactString is a convenience function that redacts with the default
// redactor and returns only the cleaned string.
func RedactString(text string) string {
	redacted, _ := DefaultRedactor().Redact(text)
	return redacted
}
