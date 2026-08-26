// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package lsp

import (
	"context"
	"fmt"
	"strings"

	"github.com/pijalu/goa/internal/lsp"
)

// lspRefactoringManager is the manager API needed by refactoring ops.
type lspRefactoringManager interface {
	PrepareRename(context.Context, string, int, int) (*lsp.PrepareRenameResult, error)
	Rename(context.Context, string, int, int, string) (*lsp.WorkspaceEdit, error)
	Completion(context.Context, string, int, int) ([]lsp.CompletionItem, error)
	CodeAction(context.Context, string, lsp.Range) ([]lsp.CodeAction, error)
	Formatting(context.Context, string, lsp.FormattingOptions) ([]lsp.TextEdit, error)
}

// refactorOp runs one refactoring op against the refactoring-capable API.
type refactorOp func(t *Tool, ctx context.Context, mgr lspRefactoringManager, p Params, path string) (string, error)

// refactorOps dispatches each refactoring op to its handler. ExecuteContext
// only routes the ops listed here, so every lookup succeeds.
var refactorOps = map[string]refactorOp{
	"prepareRename": (*Tool).prepareRenameOp,
	"rename":        (*Tool).renameOp,
	"completion":    (*Tool).completionOp,
	"codeAction":    (*Tool).codeActionOp,
	"formatting":    (*Tool).formattingOp,
}

// runRefactoring routes prepareRename/rename/completion/codeAction/formatting
// to their handlers after the capability and position guards pass.
func (t *Tool) runRefactoring(ctx context.Context, p Params, path string) (string, error) {
	mgr, ok := t.Manager.(lspRefactoringManager)
	if !ok {
		return "", newError("unavailable", "refactoring unavailable")
	}
	if p.Line < 0 || p.Character < 0 {
		return "", newError("invalid_input", "line and character must be >= 0")
	}
	run := refactorOps[p.Op]
	return run(t, ctx, mgr, p, path)
}

// prepareRenameOp reports the renameable range at the requested position.
func (t *Tool) prepareRenameOp(ctx context.Context, mgr lspRefactoringManager, p Params, path string) (string, error) {
	v, err := mgr.PrepareRename(ctx, path, p.Line, p.Character)
	if err != nil {
		return "", newError("query_failed", "prepare rename failed: %v", err)
	}
	if v == nil {
		return "no rename target\n", nil
	}
	return fmt.Sprintf("Rename range %d:%d-%d:%d (%s)\n", v.Range.Start.Line, v.Range.Start.Character, v.Range.End.Line, v.Range.End.Character, v.Placeholder), nil
}

// renameOp renames the target symbol, previewing or applying the workspace edit.
func (t *Tool) renameOp(ctx context.Context, mgr lspRefactoringManager, p Params, path string) (string, error) {
	if p.NewName == "" {
		return "", newError("invalid_input", "newName is required")
	}
	v, err := mgr.Rename(ctx, path, p.Line, p.Character, p.NewName)
	if err != nil {
		return "", newError("query_failed", "rename failed: %v", err)
	}
	if v == nil {
		return "Workspace edit: none\n", nil
	}
	policy := lsp.WorkspaceEditPolicy{Root: t.ProjectDir}
	preview, err := lsp.PreviewWorkspaceEdit(v, policy)
	if err != nil {
		return "", newError("invalid_edit", "rename workspace edit rejected: %v", err)
	}
	if !p.Apply {
		return fmt.Sprintf("Workspace edit preview (%d files, %d edits)\n", len(preview.Files), preview.EditCount), nil
	}
	if _, err := lsp.ApplyWorkspaceEdit(v, policy); err != nil {
		return "", newError("apply_failed", "rename workspace edit failed: %v", err)
	}
	return fmt.Sprintf("Workspace edit applied (%d files, %d edits)\n", len(preview.Files), preview.EditCount), nil
}

// completionOp lists completion items at the requested position.
func (t *Tool) completionOp(ctx context.Context, mgr lspRefactoringManager, p Params, path string) (string, error) {
	v, err := mgr.Completion(ctx, path, p.Line, p.Character)
	if err != nil {
		return "", newError("query_failed", "completion failed: %v", err)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Completions (%d):\n", len(v))
	for _, x := range v {
		fmt.Fprintf(&b, "  %s\n", x.Label)
	}
	return b.String(), nil
}

// codeActionOp lists code actions available at the requested position.
func (t *Tool) codeActionOp(ctx context.Context, mgr lspRefactoringManager, p Params, path string) (string, error) {
	pos := lsp.Position{Line: p.Line, Character: p.Character}
	v, err := mgr.CodeAction(ctx, path, lsp.Range{Start: pos, End: pos})
	if err != nil {
		return "", newError("query_failed", "code action failed: %v", err)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Code actions (%d):\n", len(v))
	for _, x := range v {
		fmt.Fprintf(&b, "  %s\n", x.Title)
	}
	return b.String(), nil
}

// formattingOp requests formatting edits for the whole file.
func (t *Tool) formattingOp(ctx context.Context, mgr lspRefactoringManager, _ Params, path string) (string, error) {
	v, err := mgr.Formatting(ctx, path, lsp.FormattingOptions{TabSize: 4, InsertSpaces: true})
	if err != nil {
		return "", newError("query_failed", "formatting failed: %v", err)
	}
	return fmt.Sprintf("Formatting edits (%d)\n", len(v)), nil
}
