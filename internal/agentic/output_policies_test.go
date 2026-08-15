// SPDX-License-Identifier: GPL-3.0-or-later

package agentic

import (
	"strings"
	"testing"
)

// --- Semantic output budgeting (P3) category tests ---

// outputBudgetCase is one table-driven P3 scenario.
type outputBudgetCase struct {
	name   string
	tool   string
	output string
	limit  int
	// wantSub asserts the budgeted result contains this substring.
	wantSub string
	// wantNotSub asserts the budgeted result does NOT contain this substring.
	wantNotSub string
}

// runBudgetCase applies truncateToolResult and checks the byte budget, the
// omission marker, and the category-relevant substring assertions.
func runBudgetCase(t *testing.T, tc outputBudgetCase) {
	t.Helper()
	got := truncateToolResult(tc.tool, tc.output, tc.limit)
	if len(got) > tc.limit {
		t.Errorf("byte budget violated: result %d bytes > limit %d", len(got), tc.limit)
	}
	if !strings.Contains(got, "[goa-system]") {
		t.Errorf("result should carry an omission marker, got %q", got)
	}
	if tc.wantSub != "" && !strings.Contains(got, tc.wantSub) {
		t.Errorf("result missing required content %q:\n%s", tc.wantSub, got)
	}
	if tc.wantNotSub != "" && strings.Contains(got, tc.wantNotSub) {
		t.Errorf("result should not contain %q:\n%s", tc.wantNotSub, got)
	}
}

// TestSemanticBudget_DefaultCategory exercises the default head/tail budget:
// webfetch keeps the beginning and the end, elides the bulk middle.
func TestSemanticBudget_DefaultCategory(t *testing.T) {
	got := truncateToolResult("webfetch", "HEAD\n"+strings.Repeat("middle\n", 100)+"TAIL", 200)
	if !strings.Contains(got, "HEAD") || !strings.Contains(got, "TAIL") {
		t.Errorf("default budget should keep head and tail, got %q", got)
	}
	if !strings.Contains(got, "[goa-system]") {
		t.Error("default budget should carry an omission marker")
	}
	if len(got) >= 709 {
		t.Errorf("default budget result not smaller: %d", len(got))
	}
	// A tiny cap still respects the byte budget.
	got = truncateToolResult("webfetch", "AAA\n"+strings.Repeat("m\n", 200)+"ZZZ", 100)
	if !strings.Contains(got, "AAA") || !strings.Contains(got, "ZZZ") {
		t.Errorf("default budget should keep head and tail at tiny cap, got %q", got)
	}
}

// TestSemanticBudget_FileCategory verifies read results keep the header line
// and both the beginning and the end of the window survive.
func TestSemanticBudget_FileCategory(t *testing.T) {
	var lines []string
	lines = append(lines, "file.go (lines 1-120)") // header
	for i := 0; i < 60; i++ {
		lines = append(lines, "line "+string(rune('A'+i%26)))
	}
	output := strings.Join(lines, "\n")

	got := truncateToolResult("read", output, 300)
	if !strings.Contains(got, "file.go (lines 1-120)") {
		t.Errorf("read budget should keep the header line, got %q", got)
	}
	if !strings.Contains(got, "line A") || !strings.Contains(got, "line B") {
		t.Errorf("read budget should keep the beginning lines, got %q", got)
	}
	// The end of the window must survive (alternating ends).
	last := lines[len(lines)-1]
	if !strings.Contains(got, last) {
		t.Errorf("read budget should keep the final line %q, got %q", last, got)
	}
	// Some middle content must have been elided (the budget is far smaller
	// than the 61 lines).
	if len(got) >= len(output) {
		t.Error("read budget result should be smaller than the original")
	}
}

// TestSemanticBudget_ProcessCategory verifies bash results keep the first
// lines, diagnostic lines, and the tail, with consecutive repeats collapsed.
func TestSemanticBudget_ProcessCategory(t *testing.T) {
	var lines []string
	for i := 0; i < 30; i++ {
		lines = append(lines, "progress tick")
	}
	lines = append(lines, "error: command failed with exit status 1")
	for i := 0; i < 20; i++ {
		lines = append(lines, "detail line "+string(rune('a'+i)))
	}
	output := strings.Join(lines, "\n")

	runBudgetCase(t, outputBudgetCase{
		name:       "bash first/diagnostics/tail",
		tool:       "bash",
		output:     output,
		limit:      400,
		wantSub:    "error: command failed with exit status 1",
		wantNotSub: "detail line d",
	})

	// Consecutive duplicate lines must collapse into a repeat marker. The first
	// bucket keeps only the first 10 of the 30 repeated ticks.
	got := truncateToolResult("bash", output, 400)
	if !strings.Contains(got, "(×10)") {
		t.Errorf("process budget should collapse consecutive repeats with a count, got %q", got)
	}
}

// TestSemanticBudget_TestCategory is the flagship acceptance: a test-run
// result whose failure lines sit in the MIDDLE (beyond any plain head/tail
// cut) must retain those failure lines plus context and tail summaries.
func TestSemanticBudget_TestCategory(t *testing.T) {
	var lines []string
	lines = append(lines, "=== RUN   TestPassing")
	lines = append(lines, "--- PASS: TestPassing (0.01s)")
	for i := 0; i < 30; i++ {
		lines = append(lines, "noise line "+string(rune('a'+i)))
	}
	// The failure sits in the middle, far beyond a head/tail cut.
	lines = append(lines, "=== RUN   TestBroken")
	lines = append(lines, "--- FAIL: TestBroken (0.02s)")
	lines = append(lines, "    foo_test.go:42: expected value 7, got 3")
	lines = append(lines, "    foo_test.go:43: second assertion failed")
	for i := 0; i < 30; i++ {
		lines = append(lines, "more noise "+string(rune('m'+i)))
	}
	lines = append(lines, "FAIL")
	lines = append(lines, "FAIL\tgithub.com/example/pkg\t0.42s")
	output := strings.Join(lines, "\n")

	runBudgetCase(t, outputBudgetCase{
		name:       "verify middle failures retained",
		tool:       "verify",
		output:     output,
		limit:      600,
		wantSub:    "--- FAIL: TestBroken",
		wantNotSub: "noise line c",
	})
	// The failure context line must survive too.
	got := truncateToolResult("verify", output, 600)
	if !strings.Contains(got, "expected value 7, got 3") {
		t.Errorf("test budget should keep the failure assertion line, got %q", got)
	}
	if !strings.Contains(got, "FAIL\tgithub.com/example/pkg") {
		t.Errorf("test budget should keep the tail summary, got %q", got)
	}
}

// TestSemanticBudget_TestCategory_PassingRun falls back to head/tail when no
// failure markers are present.
func TestSemanticBudget_TestCategory_PassingRun(t *testing.T) {
	output := "=== RUN   TestA\n--- PASS: TestA\n" + strings.Repeat("noise\n", 30) + "ok  \tgithub.com/example/pkg\t0.01s\nPASS"
	got := truncateToolResult("verify", output, 50)
	if !strings.Contains(got, "PASS") {
		t.Errorf("passing test run should keep the tail summary, got %q", got)
	}
	if !strings.Contains(got, "[goa-system]") {
		t.Errorf("passing test run over budget should still carry an omission marker, got %q", got)
	}
}

// TestSemanticBudget_TestCategory_BashHeuristic verifies the content heuristic:
// a bash result that carries test-run markers is budgeted as a test result.
func TestSemanticBudget_TestCategory_BashHeuristic(t *testing.T) {
	var lines []string
	for i := 0; i < 30; i++ {
		lines = append(lines, "build noise "+string(rune('a'+i)))
	}
	lines = append(lines, "--- FAIL: TestIntegration")
	lines = append(lines, "    integ_test.go:10: connection refused")
	for i := 0; i < 30; i++ {
		lines = append(lines, "build noise "+string(rune('b'+i)))
	}
	output := strings.Join(lines, "\n")

	runBudgetCase(t, outputBudgetCase{
		name:       "bash go test output classified as test",
		tool:       "bash",
		output:     output,
		limit:      400,
		wantSub:    "--- FAIL: TestIntegration",
		wantNotSub: "build noise l", // beyond the failure's ±3 context and tail
	})
}

// TestSemanticBudget_SearchCategory is the per-file representative acceptance:
// for a search result, one line per matched file is retained across the file
// set before deep lines of one file.
func TestSemanticBudget_SearchCategory(t *testing.T) {
	var lines []string
	lines = append(lines, `[search: "findme"] — 8 matches across 3 files`)
	lines = append(lines, "first.go: 3 matches")
	for i := 0; i < 20; i++ {
		lines = append(lines, "  10/20/30")
		lines = append(lines, "  10: content in first file "+string(rune('a'+i)))
		lines = append(lines, "  20: more content")
	}
	lines = append(lines, "second.go: 3 matches")
	for i := 0; i < 20; i++ {
		lines = append(lines, "  5/6/7")
		lines = append(lines, "  5: content in second file "+string(rune('b'+i)))
		lines = append(lines, "  6: more content")
	}
	lines = append(lines, "third.go: 2 matches")
	lines = append(lines, "  1/2")
	lines = append(lines, "  1: content in third file")
	lines = append(lines, "  2: more content")
	output := strings.Join(lines, "\n")

	runBudgetCase(t, outputBudgetCase{
		name:    "search per-file representatives",
		tool:    "search",
		output:  output,
		limit:   400,
		wantSub: "first.go: 3 matches",
	})
	got := truncateToolResult("search", output, 400)
	// Every file's entry line (one line per file) must survive.
	for _, file := range []string{"first.go: 3 matches", "second.go: 3 matches", "third.go: 2 matches"} {
		if !strings.Contains(got, file) {
			t.Errorf("search budget should keep one line per file (%q), got:\n%s", file, got)
		}
	}
	// The global summary survives.
	if !strings.Contains(got, "8 matches across 3 files") {
		t.Errorf("search budget should keep the global summary, got %q", got)
	}
}

// TestSemanticBudget_SearchCategory_SmartSearch exercises the smartsearch file
// entry format (numbered score lines).
func TestSemanticBudget_SearchCategory_SmartSearch(t *testing.T) {
	var lines []string
	lines = append(lines, `[smartsearch: "query"] — 2 results from 2 indexed files (index age: 1s)`)
	lines = append(lines, "Score range: 0.90 – 0.50")
	lines = append(lines, "")
	lines = append(lines, "1. [0.90] path/a.go  (3 lines)")
	for i := 0; i < 15; i++ {
		lines = append(lines, "    4: match line in a "+string(rune('a'+i)))
	}
	lines = append(lines, "2. [0.50] path/b.go  (2 lines)")
	for i := 0; i < 15; i++ {
		lines = append(lines, "    9: match line in b "+string(rune('b'+i)))
	}
	output := strings.Join(lines, "\n")

	got := truncateToolResult("smartsearch", output, 400)
	if !strings.Contains(got, "1. [0.90] path/a.go") {
		t.Errorf("smartsearch budget should keep the first file entry, got %q", got)
	}
	if !strings.Contains(got, "2. [0.50] path/b.go") {
		t.Errorf("smartsearch budget should keep the second file entry (one per file), got %q", got)
	}
}

// TestSemanticBudget_ByteBudgetEnforced is the universal byte-budget
// acceptance: every category result stays within limit (marker reserved).
func TestSemanticBudget_ByteBudgetEnforced(t *testing.T) {
	for _, tc := range []outputBudgetCase{
		{tool: "read", output: strings.Repeat("file.go (lines 1-100)\nline text\n", 50)},
		{tool: "bash", output: strings.Repeat("cmd output line\n", 100) + "error: boom"},
		{tool: "verify", output: "=== RUN\nnoise\n--- FAIL: T\n    x.go:1: msg\nFAIL\n"},
		{tool: "search", output: "a.go: 5 matches\n  1: hit\nb.go: 2 matches\n  3: hit\n"},
		{tool: "webfetch", output: strings.Repeat("content line\n", 100)},
	} {
		for _, limit := range []int{50, 150, 500} {
			got := truncateToolResult(tc.tool, tc.output, limit)
			if len(got) > limit {
				t.Errorf("%s limit %d: result %d bytes > limit", tc.tool, limit, len(got))
			}
		}
	}
}

// TestSemanticBudget_RuneSafety verifies slicing never splits a multi-byte
// UTF-8 rune for any category.
func TestSemanticBudget_RuneSafety(t *testing.T) {
	output := "file.go (lines 1-100)\n" + strings.Repeat("界界界界\n", 50) + "tail 界"
	for _, tc := range []struct {
		tool  string
		limit int
	}{
		{"read", 120},
		{"bash", 120},
		{"verify", 120},
		{"search", 120},
		{"webfetch", 120},
	} {
		got := truncateToolResult(tc.tool, output, tc.limit)
		if !strings.Contains(got, "�") {
			continue
		}
		// A replacement character is only acceptable if it was in the input.
		if strings.Contains(output, "�") {
			continue
		}
		t.Errorf("%s limit %d: result contains a split rune (U+FFFD): %q", tc.tool, tc.limit, got)
	}
}

// TestSemanticBudget_UnderLimitUnchanged verifies results at or under the
// limit pass through untouched for every category.
func TestSemanticBudget_UnderLimitUnchanged(t *testing.T) {
	for _, tc := range []struct {
		tool  string
		limit int
	}{
		{"read", 100},
		{"bash", 100},
		{"verify", 100},
		{"search", 100},
		{"webfetch", 100},
	} {
		out := "short result\nsecond line"
		if got := truncateToolResult(tc.tool, out, tc.limit); got != out {
			t.Errorf("%s under-limit result changed: %q", tc.tool, got)
		}
	}
}
