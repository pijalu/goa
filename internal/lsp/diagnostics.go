// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package lsp

import (
	"encoding/json"
	"sync"
)

// Diagnostic matches a subset of the LSP Diagnostic type.
type Diagnostic struct {
	Range    Range  `json:"range"`
	Severity int    `json:"severity"`
	Code     string `json:"code,omitempty"`
	Source   string `json:"source,omitempty"`
	Message  string `json:"message"`
}

// Range is a zero-indexed LSP range.
type Range struct {
	Start Position `json:"start"`
	End   Position `json:"end"`
}

// Position is a zero-indexed LSP position.
type Position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

// PublishDiagnosticsParams is the notification payload for textDocument/publishDiagnostics.
type PublishDiagnosticsParams struct {
	URI         string       `json:"uri"`
	Version     int          `json:"version,omitempty"`
	Diagnostics []Diagnostic `json:"diagnostics"`
}

// Diagnostics collects diagnostics published by a language server.
type Diagnostics struct {
	mu     sync.RWMutex
	byFile map[string][]Diagnostic
}

// NewDiagnostics creates an empty diagnostics store.
func NewDiagnostics() *Diagnostics {
	return &Diagnostics{byFile: make(map[string][]Diagnostic)}
}

// Handler returns a notification handler for textDocument/publishDiagnostics.
func (d *Diagnostics) Handler() func(params json.RawMessage) {
	return func(params json.RawMessage) {
		var p PublishDiagnosticsParams
		if err := json.Unmarshal(params, &p); err != nil {
			return
		}
		d.Set(p.URI, p.Diagnostics)
	}
}

// Set stores diagnostics for a file URI.
func (d *Diagnostics) Set(uri string, diags []Diagnostic) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(diags) == 0 {
		delete(d.byFile, uri)
		return
	}
	d.byFile[uri] = diags
}

// Get returns diagnostics for a file URI.
func (d *Diagnostics) Get(uri string) []Diagnostic {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.byFile[uri]
}

// HasErrors reports whether any stored diagnostic has severity Error (1).
func (d *Diagnostics) HasErrors() bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	for _, diags := range d.byFile {
		for _, diag := range diags {
			if diag.Severity == 1 {
				return true
			}
		}
	}
	return false
}

// All returns a copy of all diagnostics keyed by URI.
func (d *Diagnostics) All() map[string][]Diagnostic {
	d.mu.RLock()
	defer d.mu.RUnlock()
	out := make(map[string][]Diagnostic, len(d.byFile))
	for k, v := range d.byFile {
		out[k] = append([]Diagnostic(nil), v...)
	}
	return out
}
