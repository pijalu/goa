// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package hooks

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEngine_FireBeforeTool_Passing(t *testing.T) {
	cfg := &Config{
		Hooks: []Hook{
			{Event: EventBeforeTool, Command: "sh", Args: []string{"-c", "cat"}},
		},
	}
	engine := NewEngine(cfg, nil)
	if err := engine.FireBeforeTool(context.Background(), ToolPayload{ToolName: "bash"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := engine.store.Entries(); len(got) != 1 {
		t.Fatalf("expected 1 audit entry, got %d", len(got))
	}
}

func TestEngine_FireBeforeTool_Veto(t *testing.T) {
	cfg := &Config{
		Hooks: []Hook{
			{Event: EventBeforeTool, Command: "sh", Args: []string{"-c", "exit 1"}},
		},
	}
	engine := NewEngine(cfg, nil)
	if err := engine.FireBeforeTool(context.Background(), ToolPayload{ToolName: "bash"}); err == nil {
		t.Fatal("expected veto error from failing beforeTool hook")
	}
}

func TestEngine_FireAfterTool_DoesNotVeto(t *testing.T) {
	cfg := &Config{
		Hooks: []Hook{
			{Event: EventAfterTool, Command: "sh", Args: []string{"-c", "exit 1"}},
		},
	}
	engine := NewEngine(cfg, nil)
	if err := engine.FireAfterTool(context.Background(), ToolPayload{ToolName: "bash"}); err != nil {
		t.Fatalf("afterTool hook failure should not veto: %v", err)
	}
}

// TestEngine_NonexistentCommand_RecordsFailure verifies a hook whose command
// cannot be started is recorded in the audit log with a non-zero exit code and
// a non-empty error message, instead of being masked as success (F1).
func TestEngine_NonexistentCommand_RecordsFailure(t *testing.T) {
	cfg := &Config{
		Hooks: []Hook{
			{Event: EventAfterTool, Command: "/no/such/binary/goa-hook"},
		},
	}
	engine := NewEngine(cfg, NewStore("")) // in-memory audit
	if err := engine.FireAfterTool(context.Background(), ToolPayload{ToolName: "bash"}); err != nil {
		t.Fatalf("afterTool must not veto on launch failure: %v", err)
	}
	entries := engine.Store().Entries()
	if len(entries) != 1 {
		t.Fatalf("expected 1 audit entry, got %d", len(entries))
	}
	if entries[0].ExitCode == 0 {
		t.Errorf("expected non-zero exit code for failed launch, got 0")
	}
	if entries[0].Output == "" {
		t.Errorf("expected non-empty output describing the launch failure")
	}
}

func TestEngine_FireBeforeTool_ReceivesPayload(t *testing.T) {
	dir := t.TempDir()
	outFile := filepath.Join(dir, "out.json")
	cfg := &Config{
		Hooks: []Hook{
			{Event: EventBeforeTool, Command: "sh", Args: []string{"-c", "cat > " + outFile}},
		},
	}
	engine := NewEngine(cfg, nil)
	payload := ToolPayload{Event: string(EventBeforeTool), ToolName: "bash", ToolInput: "ls", CallID: "call_1"}
	if err := engine.FireBeforeTool(context.Background(), payload); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	data, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("read captured payload: %v", err)
	}
	if !strings.Contains(string(data), `"tool_name":"bash"`) {
		t.Errorf("expected payload to contain tool name, got %s", data)
	}
	if !strings.Contains(string(data), `"call_id":"call_1"`) {
		t.Errorf("expected payload to contain call id, got %s", data)
	}
}

func TestEngine_FireAfterTool_PassesResult(t *testing.T) {
	dir := t.TempDir()
	outFile := filepath.Join(dir, "out.json")
	cfg := &Config{
		Hooks: []Hook{
			{Event: EventAfterTool, Command: "sh", Args: []string{"-c", "cat > " + outFile}},
		},
	}
	engine := NewEngine(cfg, nil)
	payload := ToolPayload{ToolName: "bash", Output: "hello", Error: ""}
	if err := engine.FireAfterTool(context.Background(), payload); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	data, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("read captured payload: %v", err)
	}
	if !strings.Contains(string(data), `"output":"hello"`) {
		t.Errorf("expected payload to contain output, got %s", data)
	}
}

func TestEngine_FireSessionStart(t *testing.T) {
	cfg := &Config{
		Hooks: []Hook{
			{Event: EventSessionStart, Command: "sh", Args: []string{"-c", "cat"}},
		},
	}
	engine := NewEngine(cfg, nil)
	if err := engine.FireSessionStart(context.Background(), SessionPayload{SessionID: "s1"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := engine.store.Entries(); len(got) != 1 {
		t.Fatalf("expected 1 audit entry, got %d", len(got))
	}
}

func TestConfig_Validate(t *testing.T) {
	if err := (Config{Hooks: []Hook{{Event: EventBeforeTool, Command: "x"}}}).Validate(); err != nil {
		t.Errorf("expected valid config, got %v", err)
	}
	if err := (Config{Hooks: []Hook{{Event: "", Command: "x"}}}).Validate(); err == nil {
		t.Error("expected error for missing event")
	}
	if err := (Config{Hooks: []Hook{{Event: EventBeforeTool, Command: ""}}}).Validate(); err == nil {
		t.Error("expected error for missing command")
	}
	if err := (Config{Hooks: []Hook{{Event: "unknown", Command: "x"}}}).Validate(); err == nil {
		t.Error("expected error for unknown event")
	}
}

func TestLoadConfig_Cascade(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	project := filepath.Join(dir, "project")
	if err := os.MkdirAll(filepath.Join(home, ".goa"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(project, ".goa"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".goa", "hooks.yaml"), []byte("hooks:\n- event: sessionStart\n  command: a\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, ".goa", "hooks.yaml"), []byte("hooks:\n- event: beforeTool\n  command: b\n"), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(home, project)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Hooks) != 2 {
		t.Fatalf("expected 2 hooks, got %d", len(cfg.Hooks))
	}
	if cfg.Hooks[0].Event != EventSessionStart || cfg.Hooks[0].Command != "a" {
		t.Errorf("expected user sessionStart hook, got %+v", cfg.Hooks[0])
	}
	if cfg.Hooks[1].Event != EventBeforeTool || cfg.Hooks[1].Command != "b" {
		t.Errorf("expected project beforeTool hook, got %+v", cfg.Hooks[1])
	}
}

func TestStore_Record(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hooks.log")
	store := NewStore(path)
	if err := store.Record(Entry{Event: EventBeforeTool, Command: "x"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(store.Entries()) != 1 {
		t.Fatalf("expected 1 in-memory entry, got %d", len(store.Entries()))
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if !strings.Contains(string(data), `"event":"beforeTool"`) {
		t.Errorf("expected log to contain event, got %s", data)
	}
}
