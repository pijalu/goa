// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

// Package filefind provides UI-independent file-path completion: an
// fd-backed gitignore-aware search with an os.ReadDir fallback, plus the
// suppression and ranking rules shared by every path completer in goa.
// Consumers include the @-mention completer (tui) and command argument
// completers (e.g. /review:file:).
package filefind

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// maxResults bounds both the fd query and the final ranked candidate list
// (the same cap the @ completer has always applied to fd).
const maxResults = 50

// Entry is one filesystem candidate.
type Entry struct {
	Path  string // project-relative (when under root) or absolute; dirs end with "/"
	IsDir bool
}

// fd CLI discovery, cached for the process lifetime.
var (
	fdAvailable bool
	fdPath      string
)

func init() {
	if path, err := exec.LookPath("fd"); err == nil {
		fdAvailable = true
		fdPath = path
	}
}

// Available reports whether the fd CLI is on PATH (cached).
func Available() bool { return fdAvailable }

// Search is a completion query bound to a root directory.
type Search struct {
	root  string
	useFD bool
}

// New binds a search to root; completed paths that fall under root are made
// relative to it, all others stay absolute.
func New(root string) *Search { return &Search{root: root, useFD: true} }

// WithFD controls whether the fd CLI may be consulted. Callers may force
// the os.ReadDir fallback (tests, hosts without subprocess support) by
// passing false; when fd is simply not installed the fallback runs anyway.
func (s *Search) WithFD(use bool) *Search { s.useFD = use; return s }

// Complete returns ranked candidates for pathPrefix under the search root,
// applying the rules shared by every file completer:
//
//   - a token that already names an existing regular file yields nothing
//     (popup suppressed — "the path is done");
//   - directories complete with a trailing "/" so typing drills down;
//   - ranking is exact > case-sensitive prefix > case-insensitive prefix >
//     fuzzy, shorter basenames first within a tier (Issue 8);
//   - at most maxResults entries are returned.
func (s *Search) Complete(pathPrefix string) []Entry {
	dir, partial := splitPathPrefix(pathPrefix)
	searchDir := resolveSearchDir(dir, s.root)

	// Exact-path suppression: the typed token already names an existing file,
	// so completion is finished — no candidates, no popup.
	if partial != "" && isExistingFile(searchDir, partial) {
		return nil
	}

	candidates := s.fdCandidates(searchDir, partial)
	if len(candidates) == 0 {
		candidates = readDirCandidates(s.root, searchDir, dir, partial)
	}
	ranked := Rank(candidates, partial)
	if len(ranked) > maxResults {
		ranked = ranked[:maxResults]
	}
	return ranked
}

// Complete is the package-level convenience entry point: search root with
// fd when it is installed, falling back to os.ReadDir otherwise.
func Complete(root, pathPrefix string) []Entry {
	return New(root).WithFD(Available()).Complete(pathPrefix)
}

// Rank orders candidates by tier — exact basename match first, then
// case-sensitive prefix, then case-insensitive prefix, then fuzzy — with
// shorter basenames before longer ones inside a tier (closest to a complete
// path), alphabetical as the final tie-break. The input order is preserved
// for full ties. fd and readdir return filesystem order, which left exact
// matches buried under fuzzy ones (Issue 8: @plans/plan offered
// PLAN-00-TEST-INFRA.md before plan.md — both are case-insensitive prefix
// matches, so case sensitivity is the tie-breaker).
func Rank(entries []Entry, partial string) []Entry {
	lower := strings.ToLower(partial)
	tier := func(e Entry) int {
		base := filepath.Base(e.Path)
		switch {
		case base == partial:
			return 0 // exact
		case strings.HasPrefix(base, partial):
			return 1 // prefix, same case as typed
		case strings.HasPrefix(strings.ToLower(base), lower):
			return 2 // prefix, case-insensitive
		default:
			return 3 // fuzzy
		}
	}
	sort.SliceStable(entries, func(i, j int) bool {
		ti, tj := tier(entries[i]), tier(entries[j])
		if ti != tj {
			return ti < tj
		}
		bi, bj := filepath.Base(entries[i].Path), filepath.Base(entries[j].Path)
		if len(bi) != len(bj) {
			return len(bi) < len(bj)
		}
		return bi < bj
	})
	return entries
}

// fdCandidates runs the fd search when allowed and installed. An empty
// result means "no usable output — fall back to os.ReadDir", preserving the
// @ completer's original contract: fd errors and empty results both fall
// through to the directory listing.
func (s *Search) fdCandidates(searchDir, query string) []Entry {
	if !s.useFD || !fdAvailable {
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	results, err := fdSearch(ctx, searchDir, query, maxResults)
	if err != nil || len(results) == 0 {
		return nil
	}
	entries := make([]Entry, 0, len(results))
	for _, r := range results {
		path := normalizePath(s.root, r.Path)
		if r.IsDirectory {
			// Directory drill-down: end the path with "/" like the
			// os.ReadDir fallback does.
			path += "/"
		}
		entries = append(entries, Entry{
			Path:  path,
			IsDir: r.IsDirectory,
		})
	}
	return entries
}

// fdResult is a single raw fd hit: a baseDir-relative slash path plus its
// directory status learned from stat.
type fdResult struct {
	Path        string
	IsDirectory bool
}

// fdSearch runs `fd` to list files and directories matching query under
// baseDir. Output lines are relative to baseDir (fd's --base-directory
// semantics); each hit is stat-ed so callers learn IsDirectory without a
// second walk.
func fdSearch(ctx context.Context, baseDir, query string, maxResults int) ([]fdResult, error) {
	if !fdAvailable {
		return nil, nil // fd not available, caller should fall back
	}

	args := []string{
		"--base-directory", baseDir,
		"--max-results", fmt.Sprintf("%d", maxResults),
		"--type", "f",
		"--type", "d",
		"--follow",
		"--hidden",
		"--exclude", ".git",
		"--exclude", ".git/*",
		"--exclude", ".git/**",
	}

	// Use full-path mode for multi-segment queries
	if strings.Contains(query, "/") {
		args = append(args, "--full-path")
	}

	if query != "" {
		args = append(args, query)
	}

	cmd := exec.CommandContext(ctx, fdPath, args...)
	output, err := cmd.Output()
	if err != nil {
		// Command may fail if context is cancelled or no matches
		return nil, err
	}

	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	results := make([]fdResult, 0, len(lines))
	for _, line := range lines {
		if line == "" {
			continue
		}
		info, statErr := os.Stat(filepath.Join(baseDir, line))
		if statErr != nil {
			continue // vanished between listing and stat
		}
		results = append(results, fdResult{
			Path:        filepath.ToSlash(filepath.Join(baseDir, line)),
			IsDirectory: info.IsDir(),
		})
	}
	return results, nil
}

// readDirCandidates is the os.ReadDir fallback used when fd is unavailable:
// one listing level of searchDir, filtered by typed-case prefix or fuzzy
// match on the entry name (same rule the fallback always applied).
func readDirCandidates(root, searchDir, dir, partial string) []Entry {
	entries, err := os.ReadDir(searchDir)
	if err != nil {
		return nil
	}
	var candidates []Entry
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, partial) && !fuzzyMatch(partial, name) {
			continue
		}
		// Rebuild the candidate from the typed dir part so relative and
		// absolute prefixes round-trip identically.
		pathVal := filepath.ToSlash(dir + name)
		candidates = append(candidates, Entry{
			Path:  normalizePath(root, pathVal),
			IsDir: e.IsDir(),
		})
		if e.IsDir() {
			// Directory drill-down: mark the path as completable further by
			// ending it with "/".
			candidates[len(candidates)-1].Path += "/"
		}
	}
	return candidates
}

// normalizePath makes path project-relative when it falls under root and
// leaves it untouched otherwise (absolute outside root). Empty root keeps
// paths exactly as produced.
func normalizePath(root, path string) string {
	if root == "" {
		return path
	}
	rootSlash := filepath.ToSlash(root) + "/"
	if strings.HasPrefix(path, rootSlash) {
		return path[len(rootSlash):]
	}
	return path
}

// splitPathPrefix splits a path prefix into its directory part (with
// trailing "/", possibly empty) and the partial final segment.
func splitPathPrefix(prefix string) (dir, partial string) {
	if strings.Contains(prefix, "/") {
		idx := strings.LastIndex(prefix, "/")
		return prefix[:idx+1], prefix[idx+1:]
	}
	return "", prefix
}

// resolveSearchDir anchors a typed dir against workdir: relative dirs are
// appended to workdir, absolute dirs are used as-is, empty resolves to ".".
func resolveSearchDir(dir, workdir string) string {
	searchDir := dir
	if !strings.HasPrefix(dir, "/") && workdir != "" {
		searchDir = workdir + "/" + dir
	}
	if searchDir == "" {
		return "."
	}
	return searchDir
}

// isExistingFile reports whether dir/name is an existing regular file.
func isExistingFile(dir, name string) bool {
	info, err := os.Stat(filepath.Join(dir, name))
	return err == nil && info.Mode().IsRegular()
}

// fuzzyMatch implements simple prefix + character-in-order fuzzy matching.
// Returns true if:
//   - pattern matches the START of candidate (prefix match on name part)
//   - OR all characters in pattern appear in order in candidate
//     (e.g., "md" matches "/mode")
//
// Does NOT do substring matching — "res" should not match "/compress"
// even though "compress" contains "res" as a substring.
func fuzzyMatch(pattern, candidate string) bool {
	patternLower := strings.ToLower(pattern)
	candLower := strings.ToLower(candidate)

	// Only fuzzy match on the name part after /, not the leading slash itself.
	namePart := candLower
	if strings.HasPrefix(candLower, "/") && len(candLower) > 1 {
		namePart = candLower[1:]
	}
	patternName := patternLower
	if strings.HasPrefix(patternLower, "/") && len(patternLower) > 1 {
		patternName = patternLower[1:]
	}

	// Minimum meaningful pattern length for fuzzy matching —
	// single-char patterns only do prefix match.
	if len(patternName) < 2 {
		return false
	}

	// Prefix match on the name part (not substring match).
	// "res" matches "restore" but NOT "compress".
	if strings.HasPrefix(namePart, patternName) {
		return true
	}

	// Character-in-order match (e.g., "md" matches "mode")
	pi := 0
	for _, c := range namePart {
		if pi < len(patternName) && c == rune(patternName[pi]) {
			pi++
		}
	}
	return pi == len(patternName)
}
