// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package bm25

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// --- ChangeTracker ---

// TestChangeTracker_MarkDrainPending pins the tracker contract the search tool
// relies on: marks are idempotent, Pending reflects non-empty state, and Drain
// returns the changed set and resets the tracker so changes are not re-applied.
func TestChangeTracker_MarkDrainPending(t *testing.T) {
	ct := NewChangeTracker()
	if ct.Pending() {
		t.Error("fresh tracker reports pending changes")
	}
	if got := ct.Drain(); len(got) != 0 {
		t.Errorf("Drain on fresh tracker = %v, want empty", got)
	}

	ct.MarkChanged("/a.go", "/b.go")
	ct.MarkChanged("/a.go") // idempotent
	if !ct.Pending() {
		t.Error("Pending() = false after MarkChanged")
	}
	drained := ct.Drain()
	if len(drained) != 2 {
		t.Fatalf("Drain = %v, want 2 unique paths", drained)
	}
	seen := map[string]bool{}
	for _, p := range drained {
		seen[p] = true
	}
	if !seen["/a.go"] || !seen["/b.go"] {
		t.Errorf("Drain lost a path: %v", drained)
	}
	if ct.Pending() {
		t.Error("Pending() = true after Drain; changes were not reset")
	}
	if got := ct.Drain(); len(got) != 0 {
		t.Errorf("second Drain = %v, want empty (drained changes must not resurface)", got)
	}
}

// --- small helpers ---

// TestDefaultWorkersBounds pins the 2..8 concurrency clamp.
func TestDefaultWorkersBounds(t *testing.T) {
	if n := defaultWorkers(); n < 2 || n > 8 {
		t.Errorf("defaultWorkers() = %d, want within [2,8]", n)
	}
}

// TestIndexDirLayout pins the on-disk location of the persisted index.
func TestIndexDirLayout(t *testing.T) {
	got := IndexDir("/proj")
	if want := filepath.Join("/proj", ".goa", "smartsearch"); got != want {
		t.Errorf("IndexDir = %q, want %q", got, want)
	}
}

// TestIsIndexableFile pins source-file admission and binary rejection.
func TestIsIndexableFile(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"main.go", true},
		{"a/b/script.py", true},
		{"Makefile", true},
		{"dockerfile", true},
		{"config.yaml", true},
		{"notes.md", true},
		{"image.png", false},
		{"archive.tar", false},
		{"lib.dylib", false},
		{"blob.db", false},
		{".hidden.go", false}, // dotfiles are never indexed
		{"README", false},     // no extension, not a known component file
		{"noext.unknownext", false},
	}
	for _, tc := range cases {
		if got := isIndexableFile(tc.path); got != tc.want {
			t.Errorf("isIndexableFile(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}

// TestBuilder_ShouldSkipDirAndCollectFiles pins the walk filters: dot
// directories and configured excludes are pruned, non-indexable files skipped,
// and results ordered.
func TestBuilder_ShouldSkipDirAndCollectFiles(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "b.go", "package b\nfunc bbb() {}\n")
	writeFile(t, dir, "a.go", "package a\nfunc aaa() {}\n")
	writeFile(t, dir, "skipme/c.go", "package c\nfunc ccc() {}\n")
	writeFile(t, dir, "asset.png", "binary")
	writeFile(t, dir, ".hidden/d.go", "package d\n")

	b := NewBuilder(dir, IndexDir(dir), []string{"skipme"})
	for _, name := range []string{".git", ".goa", "skipme"} {
		if !b.shouldSkipDir(name) {
			t.Errorf("shouldSkipDir(%q) = false, want true", name)
		}
	}
	if b.shouldSkipDir("src") {
		t.Error("shouldSkipDir(src) = true, want false")
	}

	files, err := b.collectFiles()
	if err != nil {
		t.Fatalf("collectFiles: %v", err)
	}
	var rel []string
	for _, f := range files {
		rel = append(rel, filepath.Base(f))
	}
	if len(rel) != 2 || rel[0] != "a.go" || rel[1] != "b.go" {
		t.Errorf("collectFiles = %v, want sorted [a.go b.go]", rel)
	}

	if b.analyzerFor("x.go") == nil {
		t.Error("analyzerFor(go file) = nil, want an analyzer")
	}
}

// --- Builder lifecycle ---

// TestBuilder_BuildOrRefreshLifecycle pins the full builder contract: the first
// call builds and persists, an unchanged tree is reused without rewriting, an
// edited file is picked up, and a deleted file disappears from the index.
func TestBuilder_BuildOrRefreshLifecycle(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "alpha.go", "package x\nfunc chipmunkAlpha() {}\n")
	writeFile(t, dir, "beta.go", "package x\nfunc unrelatedBeta() {}\n")
	b := NewBuilder(dir, IndexDir(dir), nil)

	assertInitialBuild(t, b)
	assertUnchangedRefresh(t, b)
	assertEditIsReindexed(t, b, dir)
	assertDeletionIsDropped(t, b, dir)
}

// assertInitialBuild requires the first BuildOrRefresh to produce a persisted,
// timestamped two-file index.
func assertInitialBuild(t *testing.T, b *Builder) {
	t.Helper()
	idx, err := b.BuildOrRefresh()
	if err != nil {
		t.Fatalf("first BuildOrRefresh: %v", err)
	}
	if idx.FileCount() != 2 {
		t.Fatalf("FileCount = %d, want 2", idx.FileCount())
	}
	if idx.IndexTime().IsZero() || idx.IndexAge() < 0 {
		t.Error("index time not recorded")
	}
	if _, ok := idx.DocumentByID("no-such-id"); ok {
		t.Error("DocumentByID found a missing document")
	}
}

// assertUnchangedRefresh requires an unchanged tree to reuse the index.
func assertUnchangedRefresh(t *testing.T, b *Builder) {
	t.Helper()
	idx, err := b.BuildOrRefresh()
	if err != nil {
		t.Fatalf("second BuildOrRefresh: %v", err)
	}
	if got := idx.FileCount(); got != 2 {
		t.Errorf("unchanged refresh FileCount = %d, want 2", got)
	}
}

// assertEditIsReindexed requires an edited file's new content to be searchable.
func assertEditIsReindexed(t *testing.T, b *Builder, dir string) {
	t.Helper()
	writeFile(t, dir, "beta.go", "package x\nfunc chipmunkBetaReplacement() {}\n")
	idx, err := b.BuildOrRefresh()
	if err != nil {
		t.Fatalf("refresh after edit: %v", err)
	}
	if got := searchPaths(idx.SearchChunks("chipmunk", 10, 0)); !contains(got, filepath.Join(dir, "beta.go")) {
		t.Errorf("edited file not reindexed; hits = %v", got)
	}
}

// assertDeletionIsDropped requires a deleted file to vanish from the index.
func assertDeletionIsDropped(t *testing.T, b *Builder, dir string) {
	t.Helper()
	if err := os.Remove(filepath.Join(dir, "alpha.go")); err != nil {
		t.Fatalf("remove: %v", err)
	}
	idx, err := b.BuildOrRefresh()
	if err != nil {
		t.Fatalf("refresh after delete: %v", err)
	}
	if got := searchPaths(idx.Search("chipmunk", 10, 0)); contains(got, filepath.Join(dir, "alpha.go")) {
		t.Errorf("deleted file still indexed; hits = %v", got)
	}
}

// TestBuilder_BuildOrRefreshWithChangeTracker pins the tracker-driven path: only
// the notified files are re-tokenised, and untouched files survive the refresh.
func TestBuilder_BuildOrRefreshWithChangeTracker(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "keep.go", "package x\nfunc keepSurvivor() {}\n")
	writeFile(t, dir, "edit.go", "package x\nfunc beforeEdit() {}\n")

	ct := NewChangeTracker()
	b := NewBuilder(dir, IndexDir(dir), nil).WithChangeTracker(ct)
	if _, err := b.BuildOrRefresh(); err != nil {
		t.Fatalf("initial build: %v", err)
	}

	writeFile(t, dir, "edit.go", "package x\nfunc afterEdit() {}\n")
	ct.MarkChanged(filepath.Join(dir, "edit.go"))
	if !ct.Pending() {
		t.Fatal("tracker lost the mark")
	}

	refreshed, err := b.BuildOrRefresh()
	if err != nil {
		t.Fatalf("tracker refresh: %v", err)
	}
	if ct.Pending() {
		t.Error("pending changes must be consumed by the refresh")
	}
	if refreshed.FileCount() != 2 {
		t.Errorf("FileCount = %d, want 2 (survivor + refreshed file)", refreshed.FileCount())
	}
	paths := searchPaths(refreshed.SearchChunks("edit", 10, 0))
	if !contains(paths, filepath.Join(dir, "edit.go")) {
		t.Errorf("refreshed file missing from hits: %v", paths)
	}
	if paths := searchPaths(refreshed.Search("survivor", 10, 0)); !contains(paths, filepath.Join(dir, "keep.go")) {
		t.Errorf("untouched file lost by pending refresh: %v", paths)
	}
}

// TestBuilder_TrackerRefreshPreservesChunksAndSchema pins the production
// smartsearch path (ChangeTracker + BuildOrRefresh). A tracker-driven refresh
// must keep the semantic chunk table complete and must persist an index that
// still loads, otherwise every search after an edit silently degrades to
// file-level matching and then gets the index deleted and fully rebuilt.
func TestBuilder_TrackerRefreshPreservesChunksAndSchema(t *testing.T) {
	dir := t.TempDir()
	keep := filepath.Join(dir, "keep.go")
	edited := filepath.Join(dir, "edit.go")
	writeFile(t, dir, "keep.go", "package x\nfunc keepSurvivor() {}\n")
	writeFile(t, dir, "edit.go", "package x\nfunc beforeEdit() {}\n")

	ct := NewChangeTracker()
	b := NewBuilder(dir, IndexDir(dir), nil).WithChangeTracker(ct)
	first, err := b.BuildOrRefresh()
	if err != nil {
		t.Fatalf("initial build: %v", err)
	}
	if len(first.Data.Documents) == 0 {
		t.Fatal("initial build produced no semantic chunks")
	}

	writeFile(t, dir, "edit.go", "package x\nfunc afterEdit() {}\n")
	ct.MarkChanged(edited)
	refreshed, err := b.BuildOrRefresh()
	if err != nil {
		t.Fatalf("tracker refresh: %v", err)
	}

	// The chunk table must survive: survivors keep their chunks and the edited
	// file is re-chunked.
	docsByPath := map[string]int{}
	for _, d := range refreshed.Data.Documents {
		docsByPath[d.Path]++
	}
	if docsByPath[keep] == 0 {
		t.Errorf("tracker refresh dropped chunks for the untouched file: %v", docsByPath)
	}
	if docsByPath[edited] == 0 {
		t.Errorf("tracker refresh produced no chunks for the edited file: %v", docsByPath)
	}

	// Results must come from the chunk ranker (symbol metadata populated), not
	// from the file-level fallback.
	hits := refreshed.SearchChunks("afterEdit", 10, 0)
	if len(hits) == 0 {
		t.Fatal("no hits for the edited file after refresh")
	}
	if hits[0].Symbol == "" {
		t.Errorf("chunk-level metadata missing after refresh (fell back to file-level): %+v", hits[0])
	}

	// The persisted index must still load: a schema-less index is rejected by
	// Load, which makes smartsearch delete and rebuild it on the next query.
	if _, err := b.Load(); err != nil {
		t.Fatalf("index persisted by the tracker path cannot be loaded back: %v", err)
	}
}

// TestBuilder_PendingThenIncrementalRefreshStaysChunked pins that alternating
// refresh paths keep the chunk table populated across a second refresh.
func TestBuilder_PendingThenIncrementalRefreshStaysChunked(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "one.go", "package x\nfunc firstThing() {}\n")

	b := NewBuilder(dir, IndexDir(dir), nil)
	if _, err := b.BuildOrRefresh(); err != nil {
		t.Fatalf("build: %v", err)
	}
	writeFile(t, dir, "two.go", "package x\nfunc secondThing() {}\n")
	idx, err := b.BuildOrRefresh()
	if err != nil {
		t.Fatalf("incremental refresh: %v", err)
	}
	if len(idx.Data.Documents) != 2 {
		t.Fatalf("Documents = %d, want 2 (one chunk per file)", len(idx.Data.Documents))
	}
	writeFile(t, dir, "three.go", "package x\nfunc thirdThing() {}\n")
	idx, err = b.BuildOrRefresh()
	if err != nil {
		t.Fatalf("second incremental refresh: %v", err)
	}
	if len(idx.Data.Documents) != 3 {
		t.Errorf("Documents = %d, want 3; chunk table shrank across refreshes", len(idx.Data.Documents))
	}
}

// --- Index search surface ---

// TestIndex_SearchChunksCapsChunksPerPath pins the path-diversity rule: at most
// two chunks from a single file are returned, so one large file cannot swamp
// the result set.
func TestIndex_SearchChunksCapsChunksPerPath(t *testing.T) {
	dir := t.TempDir()
	var src strings.Builder
	src.WriteString("package x\n")
	for i := 0; i < 6; i++ {
		src.WriteString("func chipmunkVariant() { return }\n")
	}
	writeFile(t, dir, "many.go", src.String())

	idx, err := NewBuilder(dir, IndexDir(dir), nil).BuildOrRefresh()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	results := idx.SearchChunks("chipmunk", 10, 0)
	if len(results) == 0 {
		t.Fatal("SearchChunks returned no hits for an indexed term")
	}
	if len(results) > 2 {
		t.Errorf("SearchChunks returned %d chunks from one path, want at most 2", len(results))
	}
	wantPath := filepath.Join(dir, "many.go")
	for _, r := range results {
		assertChunkShape(t, r, wantPath)
		if _, ok := idx.DocumentByID(r.ID); !ok {
			t.Errorf("chunk %q missing from the document table", r.ID)
		}
	}
}

// assertChunkShape checks that one chunk result is fully populated.
func assertChunkShape(t *testing.T, r SearchResult, wantPath string) {
	t.Helper()
	if r.Path != wantPath {
		t.Errorf("unexpected path %q, want %q", r.Path, wantPath)
	}
	if r.Lines <= 0 || r.StartLine <= 0 || r.EndLine < r.StartLine {
		t.Errorf("chunk range not populated: %+v", r)
	}
	if r.ID == "" || r.Language == "" {
		t.Errorf("chunk identity not populated: %+v", r)
	}
}

// TestIndex_SearchChunksFallsBackWithoutDocuments pins the documented
// compatibility fallback: an index persisted before chunk metadata existed
// still answers chunk queries through the file-level ranker.
func TestIndex_SearchChunksFallsBackWithoutDocuments(t *testing.T) {
	idx := NewIndex(IndexData{
		Version:    IndexVersion,
		IndexTime:  time.Now(),
		TotalFiles: 1,
		Files:      []FileMeta{{Path: "/legacy.go", Lines: 3}},
		DocLengths: []int{1},
		DocFreq:    map[string]int{"chipmunk": 1},
		DocTerms:   []map[string]int{{"chipmunk": 1}},
	})
	results := idx.SearchChunks("chipmunk", 5, 0)
	if len(results) != 1 || results[0].Path != "/legacy.go" {
		t.Fatalf("fallback results = %+v, want the legacy file", results)
	}
	if results[0].Lines != 3 {
		t.Errorf("fallback Lines = %d, want 3", results[0].Lines)
	}
}

// TestIndex_SearchGuards pins the empty-corpus and empty-query guards plus the
// minScore cut-off on the file-level ranker.
func TestIndex_SearchGuards(t *testing.T) {
	empty := NewIndex(IndexData{Version: IndexVersion, IndexTime: time.Now()})
	if got := empty.Search("anything", 5, 0); got != nil {
		t.Errorf("Search on empty index = %+v, want nil", got)
	}
	if got := empty.SearchChunks("anything", 5, 0); got != nil {
		t.Errorf("SearchChunks on empty index = %+v, want nil", got)
	}

	idx := NewIndex(IndexData{
		Version:    IndexVersion,
		IndexTime:  time.Now(),
		TotalFiles: 1,
		Files:      []FileMeta{{Path: "/solo.go", Lines: 2}},
		DocLengths: []int{2},
		DocFreq:    map[string]int{"chipmunk": 1},
		DocTerms:   []map[string]int{{"chipmunk": 1}},
	})
	if got := idx.Search("", 5, 0); got != nil {
		t.Errorf("Search with empty query = %+v, want nil", got)
	}
	if got := idx.Search("!!!", 5, 0); got != nil {
		t.Errorf("Search with token-less query = %+v, want nil", got)
	}
	if got := idx.Search("chipmunk", 5, 0); len(got) != 1 || got[0].Path != "/solo.go" {
		t.Errorf("Search hits = %+v, want the solo file", got)
	}
	if got := idx.Search("chipmunk", 5, 1e9); len(got) != 0 {
		t.Errorf("minScore must filter everything out, got %+v", got)
	}
	if got := idx.FileCount(); got != 1 {
		t.Errorf("FileCount = %d, want 1", got)
	}
}

// --- Okapi removal ---

// TestOkapi_RemoveDocumentMiddleKeepsStatsConsistent pins removal from the
// middle: later documents shift down and every derived statistic stays
// consistent with the surviving corpus.
func TestOkapi_RemoveDocumentMiddleKeepsStatsConsistent(t *testing.T) {
	o := NewOkapi(DefaultOkapiConfig())
	o.AddDocument([]string{"alpha", "beta"})
	o.AddDocument([]string{"gamma"})
	o.AddDocument([]string{"delta", "epsilon", "zeta"})
	before := o.DocCount()
	if before != 3 {
		t.Fatalf("DocCount = %d, want 3", before)
	}

	o.RemoveDocument(1) // drop "gamma" from the middle
	if got := o.DocCount(); got != 2 {
		t.Fatalf("DocCount after removal = %d, want 2", got)
	}
	if got := o.DocFreq()["gamma"]; got != 0 {
		t.Errorf("docFreq[gamma] = %d, want pruned", got)
	}
	if got := o.DocFreq()["zeta"]; got != 1 {
		t.Errorf("docFreq[zeta] = %d, want 1 (shifted document kept)", got)
	}
	assertAvgConsistent(t, o)

	// Out-of-range removals are no-ops.
	o.RemoveDocument(-1)
	o.RemoveDocument(o.DocCount())
	if got := o.DocCount(); got != 2 {
		t.Errorf("out-of-range removal changed DocCount to %d", got)
	}

	o.RemoveDocument(0)
	o.RemoveDocument(0)
	if got := o.DocCount(); got != 0 {
		t.Errorf("DocCount = %d, want 0", got)
	}
	if got := o.AvgDocLen(); got != 0 {
		t.Errorf("AvgDocLen on empty corpus = %v, want 0", got)
	}
	if got := o.DocFreq(); len(got) != 0 {
		t.Errorf("docFreq not emptied: %v", got)
	}
}

// assertAvgConsistent verifies AvgDocLen matches the surviving doc lengths,
// which is how the incremental paths keep scoring stable.
func assertAvgConsistent(t *testing.T, o *Okapi) {
	t.Helper()
	lengths := o.DocLengths()
	if len(lengths) != o.DocCount() {
		t.Fatalf("docLengths len %d != DocCount %d", len(lengths), o.DocCount())
	}
	total := 0
	for _, l := range lengths {
		total += l
	}
	want := 0.0
	if o.DocCount() > 0 {
		want = float64(total) / float64(o.DocCount())
	}
	if got := o.AvgDocLen(); got != want {
		t.Errorf("AvgDocLen = %v, want %v", got, want)
	}
}

func searchPaths(results []SearchResult) []string {
	out := make([]string, 0, len(results))
	for _, r := range results {
		out = append(out, r.Path)
	}
	return out
}
