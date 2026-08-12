// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"os"
	"path/filepath"
	"testing"
)

// seedPlansDir creates a plans/ tree mirroring Issue 8: an exact
// plan.md among many PLAN-*.md fuzzy matches.
func seedPlansDir(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	plans := filepath.Join(root, "plans")
	if err := os.MkdirAll(plans, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		"plan.md",
		"PLAN-00-TEST-INFRA.md", "PLAN-01-AFFINITY.md", "PLAN-01-DECOMPOSE.md",
		"PLAN-02-PARSER.md", "PLAN-02-TYPES.md", "PLAN-03-ALTER.md", "PLAN-03-PARSER.md",
	} {
		if err := os.WriteFile(filepath.Join(plans, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func completeFiles(t *testing.T, fc *FileCompleter, prefix string) []Completion {
	t.Helper()
	got := fc.Complete(prefix)
	if got == nil {
		return nil
	}
	// fd order is platform-dependent; both paths must produce the same set.
	return got
}

// TestFileCompleter_CategoryIsFiles (Issue 8a): file completions must
// carry their own category — the zero value CatMostUsed used to render them
// under a "── Most Used ──" header they have nothing to do with.
func TestFileCompleter_CategoryIsFiles(t *testing.T) {
	root := seedPlansDir(t)
	fc := NewFileCompleter(root)
	for _, comp := range completeFiles(t, fc, "@plans/plan") {
		if comp.Category != CatFiles {
			t.Errorf("completion %q category = %v, want CatFiles", comp.Value, comp.Category)
		}
	}
}

// TestFileCompleter_ExactAndPrefixRankFirst (Issue 8b): the exact
// match and case-insensitive prefix matches rank above fuzzy matches,
// regardless of fd/readdir order — @plans/plan must offer plan.md first.
func TestFileCompleter_ExactAndPrefixRankFirst(t *testing.T) {
	root := seedPlansDir(t)
	for _, forceFallback := range []bool{false, true} {
		if forceFallback {
			old := fdAvailable
			fdAvailable = false
			defer func() { fdAvailable = old }()
		}
		assertPlanRanksFirst(t, NewFileCompleter(root), forceFallback)
	}
}

// assertPlanRanksFirst asserts @plans/plan completes to plan.md before any
// PLAN-*.md fuzzy match.
func assertPlanRanksFirst(t *testing.T, fc *FileCompleter, fallback bool) {
	t.Helper()
	got := completeFiles(t, fc, "@plans/plan")
	if len(got) == 0 {
		t.Fatalf("fallback=%v: no completions", fallback)
	}
	if got[0].Value != "@plans/plan.md" {
		t.Errorf("fallback=%v: first completion = %q, want @plans/plan.md", fallback, got[0].Value)
	}
	seenPlan := false
	for _, c := range got {
		if c.Value == "@plans/plan.md" {
			seenPlan = true
		}
		if !seenPlan && filepath.Base(c.Value) != "plan.md" {
			t.Errorf("fallback=%v: %q ranked before @plans/plan.md", fallback, c.Value)
		}
	}
	if !seenPlan {
		t.Errorf("fallback=%v: @plans/plan.md not in candidates %v", fallback, got)
	}
}

// TestFileCompleter_ExactPathSuppressesPopup (Issue 8c): when the
// typed token already names an existing file, completion is done — no
// candidates, no popup.
func TestFileCompleter_ExactPathSuppressesPopup(t *testing.T) {
	root := seedPlansDir(t)
	for _, forceFallback := range []bool{false, true} {
		if forceFallback {
			old := fdAvailable
			fdAvailable = false
			defer func() { fdAvailable = old }()
		}
		fc := NewFileCompleter(root)
		if got := completeFiles(t, fc, "@plans/plan.md"); len(got) != 0 {
			t.Errorf("fallback=%v: exact existing path returned %d candidates, want 0", forceFallback, len(got))
		}
	}
	// A partial token still completes.
	fc := NewFileCompleter(root)
	if got := completeFiles(t, fc, "@plans/plan"); len(got) == 0 {
		t.Error("partial token returned no candidates")
	}
}

// TestCategoryHeader_Files: the new category renders a truthful header.
func TestCategoryHeader_Files(t *testing.T) {
	if got := categoryHeader(CatFiles); got != "── Files ──" {
		t.Errorf("categoryHeader(CatFiles) = %q", got)
	}
}

// TestRankFileCompletions covers the shared post-processing both the fd and
// the readdir path run: tier order (exact > typed-case prefix >
// case-insensitive prefix > fuzzy), shorter basename first within a tier,
// and the CatFiles stamp.
func TestRankFileCompletions(t *testing.T) {
	in := []Completion{
		{Value: "@plans/PLAN-00-TEST-INFRA.md"},
		{Value: "@plans/PLAN-01-AFFINITY.md"},
		{Value: "@plans/plan.md"},
		{Value: "@plans/planner.md"},
		{Value: "@plans/sub/plan.md"},
	}
	got := rankFileCompletions(in, "plan")
	wantOrder := []string{
		// "plan.md" is not an exact match for "plan" — both plan.md files are
		// typed-case prefixes; identical basenames keep input order.
		"@plans/plan.md",
		"@plans/sub/plan.md",
		"@plans/planner.md",            // typed-case prefix
		"@plans/PLAN-01-AFFINITY.md",   // case-insensitive prefix, shorter
		"@plans/PLAN-00-TEST-INFRA.md", // case-insensitive prefix
	}
	if len(got) != len(wantOrder) {
		t.Fatalf("ranked %d items, want %d", len(got), len(wantOrder))
	}
	for i, want := range wantOrder {
		if got[i].Value != want {
			t.Errorf("rank %d = %q, want %q", i, got[i].Value, want)
		}
		if got[i].Category != CatFiles {
			t.Errorf("rank %d category = %v, want CatFiles", i, got[i].Category)
		}
	}
}
