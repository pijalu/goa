// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/pijalu/goa/internal/lsp"
)

const (
	// lspPollInterval is how often we re-check for published diagnostics.
	lspPollInterval = 50 * time.Millisecond
	// lspPollTimeout bounds the total wait so a slow gopls never stalls a
	// file tool. Diagnostics typically arrive within a few hundred ms; a cold
	// package load can take longer, so we give it up to this long before
	// returning whatever is (or isn't) available.
	lspPollTimeout = 1 * time.Second
)

// collectLSPDiagnostics polls the manager for diagnostics for resolvedPath
// until they appear or the timeout elapses, returning whatever is available.
// It replaces the previous fixed 150ms sleep, which raced gopls's asynchronous
// publishDiagnostics on cold package loads (bugs.md L1). The ctx is honoured
// so a cancelled turn stops the wait early.
func collectLSPDiagnostics(ctx context.Context, mgr LSPDocumentManager, resolvedPath string) []lsp.Diagnostic {
	if mgr == nil {
		return nil
	}
	// Fast path: diagnostics already present (e.g. gopls was warm).
	if diags := mgr.DiagnosticsFor(ctx, resolvedPath); len(diags) > 0 {
		return diags
	}
	deadline := time.NewTimer(lspPollTimeout)
	defer deadline.Stop()
	ticker := time.NewTicker(lspPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return mgr.DiagnosticsFor(ctx, resolvedPath)
		case <-deadline.C:
			return mgr.DiagnosticsFor(ctx, resolvedPath)
		case <-ticker.C:
			if diags := mgr.DiagnosticsFor(ctx, resolvedPath); len(diags) > 0 {
				return diags
			}
		}
	}
}

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
