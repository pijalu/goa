// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package common

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/internal/agentic"
)

// writeInto writes content to dir/rel, creating parent directories.
func writeInto(t *testing.T, dir, rel, content string) string {
	t.Helper()
	path := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

// --- file tool config ---

// TestFileToolConfigDefaults pins the documented "zero value means enabled"
// contract for both the dedup and fuzzy-match switches.
func TestFileToolConfigDefaults(t *testing.T) {
	var zero FileToolConfig
	if !FileToolDedupEnabled(zero) {
		t.Error("dedup must default to enabled for the zero value")
	}
	if !FileToolFuzzyMatchEnabled(zero) {
		t.Error("fuzzy match must default to enabled for the zero value")
	}

	off := false
	on := true
	if FileToolDedupEnabled(FileToolConfig{Dedup: &off}) {
		t.Error("explicit dedup=false must be honoured")
	}
	if !FileToolDedupEnabled(FileToolConfig{Dedup: &on}) {
		t.Error("explicit dedup=true must be honoured")
	}
	if FileToolFuzzyMatchEnabled(FileToolConfig{FuzzyMatch: &off}) {
		t.Error("explicit fuzzy_match=false must be honoured")
	}
	if !FileToolFuzzyMatchEnabled(FileToolConfig{FuzzyMatch: &on}) {
		t.Error("explicit fuzzy_match=true must be honoured")
	}
}

// TestResolveFileToolPath_StripsAtPrefix pins that the "@" marker is removed
// and both the resolved and normalized paths come back.
func TestResolveFileToolPath_StripsAtPrefix(t *testing.T) {
	project := t.TempDir()
	wm := internal.NewWorktreeManager(project, internal.WorktreeAlways)

	resolved, original, err := ResolveFileToolPath(wm, "@src/main.go")
	if err != nil {
		t.Fatalf("ResolveFileToolPath: %v", err)
	}
	if original != "src/main.go" {
		t.Errorf("original = %q, want src/main.go (the @ marker must be stripped)", original)
	}
	if !filepath.IsAbs(resolved) {
		t.Errorf("resolved = %q, want an absolute path", resolved)
	}
	if !strings.HasSuffix(resolved, filepath.Join("src", "main.go")) {
		t.Errorf("resolved = %q, want it to end with src/main.go", resolved)
	}
}

// TestReadFileWithFuzzyFallback pins all four read paths: exact hit, fuzzy
// recovery from a near-miss name, fuzzy disabled, and no close match.
func TestReadFileWithFuzzyFallback(t *testing.T) {
	dir := t.TempDir()
	writeInto(t, dir, "widgets.go", "package widgets\n")

	// Exact hit.
	path, data, err := ReadFileWithFuzzyFallback(FileToolConfig{}, filepath.Join(dir, "widgets.go"), "widgets.go")
	if err != nil {
		t.Fatalf("exact read: %v", err)
	}
	if path != filepath.Join(dir, "widgets.go") || !strings.Contains(string(data), "package widgets") {
		t.Errorf("exact read returned path=%q data=%q", path, data)
	}

	// Near-miss name ("widget.go" vs "widgets.go") recovers via fuzzy matching.
	path, data, err = ReadFileWithFuzzyFallback(FileToolConfig{}, filepath.Join(dir, "widget.go"), "widget.go")
	if err != nil {
		t.Fatalf("fuzzy read: %v", err)
	}
	if path != filepath.Join(dir, "widgets.go") {
		t.Errorf("fuzzy read path = %q, want the near-match widgets.go", path)
	}
	if !strings.Contains(string(data), "package widgets") {
		t.Errorf("fuzzy read data = %q", data)
	}

	// Fuzzy disabled: the original not-exist error surfaces.
	off := false
	if _, _, err := ReadFileWithFuzzyFallback(FileToolConfig{FuzzyMatch: &off}, filepath.Join(dir, "widget.go"), "widget.go"); err == nil {
		t.Error("fuzzy-disabled read of a missing file must fail")
	} else if !os.IsNotExist(err) {
		t.Errorf("error = %v, want a not-exist error", err)
	}

	// No close match: still an error, and no file is invented.
	if _, _, err := ReadFileWithFuzzyFallback(FileToolConfig{}, filepath.Join(dir, "completely_different.txt"), "completely_different.txt"); err == nil {
		t.Error("read without a close match must fail")
	}
}

// --- tool groups ---

// TestToolGroup_NamesAndMatch pins the group bookkeeping used to unregister a
// whole namespace at once.
func TestToolGroup_NamesAndMatch(t *testing.T) {
	group := &ToolGroup{
		Prefix: "mcp__server__",
		Tools: []agentic.Tool{
			stubTool{name: "mcp__server__read"},
			stubTool{name: "mcp__server__write"},
		},
	}
	names := group.Names()
	if len(names) != 2 || names[0] != "mcp__server__read" || names[1] != "mcp__server__write" {
		t.Errorf("Names() = %v", names)
	}
	if !group.Match("mcp__server__read") {
		t.Error("Match must accept a member name")
	}
	if group.Match("other__tool") {
		t.Error("Match must reject a non-member name")
	}
	if (&ToolGroup{Prefix: ""}).Match("anything") != true {
		t.Error("an empty prefix matches everything (documented prefix semantics)")
	}
	if len((&ToolGroup{}).Names()) != 0 {
		t.Error("empty group must report no names")
	}
}

type stubTool struct {
	agentic.BaseTool
	name string
}

func (s stubTool) Schema() agentic.ToolSchema {
	return agentic.ToolSchema{Name: s.name, Description: "stub"}
}

func (s stubTool) Execute(string) (string, error) { return "", nil }

// --- spill store ---

// TestSpillStore_Dir pins the accessor used by callers that report spill paths.
func TestSpillStore_Dir(t *testing.T) {
	dir := t.TempDir()
	if got := NewSpillStore(dir).Dir(); got != dir {
		t.Errorf("Dir() = %q, want %q", got, dir)
	}
	if got := NewSpillStore("").Dir(); got != "" {
		t.Errorf("Dir() = %q, want empty", got)
	}
}

// TestSanitizeSpillSegment pins the untrusted-input reduction, including the
// traversal tokens that must never survive.
func TestSanitizeSpillSegment(t *testing.T) {
	cases := map[string]string{
		"":                    "spill",
		".":                   "_",
		"..":                  "__",
		"../../etc/passwd":    ".._.._etc_passwd",
		"bash.txt":            "bash.txt",
		"a b/c\\d":            "a_b_c_d",
		"safe_name-1.2":       "safe_name-1.2",
		"emoji🙂name":          "emoji_name",
		"a\u00e9b":            "a_b",
		"UPPER_and-lower.123": "UPPER_and-lower.123",
	}
	for in, want := range cases {
		if got := sanitizeSpillSegment(in); got != want {
			t.Errorf("sanitizeSpillSegment(%q) = %q, want %q", in, got, want)
		}
	}
}

// --- truncation ---

// TestTruncateHead_Untruncated pins the no-op case and its empty description.
func TestTruncateHead_Untruncated(t *testing.T) {
	content := strings.Join([]string{"l1", "l2", "l3", "l4", "l5"}, "\n")
	whole := TruncateHead(content, 10, 1000)
	if whole.Truncated || whole.Content != content {
		t.Errorf("untruncated head result = %+v", whole)
	}
	if got := TruncResString(whole); got != "" {
		t.Errorf("TruncResString on untruncated = %q, want empty", got)
	}
}

// TestTruncateHead_ByLines pins the line-budget path and its totals.
func TestTruncateHead_ByLines(t *testing.T) {
	content := strings.Join([]string{"l1", "l2", "l3", "l4", "l5"}, "\n")
	res := TruncateHead(content, 2, 1000)
	if !res.Truncated || res.TruncatedBy != "lines" {
		t.Fatalf("by-lines truncation = %+v", res)
	}
	if res.OutputLines != 2 || res.Content != "l1\nl2" {
		t.Errorf("head kept %d lines: %q", res.OutputLines, res.Content)
	}
	if res.TotalLines != 5 || res.TotalBytes != len(content) {
		t.Errorf("totals not reported: %+v", res)
	}
	if got := TruncResString(res); !strings.Contains(got, "2/5 lines") || !strings.Contains(got, "(by lines)") {
		t.Errorf("TruncResString = %q", got)
	}
}

// TestTruncateHead_ByBytesAndOversizedFirstLine pins the byte-budget paths.
func TestTruncateHead_ByBytesAndOversizedFirstLine(t *testing.T) {
	byBytes := TruncateHead("l1\nl2\nl3\nl4\nl5", 100, 4)
	if !byBytes.Truncated || byBytes.TruncatedBy != "bytes" {
		t.Errorf("by-bytes truncation = %+v", byBytes)
	}

	firstLineTooLong := TruncateHead(strings.Repeat("x", 50), 5, 10)
	if !firstLineTooLong.FirstLineExceeds || firstLineTooLong.OutputLines != 0 {
		t.Errorf("oversized first line = %+v", firstLineTooLong)
	}
	if got := TruncResString(firstLineTooLong); !strings.Contains(got, "0/1 lines") {
		t.Errorf("TruncResString = %q", got)
	}
}

// TestSaveTruncatedOutput pins the spill helper used when output is truncated.
func TestSaveTruncatedOutput(t *testing.T) {
	spillPath, err := SaveTruncatedOutput("payload")
	if err != nil {
		t.Fatalf("SaveTruncatedOutput: %v", err)
	}
	t.Cleanup(func() { os.Remove(spillPath) })
	data, err := os.ReadFile(spillPath)
	if err != nil || string(data) != "payload" {
		t.Errorf("spilled content = %q (err %v)", data, err)
	}
}

// TestSplitLinesForCountingAndTailPartial pins the line splitter and the
// partial-last-line behaviour of tail truncation.
func TestSplitLinesForCountingAndTailPartial(t *testing.T) {
	if got := splitLinesForCounting(""); got != nil {
		t.Errorf("empty content = %v, want nil", got)
	}
	if got := splitLinesForCounting("a\nb\n"); len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("trailing newline not dropped: %#v", got)
	}
	if got := splitLinesForCounting("a\nb"); len(got) != 2 || got[1] != "b" {
		t.Errorf("no trailing newline mishandled: %#v", got)
	}

	// A tail cut that lands inside the final line keeps a partial last line.
	res := TruncateTail("first line\nsecond line", 2, 6)
	if !res.Truncated || res.TruncatedBy != "bytes" {
		t.Fatalf("tail byte truncation = %+v", res)
	}
	if !res.LastLinePartial {
		t.Errorf("LastLinePartial = false, want true for a mid-line cut: %+v", res)
	}
	if len(res.Content) > 6 {
		t.Errorf("tail content exceeds the byte limit: %q", res.Content)
	}
}

// TestTruncateStringFromEndKeepsUTF8 pins the rune-boundary guarantee.
func TestTruncateStringFromEndKeepsUTF8(t *testing.T) {
	if got := truncateStringFromEnd("short", 100); got != "short" {
		t.Errorf("short string changed: %q", got)
	}
	// Each "é" is 2 bytes; a 5-byte budget would otherwise split a rune.
	got := truncateStringFromEnd("ééééé", 5)
	if !utf8.ValidString(got) {
		t.Errorf("result is not valid UTF-8: %q", got)
	}
	if len(got) > 5 {
		t.Errorf("result exceeds the byte budget: %q (%d bytes)", got, len(got))
	}
	if len(got) > 0 && !strings.HasPrefix(got, "\xc3") {
		t.Errorf("result must start on a rune boundary: %q", got)
	}
}

// --- path scoring ---

// TestBaseMatchScoreAndScoreFile pins the fuzzy-match scoring tiers.
func TestBaseMatchScoreAndScoreFile(t *testing.T) {
	cases := []struct {
		target, base string
		want         float64
	}{
		{"main.go", "main.go.orig", 0.8}, // base has the target as a prefix
		{"main.go.orig", "main.go", 0.7}, // target has the base as a prefix
		{"main", "main.go", 0.8},         // both: prefix wins with 0.8
		{"thing", "otherthing", 0.5},     // substring only
		{"nope", "zzz", 0.0},             // no relation
	}
	for _, tc := range cases {
		if got := baseMatchScore(tc.target, tc.base); got != tc.want {
			t.Errorf("baseMatchScore(%q, %q) = %v, want %v", tc.target, tc.base, got, tc.want)
		}
	}

	// A near-miss ranks highly via Levenshtein similarity…
	if got := scoreFile("widget.go", "widgets.go"); got < 0.8 {
		t.Errorf("scoreFile near-miss = %v, want a high score", got)
	}
	// …while an exact prefix keeps its tier even if similarity is lower.
	if got := scoreFile("main", "main.go"); got != 0.8 {
		t.Errorf("scoreFile prefix = %v, want 0.8", got)
	}
	if got := levenshteinSimilarity("", ""); got != 0.0 {
		t.Errorf("levenshteinSimilarity of two empty strings = %v, want 0", got)
	}
	if got := LevenshteinDistance("", "abc"); got != 3 {
		t.Errorf("LevenshteinDistance(\"\", abc) = %d, want 3", got)
	}
	if got := LevenshteinDistance("abc", ""); got != 3 {
		t.Errorf("LevenshteinDistance(abc, \"\") = %d, want 3", got)
	}
}

// TestLazySyncFromMain pins the worktree read path: a file that exists in the
// main tree but not yet in the worktree is transparently copied across, and an
// existing worktree file is never overwritten.
func TestLazySyncFromMain(t *testing.T) {
	mainDir := t.TempDir()
	wm := internal.NewWorktreeManager(mainDir, internal.WorktreeAlways)
	worktreePath := filepath.Join(wm.WorktreeDir(), "wt1")
	if err := os.MkdirAll(worktreePath, 0o755); err != nil {
		t.Fatal(err)
	}

	// present in the main tree, absent in the worktree
	writeInto(t, mainDir, filepath.Join("docs", "guide.md"), "main tree content\n")
	resolved := filepath.Join(worktreePath, "docs", "guide.md")

	if err := LazySyncFromMain(wm, worktreePath, resolved); err != nil {
		t.Fatalf("LazySyncFromMain: %v", err)
	}
	data, err := os.ReadFile(resolved)
	if err != nil {
		t.Fatalf("file was not synced into the worktree: %v", err)
	}
	if string(data) != "main tree content\n" {
		t.Errorf("synced content = %q", data)
	}

	// existing worktree content must win
	writeInto(t, worktreePath, filepath.Join("docs", "guide.md"), "worktree content\n")
	if err := LazySyncFromMain(wm, worktreePath, resolved); err != nil {
		t.Fatalf("LazySyncFromMain on existing file: %v", err)
	}
	if data, _ := os.ReadFile(resolved); string(data) != "worktree content\n" {
		t.Errorf("existing worktree file was overwritten: %q", data)
	}

	// a file absent from the main tree is left alone (no error, no file)
	missing := filepath.Join(worktreePath, "docs", "absent.md")
	if err := LazySyncFromMain(wm, worktreePath, missing); err != nil {
		t.Fatalf("LazySyncFromMain for a main-tree miss: %v", err)
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Error("a file absent from the main tree must not be created")
	}

	// no worktree context is a no-op
	if err := LazySyncFromMain(nil, worktreePath, resolved); err != nil {
		t.Errorf("nil manager must be a no-op, got %v", err)
	}
	if err := LazySyncFromMain(wm, "", resolved); err != nil {
		t.Errorf("empty worktree path must be a no-op, got %v", err)
	}
}

// --- search priority ---

// TestSearchPriorityConfigs pins the embedded config and the built-in fallback.
func TestSearchPriorityConfigs(t *testing.T) {
	fallback := DefaultSearchPriority()
	if fallback.Extensions[".go"] != 10 || fallback.Extensions[".md"] != 100 {
		t.Errorf("fallback extensions = %v", fallback.Extensions)
	}
	if fallback.Priorities["source"] != 10 || fallback.Priorities["other"] != 150 {
		t.Errorf("fallback priorities = %v", fallback.Priorities)
	}

	embedded := LoadEmbeddedPriority()
	if embedded.Extensions[".go"] != 10 {
		t.Errorf("embedded config lost the .go priority: %v", embedded.Extensions)
	}
	if len(embedded.Extensions) <= len(fallback.Extensions) {
		t.Errorf("embedded config should be richer than the fallback: %d vs %d",
			len(embedded.Extensions), len(fallback.Extensions))
	}

	if got := ExtPriority("main.go"); got != 10 {
		t.Errorf("ExtPriority(main.go) = %d, want 10", got)
	}
	if got := ExtPriority("MAIN.GO"); got != 10 {
		t.Errorf("ExtPriority must be case-insensitive, got %d", got)
	}
	if got := ExtPriority("archive.unknownext"); got != 150 {
		t.Errorf("ExtPriority(unknown) = %d, want the 150 default", got)
	}
}

// TestMergeUserPriorityOverride pins the user override merge: extensions are
// overridden, missing/invalid override files are ignored, and the config is
// never left half-applied.
func TestMergeUserPriorityOverride(t *testing.T) {
	home := t.TempDir()
	t.Setenv("GOA_HOME", home)

	base := DefaultSearchPriority()
	MergeUserPriorityOverride(&base)
	if base.Extensions[".go"] != 10 {
		t.Errorf("merge without an override file changed the config: %v", base.Extensions)
	}

	if err := os.MkdirAll(filepath.Join(home, ".goa"), 0o755); err != nil {
		t.Fatal(err)
	}
	overridePath := filepath.Join(home, ".goa", "search_priority.json")

	// invalid JSON is ignored
	if err := os.WriteFile(overridePath, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	invalid := DefaultSearchPriority()
	MergeUserPriorityOverride(&invalid)
	if invalid.Extensions[".go"] != 10 {
		t.Errorf("invalid override mutated the config: %v", invalid.Extensions)
	}

	// a valid override replaces the listed extensions
	override, _ := json.Marshal(SearchPriorityConfig{Extensions: map[string]int{".go": 99, ".new": 7}})
	if err := os.WriteFile(overridePath, override, 0o644); err != nil {
		t.Fatal(err)
	}
	merged := DefaultSearchPriority()
	MergeUserPriorityOverride(&merged)
	if merged.Extensions[".go"] != 99 {
		t.Errorf("override not applied: .go = %d, want 99", merged.Extensions[".go"])
	}
	if merged.Extensions[".new"] != 7 {
		t.Errorf("new override key missing: %v", merged.Extensions)
	}
	if merged.Extensions[".md"] != 100 {
		t.Errorf("untouched keys must keep the embedded value: .md = %d", merged.Extensions[".md"])
	}
}

// --- dispatch log ---

// TestDispatchLog_Path pins the accessor and its nil-receiver behaviour.
func TestDispatchLog_Path(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dispatch.jsonl")
	log, err := NewDispatchLog(path)
	if err != nil {
		t.Fatalf("NewDispatchLog: %v", err)
	}
	defer log.Close()
	if got := log.Path(); got != path {
		t.Errorf("Path() = %q, want %q", got, path)
	}
	var nilLog *DispatchLog
	if got := nilLog.Path(); got != "" {
		t.Errorf("nil Path() = %q, want empty", got)
	}
	if _, err := NewDispatchLog(filepath.Join(path, "impossible", "x.jsonl")); err == nil {
		t.Error("opening a log under a file path must fail")
	}
}

// --- backups ---

// TestBackupStager_NoSnapshotAndPathErrors pins the recovery paths that do not
// involve a snapshot.
func TestBackupStager_NoSnapshotAndPathErrors(t *testing.T) {
	project := t.TempDir()
	bs := NewBackupStager(project)
	target := filepath.Join(project, "src", "file.go")
	writeInto(t, project, filepath.Join("src", "file.go"), "edited\n")

	if bs.HasBackup(target, project) {
		t.Error("HasBackup = true without a snapshot")
	}
	restored, err := bs.RestoreBackup(target, project)
	if err != nil {
		t.Fatalf("RestoreBackup without a snapshot: %v", err)
	}
	if restored {
		t.Error("RestoreBackup must report false when there is no snapshot")
	}
	if data, _ := os.ReadFile(target); string(data) != "edited\n" {
		t.Errorf("file changed by a no-op restore: %q", data)
	}

	if _, err := bs.BackupPath("/absolute/file.go", ""); err == nil {
		t.Error("BackupPath with an unusable project dir must fail")
	}
}
