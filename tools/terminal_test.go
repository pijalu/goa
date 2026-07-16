// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"context"
	"strings"
	"testing"
	"time"

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

// TestTerminalTool_ExecuteContext_CancelInterruptsLongCommand verifies that
// cancelling the turn context interrupts a running command via the sandbox's
// Cancel context instead of waiting for the configured timeout.
func TestTerminalTool_ExecuteContext_CancelInterruptsLongCommand(t *testing.T) {
	mgr, err := sandbox.NewManager("", nil)
	if err != nil {
		t.Fatalf("sandbox manager: %v", err)
	}
	tool := &TerminalTool{
		SandboxMgr:     mgr,
		TimeoutSeconds: 60,
		MaxOutputChars: 1000,
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(300 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	_, err = tool.ExecuteContext(ctx, `{"command":"sleep 30"}`)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected cancelled error, got nil")
	}
	if !strings.Contains(err.Error(), "cancelled") {
		t.Errorf("expected cancelled error, got %v", err)
	}
	if elapsed > 5*time.Second {
		t.Errorf("cancellation did not interrupt promptly: elapsed=%v", elapsed)
	}
}

// TestTerminalTool_SanitizesControlBytes: a command printing an escape
// sequence must reach the model/TUI as literal text, never as raw ESC bytes
// the terminal would execute when the widget renders.
func TestTerminalTool_SanitizesControlBytes(t *testing.T) {
	mgr, err := sandbox.NewManager("", nil)
	if err != nil {
		t.Fatalf("sandbox manager: %v", err)
	}
	tool := &TerminalTool{SandboxMgr: mgr, TimeoutSeconds: 10, MaxOutputChars: 1000}
	out, err := tool.Execute(`{"command":"printf '\\033[2Kwiped\\n'"}`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if strings.Contains(out, "\x1b") {
		t.Errorf("raw ESC byte leaked into tool output: %q", out)
	}
	if !strings.Contains(out, `\e[2Kwiped`) {
		t.Errorf("expected literal escape text, got: %q", out)
	}
}
