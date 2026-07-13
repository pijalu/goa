// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package lsp

import (
	"encoding/json"
	"testing"
)

func TestDiagnostics_Handler(t *testing.T) {
	d := NewDiagnostics()
	handler := d.Handler()
	params := PublishDiagnosticsParams{
		URI: "file:///tmp/main.go",
		Diagnostics: []Diagnostic{
			{Message: "expected ';'", Severity: 1, Range: Range{Start: Position{Line: 5}}},
		},
	}
	b, _ := json.Marshal(params)
	handler(b)

	diags := d.Get("file:///tmp/main.go")
	if len(diags) != 1 {
		t.Fatalf("expected 1 diagnostic, got %d", len(diags))
	}
	if diags[0].Message != "expected ';'" {
		t.Errorf("unexpected message %q", diags[0].Message)
	}
}

func TestDiagnostics_Clear(t *testing.T) {
	d := NewDiagnostics()
	d.Set("file:///tmp/main.go", []Diagnostic{{Message: "error", Severity: 1}})
	d.Set("file:///tmp/main.go", nil)
	if len(d.Get("file:///tmp/main.go")) != 0 {
		t.Error("expected diagnostics to be cleared")
	}
}

func TestDiagnostics_HasErrors(t *testing.T) {
	d := NewDiagnostics()
	if d.HasErrors() {
		t.Error("expected no errors initially")
	}
	d.Set("file:///tmp/main.go", []Diagnostic{{Severity: 2}})
	if d.HasErrors() {
		t.Error("warning should not count as error")
	}
	d.Set("file:///tmp/other.go", []Diagnostic{{Severity: 1}})
	if !d.HasErrors() {
		t.Error("expected error to be detected")
	}
}

func TestDiagnostics_All(t *testing.T) {
	d := NewDiagnostics()
	d.Set("a", []Diagnostic{{Message: "x"}})
	all := d.All()
	if len(all) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(all))
	}
	// Mutation of returned slice should not affect internal state.
	all["a"][0].Message = "y"
	if d.Get("a")[0].Message != "x" {
		t.Error("All returned a slice sharing backing storage")
	}
}

func TestDiagnostics_Handler_InvalidJSON(t *testing.T) {
	d := NewDiagnostics()
	handler := d.Handler()
	// Should not panic on invalid JSON.
	handler([]byte("not json"))
}
