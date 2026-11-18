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
	OpDelete         EditOperation = "delete"
)

type IndentMode string

const (
	IndentPreserve  IndentMode = "preserve"
	IndentNormalize IndentMode = "normalize"
	IndentAsIs      IndentMode = "as-is"
)

// defaultIndentMode resolves the indent mode for an operation. An explicit
// caller-supplied value always wins. insert_after/insert_before/replace_lines
// default to as-is: caller content must land byte-for-byte, because padding it
// to the target line's indent silently corrupts semantic-whitespace formats
// such as Markdown (Issue 4). replace_pattern keeps the legacy preserve default
// — its block path re-indents the whole matched region rather than a lone
// insertion, and callers rely on that.
func defaultIndentMode(op EditOperation, raw string) IndentMode {
	if raw != "" {
		return IndentMode(raw)
	}
	switch op {
	case OpInsertAfter, OpInsertBefore, OpReplaceLines:
		return IndentAsIs
	}
	return IndentPreserve
}

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

	// writeFile is an optional filesystem fault-injection seam. Production
	// writes use WriteFileVerified; tests can force a mid-batch failure to prove
	// independent writes and atomic rollback.
	writeFile func(path string, content []byte, perm os.FileMode) error
}

func (t *EditFileTool) Schema() agentic.ToolSchema {
	return agentic.ToolSchema{
		Name:        "edit",
		Description: "Edit files: search/replace.",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":       map[string]any{"type": "string"},
				"old_string": map[string]any{"type": "string"},
				"new_string": map[string]any{"type": "string"},
				"operation": map[string]any{
					"type": "string",
					"enum": []string{"replace", "replace_lines", "replace_pattern", "insert_after", "insert_before", "delete_lines", "delete"},
				},
				"start_line": map[string]any{"type": "integer"},
				"end_line":   map[string]any{"type": "integer"},
				"pattern": map[string]any{
					"type":        "string",
					"description": "regex for pattern ops (invalid regex is matched literally)",
				},
				"pattern_flags": map[string]any{
					"type":        "string",
					"description": "regex flags (e.g. 'i')",
				},
				"occurrence": map[string]any{
					"type":        "integer",
					"description": "Nth match (string) or Nth matching line (pattern)",
				},
				"replace_all": map[string]any{
					"type":        "boolean",
					"description": "apply to every non-overlapping match",
				},
				"atomic": map[string]any{
					"type":        "boolean",
					"description": "multi-file only: all-or-nothing with rollback",
				},
				"new_content": map[string]any{
					"type":        "string",
					"description": "line-op replacement; \"\" is valid (one empty line)",
				},
				"indent_mode": map[string]any{
					"type":        "string",
					"description": "as-is for replace_lines/insert_after/insert_before, preserve otherwise",
					"enum":        []string{"preserve", "normalize", "as-is"},
				},
				"edits": map[string]any{
					"type":        "array",
					"description": "One file, ordered and atomic. Line shifts + later absolute lines = stale_line_numbers; re-harvest.",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							// Field docs intentionally omitted: names/types/enums mirror the
							// flat single-edit properties documented above (context budget).
							"operation": map[string]any{
								"type": "string",
								"enum": []string{"replace", "replace_lines", "replace_pattern", "insert_after", "insert_before", "delete_lines", "delete"},
							},
							"path":          map[string]any{"type": "string"}, // optional; fallback when top-level path is omitted
							"old_string":    map[string]any{"type": "string"},
							"new_string":    map[string]any{"type": "string"},
							"start_line":    map[string]any{"type": "integer"},
							"end_line":      map[string]any{"type": "integer"},
							"pattern":       map[string]any{"type": "string"},
							"pattern_flags": map[string]any{"type": "string"},
							"occurrence":    map[string]any{"type": "integer"},
							"replace_all":   map[string]any{"type": "boolean"},
							"new_content":   map[string]any{"type": "string"},
							"indent_mode": map[string]any{
								"type": "string",
								"enum": []string{"preserve", "normalize", "as-is"},
							},
						},
					},
				},
				"files": map[string]any{
					"type":        "array",
					"description": "Multi-file targets; independent unless atomic:true.",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"path":  map[string]any{"type": "string"},
							"edits": map[string]any{"type": "array"},
						},
						"required": []string{"path", "edits"},
					},
				},
				"glob": map[string]any{
					"type":        "string",
					"description": "Apply top-level edits to matches (no '**'); zero matches fail.",
				},
			},
			"required": []string{},
		},
	}
}

// editFileParams holds the parsed input for EditFileTool. It describes both a
// single edit (flat fields) and a batch of edits (Edits); a batch element uses
// the same fields, minus Edits (nested batches are not supported). An entry's
// Path is only a fallback for an omitted top-level path (see batchEntryPath)
// and must agree with it (see checkEntryPaths).
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
	ReplaceAll   bool             `json:"replace_all"`
	Atomic       *bool            `json:"atomic"`
	NewContent   string           `json:"new_content"`
	IndentMode   string           `json:"indent_mode"`
	Edits        []editFileParams `json:"edits"`
	// Multi-file batch form (bugs.md B40): either explicit per-file targets
	// or a glob scope sharing the top-level edits. Mutually exclusive with
	// each other, with "path" and with the single-file "edits" batch.
	Files []editFileTarget `json:"files"`
	Glob  string           `json:"glob"`

	// NewContentSet records whether the caller SENT "new_content", as opposed
	// to sending it empty or omitting it. It is not serialized: the wire
	// format stays a plain string. Only UnmarshalJSON sets it, so a value
	// decoded from JSON carries presence information while a struct built in
	// Go keeps the zero value (see resolveOpContent).
	NewContentSet bool `json:"-"`
}

// editFileAlias is editFileParams minus its methods: embedding it in the wire
// type below makes every other field decode exactly as before (including the
// Edits recursion, whose elements are editFileParams) while leaving room for
// a presence-tracking new_content pointer at the shallower depth, which wins
// over the alias's own new_content field.
type editFileAlias editFileParams

// editFileWire decodes editFileParams while tracking field presence. The
// embedded alias contributes every field except new_content (shadowed by the
// pointer, which sits at a shallower depth); the pointer then distinguishes an
// explicit "" from an omitted field. A batch element is an editFileParams, so
// nested edits recurse through UnmarshalJSON automatically.
type editFileWire struct {
	editFileAlias
	NewContent *string         `json:"new_content"`
	Atomic     json.RawMessage `json:"atomic"`
	Edits      json.RawMessage `json:"edits"`
}

// UnmarshalJSON implements json.Unmarshaler for editFileParams: an explicitly
// empty "new_content": "" is a deliberate write of one empty line (Issue 5)
// and must survive decoding, while an omitted field must stay distinguishable
// from it so the d3f8416 deletion guard still rejects lost payloads.
func (p *editFileParams) UnmarshalJSON(data []byte) error {
	var w editFileWire
	if err := json.Unmarshal(data, &w); err != nil {
		return err
	}
	*p = editFileParams(w.editFileAlias)
	if w.NewContent != nil {
		p.NewContent = *w.NewContent
		p.NewContentSet = true
	}
	if w.Edits != nil {
		edits, err := decodeEditFileBatch(w.Edits)
		if err != nil {
			return err
		}
		p.Edits = edits
	}
	if w.Atomic != nil {
		var atomic *bool
		if err := json.Unmarshal(w.Atomic, &atomic); err != nil || atomic == nil {
			return fmt.Errorf("atomic must be true or false, not null or another type")
		}
		p.Atomic = atomic
	}
	return nil
}

// decodeEditFileBatch accepts native arrays and one compatibility form used
// by some OpenAI-compatible models: a string containing one JSON array. Nested
// string encoding remains invalid; no generic repair or recursive decoding is
// performed.
func decodeEditFileBatch(raw json.RawMessage) ([]editFileParams, error) {
	var edits []editFileParams
	arrayErr := json.Unmarshal(raw, &edits)
	if arrayErr == nil {
		return edits, nil
	}
	var encoded string
	if err := json.Unmarshal(raw, &encoded); err != nil {
		return nil, arrayErr
	}
	trimmed := strings.TrimSpace(encoded)
	if trimmed == "" || !strings.HasPrefix(trimmed, "[") {
		return nil, fmt.Errorf("edits string must contain one JSON array")
	}
	if err := json.Unmarshal([]byte(trimmed), &edits); err != nil {
		return nil, fmt.Errorf("edits string must contain one JSON array: %w", err)
	}
	return edits, nil
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
	if err := validateSingleFileAtomic(p); err != nil {
		return "", err
	}
	// Multi-file forms are independent by default; atomic:true preflights every
	// target and restores attempted writes if any write fails.
	if out, isMulti, err := t.executeMultiFileForm(p); isMulti {
		return out, err
	}

	// Models commonly nest "path" inside every edits[] element instead of
	// repeating it top-level (session 1789212854, export
	// goa-export-20260912-135102: two batches failed with missing_path while
	// carrying the path in each entry). Fall back to the entry path rather
	// than erroring on a technically-present path; a batch still targets
	// exactly one file, so conflicting entries are rejected.
	if p.Path == "" {
		p.Path = batchEntryPath(p.Edits)
		if p.Path == "" {
			return "", errMissingPath()
		}
	}
	if err := checkEntryPaths(p.Path, p.Edits); err != nil {
		return "", err
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

	if isStringEdit(p) {
		if err := validateStringEdit(p); err != nil {
			return "", err
		}
		return t.searchReplace(resolvedPath, originalPath, p)
	}
	if err := validateNoReplaceAllForLineOp(p); err != nil {
		return "", err
	}
	return t.editByOperation(resolvedPath, originalPath, p)
}

func (t *EditFileTool) editByOperation(resolvedPath, originalPath string, p editFileParams) (string, error) {
	op := EditOperation(p.Operation)
	if op == "" {
		return "", errMissingParam()
	}

	lines, targetPath, fuzzyNote, trailingNL, err := t.readLines(resolvedPath, originalPath)
	if err != nil {
		return "", err
	}

	newLines, contentNote, err := resolveOpContent(op, p)
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
		newLines:     newLines,
		indentMode:   defaultIndentMode(op, p.IndentMode),
	}

	result, affected, opErr := t.runOp(lines, op, ep)
	if opErr != nil {
		return "", wrapEditOpError(opErr, p.Path, string(op))
	}

	output := strings.Join(result, "\n")
	if trailingNL && output != "" && !strings.HasSuffix(output, "\n") {
		output += "\n"
	}
	diagBlock, writeErr := t.writeEditResult(targetPath, p.Path, output)
	if writeErr != nil {
		return "", writeErr
	}

	// Generate unified diff for the change so the renderer can display it.
	diff := generateUnifiedDiff(lines, result)
	resultMsg := formatEditResult(p.Path, op, affected, len(ep.newLines), resolvedPathNote(targetPath, p.Path), diff, contentNote)
	if fuzzyNote != "" {
		resultMsg = fuzzyNote + "\n" + resultMsg
	}
	if diagBlock != "" {
		resultMsg += diagBlock
	}
	return resultMsg, nil
}

// formatEditResult renders the single-edit result message: the operation
// summary (replace_lines reports both the removed and the inserted line count
// so a mismatched edit is visible at a glance instead of hiding behind "0
// lines affected"), the resolved-path note when the write landed somewhere
// other than the requested path, the unified diff, and the content note
// explaining a new_string fallback or an explicit empty new_content. The caller
// prepends fuzzy notes and appends LSP diagnostics, which it owns.
func formatEditResult(path string, op EditOperation, affected, inserted int, note, diff, contentNote string) string {
	if op == OpReplaceLines {
		return contentNote + fmt.Sprintf("[edit: %s] %s — replaced %d lines with %d%s\n%s", path, op, affected, inserted, note, diff)
	}
	return contentNote + fmt.Sprintf("[edit: %s] %s — %d lines affected%s\n%s", path, op, affected, note, diff)
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

	// Pre-flight drift guard (Issue 2): absolute line numbers harvested before
	// the batch go stale the moment an earlier entry changes the line count.
	// Rejecting here — before the apply loop and the single write — keeps the
	// file byte-identical (see editfile_batchguard.go).
	if err := checkBatchLineDrift(p.Edits); err != nil {
		return "", err
	}
	lines, matchTypes, failIdx, applyErr := t.applyBatchToLines(originalLines, p.Edits)
	if applyErr != nil {
		// lines still holds the content at the point of failure; the
		// wrapper uses it for the line-match diagnostic.
		return "", t.wrapMultiEditError(applyErr, p.Path, failIdx, len(p.Edits), p.Edits[failIdx], lines)
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
	return formatMultiEditResult(p.Path, len(p.Edits), matchTypes, resolvedPathNote(targetPath, p.Path), diff, fuzzyNote, diagBlock), nil
}

// formatMultiEditResult renders the batch result message: the summary line
// (naming the combined match tier when any content edit ran, plus the
// resolved-path note when the write landed somewhere other than the requested
// path), the net diff, and the optional fuzzy-match note and LSP diagnostics
// block.
func formatMultiEditResult(path string, count int, matchTypes []MatchType, note, diff, fuzzyNote, diagBlock string) string {
	resultMsg := fmt.Sprintf("[edit: %s] %d edits applied%s\n%s", path, count, note, diff)
	if mt := combinedMatchDesc(matchTypes); mt != "" {
		resultMsg = fmt.Sprintf("[edit: %s] %d edits applied — match: %s%s\n%s", path, count, mt, note, diff)
	}
	if fuzzyNote != "" {
		resultMsg = fuzzyNote + "\n" + resultMsg
	}
	if diagBlock != "" {
		resultMsg += diagBlock
	}
	return resultMsg
}

// applySingleEdit applies one edit command to the in-memory content and
// returns the resulting lines. For a search/replace it also reports the match
// type; line/pattern operations report an empty match type.
func (t *EditFileTool) applySingleEdit(lines []string, e editFileParams) ([]string, MatchType, error) {
	if isStringEdit(e) {
		if err := validateStringEdit(e); err != nil {
			return nil, "", err
		}
		res, err := fuzzyEditSelected(strings.Join(lines, "\n"), e.OldString, e.NewString, t.AllowFuzz,
			editSelection{occurrence: e.Occurrence, replaceAll: e.ReplaceAll, deleteMatch: EditOperation(e.Operation) == OpDelete})
		if err != nil {
			return nil, "", err
		}
		return splitLines(res.NewContent), res.MatchType, nil
	}
	if err := validateNoReplaceAllForLineOp(e); err != nil {
		return nil, "", err
	}

	op := EditOperation(e.Operation)
	if op == "" {
		return nil, "", errMissingParam()
	}
	newLines, _, err := resolveOpContent(op, e)
	if err != nil {
		return nil, "", err
	}
	ep := editParams{
		startLine:    e.StartLine,
		endLine:      e.EndLine,
		pattern:      e.Pattern,
		patternFlags: e.PatternFlags,
		occurrence:   e.Occurrence,
		newLines:     newLines,
		indentMode:   defaultIndentMode(op, e.IndentMode),
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
		errors.Is(err, ErrNoChange), errors.Is(err, ErrEmptyOldStr), errors.Is(err, ErrOccurrenceNotFound):
		// fuzzyEdit sentinel errors get the same rich mapping as the
		// single-edit path (line-match counts, drift hints).
		bm := analyzeBlockMatch(strings.Join(content, "\n"), e.OldString)
		te = t.searchReplaceError(path, e.OldString, err, bm)
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
	case MatchExactSubstring:
		return "exact substring match"
	default:
		return "exact match"
	}
}

// writeEditResult stages a backup, persists the new content in a single
// verified write, and fires the change notifiers. It is the one place every
// successful edit path (single or batch) goes through, so the file is always
// written atomically per tool call and always read back before the edit reports
// success (Issue 3: a success must mean the bytes are on disk). The content
// string is written verbatim: callers decide how to render their line-based
// results (and whether to preserve the file's trailing newline).
//
// A read-back mismatch is reported as write_verify_failed — never as a
// successful edit — because the model must not build further edits on content
// that was never persisted.
func (t *EditFileTool) writeEditResult(targetPath, displayPath, content string) (string, error) {
	if t.BackupStager != nil {
		t.BackupStager.StageBeforeEdit(targetPath, t.ProjectDir)
	}
	data := []byte(content)
	if err := t.writeVerified(targetPath, data, 0644); err != nil {
		if verifyFailed(err) {
			return "", writeVerifyFailedError("edit", targetPath, len(data), err)
		}
		return "", t.errWrite(displayPath, err)
	}
	if t.FileChangeNotifier != nil {
		t.FileChangeNotifier(targetPath)
	}
	return t.notifyLSP(context.Background(), targetPath), nil
}

func (t *EditFileTool) writeVerified(path string, data []byte, perm os.FileMode) error {
	if t.writeFile != nil {
		return t.writeFile(path, data, perm)
	}
	return WriteFileVerified(path, data, perm)
}

// validateReplacePair enforces the all-or-nothing contract for classic
// search/replace (bugs.md 2026-08-26): both sides must be present. An empty
// new_string used to be applied verbatim, silently DELETING the matched block
// and reporting success — exactly the reported "edit deleted the old block
// but the replacement content wasn't inserted" failure. Deliberate content
// removal belongs to operation delete (or delete_lines), never to an empty replace field.
func validateReplacePair(oldStr, newStr string) error {
	if oldStr == "" {
		return &internal.ToolError{Tool: "edit", Type: "missing_parameter",
			Detail:   "operation 'replace' requires 'old_string' and 'new_string'",
			HintText: "Provide the text to search for in 'old_string' and the replacement in 'new_string'."}
	}
	if newStr == "" {
		return &internal.ToolError{Tool: "edit", Type: "missing_parameter",
			Detail:   "Empty 'new_string': this edit would DELETE the matched block without inserting anything.",
			HintText: "Provide the replacement text in 'new_string'. To remove text deliberately, use operation 'delete' with old_string (or delete_lines for an exact line range)."}
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

// resolveOpContent returns the replacement LINES for a line/pattern op.
// Models frequently conflate new_string (classic search/replace) with
// new_content (line/pattern ops); for content-requiring ops it falls back to
// new_string so the edit applies the intended content instead of silently
// deleting the target range (session 1784574228: replace_lines with only
// new_string deleted lines 116-127 and reported "0 lines affected"). When the
// op requires content and neither field is set, it returns a
// missing_parameter error rather than letting a no-op edit through.
//
// An explicitly empty new_content (NewContentSet, i.e. the JSON carried
// "new_content": "") is NOT a lost payload: it asks for one empty line
// (Issue 5). splitLines("") is empty — trailing empty element dropped — so the
// single empty line is materialized here instead of derived from the string;
// replacing one line with [""] leaves the line count untouched. Only an
// OMITTED field is the ambiguous case the d3f8416 guard rejects.
func resolveOpContent(op EditOperation, p editFileParams) ([]string, string, error) {
	content := p.NewContent
	if content == "" && p.NewString != "" && opRequiresContent(op) {
		return splitLines(p.NewString), "Note: used new_string as replacement content (new_content was empty)\n", nil
	}
	if opRequiresContent(op) && content == "" {
		if !p.NewContentSet {
			return nil, "", &internal.ToolError{
				Tool: "edit", Type: "missing_parameter",
				Detail:   fmt.Sprintf("operation '%s' requires 'new_content' (or 'new_string') with the replacement text", p.Operation),
				HintText: "Provide the replacement content in 'new_content'. To delete lines without replacement, use operation 'delete_lines'.",
			}
		}
		return []string{""}, "(explicit empty new_content: writing one empty line)\n", nil
	}
	return splitLines(content), "", nil
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
	// Multi-file forms declare every target; a glob is expanded eagerly so
	// concurrent reads/writes on the matched files serialize correctly. (The
	// result only feeds the scheduler's conflict detection — expansion is a
	// read-only scan.) A pattern that cannot expand keeps the pattern itself
	// as the declared path.
	if len(p.Files) > 0 || p.Glob != "" {
		paths := make([]string, 0, len(p.Files))
		for _, f := range p.Files {
			if f.Path != "" {
				paths = append(paths, f.Path)
			}
		}
		if p.Glob != "" {
			if expanded, err := t.expandEditGlobPattern(p.Glob); err == nil {
				paths = append(paths, expanded...)
			} else {
				paths = append(paths, p.Glob)
			}
		}
		return ToolAccess{WritePaths: paths}
	}
	path := p.Path
	if path == "" {
		path = batchEntryPath(p.Edits)
	}
	return ToolAccess{WritePaths: []string{path}}
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
		`{"path": "auth.go", "old_string": "token", "new_string": "name", "occurrence": 2}`,
		`{"path": "auth.go", "operation": "delete", "old_string": "obsolete text"}`,
		`{"path": "src/main.go", "operation": "replace_lines", "start_line": 5, "end_line": 8, "new_content": "func main() {\n\tlog.Println(\"start\")\n}"}`,
		`{"path": "src/main.go", "edits": [{"old_string": "import \"fmt\"", "new_string": "import (\n\t\"fmt\"\n\t\"log\"\n)"}, {"old_string": "fmt.Println(\"hi\")", "new_string": "log.Println(\"hi\")"}]}`,
		`{"files": [{"path": "a.go", "edits": [{"old_string": "oldName", "new_string": "newName"}]}, {"path": "b.go", "edits": [{"old_string": "oldName", "new_string": "newName"}]}]}`,
		`{"glob": "internal/foo/*.go", "edits": [{"old_string": "OldSymbol", "new_string": "NewSymbol"}]}`,
		`{"atomic": true, "glob": "internal/foo/*.go", "edits": [{"old_string": "OldSymbol", "new_string": "NewSymbol"}]}`,
	}
}

// editFileSource keeps the exact bytes and resolved path from one read. Atomic
// batches use the same byte slice for editing and rollback, avoiding a second
// snapshot read that could observe a different file version.
type editFileSource struct {
	targetPath string
	data       []byte
	fuzzyNote  string
}

func (t *EditFileTool) readEditSource(resolvedPath, originalPath string) (editFileSource, error) {
	targetPath, data, err := ReadFileWithFuzzyFallback(t.Config, resolvedPath, originalPath)
	if err != nil {
		if os.IsNotExist(err) {
			return editFileSource{}, &internal.ToolError{Tool: "edit", Type: "file_not_found",
				Detail:   fmt.Sprintf("File not found: %s", originalPath),
				HintText: "Check the path or use write to create the file first."}
		}
		return editFileSource{}, &internal.ToolError{Tool: "edit", Type: "read_error",
			Detail:   fmt.Sprintf("Cannot read %s: %v", originalPath, err),
			HintText: "Ensure the file exists and is readable."}
	}
	source := editFileSource{targetPath: targetPath, data: data}
	if targetPath != resolvedPath {
		source.fuzzyNote = fmt.Sprintf("Note: file not found, used closest match: %s", targetPath)
	}
	return source, nil
}

func (t *EditFileTool) readLines(resolvedPath, originalPath string) ([]string, string, string, bool, error) {
	source, err := t.readEditSource(resolvedPath, originalPath)
	if err != nil {
		return nil, "", "", false, err
	}
	return splitLines(string(source.data)), source.targetPath, source.fuzzyNote, strings.HasSuffix(string(source.data), "\n"), nil
}

func errMissingPath() *internal.ToolError {
	return &internal.ToolError{Tool: "edit", Type: "missing_path",
		Detail:   "No 'path' provided",
		HintText: "Provide the file path in the top-level 'path' field (a batch may carry it inside its edits entries instead)."}
}

// batchEntryPath returns the file path carried by batch entries when the
// top-level 'path' was omitted: models frequently nest "path" inside every
// edits[] element (session 1789212854). Returns "" when no entry provides one.
func batchEntryPath(edits []editFileParams) string {
	for _, e := range edits {
		if e.Path != "" {
			return e.Path
		}
	}
	return ""
}

// checkEntryPaths rejects batch entries whose nested path disagrees with the
// resolved top-level path: one edit call edits exactly one file, so a
// diverging entry is a model error that must surface instead of silently
// editing a file the entry did not name.
func checkEntryPaths(path string, edits []editFileParams) error {
	for i, e := range edits {
		if e.Path != "" && e.Path != path {
			return &internal.ToolError{Tool: "edit", Type: "conflicting_path",
				Detail:   fmt.Sprintf("edit %d/%d targets %q but this batch edits %q: one edit call edits exactly one file", i+1, len(edits), e.Path, path),
				HintText: "Split the batch into one edit call per file, or drop the nested 'path' fields and keep the top-level one."}
		}
	}
	return nil
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
// When allowFuzz is true, uses 3 line-based tiers (exact → trailing whitespace →
// fuzzy) and then, for a single-line oldStr, the additive exact-substring tier.
// When false, uses exact whole-line and exact-substring matching only.
// It reads the file, applies the edit, and writes the result back.
func (t *EditFileTool) searchReplace(resolvedPath, originalPath string, p editFileParams) (string, error) {
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

	result, err := fuzzyEditSelected(string(data), p.OldString, p.NewString, t.AllowFuzz,
		editSelection{occurrence: p.Occurrence, replaceAll: p.ReplaceAll, deleteMatch: EditOperation(p.Operation) == OpDelete})
	if err != nil {
		bm := analyzeBlockMatch(string(data), p.OldString)
		return "", t.searchReplaceError(originalPath, p.OldString, err, bm)
	}

	diagBlock, writeErr := t.writeEditResult(targetPath, originalPath, result.NewContent)
	if writeErr != nil {
		return "", writeErr
	}

	// Build a clear result message
	matchDesc := matchTypeDesc(result.MatchType)

	action := "search/replace applied"
	location := fmt.Sprintf("lines %d-%d", result.StartLine, result.EndLine)
	if EditOperation(p.Operation) == OpDelete {
		action = "delete applied"
	}
	if result.MatchCount > 1 {
		location = fmt.Sprintf("%d matches (lines %d-%d)", result.MatchCount, result.StartLine, result.EndLine)
	}
	resultMsg := fmt.Sprintf("[edit: %s] %s — %s, match: %s%s\n%s",
		originalPath, action, location, matchDesc, resolvedPathNote(targetPath, originalPath), result.Diff)
	if diagBlock != "" {
		resultMsg += diagBlock
	}
	if targetPath != resolvedPath {
		resultMsg = fmt.Sprintf("Note: file not found, used closest match: %s\n%s", targetPath, resultMsg)
	}
	return resultMsg, nil
}

// blockMatchHint renders the drift diagnostic appended to not-found edit
// errors. bm.run (contiguous overlap) is the number that predicts whether the
// block can ever match; bm.anywhere (scattered per-line presence) is reported
// alongside so the model can tell plain line drift apart from an old_string
// that spans non-adjacent regions of the file — the latter must be split into
// one edit per region, not retried with different whitespace.
func blockMatchHint(bm blockMatch) string {
	switch {
	case bm.total == 0:
		return ""
	case bm.run == 0:
		return fmt.Sprintf(" — 0/%d lines of old_string matched the current file", bm.total)
	case bm.run == bm.total:
		return fmt.Sprintf(" — all %d lines of old_string match contiguously at file lines %d-%d, yet no strategy matched: the block differs only in blank lines or whitespace",
			bm.total, bm.runStart, bm.runStart+bm.run-1)
	case bm.anywhere > bm.run:
		return fmt.Sprintf(" — old_string is NOT one contiguous block: best contiguous match is %d/%d lines (file lines %d-%d); %d/%d lines of old_string matched the current file but scattered across non-adjacent regions — split old_string into one edit per region",
			bm.run, bm.total, bm.runStart, bm.runStart+bm.run-1, bm.anywhere, bm.total)
	default:
		return fmt.Sprintf(" — %d/%d lines of old_string matched the current file as one contiguous block (file lines %d-%d); the remaining lines drifted — re-read the region",
			bm.run, bm.total, bm.runStart, bm.runStart+bm.run-1)
	}
}

func (t *EditFileTool) searchReplaceError(path, oldStr string, err error, bm blockMatch) *internal.ToolError {
	switch {
	case errors.Is(err, ErrAmbiguous):
		detail := fmt.Sprintf("Text %q matches multiple locations in %s", truncateStr(oldStr, 40), path)
		var ambiguous *ambiguousMatchError
		if errors.As(err, &ambiguous) {
			detail = fmt.Sprintf("Text %q matches %d locations in %s", truncateStr(oldStr, 40), ambiguous.count, path)
		}
		return &internal.ToolError{Tool: "edit", Type: "ambiguous_match", Detail: detail,
			HintText: "Add context for a unique match, select one with occurrence:N, or apply to every non-overlapping match with replace_all:true."}
	case errors.Is(err, ErrOccurrenceNotFound):
		detail := "Requested occurrence was not found"
		var occurrence *occurrenceMatchError
		if errors.As(err, &occurrence) {
			detail = fmt.Sprintf("old_string matched %d location(s), occurrence %d requested", occurrence.count, occurrence.occurrence)
		}
		return &internal.ToolError{Tool: "edit", Type: "occurrence_not_found", Detail: detail,
			HintText: "Choose occurrence within the reported match count, or use replace_all:true."}
	case errors.Is(err, ErrNotFound):
		message := "Text %q not found in %s (exact match only)"
		if t.AllowFuzz {
			message = "Text %q not found in %s (tried exact, trailing whitespace, and fuzzy matching)"
		}
		detail := fmt.Sprintf(message, truncateStr(oldStr, 40), path)
		// surface how much of the block actually matched so the model
		// understands this is content drift (not a broken tool) and recovers by
		// re-reading + making a smaller anchored edit, instead of switching to bash.
		detail += blockMatchHint(bm)
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
