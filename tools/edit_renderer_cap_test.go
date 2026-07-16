// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"fmt"
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/ansi"
	"github.com/pijalu/goa/internal/tuirender"
)

// The collapsed edit renderer must colorize only the preview-sized slice of a
// large diff, not the whole thing — rendering a 10k-line diff to show 1k was
// an O(n) commandLoop hitch per edit. Line numbers must stay aligned with the
// full diff, and the truncation hint must reflect the true remaining count.
func TestEditRenderResult_CapsColorizeToPreview(t *testing.T) {
	r := &EditFileRenderer{}
	var sb strings.Builder
	// 4000 body lines (2000 removed + 2000 added).
	sb.WriteString("[edit: x.go] edited\n@@ -1,2000 +1,2000 @@\n")
	for i := 1; i <= 2000; i++ {
		sb.WriteString(fmt.Sprintf("-old %d\n+new %d\n", i, i))
	}
	out := r.RenderResult(sb.String(), tuirender.RenderContext{Expanded: false})

	lines := strings.Split(out, "\n")
	// Preview is 1000 lines + 1 truncation-hint line.
	if len(lines) > editDiffPreviewLines+1 {
		t.Errorf("collapsed render produced %d lines, want <= %d (preview + hint)",
			len(lines), editDiffPreviewLines+1)
	}
	// Truncation hint must report the true unshown count (4000 - 1000 = 3000).
	last := lines[len(lines)-1]
	if !strings.Contains(last, "3000") {
		t.Errorf("truncation hint should report 3000 remaining lines, got: %q", last)
	}
	// Line numbers must be padded to the full-diff width (4 digits: up to 2000),
	// so early numbers keep their alignment after truncation.
	if !strings.Contains(out, "   1") {
		t.Errorf("line-number alignment lost after truncation")
	}
}

// Expanded view renders beyond the preview cap (no truncation at the cap).
func TestEditRenderResult_ExpandedRendersAll(t *testing.T) {
	r := &EditFileRenderer{}
	var sb strings.Builder
	n := 50
	sb.WriteString(fmt.Sprintf("[edit: x.go] edited\n@@ -1,%d +1,%d @@\n", n, n))
	for i := 1; i <= n; i++ {
		sb.WriteString(fmt.Sprintf("-old %d\n+new %d\n", i, i))
	}
	out := r.RenderResult(sb.String(), tuirender.RenderContext{Expanded: true})
	// The last added line (n) must be present in the expanded output. Compare
	// ANSI-stripped: the intra-line diff splits tokens with color codes.
	stripped := stripANSI(out)
	if !strings.Contains(stripped, fmt.Sprintf("new %d", n)) {
		t.Errorf("expanded render truncated the diff; last added line %d missing", n)
	}
}

// The pre-truncation must not break intra-line diff pairing at the boundary:
// a removed line at the truncation edge without its added counterpart still
// renders (as a plain removed line), not a panic or empty line.
func TestEditRenderResult_TruncationBoundarySafe(t *testing.T) {
	r := &EditFileRenderer{}
	var sb strings.Builder
	sb.WriteString("[edit: x.go] edited\n@@ -1,2000 +1,2000 @@\n")
	for i := 1; i <= 2000; i++ {
		sb.WriteString(fmt.Sprintf("-old %d\n+new %d\n", i, i))
	}
	out := r.RenderResult(sb.String(), tuirender.RenderContext{Expanded: false})
	if strings.TrimSpace(out) == "" {
		t.Error("render produced empty output")
	}
}

func stripANSI(s string) string { return ansi.Strip(s) }
