// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package core

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// DreamDiff holds a structured comparison between a dream output and the
// current memory store.
type DreamDiff struct {
	InputMemories int
	InputSessions int
	OutputBytes   int64
	TopicsAdded   []string
	TopicsRemoved []string
	TopicsChanged []string
	RawInput      string
	RawOutput     string
}

// HumanSummary returns a concise, human-readable summary of the diff.
func (dd *DreamDiff) HumanSummary() string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Dream consolidates %d memory files into one file (%d bytes).\n", dd.InputMemories, dd.OutputBytes))
	if len(dd.TopicsAdded) > 0 {
		b.WriteString(fmt.Sprintf("Topics added: %s\n", strings.Join(dd.TopicsAdded, ", ")))
	}
	if len(dd.TopicsRemoved) > 0 {
		b.WriteString(fmt.Sprintf("Topics removed: %s\n", strings.Join(dd.TopicsRemoved, ", ")))
	}
	if len(dd.TopicsChanged) > 0 {
		b.WriteString(fmt.Sprintf("Topics changed: %s\n", strings.Join(dd.TopicsChanged, ", ")))
	}
	return strings.TrimSpace(b.String())
}

// BuildDiff compares the proposed dream output with the existing consolidated
// memory (if any) and the raw memory store.
func (d *DreamEngine) BuildDiff(outputPath string) (*DreamDiff, error) {
	memories, err := d.collectMemories()
	if err != nil {
		return nil, fmt.Errorf("collect memories: %w", err)
	}

	stat, err := os.Stat(outputPath)
	if err != nil {
		return nil, fmt.Errorf("stat dream output: %w", err)
	}
	outputData, err := os.ReadFile(outputPath)
	if err != nil {
		return nil, fmt.Errorf("read dream output: %w", err)
	}

	sessions, _ := d.collectSessions()

	dd := &DreamDiff{
		InputMemories: len(memories),
		InputSessions: len(sessions),
		OutputBytes:   stat.Size(),
		RawOutput:     string(outputData),
	}

	consolidatedPath := filepath.Join(d.consolidatedDir(), "consolidated.md")
	if data, err := os.ReadFile(consolidatedPath); err == nil {
		dd.RawInput = string(data)
		oldTopics := extractH2Topics(dd.RawInput)
		newTopics := extractH2Topics(dd.RawOutput)
		dd.TopicsAdded, dd.TopicsRemoved, dd.TopicsChanged = compareTopics(oldTopics, newTopics)
	} else {
		dd.TopicsAdded = extractH2Topics(dd.RawOutput)
	}

	return dd, nil
}

// Diff returns a human-readable summary of what a dream would change.
func (d *DreamEngine) Diff(outputPath string) (string, error) {
	dd, err := d.BuildDiff(outputPath)
	if err != nil {
		return "", err
	}
	return dd.HumanSummary(), nil
}

func extractH2Topics(text string) []string {
	var topics []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "## ") {
			topics = append(topics, strings.TrimSpace(line[3:]))
		}
	}
	return topics
}

func compareTopics(old, new []string) (added, removed, changed []string) {
	oldSet := make(map[string]bool)
	newSet := make(map[string]bool)
	for _, t := range old {
		oldSet[t] = true
	}
	for _, t := range new {
		newSet[t] = true
	}
	for _, t := range new {
		if !oldSet[t] {
			added = append(added, t)
		}
	}
	for _, t := range old {
		if !newSet[t] {
			removed = append(removed, t)
		}
	}
	// Topics present in both are marked changed if their normalized content
	// differs. For now we report all existing topics as unchanged and let the
	// caller compare RawInput/RawOutput for detailed diffs.
	return added, removed, changed
}
