// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package main

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/core/commands"
	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/internal/app"
	"github.com/pijalu/goa/provider"
)

var testCmdRegistry = core.NewCommandRegistry()

func init() {
	if err := commands.RegisterAll(testCmdRegistry, commands.CommandDependencies{}); err != nil {
		panic(err)
	}
}

// These tests validate commands through the Go API directly.
// They verify that registered commands produce meaningful output.

func newTestContext() core.Context {
	return core.Context{
		Config: &config.Config{
			Mode:      config.ModeConfig{Default: internal.ModeState{Major: internal.MajorCoder}},
			ConfigDir: "/tmp/goa-test",
			Execution: config.ExecutionConfig{Mode: internal.ExecutionYolo},
		},
		ProviderManager: provider.NewProviderManager(&config.Config{}),
		DocsProvider:    &app.DocsProvider{},
	}
}

func TestHelpCommand_ShowsCommands(t *testing.T) {
	ctx := newTestContext()
	cmd, found := testCmdRegistry.Resolve("help")
	if !found {
		t.Fatal("help command not registered")
	}

	var buf strings.Builder
	ctx.OutputBuffer = &buf

	if err := cmd.Run(ctx, nil); err != nil {
		t.Fatalf("Run: %v", err)
	}

	output := buf.String()
	if len(output) < 50 {
		t.Errorf("output too short: %d bytes", len(output))
	}
	if !strings.Contains(output, "/help") {
		t.Errorf("output should mention /help, got: %.300s", output)
	}
}

func TestHelpCommand_SpecificCommand_ShowsDetail(t *testing.T) {
	ctx := newTestContext()
	cmd, found := testCmdRegistry.Resolve("help")
	if !found {
		t.Fatal("help command not registered")
	}

	var buf strings.Builder
	ctx.OutputBuffer = &buf

	if err := cmd.Run(ctx, []string{"mode"}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	output := buf.String()
	if len(output) < 30 {
		t.Errorf("output too short: %d bytes", len(output))
	}
	if !strings.Contains(output, "Usage:") {
		t.Errorf("help mode should show usage, got: %.300s", output)
	}
}

func TestDocsCommand_ListsDocumentation(t *testing.T) {
	ctx := newTestContext()
	cmd, found := testCmdRegistry.Resolve("docs")
	if !found {
		t.Fatal("docs command not registered")
	}

	var buf strings.Builder
	ctx.OutputBuffer = &buf

	if err := cmd.Run(ctx, nil); err != nil {
		t.Fatalf("Run: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "ARCHITECTURE") {
		t.Errorf("output should list ARCHITECTURE, got: %.300s", output)
	}
}

func TestHelpCommand_ListsAllCommands(t *testing.T) {
	ctx := newTestContext()
	cmd, found := testCmdRegistry.Resolve("help")
	if !found {
		t.Fatal("help command not registered")
	}

	var buf strings.Builder
	ctx.OutputBuffer = &buf

	if err := cmd.Run(ctx, nil); err != nil {
		t.Fatalf("Run: %v", err)
	}

	output := buf.String()
	for _, name := range []string{"/help", "/docs", "/mode"} {
		if !strings.Contains(output, name) {
			t.Errorf("output should list %q", name)
		}
	}
}

func TestModeCommand_NoArgs_ShowsHelp(t *testing.T) {
	ctx := newTestContext()
	cmd, found := testCmdRegistry.Resolve("mode")
	if !found {
		t.Fatal("mode command not registered")
	}

	var buf strings.Builder
	ctx.OutputBuffer = &buf

	if err := cmd.Run(ctx, nil); err != nil {
		t.Fatalf("Run: %v", err)
	}

	output := buf.String()
	if len(output) < 10 {
		t.Errorf("output too short: %d bytes", len(output))
	}
}

func TestVersionCommand_ShowsVersion(t *testing.T) {
	ctx := newTestContext()
	cmd, found := testCmdRegistry.Resolve("version")
	if !found {
		t.Fatal("version command not registered")
	}

	var buf strings.Builder
	ctx.OutputBuffer = &buf

	if err := cmd.Run(ctx, nil); err != nil {
		t.Fatalf("Run: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "Goa") {
		t.Errorf("output should contain 'Goa', got: %.300s", output)
	}
}

func TestAutonomyCommand_ShowsStatus(t *testing.T) {
	ctx := newTestContext()
	cmd, found := testCmdRegistry.Resolve("autonomy")
	if !found {
		t.Fatal("autonomy command not registered")
	}

	var buf strings.Builder
	ctx.OutputBuffer = &buf

	if err := cmd.Run(ctx, nil); err != nil {
		t.Fatalf("Run: %v", err)
	}

	output := buf.String()
	if len(output) < 5 {
		t.Errorf("output too short: %d bytes", len(output))
	}
}
