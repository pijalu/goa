// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/internal/perms"
)

func TestPermissionCommand_ListEmpty(t *testing.T) {
	var buf strings.Builder
	ctx := core.Context{OutputBuffer: &buf}
	cmd := &PermissionCommand{}
	if err := cmd.Run(ctx, []string{"list"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(buf.String(), "No permission rules") {
		t.Errorf("output = %q, want no-rules message", buf.String())
	}
}

func TestPermissionCommand_AddAndList(t *testing.T) {
	var buf strings.Builder
	ctx := core.Context{OutputBuffer: &buf}
	cmd := &PermissionCommand{}

	if err := cmd.Run(ctx, []string{"add", "mcp__github__*", "allow"}); err != nil {
		t.Fatalf("add: %v", err)
	}
	buf.Reset()
	if err := cmd.Run(ctx, []string{"list"}); err != nil {
		t.Fatalf("list: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "mcp__github__*") {
		t.Errorf("output missing rule: %q", out)
	}
	if len(cmd.Rules()) != 1 {
		t.Errorf("rules = %d, want 1", len(cmd.Rules()))
	}
}

func TestPermissionCommand_Remove(t *testing.T) {
	var buf strings.Builder
	ctx := core.Context{OutputBuffer: &buf}
	cmd := &PermissionCommand{}
	cmd.SetRules([]perms.Rule{
		{Pattern: "bash", Decision: perms.DecisionAsk},
	})

	if err := cmd.Run(ctx, []string{"remove", "0"}); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if len(cmd.Rules()) != 0 {
		t.Errorf("rules = %d, want 0", len(cmd.Rules()))
	}
}

func TestPermissionCommand_InvalidDecision(t *testing.T) {
	var buf strings.Builder
	ctx := core.Context{OutputBuffer: &buf}
	cmd := &PermissionCommand{}
	err := cmd.Run(ctx, []string{"add", "bash", "invalid"})
	if err == nil {
		t.Fatal("expected error for invalid decision")
	}
}
