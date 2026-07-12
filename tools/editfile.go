// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/internal/agentic"
)

type EditOperation string

const (
	OpReplaceLines   EditOperation = "replace_lines"
	OpReplacePattern EditOperation = "replace_pattern"
	OpInsertAfter    EditOperation = "insert_after"
	OpInsertBefore   EditOperation = "insert_before"
	OpDeleteLines    EditOperation = "delete_lines"
)

type IndentMode string

const (
	IndentPreserve  IndentMode = "preserve"
	IndentNormalize IndentMode = "normalize"
	IndentAsIs      IndentMode = "as-is"
)

type editParams struct {
	startLine    int
	endLine      int
	pattern      string
	patternFlags string
	occurrence   int
	newLines     []string
	indentMode   IndentMode
}

type EditFileTool struct {
	WorktreeMgr        *internal.WorktreeManager
	ProjectDir         string
	GitStager          *GitStager
	AllowFuzz          bool // enable fuzzy matching (trailing whitespace, whitespace collapse, reindent)
	Config             FileToolConfig
	// FileChangeNotifier, when set, is called after every successful file
	// write with the resolved (absolute) path. Tools like SmartSearch use
	// this to trigger background index updates.
	FileChangeNotifier func(path string)
	// LSPManager, when set, is notified of content changes for .go files.
	LSPManager LSPDocumentManager
}

func (t *EditFileTool) Schema() agentic.ToolSchema {
	return agentic.ToolSchema{
		Name:        "edit",
		Description: "Edit files by search/replace.",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "Path to the file to edit",
				},
				"old_string": map[string]any{
					"type":        "string",
					"description": "Text to search for and replace",
				},
				"new_string": map[string]any{
					"type":        "string",
					"description": "Replacement text",
				},
				"operation": map[string]any{
					"type":        "string",
					"description": "Edit operation (default: replace)",
					"enum":        []string{"replace", "replace_lines", "replace_pattern", "insert_after", "insert_before", "delete_lines"},
				},
				"start_line": map[string]any{
					"type":        "integer",
					"description": "Start line (1-indexed, for replace_lines/insert_after/insert_before)",
				},
				"end_line": map[string]any{
					"type":        "integer",
					"description": "End line (1-indexed, for replace_lines/delete_lines)",
				},
				"pattern": map[string]any{
					"type":        "string",
					"description": "Regex pattern for replace_pattern/insert_after/insert_before",
				},
				"pattern_flags": map[string]any{
					"type":        "string",
					"description": "Regex flags (e.g. 'i' for case-insensitive)",
				},
				"occurrence": map[string]any{
					"type":        "integer",
					"description": "Which occurrence to replace for replace_pattern (default: 1)",
				},
				"new_content": map[string]any{
					"type":        "string",
					"description": "New content for replace_lines/insert_after/insert_before",
				},
				"indent_mode": map[string]any{
					"type":        "string",
					"description": "Indent handling: preserve (default), normalize, as-is",
					"enum":        []string{"preserve", "normalize", "as-is"},
				},
			},
			"required": []string{"path"},
		},
	}
}

// editFileParams holds the parsed input for EditFileTool.
type editFileParams struct {
	Path         string `json:"path"`
	Operation    string `json:"operation"`
	OldString    string `json:"old_string"`
	NewString    string `json:"new_string"`
	StartLine    int    `json:"start_line"`
	EndLine      int    `json:"end_line"`
	Pattern      string `json:"pattern"`
	PatternFlags string `json:"pattern_flags"`
	Occurrence   int    `json:"occurrence"`
	NewContent   string `json:"new_content"`
	IndentMode   string `json:"indent_mode"`
}

func (t *EditFileTool) Execute(input string) (string, error) {
	var p editFileParams
	if err := json.Unmarshal([]byte(input), &p); err != nil {
		return "", &internal.ToolError{
			Tool: "edit", Type: "invalid_input",
			Detail:   fmt.Sprintf("Cannot parse parameters: %v", err),
			HintText: "Ensure your input is valid JSON with the required fields.",
		}
	}
	if p.Path == "" {
		return "", errMissingPath()
	}

	resolvedPath, originalPath, err := ResolveFileToolPath(t.WorktreeMgr, p.Path)
	if err != nil {
		return "", t.errProtected(p.Path)
	}

	if p.OldString != "" {
		return t.searchReplace(resolvedPath, originalPath, p.OldString, p.NewString, t.AllowFuzz)
	}

	return t.editByOperation(resolvedPath, originalPath, p)
}

func (t *EditFileTool) editByOperation(resolvedPath, originalPath string, p editFileParams) (string, error) {
	op := p.Operation
	if op == "" {
		return "", errMissingParam()
	}

	lines, targetPath, fuzzyNote, err := t.readLines(resolvedPath, originalPath)
	if err != nil {
		return "", err
	}

	ep := editParams{
		startLine:    p.StartLine,
		endLine:      p.EndLine,
		pattern:      p.Pattern,
		patternFlags: p.PatternFlags,
		occurrence:   p.Occurrence,
		newLines:     strings.Split(strings.ReplaceAll(p.NewContent, "\\n", "\n"), "\n"),
		indentMode:   IndentMode(defaultStr(p.IndentMode, string(IndentPreserve))),
	}

	result, affected, opErr := t.runOp(lines, EditOperation(op), ep)
	if opErr != nil {
		return "", wrapEditOpError(opErr, p.Path, op)
	}

	if t.GitStager != nil {
		t.GitStager.StageBeforeEdit(targetPath, t.ProjectDir)
	}

	output := strings.Join(result, "\n")
	if err := os.WriteFile(targetPath, []byte(output), 0644); err != nil {
		return "", t.errWrite(p.Path, err)
	}

	if t.FileChangeNotifier != nil {
		t.FileChangeNotifier(targetPath)
	}
	t.notifyLSP(context.Background(), targetPath)

	// Generate unified diff for the change so the renderer can display it.
	diff := generateUnifiedDiff(lines, result)

	resultMsg := fmt.Sprintf("[edit: %s] %s — %d lines affected\n%s", p.Path, op, affected, diff)
	if fuzzyNote != "" {
		resultMsg = fuzzyNote + "\n" + resultMsg
	}
	return resultMsg, nil
}

func wrapEditOpError(opErr error, path, op string) error {
	te, ok := opErr.(*internal.ToolError)
	if ok {
		te.Detail = fmt.Sprintf("[%s] %s: %s", path, op, te.Detail)
		if te.HintText == "" {
			te.HintText = "Use 'read' to verify the file content and operation parameters, then retry."
		}
		return te
	}
	return &internal.ToolError{
		Tool: "edit", Type: "operation_failed",
		Detail:   fmt.Sprintf("[%s] %s: %v", path, op, opErr),
		HintText: "Use 'read' to verify the file content and operation parameters, then retry.",
	}
}

func (t *EditFileTool) IsRetryable(err error) bool { return false }

// Access returns WritePath for the file being edited.
func (t *EditFileTool) Access(input string) ToolAccess {
	var p editFileParams
	if err := json.Unmarshal([]byte(input), &p); err != nil {
		return ToolAccess{}
	}
	return ToolAccess{WritePaths: []string{p.Path}}
}

//go:embed editfile.short.md editfile.long.md
var editfileDocs embed.FS

func (t *EditFileTool) notifyLSP(ctx context.Context, resolvedPath string) {
	if t.LSPManager == nil || !strings.HasSuffix(resolvedPath, ".go") {
		return
	}
	content, err := os.ReadFile(resolvedPath)
	if err != nil {
		return
	}
	_ = t.LSPManager.DidChange(ctx, resolvedPath, string(content))
	time.Sleep(50 * time.Millisecond)
}

func (t *EditFileTool) ShortDoc() string { return readDoc(editfileDocs, "editfile.short.md") }
func (t *EditFileTool) LongDoc() string  { return readDoc(editfileDocs, "editfile.long.md") }

func (t *EditFileTool) Examples() []string {
	return []string{
		`{"path": "src/main.go", "old_string": "fmt.Println(\"hello\")", "new_string": "fmt.Println(\"world\")"}`,
		`{"path": "auth.go", "old_string": "func oldName()", "new_string": "func newName()"}`,
		`{"path": "src/main.go", "operation": "replace_lines", "start_line": 5, "end_line": 8, "new_content": "func main() {\n\tlog.Println(\"start\")\n}"}`,
	}
}

func (t *EditFileTool) readLines(resolvedPath, originalPath string) ([]string, string, string, error) {
	targetPath, data, err := ReadFileWithFuzzyFallback(t.Config, resolvedPath, originalPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, "", "", &internal.ToolError{Tool: "edit", Type: "file_not_found",
				Detail:   fmt.Sprintf("File not found: %s", originalPath),
				HintText: "Check the path or use write to create the file first."}
		}
		return nil, "", "", &internal.ToolError{Tool: "edit", Type: "read_error",
			Detail:   fmt.Sprintf("Cannot read %s: %v", originalPath, err),
			HintText: "Ensure the file exists and is readable."}
	}
	var fuzzyNote string
	if targetPath != resolvedPath {
		fuzzyNote = fmt.Sprintf("Note: file not found, used closest match: %s", targetPath)
	}
	return splitLines(string(data)), targetPath, fuzzyNote, nil
}

func (t *EditFileTool) runOp(lines []string, op EditOperation, p editParams) ([]string, int, error) {
	switch op {
	case OpReplaceLines:
		return t.replaceLines(lines, p.startLine, p.endLine, p.newLines, p.indentMode)
	case OpReplacePattern:
		return t.replacePattern(lines, p.pattern, p.patternFlags, p.occurrence, p.newLines, p.indentMode)
	case OpInsertAfter:
		return t.insertAfter(lines, p.startLine, p.pattern, p.newLines, p.indentMode)
	case OpInsertBefore:
		return t.insertBefore(lines, p.startLine, p.pattern, p.newLines, p.indentMode)
	case OpDeleteLines:
		return t.deleteLines(lines, p.startLine, p.endLine)
	default:
		return nil, 0, &internal.ToolError{Tool: "edit", Type: "unknown_operation",
			Detail:   fmt.Sprintf("Unknown operation: %s", op),
			HintText: "Use one of: replace_lines, replace_pattern, insert_after, insert_before, delete_lines"}
	}
}

func (t *EditFileTool) replaceLines(lines []string, startLine, endLine int, newLines []string, indentMode IndentMode) ([]string, int, error) {
	start, end, err := t.checkLineRange(lines, startLine, endLine)
	if err != nil {
		return nil, 0, err
	}
	targetLines := lines[start-1 : end]
	adjusted := t.adjustIndent(targetLines, newLines, indentMode)
	result := make([]string, 0, len(lines)-len(targetLines)+len(adjusted))
	result = append(result, lines[:start-1]...)
	result = append(result, adjusted...)
	result = append(result, lines[end:]...)
	return result, len(adjusted), nil
}

func (t *EditFileTool) replacePattern(lines []string, pattern, flags string, occurrence int, newLines []string, indentMode IndentMode) ([]string, int, error) {
	if pattern == "" {
		return nil, 0, &internal.ToolError{Tool: "edit", Type: "missing_pattern",
			Detail: "Pattern is required for replace_pattern", HintText: "Provide a 'pattern' to search for."}
	}
	caseSensitive := !strings.Contains(flags, "i")
	found := 0
	result := make([]string, 0, len(lines))
	for _, line := range lines {
		if matchLine(line, pattern, caseSensitive) {
			found++
			if found == occurrence {
				adjusted := t.adjustIndent([]string{line}, newLines, indentMode)
				result = append(result, adjusted...)
				continue
			}
		}
		result = append(result, line)
	}
	if found == 0 {
		return nil, 0, &internal.ToolError{Tool: "edit", Type: "pattern_not_found",
			Detail:   fmt.Sprintf("Pattern %q not found in file", pattern),
			HintText: "Use 'read' to verify the file content and check the pattern for typos or try different flags."}
	}
	return result, len(newLines), nil
}

func (t *EditFileTool) insertAfter(lines []string, lineNum int, pattern string, newLines []string, indentMode IndentMode) ([]string, int, error) {
	if lineNum > 0 {
		return t.insertAtLine(lines, lineNum, newLines, indentMode, false)
	}
	if pattern == "" {
		return nil, 0, &internal.ToolError{Tool: "edit", Type: "missing_parameter",
			Detail:   "Provide either start_line or pattern for insert_after",
			HintText: "Specify which line or pattern to insert after."}
	}
	return t.insertAtPattern(lines, pattern, newLines, indentMode, false)
}

func (t *EditFileTool) insertBefore(lines []string, lineNum int, pattern string, newLines []string, indentMode IndentMode) ([]string, int, error) {
	if lineNum > 0 {
		return t.insertAtLine(lines, lineNum, newLines, indentMode, true)
	}
	if pattern == "" {
		return nil, 0, &internal.ToolError{Tool: "edit", Type: "missing_parameter",
			Detail:   "Provide either start_line or pattern for insert_before",
			HintText: "Specify which line or pattern to insert before."}
	}
	return t.insertAtPattern(lines, pattern, newLines, indentMode, true)
}

func (t *EditFileTool) deleteLines(lines []string, startLine, endLine int) ([]string, int, error) {
	start, end, err := t.checkLineRange(lines, startLine, endLine)
	if err != nil {
		return nil, 0, err
	}
	result := make([]string, 0, len(lines)-(end-start+1))
	result = append(result, lines[:start-1]...)
	result = append(result, lines[end:]...)
	return result, end - start + 1, nil
}

// checkLineRange validates and normalizes a 1-indexed line range.
// Returns the normalized (start, end). endLine <= 0 means "to end of file".
// endLine > len(lines) is rejected as invalid_range (no silent clamp) so the
// caller never slices with an out-of-bounds value.
func (t *EditFileTool) checkLineRange(lines []string, startLine, endLine int) (int, int, error) {
	n := len(lines)
	if startLine < 1 || startLine > n {
		return 0, 0, &internal.ToolError{Tool: "edit", Type: "invalid_range",
			Detail:   fmt.Sprintf("start_line %d is out of range (file has %d lines)", startLine, n),
			HintText: "Use 'read' to verify the file length and provide a valid start_line."}
	}
	if endLine <= 0 {
		endLine = n
	}
	if endLine > n {
		return 0, 0, &internal.ToolError{Tool: "edit", Type: "invalid_range",
			Detail:   fmt.Sprintf("end_line %d is out of range (file has %d lines)", endLine, n),
			HintText: "Use 'read' to verify the file length and provide a valid end_line."}
	}
	if startLine > endLine {
		return 0, 0, &internal.ToolError{Tool: "edit", Type: "invalid_range",
			Detail:   fmt.Sprintf("start_line %d > end_line %d", startLine, endLine),
			HintText: "start_line must be <= end_line."}
	}
	return startLine, endLine, nil
}

func (t *EditFileTool) insertAtLine(lines []string, lineNum int, newLines []string, indentMode IndentMode, before bool) ([]string, int, error) {
	if lineNum < 1 || lineNum > len(lines) {
		return nil, 0, &internal.ToolError{Tool: "edit", Type: "invalid_line",
			Detail:   fmt.Sprintf("Line %d is out of range (file has %d lines)", lineNum, len(lines)),
			HintText: "Use 'read' to check the file content and provide a valid line number."}
	}
	idx := lineNum - 1
	target := []string{lines[idx]}
	adjusted := t.adjustIndent(target, newLines, indentMode)
	result := make([]string, 0, len(lines)+len(adjusted))
	if before {
		result = append(result, lines[:idx]...)
		result = append(result, adjusted...)
		result = append(result, lines[idx:]...)
	} else {
		result = append(result, lines[:idx+1]...)
		result = append(result, adjusted...)
		result = append(result, lines[idx+1:]...)
	}
	return result, len(adjusted), nil
}

func (t *EditFileTool) insertAtPattern(lines []string, pattern string, newLines []string, indentMode IndentMode, before bool) ([]string, int, error) {
	for i, line := range lines {
		if strings.Contains(line, pattern) {
			adjusted := t.adjustIndent([]string{line}, newLines, indentMode)
			result := make([]string, 0, len(lines)+len(adjusted))
			if before {
				result = append(result, lines[:i]...)
				result = append(result, adjusted...)
				result = append(result, lines[i:]...)
			} else {
				result = append(result, lines[:i+1]...)
				result = append(result, adjusted...)
				result = append(result, lines[i+1:]...)
			}
			return result, len(adjusted), nil
		}
	}
	return nil, 0, &internal.ToolError{Tool: "edit", Type: "pattern_not_found",
		Detail:   fmt.Sprintf("Pattern %q not found in file", pattern),
		HintText: "Use 'read' to verify the file content and check the pattern for typos."}
}

func matchLine(line, pattern string, caseSensitive bool) bool {
	// Try as regex first
	re, err := regexp.Compile(pattern)
	if err == nil {
		if caseSensitive {
			return re.MatchString(line)
		}
		return re.MatchString(strings.ToLower(line))
	}
	// Fall back to substring match
	check := line
	match := pattern
	if !caseSensitive {
		check = strings.ToLower(line)
		match = strings.ToLower(pattern)
	}
	return strings.Contains(check, match)
}

func (t *EditFileTool) adjustIndent(targetLines, newLines []string, mode IndentMode) []string {
	switch mode {
	case IndentPreserve:
		return adjustPreserve(targetLines, newLines)
	case IndentNormalize:
		return adjustNormalize(targetLines, newLines)
	default:
		return newLines
	}
}

func adjustPreserve(targetLines, newLines []string) []string {
	if len(targetLines) == 0 || len(newLines) == 0 {
		return newLines
	}
	targetIndent := len(leadingWS(targetLines[0]))
	sourceIndent := len(leadingWS(newLines[0]))
	delta := targetIndent - sourceIndent
	result := make([]string, len(newLines))
	for i, line := range newLines {
		if delta >= 0 {
			result[i] = strings.Repeat(" ", delta) + line
		} else if len(line) >= -delta {
			result[i] = line[-delta:]
		} else {
			result[i] = ""
		}
	}
	return result
}

func adjustNormalize(targetLines, newLines []string) []string {
	var indents []int
	for _, line := range targetLines {
		indents = append(indents, len(leadingWS(line)))
	}
	sort.Ints(indents)
	target := 0
	if len(indents) > 0 && indents[len(indents)/2] > 0 {
		target = indents[len(indents)/2]
		if target > 4 {
			target = 4
		}
	}
	result := make([]string, len(newLines))
	for i, line := range newLines {
		stripped := strings.TrimLeft(line, " \t")
		if stripped == "" {
			result[i] = ""
		} else {
			result[i] = strings.Repeat(" ", target) + stripped
		}
	}
	return result
}

func leadingWS(line string) string {
	for i, ch := range line {
		if ch != ' ' && ch != '\t' {
			return line[:i]
		}
	}
	return line
}

func errMissingPath() *internal.ToolError {
	return &internal.ToolError{Tool: "edit", Type: "missing_path",
		Detail:   "No 'path' provided",
		HintText: "Provide the file path in the 'path' field."}
}

func errMissingParam() *internal.ToolError {
	return &internal.ToolError{Tool: "edit", Type: "missing_parameter",
		Detail:   "Either 'old_string' or 'operation' is required",
		HintText: "Provide 'old_string'+'new_string' for search/replace, or 'operation' for line/pattern operations."}
}

func (t *EditFileTool) errProtected(path string) *internal.ToolError {
	return &internal.ToolError{Tool: "edit", Type: "protected_path",
		Detail:   fmt.Sprintf("Cannot edit %q", path),
		HintText: "Choose a path outside .goa/ and .git/ directories."}
}

func (t *EditFileTool) errWrite(path string, err error) *internal.ToolError {
	return &internal.ToolError{Tool: "edit", Type: "write_error",
		Detail:   fmt.Sprintf("Error writing %s: %v", path, err),
		HintText: "Check disk space and permissions."}
}

// searchReplace applies search/replace using the internal fuzzyEdit helper.
// When allowFuzz is true, uses 3-tier matching (exact → trailing whitespace → fuzzy).
// When false, uses exact match only.
// It reads the file, applies the edit, and writes the result back.
func (t *EditFileTool) searchReplace(resolvedPath, originalPath, oldStr, newStr string, allowFuzz bool) (string, error) {
	targetPath, data, err := ReadFileWithFuzzyFallback(t.Config, resolvedPath, originalPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", &internal.ToolError{Tool: "edit", Type: "file_not_found",
				Detail:   fmt.Sprintf("File not found: %s", originalPath),
				HintText: "Check the path or use write to create the file first."}
		}
		return "", &internal.ToolError{Tool: "edit", Type: "read_error",
			Detail:   fmt.Sprintf("Cannot read %s: %v", originalPath, err),
			HintText: "Ensure the file exists and is readable."}
	}

	result, err := fuzzyEdit(string(data), oldStr, newStr, allowFuzz)
	if err != nil {
		return "", t.searchReplaceError(originalPath, oldStr, err)
	}

	if t.GitStager != nil {
		t.GitStager.StageBeforeEdit(targetPath, t.ProjectDir)
	}

	if err := os.WriteFile(targetPath, []byte(result.NewContent), 0644); err != nil {
		return "", t.errWrite(originalPath, err)
	}

	if t.FileChangeNotifier != nil {
		t.FileChangeNotifier(targetPath)
	}
	t.notifyLSP(context.Background(), targetPath)

	// Build a clear result message
	matchDesc := "exact match"
	switch result.MatchType {
	case MatchTrailingWhitespace:
		matchDesc = "trailing whitespace normalized"
	case MatchFuzzy:
		matchDesc = "fuzzy whitespace match (indentation auto-adjusted)"
	}

	resultMsg := fmt.Sprintf("[edit: %s] search/replace applied — lines %d-%d, match: %s\n%s",
		originalPath, result.StartLine, result.EndLine, matchDesc, result.Diff)
	if targetPath != resolvedPath {
		resultMsg = fmt.Sprintf("Note: file not found, used closest match: %s\n%s", targetPath, resultMsg)
	}
	return resultMsg, nil
}

func (t *EditFileTool) searchReplaceError(path, oldStr string, err error) *internal.ToolError {
	switch {
	case errors.Is(err, ErrAmbiguous):
		return &internal.ToolError{Tool: "edit", Type: "ambiguous_match",
			Detail:   fmt.Sprintf("Text %q matches multiple locations in %s", truncateStr(oldStr, 40), path),
			HintText: "Add more surrounding context to 'old_string' so only one location matches. If the block is hard to make unique, use 'operation: replace_lines' with start_line/end_line instead."}
	case errors.Is(err, ErrNotFound):
		message := "Text %q not found in %s (exact match only)"
		if t.AllowFuzz {
			message = "Text %q not found in %s (tried exact, trailing whitespace, and fuzzy matching)"
		}
		return &internal.ToolError{Tool: "edit", Type: "not_found",
			Detail:   fmt.Sprintf(message, truncateStr(oldStr, 40), path),
			HintText: "Use 'read' to verify the current file content (the file may have changed since your last read). Match the exact text including indentation and blank lines. For deletions or multi-line changes, use 'operation: delete_lines' or 'operation: replace_lines' with line numbers."}
	case errors.Is(err, ErrNoChange):
		return &internal.ToolError{Tool: "edit", Type: "no_change",
			Detail:   "Old and new text are identical",
			HintText: "Provide different 'new_string' content."}
	case errors.Is(err, ErrEmptyOldStr):
		return &internal.ToolError{Tool: "edit", Type: "empty_old_string",
			Detail:   "'old_string' must not be empty",
			HintText: "Provide the text to search for in the 'old_string' field."}
	default:
		return &internal.ToolError{Tool: "edit", Type: "edit_error",
			Detail:   fmt.Sprintf("Edit failed: %v", err),
			HintText: "Check the file content with 'read' and try again."}
	}
}

// truncateStr returns s truncated to n runes with "..." suffix.
func truncateStr(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "..."
}

// generateUnifiedDiff produces a unified-diff hunk comparing oldLines (before)
// and newLines (after), finding the differing region automatically and including
// surrounding context. Returns the diff hunk text, or empty if no difference.
func generateUnifiedDiff(oldLines, newLines []string) string {
	// Find first differing index
	start := 0
	for start < len(oldLines) && start < len(newLines) && oldLines[start] == newLines[start] {
		start++
	}
	if start >= len(oldLines) && start >= len(newLines) {
		return "" // identical
	}

	// Find last differing index (looking from end)
	oldEnd := len(oldLines)
	newEnd := len(newLines)
	for oldEnd > start && newEnd > start && oldLines[oldEnd-1] == newLines[newEnd-1] {
		oldEnd--
		newEnd--
	}

	const ctxLines = 3

	ctxStart := start - ctxLines
	if ctxStart < 0 {
		ctxStart = 0
	}
	ctxBeforeLines := start - ctxStart

	ctxOldEnd := oldEnd + ctxLines
	if ctxOldEnd > len(oldLines) {
		ctxOldEnd = len(oldLines)
	}
	ctxAfterLines := ctxOldEnd - oldEnd

	oldCount := (oldEnd - start) + ctxBeforeLines + ctxAfterLines
	newCount := (newEnd - start) + ctxBeforeLines + ctxAfterLines

	var b strings.Builder
	fmt.Fprintf(&b, "@@ -%d,%d +%d,%d @@\n", ctxStart+1, oldCount, ctxStart+1, newCount)

	// Context before
	writeDiffRange(&b, " ", oldLines, ctxStart, start)
	// Removed lines
	writeDiffRange(&b, "-", oldLines, start, oldEnd)
	// Added lines
	writeDiffRange(&b, "+", newLines, start, newEnd)
	// Context after (from old file since they're identical in this region)
	writeDiffRange(&b, " ", oldLines, oldEnd, ctxOldEnd)

	return strings.TrimSuffix(b.String(), "\n")
}

// writeDiffRange writes lines[from:to] to b, each prefixed with prefix and
// terminated by a newline.
func writeDiffRange(b *strings.Builder, prefix string, lines []string, from, to int) {
	for i := from; i < to; i++ {
		fmt.Fprintf(b, "%s%s\n", prefix, lines[i])
	}
}
