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

	"strings"

	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/internal/agentic"
)

type EditOperation string

const (
	OpReplace        EditOperation = "replace"
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
	WorktreeMgr  *internal.WorktreeManager
	ProjectDir   string
	BackupStager *BackupStager
	AllowFuzz    bool // enable fuzzy matching (trailing whitespace, whitespace collapse, reindent)
	Config       FileToolConfig
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
					"description": "file path",
				},
				"old_string": map[string]any{
					"type":        "string",
					"description": "text to match",
				},
				"new_string": map[string]any{
					"type":        "string",
					"description": "replacement text",
				},
				"operation": map[string]any{
					"type": "string",
					"enum": []string{"replace", "replace_lines", "replace_pattern", "insert_after", "insert_before", "delete_lines"},
				},
				"start_line": map[string]any{
					"type":        "integer",
					"description": "start line (1-indexed) for line ops",
				},
				"end_line": map[string]any{
					"type":        "integer",
					"description": "end line (1-indexed) for line ops",
				},
				"pattern": map[string]any{
					"type":        "string",
					"description": "regex for pattern-based ops",
				},
				"pattern_flags": map[string]any{
					"type":        "string",
					"description": "regex flags (e.g. 'i')",
				},
				"occurrence": map[string]any{
					"type":        "integer",
					"description": "occurrence for replace_pattern (default: 1)",
				},
				"new_content": map[string]any{
					"type":        "string",
					"description": "replacement content for line ops",
				},
				"indent_mode": map[string]any{
					"type": "string",
					"enum": []string{"preserve", "normalize", "as-is"},
				},
				"edits": map[string]any{
					"type":        "array",
					"description": "Batch of edits to the same file, applied in order, atomically (all or nothing); each element mirrors single-edit fields and sees earlier results.",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							// Field docs intentionally omitted: names/types/enums mirror the
							// flat single-edit properties documented above (context budget).
							"operation": map[string]any{
								"type": "string",
								"enum": []string{"replace", "replace_lines", "replace_pattern", "insert_after", "insert_before", "delete_lines"},
							},
							"old_string":    map[string]any{"type": "string"},
							"new_string":    map[string]any{"type": "string"},
							"start_line":    map[string]any{"type": "integer"},
							"end_line":      map[string]any{"type": "integer"},
							"pattern":       map[string]any{"type": "string"},
							"pattern_flags": map[string]any{"type": "string"},
							"occurrence":    map[string]any{"type": "integer"},
							"new_content":   map[string]any{"type": "string"},
							"indent_mode": map[string]any{
								"type": "string",
								"enum": []string{"preserve", "normalize", "as-is"},
							},
						},
					},
				},
			},
			"required": []string{"path"},
		},
	}
}

// editFileParams holds the parsed input for EditFileTool. It describes both a
// single edit (flat fields) and a batch of edits (Edits); a batch element uses
// the same fields, minus Path and Edits (nested batches are not supported).
type editFileParams struct {
	Path         string           `json:"path"`
	Operation    string           `json:"operation"`
	OldString    string           `json:"old_string"`
	NewString    string           `json:"new_string"`
	StartLine    int              `json:"start_line"`
	EndLine      int              `json:"end_line"`
	Pattern      string           `json:"pattern"`
	PatternFlags string           `json:"pattern_flags"`
	Occurrence   int              `json:"occurrence"`
	NewContent   string           `json:"new_content"`
	IndentMode   string           `json:"indent_mode"`
	Edits        []editFileParams `json:"edits"`
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

	// A batch of edits takes precedence over the flat fields: all edits apply
	// in order against the in-memory content and the file is written once,
	// so a failing edit leaves the file untouched.
	if len(p.Edits) > 0 {
		return t.executeMulti(resolvedPath, originalPath, p)
	}

	// The schema advertises `operation: "replace"` as a convenience alias for
	// the classic `old_string`/`new_string` search/replace. Route it through the
	// same implementation, requiring both fields.
	if p.Operation == string(OpReplace) || p.OldString != "" {
		// All-or-nothing guard (bugs.md 2026-08-26): reject a lost/empty
		// replacement up-front — the empty string used to silently delete
		// the matched block while reporting success.
		if err := validateReplacePair(p.OldString, p.NewString); err != nil {
			return "", err
		}
		return t.searchReplace(resolvedPath, originalPath, p.OldString, p.NewString, t.AllowFuzz)
	}

	return t.editByOperation(resolvedPath, originalPath, p)
}

func (t *EditFileTool) editByOperation(resolvedPath, originalPath string, p editFileParams) (string, error) {
	op := EditOperation(p.Operation)
	if op == "" {
		return "", errMissingParam()
	}

	lines, targetPath, fuzzyNote, _, err := t.readLines(resolvedPath, originalPath)
	if err != nil {
		return "", err
	}

	content, contentNote, err := resolveOpContent(op, p)
	if err != nil {
		return "", err
	}

	// Use NewContent verbatim. JSON unmarshalling already resolved every
	// escape sequence the model intended: a real newline arrived as JSON "\n",
	// a literal backslash+n (e.g. a Go/Python source escape such as "\n")
	// arrived as JSON "\\n". Re-interpreting escapes here would silently
	// corrupt any code that legitimately contains backslash escapes — which is
	// what drove models to abandon `edit` for bash/python when editing files
	// full of regex/string escapes (see session 1784126185).
	ep := editParams{
		startLine:    p.StartLine,
		endLine:      p.EndLine,
		pattern:      p.Pattern,
		patternFlags: p.PatternFlags,
		occurrence:   p.Occurrence,
		newLines:     splitLines(content),
		indentMode:   IndentMode(defaultStr(p.IndentMode, string(IndentPreserve))),
	}

	result, affected, opErr := t.runOp(lines, op, ep)
	if opErr != nil {
		return "", wrapEditOpError(opErr, p.Path, string(op))
	}

	diagBlock, writeErr := t.writeEditResult(targetPath, p.Path, strings.Join(result, "\n"))
	if writeErr != nil {
		return "", writeErr
	}

	// Generate unified diff for the change so the renderer can display it.
	diff := generateUnifiedDiff(lines, result)

	var resultMsg string
	if op == OpReplaceLines {
		// affected = removed line count (see replaceLines); ep.newLines = inserted.
		// Reporting both makes a mismatched edit (e.g. replacement vanished)
		// visible at a glance instead of hiding behind "0 lines affected".
		resultMsg = fmt.Sprintf("[edit: %s] %s — replaced %d lines with %d\n%s", p.Path, op, affected, len(ep.newLines), diff)
	} else {
		resultMsg = fmt.Sprintf("[edit: %s] %s — %d lines affected\n%s", p.Path, op, affected, diff)
	}
	if contentNote != "" {
		resultMsg = contentNote + resultMsg
	}
	if fuzzyNote != "" {
		resultMsg = fuzzyNote + "\n" + resultMsg
	}
	if diagBlock != "" {
		resultMsg += diagBlock
	}
	return resultMsg, nil
}

// executeMulti applies a batch of edits to one file atomically: every edit is
// applied in order against the in-memory content (each edit sees the result of
// the previous ones), and the file is written exactly once at the end. If any
// edit fails, nothing is written and the error identifies the failing edit.
func (t *EditFileTool) executeMulti(resolvedPath, originalPath string, p editFileParams) (string, error) {
	originalLines, targetPath, fuzzyNote, trailingNL, err := t.readLines(resolvedPath, originalPath)
	if err != nil {
		return "", err
	}

	lines := originalLines
	var matchTypes []MatchType
	for i, e := range p.Edits {
		newLines, mt, opErr := t.applySingleEdit(lines, e)
		if opErr != nil {
			// lines still holds the content at the point of failure; the
			// wrapper uses it for the line-match diagnostic.
			return "", t.wrapMultiEditError(opErr, p.Path, i, len(p.Edits), e, lines)
		}
		lines = newLines
		if mt != "" {
			matchTypes = append(matchTypes, mt)
		}
	}

	// Preserve the file's trailing newline: splitLines drops the final empty
	// element, so a verbatim join would silently strip it.
	output := strings.Join(lines, "\n")
	if trailingNL && output != "" && !strings.HasSuffix(output, "\n") {
		output += "\n"
	}
	diagBlock, writeErr := t.writeEditResult(targetPath, p.Path, output)
	if writeErr != nil {
		return "", writeErr
	}

	// One diff from the original content to the final content: the renderer
	// shows the net effect of the whole batch.
	diff := generateUnifiedDiff(originalLines, lines)
	resultMsg := fmt.Sprintf("[edit: %s] %d edits applied\n%s", p.Path, len(p.Edits), diff)
	if mt := combinedMatchDesc(matchTypes); mt != "" {
		resultMsg = fmt.Sprintf("[edit: %s] %d edits applied — match: %s\n%s", p.Path, len(p.Edits), mt, diff)
	}
	if fuzzyNote != "" {
		resultMsg = fuzzyNote + "\n" + resultMsg
	}
	if diagBlock != "" {
		resultMsg += diagBlock
	}
	return resultMsg, nil
}

// applySingleEdit applies one edit command to the in-memory content and
// returns the resulting lines. For a search/replace it also reports the match
// type; line/pattern operations report an empty match type.
func (t *EditFileTool) applySingleEdit(lines []string, e editFileParams) ([]string, MatchType, error) {
	// Classic search/replace (or the "replace" alias) works on the joined text
	// so the fuzzy matcher sees exactly what the single-edit path sees.
	if e.Operation == string(OpReplace) || e.OldString != "" {
		// Same all-or-nothing guard as the flat single-edit path.
		if err := validateReplacePair(e.OldString, e.NewString); err != nil {
			return nil, "", err
		}
		res, err := fuzzyEdit(strings.Join(lines, "\n"), e.OldString, e.NewString, t.AllowFuzz)
		if err != nil {
			return nil, "", err
		}
		return splitLines(res.NewContent), res.MatchType, nil
	}

	op := EditOperation(e.Operation)
	if op == "" {
		return nil, "", errMissingParam()
	}
	content, _, err := resolveOpContent(op, e)
	if err != nil {
		return nil, "", err
	}
	ep := editParams{
		startLine:    e.StartLine,
		endLine:      e.EndLine,
		pattern:      e.Pattern,
		patternFlags: e.PatternFlags,
		occurrence:   e.Occurrence,
		newLines:     splitLines(content),
		indentMode:   IndentMode(defaultStr(e.IndentMode, string(IndentPreserve))),
	}
	result, _, err := t.runOp(lines, op, ep)
	return result, "", err
}

// wrapMultiEditError annotates a batch failure with the 1-indexed position of
// the failing edit and stresses the atomicity guarantee: no partial edit was
// persisted. content is the in-memory content at the point of failure (with
// the earlier edits already applied); it powers the same line-match diagnostic
// the single-edit search/replace path attaches to not-found errors.
func (t *EditFileTool) wrapMultiEditError(err error, path string, idx, batchSize int, e editFileParams, content []string) error {
	desc := e.Operation
	if desc == "" && e.OldString != "" {
		desc = string(OpReplace)
	}
	if desc == "" {
		desc = "(missing operation)"
	}
	prefix := fmt.Sprintf("edit %d/%d (%s)", idx+1, batchSize, desc)

	var te *internal.ToolError
	switch {
	case errors.Is(err, ErrAmbiguous), errors.Is(err, ErrNotFound),
		errors.Is(err, ErrNoChange), errors.Is(err, ErrEmptyOldStr):
		// fuzzyEdit sentinel errors get the same rich mapping as the
		// single-edit path (line-match counts, drift hints).
		matched, total := countMatchingLines(strings.Join(content, "\n"), e.OldString)
		te = t.searchReplaceError(path, e.OldString, err, matched, total)
	default:
		if toolErr, ok := err.(*internal.ToolError); ok {
			te = toolErr
		} else {
			te = &internal.ToolError{Tool: "edit", Type: "operation_failed", Detail: err.Error()}
		}
	}
	te.Detail = fmt.Sprintf("%s: %s — no changes were written; fix the failing edit and retry the whole batch", prefix, te.Detail)
	if te.HintText == "" {
		te.HintText = "Use 'read' to verify the file content and operation parameters, then retry."
	}
	return te
}

// combinedMatchDesc summarizes the match types of the search/replace edits in
// a batch: empty when none ran, the single match type when all agree, or
// "mixed" otherwise.
func combinedMatchDesc(types []MatchType) string {
	if len(types) == 0 {
		return ""
	}
	all := true
	for _, mt := range types[1:] {
		if mt != types[0] {
			all = false
			break
		}
	}
	if !all {
		return "mixed"
	}
	return matchTypeDesc(types[0])
}

func matchTypeDesc(mt MatchType) string {
	switch mt {
	case MatchTrailingWhitespace:
		return "trailing whitespace normalized"
	case MatchFuzzy:
		return "fuzzy whitespace match (indentation auto-adjusted)"
	default:
		return "exact match"
	}
}

// writeEditResult stages a backup, persists the new content in a single
// write, and fires the change notifiers. It is the one place every successful
// edit path (single or batch) goes through, so the file is always written
// atomically per tool call. The content string is written verbatim: callers
// decide how to render their line-based results (and whether to preserve the
// file's trailing newline).
func (t *EditFileTool) writeEditResult(targetPath, displayPath, content string) (string, error) {
	if t.BackupStager != nil {
		t.BackupStager.StageBeforeEdit(targetPath, t.ProjectDir)
	}
	if err := os.WriteFile(targetPath, []byte(content), 0644); err != nil {
		return "", t.errWrite(displayPath, err)
	}
	if t.FileChangeNotifier != nil {
		t.FileChangeNotifier(targetPath)
	}
	return t.notifyLSP(context.Background(), targetPath), nil
}

// validateReplacePair enforces the all-or-nothing contract for classic
// search/replace (bugs.md 2026-08-26): both sides must be present. An empty
// new_string used to be applied verbatim, silently DELETING the matched block
// and reporting success — exactly the reported "edit deleted the old block
// but the replacement content wasn't inserted" failure. Deliberate content
// removal belongs to operation delete_lines, never to an empty replace field.
func validateReplacePair(oldStr, newStr string) error {
	if oldStr == "" {
		return &internal.ToolError{Tool: "edit", Type: "missing_parameter",
			Detail:   "operation 'replace' requires 'old_string' and 'new_string'",
			HintText: "Provide the text to search for in 'old_string' and the replacement in 'new_string'."}
	}
	if newStr == "" {
		return &internal.ToolError{Tool: "edit", Type: "missing_parameter",
			Detail:   "Empty 'new_string': this edit would DELETE the matched block without inserting anything.",
			HintText: "Provide the replacement text in 'new_string'. To remove lines deliberately, use operation 'delete_lines' with start_line/end_line."}
	}
	return nil
}

// opRequiresContent reports whether the operation needs replacement content
// (new_content/new_string) to be meaningful. delete_lines and the classic
// old_string/new_string replace are excluded: the former deletes by design,
// the latter is routed before editByOperation (and guarded by
// validateReplacePair).
//
// replace_pattern IS included (bugs.md 2026-08-26): without this, an edit
// with neither replacement field replaced every matched line with an empty
// insertion — matched lines vanished while the tool reported success (even
// "0 lines affected"), silently breaking files. Content-requiring ops fail
// up-front instead; nothing is mutated unless a real replacement lands.
func opRequiresContent(op EditOperation) bool {
	switch op {
	case OpReplaceLines, OpReplacePattern, OpInsertAfter, OpInsertBefore:
		return true
	}
	return false
}

// resolveOpContent returns the replacement content for a line/pattern op.
// Models frequently conflate new_string (classic search/replace) with
// new_content (line/pattern ops); for content-requiring ops it falls back to
// new_string so the edit applies the intended content instead of silently
// deleting the target range (session 1784574228: replace_lines with only
// new_string deleted lines 116-127 and reported "0 lines affected"). When the
// op requires content and neither field is set, it returns a
// missing_parameter error rather than letting a no-op edit through.
func resolveOpContent(op EditOperation, p editFileParams) (string, string, error) {
	content := p.NewContent
	if content == "" && p.NewString != "" && opRequiresContent(op) {
		return p.NewString, "Note: used new_string as replacement content (new_content was empty)\n", nil
	}
	if opRequiresContent(op) && content == "" {
		return "", "", &internal.ToolError{
			Tool: "edit", Type: "missing_parameter",
			Detail:   fmt.Sprintf("operation '%s' requires 'new_content' (or 'new_string') with the replacement text", p.Operation),
			HintText: "Provide the replacement content in 'new_content'. To delete lines without replacement, use operation 'delete_lines'.",
		}
	}
	return content, "", nil
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

// MutatesState reports that a successful edit changes file state. The loop
// guardrails treat it as a state mutation that resets the no-progress repeat
// horizon (so edit→test→edit cycles never trip the loop detector).
func (t *EditFileTool) MutatesState() bool { return true }

//go:embed editfile.short.md editfile.long.md
var editfileDocs embed.FS

// notifyLSP forwards the edited document to its language server (any file
// type the manager supports — Issue LSP: not just.go) and returns a
// formatted diagnostics block for the tool result. The notification never
// blocks on a server start (async spawn); diagnostics appear once the server
// is up and has processed the change.
func (t *EditFileTool) notifyLSP(ctx context.Context, resolvedPath string) string {
	if t.LSPManager == nil || t.LSPManager.ServerIDFor(resolvedPath) == "" {
		return ""
	}
	content, err := os.ReadFile(resolvedPath)
	if err != nil {
		return ""
	}
	_ = t.LSPManager.DidChange(ctx, resolvedPath, string(content))
	// Diagnostics are published asynchronously; poll until they settle (L1).
	diags := collectLSPDiagnostics(ctx, t.LSPManager, resolvedPath)
	return formatLSPDiagnostics(resolvedPath, diags, t.LSPManager.ServerIDFor(resolvedPath))
}

func (t *EditFileTool) ShortDoc() string { return readDoc(editfileDocs, "editfile.short.md") }
func (t *EditFileTool) LongDoc() string  { return readDoc(editfileDocs, "editfile.long.md") }

func (t *EditFileTool) Examples() []string {
	return []string{
		`{"path": "src/main.go", "old_string": "fmt.Println(\"hello\")", "new_string": "fmt.Println(\"world\")"}`,
		`{"path": "auth.go", "old_string": "func oldName()", "new_string": "func newName()"}`,
		`{"path": "src/main.go", "operation": "replace_lines", "start_line": 5, "end_line": 8, "new_content": "func main() {\n\tlog.Println(\"start\")\n}"}`,
		`{"path": "src/main.go", "edits": [{"old_string": "import \"fmt\"", "new_string": "import (\n\t\"fmt\"\n\t\"log\"\n)"}, {"old_string": "fmt.Println(\"hi\")", "new_string": "log.Println(\"hi\")"}]}`,
	}
}

// readLines loads the target file and returns its lines, the resolved target
// path, a fuzzy-filename note (when the requested path did not exist), and
// whether the file ends with a newline — callers that rewrite the file need
// that to avoid silently stripping it (splitLines drops the final empty
// element).
func (t *EditFileTool) readLines(resolvedPath, originalPath string) ([]string, string, string, bool, error) {
	targetPath, data, err := ReadFileWithFuzzyFallback(t.Config, resolvedPath, originalPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, "", "", false, &internal.ToolError{Tool: "edit", Type: "file_not_found",
				Detail:   fmt.Sprintf("File not found: %s", originalPath),
				HintText: "Check the path or use write to create the file first."}
		}
		return nil, "", "", false, &internal.ToolError{Tool: "edit", Type: "read_error",
			Detail:   fmt.Sprintf("Cannot read %s: %v", originalPath, err),
			HintText: "Ensure the file exists and is readable."}
	}
	var fuzzyNote string
	if targetPath != resolvedPath {
		fuzzyNote = fmt.Sprintf("Note: file not found, used closest match: %s", targetPath)
	}
	return splitLines(string(data)), targetPath, fuzzyNote, strings.HasSuffix(string(data), "\n"), nil
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
		matched, total := countMatchingLines(string(data), oldStr)
		return "", t.searchReplaceError(originalPath, oldStr, err, matched, total)
	}

	diagBlock, writeErr := t.writeEditResult(targetPath, originalPath, result.NewContent)
	if writeErr != nil {
		return "", writeErr
	}

	// Build a clear result message
	matchDesc := matchTypeDesc(result.MatchType)

	resultMsg := fmt.Sprintf("[edit: %s] search/replace applied — lines %d-%d, match: %s\n%s",
		originalPath, result.StartLine, result.EndLine, matchDesc, result.Diff)
	if diagBlock != "" {
		resultMsg += diagBlock
	}
	if targetPath != resolvedPath {
		resultMsg = fmt.Sprintf("Note: file not found, used closest match: %s\n%s", targetPath, resultMsg)
	}
	return resultMsg, nil
}

func (t *EditFileTool) searchReplaceError(path, oldStr string, err error, matched, total int) *internal.ToolError {
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
		detail := fmt.Sprintf(message, truncateStr(oldStr, 40), path)
		// surface how much of the block actually matched so the model
		// understands this is content drift (not a broken tool) and recovers by
		// re-reading + making a smaller anchored edit, instead of switching to bash.
		if total > 0 {
			detail += fmt.Sprintf(" — %d/%d lines of old_string matched the current file", matched, total)
		}
		return &internal.ToolError{Tool: "edit", Type: "not_found",
			Detail:   detail,
			HintText: "The file has drifted from your last read (see the line-match count above). Re-read the target region with 'read' first, then retry with a SMALLER edit: fewer lines and a tight unique anchor. For multi-line or drifted blocks prefer 'operation: replace_lines' or 'delete_lines' with start_line/end_line (immune to content drift). Do NOT use bash/node/python to edit the file — always use this edit tool."}
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
