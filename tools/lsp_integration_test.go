// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/lsp"
)

type fakeLSPManager struct {
	opened   map[string]string
	changed  map[string]string
	diags    map[string][]lsp.Diagnostic
	nextDiag []lsp.Diagnostic
	// serverIDs maps file extension (with dot) → server id returned by
	// ServerIDFor; nil defaults to ".go → gopls" (legacy single-server fake).
	serverIDs map[string]string
}

func (f *fakeLSPManager) OpenDocument(ctx context.Context, path, text string) error {
	f.opened[path] = text
	return nil
}

func (f *fakeLSPManager) DidChange(ctx context.Context, path, text string) error {
	f.changed[path] = text
	return nil
}

func (f *fakeLSPManager) DiagnosticsFor(ctx context.Context, path string) []lsp.Diagnostic {
	if f.diags != nil {
		return f.diags[path]
	}
	return f.nextDiag
}

func (f *fakeLSPManager) ServerIDFor(path string) string {
	if f.serverIDs != nil {
		return f.serverIDs[filepath.Ext(path)]
	}
	if strings.HasSuffix(path, ".go") {
		return "gopls"
	}
	return ""
}

func TestWriteFileTool_LSPManager_Notify(t *testing.T) {
	dir := t.TempDir()
	mgr := &fakeLSPManager{opened: make(map[string]string)}
	tool := &WriteFileTool{
		ProjectDir: dir,
		LSPManager: mgr,
	}

	path := filepath.Join(dir, "main.go")
	_, err := tool.Execute(`{"path": "` + path + `", "content": "package main"}`)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if _, ok := mgr.opened[path]; !ok {
		t.Errorf("expected OpenDocument to be called for %s", path)
	}
}

// TestWriteFileTool_LSPManager_SkipsUnsupported verifies the tool skips files
// whose TYPE has no language server (the manager decides per extension, not
// the tool — bugs.md Issue LSP). The old behavior hardcoded a .go-only guard
// in the tool; selection now lives in ServerIDFor.
func TestWriteFileTool_LSPManager_SkipsUnsupported(t *testing.T) {
	dir := t.TempDir()
	mgr := &fakeLSPManager{opened: make(map[string]string)}
	tool := &WriteFileTool{
		ProjectDir: dir,
		LSPManager: mgr,
	}

	path := filepath.Join(dir, "README.md")
	_, err := tool.Execute(`{"path": "` + path + `", "content": "# hi"}`)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if len(mgr.opened) != 0 {
		t.Errorf("expected no LSP notification for unsupported file, got %v", mgr.opened)
	}
}

// TestWriteFileTool_LSPManager_NotifyPython verifies LSP wiring covers ALL
// LSP-supported file types, not just .go (bugs.md Issue LSP user directive).
func TestWriteFileTool_LSPManager_NotifyPython(t *testing.T) {
	dir := t.TempDir()
	mgr := &fakeLSPManager{
		opened:    make(map[string]string),
		serverIDs: map[string]string{".py": "pyright"},
		nextDiag: []lsp.Diagnostic{
			{Severity: 1, Message: "\"undefined_xyz\" is not defined", Range: lsp.Range{Start: lsp.Position{Line: 0, Character: 0}}},
		},
	}
	tool := &WriteFileTool{ProjectDir: dir, LSPManager: mgr}

	path := filepath.Join(dir, "main.py")
	out, err := tool.Execute(`{"path": "` + path + `", "content": "undefined_xyz"}`)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if _, ok := mgr.opened[path]; !ok {
		t.Errorf("expected OpenDocument to be called for Python file %s", path)
	}
	if !strings.Contains(out, "Diagnostics (pyright)") {
		t.Errorf("expected pyright-labeled diagnostics block, got:\n%s", out)
	}
	if !strings.Contains(out, "is not defined") {
		t.Errorf("expected diagnostic message in output, got:\n%s", out)
	}
}

func TestEditFileTool_LSPManager_Notify(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")
	if err := os.WriteFile(path, []byte("package main\n\nfunc main() {}"), 0644); err != nil {
		t.Fatal(err)
	}
	mgr := &fakeLSPManager{changed: make(map[string]string)}
	tool := &EditFileTool{
		ProjectDir: dir,
		LSPManager: mgr,
	}

	old := "func main() {}"
	newText := "func main() { println(x) }"
	input := fmt.Sprintf(`{"path": "%s", "old_string": "%s", "new_string": "%s"}`, path, old, newText)
	_, err := tool.Execute(input)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if _, ok := mgr.changed[path]; !ok {
		t.Errorf("expected DidChange to be called for %s", path)
	}
	if !strings.Contains(mgr.changed[path], "println") {
		t.Errorf("expected changed content to include new text, got %q", mgr.changed[path])
	}
}

func TestWriteFileTool_LSPManager_Nil(t *testing.T) {
	dir := t.TempDir()
	tool := &WriteFileTool{ProjectDir: dir}
	path := filepath.Join(dir, "main.go")
	_, err := tool.Execute(`{"path": "` + path + `", "content": "package main"}`)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
}

func TestEditFileTool_LSPManager_NotifyOperation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")
	if err := os.WriteFile(path, []byte("package main\n\nfunc main() {}"), 0644); err != nil {
		t.Fatal(err)
	}
	mgr := &fakeLSPManager{changed: make(map[string]string)}
	tool := &EditFileTool{
		ProjectDir: dir,
		LSPManager: mgr,
	}

	input := fmt.Sprintf(`{"path": "%s", "operation": "replace_lines", "start_line": 1, "end_line": 1, "new_content": "package demo"}`, path)
	_, err := tool.Execute(input)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if _, ok := mgr.changed[path]; !ok {
		t.Errorf("expected DidChange to be called for %s", path)
	}
	if !strings.Contains(mgr.changed[path], "package demo") {
		t.Errorf("expected changed content to include new content, got %q", mgr.changed[path])
	}
}

// TestEditFileTool_LSPManager_SkipsUnsupported verifies edit skips files
// whose type has no language server (manager-side selection).
func TestEditFileTool_LSPManager_SkipsUnsupported(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "notes.md")
	if err := os.WriteFile(path, []byte("# notes\n"), 0644); err != nil {
		t.Fatal(err)
	}
	mgr := &fakeLSPManager{changed: make(map[string]string)}
	tool := &EditFileTool{
		ProjectDir: dir,
		LSPManager: mgr,
	}

	input := fmt.Sprintf(`{"path": "%s", "old_string": "# notes", "new_string": "# updated"}`, path)
	_, err := tool.Execute(input)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if len(mgr.changed) != 0 {
		t.Errorf("expected no LSP notification for unsupported file, got %v", mgr.changed)
	}
}

// TestEditFileTool_LSPManager_NotifyJavaScript verifies edit→LSP wiring for a
// JS file (bugs.md Issue LSP user directive: js/py/go must all be wired).
func TestEditFileTool_LSPManager_NotifyJavaScript(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.js")
	if err := os.WriteFile(path, []byte("const x = 1;\n"), 0644); err != nil {
		t.Fatal(err)
	}
	mgr := &fakeLSPManager{
		changed:   make(map[string]string),
		serverIDs: map[string]string{".js": "typescript"},
		nextDiag: []lsp.Diagnostic{
			{Severity: 1, Message: "Cannot find name 'undefinedXYZ'.", Range: lsp.Range{Start: lsp.Position{Line: 1, Character: 0}}},
		},
	}
	tool := &EditFileTool{ProjectDir: dir, LSPManager: mgr}

	input := fmt.Sprintf(`{"path": "%s", "old_string": "const x = 1;", "new_string": "undefinedXYZ();"}`, path)
	out, err := tool.Execute(input)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if _, ok := mgr.changed[path]; !ok {
		t.Errorf("expected DidChange to be called for JavaScript file %s", path)
	}
	if !strings.Contains(out, "Diagnostics (typescript)") {
		t.Errorf("expected typescript-labeled diagnostics block, got:\n%s", out)
	}
}

func TestEditFileTool_LSPManager_Nil(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")
	if err := os.WriteFile(path, []byte("package main\n"), 0644); err != nil {
		t.Fatal(err)
	}
	tool := &EditFileTool{ProjectDir: dir}
	input := fmt.Sprintf(`{"path": "%s", "old_string": "package main", "new_string": "package demo"}`, path)
	_, err := tool.Execute(input)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
}

// TestWriteFileTool_LSPDiagnosticsAppended verifies diagnostics from the LSP
// manager are surfaced to the model in the tool result (regression for the
// dead-end diagnostics pipeline).
func TestWriteFileTool_LSPDiagnosticsAppended(t *testing.T) {
	dir := t.TempDir()
	mgr := &fakeLSPManager{
		opened: make(map[string]string),
		nextDiag: []lsp.Diagnostic{
			{Severity: 1, Message: "undefined: x", Range: lsp.Range{Start: lsp.Position{Line: 2, Character: 4}}},
		},
	}
	tool := &WriteFileTool{ProjectDir: dir, LSPManager: mgr}

	path := filepath.Join(dir, "main.go")
	out, err := tool.Execute(`{"path": "` + path + `", "content": "package main\n\nfunc main(){ x }"}`)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if !strings.Contains(out, "Diagnostics (gopls)") {
		t.Errorf("expected diagnostics block in output, got:\n%s", out)
	}
	if !strings.Contains(out, "undefined: x") {
		t.Errorf("expected diagnostic message in output, got:\n%s", out)
	}
}

// TestEditFileTool_LSPDiagnosticsAppended verifies edit results surface LSP
// diagnostics too.
func TestEditFileTool_LSPDiagnosticsAppended(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")
	if err := os.WriteFile(path, []byte("package main\n\nfunc main() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	mgr := &fakeLSPManager{
		changed: make(map[string]string),
		nextDiag: []lsp.Diagnostic{
			{Severity: 2, Message: "unused variable y", Range: lsp.Range{Start: lsp.Position{Line: 0, Character: 0}}},
		},
	}
	tool := &EditFileTool{ProjectDir: dir, LSPManager: mgr}

	input := fmt.Sprintf(`{"path": "%s", "old_string": "func main() {}", "new_string": "func main() { y := 1 }"}`, path)
	out, err := tool.Execute(input)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if !strings.Contains(out, "unused variable y") {
		t.Errorf("expected diagnostic message in edit output, got:\n%s", out)
	}
}
