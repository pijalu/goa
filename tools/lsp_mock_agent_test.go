package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/internal/agentic/provider/mock"
	"github.com/pijalu/goa/internal/lsp"
)

// TestMockAgent_EditAndLSP verifies the real Agent tool loop: a scripted model
// requests edit, the file tool notifies the fake language server, and the next
// model turn receives a useful result before completing the turn.
func TestMockAgent_EditAndLSP(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")
	if err := os.WriteFile(path, []byte("package main\n\nfunc main() { println(old) }\n"), 0644); err != nil {
		t.Fatal(err)
	}
	mgr := &mockDocumentManager{server: "gopls", diagnostics: []lsp.Diagnostic{{Message: "undefined: missing", Severity: 1, Range: lsp.Range{Start: lsp.Position{Line: 2, Character: 20}}}}}
	edit := &EditFileTool{ProjectDir: dir, LSPManager: mgr}

	p := mock.New(t)
	model := p.Model("edit-model")
	editArgs := fmt.Sprintf(`{"path":%q,"old_string":"func main() { println(old) }","new_string":"func main() { println(missing) }"}`, path)
	p.Script(model.ID, mock.ToolCallTurn("edit", "edit-1", editArgs))
	p.ReplyText(model.ID, "The edit was applied; diagnostics identify the missing symbol.")
	a := agentic.NewAgent(agentic.Config{Model: model, SystemPrompt: "Use edit.", Tools: []agentic.Tool{edit}})
	var results []string
	a.AddObserver(agentic.OutputObserverFunc(func(ev agentic.OutputEvent) {
		if ev.Type == agentic.EventToolResult {
			results = append(results, ev.ToolResult)
		}
	}))
	if err := a.Run(context.Background(), "Change old to missing and report diagnostics."); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "missing") {
		t.Fatalf("edit was not applied: %s; tool results=%#v", data, results)
	}
	if len(results) != 1 || !strings.Contains(results[0], "Diagnostics (gopls)") || !strings.Contains(results[0], "undefined: missing") {
		t.Fatalf("unhelpful edit result: %#v", results)
	}
	if mgr.changes != 1 {
		t.Fatalf("DidChange calls = %d, want 1", mgr.changes)
	}
}

// is not silently accepted: write succeeds but returns actionable LSP output.
func TestMockAgent_BogusWriteReturnsDiagnostics(t *testing.T) {
	dir := t.TempDir()
	mgr := &mockDocumentManager{server: "gopls", diagnostics: []lsp.Diagnostic{{Message: "expected declaration, found '}'", Severity: 1, Range: lsp.Range{Start: lsp.Position{Line: 0}}}}}
	write := &WriteFileTool{ProjectDir: dir, LSPManager: mgr}
	p := mock.New(t)
	model := p.Model("write-model")
	writeArgs := fmt.Sprintf(`{"path":%q,"content":"package main\n}"}`, filepath.Join(dir, "broken.go"))
	p.Script(model.ID, mock.ToolCallTurn("write", "write-1", writeArgs))
	p.ReplyText(model.ID, "The file was written but needs correction.")
	a := agentic.NewAgent(agentic.Config{Model: model, SystemPrompt: "Use write.", Tools: []agentic.Tool{write}})
	var result string
	a.AddObserver(agentic.OutputObserverFunc(func(ev agentic.OutputEvent) {
		if ev.Type == agentic.EventToolResult {
			result = ev.ToolResult
		}
	}))
	if err := a.Run(context.Background(), "Create the file and report any syntax issue."); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "Diagnostics (gopls)") || !strings.Contains(result, "expected declaration") {
		t.Fatalf("expected useful bogus-write diagnostics, got %q", result)
	}
}

type mockDocumentManager struct {
	server      string
	diagnostics []lsp.Diagnostic
	changes     int
	actions     []lsp.CodeAction
	renameEdit  *lsp.WorkspaceEdit
}

func (m *mockDocumentManager) CodeAction(context.Context, string, lsp.Range) ([]lsp.CodeAction, error) {
	return m.actions, nil
}
func (m *mockDocumentManager) Definition(context.Context, string, int, int) ([]lsp.Location, error) {
	return nil, nil
}
func (m *mockDocumentManager) References(context.Context, string, int, int) ([]lsp.Location, error) {
	return nil, nil
}
func (m *mockDocumentManager) Hover(context.Context, string, int, int) (*lsp.Hover, error) {
	return nil, nil
}
func (m *mockDocumentManager) DocumentSymbols(context.Context, string) ([]lsp.DocumentSymbol, error) {
	return nil, nil
}
func (m *mockDocumentManager) Started() bool            { return true }
func (m *mockDocumentManager) SupportsPath(string) bool { return true }
func (m *mockDocumentManager) PrepareRename(context.Context, string, int, int) (*lsp.PrepareRenameResult, error) {
	return nil, nil
}
func (m *mockDocumentManager) Rename(context.Context, string, int, int, string) (*lsp.WorkspaceEdit, error) {
	return m.renameEdit, nil
}
func (m *mockDocumentManager) Completion(context.Context, string, int, int) ([]lsp.CompletionItem, error) {
	return nil, nil
}
func (m *mockDocumentManager) Formatting(context.Context, string, lsp.FormattingOptions) ([]lsp.TextEdit, error) {
	return nil, nil
}

func (m *mockDocumentManager) OpenDocument(context.Context, string, string) error { return nil }
func (m *mockDocumentManager) DidChange(context.Context, string, string) error {
	m.changes++
	return nil
}

func TestMockAgent_UsesLSPRefactorTool(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")
	_ = os.WriteFile(path, []byte("package main\n"), 0644)
	mgr := &mockDocumentManager{server: "gopls", actions: []lsp.CodeAction{{Title: "Organize imports"}}}
	lspTool := &LSPTool{ProjectDir: dir, Manager: mgr}
	p := mock.New(t)
	model := p.Model("refactor-model")
	args := fmt.Sprintf(`{"op":"codeAction","path":%q,"line":0,"character":0}`, path)
	p.Script(model.ID, mock.ToolCallTurn("lsp", "lsp-1", args))
	p.ReplyText(model.ID, "Use the suggested import action.")
	a := agentic.NewAgent(agentic.Config{Model: model, SystemPrompt: "Use lsp for refactoring and imports.", Tools: []agentic.Tool{lspTool}})
	var result string
	a.AddObserver(agentic.OutputObserverFunc(func(ev agentic.OutputEvent) {
		if ev.Type == agentic.EventToolResult {
			result = ev.ToolResult
		}
	}))
	if err := a.Run(context.Background(), "Find import actions."); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "Code actions (1)") || !strings.Contains(result, "Organize imports") {
		t.Fatalf("unexpected refactor result: %q", result)
	}
}

func TestMockAgent_AppliesLSPRenameRefactor(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")
	if err := os.WriteFile(path, []byte("package main\n\nfunc oldName() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	root, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	resolvedPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	uri := "file://" + resolvedPath
	dir = root
	edit := &lsp.WorkspaceEdit{Changes: map[string][]lsp.TextEdit{uri: {
		{Range: lsp.Range{Start: lsp.Position{Line: 2, Character: 5}, End: lsp.Position{Line: 2, Character: 12}}, NewText: "newName"},
	}}}
	mgr := &mockDocumentManager{server: "gopls", renameEdit: edit}
	lspTool := &LSPTool{ProjectDir: dir, Manager: mgr}
	p := mock.New(t)
	model := p.Model("rename-model")
	args := fmt.Sprintf(`{"op":"rename","path":%q,"line":2,"character":6,"newName":"newName","apply":true}`, path)
	p.Script(model.ID, mock.ToolCallTurn("lsp", "rename-1", args))
	p.ReplyText(model.ID, "Rename applied.")
	a := agentic.NewAgent(agentic.Config{Model: model, SystemPrompt: "Apply the requested rename using LSP.", Tools: []agentic.Tool{lspTool}})
	var result string
	a.AddObserver(agentic.OutputObserverFunc(func(ev agentic.OutputEvent) {
		if ev.Type == agentic.EventToolResult {
			result = ev.ToolResult
		}
	}))
	if err := a.Run(context.Background(), "Rename oldName to newName and apply it."); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "func newName()") || strings.Contains(string(data), "oldName") {
		t.Fatalf("rename was not applied: %q result=%q", data, result)
	}
	if !strings.Contains(result, "Workspace edit applied (1 files, 1 edits)") {
		t.Fatalf("unexpected rename result: %q", result)
	}
}
func (m *mockDocumentManager) DiagnosticsFor(context.Context, string) []lsp.Diagnostic {
	return m.diagnostics
}
func (m *mockDocumentManager) ServerIDFor(string) string { return m.server }
