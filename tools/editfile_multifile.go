// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pijalu/goa/internal"
)

// editFileTarget is one file in the multi-file batch form ("files" array):
// its path plus the edits to apply to it. The edits mirror the top-level
// "edits" items and carry the same per-file guarantees: applied in order,
// atomic within the file, stale-line guard enforced.
type editFileTarget struct {
	Path  string           `json:"path"`
	Edits []editFileParams `json:"edits"`
}

// batchFileStatus is one per-path line in a multi-file result or failure
// report. state is "ok", "fail" or "skip"; detail explains skip/fail; err is
// the typed ToolError behind a fail (drives the aggregate error's type).
type batchFileStatus struct {
	display string
	state   string
	detail  string
	err     *internal.ToolError
}

// resolveBatchForm validates the multi-file top-level shape and returns the
// per-file targets. Exactly one form is allowed:
//
//	{"files": [{"path": ..., "edits": [...]}]}   — explicit per-file targets
//	{"glob": "pkg/*.go", "edits": [...]}         — same edits for every match
//
// Both are mutually exclusive with each other, with the single-file "path",
// and with the flat top-level "edits" (which belongs to the single-file batch
// form). An empty/missing path inside a files entry, or an entry without
// edits, is rejected up-front — before any file is read or written.
func (t *EditFileTool) resolveBatchForm(p editFileParams) ([]editFileTarget, error) {
	switch {
	case len(p.Files) > 0:
		if err := checkBatchExclusivity(p, true); err != nil {
			return nil, err
		}
		return validateFilesForm(p.Files)
	case p.Glob != "":
		if err := checkBatchExclusivity(p, false); err != nil {
			return nil, err
		}
		return t.targetsFromGlob(p.Glob, p.Edits)
	}
	return nil, nil
}

// checkBatchExclusivity rejects forbidden top-level field combinations. The
// files form must not carry path/glob/edits; the glob form must not carry
// path/files and requires top-level edits.
func checkBatchExclusivity(p editFileParams, filesForm bool) error {
	if p.Path != "" {
		return errConflictingEditForm("'path' and the multi-file forms are mutually exclusive: 'files' carries its own per-entry paths, 'glob' expands to its own file set")
	}
	if filesForm {
		if p.Glob != "" {
			return errConflictingEditForm("'files' and 'glob' are mutually exclusive: pick one multi-file form")
		}
		if len(p.Edits) > 0 {
			return errConflictingEditForm("top-level 'edits' belongs to the single-file batch form; put the edits inside each 'files' entry")
		}
		return nil
	}
	if len(p.Files) > 0 {
		return errConflictingEditForm("'files' and 'glob' are mutually exclusive: pick one multi-file form")
	}
	if len(p.Edits) == 0 {
		return &internal.ToolError{Tool: "edit", Type: "missing_parameter",
			Detail:   "The 'glob' form requires the top-level 'edits' array with the edits to apply to every matching file",
			HintText: "Pass the shared edits in top-level 'edits', e.g. {\"glob\": \"pkg/*.go\", \"edits\": [{\"old_string\": ..., \"new_string\": ...}]}"}
	}
	return nil
}

// validateFilesForm checks each files[] entry: path and edits are required and
// entries must not nest multi-file forms.
func validateFilesForm(files []editFileTarget) ([]editFileTarget, error) {
	targets := make([]editFileTarget, 0, len(files))
	for i, f := range files {
		if f.Path == "" {
			return nil, &internal.ToolError{Tool: "edit", Type: "missing_path",
				Detail:   fmt.Sprintf("files[%d] is missing 'path'", i),
				HintText: "Every 'files' entry must carry the file it edits in its 'path' field."}
		}
		if len(f.Edits) == 0 {
			return nil, &internal.ToolError{Tool: "edit", Type: "missing_parameter",
				Detail:   fmt.Sprintf("files[%d] (%s) is missing 'edits'", i, f.Path),
				HintText: "Every 'files' entry needs an 'edits' array; an entry without edits would silently do nothing."}
		}
		if err := checkNoNestedBatch(f.Edits, fmt.Sprintf("files[%d] (%s)", i, f.Path)); err != nil {
			return nil, err
		}
		targets = append(targets, f)
	}
	return targets, nil
}

// targetsFromGlob expands the pattern into one target per matching file, all
// sharing the caller's edits.
func (t *EditFileTool) targetsFromGlob(pattern string, edits []editFileParams) ([]editFileTarget, error) {
	if err := checkNoNestedBatch(edits, "top-level 'edits'"); err != nil {
		return nil, err
	}
	paths, err := t.expandEditGlobPattern(pattern)
	if err != nil {
		return nil, err
	}
	targets := make([]editFileTarget, 0, len(paths))
	for _, path := range paths {
		targets = append(targets, editFileTarget{Path: path, Edits: edits})
	}
	return targets, nil
}

// checkNoNestedBatch rejects "files"/"glob" inside a batch edit entry: nested
// multi-file forms are not supported and would otherwise be silently ignored.
func checkNoNestedBatch(edits []editFileParams, where string) error {
	for i, e := range edits {
		if len(e.Files) > 0 || e.Glob != "" {
			return &internal.ToolError{Tool: "edit", Type: "invalid_input",
				Detail:   fmt.Sprintf("%s: edits[%d] carries a nested 'files'/'glob' — nesting multi-file forms is not supported", where, i),
				HintText: "Flatten the call: one edit call per multi-file batch, with the files listed in the top-level 'files' or selected by 'glob'."}
		}
	}
	return nil
}

// errConflictingEditForm reports mutually exclusive top-level fields.
func errConflictingEditForm(detail string) *internal.ToolError {
	return &internal.ToolError{Tool: "edit", Type: "invalid_input",
		Detail:   detail,
		HintText: "Single file: 'path' (+ optional 'edits'). Multiple files: 'files' [{path, edits}] or 'glob' + top-level 'edits'."}
}

// expandEditGlobPattern expands a glob pattern against the project directory
// and returns matching regular files, sorted, duplicates removed. Relative
// patterns resolve against the project directory; absolute patterns are used
// as-is. Zero matches is a hard failure (glob_no_match): a rename batch that
// touches nothing must surface, not silently succeed. Shell-style single-star
// patterns only — "**" is not supported; list such paths explicitly via
// 'files'.
func (t *EditFileTool) expandEditGlobPattern(pattern string) ([]string, error) {
	pat := pattern
	if !filepath.IsAbs(pat) {
		pat = filepath.Join(t.projectRoot(), pat)
	}
	matches, err := filepath.Glob(pat)
	if err != nil {
		return nil, &internal.ToolError{Tool: "edit", Type: "invalid_input",
			Detail:   fmt.Sprintf("invalid glob %q: %v", pattern, err),
			HintText: "Use shell-style patterns like 'pkg/*.go' or 'docs/*.md'; '**' is not supported."}
	}
	seen := make(map[string]struct{}, len(matches))
	files := make([]string, 0, len(matches))
	for _, m := range matches {
		if fi, statErr := os.Stat(m); statErr != nil || !fi.Mode().IsRegular() {
			continue
		}
		if _, dup := seen[m]; dup {
			continue
		}
		seen[m] = struct{}{}
		files = append(files, m)
	}
	if len(files) == 0 {
		return nil, &internal.ToolError{Tool: "edit", Type: "glob_no_match",
			Detail:   fmt.Sprintf("glob %q matched no files (searched %s)", pattern, filepath.Dir(pat)),
			HintText: "Check the pattern against the actual layout (use 'read' or a directory listing). Zero-match globs fail loudly instead of silently applying nothing."}
	}
	sort.Strings(files)
	return files, nil
}

// projectRoot returns the directory relative glob patterns resolve against.
// EditFileTool.ProjectDir is always set for registered tools; the "."
// fallback keeps the method total for bare test constructs.
func (t *EditFileTool) projectRoot() string {
	if t.ProjectDir != "" {
		return t.ProjectDir
	}
	return "."
}

// displayPathFor renders a file path the way the tool reports paths: as given
// for explicit targets, relative to the project root for glob matches.
func (t *EditFileTool) displayPathFor(resolvedPath, originalPath string) string {
	if originalPath != "" {
		return originalPath
	}
	if rel, err := filepath.Rel(t.projectRoot(), resolvedPath); err == nil && !strings.HasPrefix(rel, "..") {
		return rel
	}
	return resolvedPath
}

// executeMultiFileForm routes the multi-file forms ("files"/"glob").
// isMulti is false when the input is a single-file call, so the caller falls
// through to the single-file paths.
func (t *EditFileTool) executeMultiFileForm(p editFileParams) (out string, isMulti bool, err error) {
	if len(p.Files) == 0 && p.Glob == "" {
		return "", false, nil
	}
	targets, err := t.resolveBatchForm(p)
	if err != nil {
		return "", true, err
	}
	out, err = t.executeBatchFiles(targets)
	return out, true, err
}

// executeBatchFiles applies the multi-file batch: every file is read and its
// edits applied IN MEMORY first; only when every file applies cleanly is any
// file written. A failure in any file is a hard failure of the whole call —
// nothing is written, and the error lists a per-path result (ok / fail /
// skip) so no silent skip can hide a drifted file.
func (t *EditFileTool) executeBatchFiles(targets []editFileTarget) (string, error) {
	var (
		works    []*batchFileWork
		statuses []batchFileStatus
		failed   bool
	)
	// Phase 1 — resolve, read, drift-check and apply every file in memory.
	for _, tgt := range targets {
		w, st := t.prepareBatchFileWork(tgt, works)
		statuses = append(statuses, st)
		if st.state == "fail" {
			failed = true
			continue
		}
		if st.state == "skip" {
			continue
		}
		works = append(works, w)
	}

	// Phase 2 — all-or-nothing: any Phase 1 failure means nothing is written.
	if failed {
		return "", t.batchFilesError(statuses, nil)
	}
	return t.writeBatchFileResults(works, statuses)
}

// batchFileWork holds one target's in-memory edit state between the apply
// phase and the write phase of a multi-file batch.
type batchFileWork struct {
	target     editFileTarget
	resolved   string
	original   string
	targetPath string // post fuzzy-fallback read path
	fuzzyNote  string
	trailingNL bool
	originalLn []string
	lines      []string
	matchTypes []MatchType
}

// prepareBatchFileWork resolves, reads, drift-checks and applies one target
// fully in memory. It returns the work item (nil when the file failed or is a
// duplicate skip) and its per-path status. accepted holds the already-prepared
// work items, used to resolve targets that name the same file twice.
func (t *EditFileTool) prepareBatchFileWork(tgt editFileTarget, accepted []*batchFileWork) (*batchFileWork, batchFileStatus) {
	fail := func(display, detail string, err *internal.ToolError) (*batchFileWork, batchFileStatus) {
		return nil, batchFileStatus{display: display, state: "fail", detail: detail, err: err}
	}
	resolved, original, err := ResolveFileToolPath(t.WorktreeMgr, tgt.Path)
	if err != nil {
		return fail(tgt.Path, toolErrSummary(err), toolErrOf(err))
	}
	display := t.displayPathFor(resolved, original)
	for _, w := range accepted {
		if w.resolved == resolved {
			return nil, batchFileStatus{display: display, state: "skip",
				detail: fmt.Sprintf("same file as %s — edits apply once", w.target.Path)}
		}
	}
	w := &batchFileWork{target: tgt, resolved: resolved, original: original}
	w.originalLn, w.targetPath, w.fuzzyNote, w.trailingNL, err = t.readLines(resolved, original)
	if err != nil {
		return fail(display, toolErrSummary(err), toolErrOf(err))
	}
	if err := checkBatchLineDrift(tgt.Edits); err != nil {
		return fail(display, toolErrSummary(err), toolErrOf(err))
	}
	lines, matchTypes, failIdx, applyErr := t.applyBatchToLines(w.originalLn, tgt.Edits)
	if applyErr != nil {
		wrapped := t.wrapMultiEditError(applyErr, display, failIdx, len(tgt.Edits), tgt.Edits[failIdx], lines)
		te := wrapped.(*internal.ToolError)
		return fail(display, te.Detail, te)
	}
	w.lines = lines
	w.matchTypes = matchTypes
	return w, batchFileStatus{display: display, state: "ok", detail: fmt.Sprintf("%d edits", len(tgt.Edits))}
}

// writeBatchFileResults persists every prepared file (Phase 3) and composes
// the success result: a batch header, skip/fuzzy notes, then one per-file
// block (summary + net diff) per target.
func (t *EditFileTool) writeBatchFileResults(works []*batchFileWork, statuses []batchFileStatus) (string, error) {
	var (
		blocks  []string
		notes   []string
		written []string
	)
	for _, w := range works {
		output := strings.Join(w.lines, "\n")
		if w.trailingNL && output != "" && !strings.HasSuffix(output, "\n") {
			output += "\n"
		}
		diagBlock, writeErr := t.writeEditResult(w.targetPath, w.target.Path, output)
		if writeErr != nil {
			// A write failure leaves earlier files already persisted; say so.
			statuses = append(statuses, batchFileStatus{display: w.target.Path, state: "fail",
				detail: toolErrSummary(writeErr), err: toolErrOf(writeErr)})
			return "", t.batchFilesError(statuses, written)
		}
		written = append(written, w.target.Path)
		if w.fuzzyNote != "" {
			notes = append(notes, w.fuzzyNote)
		}
		blocks = append(blocks, formatMultiEditResult(w.target.Path, len(w.target.Edits), w.matchTypes,
			resolvedPathNote(w.targetPath, w.target.Path), generateUnifiedDiff(w.originalLn, w.lines), "", diagBlock))
	}
	return formatBatchFilesResult(len(works), statuses, notes, blocks), nil
}

// formatBatchFilesResult renders the multi-file success message: the header
// (file count), the skip notes (a skipped duplicate must never be silent) and
// one result block per file.
func formatBatchFilesResult(fileCount int, statuses []batchFileStatus, notes, blocks []string) string {
	header := fmt.Sprintf("[edit: %d files] batch applied", fileCount)
	for _, s := range statuses {
		if s.state == "skip" {
			notes = append(notes, "Note: "+s.display+" skipped — "+s.detail)
		}
	}
	if len(notes) > 0 {
		header += "\n" + strings.Join(notes, "\n")
	}
	return header + "\n" + strings.Join(blocks, "\n")
}

// batchFilesError builds the aggregate hard failure for a multi-file batch:
// the per-path result lines (ok / fail / skip) plus the written-files note
// when a write failed mid-way. The first failing typed error's type and hint
// carry over so callers that dispatch on ToolError.Type keep their behavior
// (loop guardrails, retry logic).
func (t *EditFileTool) batchFilesError(statuses []batchFileStatus, written []string) error {
	var first *internal.ToolError
	var b strings.Builder
	b.WriteString("Multi-file edit failed — no changes were written")
	if len(written) > 0 {
		b.WriteString(fmt.Sprintf(" except: %s (already persisted; verify their content)", strings.Join(written, ", ")))
	}
	b.WriteString(".\n")
	for _, s := range statuses {
		switch s.state {
		case "ok":
			fmt.Fprintf(&b, "  ok   %s (%s)\n", s.display, s.detail)
		case "fail":
			fmt.Fprintf(&b, "  fail %s — %s\n", s.display, s.detail)
			if s.err != nil && first == nil {
				first = s.err
			}
		case "skip":
			fmt.Fprintf(&b, "  skip %s — %s\n", s.display, s.detail)
		}
	}
	te := &internal.ToolError{
		Tool:     "edit",
		Type:     "batch_failed",
		Detail:   strings.TrimSuffix(b.String(), "\n"),
		HintText: "Multi-file batches are all-or-nothing: nothing is written until every file applies cleanly. Fix the failing file and retry the whole batch.",
	}
	if first != nil {
		te.Type = first.Type
		if first.HintText != "" {
			te.HintText = "Multi-file batch: " + first.HintText
		}
	}
	return te
}

// applyBatchToLines applies a batch of edits to in-memory lines, returning
// the resulting lines, the collected match types, and on failure the index of
// the failing edit (-1 on success) with `lines` holding the content at the
// point of failure (earlier edits applied) for the diagnostic.
func (t *EditFileTool) applyBatchToLines(original []string, edits []editFileParams) (lines []string, matchTypes []MatchType, failIdx int, err error) {
	lines = original
	for i, e := range edits {
		newLines, mt, opErr := t.applySingleEdit(lines, e)
		if opErr != nil {
			return lines, matchTypes, i, opErr
		}
		lines = newLines
		if mt != "" {
			matchTypes = append(matchTypes, mt)
		}
	}
	return lines, matchTypes, -1, nil
}

// toolErrOf unwraps a *internal.ToolError, or nil for anything else.
func toolErrOf(err error) *internal.ToolError {
	if te, ok := err.(*internal.ToolError); ok {
		return te
	}
	return nil
}

// toolErrSummary renders an error for the per-path report: typed tool errors
// as "type: detail", anything else verbatim.
func toolErrSummary(err error) string {
	if te := toolErrOf(err); te != nil {
		return fmt.Sprintf("%s: %s", te.Type, te.Detail)
	}
	return err.Error()
}
