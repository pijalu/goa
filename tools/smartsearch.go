// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/tools/search/bm25"
)

// SmartSearchTool finds relevant code files using BM25Okapi relevance ranking.
// It accepts natural language queries and returns ranked file paths with
// relevance scores based on term frequency and inverse document frequency.
//
// Unlike the regex-based SearchTool, SmartSearch understands what code *does*
// rather than what it *looks like*. Build and maintain a persistent BM25 index
// under .goa/smartsearch/.
type SmartSearchTool struct {
	WorktreeMgr *internal.WorktreeManager
	ProjectDir  string
	MaxResults  int
	MinScore    float64
	ExcludeDirs []string
	K1          float64 // BM25 k1 parameter (term frequency saturation)
	B           float64 // BM25 b parameter (length normalisation)

	ChangeTracker *bm25.ChangeTracker

	// indexMu serialises index build/refresh so concurrent calls do not race
	// on the on-disk index or on the in-memory cached index.
	indexMu sync.Mutex

	// Cached index, rebuilt lazily on first call or after file changes.
	index *bm25.Index
}

// Schema returns the tool schema for smartsearch.
func (t *SmartSearchTool) Schema() agentic.ToolSchema {
	return agentic.ToolSchema{
		Name: "smartsearch",
		Description: "Search for relevant code files using BM25 relevance ranking. " +
			"Accepts natural language queries and returns ranked file paths with relevance scores " +
			"and the matching source lines so you can act on the results. " +
			"Builds and maintains a persistent index under .goa/smartsearch/. " +
			"Best for finding code by what it does (e.g. \"authentication middleware\", \"HTTP handler for users\"), " +
			"rather than by an exact pattern. For exact pattern matching, use the search tool instead.",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "Natural language query describing what you are looking for (e.g. 'database migration', 'user authentication')",
				},
				"glob": map[string]any{
					"type":        "string",
					"description": "File glob pattern to filter results (e.g. '*.go')",
				},
				"path": map[string]any{
					"type":        "string",
					"description": "Root directory to search (default: project root)",
				},
				"max_results": map[string]any{
					"type":        "integer",
					"description": "Maximum number of results to return (default: 20)",
				},
				"min_score": map[string]any{
					"type":        "number",
					"description": "Minimum relevance score threshold (0.0 to 1.0, default: 0.0)",
				},
			},
			"required": []string{"query"},
		},
	}
}

// smartSearchParams holds the parsed input.
type smartSearchParams struct {
	Query      string  `json:"query"`
	Glob       string  `json:"glob"`
	RootPath   string  `json:"path"`
	MaxResults int     `json:"max_results"`
	MinScore   float64 `json:"min_score"`
}

// ExecuteContext performs the search with cancellation support.
func (t *SmartSearchTool) ExecuteContext(ctx context.Context, input string) (string, error) {
	var p smartSearchParams
	if err := json.Unmarshal([]byte(input), &p); err != nil {
		return "", &internal.ToolError{
			Tool: "smartsearch", Type: "invalid_input",
			Detail:   fmt.Sprintf("Cannot parse parameters: %v", err),
			HintText: "Ensure your input is valid JSON with query as a string.",
		}
	}

	if p.Query == "" {
		return "", &internal.ToolError{
			Tool: "smartsearch", Type: "missing_query",
			Detail:   "Query is required",
			HintText: "Provide a natural language query describing what code you are looking for.",
		}
	}

	// Resolve project root.
	rootPath := t.resolveRootPath(p.RootPath)
	if rootPath == "" {
		return "", &internal.ToolError{
			Tool: "smartsearch", Type: "no_project",
			Detail:   "Could not determine project root directory",
			HintText: "Set a path or run from within a project directory.",
		}
	}

	// Defaults.
	maxResults := p.MaxResults
	if maxResults <= 0 {
		maxResults = defaultInt(t.MaxResults, 20)
	}
	minScore := p.MinScore
	if minScore <= 0 {
		minScore = t.MinScore
	}

	// Ensure we have an index.
	idx, rebuilt, err := t.getOrBuildIndex(rootPath)
	if err != nil {
		return "", &internal.ToolError{
			Tool: "smartsearch", Type: "index_error",
			Detail:   fmt.Sprintf("Failed to build search index: %v", err),
			HintText: "Check that the project directory contains indexable source files.",
		}
	}

	if idx.FileCount() == 0 {
		return fmt.Sprintf("[smartsearch: %q] — No files indexed (project may be empty or contain only binary files)", p.Query), nil
	}

	// Search.
	results := idx.Search(p.Query, maxResults, minScore)
	if len(results) == 0 {
		return fmt.Sprintf("[smartsearch: %q] — No relevant results found (try a different query or the search tool for exact matches)", p.Query), nil
	}

	// Apply glob filter if specified.
	if p.Glob != "" {
		results = filterByGlob(results, p.Glob)
		if len(results) == 0 {
			return fmt.Sprintf("[smartsearch: %q] — No relevant results matching %q (try removing the glob filter)", p.Query, p.Glob), nil
		}
	}

	// Normalise scores for display.
	normaliseResults(results)

	// Enrich each ranked candidate (most-relevant first) with the matching
	// source lines, mirroring the search tool's output so the agent can act on
	// the results rather than only seeing file paths.
	terms := extractQueryTerms(p.Query)
	matches := buildMatchingLines(results, terms, maxResults)

	idxDir := bm25.IndexDir(rootPath)
	return t.formatResults(results, matches, p.Query, idx, rebuilt, idxDir), nil
}

// Execute implements agentic.Tool. Delegates to ExecuteContext.
func (t *SmartSearchTool) Execute(input string) (string, error) {
	return t.ExecuteContext(context.Background(), input)
}

// IsRetryable returns false — most search errors are deterministic.
func (t *SmartSearchTool) IsRetryable(err error) bool { return false }

// resolveRootPath determines the root directory for indexing.
func (t *SmartSearchTool) resolveRootPath(path string) string {
	if path != "" {
		if t.WorktreeMgr != nil {
			worktreePath := t.WorktreeMgr.CurrentWorktree()
			if worktreePath != "" {
				return t.WorktreeMgr.ResolvePath(worktreePath, path)
			}
		}
		return path
	}

	if t.ProjectDir != "" {
		return t.ProjectDir
	}

	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return cwd
}

// getOrBuildIndex returns the cached index, building or refreshing it if
// needed. The returned boolean is true when the on-disk index was missing,
// corrupted, or otherwise had to be rebuilt from scratch. In that case the
// caller should surface a note to the user.
func (t *SmartSearchTool) getOrBuildIndex(rootPath string) (*bm25.Index, bool, error) {
	t.indexMu.Lock()
	defer t.indexMu.Unlock()

	// Determine the index directory.
	idxDir := bm25.IndexDir(rootPath)

	// Build the indexer with shared change tracker.
	builder := bm25.NewBuilder(rootPath, idxDir, t.excludes())
	if t.ChangeTracker != nil {
		builder.WithChangeTracker(t.ChangeTracker)
	}

	idx, err := builder.BuildOrRefresh()
	if err == nil {
		t.index = idx
		return idx, false, nil
	}

	// Index issue: log it, remove the corrupted index, and rebuild from scratch.
	idxPath := filepath.Join(idxDir, bm25.IndexFile)
	if removeErr := os.Remove(idxPath); removeErr != nil && !os.IsNotExist(removeErr) {
		return nil, false, fmt.Errorf("index corrupted (%v); failed to remove corrupted index at %q: %w", err, idxPath, removeErr)
	}

	idx, rebuildErr := builder.BuildOrRefresh()
	if rebuildErr != nil {
		return nil, false, fmt.Errorf("index corrupted (%v); rebuild failed: %w", err, rebuildErr)
	}
	t.index = idx
	return idx, true, nil
}

// excludes returns the default exclude directories combined with user config.
func (t *SmartSearchTool) excludes() []string {
	// Always exclude these.
	defaults := []string{".git", "node_modules", "vendor", ".goa", "dist", "build", ".venv", "__pycache__"}
	if len(t.ExcludeDirs) > 0 {
		return append(defaults, t.ExcludeDirs...)
	}
	return defaults
}

// smartLineMatch is a single matching source line for a candidate file.
type smartLineMatch struct {
	Num  int
	Text string
}

// extractQueryTerms returns deduplicated, lowercased significant terms from a
// natural-language query, using the same code tokenizer the index uses so the
// grep stage looks for the same units BM25 ranked on.
func extractQueryTerms(query string) []string {
	toks := bm25.NewCodeTokenizer().Tokenize(query)
	seen := make(map[string]bool, len(toks))
	out := make([]string, 0, len(toks))
	for _, t := range toks {
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	return out
}

// smartMatchBudget caps how many matching lines are surfaced overall so the
// result stays bounded regardless of corpus size.
const smartMatchBudget = 30

// linesPerCandidate bounds how many matching lines a single file contributes.
const linesPerCandidate = 3

// buildMatchingLines greps each ranked candidate (highest score first) for the
// query terms and returns the matching source lines per file path. It walks
// results in relevance order and stops once the overall line budget is spent,
// so the most relevant hits are always shown.
func buildMatchingLines(results []bm25.SearchResult, terms []string, maxResults int) map[string][]smartLineMatch {
	if len(terms) == 0 {
		return nil
	}
	pattern := buildTermsRegex(terms)
	if pattern == "" {
		return nil
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil
	}

	budget := smartMatchBudget
	if maxResults > 0 && maxResults*linesPerCandidate < budget {
		budget = maxResults * linesPerCandidate
	}

	out := make(map[string][]smartLineMatch, len(results))
	for _, r := range results {
		if budget <= 0 {
			break
		}
		lines := grepFile(r.Path, re, linesPerCandidate, &budget)
		if len(lines) > 0 {
			out[r.Path] = lines
		}
	}
	return out
}

// buildTermsRegex turns a list of terms into a case-insensitive alternation
// regex such as "(?i)(foo|bar)". Returns an empty string when there is nothing
// to match.
func buildTermsRegex(terms []string) string {
	var sb strings.Builder
	sb.WriteString("(?i)(")
	for i, t := range terms {
		if t == "" {
			continue
		}
		if sb.Len() > len("(?i)(") {
			sb.WriteByte('|')
		}
		sb.WriteString(regexp.QuoteMeta(t))
		_ = i
	}
	sb.WriteByte(')')
	if sb.Len() <= len("(?i)()") {
		return ""
	}
	return sb.String()
}

// grepFile reads path and returns up to maxLines lines matching re, decrementing
// the shared *budget for each line returned. The budget bounds total output.
func grepFile(path string, re *regexp.Regexp, maxLines int, budget *int) []smartLineMatch {
	data, err := os.ReadFile(path)
	if err != nil || isBinary(data) {
		return nil
	}
	var matches []smartLineMatch
	num := 1
	for line := range strings.SplitSeq(string(data), "\n") {
		if re.MatchString(line) {
			matches = append(matches, smartLineMatch{Num: num, Text: line})
			*budget--
			if len(matches) >= maxLines || *budget <= 0 {
				return matches
			}
		}
		num++
	}
	return matches
}

// formatResults produces the output string for a set of ranked results.
// matches carries the matching source lines keyed by absolute path. If rebuilt
// is true, a leading note warns the user that the index had to be rebuilt from
// scratch due to a detected issue. idxDir is the directory that holds the
// on-disk index.
func (t *SmartSearchTool) formatResults(results []bm25.SearchResult, matches map[string][]smartLineMatch, query string, idx *bm25.Index, rebuilt bool, idxDir string) string {
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

	for i, r := range results {
		relPath := r.Path
		if t.ProjectDir != "" {
			if p, err := filepath.Rel(t.ProjectDir, r.Path); err == nil && !strings.HasPrefix(p, "..") {
				relPath = p
			}
		}
		fmt.Fprintf(&buf, "%d. [%.2f] %s  (%d lines)\n", i+1, r.Score, relPath, r.Lines)
		for _, m := range matches[r.Path] {
			content := strings.TrimSpace(m.Text)
			if len(content) > 140 {
				content = content[:140] + "…"
			}
			fmt.Fprintf(&buf, "    %d: %s\n", m.Num, content)
		}
	}

	if idxDir != "" {
		fmt.Fprintf(&buf, "\n(Index: %s)", filepath.Join(idxDir, bm25.IndexFile))
	}

	return buf.String()
}

// normaliseResults applies min-max normalisation to scores in-place and sorts
// descending.
func normaliseResults(results []bm25.SearchResult) {
	if len(results) == 0 {
		return
	}

	// Sort by score descending.
	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	// Min-max normalisation to [0, 1] for readability.
	maxS := results[0].Score
	minS := results[len(results)-1].Score
	scale := maxS - minS
	if scale > 0 {
		for i := range results {
			results[i].Score = (results[i].Score - minS) / scale
		}
	}
}

// filterByGlob filters results to only those matching the glob pattern.
func filterByGlob(results []bm25.SearchResult, glob string) []bm25.SearchResult {
	filtered := make([]bm25.SearchResult, 0, len(results))
	for _, r := range results {
		matched, _ := filepath.Match(glob, filepath.Base(r.Path))
		if !matched {
			matched, _ = filepath.Match(glob, r.Path)
		}
		if matched {
			filtered = append(filtered, r)
		}
	}
	return filtered
}

// formatDuration formats a duration for human display.
func formatDuration(d interface{}) string {
	switch v := d.(type) {
	case string:
		return v
	default:
		return fmt.Sprintf("%v", d)
	}
}

// --- Documentation ---

//go:embed smartsearch.short.md smartsearch.long.md
var smartsearchDocs embed.FS

func (t *SmartSearchTool) ShortDoc() string { return readDoc(smartsearchDocs, "smartsearch.short.md") }
func (t *SmartSearchTool) LongDoc() string  { return readDoc(smartsearchDocs, "smartsearch.long.md") }

func (t *SmartSearchTool) Examples() []string {
	return []string{
		`{"query": "user authentication middleware"}`,
		`{"query": "database migration helpers", "glob": "*.go", "max_results": 10}`,
		`{"query": "HTTP route handler for profile pages", "min_score": 0.3}`,
		`{"query": "configuration loading", "path": "src/"}`,
	}
}

// compile-time interface check.
var _ agentic.ContextTool = (*SmartSearchTool)(nil)
