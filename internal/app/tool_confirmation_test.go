// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"context"
	"testing"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/tui"
)

func TestApprovalKey_NormalizesRelativePaths(t *testing.T) {
	project := t.TempDir()
	key := approvalKey("read", `{"path":"src/main.go"}`, project)
	if key == "" {
		t.Fatal("expected non-empty key")
	}
	if key == `read:src/main.go` {
		t.Error("approval key should normalize relative paths to absolute")
	}
}

func TestApprovalKey_KeepsAbsolutePaths(t *testing.T) {
	key := approvalKey("read", `{"path":"/etc/passwd"}`, "/project")
	want := "read:/etc/passwd"
	if key != want {
		t.Errorf("approvalKey = %q, want %q", key, want)
	}
}

func TestApprovalKey_NoPath(t *testing.T) {
	key := approvalKey("bash", `{"command":"ls"}`, "/project")
	want := "bash:*"
	if key != want {
		t.Errorf("approvalKey = %q, want %q", key, want)
	}
}

func TestLoadPersistedPathApprovals(t *testing.T) {
	project := t.TempDir()
	store := core.NewStateStore(project)
	_ = store.Save(core.SessionStateSnapshot{
		ModeState:     internal.ModeState{Major: internal.MajorCoder, Autonomy: internal.AutonomyConfirm},
		ApprovedPaths: []string{"read:/etc/passwd"},
		DeniedPaths:   []string{"bash:/etc/passwd"},
	})

	a := New(testSubsystems())
	a.subs.projectDir = project
	a.subs.stateStore = store
	a.loadPersistedPathApprovals()

	if !a.isPathApproved("read:/etc/passwd") {
		t.Error("expected read:/etc/passwd to be approved")
	}
	if !a.isPathDenied("bash:/etc/passwd") {
		t.Error("expected bash:/etc/passwd to be denied")
	}
}

func TestRecordPathApproval_Persists(t *testing.T) {
	project := t.TempDir()
	store := core.NewStateStore(project)

	a := New(testSubsystems())
	a.subs.projectDir = project
	a.subs.stateStore = store

	a.recordPathApproval("read:/tmp/x", true)

	snap, err := store.Load()
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	found := false
	for _, k := range snap.ApprovedPaths {
		if k == "read:/tmp/x" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected approved path persisted, got %v", snap.ApprovedPaths)
	}
}

func TestConfirmTool_AllowsWithoutDialog(t *testing.T) {
	cfg := &config.Config{}
	am := core.NewAgentManager(cfg, nil, nil, core.NewSessionState(internal.ModeState{Major: internal.MajorCoder, Autonomy: internal.AutonomyConfirm}), nil, t.TempDir())
	subs := testSubsystems()
	subs.agentMgr = am
	a := New(subs)
	a.subs.projectDir = t.TempDir()
	engine := tui.NewTUI(tui.NewProcessTerminal())

	// Read inside project in confirm mode should not ask.
	allowed, err := a.confirmTool(context.Background(), engine, "read", `{"path":"main.go"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !allowed {
		t.Error("expected read inside project to be allowed")
	}
}

func TestConfirmTool_YoloBypasses(t *testing.T) {
	cfg := &config.Config{}
	am := core.NewAgentManager(cfg, nil, nil, core.NewSessionState(internal.ModeState{Major: internal.MajorCoder, Autonomy: internal.AutonomyYolo}), nil, t.TempDir())
	subs := testSubsystems()
	subs.agentMgr = am
	a := New(subs)
	a.subs.projectDir = t.TempDir()
	engine := tui.NewTUI(tui.NewProcessTerminal())

	// YOLO should allow bash outside project without asking.
	allowed, err := a.confirmTool(context.Background(), engine, "bash", `{"command":"cat /etc/passwd"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !allowed {
		t.Error("expected YOLO to allow outside bash")
	}
}
