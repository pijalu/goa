package tools

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
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
