// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package common

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/pijalu/goa/internal"
)

// FileToolConfig controls filename fuzzy-matching behavior for file tools.
// The zero value (nil FuzzyMatch pointer) defaults fuzzy matching to enabled.
type FileToolConfig struct {
	// FuzzyMatch enables fuzzy file-name matching when the requested path
	// does not exist. When nil (the default) fuzzy matching is enabled.
	FuzzyMatch *bool `yaml:"fuzzy_match"`
}

// ReadFileConfig is the historical name for FileToolConfig.
type ReadFileConfig = FileToolConfig

// NormalizeFileToolPath strips a leading "@" used as a current-directory
// marker. Read, edit, and write tools all accept @-prefixed paths.
func NormalizeFileToolPath(path string) string {
	return strings.TrimPrefix(path, "@")
}

// ResolveFileToolPath normalizes the path (strips a leading "@") and resolves
// it through the worktree manager. It returns both the resolved absolute path
// and the normalized original path so callers can report fuzzy matches with
// the user-supplied name.
func ResolveFileToolPath(wm *internal.WorktreeManager, path string) (resolvedPath, originalPath string, err error) {
	originalPath = NormalizeFileToolPath(path)
	resolvedPath, err = ResolveToolPath(wm, originalPath)
	return resolvedPath, originalPath, err
}

// FileToolFuzzyMatchEnabled reports whether fuzzy matching should be used.
// The zero value (nil) means enabled, so the feature defaults to on.
func FileToolFuzzyMatchEnabled(cfg FileToolConfig) bool {
	if cfg.FuzzyMatch == nil {
		return true
	}
	return *cfg.FuzzyMatch
}

// ReadFileWithFuzzyFallback reads resolvedPath; if it does not exist and fuzzy
// matching is enabled, it searches the containing directory for the closest
// matching file and reads that file instead. It returns the actual path read
// and the file contents.
func ReadFileWithFuzzyFallback(cfg FileToolConfig, resolvedPath, originalPath string) (string, []byte, error) {
	data, err := os.ReadFile(resolvedPath)
	if err == nil {
		return resolvedPath, data, nil
	}
	if !os.IsNotExist(err) || !FileToolFuzzyMatchEnabled(cfg) {
		return "", nil, err
	}

	projectDir := filepath.Dir(resolvedPath)
	if projectDir == "" {
		projectDir = "."
	}
	suggest, confidence := FuzzyFindFile(projectDir, originalPath)
	if suggest == "" || confidence < 0.6 {
		return "", nil, err
	}

	data, err = os.ReadFile(suggest)
	if err != nil {
		return "", nil, err
	}
	return suggest, data, nil
}
