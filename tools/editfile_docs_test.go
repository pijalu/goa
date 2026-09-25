// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"strings"
	"testing"
)

// TestEditFileTool_DocsDocumentBehaviorChanges pins the embedded long doc to
// the behavior of fixes F1-F6. The doc is what the model actually reads, so a
// behavior change that silently drops its documentation regresses the tool just
// as surely as a code change would; each entry below names the fix it covers.
func TestEditFileTool_DocsDocumentBehaviorChanges(t *testing.T) {
	tool := &EditFileTool{}
	long := tool.LongDoc()

	cases := []struct {
		fix  string
		text string // substring that must appear verbatim in the long doc
	}{
		{"F1 substring tier", "4-tier matching"},
		{"F1 substring tier", "exact substring"},
		{"F1 substring tier unique file-wide", "exactly once in the whole file"},
		{"F2 batch line rule", "stale_line_numbers"},
		{"F2 two-call workflow", "re-harvest line numbers"},
		{"F4 indent default", "as-is (default for insert_after/insert_before/replace_lines)"},
		{"F4 indent opt-in", `indent_mode: "preserve"`},
		{"F5 explicit empty new_content", `"new_content": ""`},
		{"F5 omitted still refused", "Omitting new_content"},
		{"F6 occurrence error", "occurrence_not_found"},
		{"F6 literal replacement", `"$1"-style group references are NOT expanded`},
		{"F6 preserves prefix/suffix", "prefix and suffix"},
		{"F6 invalid regex literal", "literal string"},
	}
	for _, c := range cases {
		if !strings.Contains(long, c.text) {
			t.Errorf("%s: LongDoc does not document %q", c.fix, c.text)
		}
	}

	// The one-line short doc is what a compact tool listing shows; it must
	// still flag the two behaviors that change how the model uses the tool.
	short := tool.ShortDoc()
	for _, want := range []string{"4-tier", "line-addressed"} {
		if !strings.Contains(short, want) {
			t.Errorf("ShortDoc does not mention %q: %q", want, short)
		}
	}
}

// TestEditFileTool_Schema_DocumentsNewBehavior verifies the schema description
// strings the model sees for the parameters whose semantics changed.
func TestEditFileTool_Schema_DocumentsNewBehavior(t *testing.T) {
	schema := (&EditFileTool{}).Schema()
	props := schema.Schema["properties"].(map[string]any)

	checks := []struct {
		prop string
		text string
	}{
		{"indent_mode", "as-is for replace_lines/insert_after/insert_before"},
		{"indent_mode", "preserve otherwise"},
		{"occurrence", "Nth matching line"},
		{"new_content", `"" is valid`},
		{"pattern", "matched literally"},
		{"edits", "stale_line_numbers"},
	}
	for _, c := range checks {
		field, ok := props[c.prop].(map[string]any)
		if !ok {
			t.Errorf("schema property %q missing", c.prop)
			continue
		}
		desc, _ := field["description"].(string)
		if !strings.Contains(desc, c.text) {
			t.Errorf("schema %q description %q does not mention %q", c.prop, desc, c.text)
		}
	}
}

// TestWriteFileTool_DocsDocumentWholeFileSemantics pins the write clarification:
// the tool overwrites the whole file, and its result previews only the first 10
// lines of the written content (it is not a diff).
func TestWriteFileTool_DocsDocumentWholeFileSemantics(t *testing.T) {
	long := (&WriteFileTool{}).LongDoc()
	for _, want := range []string{
		"completely overwrite",
		"whole-file operation",
		"FIRST 10 lines",
		"not a diff",
	} {
		if !strings.Contains(long, want) {
			t.Errorf("writefile LongDoc does not document %q", want)
		}
	}
	if short := (&WriteFileTool{}).ShortDoc(); !strings.Contains(short, "whole-file replace") {
		t.Errorf("writefile ShortDoc does not document overwrite semantics: %q", short)
	}

	schema := (&WriteFileTool{}).Schema()
	content := schema.Schema["properties"].(map[string]any)["content"].(map[string]any)
	desc, _ := content["description"].(string)
	if !strings.Contains(desc, "overwrites the file") {
		t.Errorf("write schema content description %q does not document overwrite semantics", desc)
	}
}
