// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package export

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// ZipBuilder constructs a diagnostic bundle ZIP archive.
type ZipBuilder struct {
	zw     *zip.Writer
	file   *os.File
	paths  []string
	size   int64
	closed bool
}

// NewZipBuilder creates a builder writing to outputPath.
func NewZipBuilder(outputPath string) (*ZipBuilder, error) {
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return nil, fmt.Errorf("create export directory: %w", err)
	}
	f, err := os.Create(outputPath)
	if err != nil {
		return nil, fmt.Errorf("create zip file: %w", err)
	}
	return &ZipBuilder{
		zw:   zip.NewWriter(f),
		file: f,
	}, nil
}

// AddBytes adds an in-memory entry to the archive.
func (z *ZipBuilder) AddBytes(targetPath string, data []byte) error {
	if z.closed {
		return fmt.Errorf("zip builder is closed")
	}
	w, err := z.zw.Create(normalizeZipPath(targetPath))
	if err != nil {
		return fmt.Errorf("create zip entry %q: %w", targetPath, err)
	}
	n, err := w.Write(data)
	if err != nil {
		return fmt.Errorf("write zip entry %q: %w", targetPath, err)
	}
	z.paths = append(z.paths, targetPath)
	z.size += int64(n)
	return nil
}

// AddFile copies a file from disk into the archive at targetPath.
// If redact is non-nil, the file content is passed through redact before writing.
func (z *ZipBuilder) AddFile(sourcePath, targetPath string, redact func(string) string) error {
	if z.closed {
		return fmt.Errorf("zip builder is closed")
	}
	data, err := os.ReadFile(sourcePath)
	if err != nil {
		return fmt.Errorf("read %q: %w", sourcePath, err)
	}
	content := string(data)
	if redact != nil {
		content = redact(content)
	}
	return z.AddBytes(targetPath, []byte(content))
}

// AddDir recursively adds a directory tree into the archive under baseTarget.
// Empty directories are skipped. Files are added relative to baseTarget.
func (z *ZipBuilder) AddDir(sourceDir, baseTarget string) error {
	return filepath.WalkDir(sourceDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(sourceDir, path)
		if err != nil {
			return err
		}
		target := filepath.Join(baseTarget, rel)
		return z.AddFile(path, target, nil)
	})
}

// AddStream copies an io.Reader into the archive at targetPath.
func (z *ZipBuilder) AddStream(targetPath string, r io.Reader) error {
	if z.closed {
		return fmt.Errorf("zip builder is closed")
	}
	w, err := z.zw.Create(normalizeZipPath(targetPath))
	if err != nil {
		return fmt.Errorf("create zip entry %q: %w", targetPath, err)
	}
	n, err := io.Copy(w, r)
	if err != nil {
		return fmt.Errorf("write zip entry %q: %w", targetPath, err)
	}
	z.paths = append(z.paths, targetPath)
	z.size += n
	return nil
}

// Paths returns the relative paths added so far.
func (z *ZipBuilder) Paths() []string {
	return append([]string(nil), z.paths...)
}

// Size returns the number of bytes written to the archive so far.
func (z *ZipBuilder) Size() int64 {
	return z.size
}

// Close finalizes the zip and returns the output path.
func (z *ZipBuilder) Close() (string, error) {
	if z.closed {
		return z.file.Name(), nil
	}
	z.closed = true
	if err := z.zw.Close(); err != nil {
		_ = z.file.Close()
		return z.file.Name(), fmt.Errorf("close zip writer: %w", err)
	}
	if err := z.file.Close(); err != nil {
		return z.file.Name(), fmt.Errorf("close zip file: %w", err)
	}
	return z.file.Name(), nil
}

func normalizeZipPath(p string) string {
	return strings.TrimPrefix(filepath.ToSlash(p), "/")
}
