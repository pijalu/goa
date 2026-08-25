// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package lsp

import (
	"context"
	"encoding/json"
	"sync"
)

// Diagnostic matches a subset of the LSP Diagnostic type.
type Diagnostic struct {
	Range    Range `json:"range"`
	Severity int   `json:"severity"`
	// Code is `any` because the LSP spec allows integer | string — tsserver
	// sends numeric codes (2552) while pyright sends strings; a strict string
	// field made the notification handler drop tsserver diagnostics entirely
	// (Issue LSP).
	Code    any    `json:"code,omitempty"`
	Source  string `json:"source,omitempty"`
	Message string `json:"message"`
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

// DocumentDiagnosticParams requests pull diagnostics.
type DocumentDiagnosticParams struct {
	TextDocument     TextDocumentIdentifier `json:"textDocument"`
	Identifier       string                 `json:"identifier,omitempty"`
	PreviousResultID string                 `json:"previousResultId,omitempty"`
}

// DocumentDiagnosticReport is the pull-diagnostic response.
type DocumentDiagnosticReport struct {
	Kind             string                              `json:"kind"`
	ResultID         string                              `json:"resultId,omitempty"`
	Items            []Diagnostic                        `json:"items,omitempty"`
	RelatedDocuments map[string]DocumentDiagnosticReport `json:"relatedDocuments,omitempty"`
}

type PublishDiagnosticsParams struct {
	URI         string       `json:"uri"`
	Version     int          `json:"version,omitempty"`
	Diagnostics []Diagnostic `json:"diagnostics"`
}

// Diagnostics collects diagnostics published by a language server.
type Diagnostics struct {
	mu     sync.RWMutex
	byFile map[string]diagnosticEntry
}

type diagnosticEntry struct {
	diagnostics []Diagnostic
	version     int
	published   bool
	pending     int
	updated     chan struct{}
}

// DiagnosticSnapshot describes the latest publication for a document. A
// published empty slice is meaningful: it is an explicit clean state, not a
// missing value.
type DiagnosticSnapshot struct {
	Diagnostics []Diagnostic
	Version     int
	Published   bool
	Pending     int
}

// NewDiagnostics creates an empty diagnostics store.
func NewDiagnostics() *Diagnostics {
	return &Diagnostics{byFile: make(map[string]diagnosticEntry)}
}

// Handler returns a notification handler for textDocument/publishDiagnostics.
func (d *Diagnostics) Handler() func(params json.RawMessage) {
	return func(params json.RawMessage) {
		var p PublishDiagnosticsParams
		if err := json.Unmarshal(params, &p); err != nil {
			return
		}
		d.SetVersion(p.URI, p.Version, p.Diagnostics)
	}
}

// Set stores diagnostics for a file URI.
func (d *Diagnostics) Set(uri string, diags []Diagnostic) { d.SetVersion(uri, 0, diags) }

// SetVersion records a publication, ignoring older versions. Empty
// publications remain stored so callers can observe a clean document.
func (d *Diagnostics) SetVersion(uri string, version int, diags []Diagnostic) {
	d.mu.Lock()
	entry := d.byFile[uri]
	if version > 0 && ((entry.published && entry.version > version) || (!entry.published && entry.pending > version)) {
		d.mu.Unlock()
		return
	}
	entry.diagnostics = append([]Diagnostic(nil), diags...)
	if version == 0 {
		// Push servers may omit the document version. Associate that publication
		// with the outstanding edit, or retain the latest known version rather
		// than regressing a previously versioned cache entry to zero.
		if entry.pending > 0 {
			version = entry.pending
		} else if entry.published {
			version = entry.version
		}
	}
	entry.version, entry.published, entry.pending = version, true, 0
	if entry.updated == nil {
		entry.updated = make(chan struct{})
	} else {
		close(entry.updated)
		entry.updated = make(chan struct{})
	}
	d.byFile[uri] = entry
	d.mu.Unlock()
}

// MarkPending invalidates the current publication and waits for a result at
// least version. It also wakes any existing waiters.
func (d *Diagnostics) MarkPending(uri string, version int) {
	d.mu.Lock()
	entry := d.byFile[uri]
	if version > entry.version {
		entry.version = version
	}
	// Document versions are monotonic. Keep the highest outstanding version so
	// an older publication cannot resolve a newer edit's wait.
	if version < entry.version {
		version = entry.version
	}
	if version < entry.pending {
		version = entry.pending
	}
	entry.published, entry.pending = false, version
	if entry.updated == nil {
		entry.updated = make(chan struct{})
	} else {
		close(entry.updated)
		entry.updated = make(chan struct{})
	}
	d.byFile[uri] = entry
	d.mu.Unlock()
}

func (d *Diagnostics) Snapshot(uri string) DiagnosticSnapshot {
	d.mu.Lock()
	defer d.mu.Unlock()
	e := d.byFile[uri]
	return DiagnosticSnapshot{Diagnostics: append([]Diagnostic(nil), e.diagnostics...), Version: e.version, Published: e.published, Pending: e.pending}
}

// Wait blocks until a publication reaches requested version or ctx expires.
func (d *Diagnostics) Wait(ctx context.Context, uri string, version int) DiagnosticSnapshot {
	for {
		d.mu.Lock()
		e := d.byFile[uri]
		snap := DiagnosticSnapshot{append([]Diagnostic(nil), e.diagnostics...), e.version, e.published, e.pending}
		ch := e.updated
		d.mu.Unlock()
		if snap.Published && snap.Version >= version {
			return snap
		}
		select {
		case <-ctx.Done():
			return snap
		case <-ch:
		}
	}
}

// Get returns diagnostics for a file URI.
func (d *Diagnostics) Get(uri string) []Diagnostic {
	d.mu.RLock()
	defer d.mu.RUnlock()
	entry := d.byFile[uri]
	if !entry.published {
		return nil
	}
	return append([]Diagnostic(nil), entry.diagnostics...)
}

// HasErrors reports whether any stored diagnostic has severity Error (1).
func (d *Diagnostics) HasErrors() bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	for _, entry := range d.byFile {
		for _, diag := range entry.diagnostics {
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
		out[k] = append([]Diagnostic(nil), v.diagnostics...)
	}
	return out
}
