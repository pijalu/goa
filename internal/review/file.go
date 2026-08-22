// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package review

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	// maxReviewFileBytes caps how much of a file is loaded for review
	// (2 MiB). Larger files load truncated.
	maxReviewFileBytes = 2 << 20 // 2 MiB
	// maxReviewFileLines caps how many lines are kept (20k). Files with more
	// lines load truncated.
	maxReviewFileLines = 20000

	// binarySniffLen is how many leading bytes are scanned for NUL when
	// classifying a file as binary — same heuristic as tools/readfile.go.
	binarySniffLen = 8192
)

// FileReviewContent is the validated, size-capped content of a single file
// under review. Line anchors are 1-based over Lines (index + 1).
type FileReviewContent struct {
	Path       string   // anchor path (project-relative when inside project, else absolute)
	AbsPath    string   // resolved absolute path
	Ext        string   // lower-cased, dot-less extension ("", "go", "md", ...)
	IsMarkdown bool     // Ext in {md, markdown}
	Lines      []string // source lines (1-based anchors = index+1)
	Truncated  bool     // hit either the byte or the line cap
	Bytes      int      // number of bytes actually loaded (capped)
}

// LoadReviewFile validates and loads a text file for review. Missing files,
// directories, and binary content yield descriptive errors; over-cap files
// are NOT errors — they load truncated (first 2 MiB / first 20k lines,
// Truncated set). Binary detection scans for a NUL byte within the first
// 8 KiB, the same heuristic as tools/readfile.go. The result never panics on
// hostile input; control bytes are sanitized later at render time.
//
// path may be absolute or project-relative; Path reports the canonical
// anchor path (project-relative when inside projectDir, else absolute).
func LoadReviewFile(projectDir, path string) (*FileReviewContent, error) {
	if path == "" {
		return nil, fmt.Errorf("file path is required")
	}
	if projectDir == "" {
		return nil, fmt.Errorf("project dir is required")
	}

	display := anchorPath(projectDir, path)
	absPath := reviewAbsPath(projectDir, path)

	f, size, err := openReviewTarget(absPath, display)
	if err != nil {
		return nil, err
	}
	defer f.Close() //nolint:errcheck // read-only handle

	data, byteTruncated, err := readCapped(f, size)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", display, err)
	}

	// Binary sniff: any NUL in the first 8 KiB means "not text" — reviewing
	// raw bytes would produce garbage anchors and unreadable comments.
	if looksBinary(data) {
		return nil, fmt.Errorf("binary file cannot be reviewed: %s", display)
	}

	lines, lineTruncated := splitReviewLines(data)

	ext := filepath.Ext(absPath)
	return &FileReviewContent{
		Path:       display,
		AbsPath:    absPath,
		Ext:        strings.TrimPrefix(strings.ToLower(ext), "."),
		IsMarkdown: isMarkdownExt(ext),
		Lines:      lines,
		Truncated:  byteTruncated || lineTruncated,
		Bytes:      len(data),
	}, nil
}

// reviewAbsPath resolves a review path against the project dir: absolute
// paths pass through cleaned, relative ones join projectDir.
func reviewAbsPath(projectDir, path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Join(projectDir, path)
}

// openReviewTarget opens the file and returns it with its size, rejecting
// missing files and directories with descriptive errors (display is the
// user-facing anchor path used in messages).
func openReviewTarget(absPath, display string) (*os.File, int64, error) {
	f, err := os.Open(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, 0, fmt.Errorf("file not found: %s", display)
		}
		return nil, 0, fmt.Errorf("open %s: %w", display, err)
	}
	info, err := f.Stat()
	if err != nil {
		f.Close() //nolint:errcheck // already failing
		return nil, 0, fmt.Errorf("stat %s: %w", display, err)
	}
	if info.IsDir() {
		f.Close() //nolint:errcheck // already failing
		return nil, 0, fmt.Errorf("cannot review a directory: %s", display)
	}
	return f, info.Size(), nil
}

// readCapped reads at most maxReviewFileBytes bytes, reporting whether the
// byte cap cut content off (size comes from Stat and is authoritative even
// when the reader yields fewer bytes).
func readCapped(f *os.File, size int64) ([]byte, bool, error) {
	data, err := io.ReadAll(io.LimitReader(f, int64(maxReviewFileBytes)+1))
	if err != nil {
		return nil, false, err
	}
	truncated := size > int64(maxReviewFileBytes)
	if len(data) > maxReviewFileBytes {
		data = data[:maxReviewFileBytes]
	}
	return data, truncated, nil
}

// looksBinary reports whether data starts looking like binary content: any
// NUL byte within the first binarySniffLen bytes.
func looksBinary(data []byte) bool {
	sniff := data
	if len(sniff) > binarySniffLen {
		sniff = sniff[:binarySniffLen]
	}
	return bytes.IndexByte(sniff, 0) >= 0
}

// splitReviewLines splits raw file data into source lines. A trailing newline
// does not produce an empty phantom line ("" file -> zero lines); CRLF line
// endings have their "\r" stripped so anchors index clean text. Returns
// whether the line cap was hit.
func splitReviewLines(data []byte) ([]string, bool) {
	all := strings.Split(string(data), "\n")
	// Drop the empty element produced by a trailing newline.
	if n := len(all); n > 0 && all[n-1] == "" {
		all = all[:n-1]
	}
	truncated := false
	if len(all) > maxReviewFileLines {
		all = all[:maxReviewFileLines]
		truncated = true
	}
	for i, l := range all {
		all[i] = strings.TrimSuffix(l, "\r")
	}
	return all, truncated
}

// isMarkdownExt reports whether a raw filepath.Ext result identifies a
// Markdown file (matched case-insensitively).
func isMarkdownExt(ext string) bool {
	switch strings.ToLower(strings.TrimPrefix(ext, ".")) {
	case "md", "markdown":
		return true
	default:
		return false
	}
}
