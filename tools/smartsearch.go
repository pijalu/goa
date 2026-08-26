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
	"time"

	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/internal/ansi"
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
		Name:        "smartsearch",
		Description: "Search code by natural language.",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "natural language query",
				},
				"glob": map[string]any{
					"type":        "string",
					"description": "comma-separated globs, e.g. *.go",
				},
				"path": map[string]any{
					"type":        "string",
					"description": "root directory (default: project root)",
				},
				"max_results": map[string]any{
					"type":        "integer",
					"description": "max results (default: 20)",
				},
				"fetch_id": map[string]any{
					"type":        "string",
					"description": "fetch a bounded chunk by the id returned in search results",
				},
				"start_line": map[string]any{"type": "integer"},
				"end_line":   map[string]any{"type": "integer"},
				"min_score": map[string]any{
					"type":        "number",
					"description": "min score 0.0-1.0 (default: 0.0)",
				},
				"output":        map[string]any{"type": "string", "enum": []string{"text", "json"}, "description": "result format (default: text; json is agent-friendly)"},
				"max_tokens":    map[string]any{"type": "integer", "description": "approximate maximum tokens in returned context (default: 4000)"},
				"context_lines": map[string]any{"type": "integer", "description": "maximum evidence lines per result (default: 3)"},
				"language":      map[string]any{"type": "string", "description": "language filter, e.g. go or python"},
				"kind":          map[string]any{"type": "string", "description": "semantic chunk kind filter, e.g. function or type"},
			},
			"required": []string{"query"},
		},
	}
}

// smartSearchParams holds the parsed input.
type smartSearchParams struct {
	Query        string  `json:"query"`
	Glob         string  `json:"glob"`
	RootPath     string  `json:"path"`
	MaxResults   int     `json:"max_results"`
	MinScore     float64 `json:"min_score"`
	FetchID      string  `json:"fetch_id"`
	StartLine    int     `json:"start_line"`
	EndLine      int     `json:"end_line"`
	Output       string  `json:"output"`
	MaxTokens    int     `json:"max_tokens"`
	ContextLines int     `json:"context_lines"`
	Language     string  `json:"language"`
	Kind         string  `json:"kind"`
}

// ExecuteContext performs the search with cancellation support.
func (t *SmartSearchTool) ExecuteContext(ctx context.Context, input string) (string, error) {
	p, err := t.parseParams(input)
	if err != nil {
		return "", err
	}

	if err := ctx.Err(); err != nil {
		return "", err
	}
	rootPath := t.resolveRootPath(p.RootPath)
	if rootPath == "" {
		return "", &internal.ToolError{
			Tool: "smartsearch", Type: "no_project",
			Detail:   "Could not determine project root directory",
			HintText: "Set a path or run from within a project directory.",
		}
	}

	if p.FetchID != "" {
		return t.fetchChunk(rootPath, p)
	}

	maxResults := t.resolveMaxResults(p.MaxResults)
	minScore := t.resolveMinScore(p.MinScore)

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

	results := t.searchAndFilter(idx, p.Query, p.Glob, p.Language, p.Kind, maxResults, minScore)
	if len(results) == 0 {
		if p.Glob != "" {
			return fmt.Sprintf("[smartsearch: %q] — No relevant results matching %q (try removing the glob filter)", p.Query, p.Glob), nil
		}
		return fmt.Sprintf("[smartsearch: %q] — No relevant results found (try a different query or the search tool for exact matches)", p.Query), nil
	}

	terms := extractQueryTerms(p.Query)
	lineLimit := p.ContextLines
	if lineLimit <= 0 {
		lineLimit = linesPerCandidate
	}
	matches := buildMatchingLinesBounded(results, terms, maxResults, lineLimit, p.MaxTokens)

	idxDir := bm25.IndexDir(rootPath)
	if p.Output == "json" {
		return t.formatStructured(results, matches, p.Query, idx, rebuilt, p.MaxTokens)
	}
	return t.formatResults(results, matches, p.Query, idx, rebuilt, idxDir), nil
}

// fetchChunk resolves an indexed chunk and re-reads the current file, rejecting
// stale IDs rather than returning potentially unsafe edit context.
func (t *SmartSearchTool) fetchChunk(rootPath string, p smartSearchParams) (string, error) {
	idx, _, err := t.getOrBuildIndex(rootPath)
	if err != nil {
		return "", &internal.ToolError{Tool: "smartsearch", Type: "index_error", Detail: err.Error(), HintText: "Re-run smartsearch to rebuild the index."}
	}
	doc, ok := idx.DocumentByID(p.FetchID)
	if !ok {
		return "", &internal.ToolError{Tool: "smartsearch", Type: "invalid_result", Detail: "The requested search result is unknown or stale.", HintText: "Run smartsearch again and fetch a current result id."}
	}
	data, err := os.ReadFile(doc.Path)
	if err != nil {
		return "", &internal.ToolError{Tool: "smartsearch", Type: "fetch_error", Detail: fmt.Sprintf("Cannot read %s: %v", doc.Path, err), HintText: "Check that the file still exists."}
	}
	lines := strings.Split(string(data), "\n")
	start, end := doc.StartLine, doc.EndLine
	if p.StartLine > 0 {
		start = p.StartLine
	}
	if p.EndLine > 0 {
		end = p.EndLine
	}
	if start < 1 || end < start || start > len(lines) {
		return "", &internal.ToolError{Tool: "smartsearch", Type: "invalid_range", Detail: "Requested line range is outside the current file.", HintText: "Use the line range returned by a fresh search result."}
	}
	if end > len(lines) {
		end = len(lines)
	}
	content := strings.Join(lines[start-1:end], "\n")
	if p.MaxTokens > 0 {
		content = limitTokens(content, p.MaxTokens)
	}
	if p.Output == "json" {
		return marshalStructured(fetchResponse{Results: []structuredResult{{ID: doc.ID, Path: doc.Path, Language: doc.Language, Kind: doc.Kind, Symbol: doc.Symbol, StartLine: start, EndLine: end, Evidence: content}}})
	}
	return fmt.Sprintf("smartsearch fetch %s:%d:%d (id %s)\n%s", doc.Path, start, end, doc.ID, content), nil
}

func (t *SmartSearchTool) parseParams(input string) (smartSearchParams, error) {
	var p smartSearchParams
	if err := json.Unmarshal([]byte(input), &p); err != nil {
		return p, &internal.ToolError{Tool: "smartsearch", Type: "invalid_input", Detail: fmt.Sprintf("Cannot parse parameters: %v", err), HintText: "Ensure your input is valid JSON with query as a string."}
	}
	if p.FetchID != "" {
		return p, nil
	}
	if p.Query == "" {
		return p, &internal.ToolError{Tool: "smartsearch", Type: "missing_query", Detail: "Query is required", HintText: "Provide a natural language query describing what code you are looking for."}
	}
	return p, nil
}

func (t *SmartSearchTool) resolveMaxResults(v int) int {
	if v > 0 {
		return v
	}
	return defaultInt(t.MaxResults, 20)
}

func (t *SmartSearchTool) resolveMinScore(v float64) float64 {
	if v > 0 {
		return v
	}
	return t.MinScore
}

func (t *SmartSearchTool) searchAndFilter(idx *bm25.Index, query, glob, language, kind string, maxResults int, minScore float64) []bm25.SearchResult {
	searchLimit := maxResults * 10
	if searchLimit < 100 {
		searchLimit = 100
	}
	results := idx.SearchChunks(query, searchLimit, minScore)
	if glob != "" {
		results = filterByGlob(results, glob)
	}
	if language != "" || kind != "" {
		filtered := results[:0]
		for _, r := range results {
			if (language == "" || strings.EqualFold(r.Language, language)) && (kind == "" || strings.EqualFold(r.Kind, kind)) {
				filtered = append(filtered, r)
			}
		}
		results = filtered
	}
	if len(results) > maxResults {
		results = results[:maxResults]
	}
	normaliseResults(results)
	return results
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
	builder := bm25.NewBuilder(rootPath, idxDir, t.excludes(rootPath))
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

// excludes returns the default exclude directories combined with user config
// and any directory patterns found in the project .gitignore.
func (t *SmartSearchTool) excludes(rootPath string) []string {
	// Always exclude these.
	defaults := []string{".git", "node_modules", "vendor", ".goa", "dist", "build", ".venv", "__pycache__"}
	seen := make(map[string]bool, len(defaults))
	for _, d := range defaults {
		seen[d] = true
	}
	out := append([]string(nil), defaults...)

	if rootPath != "" {
		for _, d := range gitignoreDirs(rootPath) {
			if !seen[d] {
				seen[d] = true
				out = append(out, d)
			}
		}
	}

	for _, d := range t.ExcludeDirs {
		if !seen[d] {
			seen[d] = true
			out = append(out, d)
		}
	}
	return out
}

// gitignoreDirs reads the project .gitignore and returns simple directory-name
// excludes (lines ending with "/" or names without wildcards). Full glob
// support is intentionally limited; this is a best-effort expansion.
func gitignoreDirs(rootPath string) []string {
	data, err := os.ReadFile(filepath.Join(rootPath, ".gitignore"))
	if err != nil {
		return nil
	}
	var dirs []string
	for line := range strings.SplitSeq(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// Strip leading slash if present.
		line = strings.TrimPrefix(line, "/")
		// Only accept simple directory or name patterns without wildcards.
		if strings.ContainsAny(line, "*?[]") {
			continue
		}
		if strings.HasSuffix(line, "/") {
			dirs = append(dirs, strings.TrimSuffix(line, "/"))
			continue
		}
		if !strings.Contains(line, "/") && !strings.Contains(line, ".") {
			// Likely a generated directory name such as "coverage" or "out".
			dirs = append(dirs, line)
		}
	}
	return dirs
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

type structuredResult struct {
	ID         string  `json:"id"`
	Path       string  `json:"path"`
	Score      float64 `json:"score,omitempty"`
	Language   string  `json:"language,omitempty"`
	Kind       string  `json:"kind,omitempty"`
	Symbol     string  `json:"symbol,omitempty"`
	StartLine  int     `json:"start_line"`
	EndLine    int     `json:"end_line"`
	Evidence   string  `json:"evidence,omitempty"`
	Coverage   float64 `json:"coverage,omitempty"`
	Confidence float64 `json:"confidence,omitempty"`
}

type fetchResponse struct {
	Query   string             `json:"query,omitempty"`
	Results []structuredResult `json:"results"`
}

func limitTokens(s string, max int) string {
	if max <= 0 {
		max = 4000
	}
	r := []rune(s)
	if len(r) > max*4 {
		r = r[:max*4]
	}
	return string(r)
}

func buildMatchingLinesBounded(results []bm25.SearchResult, terms []string, maxResults, lineLimit, maxTokens int) map[string][]smartLineMatch {
	matches := buildMatchingLines(results, terms, maxResults)
	used := 0
	for path, lines := range matches {
		if len(lines) > lineLimit {
			lines = lines[:lineLimit]
		}
		for i := range lines {
			if maxTokens > 0 && used+len([]rune(lines[i].Text))/4 > maxTokens {
				lines = lines[:i]
				break
			}
			used += len([]rune(lines[i].Text)) / 4
		}
		if len(lines) == 0 {
			delete(matches, path)
		} else {
			matches[path] = lines
		}
	}
	return matches
}

func (t *SmartSearchTool) formatStructured(results []bm25.SearchResult, matches map[string][]smartLineMatch, query string, idx *bm25.Index, rebuilt bool, maxTokens int) (string, error) {
	out := fetchResponse{Query: query, Results: make([]structuredResult, 0, len(results))}
	for _, r := range results {
		var ev strings.Builder
		for _, line := range matches[r.Path] {
			fmt.Fprintf(&ev, "%d: %s\n", line.Num, ansi.Sanitize(line.Text))
		}
		evidence := strings.TrimSuffix(ev.String(), "\n")
		if evidence == "" {
			evidence = ansi.Sanitize(r.Content)
		}
		out.Results = append(out.Results, structuredResult{ID: r.ID, Path: r.Path, Score: r.Score, Coverage: r.Coverage, Confidence: r.Confidence, Language: r.Language, Kind: r.Kind, Symbol: r.Symbol, StartLine: r.StartLine, EndLine: r.EndLine, Evidence: limitTokens(evidence, maxTokens)})
	}
	return marshalStructured(out)
}

func marshalStructured(v any) (string, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("encode smartsearch results: %w", err)
	}
	return string(data), nil
}

// formatResults produces the output string for a set of ranked results.
// matches carries the matching source lines keyed by absolute path. If rebuilt
// is true, a leading note warns the user that the index had to be rebuilt from
// scratch due to a detected issue. idxDir is the directory that holds the
// on-disk index.
func (t *SmartSearchTool) formatResults(results []bm25.SearchResult, matches map[string][]smartLineMatch, query string, idx *bm25.Index, rebuilt bool, idxDir string) string {
	var buf bytes.Buffer
	buf.WriteString(formatHeader(results, query, idx, rebuilt))
	for i, r := range results {
		buf.WriteString(t.formatOneResult(i, r, matches[r.Path]))
	}
	if idxDir != "" {
		fmt.Fprintf(&buf, "\n(Index: %s)", filepath.Join(idxDir, bm25.IndexFile))
	}
	return buf.String()
}

// formatHeader renders the rebuild warning (when present), the result-count
// summary line, and the score range for user orientation.
func formatHeader(results []bm25.SearchResult, query string, idx *bm25.Index, rebuilt bool) string {
	var b strings.Builder
	if rebuilt {
		fmt.Fprintf(&b, "[smartsearch: %q] — Index was missing or corrupted; rebuilt from scratch.\n", query)
	}
	fmt.Fprintf(&b, "[smartsearch: %q] — %d results from %d indexed files (index age: %s)\n",
		query, len(results), idx.FileCount(), formatDuration(idx.IndexAge()))
	if len(results) > 0 {
		fmt.Fprintf(&b, "Score range: %.2f – %.2f\n\n", results[0].Score, results[len(results)-1].Score)
	}
	return b.String()
}

// formatOneResult renders a single numbered search result followed by its
// matching source lines.
func (t *SmartSearchTool) formatOneResult(i int, r bm25.SearchResult, lineMatches []smartLineMatch) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d. [%.2f] %s  (%d lines)", i+1, r.Score, t.relPath(r.Path), r.Lines)
	if r.ID != "" {
		fmt.Fprintf(&b, " [id: %s, lines: %d-%d, language: %s]", r.ID, r.StartLine, r.EndLine, r.Language)
	}
	b.WriteByte('\n')
	for _, m := range lineMatches {
		b.WriteString(formatMatchLine(m))
	}
	return b.String()
}

// formatMatchLine renders one matching source line, sanitised and truncated
// to at most 140 display columns.
func formatMatchLine(m smartLineMatch) string {
	content := ansi.Sanitize(strings.TrimSpace(m.Text))
	if ansi.Width(content) > 140 {
		content = ansi.Truncate(content, 140) + "…"
	}
	return fmt.Sprintf("    %d: %s\n", m.Num, content)
}

// relPath renders abs relative to the project directory when it points inside
// the project. Paths outside it — or when no project dir is known — stay
// absolute so they remain unambiguous.
func (t *SmartSearchTool) relPath(abs string) string {
	if t.ProjectDir == "" {
		return abs
	}
	p, err := filepath.Rel(t.ProjectDir, abs)
	if err != nil || strings.HasPrefix(p, "..") {
		return abs
	}
	return p
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
func formatDuration(d time.Duration) string {
	return d.String()
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
