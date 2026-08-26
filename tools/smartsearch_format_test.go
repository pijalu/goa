package tools

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/ansi"
	"github.com/pijalu/goa/tools/search/bm25"
)

// --- Byte-identical oracle -------------------------------------------------
//
// The ref* helpers below are verbatim copies of SmartSearchTool.formatResults
// as it existed BEFORE the decomposition refactor (see git history). They share
// no code with the refactored formatter — splitting them along the same seams
// (header / result / match line / relative path) preserves that independence,
// because only the surrounding plumbing differs while every format verb and
// byte of literal text stays a faithful duplicate. If the refactored formatter
// drifts at all, TestSmartSearchFormatResults_ByteIdentical fails.

// refRelPath is the verbatim pre-refactor relative-path computation: relativise
// against the project dir only when it is known and the path stays inside it.
func refRelPath(t *SmartSearchTool, abs string) string {
	refRel := abs
	if t.ProjectDir != "" {
		if p, err := filepath.Rel(t.ProjectDir, abs); err == nil && !strings.HasPrefix(p, "..") {
			refRel = p
		}
	}
	return refRel
}

// refMatchLine renders one matching source line verbatim: sanitised,
// space-trimmed, truncated to 140 display columns with a trailing ellipsis.
func refMatchLine(m smartLineMatch) string {
	content := ansi.Sanitize(strings.TrimSpace(m.Text))
	if ansi.Width(content) > 140 {
		content = ansi.Truncate(content, 140) + "…"
	}
	return fmt.Sprintf("    %d: %s\n", m.Num, content)
}

// refOneResult renders one numbered result exactly as the pre-refactor loop
// body did: rank, score, relative path, line count, optional chunk tag, then
// the matching lines.
func refOneResult(t *SmartSearchTool, i int, r bm25.SearchResult, lineMatches []smartLineMatch) string {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "%d. [%.2f] %s  (%d lines)", i+1, r.Score, refRelPath(t, r.Path), r.Lines)
	if r.ID != "" {
		fmt.Fprintf(&buf, " [id: %s, lines: %d-%d, language: %s]", r.ID, r.StartLine, r.EndLine, r.Language)
	}
	buf.WriteByte('\n')
	for _, m := range lineMatches {
		buf.WriteString(refMatchLine(m))
	}
	return buf.String()
}

// refHeader renders the rebuild warning, the result-count summary line and the
// score range, exactly as the pre-refactor formatter opened its output.
func refHeader(results []bm25.SearchResult, query string, idx *bm25.Index, rebuilt bool) string {
	var buf bytes.Buffer
	if rebuilt {
		fmt.Fprintf(&buf, "[smartsearch: %q] — Index was missing or corrupted; rebuilt from scratch.\n", query)
	}
	fmt.Fprintf(&buf, "[smartsearch: %q] — %d results from %d indexed files (index age: %s)\n",
		query, len(results), idx.FileCount(), formatDuration(idx.IndexAge()))

	// Score range for user orientation.
	if len(results) > 0 {
		fmt.Fprintf(&buf, "Score range: %.2f – %.2f\n\n", results[0].Score, results[len(results)-1].Score)
	}
	return buf.String()
}

// refFormatResults recomposes the original single-function formatResults byte
// stream from the verbatim oracle helpers.
func refFormatResults(t *SmartSearchTool, results []bm25.SearchResult, matches map[string][]smartLineMatch, query string, idx *bm25.Index, rebuilt bool, idxDir string) string {
	var buf bytes.Buffer
	buf.WriteString(refHeader(results, query, idx, rebuilt))
	for i, r := range results {
		buf.WriteString(refOneResult(t, i, r, matches[r.Path]))
	}
	if idxDir != "" {
		fmt.Fprintf(&buf, "\n(Index: %s)", filepath.Join(idxDir, bm25.IndexFile))
	}
	return buf.String()
}

// --- Tests ------------------------------------------------------------------

// volatileFields normalises values that tick between the production call and
// the oracle call. Only "(index age: …)" matches this pattern; every other
// byte of output is compared verbatim. The header samples idx.IndexAge() on
// the live clock, so calling it twice in a row inevitably yields different
// durations even for byte-identical formatters.
var volatileFields = regexp.MustCompile(`\(index age: [^)]*\)`)

// assertByteIdentical runs one structural scenario through both formatter
// variants across every combination of query, rebuild flag and index-dir
// visibility for a single tool projection, failing on any drift outside the
// volatile-field allowlist.
func assertByteIdentical(t *testing.T, tl *SmartSearchTool, idx *bm25.Index, sc formatScenario) {
	t.Helper()
	const tempIdxDir = "/tmp/.goa/smartsearch"
	for _, q := range []string{"", "greet function"} {
		for _, rebuilt := range []bool{false, true} {
			for _, idxD := range []string{"", tempIdxDir} {
				got := tl.formatResults(sc.results, sc.matches, q, idx, rebuilt, idxD)
				want := refFormatResults(tl, sc.results, sc.matches, q, idx, rebuilt, idxD)
				if normGot, normWant := volatileFields.ReplaceAllString(got, "AGE"), volatileFields.ReplaceAllString(want, "AGE"); normGot != normWant {
					t.Fatalf("formatResults drifted (q=%q rebuilt=%v idxDir=%q):\n got: %q\nwant: %q", q, rebuilt, idxD, got, want)
				}
			}
		}
	}
}

// formatScenario is one structural input shape fed to both formatter variants.
type formatScenario struct {
	name    string
	results []bm25.SearchResult
	matches map[string][]smartLineMatch
}

// formatByteIdenticalScenarios builds the structural input shapes: empty output,
// a single result with simple evidence, a mixed inside/outside trio carrying the
// full edge-line battery (whitespace, 140-column boundary, over-width, escape
// and control characters, wide rune), and the same trio without any evidence.
func formatByteIdenticalScenarios(src, outside string) []formatScenario {
	exact140 := strings.Repeat("a", 140)
	greet := `func greet() string { return "hi" }`
	edgeResults := []bm25.SearchResult{
		{Path: src, Score: 0.87, Lines: 150, ID: "main.go::1-2", StartLine: 1, EndLine: 2, Language: "go"},
		{Path: filepath.Join(outside, "other.py"), Score: 0.42, Lines: 151},
		{Path: "/etc/passwd", Score: 0.01, Lines: 3, ID: "passwd::1-3", StartLine: 1, EndLine: 3, Language: "conf"},
	}
	edgeMatches := map[string][]smartLineMatch{src: {
		{Num: 1, Text: "  \tpackage main  "},           // TrimSpace + tab expansion
		{Num: 2, Text: exact140},                       // exactly 140 columns → kept intact
		{Num: 3, Text: exact140 + "a"},                 // 141 columns → truncated + ellipsis
		{Num: 4, Text: "\x1b[2Kred"},                   // ESC sanitised to literal \e
		{Num: 5, Text: "null\x00byte"},                 // control char → '?'
		{Num: 6, Text: strings.Repeat("a", 139) + "世"}, // wide rune pushes width to 141
	}}
	return []formatScenario{
		{name: "empty"},
		{name: "single_inside_with_evidence",
			results: []bm25.SearchResult{{Path: src, Score: 1.0, Lines: 2}},
			matches: map[string][]smartLineMatch{src: {{Num: 2, Text: greet}}},
		},
		{name: "mixed_paths_edge_lines", results: edgeResults, matches: edgeMatches},
		{name: "mixed_paths_no_evidence", results: edgeResults},
	}
}

// TestSmartSearchFormatResults_ByteIdentical drives the refactored formatter
// and the pre-refactor oracle over every combination of structural scenario,
// tool projection, query, rebuild flag and index-dir visibility, asserting the
// produced bytes never drifted.
func TestSmartSearchFormatResults_ByteIdentical(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	src := filepath.Join(dir, "main.go")
	if err := os.WriteFile(src, []byte("package main\n"+`func greet() string { return "hi" }`+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	withProject := &SmartSearchTool{ProjectDir: dir}
	idx, _, err := withProject.getOrBuildIndex(dir)
	if err != nil {
		t.Fatalf("build index: %v", err)
	}

	for _, sc := range formatByteIdenticalScenarios(src, outside) {
		sc := sc
		t.Run(sc.name, func(t *testing.T) {
			for _, tl := range []*SmartSearchTool{withProject, {}} {
				assertByteIdentical(t, tl, idx, sc)
			}
		})
	}
}

// TestSmartSearchRelPath_GuardsOutsideProject verifies relPath relativises
// paths inside the project but keeps anything outside — or when no project
// dir is known — absolute.
func TestSmartSearchRelPath_GuardsOutsideProject(t *testing.T) {
	project := t.TempDir()
	outside := t.TempDir()
	tool := &SmartSearchTool{ProjectDir: project}

	inside := filepath.Join(project, "internal", "deep", "x.go")
	if got, want := tool.relPath(inside), filepath.Join("internal", "deep", "x.go"); got != want {
		t.Fatalf("inside path not relativised:\n got: %q\nwant: %q", got, want)
	}

	// A sibling temp dir escapes the project via "..".
	escape := filepath.Join(outside, "evil.go")
	if got := tool.relPath(escape); got != escape {
		t.Fatalf("outside path must stay absolute:\n got: %q\nwant: %q", got, escape)
	}

	// A lexically crafted traversal must not be relativised either.
	traversal := filepath.Join(project, "..", "..", "escape.go")
	if got := tool.relPath(traversal); got != traversal {
		t.Fatalf("traversing path must stay absolute:\n got: %q\nwant: %q", got, traversal)
	}

	// No project dir configured → everything stays absolute.
	noProject := &SmartSearchTool{}
	abs := filepath.Join(project, "y.go")
	if got := noProject.relPath(abs); got != abs {
		t.Fatalf("without ProjectDir the path must stay absolute:\n got: %q\nwant: %q", got, abs)
	}
}

// TestSmartSearchFormatMatchLine_Exact140Boundary pins the truncation
// boundary: exactly 140 display columns pass through untouched, one more
// column triggers truncation to 140 plus the ellipsis.
func TestSmartSearchFormatMatchLine_Exact140Boundary(t *testing.T) {
	exact140 := strings.Repeat("a", 140)
	if got, want := formatMatchLine(smartLineMatch{Num: 1, Text: exact140}), "    1: "+exact140+"\n"; got != want {
		t.Fatalf("exact-width line must not be truncated:\n got: %q\nwant: %q", got, want)
	}

	over := strings.Repeat("a", 141)
	if got, want := formatMatchLine(smartLineMatch{Num: 2, Text: over}), "    2: "+strings.Repeat("a", 140)+"…\n"; got != want {
		t.Fatalf("over-width line must truncate to 140 + ellipsis:\n got: %q\nwant: %q", got, want)
	}

	// Wide rune counts as two columns and is never split mid-cluster: 139
	// ASCII + '世' (141 columns) truncates to the 139 fitting ASCII columns.
	wide := strings.Repeat("a", 139) + "世"
	if w := ansi.Width(wide); w != 141 {
		t.Fatalf("precondition failed: expected width 141, got %d", w)
	}
	if got, want := formatMatchLine(smartLineMatch{Num: 3, Text: wide}), "    3: "+strings.Repeat("a", 139)+"…\n"; got != want {
		t.Fatalf("wide-rune overflow must drop the unbreakable cluster:\n got: %q\nwant: %q", got, want)
	}

	// Exactly 140 columns made of a wide rune: 138 ASCII + '世'.
	wideExact := strings.Repeat("a", 138) + "世"
	if w := ansi.Width(wideExact); w != 140 {
		t.Fatalf("precondition failed: expected width 140, got %d", w)
	}
	if got, want := formatMatchLine(smartLineMatch{Num: 4, Text: wideExact}), "    4: "+wideExact+"\n"; got != want {
		t.Fatalf("exact-width wide-rune line must not be truncated:\n got: %q\nwant: %q", got, want)
	}
}
