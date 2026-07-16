// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pijalu/goa/docs"
	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/internal/ansi"
)

// ReadFileTool reads file contents with line range support, binary detection,
// and truncation. It resolves paths through the WorktreeManager.
type ReadFileTool struct {
	WorktreeMgr *internal.WorktreeManager
	Config      ReadFileConfig
}

// Schema returns the tool schema for read.
func (t *ReadFileTool) Schema() agentic.ToolSchema {
	return agentic.ToolSchema{
		Name:        "read",
		Description: "Read a file.",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "file path",
				},
				"start_line": map[string]any{
					"type":        "integer",
					"description": "first line (1-indexed, default: 1)",
				},
				"end_line": map[string]any{
					"type":        "integer",
					"description": "last line (1-indexed, default: end)",
				},
				"max_lines": map[string]any{
					"type":        "integer",
					"description": "max lines (default: 500, max: 4096)",
				},
				"max_bytes": map[string]any{
					"type":        "integer",
					"description": "max bytes (default: 50000)",
				},
				"show_numbers": map[string]any{
					"type":        "boolean",
					"description": "show line numbers (default: true)",
				},
			},
			"required": []string{"path"},
		},
	}
}

// readFileParams holds the parsed input for ReadFileTool.
type readFileParams struct {
	Path        string `json:"path"`
	StartLine   int    `json:"start_line"`
	EndLine     int    `json:"end_line"`
	MaxLines    int    `json:"max_lines"`
	MaxBytes    int    `json:"max_bytes"`
	ShowNumbers *bool  `json:"show_numbers"`
}

// showNumbers returns the effective show_numbers value.
func (p readFileParams) showNumbers() bool {
	if p.ShowNumbers == nil {
		return true
	}
	return *p.ShowNumbers
}

// Execute reads the file at the given path. Paths with the goa:// scheme are
// resolved against Goa's embedded documentation instead of the filesystem.
func (t *ReadFileTool) Execute(input string) (string, error) {
	p, err := t.parseParams(input)
	if err != nil {
		return "", err
	}

	if docName, ok := parseGoaDocPath(p.Path); ok {
		data, err := docs.Get(docName)
		if err != nil {
			return "", t.readFileError(p.Path, err)
		}
		docPath := goaDocDisplayPath(docName)
		return t.renderFile(docPath, []byte(data), p), nil
	}

	resolvedPath, err := ResolveToolPath(t.WorktreeMgr, p.Path)
	if err != nil {
		return "", t.readFileError(p.Path, err)
	}

	if err := t.syncWorktree(resolvedPath); err != nil {
		return "", t.worktreeSyncError(err)
	}

	// If the path is a directory, return a listing instead of an error.
	if fi, statErr := os.Stat(resolvedPath); statErr == nil && fi.IsDir() {
		return t.readDir(resolvedPath)
	}

	targetPath, data, err := t.readFile(resolvedPath, p.Path)
	if err != nil {
		return "", err
	}

	rendered := t.renderFile(targetPath, data, p)
	if targetPath != resolvedPath {
		return fmt.Sprintf("Note: file not found, used closest match: %s\n%s", targetPath, rendered), nil
	}
	return rendered, nil
}

// parseParams parses and validates the tool input.
func (t *ReadFileTool) parseParams(input string) (readFileParams, error) {
	var p readFileParams
	if err := json.Unmarshal([]byte(input), &p); err != nil {
		return p, &internal.ToolError{
			Tool: "read", Type: "invalid_input",
			Detail:   fmt.Sprintf("Cannot parse parameters: %v", err),
			HintText: "Ensure your input is valid JSON with the required fields.",
		}
	}
	if p.Path == "" {
		return p, &internal.ToolError{
			Tool: "read", Type: "missing_path",
			Detail: "No path provided", HintText: "Provide a file path in the 'path' field.",
		}
	}
	p.Path = NormalizeFileToolPath(p.Path)
	if p.StartLine == 0 {
		p.StartLine = 1
	}
	if p.MaxLines == 0 {
		p.MaxLines = 500
	}
	if p.MaxLines > maxReadFileLines {
		p.MaxLines = maxReadFileLines
	}
	if p.MaxBytes == 0 {
		p.MaxBytes = defaultReadMaxBytes
	}
	return p, nil
}

// readFile reads the resolved file, falling back to a fuzzy match when the
// file does not exist and fuzzy matching is enabled. It returns the actual
// path read and the file contents.
func (t *ReadFileTool) readFile(resolvedPath, originalPath string) (string, []byte, error) {
	targetPath, data, err := ReadFileWithFuzzyFallback(t.Config, resolvedPath, originalPath)
	if err != nil {
		return "", nil, t.readFileError(originalPath, err)
	}
	return targetPath, data, nil
}

// readDir lists the contents of a directory for the agent instead of
// returning a "is a directory" error. The listing shows one entry per
// line with a trailing / for subdirectories.
func (t *ReadFileTool) readDir(path string) (string, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return "", t.readFileError(path, err)
	}

	displayPath := shortenPath(path)
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "read directory %s\n", displayPath)
	fmt.Fprintf(&buf, "[directory: %d entries]\n", len(entries))

	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() {
			buf.WriteString(name + "/\n")
		} else {
			buf.WriteString(name + "\n")
		}
	}

	return strings.TrimRight(buf.String(), "\n"), nil
}

// shortenPath replaces the home directory prefix with ~ for display.
func shortenPath(path string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	if strings.HasPrefix(path, home) {
		return "~" + path[len(home):]
	}
	return path
}

// renderFile formats file contents for the agent, applying binary detection,
// line-range clamping, and byte truncation. It always reports file metadata
// so the LLM can tell a successful read from a failure, even when truncated.
func (t *ReadFileTool) renderFile(path string, data []byte, p readFileParams) string {
	if isBinary(data) {
		return formatBinaryResult(path, len(data))
	}

	content := string(data)
	totalBytes := len(data)
	lines := splitLines(content)
	totalLines := len(lines)
	if totalLines == 0 {
		return t.formatOutput(path, nil, 0, 0, totalBytes, p.MaxLines, p.MaxBytes, p.showNumbers(), false, "")
	}

	startLine, endLine := clampLineRange(p.StartLine, p.EndLine, totalLines, p.MaxLines)
	selected := lines[startLine-1 : endLine]
	lineTruncated := false
	if len(selected) > p.MaxLines {
		selected = selected[:p.MaxLines]
		lineTruncated = true
	}

	// Apply byte limit to the selected content lines (excluding header/footer).
	renderedContent := renderReadLines(selected, startLine, p.showNumbers())
	byteTruncated := false
	if len(renderedContent) > p.MaxBytes {
		trunc := TruncateHead(renderedContent, p.MaxLines, p.MaxBytes)
		selected = splitLines(trunc.Content)
		byteTruncated = true
	}

	truncated := lineTruncated || byteTruncated
	reason := ""
	if lineTruncated {
		reason = "lines"
	} else if byteTruncated {
		reason = "bytes"
	}
	return t.formatOutput(path, selected, startLine, totalLines, totalBytes, p.MaxLines, p.MaxBytes, p.showNumbers(), truncated, reason)
}

// renderReadLines renders the selected content lines, optionally with line
// numbers, without the header or footer.
func renderReadLines(selected []string, startLine int, showNumbers bool) string {
	var buf bytes.Buffer
	for i, line := range selected {
		if showNumbers {
			fmt.Fprintf(&buf, "%6d  %s\n", startLine+i, line)
		} else {
			buf.WriteString(line)
			buf.WriteByte('\n')
		}
	}
	return buf.String()
}

func (t *ReadFileTool) syncWorktree(resolvedPath string) error {
	if t.WorktreeMgr == nil {
		return nil
	}
	worktreePath := t.WorktreeMgr.CurrentWorktree()
	if worktreePath == "" {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- LazySyncFromMain(t.WorktreeMgr, worktreePath, resolvedPath) }()
	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		return fmt.Errorf("worktree sync timed out")
	}
}

func (t *ReadFileTool) worktreeSyncError(err error) *internal.ToolError {
	return &internal.ToolError{
		Tool: "read", Type: "worktree_sync",
		Detail:   fmt.Sprintf("Worktree sync failed: %v", err),
		HintText: "Check worktree state or retry without worktree isolation.",
	}
}

func (t *ReadFileTool) readFileError(path string, err error) *internal.ToolError {
	if os.IsNotExist(err) {
		return &internal.ToolError{
			Tool: "read", Type: "file_not_found",
			Detail:   fmt.Sprintf("File not found: %s", path),
			HintText: "Check the file path and try again. Use search to find the correct path.",
		}
	}
	if os.IsPermission(err) {
		return &internal.ToolError{
			Tool: "read", Type: "permission_denied",
			Detail:   fmt.Sprintf("Permission denied: %s", path),
			HintText: "Check file permissions or run as a different user.",
		}
	}
	return &internal.ToolError{
		Tool: "read", Type: "read_error",
		Detail:   fmt.Sprintf("Error reading %s: %v", path, err),
		HintText: "Ensure the file is accessible and try again.",
	}
}

// maxReadFileLines is the hard upper bound for max_lines to prevent
// enormous tool outputs from stalling the agent.
const maxReadFileLines = 4096

// defaultReadMaxBytes is the default byte cap for a single read result.
// DEFAULT_MAX_BYTES ensures read results fit comfortably in the
// agent context window.
const defaultReadMaxBytes = 50 * 1024

func (t *ReadFileTool) formatOutput(path string, selected []string, startLine, totalLines, totalBytes, maxLines, maxBytes int, showNumbers, truncated bool, truncReason string) string {
	var buf bytes.Buffer
	endLine := startLine + len(selected) - 1
	if endLine < startLine {
		endLine = startLine
	}
	displayPath := shortenPath(path)
	fmt.Fprintf(&buf, "read file %s:%d:%d\n", displayPath, startLine, endLine)
	fmt.Fprintf(&buf, "[file: %d lines, %d bytes]\n", totalLines, totalBytes)
	if truncated {
		switch truncReason {
		case "lines":
			fmt.Fprintf(&buf, "[read size is limited to %d lines; use max_lines or a range for more]\n", maxLines)
		case "bytes":
			fmt.Fprintf(&buf, "[read size is limited to %d bytes; use max_bytes or a range for more]\n", maxBytes)
		default:
			fmt.Fprintln(&buf, "[read result truncated; use a range for more]")
		}
	}
	buf.WriteString(renderReadLines(selected, startLine, showNumbers))
	remaining := totalLines - (startLine + len(selected) - 1)
	if remaining < 0 {
		remaining = 0
	}
	if truncated {
		fmt.Fprintf(&buf, "(end — %d lines shown, %d remaining; output truncated)\n", len(selected), remaining)
	} else if remaining > 0 {
		fmt.Fprintf(&buf, "(end — %d lines shown, %d remaining)\n", len(selected), remaining)
	} else {
		fmt.Fprintf(&buf, "(end — %d lines shown)\n", len(selected))
	}
	return buf.String()
}

// splitLines splits text into lines, removing the trailing empty line if present.
func splitLines(text string) []string {
	lines := strings.Split(text, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// clampLineRange adjusts start/end lines to valid ranges.
func clampLineRange(startLine, endLine, totalLines, maxLines int) (int, int) {
	if startLine < 1 {
		startLine = 1
	}
	if endLine <= 0 || endLine > totalLines {
		endLine = totalLines
	}
	if startLine > endLine {
		startLine = endLine
	}
	return startLine, endLine
}

// IsRetryable returns false — file read errors are deterministic.
func (t *ReadFileTool) IsRetryable(err error) bool {
	return false
}

// Access returns ReadPath for the file being read.
func (t *ReadFileTool) Access(input string) ToolAccess {
	var p readFileParams
	if err := json.Unmarshal([]byte(input), &p); err != nil {
		return ToolAccess{}
	}
	return ToolAccess{ReadPaths: []string{p.Path}}
}

// ShortDoc returns the short description.
//
//go:embed readfile.short.md readfile.long.md
var readfileDocs embed.FS

func (t *ReadFileTool) ShortDoc() string { return readDoc(readfileDocs, "readfile.short.md") }
func (t *ReadFileTool) LongDoc() string  { return readDoc(readfileDocs, "readfile.long.md") }
func (t *ReadFileTool) Examples() []string {
	return []string{
		`{"path": "src/main.go"}`,
		`{"path": "src/auth.go", "start_line": 10, "end_line": 30}`,
		`{"path": "README.md", "max_lines": 50}`,
		`{"path": "goa://docs/SKILLS.md"}`,
	}
}

// parseGoaDocPath extracts a documentation name from a goa:// URL.
// It delegates to the docs package so URL parsing is centralized.
func parseGoaDocPath(path string) (string, bool) {
	return docs.ParseGoaURL(path)
}

// goaDocDisplayPath returns a canonical display path for an embedded doc.
func goaDocDisplayPath(name string) string {
	return "goa://docs/" + name + ".md"
}

// isBinary checks if data contains null bytes (simple binary detection).
func isBinary(data []byte) bool {
	checkLen := 8192
	if len(data) < checkLen {
		checkLen = len(data)
	}
	return bytes.IndexByte(data[:checkLen], 0) >= 0
}

// formatBinaryResult returns a message for binary files.
func formatBinaryResult(path string, size int) string {
	return fmt.Sprintf("[binary file: %s, %d bytes — not readable as text]", filepath.Base(path), size)
}
