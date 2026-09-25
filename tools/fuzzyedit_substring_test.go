// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"errors"
	"strings"
	"testing"
)

// sessionLine744 is the byte-for-byte shape of the line that produced the
// spurious not_found in the bug report (export goa-export-20260925-112854,
// docs/care-expert.md line 744 as the session saw it): a ~1.2 KB Markdown
// table row carrying multi-byte characters (① ② — plus typographic quotes).
const sessionLine744 = `| **Day plan** | ` + "`/care_plan`" + ` (also replaces/augments ` + "`/feeding`" + `) | caretakers | **"What should I do next"** — primary work surface. Two view modes: **① Action-first** (default): items sorted by urgency — overdue/missing first, then due-now, then upcoming — each item is an actionable card with **Apply**/**Defer**/**Skip** one-click buttons, animal name + cage, action description, and status badge. **② Timeline**: chronological trace grouped by zone (existing ` + "`AnimalByZoneMap`" + ` convention) then time — for review and shift handover. Status badges (due/late/missing/applied/skipped); filters by action kind, zone, animal; counts header. |`

// sessionAnchor744 is the exact old_string the failing session sent: an
// intra-line substring of sessionLine744, never a whole line.
const sessionAnchor744 = `each item is an actionable card with **Apply**/**Defer**/**Skip** one-click buttons, animal name + cage, action description, and status badge.`

// exportAnchor744 is the same anchor as recorded in the export (the drifted
// file had dropped **Defer**, the export variant still carries **Skip** only).
const exportAnchor744 = `each item is an actionable card with **Apply**/**Skip** one-click buttons, animal name + cage, action description, and status badge.`

// exportLine744 is the export-era line: sessionLine744 without **Defer**.
var exportLine744 = strings.Replace(sessionLine744, "/**Defer**", "", 1)

func TestSubstringEdit_SessionRepro(t *testing.T) {
	file := "| Page | Route |\n|---|---|\n" + sessionLine744 + "\n| **Animal plan tab** | x | y | z |\n"

	res, err := fuzzyEdit(file, sessionAnchor744, "ANCHOR-REPLACED", true)
	if err != nil {
		t.Fatalf("session anchor must match now, got error: %v", err)
	}
	if res.MatchType != MatchExactSubstring {
		t.Errorf("MatchType = %q, want %q", res.MatchType, MatchExactSubstring)
	}
	if res.StartLine != 3 || res.EndLine != 3 {
		t.Errorf("StartLine/EndLine = %d/%d, want 3/3", res.StartLine, res.EndLine)
	}

	want := strings.Replace(file, sessionAnchor744, "ANCHOR-REPLACED", 1)
	if res.NewContent != want {
		t.Errorf("NewContent mismatch\n got: %q\nwant: %q", res.NewContent, want)
	}
	// The untouched prefix/suffix of the ~1.2 KB line must survive exactly.
	prefixed := `| **Day plan** | ` + "`/care_plan`" + ` (also replaces/augments ` + "`/feeding`" + `) | caretakers | **"What should I do next"** — primary work surface. Two view modes: **① Action-first** (default): items sorted by urgency — overdue/missing first, then due-now, then upcoming — `
	if !strings.HasPrefix(res.NewContent, "| Page | Route |\n|---|---|\n"+prefixed) {
		t.Errorf("multi-byte prefix not preserved byte-for-byte")
	}
	if !strings.Contains(res.NewContent, "ANCHOR-REPLACED **② Timeline**") {
		t.Errorf("multi-byte suffix not preserved byte-for-byte:\n%q", res.NewContent)
	}
	if !strContainsInDiff(res.Diff, "-"+sessionLine744) {
		t.Errorf("diff missing the full original line, got:\n%s", res.Diff)
	}
	if !strContainsInDiff(res.Diff, "+"+strings.Replace(sessionLine744, sessionAnchor744, "ANCHOR-REPLACED", 1)) {
		t.Errorf("diff missing the spliced line, got:\n%s", res.Diff)
	}
}

func TestSubstringEdit_ExportAnchor(t *testing.T) {
	res, err := fuzzyEdit(exportLine744+"\n", exportAnchor744, exportAnchor744+" **Apply is one tap.**", true)
	if err != nil {
		t.Fatalf("export anchor must match, got: %v", err)
	}
	if res.MatchType != MatchExactSubstring {
		t.Errorf("MatchType = %q, want %q", res.MatchType, MatchExactSubstring)
	}
	want := strings.Replace(exportLine744+"\n", exportAnchor744, exportAnchor744+" **Apply is one tap.**", 1)
	if res.NewContent != want {
		t.Errorf("NewContent mismatch\n got: %q\nwant: %q", res.NewContent, want)
	}
}

func TestSubstringEdit_MultiByteAnchorOffsets(t *testing.T) {
	// ① and the surrounding letters are multi-byte; byte offsets must not drift.
	file := "αα ① Action-first ββ\nnext\n"
	res, err := fuzzyEdit(file, "① Action-first", "① Timeline-view", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.MatchType != MatchExactSubstring {
		t.Errorf("MatchType = %q, want %q", res.MatchType, MatchExactSubstring)
	}
	if res.NewContent != "αα ① Timeline-view ββ\nnext\n" {
		t.Errorf("NewContent = %q", res.NewContent)
	}
	if res.StartLine != 1 || res.EndLine != 1 {
		t.Errorf("StartLine/EndLine = %d/%d, want 1/1", res.StartLine, res.EndLine)
	}
}

func TestSubstringEdit_MultiLineNewStringExpandsLine(t *testing.T) {
	file := "aaa\nbbb anchor ccc\nddd\n"
	res, err := fuzzyEdit(file, "anchor", "X\nY", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.MatchType != MatchExactSubstring {
		t.Errorf("MatchType = %q, want %q", res.MatchType, MatchExactSubstring)
	}
	// A single matched line is replaced by two lines; EndLine still refers to
	// the original (single-line) region.
	if res.StartLine != 2 || res.EndLine != 2 {
		t.Errorf("StartLine/EndLine = %d/%d, want 2/2", res.StartLine, res.EndLine)
	}
	if res.NewContent != "aaa\nbbb X\nY ccc\nddd\n" {
		t.Errorf("NewContent = %q", res.NewContent)
	}
}

func TestSubstringEdit_CRLFPreserved(t *testing.T) {
	file := "aaa\r\nbbb anchor ccc\r\nddd\r\n"
	res, err := fuzzyEdit(file, "anchor", "ANCHOR", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.MatchType != MatchExactSubstring {
		t.Errorf("MatchType = %q, want %q", res.MatchType, MatchExactSubstring)
	}
	if res.NewContent != "aaa\r\nbbb ANCHOR ccc\r\nddd\r\n" {
		t.Errorf("NewContent = %q", res.NewContent)
	}
	if strings.Count(res.NewContent, "\n") != strings.Count(res.NewContent, "\r\n") {
		t.Errorf("lone LF introduced in CRLF file: %q", res.NewContent)
	}
	if strings.Contains(strings.ReplaceAll(res.Diff, "\r\n", ""), "\n") {
		t.Errorf("diff lost CRLF line endings:\n%q", res.Diff)
	}
}

func TestSubstringEdit_BoundaryLines(t *testing.T) {
	tests := []struct {
		name     string
		file     string
		old, nw  string
		want     string
		startLn  int
		matchTyp MatchType
	}{
		{
			name: "anchor on first line",
			file: "head anchor tail\nsecond\n",
			old:  "anchor", nw: "ANCHOR",
			want: "head ANCHOR tail\nsecond\n", startLn: 1,
		},
		{
			name: "anchor on last line without trailing newline",
			file: "first\nhead anchor tail",
			old:  "anchor", nw: "ANCHOR",
			want: "first\nhead ANCHOR tail", startLn: 2,
		},
		{
			name: "anchor on last line with trailing newline",
			file: "first\nhead anchor tail\n",
			old:  "anchor", nw: "ANCHOR",
			want: "first\nhead ANCHOR tail\n", startLn: 2,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res, err := fuzzyEdit(tc.file, tc.old, tc.nw, true)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if res.MatchType != MatchExactSubstring {
				t.Errorf("MatchType = %q, want %q", res.MatchType, MatchExactSubstring)
			}
			if res.NewContent != tc.want {
				t.Errorf("NewContent = %q, want %q", res.NewContent, tc.want)
			}
			if res.StartLine != tc.startLn {
				t.Errorf("StartLine = %d, want %d", res.StartLine, tc.startLn)
			}
		})
	}
}

func TestSubstringEdit_ErrorCases(t *testing.T) {
	const file = "alpha anchor beta\nsecond anchor here\n"

	tests := []struct {
		name    string
		file    string
		old     string
		nw      string
		wantErr error
	}{
		{
			name: "absent substring",
			file: file, old: "nowhere", nw: "x",
			wantErr: ErrNotFound,
		},
		{
			name: "ambiguous substring",
			file: file, old: "anchor", nw: "x",
			wantErr: ErrAmbiguous,
		},
		{
			name: "exact replacement is a no-op",
			file: "alpha anchor beta\n", old: "anchor", nw: "anchor",
			wantErr: ErrNoChange,
		},
		{
			name: "multi-line old_string is not substring-matched",
			file: "aaa\nbbb\n", old: "aaa\nzzz", nw: "ccc",
			wantErr: ErrNotFound,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res, err := fuzzyEdit(tc.file, tc.old, tc.nw, true)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			if res != nil {
				t.Errorf("expected nil result on error, got %+v", res)
			}
		})
	}
}

func TestSubstringEdit_AmbiguousReportsCount(t *testing.T) {
	_, err := fuzzyEdit("a anchor b\nc anchor d\ne anchor f\n", "anchor", "x", true)
	if !errors.Is(err, ErrAmbiguous) {
		t.Fatalf("err = %v, want ErrAmbiguous", err)
	}
	if !strings.Contains(err.Error(), "found 3 possible matches") {
		t.Errorf("error should report the occurrence count, got: %v", err)
	}
}

// TestSubstringEdit_DoesNotShadowExistingTiers proves the new tier is strictly
// additive: inputs that succeeded before keep their exact code path and result.
// Each subtest lives in its own helper so neither the test nor the helpers
// exceed the complexity budget.
func TestSubstringEdit_DoesNotShadowExistingTiers(t *testing.T) {
	cases := []struct {
		name string
		run  func(*testing.T)
	}{
		{"full-line anchor resolves as tier-1 exact", assertTier1ExactWins},
		{"whitespace-different anchor still resolves as fuzzy", assertFuzzyTierWins},
		{"whitespace-only difference resolves as trailing-whitespace", assertTrailingWhitespaceTierWins},
	}
	for _, tc := range cases {
		t.Run(tc.name, tc.run)
	}
}

// assertTier1ExactWins pins that a whole-line anchor keeps the exact-match tier.
func assertTier1ExactWins(t *testing.T) {
	res, err := fuzzyEdit("aaa\nbbb\nccc\n", "bbb", "BBB", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.MatchType != MatchExact {
		t.Errorf("MatchType = %q, want %q", res.MatchType, MatchExact)
	}
	if res.NewContent != "aaa\nBBB\nccc\n" {
		t.Errorf("NewContent = %q", res.NewContent)
	}
}

// assertFuzzyTierWins pins that an indentation-only difference still resolves
// through the fuzzy tier rather than the new substring tier.
func assertFuzzyTierWins(t *testing.T) {
	res, err := fuzzyEdit("  foo bar\n", "foo bar", "baz", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.MatchType != MatchFuzzy {
		t.Errorf("MatchType = %q, want %q", res.MatchType, MatchFuzzy)
	}
	if res.NewContent != "  baz\n" {
		t.Errorf("NewContent = %q", res.NewContent)
	}
}

// assertTrailingWhitespaceTierWins pins that a trailing-whitespace difference
// keeps resolving through the trailing-whitespace tier.
func assertTrailingWhitespaceTierWins(t *testing.T) {
	res, err := fuzzyEdit("foo bar   \nbaz\n", "foo bar", "FOO BAR", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.MatchType != MatchTrailingWhitespace {
		t.Errorf("MatchType = %q, want %q", res.MatchType, MatchTrailingWhitespace)
	}
}

// TestSubstringEdit_ExactOnlyMode documents that the new tier is an exact byte
// comparison: it works with fuzzing disabled, and it does not introduce any
// whitespace tolerance there.
func TestSubstringEdit_ExactOnlyMode(t *testing.T) {
	t.Run("intra-line anchor matches without fuzzing", func(t *testing.T) {
		res, err := fuzzyEdit("head anchor tail\n", "anchor", "ANCHOR", false)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.MatchType != MatchExactSubstring {
			t.Errorf("MatchType = %q, want %q", res.MatchType, MatchExactSubstring)
		}
		if res.NewContent != "head ANCHOR tail\n" {
			t.Errorf("NewContent = %q", res.NewContent)
		}
	})

	t.Run("no whitespace tolerance without fuzzing", func(t *testing.T) {
		if _, err := fuzzyEdit("foo   bar\n", "foo bar", "x", false); !errors.Is(err, ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})
}
