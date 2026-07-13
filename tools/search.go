// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"bytes"
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
)

// SearchTool searches for patterns in files using goroutine pool for
// parallelism. Results are sorted by file path and line number.
type SearchTool struct {
	WorktreeMgr *internal.WorktreeManager
	Threads     int
	MaxResults  int
	ExcludeDirs []string
}

// searchResult holds a single match from the search.
type searchResult struct {
	Path    string
	LineNum int
	Line    string
	Score   int // number of regex matches on this line
}

// Schema returns the tool schema for search.
func (t *SearchTool) Schema() agentic.ToolSchema {
	return agentic.ToolSchema{
		Name: "search",
		Description: "Fast parallel code/text search (regex) across files. PREFER this over `bash`+grep/rg for any codebase search: it auto-excludes .git/vendor/node_modules, respects globs, and returns ranked, structured results (file: matches + line numbers + content). Use `bash` with grep only for features this tool cannot do (e.g. searching command output, pipes, PCRE-only constructs).",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"pattern": map[string]any{
					"type":        "string",
					"description": "Regex pattern to search for (Go RE2 syntax; case-insensitive by default).",
				},
				"path": map[string]any{
					"type":        "string",
					"description": "Root directory or a single file to search (default: project root). Pass a file path to search one file.",
				},
				"glob": map[string]any{
					"type":        "string",
					"description": "Comma-separated file glob filter, e.g. \"*.go\" or \"*.go,*.ts\".",
				},
				"recursive": map[string]any{
					"type":        "boolean",
					"description": "Search subdirectories recursively (default: true).",
				},
				"case_sensitive": map[string]any{
					"type":        "boolean",
					"description": "Case-sensitive match (default: false).",
				},
				"max_results": map[string]any{
					"type":        "integer",
					"description": "Maximum total matched lines to return.",
				},
				"context_lines": map[string]any{
					"type":        "integer",
					"description": "Context lines shown around each match.",
				},
				"showing": map[string]any{
					"type":        "integer",
					"description": "Content lines per file (default: 10% of max_results; 0 = line numbers only).",
				},
				"exclude_glob": map[string]any{
					"type":        "string",
					"description": "Glob pattern to exclude files (e.g. '*_test.go').",
				},
			},
			"required": []string{"pattern"},
		},
	}
}

// searchParams holds the parsed input for SearchTool.
type searchParams struct {
	Pattern       string   `json:"pattern"`
	RootPath      string   `json:"path"`
	Glob          string   `json:"glob"`
	ExcludeGlob   string   `json:"exclude_glob"`
	Recursive     bool     `json:"recursive"`
	CaseSensitive bool     `json:"case_sensitive"`
	MaxResults    int      `json:"max_results"`
	ContextLines  int      `json:"context_lines"`
	Showing       int      `json:"showing"`
	Threads       int      `json:"threads"`
	Exclude       []string `json:"exclude"`
}

// normalizeSearchParams fills defaults and resolves paths for a search.
func (t *SearchTool) normalizeSearchParams(p *searchParams, input string) (rootPath string, excludes []string) {
	if p.RootPath == "" {
		p.RootPath = "."
	}
	if p.MaxResults <= 0 {
		p.MaxResults = defaultInt(t.MaxResults, 30)
	}
	if p.Threads <= 0 {
		p.Threads = defaultInt(t.Threads, 4)
	}
	if p.ContextLines <= 0 {
		p.ContextLines = 1
	}

	// Parse raw JSON to detect missing optional fields
	var raw map[string]any
	json.Unmarshal([]byte(input), &raw)

	if _, ok := raw["showing"]; !ok {
		p.Showing = p.MaxResults / 10
		if p.Showing < 1 {
			p.Showing = 1
		}
	}
	if _, ok := raw["recursive"]; !ok {
		p.Recursive = true
	}

	excludes = append(t.ExcludeDirs, p.Exclude...)

	rootPath = p.RootPath
	if t.WorktreeMgr != nil {
		worktreePath := t.WorktreeMgr.CurrentWorktree()
		if worktreePath != "" {
			rootPath = t.WorktreeMgr.ResolvePath(worktreePath, rootPath)
		}
	}
	return rootPath, excludes
}

// searchPipePattern attempts splitting pattern on | and merging results.
func (t *SearchTool) searchPipePattern(files []string, pattern string, caseSensitive bool, threads, maxResults, showing int) (string, bool) {
	if !strings.Contains(pattern, "|") {
		return "", false
	}
	parts := strings.Split(pattern, "|")
	var combined []searchResult
	seen := make(map[string]bool)
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		results := t.searchWithPattern(files, part, caseSensitive, threads)
		for _, res := range results {
			key := fmt.Sprintf("%s:%d", res.Path, res.LineNum)
			if !seen[key] {
				seen[key] = true
				combined = append(combined, res)
			}
		}
	}
	if len(combined) == 0 {
		return "", false
	}
	t.sortResults(combined)
	return t.formatResults(combined, pattern, maxResults, showing), true
}

// Execute performs the search.
func (t *SearchTool) Execute(input string) (string, error) {
	var p searchParams
	if err := json.Unmarshal([]byte(input), &p); err != nil {
		return "", &internal.ToolError{
			Tool: "search", Type: "invalid_input",
			Detail:   fmt.Sprintf("Cannot parse parameters: %v", err),
			HintText: "Ensure your input is valid JSON with the required fields.",
		}
	}
	if p.Pattern == "" {
		return "", &internal.ToolError{
			Tool: "search", Type: "missing_pattern",
			Detail: "Pattern is required", HintText: "Provide a regex pattern to search for.",
		}
	}

	rootPath, excludes := t.normalizeSearchParams(&p, input)
	files := t.collectFiles(rootPath, p.Glob, p.ExcludeGlob, excludes, p.Recursive)
	if len(files) == 0 {
		return "[search: no matching files found]", nil
	}

	results := t.searchWithPattern(files, p.Pattern, p.CaseSensitive, p.Threads)
	if len(results) == 0 {
		if out, ok := t.searchPipePattern(files, p.Pattern, p.CaseSensitive, p.Threads, p.MaxResults, p.Showing); ok {
			return out, nil
		}
	}

	t.sortResults(results)
	return t.formatResults(results, p.Pattern, p.MaxResults, p.Showing), nil
}

// searchWithPattern compiles a regex and searches the given files.
func (t *SearchTool) searchWithPattern(files []string, pattern string, caseSensitive bool, threads int) []searchResult {
	patternStr := pattern
	if !caseSensitive {
		patternStr = "(?i:" + pattern + ")"
	}
	re, err := regexp.Compile(patternStr)
	if err != nil {
		return nil
	}
	return t.searchFiles(files, re, caseSensitive, threads)
}

func (t *SearchTool) walkRecursiveFiles(rootPath, glob, excludeGlob string, excludes []string) []string {
	globs := trimGlobParts(glob)
	var files []string
	filepath.WalkDir(rootPath, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if t.shouldSkipDir(d.Name(), excludes) {
				return filepath.SkipDir
			}
			return nil
		}
		if !matchAnyGlob(path, globs, rootPath) {
			return nil
		}
		if excludeGlob != "" && matchGlob(path, excludeGlob, rootPath) {
			return nil
		}
		files = append(files, path)
		return nil
	})
	return files
}

func (t *SearchTool) listDirFiles(rootPath, glob, excludeGlob string) []string {
	globs := trimGlobParts(glob)
	entries, err := os.ReadDir(rootPath)
	if err != nil {
		return nil
	}
	var files []string
	for _, d := range entries {
		if d.IsDir() {
			continue
		}
		fullPath := filepath.Join(rootPath, d.Name())
		if !matchAnyGlob(fullPath, globs, rootPath) {
			continue
		}
		if excludeGlob != "" && matchGlob(fullPath, excludeGlob, rootPath) {
			continue
		}
		files = append(files, fullPath)
	}
	return files
}

func trimGlobParts(glob string) []string {
	parts := strings.Split(glob, ",")
	for i, g := range parts {
		parts[i] = strings.TrimSpace(g)
	}
	return parts
}

func (t *SearchTool) collectFiles(rootPath, glob, excludeGlob string, excludes []string, recursive bool) []string {
	// If rootPath points to a single file, search that file directly
	// (single-file mode). This allows "search in <file>" usage.
	if fi, err := os.Stat(rootPath); err == nil && !fi.IsDir() {
		// Apply glob filter if specified
		if glob != "" && !matchAnyGlob(rootPath, trimGlobParts(glob), filepath.Dir(rootPath)) {
			return nil
		}
		if excludeGlob != "" && matchGlob(rootPath, excludeGlob, filepath.Dir(rootPath)) {
			return nil
		}
		return []string{rootPath}
	}
	if recursive {
		return t.walkRecursiveFiles(rootPath, glob, excludeGlob, excludes)
	}
	return t.listDirFiles(rootPath, glob, excludeGlob)
}

// matchAnyGlob returns true if the file path matches any of the given glob
// patterns. An empty globs slice means all files match.
func matchAnyGlob(filePath string, globs []string, rootPath string) bool {
	if len(globs) == 0 || (len(globs) == 1 && globs[0] == "") {
		return true
	}
	for _, g := range globs {
		if g == "" {
			continue
		}
		if matchGlob(filePath, g, rootPath) {
			return true
		}
	}
	return false
}

// matchGlob checks whether filePath matches the given glob pattern, with
// support for ** (match any number of directory levels). rootPath is used
// to compute the relative path for matching.
func matchGlob(filePath, glob, rootPath string) bool {
	// Get path relative to root
	rel, err := filepath.Rel(rootPath, filePath)
	if err != nil {
		return false
	}
	rel = filepath.ToSlash(rel)
	glob = filepath.ToSlash(glob)

	// If no ** in the pattern, use filepath.Match on the relative path
	if !strings.Contains(glob, "**") {
		matched, _ := filepath.Match(glob, filepath.Base(rel))
		return matched
	}

	// Support ** glob: split by **, each part must match in order
	parts := strings.Split(glob, "**")
	// Build the regex from parts
	var buf bytes.Buffer
	buf.WriteString("^")
	for i, part := range parts {
		if i > 0 {
			buf.WriteString(".*")
		}
		if part != "" {
			// Escape special regex chars and convert * to [^/]* for single-component matching
			regexPart := regexp.QuoteMeta(part)
			regexPart = strings.ReplaceAll(regexPart, "\\*", "[^/]*")
			buf.WriteString(regexPart)
		}
	}
	buf.WriteString("$")
	re, err := regexp.Compile(buf.String())
	if err != nil {
		return false
	}
	return re.MatchString(rel)
}

func (t *SearchTool) shouldSkipDir(name string, excludes []string) bool {
	// Never skip . or .. (the root directory itself)
	if name == "." || name == ".." {
		return false
	}
	for _, ex := range excludes {
		if name == ex || strings.HasPrefix(name, ".") {
			return true
		}
	}
	return false
}

func (t *SearchTool) searchFiles(files []string, re *regexp.Regexp, caseSensitive bool, threads int) []searchResult {
	resultChan := make(chan searchResult, 100)
	var wg sync.WaitGroup
	fileChan := make(chan string, len(files))

	worker := func() {
		defer wg.Done()
		for f := range fileChan {
			for _, m := range t.searchFile(f, re, caseSensitive) {
				resultChan <- m
			}
		}
	}

	workerCount := threads
	if workerCount > len(files) {
		workerCount = len(files)
	}
	wg.Add(workerCount)
	for i := 0; i < workerCount; i++ {
		go worker()
	}

	for _, f := range files {
		fileChan <- f
	}
	close(fileChan)

	go func() {
		wg.Wait()
		close(resultChan)
	}()

	var results []searchResult
	for r := range resultChan {
		results = append(results, r)
	}
	return results
}

// sortResults sorts results by file extension priority (source code first),
// then by number of matches per file (most matches first), then by line number.
func (t *SearchTool) sortResults(results []searchResult) {
	// Compute total score per file for secondary sort.
	fileScore := make(map[string]int)
	for _, r := range results {
		fileScore[r.Path] += r.Score
	}

	sort.Slice(results, func(i, j int) bool {
		a, b := results[i], results[j]
		if a.Path != b.Path {
			pa, pb := extPriority(a.Path), extPriority(b.Path)
			if pa != pb {
				return pa < pb // lower priority = higher rank (source first)
			}
			// Same extension priority: files with highest total score first
			sa, sb := fileScore[a.Path], fileScore[b.Path]
			if sa != sb {
				return sa > sb
			}
			return a.Path < b.Path
		}
		// Same file: higher line score first, then line number
		if a.Score != b.Score {
			return a.Score > b.Score
		}
		return a.LineNum < b.LineNum
	})
}

func (t *SearchTool) formatResults(results []searchResult, pattern string, maxResults, showing int) string {
	groups := groupAndSortResults(results)
	fileLimit := computeFileLimit(maxResults, showing)

	var buf bytes.Buffer
	totalShown := 0
	filesShown := 0

	for _, g := range groups {
		if filesShown >= fileLimit {
			break
		}
		filesShown++
		fmt.Fprintf(&buf, "%s: %d matches\n", g.path, g.matches)
		formatFileLineNums(&buf, g, showing)
		// formatFileContentLines advances totalShown via the pointer for the
		// maxResults guard; do not also add its return value — that double-counts
		// and inflates the "showing N" summary while prematurely truncating later
		// files once the doubled counter reaches maxResults.
		formatFileContentLines(&buf, g, maxResults, showing, &totalShown)
	}

	remaining := len(results) - countLinesShown(groups, showing)
	totalFiles := len(groups)
	if remaining > 0 {
		return fmt.Sprintf("[search: %q] — %d matches across %d files, showing %d (%d truncated)\n%s",
			pattern, len(results), totalFiles, totalShown, remaining, buf.String())
	}
	return fmt.Sprintf("[search: %q] — %d matches across %d files\n%s", pattern, len(results), totalFiles, buf.String())
}

type fileGroup struct {
	path    string
	matches int
	lines   []searchResult
}

func groupAndSortResults(results []searchResult) []*fileGroup {
	fileMap := make(map[string]*fileGroup)
	for _, r := range results {
		g, ok := fileMap[r.Path]
		if !ok {
			g = &fileGroup{path: r.Path}
			fileMap[r.Path] = g
		}
		g.matches += r.Score
		g.lines = append(g.lines, r)
	}
	for _, g := range fileMap {
		sort.Slice(g.lines, func(i, j int) bool {
			if g.lines[i].Score != g.lines[j].Score {
				return g.lines[i].Score > g.lines[j].Score
			}
			return g.lines[i].LineNum < g.lines[j].LineNum
		})
	}
	groups := make([]*fileGroup, 0, len(fileMap))
	for _, g := range fileMap {
		groups = append(groups, g)
	}
	sort.Slice(groups, func(i, j int) bool {
		pi, pj := extPriority(groups[i].path), extPriority(groups[j].path)
		if pi != pj {
			return pi < pj
		}
		if groups[i].matches != groups[j].matches {
			return groups[i].matches > groups[j].matches
		}
		return groups[i].path < groups[j].path
	})
	return groups
}

func computeFileLimit(maxResults, showing int) int {
	if showing <= 1 {
		return maxResults
	}
	limit := maxResults / showing
	if limit < 1 {
		limit = 1
	}
	return limit
}

func formatFileLineNums(buf *bytes.Buffer, g *fileGroup, showing int) {
	lineNums := make([]string, len(g.lines))
	for i, r := range g.lines {
		lineNums[i] = fmt.Sprintf("%d", r.LineNum)
	}
	displayLines := lineNums
	moreLines := 0
	if showing > 0 && len(lineNums) > showing {
		displayLines = lineNums[:showing]
		moreLines = len(lineNums) - showing
	}
	lineStr := strings.Join(displayLines, "/")
	if moreLines > 0 {
		lineStr += fmt.Sprintf(" (+%d more)", moreLines)
	}
	if lineStr != "" {
		fmt.Fprintf(buf, "  %s\n", lineStr)
	}
}

func formatFileContentLines(buf *bytes.Buffer, g *fileGroup, maxResults, showing int, totalShown *int) {
	for i, r := range g.lines {
		if *totalShown >= maxResults || (showing > 0 && i >= showing) {
			break
		}
		content := strings.TrimSpace(r.Line)
		if len(content) > 120 {
			content = content[:120] + "…"
		}
		fmt.Fprintf(buf, "  %d: %s\n", r.LineNum, content)
		(*totalShown)++
	}
}

func countLinesShown(groups []*fileGroup, showing int) int {
	total := 0
	for _, g := range groups {
		if showing > 0 && len(g.lines) > showing {
			total += showing
		} else {
			total += len(g.lines)
		}
	}
	return total
}

// defaultStr returns val if non-empty, otherwise defaultVal.
func defaultStr(val, defaultVal string) string {
	if val == "" {
		return defaultVal
	}
	return val
}

// defaultInt returns val if > 0, otherwise defaultVal.
func defaultInt(val, defaultVal int) int {
	if val <= 0 {
		return defaultVal
	}
	return val
}

func (t *SearchTool) IsRetryable(err error) bool { return false }

//go:embed search.short.md search.long.md
var searchDocs embed.FS

func (t *SearchTool) ShortDoc() string { return readDoc(searchDocs, "search.short.md") }
func (t *SearchTool) LongDoc() string  { return readDoc(searchDocs, "search.long.md") }

func (t *SearchTool) Examples() []string {
	return []string{
		`{"pattern": "TODO"}`,
		`{"pattern": "func.*Handler", "glob": "*.go", "max_results": 10}`,
		`{"pattern": "FIXME", "path": "src/", "case_sensitive": true}`,
		`{"pattern": "configSetters", "path": "core/commands/config_cli.go"}`, // single-file search
	}
}

// searchFile searches a single file for pattern matches.
func (t *SearchTool) searchFile(path string, re *regexp.Regexp, caseSensitive bool) []searchResult {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}

	// Skip binary files
	if isBinary(data) {
		return nil
	}

	lines := strings.Split(string(data), "\n")
	var results []searchResult

	for i, line := range lines {
		// Regex already handles case-insensitivity via (?i:) prefix when needed
		matches := re.FindAllStringIndex(line, -1)
		if len(matches) > 0 {
			results = append(results, searchResult{
				Path:    path,
				LineNum: i + 1,
				Line:    line,
				Score:   len(matches),
			})
		}
	}

	return results
}
