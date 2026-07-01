// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"strings"
	"testing"
)

func TestTruncateTail_ShortContent_ReturnsAsIs(t *testing.T) {
	input := "line1\nline2\nline3"
	result := TruncateTail(input, 10, 50000)
	if result.Truncated {
		t.Error("Short content should not be truncated")
	}
	if result.Content != input {
		t.Errorf("Content should match input, got: %q", result.Content)
	}
	if result.TotalLines != 3 {
		t.Errorf("Expected 3 lines, got %d", result.TotalLines)
	}
}

func TestTruncateTail_LongContent_TruncatesLines(t *testing.T) {
	var lines []string
	for i := 0; i < 100; i++ {
		lines = append(lines, "line")
	}
	input := strings.Join(lines, "\n")
	result := TruncateTail(input, 10, 50000)
	if !result.Truncated {
		t.Error("Long content should be truncated")
	}
	if result.OutputLines > 10 {
		t.Errorf("Output should be at most 10 lines, got %d", result.OutputLines)
	}
	if result.TotalLines != 100 {
		t.Errorf("TotalLines should be 100, got %d", result.TotalLines)
	}
	if result.TruncatedBy != "lines" {
		t.Errorf("Should be truncated by lines, got: %s", result.TruncatedBy)
	}
}

func TestTruncateTail_LongContent_LastLinesPreserved(t *testing.T) {
	var lines []string
	for i := 0; i < 100; i++ {
		lines = append(lines, "line")
	}
	input := strings.Join(lines, "\n")
	result := TruncateTail(input, 10, 50000)
	outputLines := strings.Split(result.Content, "\n")
	if len(outputLines) > 10 {
		t.Fatalf("Expected at most 10 output lines, got %d", len(outputLines))
	}
	// Last line of output should match last line of input
	if outputLines[len(outputLines)-1] != "line" {
		t.Errorf("Last output line should be 'line', got: %q", outputLines[len(outputLines)-1])
	}
}

func TestTruncateTail_ByteLimit_TruncatesByBytes(t *testing.T) {
	input := strings.Repeat("x", 100) + "\n" + strings.Repeat("y", 100)
	result := TruncateTail(input, 100, 50)
	if !result.Truncated {
		t.Error("Content exceeding byte limit should be truncated")
	}
	if result.OutputBytes > 60 {
		t.Errorf("Output should be within byte limit, got %d bytes", result.OutputBytes)
	}
	if result.TruncatedBy != "bytes" {
		t.Errorf("Should be truncated by bytes, got: %s", result.TruncatedBy)
	}
}

func TestTruncateTail_EmptyContent(t *testing.T) {
	result := TruncateTail("", 10, 50000)
	if result.Truncated {
		t.Error("Empty content should not be truncated")
	}
	if result.Content != "" {
		t.Errorf("Content should be empty, got: %q", result.Content)
	}
}

func TestTruncateHead_ShortContent_ReturnsAsIs(t *testing.T) {
	input := "line1\nline2\nline3"
	result := TruncateHead(input, 10, 50000)
	if result.Truncated {
		t.Error("Short content should not be truncated")
	}
	if result.Content != input {
		t.Errorf("Content should match input, got: %q", result.Content)
	}
}

func TestTruncateHead_LongContent_TruncatesLines(t *testing.T) {
	var lines []string
	for i := 0; i < 100; i++ {
		lines = append(lines, "line")
	}
	input := strings.Join(lines, "\n")
	result := TruncateHead(input, 10, 50000)
	if !result.Truncated {
		t.Error("Long content should be truncated")
	}
	if result.OutputLines > 10 {
		t.Errorf("Output should be at most 10 lines, got %d", result.OutputLines)
	}
	if result.TruncatedBy != "lines" {
		t.Errorf("Should be truncated by lines, got: %s", result.TruncatedBy)
	}
}

func TestTruncateHead_FirstLinesPreserved(t *testing.T) {
	var lines []string
	for i := 0; i < 100; i++ {
		lines = append(lines, "line")
	}
	input := strings.Join(lines, "\n")
	result := TruncateHead(input, 10, 50000)
	outputLines := strings.Split(result.Content, "\n")
	if len(outputLines) > 10 {
		t.Fatalf("Expected at most 10 output lines, got %d", len(outputLines))
	}
	if outputLines[0] != "line" {
		t.Errorf("First output line should be 'line', got: %q", outputLines[0])
	}
}

func TestTruncateHead_LargeFirstLine_ReturnsFirstLineExceeds(t *testing.T) {
	input := strings.Repeat("x", 5000)
	result := TruncateHead(input, 10, 100)
	if !result.Truncated {
		t.Error("Single long line exceeding byte limit should be truncated")
	}
	if !result.FirstLineExceeds {
		t.Error("FirstLineExceeds should be true when first line exceeds byte limit")
	}
}

func TestTruncResString_Truncated(t *testing.T) {
	r := TruncationResult{
		Truncated:   true,
		TotalLines:  100,
		TotalBytes:  5000,
		OutputLines: 10,
		OutputBytes: 500,
		TruncatedBy: "lines",
	}
	s := TruncResString(r)
	if s == "" {
		t.Error("TruncResString should not be empty for truncated result")
	}
	if !strings.Contains(s, "10/100") {
		t.Errorf("Expected line ratio in output, got: %s", s)
	}
}

func TestTruncResString_NotTruncated(t *testing.T) {
	r := TruncationResult{Truncated: false}
	s := TruncResString(r)
	if s != "" {
		t.Errorf("TruncResString should be empty for non-truncated, got: %q", s)
	}
}

func TestSaveTruncatedOutput_CreatesFile(t *testing.T) {
	content := "test output content"
	path, err := SaveTruncatedOutput(content)
	if err != nil {
		t.Fatalf("SaveTruncatedOutput should succeed: %v", err)
	}
	if path == "" {
		t.Fatal("Path should not be empty")
	}
}

func TestDefaultConstants_Reasonable(t *testing.T) {
	if DefaultMaxLines <= 0 {
		t.Errorf("DefaultMaxLines should be positive, got %d", DefaultMaxLines)
	}
	if DefaultMaxBytes <= 0 {
		t.Errorf("DefaultMaxBytes should be positive, got %d", DefaultMaxBytes)
	}
	if DefaultMaxLines < 100 {
		t.Errorf("DefaultMaxLines should be at least 100, got %d", DefaultMaxLines)
	}
	if DefaultMaxBytes < 1024 {
		t.Errorf("DefaultMaxBytes should be at least 1KB, got %d", DefaultMaxBytes)
	}
}
