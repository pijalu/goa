// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package help

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestHelpDocsColonSyntax is a regression guard for the command router's
// colon-only syntax. The router (core.CommandRouter.Parse) splits on ':' only,
// so any documented form like /pair <text>, /exchange 3, /session save name,
// /memory:show context, /ui theme set ... is unreachable and silently fails
// with "Unknown command". This test forbids the known-broken example
// substrings so the docs cannot regress to the space form.
func TestHelpDocsColonSyntax(t *testing.T) {
	forbidden := []string{
		"/pair Implement", "/pair Refactor",
		"/reviewer Add", "/reviewer Fix",
		"/orchestrate Build",
		"/thinking high",
		"/exchange 3",
		"/stats turn",
		"/memory:show context", "/memory:show a;b;c", "/memory:edit decisions", "/memory:clear todos",
		"/session save ", "/session restore ", "/session delete ",
		"/skill:run refactor",
		"/pipeline:run implement-feature \"",
		"/ui theme set", "/ui flash \"",
		"/goal:new <text>", "/goal:next <text>", "/goal:replace <text>", "/goal:reorder <map>",
	}
	files, err := filepath.Glob("*.long.md")
	if err != nil {
		t.Fatalf("glob help docs: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("no help docs found; test running from the wrong directory?")
	}
	for _, f := range files {
		body, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		text := string(body)
		for _, bad := range forbidden {
			if strings.Contains(text, bad) {
				t.Errorf("%s: documentation still contains broken space-form %q (router is colon-only)", f, bad)
			}
		}
	}
}
