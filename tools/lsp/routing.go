// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package lsp

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/pijalu/goa/tools/common"
)

// opFunc runs one routed operation against its resolved path.
type opFunc func(ctx context.Context, p Params, resolvedPath string) (string, error)

// parseInput decodes the request and validates the common op/path fields.
func parseInput(input string) (Params, error) {
	var p Params
	if err := json.Unmarshal([]byte(input), &p); err != nil {
		return p, newError("invalid_input", "invalid JSON input: %v", err)
	}
	if p.Op == "" {
		return p, newError("invalid_input", "op is required")
	}
	if p.Path == "" {
		return p, newError("invalid_input", "path is required")
	}
	return p, nil
}

// resolveTarget maps the input path to an absolute path and verifies that a
// configured language server claims it.
func (t *Tool) resolveTarget(p Params) (string, error) {
	resolvedPath, _, err := common.ResolveFileToolPath(t.WorktreeMgr, p.Path)
	if err != nil {
		return "", newError("invalid_path", "cannot resolve path %q: %v", p.Path, err)
	}
	if support, ok := t.Manager.(interface{ SupportsPath(string) bool }); ok {
		if !support.SupportsPath(resolvedPath) {
			return "", newError("invalid_path", "no configured language server supports %q", p.Path)
		}
	} else if !knownPath(resolvedPath) {
		return "", newError("invalid_path", "lsp only supports configured language files (not Go files), got %q", p.Path)
	}
	return resolvedPath, nil
}

// knownPath reports whether the file extension belongs to a language with
// bundled LSP support.
func knownPath(path string) bool {
	for _, ext := range []string{".go", ".js", ".jsx", ".ts", ".tsx", ".java"} {
		if strings.HasSuffix(path, ext) {
			return true
		}
	}
	return false
}

// opRoutes maps every supported op to the runner that serves it. Ops missing
// here are rejected by ExecuteContext with the generic unknown-op error.
func opRoutes(t *Tool) map[string]opFunc {
	return map[string]opFunc{
		"definition": t.runPositionOp,
		"references": t.runPositionOp,
		"hover":      t.runPositionOp,
		"symbols": func(ctx context.Context, _ Params, resolvedPath string) (string, error) {
			return t.runSymbols(ctx, resolvedPath)
		},
		"implementation":       t.runAdvanced,
		"workspaceSymbol":      t.runAdvanced,
		"prepareCallHierarchy": t.runAdvanced,
		"incomingCalls":        t.runAdvanced,
		"outgoingCalls":        t.runAdvanced,
		"prepareRename":        t.runRefactoring,
		"rename":               t.runRefactoring,
		"completion":           t.runRefactoring,
		"codeAction":           t.runRefactoring,
		"formatting":           t.runRefactoring,
	}
}
