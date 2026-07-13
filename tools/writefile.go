// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/internal/lsp"
)

// WriteFileTool creates or overwrites a file with the given content.
//
// IMPORTANT: write MUST NOT use fuzzy filename matching. Fuzzy path
// resolution can redirect writes to the wrong file, causing irreversible
// data loss. Unlike edit/read which handle existing files, write's
// destructive nature requires exact path fidelity.
// formatLSPDiagnostics renders diagnostics as a compact, model-readable block
// appended to tool output. Returns "" when there is nothing to report.
func formatLSPDiagnostics(path string, diags []lsp.Diagnostic) string {
	if len(diags) == 0 {
		return ""
	}
	name := filepath.Base(path)
	var b strings.Builder
	b.WriteString("\nDiagnostics (gopls):\n")
	for _, d := range diags {
		fmt.Fprintf(&b, "  %s:%d:%d: %s: %s\n", name, d.Range.Start.Line+1, d.Range.Start.Character+1, lspSeverityName(d.Severity), d.Message)
	}
	return b.String()
}

// lspSeverityName maps an LSP severity integer to a short label.
func lspSeverityName(sev int) string {
	switch sev {
	case 1:
		return "error"
	case 2:
		return "warning"
	case 3:
		return "info"
	case 4:
		return "hint"
	default:
		return fmt.Sprintf("sev%d", sev)
	}
}

// LSPDiagnostic is the subset of an LSP diagnostic surfaced to tool output.
// Severity follows LSP: 1=Error, 2=Warning, 3=Info, 4=Hint. Line/Col are 0-indexed.
// LSPDocumentManager is the subset of the LSP manager used by file tools.
type LSPDocumentManager interface {
	OpenDocument(ctx context.Context, path, text string) error
	DidChange(ctx context.Context, path, text string) error
	// DiagnosticsFor returns the latest diagnostics published for path, or nil.
	DiagnosticsFor(ctx context.Context, path string) []lsp.Diagnostic
}

type WriteFileTool struct {
	WorktreeMgr        *internal.WorktreeManager
	ProjectDir         string
	GitStager          *GitStager
	// FileChangeNotifier, when set, is called after every successful file
	// write with the resolved (absolute) path. Tools like SmartSearch use
	// this to trigger background index updates.
	FileChangeNotifier func(path string)
	// LSPManager, when set, is notified of the new document and diagnostics
	// are queried after a short delay.
	LSPManager LSPDocumentManager
}

// Schema returns the tool schema for write.
func (t *WriteFileTool) Schema() agentic.ToolSchema {
	return agentic.ToolSchema{
		Name:        "write",
		Description: "Write a file.",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "file path",
				},
				"content": map[string]any{
					"type":        "string",
					"description": "file content",
				},
			},
			"required": []string{"path", "content"},
		},
	}
}

// writeFileParams holds the parsed input for WriteFileTool.
type writeFileParams struct {
	Path       string `json:"path"`
	Content    string `json:"content"`
	CreateDirs *bool  `json:"create_dirs"`
}

// Execute writes content to a file.
func (t *WriteFileTool) Execute(input string) (string, error) {
	p, err := parseWriteFileParams(input)
	if err != nil {
		return "", err
	}

	resolvedPath, originalPath, err := ResolveFileToolPath(t.WorktreeMgr, p.Path)
	if err != nil {
		return "", &internal.ToolError{
			Tool: "write", Type: "protected_path",
			Detail:   fmt.Sprintf("Cannot write to %q", p.Path),
			HintText: "Choose a path outside .goa/ and .git/ directories.",
		}
	}

	processedContent := strings.ReplaceAll(p.Content, "\\n", "\n")
	if err := t.ensureParentDirs(resolvedPath, p.CreateDirs); err != nil {
		return "", err
	}

	t.stageIfExists(resolvedPath)

	if err := os.WriteFile(resolvedPath, []byte(processedContent), 0644); err != nil {
		return "", formatWriteError(originalPath, err)
	}

	if t.FileChangeNotifier != nil {
		t.FileChangeNotifier(resolvedPath)
	}
	diagBlock := t.lspDiagnostics(context.Background(), resolvedPath, processedContent, true)

	preview := buildWritePreview(originalPath, processedContent)
	if diagBlock != "" {
		preview += diagBlock
	}
	return preview, nil
}

// lspDiagnostics notifies the LSP server of a document change (open or edit)
// and returns a formatted diagnostics block for the tool result. It is a
// no-op for non-Go files or when no manager is configured. The open flag
// selects DidOpen (write) vs DidChange (edit).
func (t *WriteFileTool) lspDiagnostics(ctx context.Context, resolvedPath, content string, open bool) string {
	if t.LSPManager == nil || !strings.HasSuffix(resolvedPath, ".go") {
		return ""
	}
	if open {
		_ = t.LSPManager.OpenDocument(ctx, resolvedPath, content)
	} else {
		_ = t.LSPManager.DidChange(ctx, resolvedPath, content)
	}
	// Diagnostics are published asynchronously; give gopls a moment to settle.
	time.Sleep(150 * time.Millisecond)
	diags := t.LSPManager.DiagnosticsFor(ctx, resolvedPath)
	return formatLSPDiagnostics(resolvedPath, diags)
}

func parseWriteFileParams(input string) (writeFileParams, error) {
	var p writeFileParams
	if err := json.Unmarshal([]byte(input), &p); err != nil {
		return writeFileParams{}, &internal.ToolError{
			Tool: "write", Type: "invalid_input",
			Detail:   fmt.Sprintf("Cannot parse parameters: %v", err),
			HintText: "Ensure your input is valid JSON with the required fields.",
		}
	}
	if p.Path == "" {
		return writeFileParams{}, &internal.ToolError{
			Tool: "write", Type: "missing_path",
			Detail: "No path provided", HintText: "Provide a file path in the 'path' field.",
		}
	}
	if p.CreateDirs == nil {
		p.CreateDirs = boolPtr(true)
	}
	return p, nil
}

func boolPtr(b bool) *bool {
	return &b
}

func (t *WriteFileTool) ensureParentDirs(resolvedPath string, createDirs *bool) error {
	if createDirs == nil || !*createDirs {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(resolvedPath), 0755); err != nil {
		return &internal.ToolError{
			Tool: "write", Type: "mkdir_failed",
			Detail:   fmt.Sprintf("Cannot create parent directories: %v", err),
			HintText: "Set create_dirs=false if you want to manually create the directory.",
		}
	}
	return nil
}

func (t *WriteFileTool) stageIfExists(resolvedPath string) {
	if _, err := os.Stat(resolvedPath); err == nil && t.GitStager != nil {
		t.GitStager.StageBeforeEdit(resolvedPath, t.ProjectDir)
	}
}

func formatWriteError(path string, err error) error {
	if os.IsPermission(err) {
		return &internal.ToolError{
			Tool: "write", Type: "permission_denied",
			Detail:   fmt.Sprintf("Permission denied: %s", path),
			HintText: "Check file permissions or use a different path.",
		}
	}
	return &internal.ToolError{
		Tool: "write", Type: "write_error",
		Detail:   fmt.Sprintf("Error writing %s: %v", path, err),
		HintText: "Ensure the path is valid and writable.",
	}
}

func buildWritePreview(path, content string) string {
	lines := strings.Split(content, "\n")
	lineCount := len(lines)
	preview := fmt.Sprintf("[write: %s]\n✓ Written — %d bytes, %d lines\n", path, len(content), lineCount)
	previewLines := 10
	if len(lines) < previewLines {
		previewLines = len(lines)
	}
	if previewLines == 0 {
		return preview
	}
	preview += "```\n"
	for i := 0; i < previewLines; i++ {
		preview += lines[i] + "\n"
	}
	preview += "```\n"
	if len(lines) > previewLines {
		preview += fmt.Sprintf("… %d more lines (Ctrl+O to expand)\n", len(lines)-previewLines)
	}
	return preview
}

// IsRetryable returns false.
func (t *WriteFileTool) IsRetryable(err error) bool { return false }

// Access returns WritePath for the file being written.
func (t *WriteFileTool) Access(input string) ToolAccess {
	var p writeFileParams
	if err := json.Unmarshal([]byte(input), &p); err != nil {
		return ToolAccess{}
	}
	return ToolAccess{WritePaths: []string{p.Path}}
}

//go:embed writefile.short.md writefile.long.md
var writefileDocs embed.FS

func (t *WriteFileTool) ShortDoc() string { return readDoc(writefileDocs, "writefile.short.md") }
func (t *WriteFileTool) LongDoc() string  { return readDoc(writefileDocs, "writefile.long.md") }

func (t *WriteFileTool) Examples() []string {
	return []string{
		`{"path": "src/main.go", "content": "package main\n\nfunc main() {\n\tprintln(\"hello\")\n}"}`,
		`{"path": "docs/notes.md", "content": "# Notes", "create_dirs": true}`,
	}
}
