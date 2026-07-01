// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core"
)

func TestThinkingBlocksCommand_Name(t *testing.T) {
	cmd := &ThinkingBlocksCommand{}
	if cmd.Name() != "thinking-blocks" {
		t.Errorf("expected name 'thinking-blocks', got %q", cmd.Name())
	}
}

func TestThinkingBlocksCommand_ToggleAndPersist(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)

	loader := config.NewCascadeLoader(homeDir, "", nil)
	cfg := &config.Config{}
	cfg.TUI.Transparency.ThinkingCollapsed = false

	cmd := &ThinkingBlocksCommand{}
	ctx := core.Context{
		Config:      cfg,
		ConfigSaver: loader,
	}

	if err := cmd.Run(ctx, []string{"off"}); err != nil {
		t.Fatalf("Run(off) failed: %v", err)
	}
	if !cfg.TUI.Transparency.ThinkingCollapsed {
		t.Error("expected ThinkingCollapsed=true after off")
	}

	data, err := os.ReadFile(filepath.Join(homeDir, ".goa", "config.yaml"))
	if err != nil {
		t.Fatalf("failed to read saved config: %v", err)
	}
	if !strings.Contains(string(data), "thinking_collapsed") {
		t.Errorf("saved config should contain thinking_collapsed, got: %s", data)
	}

	if err := cmd.Run(ctx, []string{"on"}); err != nil {
		t.Fatalf("Run(on) failed: %v", err)
	}
	if cfg.TUI.Transparency.ThinkingCollapsed {
		t.Error("expected ThinkingCollapsed=false after on")
	}
}

func TestThinkingBlocksCommand_Status(t *testing.T) {
	cmd := &ThinkingBlocksCommand{}
	ctx := core.Context{
		Config: &config.Config{
			TUI: config.TUIConfig{
				Transparency: config.TransparencyConfig{ThinkingCollapsed: true},
			},
		},
	}
	if err := cmd.Run(ctx, nil); err != nil {
		t.Fatalf("Run(nil) failed: %v", err)
	}
}
