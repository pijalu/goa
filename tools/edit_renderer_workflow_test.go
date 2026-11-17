// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/ansi"
	"github.com/pijalu/goa/internal/tuirender"
)

func TestEditRenderer_MultiFileAtomicCallAndPartial(t *testing.T) {
	renderer := NewEditFileRenderer()
	args := map[string]any{
		"atomic": true,
		"files": []any{
			map[string]any{"path": "a.go", "edits": []any{}},
			map[string]any{"path": "b.go", "edits": []any{}},
		},
	}
	ctx := tuirender.RenderContext{Cwd: "/tmp"}
	if got := ansi.Strip(renderer.RenderCall(args, ctx)); !strings.Contains(got, "atomic") {
		t.Errorf("atomic files call is not visible: %q", got)
	}
	if got := ansi.Strip(renderer.RenderPartial(args, ctx)); !strings.Contains(got, "2 files — atomic") {
		t.Errorf("atomic files partial is not visible: %q", got)
	}
	globArgs := map[string]any{"atomic": true, "glob": "pkg/*.go", "edits": []any{map[string]any{"old_string": "x"}}}
	if got := ansi.Strip(renderer.RenderCall(globArgs, ctx)); !strings.Contains(got, "glob:pkg/*.go (atomic)") {
		t.Errorf("atomic glob call is not visible: %q", got)
	}
	if got := ansi.Strip(renderer.RenderPartial(globArgs, ctx)); !strings.Contains(got, "1 edits — atomic") {
		t.Errorf("atomic glob partial is not visible: %q", got)
	}
}

func TestEditRenderer_MultipleHunksShareOneFileHeader(t *testing.T) {
	output := "[edit: large.go] 2 edits applied\n" +
		"@@ -48,3 +48,3 @@\n before-a\n-old-a\n+new-a\n after-a\n" +
		"@@ -298,3 +298,3 @@\n before-b\n-old-b\n+new-b\n after-b\n"
	rendered := ansi.Strip(NewEditFileRenderer().RenderResult(output, tuirender.RenderContext{Expanded: true}))
	if strings.Count(rendered, "[edit: large.go]") != 1 {
		t.Errorf("file summary should be rendered once:\n%s", rendered)
	}
	for _, want := range []string{"old-a", "new-a", "old-b", "new-b"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("rendered multi-hunk output lacks %q:\n%s", want, rendered)
		}
	}
	if strings.Contains(rendered, "unchanged middle") {
		t.Errorf("renderer introduced unchanged middle content:\n%s", rendered)
	}
}
