// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"errors"
	"strings"
	"testing"
)

func TestFuzzyEdit_ExactMatch(t *testing.T) {
	file := "package main\n\nfunc main() {\n\tfmt.Println(\"hello\")\n}\n"
	oldStr := "\tfmt.Println(\"hello\")"
	newStr := "\tfmt.Println(\"world\")"

	res, err := fuzzyEdit(file, oldStr, newStr, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if res.MatchType != MatchExact {
		t.Errorf("MatchType = %q, want %q", res.MatchType, MatchExact)
	}
	if res.StartLine != 4 || res.EndLine != 4 {
		t.Errorf("StartLine/EndLine = %d/%d, want 4/4", res.StartLine, res.EndLine)
	}

	want := "package main\n\nfunc main() {\n\tfmt.Println(\"world\")\n}\n"
	if res.NewContent != want {
		t.Errorf("NewContent = %q, want %q", res.NewContent, want)
	}

	if !strContainsInDiff(res.Diff, `-	fmt.Println("hello")`) {
		t.Errorf("Diff missing removed line, got:\n%s", res.Diff)
	}
	if !strContainsInDiff(res.Diff, `+	fmt.Println("world")`) {
		t.Errorf("Diff missing added line, got:\n%s", res.Diff)
	}
}

func strContainsInDiff(diff, substr string) bool {
	for _, line := range strings.Split(diff, "\n") {
		if strings.TrimRight(line, "\r") == substr {
			return true
		}
	}
	return false
}

func TestFuzzyEdit_ExactDiffFormat(t *testing.T) {
	file := "package main\n\nfunc main() {\n\tfmt.Println(\"hello\")\n}\n"
	oldStr := "\tfmt.Println(\"hello\")"
	newStr := "\tfmt.Println(\"world\")"

	res, err := fuzzyEdit(file, oldStr, newStr, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := "@@ -1,6 +1,6 @@\n" +
		" package main\n" +
		" \n" +
		" func main() {\n" +
		"-\tfmt.Println(\"hello\")\n" +
		"+\tfmt.Println(\"world\")\n" +
		" }\n" +
		" "

	if res.Diff != want {
		t.Errorf("Diff = %q, want %q", res.Diff, want)
	}
}

func TestFuzzyEdit_AmbiguousExactMatch(t *testing.T) {
	file := "a\nb\na\n"

	_, err := fuzzyEdit(file, "a", "c", true)
	if !errors.Is(err, ErrAmbiguous) {
		t.Fatalf("err = %v, want ErrAmbiguous", err)
	}
}

func TestFuzzyEdit_NotFound(t *testing.T) {
	file := "foo\nbar\n"

	_, err := fuzzyEdit(file, "baz", "qux", true)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestFuzzyEdit_EmptyOldStr(t *testing.T) {
	_, err := fuzzyEdit("anything", "", "new", true)
	if !errors.Is(err, ErrEmptyOldStr) {
		t.Fatalf("err = %v, want ErrEmptyOldStr", err)
	}
}

func TestFuzzyEdit_NoChange(t *testing.T) {
	file := "foo\nbar\nbaz\n"

	_, err := fuzzyEdit(file, "bar", "bar", true)
	if !errors.Is(err, ErrNoChange) {
		t.Fatalf("err = %v, want ErrNoChange", err)
	}
}

func TestFuzzyEdit_TrailingWhitespaceNormalized(t *testing.T) {
	file := "func foo() {   \n\treturn 1\n}\n"
	oldStr := "func foo() {\n\treturn 1\n}"
	newStr := "func foo() {\n\treturn 2\n}"

	res, err := fuzzyEdit(file, oldStr, newStr, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if res.MatchType != MatchTrailingWhitespace {
		t.Errorf("MatchType = %q, want %q", res.MatchType, MatchTrailingWhitespace)
	}

	want := "func foo() {\n\treturn 2\n}\n"
	if res.NewContent != want {
		t.Errorf("NewContent = %q, want %q", res.NewContent, want)
	}
}

func TestFuzzyEdit_CRLFLineEndings(t *testing.T) {
	file := "line1\r\nline2\r\nline3\r\n"
	oldStr := "line2"
	newStr := "line2_modified"

	res, err := fuzzyEdit(file, oldStr, newStr, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if res.MatchType != MatchExact {
		t.Errorf("MatchType = %q, want %q", res.MatchType, MatchExact)
	}

	want := "line1\r\nline2_modified\r\nline3\r\n"
	if res.NewContent != want {
		t.Errorf("NewContent = %q, want %q", res.NewContent, want)
	}

	if !strings.Contains(res.Diff, "\r\n") {
		t.Errorf("Diff does not use CRLF line endings:\n%q", res.Diff)
	}
}

func TestFuzzyEdit_FuzzyIndentationIncrease(t *testing.T) {
	file := "package main\n\nfunc main() {\n\tx := 1\n\ty := 2\n\tfmt.Println(x + y)\n}\n"
	oldStr := "x := 1\ny := 2\nfmt.Println(x + y)"
	newStr := "x := 10\ny := 20\nfmt.Println(x * y)"

	res, err := fuzzyEdit(file, oldStr, newStr, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if res.MatchType != MatchFuzzy {
		t.Errorf("MatchType = %q, want %q", res.MatchType, MatchFuzzy)
	}

	want := "package main\n\nfunc main() {\n\tx := 10\n\ty := 20\n\tfmt.Println(x * y)\n}\n"
	if res.NewContent != want {
		t.Errorf("NewContent = %q, want %q", res.NewContent, want)
	}

	if res.StartLine != 4 || res.EndLine != 6 {
		t.Errorf("StartLine/EndLine = %d/%d, want 4/6", res.StartLine, res.EndLine)
	}
}

func TestFuzzyEdit_FuzzyIndentationWithLineInsertedAndDedent(t *testing.T) {
	file := "class Foo:\n" +
		"    def bar(self):\n" +
		"        if cond:\n" +
		"            do_a()\n" +
		"        do_b()\n"

	oldStr := "if cond:\n    do_a()\ndo_b()"
	newStr := "if cond:\n    do_a()\n    do_c()\ndo_b()"

	res, err := fuzzyEdit(file, oldStr, newStr, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if res.MatchType != MatchFuzzy {
		t.Errorf("MatchType = %q, want %q", res.MatchType, MatchFuzzy)
	}

	want := "class Foo:\n" +
		"    def bar(self):\n" +
		"        if cond:\n" +
		"            do_a()\n" +
		"            do_c()\n" +
		"        do_b()\n"

	if res.NewContent != want {
		t.Errorf("NewContent =\n%q\nwant\n%q", res.NewContent, want)
	}
}

func TestFuzzyEdit_FuzzyIndentationClampsToZero(t *testing.T) {
	file := "result = 1\nresult2 = 2\n"
	oldStr := "    result = 1"
	newStr := "  result = 10"

	res, err := fuzzyEdit(file, oldStr, newStr, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if res.MatchType != MatchFuzzy {
		t.Errorf("MatchType = %q, want %q", res.MatchType, MatchFuzzy)
	}

	want := "result = 10\nresult2 = 2\n"
	if res.NewContent != want {
		t.Errorf("NewContent = %q, want %q", res.NewContent, want)
	}
}

func TestFuzzyEdit_FuzzyInternalWhitespace(t *testing.T) {
	file := "func calc() {\n\tx  =  1\n\treturn x\n}\n"
	oldStr := "\tx = 1"
	newStr := "\tx = 2"

	res, err := fuzzyEdit(file, oldStr, newStr, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if res.MatchType != MatchFuzzy {
		t.Errorf("MatchType = %q, want %q", res.MatchType, MatchFuzzy)
	}

	want := "func calc() {\n\tx = 2\n\treturn x\n}\n"
	if res.NewContent != want {
		t.Errorf("NewContent = %q, want %q", res.NewContent, want)
	}
}

func TestFuzzyEdit_FuzzyAmbiguousMatch(t *testing.T) {
	file := "func a() {\n    return 1\n}\n\nfunc b() {\n\treturn 1\n}\n"

	_, err := fuzzyEdit(file, "return 1", "return 2", true)
	if !errors.Is(err, ErrAmbiguous) {
		t.Fatalf("err = %v, want ErrAmbiguous", err)
	}
}

func TestFuzzyEdit_MultiLineInsertion(t *testing.T) {
	file := "a\nb\nc\nd\ne\n"

	res, err := fuzzyEdit(file, "c", "c1\nc2\nc3", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := "a\nb\nc1\nc2\nc3\nd\ne\n"
	if res.NewContent != want {
		t.Errorf("NewContent = %q, want %q", res.NewContent, want)
	}

	if res.StartLine != 3 || res.EndLine != 3 {
		t.Errorf("StartLine/EndLine = %d/%d, want 3/3", res.StartLine, res.EndLine)
	}

	if !strings.Contains(res.Diff, "@@ -1,6 +1,8 @@") {
		t.Errorf("Diff hunk header incorrect, got:\n%s", res.Diff)
	}

	for _, l := range []string{"-c", "+c1", "+c2", "+c3"} {
		if !lineInDiff(res.Diff, l) {
			t.Errorf("Diff missing line %q, got:\n%s", l, res.Diff)
		}
	}
}

func TestFuzzyEdit_TrailingNewlineIsIgnoredOnInputs(t *testing.T) {
	file := "alpha\nbeta\ngamma\n"
	res, err := fuzzyEdit(file, "beta\n", "beta_modified\n", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := "alpha\nbeta_modified\ngamma\n"
	if res.NewContent != want {
		t.Errorf("NewContent = %q, want %q", res.NewContent, want)
	}
}

func TestFuzzyEdit_MultipleEditsAreIndependentlyApplicable(t *testing.T) {
	file := "if a {\n    doX()\n}\n"

	res1, err := fuzzyEdit(file, "doX()", "doY()", true)
	if err != nil {
		t.Fatalf("first edit failed: %v", err)
	}

	res2, err := fuzzyEdit(res1.NewContent, "doY()", "doZ()", true)
	if err != nil {
		t.Fatalf("second edit failed: %v", err)
	}

	want := "if a {\n    doZ()\n}\n"
	if res2.NewContent != want {
		t.Errorf("NewContent = %q, want %q", res2.NewContent, want)
	}
}

func TestFuzzyEdit_ExactOnly_WhenAllowFuzzIsFalse(t *testing.T) {
	// File has trailing spaces — without fuzz, exact match fails
	file := "func foo() {   \n\treturn 1\n}\n"

	_, err := fuzzyEdit(file, "func foo() {\n\treturn 1\n}", "", false)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound (exact only, no fuzzy)", err)
	}
}

func TestFuzzyEdit_ExactMatch_WhenAllowFuzzIsFalse(t *testing.T) {
	file := "func foo() {\n\treturn 1\n}\n"
	oldStr := "func foo() {\n\treturn 1\n}"
	newStr := "func foo() {\n\treturn 2\n}"

	res, err := fuzzyEdit(file, oldStr, newStr, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if res.MatchType != MatchExact {
		t.Errorf("MatchType = %q, want %q", res.MatchType, MatchExact)
	}

	want := "func foo() {\n\treturn 2\n}\n"
	if res.NewContent != want {
		t.Errorf("NewContent = %q, want %q", res.NewContent, want)
	}
}

// lineInDiff reports whether the diff contains a line whose content
// (ignoring the leading "+"/"-"/" " marker and any trailing CR) equals want.
func lineInDiff(diff, want string) bool {
	for _, line := range strings.Split(diff, "\n") {
		line = strings.TrimSuffix(line, "\r")
		if len(line) == 0 {
			continue
		}
		if line == want {
			return true
		}
	}
	return false
}
