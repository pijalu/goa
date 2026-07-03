package tools

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/pijalu/goa/internal/ansi"
	"github.com/pijalu/goa/internal/tuirender"
)

// TestSmartSearchTool_CorruptedIndexRebuilt verifies that the smartsearch tool
// detects a corrupted index file, removes it, rebuilds from scratch, and reports
// the rebuild in the result.
func TestSmartSearchTool_CorruptedIndexRebuilt(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package main\nfunc main() {}\n"), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	tool := &SmartSearchTool{ProjectDir: dir}

	// First call builds the index.
	res1, err := tool.Execute(`{"query": "main function"}`)
	if err != nil {
		t.Fatalf("first smartsearch: %v", err)
	}
	if strings.Contains(res1, "corrupted") {
		t.Errorf("unexpected corrupted note on first call: %s", res1)
	}

	// Corrupt the index on disk.
	idxPath := filepath.Join(dir, ".goa", "smartsearch", "index.gob")
	if err := os.WriteFile(idxPath, []byte("not a valid gob"), 0644); err != nil {
		t.Fatalf("corrupt index: %v", err)
	}

	// Next call should detect corruption, rebuild, and report it.
	res2, err := tool.Execute(`{"query": "main function"}`)
	if err != nil {
		t.Fatalf("second smartsearch after corruption: %v", err)
	}
	if !strings.Contains(res2, "Index was missing or corrupted") {
		t.Errorf("expected rebuild note in result, got: %s", res2)
	}
	if !strings.Contains(res2, "a.go") {
		t.Errorf("expected search results after rebuild, got: %s", res2)
	}
}

// TestSmartSearchTool_ConcurrentCallsDoNotCorruptIndex verifies that multiple
// concurrent smartsearch calls on the same project produce valid results without
// tripping over the same temp file path.
func TestSmartSearchTool_ConcurrentCallsDoNotCorruptIndex(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []string{"a.go", "b.go", "c.go"} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("package main\nfunc main() {}\n"), 0644); err != nil {
			t.Fatalf("write file: %v", err)
		}
	}

	tool := &SmartSearchTool{ProjectDir: dir}

	var wg sync.WaitGroup
	errs := make(chan error, 4)
	results := make(chan string, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := tool.Execute(`{"query": "main function"}`)
			if err != nil {
				errs <- err
				return
			}
			results <- res
		}()
	}
	wg.Wait()
	close(errs)
	close(results)

	for err := range errs {
		t.Errorf("concurrent smartsearch failed: %v", err)
	}
	for res := range results {
		if !strings.Contains(res, "results from") {
			t.Errorf("unexpected result: %s", res)
		}
	}

	// Ensure no stale temp files are left behind.
	idxDir := filepath.Join(dir, ".goa", "smartsearch")
	entries, err := os.ReadDir(idxDir)
	if err != nil {
		t.Fatalf("read index dir: %v", err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("stale temp file left behind: %s", e.Name())
		}
	}
}

// TestSmartSearchTool_ReturnsMatchingLines verifies that smartsearch surfaces
// the matching source lines (like the normal search tool) so the agent can act
// on results, not just file paths. The most-relevant candidate is grepped first.
func TestSmartSearchTool_ReturnsMatchingLines(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "auth.go"),
		[]byte("package main\n\nfunc authenticateUser(token string) error {\n\treturn nil\n}\n"), 0644); err != nil {
		t.Fatalf("write auth.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "other.go"),
		[]byte("package main\n\nfunc unrelated() {}\n"), 0644); err != nil {
		t.Fatalf("write other.go: %v", err)
	}

	tool := &SmartSearchTool{ProjectDir: dir}
	res, err := tool.Execute(`{"query": "authenticate user"}`)
	if err != nil {
		t.Fatalf("smartsearch: %v", err)
	}
	if !strings.Contains(res, "auth.go") {
		t.Fatalf("expected auth.go in results: %s", res)
	}
	// The matching line must be surfaced with its line number and content.
	if !strings.Contains(res, "authenticateUser") {
		t.Errorf("expected matching line content in results: %s", res)
	}
	// The "3:" prefix indicates line-numbered output, mirroring the search tool.
	if !strings.Contains(res, "3: ") {
		t.Errorf("expected a line-numbered match (\"3: ...\"), got: %s", res)
	}
}

// TestExtractQueryTerms_DedupesAndFilters confirms query term extraction uses
// the code tokenizer and deduplicates.
func TestExtractQueryTerms_DedupesAndFilters(t *testing.T) {
	terms := extractQueryTerms("User user authentication")
	seen := map[string]bool{}
	for _, term := range terms {
		if seen[term] {
			t.Errorf("duplicate term: %q", term)
		}
		seen[term] = true
	}
	if !seen["user"] || !seen["authentication"] {
		t.Errorf("expected user+authentication, got %v", terms)
	}
}

// TestSmartSearchRenderer_MatchingLines verifies that matching source lines
// from the tool output are rendered by the SmartSearchRenderer.
func TestSmartSearchRenderer_MatchingLines(t *testing.T) {
	r := &SmartSearchRenderer{}
	// Simulate the smartsearch tool output with matching lines.
	output := `[smartsearch: "authenticate user"] — 1 results from 2 indexed files (index age: 0s)
Score range: 1.00 – 1.00

1. [1.00] auth.go  (5 lines)
    3: func authenticateUser(token string) error {
    4:     return nil
`

	result := r.RenderResult(output, tuirender.RenderContext{})
	// Strip ANSI for assertion clarity.
	clean := ansi.Strip(result)
	if !strings.Contains(clean, "authenticateUser") {
		t.Errorf("expected matching line content in rendered output:\n%s", clean)
	}
	if !strings.Contains(clean, "auth.go") {
		t.Errorf("expected file header in rendered output:\n%s", clean)
	}
	if !strings.Contains(clean, "3  ") {
		t.Errorf("expected line number in rendered output:\n%s", clean)
	}
}
