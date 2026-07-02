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
	return strings.Join(header, "\n"), true
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

// compressTestOutput strips PASS lines, shows FAIL + summary.
func compressTestOutput(output string) (string, bool) {
	scanner := bufio.NewScanner(strings.NewReader(output))
	var result []string
	passCount := 0
	failCount := 0
	inStack := false

	for scanner.Scan() {
		line := scanner.Text()
		// Strip PASS lines
		if strings.Contains(line, "PASS") && !strings.Contains(line, "FAIL") {
			passCount++
			continue
		}
		// Compress stack traces (show only first 3 lines)
		if strings.HasPrefix(line, "\t") || strings.HasPrefix(line, "    ") {
			if inStack {
				continue
			}
			inStack = true
			result = append(result, line)
			result = append(result, "  ... (stack trace compressed)")
			continue
		}
		inStack = false

		if strings.Contains(line, "FAIL") {
			failCount++
		}
		result = append(result, line)
	}

	if failCount == 0 && passCount == 0 {
		return output, false
	}

	header := formatCompressHeader("test")
	header = append(header, pluralize(passCount, "passed")+", "+pluralize(failCount, "failed"))
	return strings.Join(append(header, result...), "\n"), true
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

func pluralize(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}
