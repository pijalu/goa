// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package bm25

import (
	"encoding/gob"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

// IndexVersion is bumped when the on-disk format changes.
const IndexVersion = 2

// IndexSchemaVersion identifies the document model used by persisted indexes.
const IndexSchemaVersion = "chunked-code-v1"

// IndexFile is the name of the serialised index inside the index directory.
const IndexFile = "index.gob"

// indexSaveMu serialises index writes to the same process so concurrent
// builders do not race on the same temp/dst file paths.
var indexSaveMu sync.Mutex

// --- Change tracking ---

// ChangeTracker accumulates file paths that have been modified and need
// reindexing. Tools that write or edit files notify the tracker, and the
// SmartSearch builder consumes those notifications on the next refresh.
type ChangeTracker struct {
	mu    sync.Mutex
	paths map[string]struct{}
}

// NewChangeTracker creates a ChangeTracker ready for use.
func NewChangeTracker() *ChangeTracker {
	return &ChangeTracker{paths: make(map[string]struct{})}
}

// MarkChanged records one or more files as changed for later reindexing.
// Multiple calls for the same path are idempotent.
func (ct *ChangeTracker) MarkChanged(paths ...string) {
	ct.mu.Lock()
	for _, p := range paths {
		ct.paths[p] = struct{}{}
	}
	ct.mu.Unlock()
}

// Drain returns all accumulated changed paths and resets the tracker.
func (ct *ChangeTracker) Drain() []string {
	ct.mu.Lock()
	result := make([]string, 0, len(ct.paths))
	for p := range ct.paths {
		result = append(result, p)
	}
	ct.paths = make(map[string]struct{})
	ct.mu.Unlock()
	return result
}

// Pending returns whether any changes are pending.
func (ct *ChangeTracker) Pending() bool {
	ct.mu.Lock()
	defer ct.mu.Unlock()
	return len(ct.paths) > 0
}

// defaultWorkers returns the number of concurrent tokenisation workers,
// capped at 8 to avoid excessive I/O contention.
func defaultWorkers() int {
	n := runtime.NumCPU()
	if n > 8 {
		return 8
	}
	if n < 2 {
		return 2
	}
	return n
}

// FileMeta records the metadata of an indexed file for change detection.
type FileMeta struct {
	Path    string    `gob:"path"`
	Size    int64     `gob:"size"`
	ModTime time.Time `gob:"mod_time"`
	Lines   int       `gob:"lines"`
}

// DocumentMeta describes one retrievable chunk. File-level metadata remains
// available for compatibility while chunks provide precise agent context.
type DocumentMeta struct {
	ID        string `gob:"id"`
	Path      string `gob:"path"`
	Language  string `gob:"language"`
	Kind      string `gob:"kind"`
	Symbol    string `gob:"symbol"`
	Parent    string `gob:"parent"`
	Signature string `gob:"signature"`
	Imports   string `gob:"imports"`
	StartLine int    `gob:"start_line"`
	EndLine   int    `gob:"end_line"`
	Content   string `gob:"content"`
}

// IndexData is the complete serialisable state of a BM25 index, persisted as
// a gob-encoded file under .goa/smartsearch/index.gob.
type IndexData struct {
	Version       int              `gob:"version"`
	SchemaVersion string           `gob:"schema_version"`
	IndexTime     time.Time        `gob:"index_time"`
	TotalFiles    int              `gob:"total_files"`
	Files         []FileMeta       `gob:"files"`
	Documents     []DocumentMeta   `gob:"documents"`
	AvgDocLen     float64          `gob:"avg_doc_len"`
	DocLengths    []int            `gob:"doc_lengths"`
	DocFreq       map[string]int   `gob:"doc_freq"`
	DocTerms      []map[string]int `gob:"doc_terms"`
}

// Index wraps an IndexData with a code tokenizer and an Okapi scorer,
// providing high-level search and lifecycle management.
type Index struct {
	Data      IndexData
	okapi     *Okapi
	tokenizer *CodeTokenizer
	// chunkOKAPI ranks semantic chunks independently from the legacy file index.
	chunkOKAPI *Okapi
	chunkTerms []map[string]int
	fielded    *FieldedScorer
	mu         sync.RWMutex
}

// NewIndex builds an Index from IndexData, initialising the Okapi scorer.
func NewIndex(data IndexData) *Index {
	o := NewOkapi(DefaultOkapiConfig())
	o.SetDocData(data.DocLengths, data.DocFreq, data.DocTerms)
	idx := &Index{Data: data, okapi: o, tokenizer: NewCodeTokenizer()}
	if len(data.Documents) > 0 {
		docs := make([][]string, len(data.Documents))
		for i, doc := range data.Documents {
			docs[i] = idx.tokenizer.Tokenize(strings.Join([]string{doc.Symbol, doc.Signature, doc.Imports, doc.Content}, " "))
		}
		idx.chunkOKAPI = NewOkapi(DefaultOkapiConfig())
		idx.chunkOKAPI.Build(docs)
		idx.chunkTerms = make([]map[string]int, len(docs))
		for i := range docs {
			idx.chunkTerms[i] = tokensToFreqs(docs[i])
		}
		idx.fielded = NewFieldedScorer(data.Documents, DefaultFieldedWeights())
	}
	return idx
}

// SearchResult is a single ranked document returned by Search.
type SearchResult struct {
	Path       string  `json:"path"`
	Score      float64 `json:"score"`
	Lines      int     `json:"lines"`
	ID         string  `json:"id,omitempty"`
	Language   string  `json:"language,omitempty"`
	Kind       string  `json:"kind,omitempty"`
	Symbol     string  `json:"symbol,omitempty"`
	StartLine  int     `json:"start_line,omitempty"`
	EndLine    int     `json:"end_line,omitempty"`
	Content    string  `json:"content,omitempty"`
	Coverage   float64 `json:"coverage,omitempty"`
	Confidence float64 `json:"confidence,omitempty"`
}

// Search runs the legacy file-level search. Semantic callers should use SearchChunks.
func (idx *Index) Search(query string, maxResults int, minScore float64) []SearchResult {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return idx.searchFilesLocked(query, maxResults, minScore)
}

// SearchChunks returns ranked semantic chunks with bounded source context. It
// falls back to the file-level index for indexes written before chunk metadata.
func (idx *Index) SearchChunks(query string, maxResults int, minScore float64) []SearchResult {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	if idx.chunkOKAPI == nil || len(idx.Data.Documents) == 0 {
		return idx.searchFilesLocked(query, maxResults, minScore)
	}
	if maxResults <= 0 {
		maxResults = 20
	}
	limit := maxResults * 12
	if limit < 100 {
		limit = 100
	}
	return idx.resultChunks(idx.rankChunks(query, limit, minScore), maxResults)
}

func (idx *Index) searchFilesLocked(query string, maxResults int, minScore float64) []SearchResult {
	if idx.Data.TotalFiles == 0 {
		return nil
	}
	qTokens := idx.tokenizer.Tokenize(query)
	if len(qTokens) == 0 {
		return nil
	}
	docIndices, scores := idx.okapi.TopN(qTokens, maxResults)
	results := make([]SearchResult, 0, len(docIndices))
	for i, docIdx := range docIndices {
		if scores[i] < minScore {
			continue
		}
		results = append(results, SearchResult{Path: idx.Data.Files[docIdx].Path, Score: scores[i], Lines: idx.Data.Files[docIdx].Lines})
	}
	return results
}

// FileCount returns the number of indexed files.
func (idx *Index) FileCount() int {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return idx.Data.TotalFiles
}

// DocumentByID returns a copy of a semantic document, if present.
func (idx *Index) DocumentByID(id string) (DocumentMeta, bool) {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	for _, doc := range idx.Data.Documents {
		if doc.ID == id {
			return doc, true
		}
	}
	return DocumentMeta{}, false
}

// IndexAge returns the duration since the index was built.
func (idx *Index) IndexAge() time.Duration {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return time.Since(idx.Data.IndexTime)
}

// IndexTime returns when the index was last built.
func (idx *Index) IndexTime() time.Time {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return idx.Data.IndexTime
}

// --- Builder ---

// Builder constructs, persists, and incrementally updates a BM25 index for a
// project directory. It walks the filesystem, tokenises files in parallel,
// and maintains an on-disk cache under the configured index directory.
type Builder struct {
	projectDir    string
	indexDir      string
	excludes      []string
	tokenizer     *CodeTokenizer
	analyzers     *AnalyzerRegistry
	workers       int
	changeTracker *ChangeTracker // optional, notified when refresh completes
}

// NewBuilder creates an index builder for the given project. indexDir is the
// directory where the serialised index is stored (typically
// projectDir/.goa/smartsearch/).
func NewBuilder(projectDir, indexDir string, excludes []string) *Builder {
	return &Builder{
		projectDir: projectDir,
		indexDir:   indexDir,
		excludes:   excludes,
		tokenizer:  NewCodeTokenizer(),
		analyzers:  NewAnalyzerRegistry(),
		workers:    defaultWorkers(),
	}
}

// WithChangeTracker attaches a change tracker. When set, BuildOrRefresh
// drains pending changes and uses them for incremental refresh instead of
// a full file-system walk for change detection.
func (b *Builder) WithChangeTracker(ct *ChangeTracker) *Builder {
	b.changeTracker = ct
	return b
}

func (b *Builder) analyzerFor(path string) CodeAnalyzer {
	return b.analyzers.Analyzer(LanguageForPath(path))
}

// Load attempts to deserialise and return the index from the index directory.
// Returns nil, nil when no index file exists (first run).
func (b *Builder) Load() (*Index, error) {
	path := filepath.Join(b.indexDir, IndexFile)
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("open index: %w", err)
	}
	defer f.Close()

	var data IndexData
	if err := gob.NewDecoder(f).Decode(&data); err != nil {
		return nil, fmt.Errorf("decode index: %w", err)
	}
	if data.Version != IndexVersion || data.SchemaVersion != IndexSchemaVersion {
		return nil, fmt.Errorf("index schema %q version %d != current %q version %d", data.SchemaVersion, data.Version, IndexSchemaVersion, IndexVersion)
	}
	return NewIndex(data), nil
}

// Save persists the index to the index directory using an atomic write
// (write to a unique temp file, rename). The temp file name is unique per
// invocation to avoid collisions when multiple builders/threads save concurrently.
func (b *Builder) Save(idx *Index) error {
	indexSaveMu.Lock()
	defer indexSaveMu.Unlock()

	if err := os.MkdirAll(b.indexDir, 0755); err != nil {
		return fmt.Errorf("create index dir: %w", err)
	}

	path := filepath.Join(b.indexDir, IndexFile)
	tmp := fmt.Sprintf("%s.%d.%d.tmp", path, os.Getpid(), time.Now().UnixNano())

	f, err := os.Create(tmp)
	if err != nil {
		return fmt.Errorf("create temp index: %w", err)
	}

	idx.mu.RLock()
	encErr := gob.NewEncoder(f).Encode(&idx.Data)
	idx.mu.RUnlock()

	if encErr != nil {
		f.Close()
		os.Remove(tmp)
		return fmt.Errorf("encode index: %w", encErr)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("close temp index: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("rename index: %w", err)
	}
	return nil
}

// BuildOrRefresh returns an up-to-date index, rebuilding from scratch or
// incrementally updating the cached index when source files change. It
// first drains any pending change notifications (if a ChangeTracker is
// configured), then detects remaining changes by comparing file metadata.
func (b *Builder) BuildOrRefresh() (*Index, error) {
	pendingChanges := b.drainChanges()

	idx, err := b.Load()
	if err != nil {
		return nil, err
	}

	if idx == nil {
		return b.buildAndSave()
	}

	return b.refreshOrBypass(idx, pendingChanges)
}

// buildAndSave builds the index from scratch and persists it.
func (b *Builder) buildAndSave() (*Index, error) {
	idx, err := b.buildFull()
	if err != nil {
		return nil, err
	}
	if err := b.Save(idx); err != nil {
		return nil, fmt.Errorf("save new index: %w", err)
	}
	return idx, nil
}

// refreshOrBypass applies pending or incremental changes to an existing
// index, or returns it unchanged when nothing needs refreshing.
func (b *Builder) refreshOrBypass(idx *Index, pendingChanges []string) (*Index, error) {
	if len(pendingChanges) > 0 {
		return b.refreshWithPendingSave(idx, pendingChanges)
	}
	return b.refreshIncrementalSave(idx)
}

// refreshWithPendingSave refreshes using pending changes and persists.
func (b *Builder) refreshWithPendingSave(idx *Index, pending []string) (*Index, error) {
	updated, err := b.refreshWithPending(idx, pending)
	if err != nil {
		return nil, err
	}
	if err := b.Save(updated); err != nil {
		return nil, fmt.Errorf("save refreshed index: %w", err)
	}
	return updated, nil
}

// refreshIncrementalSave applies incremental refresh and persists.
func (b *Builder) refreshIncrementalSave(idx *Index) (*Index, error) {
	updated, changed, err := b.refreshIncremental(idx)
	if err != nil {
		return nil, err
	}
	if !changed {
		return idx, nil
	}
	if err := b.Save(updated); err != nil {
		return nil, fmt.Errorf("save refreshed index: %w", err)
	}
	return updated, nil
}

// drainChanges returns pending change notifications, if a tracker is set.
func (b *Builder) drainChanges() []string {
	if b.changeTracker == nil {
		return nil
	}
	return b.changeTracker.Drain()
}

// --- File collection ---

// collectFiles walks the project directory and returns paths of indexable
// (non-binary, non-excluded) source files.
func (b *Builder) collectFiles() ([]string, error) {
	var files []string
	err := filepath.WalkDir(b.projectDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // skip inaccessible
		}
		if d.IsDir() {
			if b.shouldSkipDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !isIndexableFile(path) {
			return nil
		}
		files = append(files, path)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	return files, nil
}

func (b *Builder) shouldSkipDir(name string) bool {
	if strings.HasPrefix(name, ".") {
		return true
	}
	for _, ex := range b.excludes {
		if name == ex {
			return true
		}
	}
	return false
}

// --- Full build (parallel) ---
