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
const IndexVersion = 1

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
	mu     sync.Mutex
	paths  map[string]struct{}
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

// IndexData is the complete serialisable state of a BM25 index, persisted as
// a gob-encoded file under .goa/smartsearch/index.gob.
type IndexData struct {
	Version    int              `gob:"version"`
	IndexTime  time.Time        `gob:"index_time"`
	TotalFiles int              `gob:"total_files"`
	Files      []FileMeta       `gob:"files"`
	AvgDocLen  float64          `gob:"avg_doc_len"`
	DocLengths []int            `gob:"doc_lengths"`
	DocFreq    map[string]int   `gob:"doc_freq"`
	DocTerms   []map[string]int `gob:"doc_terms"`
}

// Index wraps an IndexData with a code tokenizer and an Okapi scorer,
// providing high-level search and lifecycle management.
type Index struct {
	Data      IndexData
	okapi     *Okapi
	tokenizer *CodeTokenizer
	mu        sync.RWMutex
}

// NewIndex builds an Index from IndexData, initialising the Okapi scorer.
func NewIndex(data IndexData) *Index {
	o := NewOkapi(DefaultOkapiConfig())
	o.SetDocData(data.DocLengths, data.DocFreq, data.DocTerms)
	return &Index{
		Data:      data,
		okapi:     o,
		tokenizer: NewCodeTokenizer(),
	}
}

// SearchResult is a single ranked document returned by Search.
type SearchResult struct {
	Path  string  `json:"path"`
	Score float64 `json:"score"`
	Lines int     `json:"lines"`
}

// Search runs the given query against the index, returning up to maxResults
// results that score above minScore. Results are ordered by descending score.
func (idx *Index) Search(query string, maxResults int, minScore float64) []SearchResult {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	if idx.Data.TotalFiles == 0 {
		return nil
	}

	qTokens := idx.tokenizer.Tokenize(query)
	if len(qTokens) == 0 {
		return nil
	}

	docIndices, scores := idx.okapi.TopN(qTokens, maxResults)
	if len(docIndices) == 0 {
		return nil
	}

	results := make([]SearchResult, 0, len(docIndices))
	for i, docIdx := range docIndices {
		if scores[i] < minScore {
			continue
		}
		results = append(results, SearchResult{
			Path:  idx.Data.Files[docIdx].Path,
			Score: scores[i],
			Lines: idx.Data.Files[docIdx].Lines,
		})
	}
	return results
}

// FileCount returns the number of indexed files.
func (idx *Index) FileCount() int {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return idx.Data.TotalFiles
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
	if data.Version != IndexVersion {
		return nil, fmt.Errorf("index version %d != current %d", data.Version, IndexVersion)
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

// tokenizeJob is a unit of work for the parallel tokenisation pool.
type tokenizeJob struct {
	path  string
	index int
}

// tokenizeResult carries the output of tokenising one file.
type tokenizeResult struct {
	index  int
	meta   FileMeta
	tokens []string
	err    error
}

// buildFull performs a complete index build from scratch using parallel
// tokenisation workers.
func (b *Builder) buildFull() (*Index, error) {
	files, err := b.collectFiles()
	if err != nil {
		return nil, fmt.Errorf("collect files: %w", err)
	}
	if len(files) == 0 {
		return NewIndex(IndexData{Version: IndexVersion, IndexTime: time.Now()}), nil
	}

	// Parallel tokenisation pipeline.
	jobs := make(chan tokenizeJob, len(files))
	results := make(chan tokenizeResult, len(files))

	var wg sync.WaitGroup
	for i := 0; i < b.workers; i++ {
		wg.Add(1)
		go b.tokenizeWorker(jobs, results, &wg)
	}

	for i, path := range files {
		jobs <- tokenizeJob{path: path, index: i}
	}
	close(jobs)
	wg.Wait()
	close(results)

	// Collect results in index order.
	metas := make([]FileMeta, len(files))
	allTokens := make([][]string, len(files))
	for r := range results {
		if r.err != nil {
			continue // skip unreadable files
		}
		metas[r.index] = r.meta
		allTokens[r.index] = r.tokens
	}

	o := NewOkapi(DefaultOkapiConfig())
	o.Build(allTokens)

	return NewIndex(IndexData{
		Version:    IndexVersion,
		IndexTime:  time.Now(),
		TotalFiles: len(files),
		Files:      metas,
		DocLengths: o.DocLengths(),
		DocFreq:    o.DocFreq(),
		DocTerms:   o.DocTerms(),
	}), nil
}

func (b *Builder) tokenizeWorker(jobs <-chan tokenizeJob, results chan<- tokenizeResult, wg *sync.WaitGroup) {
	defer wg.Done()
	for j := range jobs {
		info, err := os.Stat(j.path)
		if err != nil {
			results <- tokenizeResult{index: j.index, err: err}
			continue
		}
		tokens, lines, err := b.tokenizeFile(j.path)
		results <- tokenizeResult{
			index: j.index,
			meta: FileMeta{
				Path:    j.path,
				Size:    info.Size(),
				ModTime: info.ModTime(),
				Lines:   lines,
			},
			tokens: tokens,
			err:    err,
		}
	}
}

// --- Incremental refresh ---

// refreshIncremental detects added, modified, and removed files and applies
// the minimal set of changes to the existing index. It returns (nil, false)
// when nothing has changed.
func (b *Builder) refreshIncremental(idx *Index) (*Index, bool, error) {
	oldFileMap, oldTotal := b.buildOldFileMap(idx)

	currentPaths, currentSet, err := b.collectFileChanges(idx, oldFileMap)
	if err != nil {
		return nil, false, err
	}

	added, modified, removed := b.classifyChanges(currentPaths, currentSet, idx, oldFileMap)

	totalChanges := len(added) + len(modified) + len(removed)
	if totalChanges == 0 {
		return nil, false, nil
	}

	// If too many files changed relative to the corpus, do a full rebuild.
	if totalChanges > oldTotal/2 && oldTotal > 100 {
		idx, err := b.buildFull()
		return idx, true, err
	}

	// Copy surviving old docs and tokenize new/modified files in parallel.
	o := b.buildIncrementalIndex(idx, added, modified, oldFileMap, currentSet)
	return o, true, nil
}

// buildOldFileMap extracts the file path→index mapping from the old index.
func (b *Builder) buildOldFileMap(idx *Index) (map[string]int, int) {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	oldFileMap := make(map[string]int, len(idx.Data.Files))
	for i, f := range idx.Data.Files {
		oldFileMap[f.Path] = i
	}
	return oldFileMap, idx.Data.TotalFiles
}

// collectFileChanges gathers current file paths and builds the path set.
func (b *Builder) collectFileChanges(idx *Index, oldFileMap map[string]int) ([]string, map[string]bool, error) {
	currentPaths, err := b.collectFiles()
	if err != nil {
		return nil, nil, err
	}
	currentSet := make(map[string]bool, len(currentPaths))
	for _, p := range currentPaths {
		currentSet[p] = true
	}
	return currentPaths, currentSet, nil
}

// classifyChanges diffs the current fileset against the old index to find
// added, modified, and removed files.
func (b *Builder) classifyChanges(currentPaths []string, currentSet map[string]bool, idx *Index, oldFileMap map[string]int) (added, modified, removed []string) {
	for _, p := range currentPaths {
		if oldIdx, ok := oldFileMap[p]; ok {
			oldMeta := idx.Data.Files[oldIdx]
			info, err := os.Stat(p)
			if err != nil || oldMeta.Size != info.Size() || !oldMeta.ModTime.Equal(info.ModTime()) {
				modified = append(modified, p)
			}
		} else {
			added = append(added, p)
		}
	}
	for _, f := range idx.Data.Files {
		if !currentSet[f.Path] {
			removed = append(removed, f.Path)
		}
	}
	return
}

// buildIncrementalIndex constructs a new Index from surviving old docs plus
// newly tokenised files. Returns the new Index.
func (b *Builder) buildIncrementalIndex(idx *Index, added, modified []string, oldFileMap map[string]int, currentSet map[string]bool) *Index {
	oldFiles := idx.Data.Files
	oldDocLengths := idx.okapi.DocLengths()
	oldDocTerms := idx.okapi.DocTerms()

	// Mark surviving old docs.
	survivors := make([]bool, len(oldFiles))
	for i, f := range oldFiles {
		survivors[i] = currentSet[f.Path] && !contains(modified, f.Path)
	}

	// Pre-allocate slices.
	survivingCount := 0
	for _, s := range survivors {
		if s {
			survivingCount++
		}
	}
	newTotal := survivingCount + len(added)
	newFiles := make([]FileMeta, 0, newTotal)
	newDocLengths := make([]int, 0, newTotal)
	newDocTerms := make([]map[string]int, 0, newTotal)

	// Copy surviving docs.
	for i, s := range survivors {
		if s {
			newFiles = append(newFiles, oldFiles[i])
			newDocLengths = append(newDocLengths, oldDocLengths[i])
			newDocTerms = append(newDocTerms, oldDocTerms[i])
		}
	}

	// Tokenise added/modified files in parallel.
	allChanges := b.collectChanges(added, modified, oldFileMap)
	b.tokenizeFileBatch(allChanges, &newFiles, &newDocLengths, &newDocTerms)

	// Build fresh Okapi scorer.
	o := NewOkapi(DefaultOkapiConfig())
	o.SetDocData(newDocLengths, computeDocFreq(newDocTerms), newDocTerms)

	return NewIndex(IndexData{
		Version:    IndexVersion,
		IndexTime:  time.Now(),
		TotalFiles: len(newFiles),
		Files:      newFiles,
		AvgDocLen:  o.AvgDocLen(),
		DocLengths: o.DocLengths(),
		DocFreq:    o.DocFreq(),
		DocTerms:   o.DocTerms(),
	})
}

// collectChanges assembles the list of files to tokenise (added + modified)
// into a slice of change descriptors.
func (b *Builder) collectChanges(added, modified []string, oldFileMap map[string]int) []changeDescriptor {
	total := len(added) + len(modified)
	allChanges := make([]changeDescriptor, 0, total)
	for _, p := range added {
		allChanges = append(allChanges, changeDescriptor{path: p})
	}
	for _, p := range modified {
		allChanges = append(allChanges, changeDescriptor{path: p, isMod: true, oldID: oldFileMap[p]})
	}
	return allChanges
}

// changeDescriptor describes a file to tokenise during incremental refresh.
type changeDescriptor struct {
	path  string
	isMod bool
	oldID int
}

// changeResult holds the tokenisation result for one file.
type changeResult struct {
	path   string
	meta   FileMeta
	tokens []string
	err    error
	isMod  bool
	oldID  int
}

// tokenizeFileBatch tokenises all files concurrently and appends results
// to the output slices.
func (b *Builder) tokenizeFileBatch(changes []changeDescriptor, newFiles *[]FileMeta, newDocLengths *[]int, newDocTerms *[]map[string]int) {
	jobs := make(chan changeDescriptor, len(changes))
	results := make(chan changeResult, len(changes))

	var wg sync.WaitGroup
	for i := 0; i < b.workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				b.tokenizeOneFile(j, results)
			}
		}()
	}
	for _, ch := range changes {
		jobs <- ch
	}
	close(jobs)
	wg.Wait()
	close(results)

	for r := range results {
		if r.err != nil {
			continue
		}
		*newFiles = append(*newFiles, r.meta)
		*newDocLengths = append(*newDocLengths, len(r.tokens))
		*newDocTerms = append(*newDocTerms, tokensToFreqs(r.tokens))
	}
}

// tokenizeOneFile stat's and tokenises a single file, sending the result
// to the results channel.
func (b *Builder) tokenizeOneFile(j changeDescriptor, results chan<- changeResult) {
	info, err := os.Stat(j.path)
	if err != nil {
		results <- changeResult{path: j.path, err: err}
		return
	}
	tokens, lines, err := b.tokenizeFile(j.path)
	results <- changeResult{
		path:   j.path,
		meta:   FileMeta{Path: j.path, Size: info.Size(), ModTime: info.ModTime(), Lines: lines},
		tokens: tokens,
		err:    err,
		isMod:  j.isMod,
		oldID:  j.oldID,
	}
}

// tokensToFreqs converts a token slice to a term-frequency map.
func tokensToFreqs(tokens []string) map[string]int {
	freqs := make(map[string]int, len(tokens))
	for _, t := range tokens {
		freqs[t]++
	}
	return freqs
}

// computeDocFreq builds the global term→document-frequency map from
// per-document term maps.
func computeDocFreq(docTerms []map[string]int) map[string]int {
	df := make(map[string]int)
	for _, terms := range docTerms {
		for t := range terms {
			df[t]++
		}
	}
	return df
}

// contains reports whether s is in strs.
func contains(strs []string, s string) bool {
	for _, v := range strs {
		if v == s {
			return true
		}
	}
	return false
}

// --- File-level helpers ---

// tokenizeFile reads a file, counts lines, and returns its tokens.
func (b *Builder) tokenizeFile(path string) (tokens []string, lines int, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, 0, err
	}

	// Quick binary detection: NUL byte in first 8KB → skip.
	if hasNullByte(data) {
		return nil, 0, nil
	}

	text := string(data)
	lines = countLines(text)
	tokens = b.tokenizer.Tokenize(text)
	return tokens, lines, nil
}

// hasNullByte checks for NUL bytes in the first 8KB, indicating binary content.
func hasNullByte(data []byte) bool {
	end := len(data)
	if end > 8192 {
		end = 8192
	}
	for _, b := range data[:end] {
		if b == 0 {
			return true
		}
	}
	return false
}

// countLines returns the number of lines in text.
func countLines(text string) int {
	n := 0
	for i := 0; i < len(text); i++ {
		if text[i] == '\n' {
			n++
		}
	}
	if len(text) > 0 && text[len(text)-1] != '\n' {
		n++
	}
	return n
}

// refreshWithPending reindexes only the files in pendingChanges and merges
// them into the existing index. It collects metadata + tokens for each
// pending file in parallel and applies updates to the existing Okapi scorer.
func (b *Builder) refreshWithPending(idx *Index, pendingChanges []string) (*Index, error) {
	// Tokenise pending changes using the shared batch infrastructure.
	changes := make([]changeDescriptor, len(pendingChanges))
	for i, p := range pendingChanges {
		changes[i] = changeDescriptor{path: p}
	}

	newFiles, newDocLengths, newDocTerms := b.buildPendingIndex(idx, pendingChanges, changes)

	o := NewOkapi(DefaultOkapiConfig())
	o.SetDocData(newDocLengths, computeDocFreq(newDocTerms), newDocTerms)
	return NewIndex(IndexData{
		Version:    IndexVersion,
		IndexTime:  time.Now(),
		TotalFiles: len(newFiles),
		Files:      newFiles,
		AvgDocLen:  o.AvgDocLen(),
		DocLengths: o.DocLengths(),
		DocFreq:    o.DocFreq(),
		DocTerms:   o.DocTerms(),
	}), nil
}

// buildPendingIndex tokenises pending files and merges survivors from the
// old index with new results. Returns the three parallel slices.
func (b *Builder) buildPendingIndex(idx *Index, pendingChanges []string, changes []changeDescriptor) ([]FileMeta, []int, []map[string]int) {
	// Collect tokenisation results.
	var newFiles []FileMeta
	var newDocLengths []int
	var newDocTerms []map[string]int
	b.tokenizeFileBatch(changes, &newFiles, &newDocLengths, &newDocTerms)

	// Build pending set for survivor check.
	pendingSet := make(map[string]bool, len(pendingChanges))
	for _, p := range pendingChanges {
		pendingSet[p] = true
	}

	// Collect surviving old docs.
	idx.mu.RLock()
	oldFiles := idx.Data.Files
	oldDocLengths := idx.okapi.DocLengths()
	oldDocTerms := idx.okapi.DocTerms()
	idx.mu.RUnlock()

	// Count survivors for pre-allocation.
	survivingCount := 0
	for _, f := range oldFiles {
		if !pendingSet[f.Path] {
			survivingCount++
		}
	}

	newTotal := survivingCount + len(pendingChanges)
	mergedFiles := make([]FileMeta, 0, newTotal)
	mergedLengths := make([]int, 0, newTotal)
	mergedTerms := make([]map[string]int, 0, newTotal)

	// Copy survivors first, then new results.
	for i, f := range oldFiles {
		if !pendingSet[f.Path] {
			mergedFiles = append(mergedFiles, f)
			mergedLengths = append(mergedLengths, oldDocLengths[i])
			mergedTerms = append(mergedTerms, oldDocTerms[i])
		}
	}
	mergedFiles = append(mergedFiles, newFiles...)
	mergedLengths = append(mergedLengths, newDocLengths...)
	mergedTerms = append(mergedTerms, newDocTerms...)

	return mergedFiles, mergedLengths, mergedTerms
}

// isIndexableFile reports whether a file should be included in the index
// based on its extension or name.
func isIndexableFile(path string) bool {
	if strings.HasPrefix(filepath.Base(path), ".") {
		return false
	}

	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".go", ".py", ".js", ".ts", ".jsx", ".tsx", ".rs", ".c", ".h",
		".cpp", ".hpp", ".cs", ".java", ".kt", ".swift", ".rb", ".php",
		".sh", ".bash", ".zsh", ".fish", ".lua", ".pl", ".pm",
		".r", ".scala", ".clj", ".ex", ".exs", ".elm", ".erl", ".hs",
		".ml", ".zig", ".nim", ".cr", ".dart", ".vue", ".svelte",
		".dockerfile", ".makefile", ".cmake",
		".json", ".yaml", ".yml", ".toml", ".ini", ".cfg", ".conf",
		".env", ".md", ".rst", ".txt", ".csv", ".tsv", ".xml", ".html",
		".htm", ".css", ".scss", ".less",
		".sql", ".graphql", ".proto", ".gradle", ".lock":
		return true
	}

	base := strings.ToLower(filepath.Base(path))
	switch base {
	case "makefile", "dockerfile", "gemfile", "rakefile",
		"cmakelists.txt", "justfile", "taskfile.yml":
		return true
	}

	// Skip known binary extensions not matched above.
	switch ext {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".ico", ".svg",
		".woff", ".woff2", ".ttf", ".eot", ".otf",
		".zip", ".tar", ".gz", ".bz2", ".xz", ".zst",
		".pdf", ".doc", ".docx", ".xls", ".xlsx", ".ppt", ".pptx",
		".mp3", ".mp4", ".avi", ".mov", ".wmv", ".flv",
		".o", ".a", ".lib", ".dll", ".so", ".dylib", ".exe",
		".class", ".jar", ".war", ".pyc", ".pyo",
		".DS_Store", ".db":
		return false
	}

	return false
}

// IndexDir returns the path to the smartsearch index directory for a project.
func IndexDir(projectDir string) string {
	return filepath.Join(projectDir, ".goa", "smartsearch")
}
