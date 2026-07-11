// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core/orchestrator"
)

// TestHeadless_OrchestrateFlagParses confirms --orchestrate is wired through
// flag parsing into RuntimeOptions and forces headless mode.
func TestHeadless_OrchestrateFlagParses(t *testing.T) {
	opts := RuntimeOptions{Orchestrate: "run-xyz"}
	if !opts.Headless() {
		t.Error("--orchestrate should imply headless")
	}
	if err := opts.validateModes(); err != nil {
		t.Errorf("--orchestrate validate failed: %v", err)
	}
}

// TestHeadless_OrchestrateRejectsFinishedRun proves the resume path reads the
// event log and refuses an already-finished run (returns before rendering).
func TestHeadless_OrchestrateRejectsFinishedRun(t *testing.T) {
	dir := t.TempDir()
	rootDir := filepath.Join(dir, ".goa", "orchestrator")
	store := orchestrator.NewFileEventStore(rootDir, "run-fin")
	_ = store.Append(orchestrator.Event{Type: orchestrator.EventRunStarted,
		Payload: map[string]any{"objective": "done already", "topology": "fanout"}})
	_ = store.Append(orchestrator.Event{Type: orchestrator.EventRunFinished})

	subs := &subsystems{
		cfg:         &config.Config{},
		projectDir:  dir,
		orchAdapter: NewOrchestratorAdapter(nil, &config.Config{}, ""),
		orchActive:  orchestrator.NewActiveRuntime(),
	}
	h := &HeadlessApp{subs: subs, opts: RuntimeOptions{Orchestrate: "run-fin"}}
	err := h.startOrchestrate(context.Background(), "run-fin")
	if err == nil {
		t.Fatalf("expected error resuming a finished run")
	}
	if !strings.Contains(err.Error(), "finished") {
		t.Errorf("expected 'finished' in error, got %v", err)
	}
}

// TestHeadless_OrchestrateRejectsUnknownRun proves a missing run-id errors.
func TestHeadless_OrchestrateRejectsUnknownRun(t *testing.T) {
	subs := &subsystems{
		cfg:         &config.Config{},
		projectDir:  t.TempDir(),
		orchAdapter: NewOrchestratorAdapter(nil, &config.Config{}, ""),
		orchActive:  orchestrator.NewActiveRuntime(),
	}
	h := &HeadlessApp{subs: subs, opts: RuntimeOptions{Orchestrate: "ghost"}}
	if err := h.startOrchestrate(context.Background(), "ghost"); err == nil {
		t.Fatalf("expected error resuming an unknown run")
	}
}

// TestHeadless_OrchestrateRejectsNoAdapter proves a missing adapter errors
// cleanly instead of panicking.
func TestHeadless_OrchestrateRejectsNoAdapter(t *testing.T) {
	subs := &subsystems{cfg: &config.Config{}, projectDir: t.TempDir()}
	h := &HeadlessApp{subs: subs, opts: RuntimeOptions{Orchestrate: "x"}}
	if err := h.startOrchestrate(context.Background(), "x"); err == nil {
		t.Fatalf("expected error with no adapter")
	}
}
