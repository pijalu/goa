// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

// Package tools provides the tool system for Goa agents.
// Package truncate provides output truncation for tool results.
// Inspired by Pi's truncate.ts — shared between bash, read, and TUI components.
package common

import (
	"fmt"
	"os"
	"strings"
)

const (
	// DefaultMaxLines is the default maximum number of lines in truncated output.
	DefaultMaxLines = 2000
	// DefaultMaxBytes is the default maximum bytes in truncated output (50KB).
	DefaultMaxBytes = 50 * 1024
)

// TruncationResult holds the result of truncating content.
type TruncationResult struct {
	Content          string
	Truncated        bool
	TruncatedBy      string // "lines", "bytes", or ""
	TotalLines       int
	TotalBytes       int
	OutputLines      int
	OutputBytes      int
	LastLinePartial  bool
	FirstLineExceeds bool
	MaxLines         int
	MaxBytes         int
}

// TruncateTail keeps the last N lines/bytes of content.
// Suitable for bash output where you want to see the end (errors, final results).
func TruncateTail(content string, maxLines, maxBytes int) TruncationResult {
	totalBytes := len(content)
	lines := splitLinesForCounting(content)
	totalLines := len(lines)

	// Check if no truncation needed
	if totalLines <= maxLines && totalBytes <= maxBytes {
		return TruncationResult{
			Content:     content,
			OutputLines: totalLines,
			OutputBytes: totalBytes,
			TotalLines:  totalLines,
			TotalBytes:  totalBytes,
			MaxLines:    maxLines,
			MaxBytes:    maxBytes,
		}
	}

	// Work backwards from the end
	var outputLines []string
	outputBytesCount := 0
	truncatedBy := "lines"
	lastLinePartial := false

	for i := len(lines) - 1; i >= 0 && len(outputLines) < maxLines; i-- {
		lineBytes := len(lines[i])
		if len(outputLines) > 0 {
			lineBytes++ // +1 for newline
		}

		if outputBytesCount+lineBytes > maxBytes {
			truncatedBy = "bytes"
			// If we haven't added ANY lines yet and this line exceeds maxBytes,
			// take the end of the line (partial)
			if len(outputLines) == 0 {
				truncatedLine := truncateStringFromEnd(lines[i], maxBytes)
				outputLines = append([]string{truncatedLine}, outputLines...)
				lastLinePartial = true
			}
			break
		}

		outputLines = append([]string{lines[i]}, outputLines...)
		outputBytesCount += lineBytes
	}

	outputContent := strings.Join(outputLines, "\n")
	return TruncationResult{
		Content:         outputContent,
		Truncated:       true,
		TruncatedBy:     truncatedBy,
		TotalLines:      totalLines,
		TotalBytes:      totalBytes,
		OutputLines:     len(outputLines),
		OutputBytes:     len(outputContent),
		LastLinePartial: lastLinePartial,
		MaxLines:        maxLines,
		MaxBytes:        maxBytes,
	}
}

// TruncateHead keeps the first N lines/bytes of content.
// Suitable for file reads where you want to see the beginning.
func TruncateHead(content string, maxLines, maxBytes int) TruncationResult {
	totalBytes := len(content)
	lines := splitLinesForCounting(content)
	totalLines := len(lines)

	if totalLines <= maxLines && totalBytes <= maxBytes {
		return TruncationResult{
			Content:     content,
			OutputLines: totalLines,
			OutputBytes: totalBytes,
			TotalLines:  totalLines,
			TotalBytes:  totalBytes,
			MaxLines:    maxLines,
			MaxBytes:    maxBytes,
		}
	}

	// Check if first line alone exceeds byte limit
	if len(lines) > 0 && len(lines[0]) > maxBytes {
		return TruncationResult{
			Truncated:        true,
			TruncatedBy:      "bytes",
			TotalLines:       totalLines,
			TotalBytes:       totalBytes,
			OutputLines:      0,
			OutputBytes:      0,
			FirstLineExceeds: true,
			MaxLines:         maxLines,
			MaxBytes:         maxBytes,
		}
	}

	var outputLines []string
	outputBytesCount := 0
	truncatedBy := "lines"

	for _, line := range lines {
		if len(outputLines) >= maxLines {
			truncatedBy = "lines"
			break
		}
		lineBytes := len(line)
		if len(outputLines) > 0 {
			lineBytes++ // +1 for newline
		}
		if outputBytesCount+lineBytes > maxBytes {
			truncatedBy = "bytes"
			break
		}
		outputLines = append(outputLines, line)
		outputBytesCount += lineBytes
	}

	outputContent := strings.Join(outputLines, "\n")
	return TruncationResult{
		Content:     outputContent,
		Truncated:   true,
		TruncatedBy: truncatedBy,
		TotalLines:  totalLines,
		TotalBytes:  totalBytes,
		OutputLines: len(outputLines),
		OutputBytes: len(outputContent),
		MaxLines:    maxLines,
		MaxBytes:    maxBytes,
	}
}

// TruncResString returns a human-readable description of a truncation result.
func TruncResString(r TruncationResult) string {
	if !r.Truncated {
		return ""
	}
	return fmt.Sprintf("%d/%d lines, %d/%d bytes (by %s)",
		r.OutputLines, r.TotalLines, r.OutputBytes, r.TotalBytes, r.TruncatedBy)
}

// SaveTruncatedOutput saves content to a temp file and returns the path.
// Used when output is truncated and full content should be available.
func SaveTruncatedOutput(content string) (string, error) {
	f, err := os.CreateTemp("", "goa-output-*.txt")
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := f.WriteString(content); err != nil {
		return "", err
	}
	return f.Name(), nil
}

// splitLinesForCounting splits content into lines, handling trailing newlines.
func splitLinesForCounting(content string) []string {
	if content == "" {
		return nil
	}
	lines := strings.Split(content, "\n")
	if content[len(content)-1] == '\n' {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// truncateStringFromEnd truncates a string to fit within maxBytes, taking from the end.
// The cut lands on a rune boundary so the result is always valid UTF-8.
func truncateStringFromEnd(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	start := len(s) - maxBytes
	// Advance past UTF-8 continuation bytes (0b10xxxxxx) so the first byte is
	// a rune start; this can only shrink the result below maxBytes.
	for start < len(s) && s[start]&0xC0 == 0x80 {
		start++
	}
	return s[start:]
}
