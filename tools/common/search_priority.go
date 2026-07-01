// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package common

import (
	"embed"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

//go:embed search_priority.json
var searchPriorityFS embed.FS

// SearchPriorityConfig maps file extensions to priority values.
// Lower priority values = higher display rank (source code first).
type SearchPriorityConfig struct {
	Priorities map[string]int `json:"priorities"`
	Extensions map[string]int `json:"extensions"`
}

var (
	searchPriority     SearchPriorityConfig
	searchPriorityOnce sync.Once
)

// LoadSearchPriority loads the embedded search priority config, then checks
// for a user override at ~/.goa/search_priority.json. The embedded default
// is always used as fallback for missing keys.
func LoadSearchPriority() SearchPriorityConfig {
	searchPriorityOnce.Do(func() {
		searchPriority = LoadEmbeddedPriority()
		MergeUserPriorityOverride(&searchPriority)
	})
	return searchPriority
}

// LoadEmbeddedPriority loads the embedded search priority config.
func LoadEmbeddedPriority() SearchPriorityConfig {
	data, err := searchPriorityFS.ReadFile("search_priority.json")
	if err != nil {
		return DefaultSearchPriority()
	}
	var cfg SearchPriorityConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return DefaultSearchPriority()
	}
	return cfg
}

// MergeUserPriorityOverride merges a user override file into cfg.
func MergeUserPriorityOverride(cfg *SearchPriorityConfig) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return
	}
	userPath := filepath.Join(home, ".goa", "search_priority.json")
	userData, err := os.ReadFile(userPath)
	if err != nil {
		return
	}
	var userCfg SearchPriorityConfig
	if err := json.Unmarshal(userData, &userCfg); err != nil {
		return
	}
	for ext, pri := range userCfg.Extensions {
		cfg.Extensions[ext] = pri
	}
}

// ExtPriority returns the sort priority for a file path.
// Lower number = shown first.
func ExtPriority(path string) int {
	cfg := LoadSearchPriority()
	ext := strings.ToLower(filepath.Ext(path))
	if pri, ok := cfg.Extensions[ext]; ok {
		return pri
	}
	// Check for extensionless files like Makefile, Dockerfile
	base := strings.ToLower(filepath.Base(path))
	if pri, ok := cfg.Extensions["."+base]; ok {
		return pri
	}
	return 150 // default "other" priority
}

// DefaultSearchPriority returns the fallback search priority config.
func DefaultSearchPriority() SearchPriorityConfig {
	return SearchPriorityConfig{
		Priorities: map[string]int{
			"source": 10, "config": 50, "data": 100, "media": 200, "other": 150,
		},
		Extensions: map[string]int{
			".go": 10, ".py": 10, ".js": 10, ".ts": 10,
			".rs": 10, ".c": 10, ".h": 10, ".cpp": 10, ".java": 10,
			".sh": 10, ".rb": 10, ".md": 100, ".json": 50, ".yaml": 50,
			".yml": 50, ".toml": 50, ".txt": 100, ".html": 100, ".css": 100,
		},
	}
}
