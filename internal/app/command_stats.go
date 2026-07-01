// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// CommandStats tracks how often each command is used in a project.
// Data is stored in .goa/command_stats.json and loaded on startup.
// Used by completion to favor frequently used commands.
type CommandStats struct {
	mu         sync.Mutex
	projectDir string
	counts     map[string]int // "/skill:run:refactor" → 5
	dirty      bool
}

// NewCommandStats creates or loads command stats for the project.
func NewCommandStats(projectDir string) *CommandStats {
	cs := &CommandStats{
		projectDir: projectDir,
		counts:     make(map[string]int),
	}
	cs.load()
	return cs
}

func (cs *CommandStats) path() string {
	return filepath.Join(cs.projectDir, ".goa", "command_stats.json")
}

func (cs *CommandStats) load() {
	data, err := os.ReadFile(cs.path())
	if err != nil {
		return // file doesn't exist yet
	}
	cs.mu.Lock()
	defer cs.mu.Unlock()
	json.Unmarshal(data, &cs.counts)
}

// Record increments the usage count for a command string.
func (cs *CommandStats) Record(command string) {
	cs.mu.Lock()
	cs.counts[command]++
	cs.dirty = true
	cs.mu.Unlock()
}

// Save writes stats to disk if dirty.
func (cs *CommandStats) Save() error {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	if !cs.dirty {
		return nil
	}
	dir := filepath.Dir(cs.path())
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cs.counts, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(cs.path(), data, 0644); err != nil {
		return err
	}
	cs.dirty = false
	return nil
}

// All returns all command usage counts.
func (cs *CommandStats) All() map[string]int {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	result := make(map[string]int, len(cs.counts))
	for k, v := range cs.counts {
		result[k] = v
	}
	return result
}

// Score returns the usage count for a command (0 if never used).
func (cs *CommandStats) Score(command string) int {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	return cs.counts[command]
}

// TopN returns the N most-used commands and their counts.
func (cs *CommandStats) TopN(n int) []struct {
	Command string
	Count   int
} {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	type pair struct {
		cmd   string
		count int
	}
	var sorted []pair
	for cmd, count := range cs.counts {
		sorted = append(sorted, pair{cmd, count})
	}
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].count > sorted[j].count
	})
	if n > len(sorted) {
		n = len(sorted)
	}
	result := make([]struct {
		Command string
		Count   int
	}, n)
	for i := 0; i < n; i++ {
		result[i].Command = sorted[i].cmd
		result[i].Count = sorted[i].count
	}
	return result
}

// OftenUsed returns commands with count >= threshold (default: 3).
func (cs *CommandStats) OftenUsed(threshold int) map[string]int {
	if threshold <= 0 {
		threshold = 3
	}
	cs.mu.Lock()
	defer cs.mu.Unlock()
	result := make(map[string]int)
	for cmd, count := range cs.counts {
		if count >= threshold {
			result[cmd] = count
		}
	}
	return result
}
