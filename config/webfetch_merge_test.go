// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWebFetchDefaultsNotLostOnProjectMerge(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	projectDir := filepath.Join(dir, "project")
	if err := os.MkdirAll(filepath.Join(projectDir, ".goa"), 0755); err != nil {
		t.Fatal(err)
	}
	projectYAML := `
tools:
  bash:
    blocked_commands: []
`
	if err := os.WriteFile(filepath.Join(projectDir, ".goa", "config.yaml"), []byte(projectYAML), 0644); err != nil {
		t.Fatal(err)
	}

	loader := NewCascadeLoader(projectDir, "", nil)
	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}
	t.Logf("WebFetch config: %+v", cfg.Tools.WebFetch)

	if cfg.Tools.WebFetch.MaxLinesDefault == 0 {
		t.Errorf("webfetch max_lines_default was lost on merge: got %d", cfg.Tools.WebFetch.MaxLinesDefault)
	}
	if cfg.Tools.WebFetch.MaxLinesDefault != 250 {
		t.Errorf("webfetch max_lines_default = %d, want 250", cfg.Tools.WebFetch.MaxLinesDefault)
	}
	if cfg.Tools.WebFetch.MaxLinesHard != 4096 {
		t.Errorf("webfetch max_lines_hard = %d, want 4096", cfg.Tools.WebFetch.MaxLinesHard)
	}
	if cfg.Tools.WebFetch.MaxTotalBytes != 20*1024*1024 {
		t.Errorf("webfetch max_total_bytes = %d, want 20MiB", cfg.Tools.WebFetch.MaxTotalBytes)
	}
	if cfg.Tools.WebFetch.Cache.TTLHours != 24 {
		t.Errorf("webfetch cache ttl_hours = %d, want 24", cfg.Tools.WebFetch.Cache.TTLHours)
	}
}
