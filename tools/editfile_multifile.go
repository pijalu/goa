// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pijalu/goa/internal"
)

// editFileTarget is one independently validated multi-file target. A malformed
// entry stays in the list with validationErr so valid siblings can still run in
// the default non-atomic mode and atomic mode can report a complete preflight.
type editFileTarget struct {
	Path          string           `json:"path"`
	Edits         []editFileParams `json:"edits"`
	validationErr error            `json:"-"`
}

// batchFileStatus is one per-path result. state is ok, fail, or skip; detail
// distinguishes validated, written, and rolled-back successes explicitly.
type batchFileStatus struct {
	display string
	state   string
	detail  string
	err     *internal.ToolError
}

func (t *EditFileTool) resolveBatchForm(p editFileParams) ([]editFileTarget, error) {
	switch {
	case len(p.Files) > 0:
		if err := checkBatchExclusivity(p, true); err != nil {
			return nil, err
		}
		return validateFilesForm(p.Files), nil
	case p.Glob != "":
		if err := checkBatchExclusivity(p, false); err != nil {
			return nil, err
		}
		return t.targetsFromGlob(p.Glob, p.Edits)
	}
	return nil, nil
}

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

// validateFilesForm converts entry-local shape errors into per-file failures.
// Only top-level form conflicts are global; one malformed files[] entry must
// not prevent valid siblings from being written in non-atomic mode.
func validateFilesForm(files []editFileTarget) []editFileTarget {
	targets := make([]editFileTarget, 0, len(files))
	for i, f := range files {
		where := fmt.Sprintf("files[%d]", i)
		if f.Path == "" {
			f.Path = where
			f.validationErr = &internal.ToolError{Tool: "edit", Type: "missing_path",
				Detail:   where + " is missing 'path'",
				HintText: "Every 'files' entry must carry the file it edits in its 'path' field."}
		} else {
			where = fmt.Sprintf("%s (%s)", where, f.Path)
		}
		if len(f.Edits) == 0 && f.validationErr == nil {
			f.validationErr = &internal.ToolError{Tool: "edit", Type: "missing_parameter",
				Detail:   where + " is missing 'edits'",
				HintText: "Every 'files' entry needs an 'edits' array; an entry without edits would silently do nothing."}
		}
		if err := checkNoNestedBatch(f.Edits, where); err != nil && f.validationErr == nil {
			f.validationErr = err
		}
		targets = append(targets, f)
	}
	return targets
}

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

func checkNoNestedBatch(edits []editFileParams, where string) error {
	for i, e := range edits {
		if len(e.Files) > 0 || e.Glob != "" || e.Atomic != nil {
			return &internal.ToolError{Tool: "edit", Type: "invalid_input",
				Detail:   fmt.Sprintf("%s: edits[%d] carries a nested multi-file field ('files'/'glob'/'atomic')", where, i),
				HintText: "Keep files/glob/atomic at the top level; edits[] contains only operations for its owning file."}
		}
	}
	return nil
}

func errConflictingEditForm(detail string) *internal.ToolError {
	return &internal.ToolError{Tool: "edit", Type: "invalid_input",
		Detail:   detail,
		HintText: "Single file: 'path' (+ optional 'edits'). Multiple files: 'files' [{path, edits}] or 'glob' + top-level 'edits'; optional top-level 'atomic'."}
}

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
	for _, match := range matches {
		info, statErr := os.Stat(match)
		if statErr != nil || !info.Mode().IsRegular() {
			continue
		}
		if _, duplicate := seen[match]; duplicate {
			continue
		}
		seen[match] = struct{}{}
		files = append(files, match)
	}
	if len(files) == 0 {
		return nil, &internal.ToolError{Tool: "edit", Type: "glob_no_match",
			Detail:   fmt.Sprintf("glob %q matched no files (searched %s)", pattern, filepath.Dir(pat)),
			HintText: "Check the pattern against the actual layout (use 'read' or a directory listing). Zero-match globs fail loudly instead of silently applying nothing."}
	}
	sort.Strings(files)
	return files, nil
}

func (t *EditFileTool) projectRoot() string {
	if t.ProjectDir != "" {
		return t.ProjectDir
	}
	return "."
}

func (t *EditFileTool) displayPathFor(resolvedPath, originalPath string) string {
	if originalPath != "" {
		return originalPath
	}
	if rel, err := filepath.Rel(t.projectRoot(), resolvedPath); err == nil && !strings.HasPrefix(rel, "..") {
		return rel
	}
	return resolvedPath
}

func (t *EditFileTool) executeMultiFileForm(p editFileParams) (out string, isMulti bool, err error) {
	if len(p.Files) == 0 && p.Glob == "" {
		return "", false, nil
	}
	targets, err := t.resolveBatchForm(p)
	if err != nil {
		return "", true, err
	}
	atomic := p.Atomic != nil && *p.Atomic
	out, err = t.executeBatchFiles(targets, atomic)
	return out, true, err
}

// executeBatchFiles always preflights every target. Atomic mode stops before
// writing on any validation failure; default mode writes every valid target and
// reports both successful and failed paths in the aggregate result.
func (t *EditFileTool) executeBatchFiles(targets []editFileTarget, atomic bool) (string, error) {
	works := make([]*batchFileWork, 0, len(targets))
	statuses := make([]batchFileStatus, 0, len(targets))
	failed := false
	for _, target := range targets {
		work, status := t.prepareBatchFileWork(target, works)
		statuses = append(statuses, status)
		if status.state == "fail" {
			failed = true
			continue
		}
		if status.state == "skip" {
			continue
		}
		work.statusIndex = len(statuses) - 1
		works = append(works, work)
	}
	if atomic && failed {
		for _, work := range works {
			statuses[work.statusIndex].detail = "validated; not written"
		}
		return "", t.batchFilesError(statuses, nil, true, false, nil)
	}
	return t.writeBatchFileResults(works, statuses, atomic, failed)
}

// batchFileWork retains exact original bytes for atomic rollback in addition
// to normalized line state used to apply and render edits.
type batchFileWork struct {
	target        editFileTarget
	resolved      string
	targetPath    string
	fuzzyNote     string
	trailingNL    bool
	originalLn    []string
	lines         []string
	matchTypes    []MatchType
	originalBytes []byte
	originalMode  os.FileMode
	statusIndex   int
}

func (t *EditFileTool) prepareBatchFileWork(target editFileTarget, accepted []*batchFileWork) (*batchFileWork, batchFileStatus) {
	fail := func(display string, err error) (*batchFileWork, batchFileStatus) {
		return nil, batchFileStatus{display: display, state: "fail", detail: toolErrSummary(err), err: toolErrOf(err)}
	}
	if target.validationErr != nil {
		return fail(target.Path, target.validationErr)
	}
	resolved, original, err := ResolveFileToolPath(t.WorktreeMgr, target.Path)
	if err != nil {
		return fail(target.Path, err)
	}
	display := t.displayPathFor(resolved, original)
	for _, work := range accepted {
		if work.resolved == resolved {
			return nil, batchFileStatus{display: display, state: "skip", detail: fmt.Sprintf("same file as %s — edits apply once", work.target.Path)}
		}
	}
	work := &batchFileWork{target: target, resolved: resolved}
	source, err := t.readEditSource(resolved, original)
	if err != nil {
		return fail(display, err)
	}
	work.originalLn = splitLines(string(source.data))
	work.targetPath = source.targetPath
	work.fuzzyNote = source.fuzzyNote
	work.trailingNL = strings.HasSuffix(string(source.data), "\n")
	if err := work.captureOriginal(source.data); err != nil {
		return fail(display, err)
	}
	if err := checkBatchLineDrift(target.Edits); err != nil {
		return fail(display, err)
	}
	lines, matchTypes, failIndex, applyErr := t.applyBatchToLines(work.originalLn, target.Edits)
	if applyErr != nil {
		wrapped := t.wrapMultiEditError(applyErr, display, failIndex, len(target.Edits), target.Edits[failIndex], lines)
		return fail(display, wrapped)
	}
	work.lines = lines
	work.matchTypes = matchTypes
	return work, batchFileStatus{display: display, state: "ok", detail: fmt.Sprintf("%d edits validated", len(target.Edits))}
}

func (w *batchFileWork) captureOriginal(data []byte) error {
	info, err := os.Stat(w.targetPath)
	if err != nil {
		return &internal.ToolError{Tool: "edit", Type: "read_error", Detail: fmt.Sprintf("Cannot stat %s: %v", w.target.Path, err)}
	}
	w.originalBytes = data
	w.originalMode = info.Mode()
	return nil
}

func (w *batchFileWork) output() string {
	output := strings.Join(w.lines, "\n")
	if w.trailingNL && output != "" && !strings.HasSuffix(output, "\n") {
		output += "\n"
	}
	return output
}

func (t *EditFileTool) writeBatchFileResults(works []*batchFileWork, statuses []batchFileStatus, atomic, failed bool) (string, error) {
	blocks := make([]string, 0, len(works))
	var notes, written []string
	for i, work := range works {
		diagBlock, writeErr := t.writeEditResult(work.targetPath, work.target.Path, work.output())
		if writeErr != nil {
			failed = true
			status := &statuses[work.statusIndex]
			status.state = "fail"
			status.detail = toolErrSummary(writeErr)
			status.err = toolErrOf(writeErr)
			if atomic {
				return t.rollbackAtomicBatch(works[:i+1], works[i+1:], statuses)
			}
			continue
		}
		written = append(written, work.target.Path)
		statuses[work.statusIndex].detail = fmt.Sprintf("%d edits written", len(work.target.Edits))
		if work.fuzzyNote != "" {
			notes = append(notes, work.fuzzyNote)
		}
		blocks = append(blocks, formatMultiEditResult(work.target.Path, len(work.target.Edits), work.matchTypes,
			resolvedPathNote(work.targetPath, work.target.Path), generateUnifiedDiff(work.originalLn, work.lines), "", diagBlock))
	}
	out := formatBatchFilesResult(len(blocks), statuses, notes, blocks)
	if failed {
		return out, t.batchFilesError(statuses, written, false, false, nil)
	}
	return out, nil
}

func (t *EditFileTool) rollbackAtomicBatch(attempted, pending []*batchFileWork, statuses []batchFileStatus) (string, error) {
	rollbackFailures := t.restoreBatchFiles(attempted, statuses)
	for _, work := range pending {
		statuses[work.statusIndex].detail = "validated; not written"
	}
	return "", t.batchFilesError(statuses, nil, true, true, rollbackFailures)
}

// restoreBatchFiles restores attempted writes in reverse order, including the
// failing write (which may have changed bytes before read-back verification
// failed). Per-file statuses and notifiers reflect the actual rollback result.
func (t *EditFileTool) restoreBatchFiles(attempted []*batchFileWork, statuses []batchFileStatus) []string {
	var failures []string
	for i := len(attempted) - 1; i >= 0; i-- {
		work := attempted[i]
		status := &statuses[work.statusIndex]
		if err := t.writeVerified(work.targetPath, work.originalBytes, work.originalMode); err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", work.target.Path, err))
			if status.state == "fail" {
				status.detail += "; rollback_failed: " + err.Error()
			} else {
				status.detail = "rollback_failed: " + err.Error()
			}
			status.state = "fail"
			status.err = &internal.ToolError{Tool: "edit", Type: "rollback_failed", Detail: err.Error()}
			continue
		}
		if status.state == "ok" {
			status.detail = "rolled back after write failure"
		} else {
			status.detail += "; original bytes restored (rolled back)"
		}
		if t.FileChangeNotifier != nil {
			t.FileChangeNotifier(work.targetPath)
		}
		_ = t.notifyLSP(context.Background(), work.targetPath)
	}
	return failures
}

func formatBatchFilesResult(fileCount int, statuses []batchFileStatus, notes, blocks []string) string {
	header := fmt.Sprintf("[edit: %d files] batch applied", fileCount)
	for _, status := range statuses {
		if status.state == "skip" {
			notes = append(notes, "Note: "+status.display+" skipped — "+status.detail)
		}
	}
	if len(notes) > 0 {
		header += "\n" + strings.Join(notes, "\n")
	}
	if len(blocks) == 0 {
		return header
	}
	return header + "\n" + strings.Join(blocks, "\n")
}

func (t *EditFileTool) batchFilesError(statuses []batchFileStatus, written []string, atomic, rolledBack bool, rollbackFailures []string) error {
	var first *internal.ToolError
	var b strings.Builder
	writeBatchErrorHeader(&b, atomic, len(written) > 0, rollbackFailures)
	for _, status := range statuses {
		writeBatchStatus(&b, status)
		if status.state == "fail" && status.err != nil && first == nil {
			first = status.err
		}
	}
	if atomic && rolledBack && len(rollbackFailures) == 0 {
		b.WriteString("  rollback: all attempted writes rolled back\n")
	}
	toolErr := &internal.ToolError{Tool: "edit", Type: "batch_failed", Detail: strings.TrimSuffix(b.String(), "\n")}
	if atomic {
		toolErr.HintText = "Atomic multi-file edit: fix the failing file and retry the whole batch. Validated files were not left partially edited."
	} else {
		toolErr.HintText = "Non-atomic multi-file edit: ok files are already persisted. Inspect failed paths after write/verify errors, fix only failures, and do not assume rollback."
	}
	if first != nil {
		toolErr.Type = first.Type
		if first.HintText != "" {
			toolErr.HintText = "Multi-file batch: " + first.HintText + " " + toolErr.HintText
		}
	}
	if len(rollbackFailures) > 0 {
		toolErr.Type = "rollback_failed"
		toolErr.HintText = "Atomic rollback incomplete: inspect every path marked rollback_failed before retrying; successfully restored paths need no edit."
	}
	return toolErr
}

func writeBatchErrorHeader(b *strings.Builder, atomic, hasWritten bool, rollbackFailures []string) {
	if atomic && len(rollbackFailures) > 0 {
		b.WriteString("Atomic multi-file edit failed; rollback incomplete: ")
		b.WriteString(strings.Join(rollbackFailures, "; "))
		b.WriteString(".\n")
		return
	}
	if atomic {
		b.WriteString("Atomic multi-file edit failed — no changes were left written.\n")
		return
	}
	if hasWritten {
		b.WriteString("Multi-file edit completed with errors — successful files were written; failures were not rolled back")
	} else {
		b.WriteString("Multi-file edit completed with errors — no successful files were written; inspect failed paths")
	}
	b.WriteString(".\n")
}

func writeBatchStatus(b *strings.Builder, status batchFileStatus) {
	switch status.state {
	case "ok":
		fmt.Fprintf(b, "  ok   %s (%s)\n", status.display, status.detail)
	case "fail":
		fmt.Fprintf(b, "  fail %s — %s\n", status.display, status.detail)
	case "skip":
		fmt.Fprintf(b, "  skip %s — %s\n", status.display, status.detail)
	}
}

func (t *EditFileTool) applyBatchToLines(original []string, edits []editFileParams) (lines []string, matchTypes []MatchType, failIdx int, err error) {
	lines = original
	for i, edit := range edits {
		newLines, matchType, opErr := t.applySingleEdit(lines, edit)
		if opErr != nil {
			return lines, matchTypes, i, opErr
		}
		lines = newLines
		if matchType != "" {
			matchTypes = append(matchTypes, matchType)
		}
	}
	return lines, matchTypes, -1, nil
}

func toolErrOf(err error) *internal.ToolError {
	if toolErr, ok := err.(*internal.ToolError); ok {
		return toolErr
	}
	return nil
}

func toolErrSummary(err error) string {
	if toolErr := toolErrOf(err); toolErr != nil {
		return fmt.Sprintf("%s: %s", toolErr.Type, toolErr.Detail)
	}
	return err.Error()
}
