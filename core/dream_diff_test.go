// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/memory"
)

func TestDreamDiff_HumanSummary(t *testing.T) {
	dd := &DreamDiff{
		InputMemories: 3,
		OutputBytes:   1024,
		TopicsAdded:   []string{"Architecture"},
		TopicsRemoved: []string{"Stale"},
	}
	summary := dd.HumanSummary()
	if !strings.Contains(summary, "3 memory files") {
		t.Fatalf("expected memory count, got %q", summary)
	}
	if !strings.Contains(summary, "Architecture") {
		t.Fatalf("expected added topic, got %q", summary)
	}
	if !strings.Contains(summary, "Stale") {
		t.Fatalf("expected removed topic, got %q", summary)
	}
}

func TestDreamEngine_BuildDiff_NoConsolidated(t *testing.T) {
	dir := t.TempDir()
	store := memory.NewMemoryStore(dir, "")
	if err := store.Write("facts", "summary: facts\n\n## Facts\n\n- old"); err != nil {
		t.Fatalf("write memory: %v", err)
	}

	outputDir := filepath.Join(dir, ".goa", "memory.dream")
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	outputPath := filepath.Join(outputDir, "dream.md")
	if err := os.WriteFile(outputPath, []byte("# Consolidated\n\n## Architecture\n\n- fact"), 0644); err != nil {
		t.Fatalf("write output: %v", err)
	}

	engine := NewDreamEngine(
		&config.Config{Memory: config.MemoryConfig{Enabled: true}},
		&fakeProviderResolver{},
		store,
		nil,
		dir,
		"",
	)
	dd, err := engine.BuildDiff(outputPath)
	if err != nil {
		t.Fatalf("BuildDiff failed: %v", err)
	}
	if dd.InputMemories != 1 {
		t.Fatalf("expected 1 memory, got %d", dd.InputMemories)
	}
	if len(dd.TopicsAdded) != 1 || dd.TopicsAdded[0] != "Architecture" {
		t.Fatalf("unexpected added topics: %v", dd.TopicsAdded)
	}
	if len(dd.TopicsRemoved) != 0 {
		t.Fatalf("unexpected removed topics: %v", dd.TopicsRemoved)
	}
}

func TestDreamEngine_BuildDiff_WithConsolidated(t *testing.T) {
	dir := t.TempDir()
	store := memory.NewMemoryStore(dir, "")
	if err := store.Write("facts", "summary: facts\n\n## Facts\n\n- old"); err != nil {
		t.Fatalf("write memory: %v", err)
	}

	consolidatedDir := filepath.Join(dir, ".goa", "memory.consolidated")
	if err := os.MkdirAll(consolidatedDir, 0755); err != nil {
		t.Fatalf("mkdir consolidated: %v", err)
	}
	if err := os.WriteFile(filepath.Join(consolidatedDir, "consolidated.md"), []byte("# Old\n\n## Stale\n\n- x"), 0644); err != nil {
		t.Fatalf("write consolidated: %v", err)
	}

	outputDir := filepath.Join(dir, ".goa", "memory.dream")
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		t.Fatalf("mkdir dream: %v", err)
	}
	outputPath := filepath.Join(outputDir, "dream.md")
	if err := os.WriteFile(outputPath, []byte("# Consolidated\n\n## Architecture\n\n- fact"), 0644); err != nil {
		t.Fatalf("write output: %v", err)
	}

	engine := NewDreamEngine(
		&config.Config{Memory: config.MemoryConfig{Enabled: true}},
		&fakeProviderResolver{},
		store,
		nil,
		dir,
		"",
	)
	dd, err := engine.BuildDiff(outputPath)
	if err != nil {
		t.Fatalf("BuildDiff failed: %v", err)
	}
	if len(dd.TopicsAdded) != 1 || dd.TopicsAdded[0] != "Architecture" {
		t.Fatalf("unexpected added topics: %v", dd.TopicsAdded)
	}
	if len(dd.TopicsRemoved) != 1 || dd.TopicsRemoved[0] != "Stale" {
		t.Fatalf("unexpected removed topics: %v", dd.TopicsRemoved)
	}
}
