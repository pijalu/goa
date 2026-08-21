// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"testing"

	"github.com/pijalu/goa/core"
)

// newAgentTestContext returns a bare context whose event bus feeds a
// flashCollector, so flash assertions are possible without a TUI.
func newAgentTestContext(t *testing.T) (core.Context, *flashCollector) {
	t.Helper()
	ctx := core.Context{}
	fc := newFlashCollector(&ctx)
	return ctx, fc
}

// waitForFlash polls the collector for a message containing want.
func waitForFlash(t *testing.T, fc *flashCollector, want string) bool {
	t.Helper()
	return fc.contains(want)
}

// TestAgentCommand_TabSelects is the /agent:tab:<id> happy path: the host's
// SelectTab receives the reference and the selected label is flashed back.
func TestAgentCommand_TabSelects(t *testing.T) {
	var gotRef string
	ctx, fc := newAgentTestContext(t)
	cmd := &AgentCommand{
		SelectTab: func(ref string) (string, bool) {
			gotRef = ref
			return "coder·dlg-03", true
		},
	}
	if err := cmd.Run(ctx, []string{"tab", "dlg-coder-03"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if gotRef != "dlg-coder-03" {
		t.Errorf("SelectTab ref = %q, want dlg-coder-03", gotRef)
	}
	if !fc.contains("coder·dlg-03") {
		t.Error("expected the selected tab label to be flashed")
	}
}

// TestAgentCommand_TabUnknownListsTabs pins the actionable-error rule: an
// unknown id flashes an error that lists every known tab so the user can
// pick a valid one directly.
func TestAgentCommand_TabUnknownListsTabs(t *testing.T) {
	ctx, fc := newAgentTestContext(t)
	cmd := &AgentCommand{
		SelectTab:  func(string) (string, bool) { return "", false },
		ActiveTabs: func() []string { return []string{"main", "coder·dlg-03"} },
	}
	if err := cmd.Run(ctx, []string{"tab", "nope"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !waitForFlash(t, fc, `Unknown tab "nope"`) {
		t.Error("expected an unknown-tab error flash")
	}
	if !waitForFlash(t, fc, "coder·dlg-03") {
		t.Error("expected the error to list known tabs")
	}
}

// TestAgentCommand_ReplayDispatches verifies /agent:replay invokes the host
// replay callback and reports the replayed tab.
func TestAgentCommand_ReplayDispatches(t *testing.T) {
	called := false
	ctx, fc := newAgentTestContext(t)
	cmd := &AgentCommand{
		ReplayActiveTab: func() (string, bool) {
			called = true
			return "coder·dlg-03", true
		},
	}
	if err := cmd.Run(ctx, []string{"replay"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !called {
		t.Fatal("ReplayActiveTab was not invoked")
	}
	if !fc.contains("replaying coder·dlg-03") {
		t.Error("expected a replay-started flash naming the tab")
	}
}

// TestAgentCommand_ReplayUnavailable covers the host-rejected path (gate off,
// empty canvas): the user gets guidance instead of silence.
func TestAgentCommand_ReplayUnavailable(t *testing.T) {
	ctx, fc := newAgentTestContext(t)
	cmd := &AgentCommand{ReplayActiveTab: func() (string, bool) { return "", false }}
	if err := cmd.Run(ctx, []string{"replay"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !waitForFlash(t, fc, "multi_agent_scrollback_replay") {
		t.Error("expected an actionable unavailable-replay flash")
	}
}

// TestAgentCommand_UsageAndUnknownSubcommand verifies both no-args and bad
// subcommands surface the usage block.
func TestAgentCommand_UsageAndUnknownSubcommand(t *testing.T) {
	ctx, fc := newAgentTestContext(t)
	cmd := &AgentCommand{}
	if err := cmd.Run(ctx, nil); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !waitForFlash(t, fc, "/agent:tab:") || !waitForFlash(t, fc, "/agent:replay") {
		t.Error("bare /agent must show usage for both subcommands")
	}
	if err := cmd.Run(ctx, []string{"bogus"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !waitForFlash(t, fc, `Unknown /agent subcommand "bogus"`) {
		t.Error("unknown subcommand must be reported with usage")
	}
}

// TestAgentCommand_NilHostCallbacks verifies the graceful degradation paths.
func TestAgentCommand_NilHostCallbacks(t *testing.T) {
	ctx, fc := newAgentTestContext(t)
	cmd := &AgentCommand{}
	if err := cmd.Run(ctx, []string{"tab", "dlg-coder-01"}); err != nil {
		t.Fatalf("Run(tab): %v", err)
	}
	if !waitForFlash(t, fc, "not available") {
		t.Error("nil SelectTab must explain unavailability")
	}
	if err := cmd.Run(ctx, []string{"replay"}); err != nil {
		t.Fatalf("Run(replay): %v", err)
	}
	if !waitForFlash(t, fc, "not available") {
		t.Error("nil ReplayActiveTab must explain unavailability")
	}
}
