// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package filefind

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// seedIssue8Tree creates a tree mirroring Issue 8: an exact plan.md among
// many PLAN-*.md fuzzy matches, plus nested dirs for drill-down tests.
//
//	root/
//	  plans/plan.md, PLAN-00-TEST-INFRA.md, ... (files)
//	  plans/sub/nested.md
//	  src/main.go, src/util.go
//	  README.md
func seedIssue8Tree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"plans/plan.md":               "x",
		"plans/planner.md":            "x",
		"plans/PLAN-00-TEST-INFRA.md": "x",
		"plans/PLAN-01-AFFINITY.md":   "x",
		"plans/sub/nested.md":         "x",
		"src/main.go":                 "x",
		"src/util.go":                 "x",
		"README.md":                   "x",
	}
	for rel, content := range files {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// paths extracts just the entry paths from candidates.
func paths(entries []Entry) []string {
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.Path
	}
	return out
}

func containsPath(entries []Entry, want string) bool {
	for _, p := range paths(entries) {
		if p == want {
			return true
		}
	}
	return false
}

// TestComplete_Fallback covers the os.ReadDir fallback (forced via WithFD(false)):
// nesting, dirs-with-slash drill-down, exact-file suppression and ranking —
// the shared rules every consumer relies on. Scenarios are data-driven so the
// runner stays inside the complexity budget.
func TestComplete_Fallback(t *testing.T) {
	s := New(seedIssue8Tree(t)).WithFD(false)
	for _, tc := range []completeCase{
		{
			name:         "top level lists dirs with trailing slash",
			token:        "",
			wantContains: []string{"plans/", "src/", "README.md"},
		},
		{
			name:         "nesting drills into subdirectories",
			token:        "plans/sub/",
			wantContains: []string{"plans/sub/nested.md"},
		},
		{
			name:    "partial segment completes dir-relative",
			token:   "src/ma",
			wantAll: []string{"src/main.go"}, // exact list; files only
		},
		{
			name:         "dir candidate keeps trailing slash for drill-down",
			token:        "pl",
			wantContains: []string{"plans/"},
		},
		{
			name:     "exact existing file suppresses popup",
			token:    "plans/plan.md",
			wantNone: true,
		},
		// A partial token naming an existing file still completes; issue-8
		// ranking must put the exact basename first.
		{
			name:      "partial existing file still completes",
			token:     "plans/plan",
			wantFirst: "plans/plan.md",
		},
		{
			name:         "existing directory is not suppressed",
			token:        "plans",
			wantContains: []string{"plans/"},
		},
		{
			name:     "no match yields nothing",
			token:    "zzz",
			wantNone: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			checkCompletionCase(t, s.Complete(tc.token), tc)
		})
	}
}

// completeCase is one Complete() expectation against the fallback engine:
// wantNone / wantAll / wantContains / wantFirst are mutually exclusive knobs
// (checked in that order).
type completeCase struct {
	name         string
	token        string
	wantNone     bool
	wantAll      []string // exact ordered candidate paths
	wantContains []string // each must appear among the candidates
	wantFirst    string   // ≥1 candidate required; first must be this path
}

// checkCompletionCase asserts one fallback-completion scenario by delegating
// to the expectation-specific checker.
func checkCompletionCase(t *testing.T, got []Entry, tc completeCase) {
	t.Helper()
	switch {
	case tc.wantNone:
		assertNoCandidates(t, got, tc)
	case tc.wantAll != nil:
		assertExactCandidates(t, got, tc)
	case tc.wantFirst != "":
		assertFirstCandidate(t, got, tc)
	case len(tc.wantContains) > 0:
		assertContainsCandidates(t, got, tc)
	}
}

func assertNoCandidates(t *testing.T, got []Entry, tc completeCase) {
	t.Helper()
	if len(got) != 0 {
		t.Errorf("Complete(%q) = %v, want none", tc.token, paths(got))
	}
}

// assertExactCandidates pins the full ordered candidate list; every entry
// must be a file.
func assertExactCandidates(t *testing.T, got []Entry, tc completeCase) {
	t.Helper()
	if len(got) != len(tc.wantAll) {
		t.Fatalf("Complete(%q) = %v, want exactly %v", tc.token, paths(got), tc.wantAll)
	}
	for i, want := range tc.wantAll {
		if got[i].Path != want || got[i].IsDir {
			t.Errorf("candidate[%d] = %+v, want file %q", i, got[i], want)
		}
	}
}

// assertFirstCandidate requires at least one candidate and pins the top one.
func assertFirstCandidate(t *testing.T, got []Entry, tc completeCase) {
	t.Helper()
	if len(got) == 0 {
		t.Fatalf("Complete(%q) returned no candidates", tc.token)
	}
	if got[0].Path != tc.wantFirst {
		t.Errorf("first candidate = %q, want %q; all: %v", got[0].Path, tc.wantFirst, paths(got))
	}
}

func assertContainsCandidates(t *testing.T, got []Entry, tc completeCase) {
	t.Helper()
	for _, want := range tc.wantContains {
		if !containsPath(got, want) {
			t.Errorf("Complete(%q) = %v, missing %q", tc.token, paths(got), want)
		}
	}
}

// TestComplete_Fallback_Issue8Ranking: @-style ranking must put the exact
// basename first regardless of readdir order.
func TestComplete_Fallback_Issue8Ranking(t *testing.T) {
	root := seedIssue8Tree(t)
	got := New(root).WithFD(false).Complete("plans/plan")
	if len(got) == 0 {
		t.Fatal("no completions")
	}
	if got[0].Path != "plans/plan.md" {
		t.Errorf("first completion = %q, want plans/plan.md; all: %v", got[0].Path, paths(got))
	}
}

// TestRank covers the tier ordering directly: exact > case-sensitive prefix >
// case-insensitive prefix > fuzzy; shorter basenames first within a tier;
// alphabetical tie-break; stable for full ties. Mirrors the tui regression
// test over Completions — this is the same logic over Entries.
func TestRank(t *testing.T) {
	in := []Entry{
		{Path: "plans/PLAN-00-TEST-INFRA.md"},
		{Path: "plans/PLAN-01-AFFINITY.md"},
		{Path: "plans/plan.md"},
		{Path: "plans/planner.md"},
		{Path: "plans/sub/plan.md"},
	}
	wantOrder := []string{
		// "plan.md" is not an exact match for "plan" — both plan.md files are
		// typed-case prefixes; identical basenames keep input order.
		"plans/plan.md",
		"plans/sub/plan.md",
		"plans/planner.md",            // typed-case prefix
		"plans/PLAN-01-AFFINITY.md",   // case-insensitive prefix, shorter
		"plans/PLAN-00-TEST-INFRA.md", // case-insensitive prefix
	}
	got := Rank(in, "plan")
	if !reflect.DeepEqual(paths(got), wantOrder) {
		t.Errorf("Rank order = %v, want %v", paths(got), wantOrder)
	}

	exact := Rank([]Entry{{Path: "a/b"}, {Path: "a/b/c"}}, "b")
	if len(exact) != 2 || exact[0].Path != "a/b" {
		t.Errorf("exact tier not first: %v", paths(exact))
	}
}

// TestComplete_AbsoluteOutsideRoot: prefixes outside root stay absolute.
func TestComplete_AbsoluteOutsideRoot(t *testing.T) {
	root := seedIssue8Tree(t)
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "note.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	prefix := outside + "/no"
	got := New(root).Complete(prefix)
	if len(got) != 1 || got[0].Path != outside+"/note.txt" {
		t.Errorf("Complete(%q) = %v, want [%s/note.txt] absolute", prefix, paths(got), outside)
	}
}

// TestComplete_Cap: results are capped at maxResults after ranking.
func TestComplete_Cap(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < maxResults+10; i++ {
		name := filepath.Join(root, strings.Repeat("f", 3)+string(rune('a'+i%26))+string(rune('a'+i/26))+".txt")
		if err := os.WriteFile(name, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got := New(root).WithFD(false).Complete("fff")
	if len(got) != maxResults {
		t.Errorf("got %d candidates, want cap %d", len(got), maxResults)
	}
}

// fakeFD installs a shell script as the fd binary so fdSearch parsing,
// normalization and fallback-on-failure are testable without a real fd.
// It echoes lines relative to the --base-directory argument (real fd's
// contract); fail=1 makes it exit non-zero instead.
func fakeFD(t *testing.T, output string, exitCode int) {
	t.Helper()
	dir := t.TempDir()
	script := filepath.Join(dir, "fd")
	body := "#!/bin/sh\n"
	if exitCode != 0 {
		body += "exit " + string(rune('0'+exitCode)) + "\n"
	} else if output != "" {
		body += "cat <<'EOF'\n" + output + "\nEOF\n"
	}
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	oldAvail, oldPath := fdAvailable, fdPath
	fdAvailable, fdPath = true, script
	t.Cleanup(func() { fdAvailable, fdPath = oldAvail, oldPath })
}

// TestComplete_FDPath: with a stubbed fd, hits are normalized to
// project-relative paths, directories end with "/", and an fd failure falls
// back to os.ReadDir.
func TestComplete_FDPath(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh available for fd stub")
	}
	root := seedIssue8Tree(t)

	t.Run("hits normalize to root-relative with dir slash", func(t *testing.T) {
		// Real fd prints searchDir-relative lines: query "plan" under
		// root/plans prints every case-insensitive hit.
		fakeFD(t, "plan.md\nplanner.md\nPLAN-01-AFFINITY.md\nPLAN-00-TEST-INFRA.md", 0)
		got := New(root).WithFD(true).Complete("plans/plan")
		want := []string{
			"plans/plan.md",
			"plans/planner.md",
			"plans/PLAN-01-AFFINITY.md",
			"plans/PLAN-00-TEST-INFRA.md",
		}
		if !reflect.DeepEqual(paths(got), want) {
			t.Errorf("fd path ranked = %v, want %v", paths(got), want)
		}

		fakeFD(t, "sub", 0)
		dirs := New(root).WithFD(true).Complete("plans/su")
		if len(dirs) != 1 || dirs[0].Path != "plans/sub/" || !dirs[0].IsDir {
			t.Errorf("dir hit = %+v, want [{plans/sub/ true}]", dirs)
		}
	})

	t.Run("absolute hits outside root stay absolute", func(t *testing.T) {
		fakeFD(t, "/etc/hosts", 0)
		got := New(root).WithFD(true).Complete("/et")
		if !containsPath(got, "/etc/hosts") {
			t.Errorf("fd path = %v, want /etc/hosts kept absolute", paths(got))
		}
	})

	t.Run("fd failure falls back to ReadDir", func(t *testing.T) {
		fakeFD(t, "", 1)
		got := New(root).WithFD(true).Complete("src/ma")
		if len(got) != 1 || got[0].Path != "src/main.go" {
			t.Errorf("fallback after fd failure = %v, want [src/main.go]", paths(got))
		}
	})

	t.Run("empty fd output falls back to ReadDir", func(t *testing.T) {
		fakeFD(t, "", 0)
		got := New(root).WithFD(true).Complete("src/u")
		if len(got) != 1 || got[0].Path != "src/util.go" {
			t.Errorf("fallback after empty fd = %v, want [src/util.go]", paths(got))
		}
	})
}

// TestAvailable mirrors the package var (the cached PATH lookup).
func TestAvailable(t *testing.T) {
	old := fdAvailable
	defer func() { fdAvailable = old }()
	fdAvailable = true
	if !Available() {
		t.Error("Available() = false, want true")
	}
	fdAvailable = false
	if Available() {
		t.Error("Available() = true, want false")
	}
}

// TestPackageLevelComplete: Complete(root, prefix) applies suppression and
// ranking through the default fd availability.
func TestPackageLevelComplete(t *testing.T) {
	root := seedIssue8Tree(t)
	if got := Complete(root, "plans/plan.md"); len(got) != 0 {
		t.Errorf("Complete exact file = %v, want none", paths(got))
	}
	got := Complete(root, "README")
	if len(got) != 1 || got[0].Path != "README.md" {
		t.Errorf("Complete(\"README\") = %v, want [README.md]", paths(got))
	}
}
