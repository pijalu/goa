// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/internal/sandbox"
)

func TestTerminalToolEcho(t *testing.T) {
	mgr, err := sandbox.NewManager("", nil)
	if err != nil {
		t.Fatalf("sandbox manager: %v", err)
	}
	tool := &TerminalTool{
		SandboxMgr:     mgr,
		TimeoutSeconds: 10,
		MaxOutputChars: 1000,
	}
	out, err := tool.Execute(`{"command":"echo hello"}`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "hello") {
		t.Fatalf("output %q does not contain hello", out)
	}
}

func TestTerminalToolBlocksDangerousCommand(t *testing.T) {
	mgr, err := sandbox.NewManager("", nil)
	if err != nil {
		t.Fatalf("sandbox manager: %v", err)
	}
	tool := &TerminalTool{
		SandboxMgr:     mgr,
		TimeoutSeconds: 10,
		MaxOutputChars: 1000,
	}
	_, err = tool.Execute(`{"command":"rm -rf /"}`)
	if err == nil {
		t.Fatal("expected block error")
	}
	te, ok := err.(*internal.ToolError)
	if !ok {
		t.Fatalf("expected ToolError, got %T", err)
	}
	if te.Type != "blocked_command" {
		t.Fatalf("type = %q, want blocked_command", te.Type)
	}
}

func TestTerminalToolArgumentPositionSafe(t *testing.T) {
	mgr, err := sandbox.NewManager("", nil)
	if err != nil {
		t.Fatalf("sandbox manager: %v", err)
	}
	tool := &TerminalTool{
		SandboxMgr:     mgr,
		TimeoutSeconds: 10,
		MaxOutputChars: 1000,
	}
	out, err := tool.Execute(`{"command":"echo curl"}`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "curl") {
		t.Fatalf("output %q does not contain curl", out)
	}
}
