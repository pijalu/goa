// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package lsp

import (
	"context"
	"fmt"

	"github.com/pijalu/goa/internal/lsp"
)

// runPositionOp handles definition/references/hover, which all take a position.
func (t *Tool) runPositionOp(ctx context.Context, p Params, resolvedPath string) (string, error) {
	if p.Line < 0 || p.Character < 0 {
		return "", newError("invalid_input", "line and character must be >= 0 for op %q", p.Op)
	}
	switch p.Op {
	case "definition":
		locs, err := t.Manager.Definition(ctx, resolvedPath, p.Line, p.Character)
		if err != nil {
			return "", newError("query_failed", "definition failed: %v", err)
		}
		return formatLocations("Definition", locs), nil
	case "references":
		locs, err := t.Manager.References(ctx, resolvedPath, p.Line, p.Character)
		if err != nil {
			return "", newError("query_failed", "references failed: %v", err)
		}
		return formatLocations("References", locs), nil
	default: // hover
		h, err := t.Manager.Hover(ctx, resolvedPath, p.Line, p.Character)
		if err != nil {
			return "", newError("query_failed", "hover failed: %v", err)
		}
		return formatHover(h), nil
	}
}

// runSymbols handles the documentSymbol operation.
func (t *Tool) runSymbols(ctx context.Context, resolvedPath string) (string, error) {
	syms, err := t.Manager.DocumentSymbols(ctx, resolvedPath)
	if err != nil {
		return "", newError("query_failed", "document symbols failed: %v", err)
	}
	return formatSymbols(syms), nil
}

// advancedOp runs one advanced navigation op against the navigation API.
type advancedOp func(t *Tool, ctx context.Context, mgr lspNavigationManager, p Params, path string) (string, error)

// advancedOps dispatches each advanced navigation op to its handler.
// ExecuteContext only routes the ops listed here, so every lookup succeeds.
var advancedOps = map[string]advancedOp{
	"implementation":       (*Tool).implementationOp,
	"workspaceSymbol":      (*Tool).workspaceSymbolOp,
	"prepareCallHierarchy": (*Tool).prepareCallHierarchyOp,
	"incomingCalls":        (*Tool).incomingCallsOp,
	"outgoingCalls":        (*Tool).outgoingCallsOp,
}

// lspNavigationManager is the manager API needed by advanced navigation ops.
type lspNavigationManager interface {
	Implementation(context.Context, string, int, int) ([]lsp.Location, error)
	WorkspaceSymbols(context.Context, string, string) ([]lsp.WorkspaceSymbol, error)
	PrepareCallHierarchy(context.Context, string, int, int) ([]lsp.CallHierarchyItem, error)
	IncomingCalls(context.Context, string, int, int) ([]lsp.CallHierarchyIncomingCall, error)
	OutgoingCalls(context.Context, string, int, int) ([]lsp.CallHierarchyOutgoingCall, error)
	SupportsPath(string) bool
}

// runAdvanced routes implementation/workspaceSymbol/call-hierarchy ops to
// their handlers after the capability and position guards pass;
// workspaceSymbol queries by name and needs no position.
func (t *Tool) runAdvanced(ctx context.Context, p Params, path string) (string, error) {
	mgr, ok := t.Manager.(lspNavigationManager)
	if !ok {
		return "", newError("unavailable", "advanced LSP operation unavailable")
	}
	if p.Op != "workspaceSymbol" && (p.Line < 0 || p.Character < 0) {
		return "", newError("invalid_input", "line and character must be >= 0")
	}
	run := advancedOps[p.Op]
	return run(t, ctx, mgr, p, path)
}

// implementationOp lists implementation locations for the target symbol.
func (t *Tool) implementationOp(ctx context.Context, mgr lspNavigationManager, p Params, path string) (string, error) {
	v, err := mgr.Implementation(ctx, path, p.Line, p.Character)
	if err != nil {
		return "", newError("query_failed", "implementation failed: %v", err)
	}
	return formatLocations("Implementation", v), nil
}

// workspaceSymbolOp searches workspace-wide symbols by query text.
func (t *Tool) workspaceSymbolOp(ctx context.Context, mgr lspNavigationManager, p Params, path string) (string, error) {
	v, err := mgr.WorkspaceSymbols(ctx, path, p.Query)
	if err != nil {
		return "", newError("query_failed", "workspace symbols failed: %v", err)
	}
	return formatWorkspaceSymbols(v), nil
}

// prepareCallHierarchyOp returns the call-hierarchy items at the position.
func (t *Tool) prepareCallHierarchyOp(ctx context.Context, mgr lspNavigationManager, p Params, path string) (string, error) {
	v, err := mgr.PrepareCallHierarchy(ctx, path, p.Line, p.Character)
	if err != nil {
		return "", newError("query_failed", "call hierarchy failed: %v", err)
	}
	return formatCallItems(v), nil
}

// incomingCallsOp lists callers of the symbol at the position.
func (t *Tool) incomingCallsOp(ctx context.Context, mgr lspNavigationManager, p Params, path string) (string, error) {
	v, err := mgr.IncomingCalls(ctx, path, p.Line, p.Character)
	if err != nil {
		return "", newError("query_failed", "incoming calls failed: %v", err)
	}
	return fmt.Sprintf("Incoming calls (%d)\n", len(v)), nil
}

// outgoingCallsOp lists callees of the symbol at the position.
func (t *Tool) outgoingCallsOp(ctx context.Context, mgr lspNavigationManager, p Params, path string) (string, error) {
	v, err := mgr.OutgoingCalls(ctx, path, p.Line, p.Character)
	if err != nil {
		return "", newError("query_failed", "outgoing calls failed: %v", err)
	}
	return fmt.Sprintf("Outgoing calls (%d)\n", len(v)), nil
}
