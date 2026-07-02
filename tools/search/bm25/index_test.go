package bm25

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// writeFile is a test helper that creates parent dirs and writes content to path.
func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}
}

// TestBuilder_SaveUniqueTempNames verifies that concurrent Save calls use
// distinct temp file names and do not collide.
func TestBuilder_SaveUniqueTempNames(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.go", "package main\nfunc main() {}\n")

	b := NewBuilder(dir, filepath.Join(dir, ".goa", "smartsearch"), nil)
	idx, err := b.buildFull()
	if err != nil {
		t.Fatalf("buildFull: %v", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := b.Save(idx); err != nil {
				t.Errorf("Save failed: %v", err)
			}
		}()
	}
	wg.Wait()

	loaded, err := b.Load()
	if err != nil {
		t.Fatalf("Load after concurrent saves: %v", err)
	}
	if loaded == nil {
		t.Fatal("expected loaded index after concurrent saves")
	}
	if loaded.FileCount() != 1 {
		t.Errorf("expected 1 file, got %d", loaded.FileCount())
	}

	// Ensure no stale temp files are left behind.
	entries, err := os.ReadDir(b.indexDir)
	if err != nil {
		t.Fatalf("read index dir: %v", err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("stale temp file left behind: %s", e.Name())
		}
	}
}

// TestBuilder_LoadCorruptedIndexRebuiltFromScratch verifies that a corrupted
// index file is detected by Load and a fresh build can replace it.
func TestBuilder_LoadCorruptedIndexRebuiltFromScratch(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.go", "package main\nfunc main() {}\n")

	b := NewBuilder(dir, filepath.Join(dir, ".goa", "smartsearch"), nil)
	idx, err := b.buildFull()
	if err != nil {
		t.Fatalf("buildFull: %v", err)
	}
	if err := b.Save(idx); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Corrupt the on-disk index.
	idxPath := filepath.Join(b.indexDir, IndexFile)
	if err := os.WriteFile(idxPath, []byte("not a valid gob"), 0644); err != nil {
		t.Fatalf("corrupt index: %v", err)
	}

	loaded, err := b.Load()
	if err == nil {
		t.Fatal("expected Load to fail on corrupted index")
	}
	if loaded != nil {
		t.Fatal("expected nil index on corrupted load")
	}

	// Rebuilding from scratch should succeed and produce a valid index.
	fresh, err := b.buildFull()
	if err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	if err := b.Save(fresh); err != nil {
		t.Fatalf("Save rebuilt index: %v", err)
	}

	loaded, err = b.Load()
	if err != nil {
		t.Fatalf("Load after rebuild: %v", err)
	}
	if loaded == nil {
		t.Fatal("expected loaded index after rebuild")
	}
	if loaded.FileCount() != 1 {
		t.Errorf("expected 1 file after rebuild, got %d", loaded.FileCount())
	}
}
