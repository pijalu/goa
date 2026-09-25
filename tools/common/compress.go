// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package common

import (
	"bufio"
	"fmt"
	"regexp"
	"strings"
)

// OutputCompressors controls built-in output compression for bash tool output.
// The master switch is disabled by default. Per-model compress_output in
// model config and provider-based auto-detection (enabled for local providers
// like LM Studio / Ollama) determine the effective setting at call time.
var OutputCompressors = struct {
	Enabled bool
}{
	Enabled: true,
}

// CompressOutput applies built-in compression to the given command's output.
// Returns the compressed output and whether compression was applied.
func CompressOutput(command, output string) (string, bool) {
	if output == "" {
		return output, false
	}
	cmd := strings.TrimSpace(command)

	// Use routing pattern: find compressor by command prefix
	for _, route := range compressorRoutes {
		if route.Match(cmd) {
			return route.Compress(output)
		}
	}
	return output, false
}

// compressorRoute maps a command matcher to its compress function.
type compressorRoute struct {
	Match    func(cmd string) bool
	Compress func(output string) (string, bool)
}

var compressorRoutes = []compressorRoute{
	{isGitDiff, compressGitDiff},
	{isGitStatus, compressGitStatus},
	{isGitLog, compressGitLog},
	{isLs, compressLs},
	{isGrep, compressGrep},
	{isRead, compressRead},
	{isTestOutput, compressTestOutput},
}

func isGitDiff(cmd string) bool {
	return strings.HasPrefix(cmd, "git diff") || strings.HasPrefix(cmd, "git show")
}
func isGitStatus(cmd string) bool { return strings.HasPrefix(cmd, "git status") }
func isGitLog(cmd string) bool    { return strings.HasPrefix(cmd, "git log") }
func isLs(cmd string) bool        { return strings.HasPrefix(cmd, "ls") }
func isGrep(cmd string) bool      { return strings.HasPrefix(cmd, "grep") || strings.HasPrefix(cmd, "rg") }
func isRead(cmd string) bool {
	return strings.HasPrefix(cmd, "cat ") || strings.HasPrefix(cmd, "head ") || strings.HasPrefix(cmd, "tail ")
}
func isTestOutput(cmd string) bool {
	return strings.Contains(cmd, "test") || strings.Contains(cmd, "TEST")
}

// compressGitDiff condenses git diff: only changed lines, grouped by file.
func compressGitDiff(output string) (string, bool) {
	result, fileCount := scanGitDiff(output)
	if fileCount == 0 {
		return output, false
	}
	// Prepend the header. The previous implementation used copy(result, summary),
	// which OVERWRITES the first entries of result (the first file's path header
	// and first hunk line) instead of prepending. append(header, result...) keeps
	// every scanned line, matching every other compressor in this file.
	header := formatCompressHeader("git diff")
	return strings.Join(append(header, result...), "\n"), true
}

func scanGitDiff(output string) ([]string, int) {
	scanner := bufio.NewScanner(strings.NewReader(output))
	var result []string
	var fileCount int
	fileChanged := false

	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "diff --git"):
			if fileChanged {
				fileCount++
			}
			fileChanged = false
			if parts := strings.Split(line, " b/"); len(parts) >= 2 {
				result = append(result, "--- "+parts[1])
			}
		case strings.HasPrefix(line, "--- "), strings.HasPrefix(line, "+++ "):
			continue
		case strings.HasPrefix(line, "@@"):
			fileChanged = true
			result = append(result, "  "+line)
		case strings.HasPrefix(line, "+"), strings.HasPrefix(line, "-"):
			result = append(result, "  "+line)
		}
	}
	if fileChanged {
		fileCount++
	}
	return result, fileCount
}

// compressGitStatus produces compact one-line-per-file status.
func compressGitStatus(output string) (string, bool) {
	scanner := bufio.NewScanner(strings.NewReader(output))
	var result []string
	changed := 0
	untracked := 0

	for scanner.Scan() {
		line := scanner.Text()
		if len(line) < 3 {
			continue
		}
		status := strings.TrimSpace(line[:2])
		file := strings.TrimSpace(line[2:])
		if file == "" {
			continue
		}
		if status == "??" {
			untracked++
			result = append(result, "? "+file)
		} else {
			changed++
			result = append(result, status+" "+file)
		}
	}

	if len(result) == 0 {
		return output, false
	}

	var header []string
	header = append(header, formatCompressHeader("git status")...)
	if changed > 0 {
		header = append(header, "Changed: "+pluralize(changed, "file"))
	}
	if untracked > 0 {
		header = append(header, "Untracked: "+pluralize(untracked, "file"))
	}
	// The per-file lines ARE the payload: appending (never overwriting) keeps
	// every scanned entry after the summary header.
	return strings.Join(append(header, result...), "\n"), true
}

// compressGitLog deduplicates and compacts git log output.
func compressGitLog(output string) (string, bool) {
	scanner := bufio.NewScanner(strings.NewReader(output))
	var result []string
	commitCount := 0
	seen := make(map[string]bool)

	for scanner.Scan() {
		line := scanner.Text()
		// Extract commit hash + message (first line)
		if strings.HasPrefix(line, "commit ") {
			continue
		}
		if strings.HasPrefix(line, "Author:") || strings.HasPrefix(line, "Date:") {
			continue
		}
		line = strings.TrimSpace(line)
		if line == "" || seen[line] {
			continue
		}
		seen[line] = true
		// Strip email: "Author <email>" → "Author"
		if idx := strings.Index(line, "<"); idx > 0 {
			line = strings.TrimSpace(line[:idx])
		}
		commitCount++
		result = append(result, line)
	}

	if commitCount == 0 {
		return output, false
	}

	header := formatCompressHeader("git log")
	return strings.Join(append(header, result...), "\n"), true
}

// compressLs strips permissions/owner/group, compact format.
func compressLs(output string) (string, bool) {
	scanner := bufio.NewScanner(strings.NewReader(output))
	var result []string
	total := 0
	hidden := 0

	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "total ") {
			continue
		}
		// Parse "permissions links owner group size date name"
		parts := strings.Fields(line)
		if len(parts) >= 9 {
			name := strings.Join(parts[8:], " ")
			if strings.HasPrefix(name, ".") {
				hidden++
			}
			total++
			result = append(result, name)
		} else if len(parts) > 0 {
			result = append(result, line)
		}
	}

	if total == 0 {
		return output, false
	}

	header := formatCompressHeader("ls")
	if hidden > 0 {
		header = append(header, pluralize(hidden, "hidden file"))
	}
	return strings.Join(append(header, result...), "\n"), true
}

// compressGrep groups by file, truncates long lines.
func compressGrep(output string) (string, bool) {
	scanner := bufio.NewScanner(strings.NewReader(output))
	var result []string
	fileMatches := make(map[string]int)
	currentFile := ""

	for scanner.Scan() {
		line := scanner.Text()
		// grep format: file:line:content
		parts := strings.SplitN(line, ":", 3)
		if len(parts) >= 3 {
			file := parts[0]
			content := parts[2]
			if file != currentFile {
				currentFile = file
				result = append(result, file+":")
			}
			// Truncate long lines
			if len(content) > 200 {
				content = content[:197] + "..."
			}
			result = append(result, "  "+content)
			fileMatches[file]++
		} else if line != "" {
			result = append(result, line)
		}
	}

	fileCount := len(fileMatches)
	if fileCount == 0 {
		return output, false
	}

	totalMatches := 0
	for _, c := range fileMatches {
		totalMatches += c
	}

	header := formatCompressHeader("grep")
	header = append(header, pluralize(fileCount, "file")+" with "+pluralize(totalMatches, "match"))
	return strings.Join(append(header, result...), "\n"), true
}

// compressRead prepends line numbers.
func compressRead(output string) (string, bool) {
	scanner := bufio.NewScanner(strings.NewReader(output))
	var result []string
	lineNum := 1
	blankCount := 0

	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			blankCount++
			if blankCount > 2 {
				continue
			}
		} else {
			blankCount = 0
		}
		result = append(result, fmtLineNum(lineNum, line))
		lineNum++
	}

	if lineNum <= 1 {
		return output, false
	}

	header := formatCompressHeader("read")
	return strings.Join(append(header, result...), "\n"), true
}

// stackCompressedMarker replaces the truncated tail of a stack trace.
const stackCompressedMarker = "  ... (stack trace compressed)"

// compressTestOutput strips passing result lines, compresses stack traces, and
// summarises the outcome. Counts come from the test-result lines
// ("--- PASS:" / "--- FAIL:") only, so a failing test is counted once even
// though the package summary repeats "FAIL".
func compressTestOutput(output string) (string, bool) {
	scanner := bufio.NewScanner(strings.NewReader(output))
	var c testOutputCompressor
	for scanner.Scan() {
		c.add(scanner.Text())
	}
	if c.empty() {
		return output, false
	}
	return strings.Join(append(c.header(), c.lines...), "\n"), true
}

// testOutputCompressor accumulates the compacted form of `go test` output.
type testOutputCompressor struct {
	lines      []string
	passed     int
	failed     int
	sawFailure bool
	inStack    bool
}

// add classifies one output line and keeps whatever should survive.
func (c *testOutputCompressor) add(line string) {
	switch {
	case isTestResultLine(line):
		c.addResult(line)
	case isStackLine(line):
		c.addStackLine(line)
	default:
		c.addPlainLine(line)
	}
}

// addResult counts a per-test result. Passing results are the bulk of test
// output and carry no information once counted, so only failures are kept.
func (c *testOutputCompressor) addResult(line string) {
	if !strings.Contains(line, "FAIL") {
		c.passed++
		return
	}
	c.failed++
	c.inStack = false
	c.lines = append(c.lines, line)
}

// addStackLine keeps the first line of a stack trace and elides the rest.
func (c *testOutputCompressor) addStackLine(line string) {
	if c.inStack {
		return
	}
	c.inStack = true
	c.lines = append(c.lines, line, stackCompressedMarker)
}

// addPlainLine keeps an ordinary line, dropping passing package summaries and
// recording any failure so a build failure still compresses.
func (c *testOutputCompressor) addPlainLine(line string) {
	c.inStack = false
	if isPassSummary(line) {
		return
	}
	if strings.Contains(line, "FAIL") {
		c.sawFailure = true
	}
	c.lines = append(c.lines, line)
}

// empty reports whether the output carried no recognisable test outcome.
func (c *testOutputCompressor) empty() bool {
	return c.passed == 0 && c.failed == 0 && !c.sawFailure
}

// header renders the compression header plus the outcome counts, which are
// omitted when the output held no per-test results (e.g. a build failure).
func (c *testOutputCompressor) header() []string {
	header := formatCompressHeader("test")
	if c.passed > 0 || c.failed > 0 {
		header = append(header, fmt.Sprintf("%d passed, %d failed", c.passed, c.failed))
	}
	return header
}

// isStackLine reports whether line is indented, i.e. part of a stack trace.
func isStackLine(line string) bool {
	return strings.HasPrefix(line, "\t") || strings.HasPrefix(line, "    ")
}

// isTestResultLine reports whether line is a per-test result line, which is the
// only line form that carries test counts ("--- PASS: TestX", "--- FAIL:").
func isTestResultLine(line string) bool {
	return strings.HasPrefix(line, "--- ") && (strings.Contains(line, "PASS") || strings.Contains(line, "FAIL"))
}

// isPassSummary reports whether line is a passing package summary ("PASS",
// "ok  pkg  0.5s") that adds no information to the compressed report.
func isPassSummary(line string) bool {
	trimmed := strings.TrimSpace(line)
	return trimmed == "PASS" || strings.HasPrefix(trimmed, "ok ")
}

// ── Helpers ──

var lineNumRe = regexp.MustCompile(`^(\d+)`)

func fmtLineNum(n int, line string) string {
	// If line already starts with a number, preserve alignment
	if lineNumRe.MatchString(line) {
		return line
	}
	return fmt.Sprintf("%5d  %s", n, line)
}

func formatCompressHeader(cmd string) []string {
	return []string{
		"",
		"[compress: " + cmd + "]",
	}
}

// pluralize renders a count with an English plural noun. Sibilant endings take
// "es" ("match" → "matches"), everything else takes "s".
func pluralize(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	if strings.HasSuffix(word, "s") || strings.HasSuffix(word, "x") ||
		strings.HasSuffix(word, "z") || strings.HasSuffix(word, "ch") ||
		strings.HasSuffix(word, "sh") {
		return fmt.Sprintf("%d %ses", n, word)
	}
	return fmt.Sprintf("%d %ss", n, word)
}
