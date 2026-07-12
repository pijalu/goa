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

func TestWriteFileTool_LSPManager_SkipsNonGo(t *testing.T) {
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
		t.Errorf("expected no LSP notification for non-Go file, got %v", mgr.opened)
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

func TestEditFileTool_LSPManager_SkipsNonGo(t *testing.T) {
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
		t.Errorf("expected no LSP notification for non-Go file, got %v", mgr.changed)
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
