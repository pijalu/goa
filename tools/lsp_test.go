// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/lsp"
)

// fakeLSPQueryManager implements LSPQueryManager for tool tests.
type fakeLSPQueryManager struct {
	started bool
	defs    []lsp.Location
	refs    []lsp.Location
	hover   *lsp.Hover
	syms    []lsp.DocumentSymbol
	err     error
}

func (f *fakeLSPQueryManager) Started() bool { return f.started }
func (f *fakeLSPQueryManager) Definition(ctx context.Context, path string, line, ch int) ([]lsp.Location, error) {
	return f.defs, f.err
}
func (f *fakeLSPQueryManager) References(ctx context.Context, path string, line, ch int) ([]lsp.Location, error) {
	return f.refs, f.err
}
func (f *fakeLSPQueryManager) Hover(ctx context.Context, path string, line, ch int) (*lsp.Hover, error) {
	return f.hover, f.err
}
func (f *fakeLSPQueryManager) DocumentSymbols(ctx context.Context, path string) ([]lsp.DocumentSymbol, error) {
	return f.syms, f.err
}

func TestLSPTool_Unavailable(t *testing.T) {
	tool := &LSPTool{ProjectDir: t.TempDir(), Manager: &fakeLSPQueryManager{started: false}}
	_, err := tool.Execute(`{"op":"definition","path":"main.go","line":1,"character":1}`)
	if err == nil || !strings.Contains(err.Error(), "no language server is running") {
		t.Fatalf("expected unavailable error, got %v", err)
	}
}

func TestLSPTool_JavaScriptAccepted(t *testing.T) {
	dir := t.TempDir()
	mgr := &fakeLSPQueryManager{started: true, defs: []lsp.Location{{URI: "file://" + dir + "/x.js"}}}
	tool := &LSPTool{ProjectDir: dir, Manager: mgr}
	if _, err := tool.Execute(`{"op":"definition","path":"x.js","line":0,"character":0}`); err != nil {
		t.Fatalf("JavaScript should be supported: %v", err)
	}
}

func TestLSPTool_JavaAccepted(t *testing.T) {
	dir := t.TempDir()
	tool := &LSPTool{ProjectDir: dir, Manager: &fakeLSPQueryManager{started: true}}
	if _, err := tool.Execute(`{"op":"hover","path":"Main.java","line":0,"character":0}`); err != nil {
		t.Fatalf("Java should be supported: %v", err)
	}
}
func TestLSPTool_NonGoFileRejected(t *testing.T) {
	tool := &LSPTool{ProjectDir: t.TempDir(), Manager: &fakeLSPQueryManager{started: true}}
	_, err := tool.Execute(`{"op":"definition","path":"readme.md","line":1,"character":1}`)
	if err == nil || !strings.Contains(err.Error(), "Go files") {
		t.Fatalf("expected Go-file rejection, got %v", err)
	}
}

func TestLSPTool_UnknownOp(t *testing.T) {
	tool := &LSPTool{ProjectDir: t.TempDir(), Manager: &fakeLSPQueryManager{started: true}}
	_, err := tool.Execute(`{"op":"frobnicate","path":"main.go","line":1,"character":1}`)
	if err == nil || !strings.Contains(err.Error(), "unknown op") {
		t.Fatalf("expected unknown op error, got %v", err)
	}
}

func TestLSPTool_Definition(t *testing.T) {
	dir := t.TempDir()
	mgr := &fakeLSPQueryManager{
		started: true,
		defs:    []lsp.Location{{URI: "file://" + dir + "/helper.go", Range: lsp.Range{Start: lsp.Position{Line: 2, Character: 4}}}},
	}
	tool := &LSPTool{ProjectDir: dir, Manager: mgr}
	out, err := tool.Execute(`{"op":"definition","path":"main.go","line":1,"character":1}`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "helper.go") || !strings.Contains(out, "3:5") {
		t.Errorf("unexpected definition output:\n%s", out)
	}
}

func TestLSPTool_References(t *testing.T) {
	dir := t.TempDir()
	mgr := &fakeLSPQueryManager{
		started: true,
		refs: []lsp.Location{
			{URI: "file://" + dir + "/a.go", Range: lsp.Range{Start: lsp.Position{Line: 0}}},
			{URI: "file://" + dir + "/b.go", Range: lsp.Range{Start: lsp.Position{Line: 9, Character: 2}}},
		},
	}
	tool := &LSPTool{ProjectDir: dir, Manager: mgr}
	out, err := tool.Execute(`{"op":"references","path":"helper.go","line":1,"character":1}`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "References (2)") || !strings.Contains(out, "a.go") || !strings.Contains(out, "b.go") {
		t.Errorf("unexpected references output:\n%s", out)
	}
}

func TestLSPTool_Hover(t *testing.T) {
	dir := t.TempDir()
	mgr := &fakeLSPQueryManager{
		started: true,
		hover:   &lsp.Hover{Contents: map[string]any{"kind": "markdown", "value": "func Greeting() string"}},
	}
	tool := &LSPTool{ProjectDir: dir, Manager: mgr}
	out, err := tool.Execute(`{"op":"hover","path":"helper.go","line":1,"character":1}`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "func Greeting() string") {
		t.Errorf("unexpected hover output:\n%s", out)
	}
}

func TestLSPTool_Symbols(t *testing.T) {
	dir := t.TempDir()
	mgr := &fakeLSPQueryManager{
		started: true,
		syms: []lsp.DocumentSymbol{
			{Name: "main", Kind: 12, SelectionRange: lsp.Range{Start: lsp.Position{Line: 4}}},
			{Name: "Greeting", Detail: "func()", Kind: 12, SelectionRange: lsp.Range{Start: lsp.Position{Line: 1}}},
		},
	}
	tool := &LSPTool{ProjectDir: dir, Manager: mgr}
	out, err := tool.Execute(`{"op":"symbols","path":"main.go"}`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	for _, want := range []string{"main (line 5)", "Greeting func() (line 2)"} {
		if !strings.Contains(out, want) {
			t.Errorf("symbols output missing %q:\n%s", want, out)
		}
	}
}

func TestLSPTool_Schema(t *testing.T) {
	tool := &LSPTool{}
	s := tool.Schema()
	if s.Name != "lsp" {
		t.Errorf("schema name = %q, want lsp", s.Name)
	}
	props, ok := s.Schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("schema properties missing")
	}
	for _, field := range []string{"op", "path", "line", "character"} {
		if _, ok := props[field]; !ok {
			t.Errorf("schema missing property %q", field)
		}
	}
}
