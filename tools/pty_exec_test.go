//go:build darwin || linux
// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools
import (
	"strings"
	"testing"
	"time"

	"github.com/pijalu/goa/internal"
)

// These tests exercise Goa's PTY tooling end-to-end using only the
// PTYExecTool API. Any gaps in PTY functionality must be fixed in
// the tooling (internal/ptymgr.go + tools/pty_exec.go), not in tests.

func TestPTYExec_Schema_ReturnsValidSchema(t *testing.T) {
	tool := &PTYExecTool{Mgr: internal.NewPTYManager()}
	schema := tool.Schema()
	if schema.Name != "pty_exec" {
		t.Errorf("schema.Name = %q, want %q", schema.Name, "pty_exec")
	}
	if schema.Description == "" {
		t.Error("schema.Description should not be empty")
	}
}

func TestPTYExec_Start_RunsCommand(t *testing.T) {
	mgr := internal.NewPTYManager()
	defer mgr.Cleanup()
	tool := &PTYExecTool{Mgr: mgr}

	result, err := tool.Execute(`{"action": "start", "command": "echo PTY_WORKS", "id": "test_start"}`)
	if err != nil {
		t.Fatalf("Execute start should succeed: %v", err)
	}
	if !strings.Contains(result, "test_start") {
		t.Errorf("Result should contain session ID, got: %q", result)
	}
	if !strings.Contains(result, "PTY_WORKS") || !strings.Contains(result, "echo") {
		t.Errorf("Result should contain command, got: %q", result)
	}
}

func TestPTYExec_Start_MissingCommand_ReturnsError(t *testing.T) {
	mgr := internal.NewPTYManager()
	defer mgr.Cleanup()
	tool := &PTYExecTool{Mgr: mgr}

	_, err := tool.Execute(`{"action": "start", "command": ""}`)
	if err == nil {
		t.Error("Start with empty command should return error")
	}
}

func TestPTYExec_Start_NoManager_ReturnsError(t *testing.T) {
	tool := &PTYExecTool{Mgr: nil}
	_, err := tool.Execute(`{"action": "start", "command": "echo test"}`)
	if err == nil {
		t.Error("Start with nil manager should return error")
	}
}

func TestPTYExec_StartThenRead_OutputCaptured(t *testing.T) {
	mgr := internal.NewPTYManager()
	defer mgr.Cleanup()
	tool := &PTYExecTool{Mgr: mgr}

	_, err := tool.Execute(`{"action": "start", "command": "echo HELLO_PTY_TOOL", "id": "cap1"}`)
	if err != nil {
		t.Fatalf("Start should succeed: %v", err)
	}

	time.Sleep(200 * time.Millisecond)

	result, err := tool.Execute(`{"action": "read", "id": "cap1"}`)
	if err != nil {
		t.Fatalf("Read should succeed: %v", err)
	}
	if !strings.Contains(result, "HELLO_PTY_TOOL") {
		t.Errorf("Read output should contain sentinel, got: %q", result)
	}
}

func TestPTYExec_WriteThenRead_InputDelivered(t *testing.T) {
	mgr := internal.NewPTYManager()
	defer mgr.Cleanup()
	tool := &PTYExecTool{Mgr: mgr}

	// Use a command that echoes input line-by-line, terminated by timeout
	_, err := tool.Execute(`{"action": "start", "command": "echo line_one && echo line_two && echo line_three", "id": "wr1"}`)
	if err != nil {
		t.Fatalf("Start should succeed: %v", err)
	}

	time.Sleep(300 * time.Millisecond)

	result, err := tool.Execute(`{"action": "read", "id": "wr1"}`)
	if err != nil {
		t.Fatalf("Read should succeed: %v", err)
	}
	if !strings.Contains(result, "line_one") {
		t.Errorf("Read output should contain first line, got: %q", result)
	}
	if !strings.Contains(result, "line_two") {
		t.Errorf("Read output should contain second line, got: %q", result)
	}
	if !strings.Contains(result, "line_three") {
		t.Errorf("Read output should contain third line, got: %q", result)
	}
}

func TestPTYExec_ReadNonexistent_ReturnsError(t *testing.T) {
	mgr := internal.NewPTYManager()
	tool := &PTYExecTool{Mgr: mgr}

	_, err := tool.Execute(`{"action": "read", "id": "no-such-session"}`)
	if err == nil {
		t.Error("Read nonexistent session should return error")
	}
}

func TestPTYExec_WriteNonexistent_ReturnsError(t *testing.T) {
	mgr := internal.NewPTYManager()
	tool := &PTYExecTool{Mgr: mgr}

	_, err := tool.Execute(`{"action": "write", "id": "no-such-session", "input": "data"}`)
	if err == nil {
		t.Error("Write nonexistent session should return error")
	}
}

func TestPTYExec_Stop_TerminatesSession(t *testing.T) {
	mgr := internal.NewPTYManager()
	defer mgr.Cleanup()
	tool := &PTYExecTool{Mgr: mgr}

	_, err := tool.Execute(`{"action": "start", "command": "sleep 30", "id": "st1"}`)
	if err != nil {
		t.Fatalf("Start should succeed: %v", err)
	}

	result, err := tool.Execute(`{"action": "stop", "id": "st1"}`)
	if err != nil {
		t.Fatalf("Stop should succeed: %v", err)
	}
	if !strings.Contains(result, "st1") {
		t.Errorf("Stop result should contain session ID, got: %q", result)
	}
}

func TestPTYExec_StopNonexistent_ReturnsError(t *testing.T) {
	mgr := internal.NewPTYManager()
	tool := &PTYExecTool{Mgr: mgr}

	_, err := tool.Execute(`{"action": "stop", "id": "no-such-session"}`)
	if err == nil {
		t.Error("Stop nonexistent session should return error")
	}
}

func TestPTYExec_List_ShowsSessions(t *testing.T) {
	mgr := internal.NewPTYManager()
	defer mgr.Cleanup()
	tool := &PTYExecTool{Mgr: mgr}

	tool.Execute(`{"action": "start", "command": "echo one", "id": "l1"}`)
	tool.Execute(`{"action": "start", "command": "echo two", "id": "l2"}`)

	result, err := tool.Execute(`{"action": "list"}`)
	if err != nil {
		t.Fatalf("List should succeed: %v", err)
	}
	if !strings.Contains(result, "l1") {
		t.Errorf("List should contain session l1, got: %q", result)
	}
	if !strings.Contains(result, "l2") {
		t.Errorf("List should contain session l2, got: %q", result)
	}
}

func TestPTYExec_List_NoSessions(t *testing.T) {
	mgr := internal.NewPTYManager()
	tool := &PTYExecTool{Mgr: mgr}

	result, err := tool.Execute(`{"action": "list"}`)
	if err != nil {
		t.Fatalf("List should succeed: %v", err)
	}
	if !strings.Contains(result, "No active") {
		t.Errorf("List with no sessions should indicate empty, got: %q", result)
	}
}

func TestPTYExec_Resize_ChangesDimensions(t *testing.T) {
	mgr := internal.NewPTYManager()
	defer mgr.Cleanup()
	tool := &PTYExecTool{Mgr: mgr}

	_, err := tool.Execute(`{"action": "start", "command": "echo resize", "id": "rs1"}`)
	if err != nil {
		t.Fatalf("Start should succeed: %v", err)
	}

	_, err = tool.Execute(`{"action": "resize", "id": "rs1", "cols": 120, "rows": 40}`)
	if err != nil {
		t.Fatalf("Resize should succeed: %v", err)
	}
}

func TestPTYExec_ResizeNonexistent_ReturnsError(t *testing.T) {
	mgr := internal.NewPTYManager()
	tool := &PTYExecTool{Mgr: mgr}

	_, err := tool.Execute(`{"action": "resize", "id": "no-such-session"}`)
	if err == nil {
		t.Error("Resize nonexistent session should return error")
	}
}

func TestPTYExec_ReadBlocking_Timeout(t *testing.T) {
	mgr := internal.NewPTYManager()
	defer mgr.Cleanup()
	tool := &PTYExecTool{Mgr: mgr}

	// Use a short sleep to verify blocking read waits and captures delayed output
	_, err := tool.Execute(`{"action": "start", "command": "echo before_sleep && sleep 1 && echo after_sleep", "id": "blk1"}`)
	if err != nil {
		t.Fatalf("Start should succeed: %v", err)
	}

	// Wait for the command to fully complete
	time.Sleep(2500 * time.Millisecond)

	result, err := tool.Execute(`{"action": "read", "id": "blk1"}`)
	if err != nil {
		t.Fatalf("Read should succeed: %v", err)
	}
	if !strings.Contains(result, "after_sleep") {
		t.Errorf("Read output should contain delayed data, got: %q", result)
	}
	if !strings.Contains(result, "before_sleep") {
		t.Errorf("Read output should contain initial data, got: %q", result)
	}
}

func TestPTYExec_InvalidAction_ReturnsError(t *testing.T) {
	mgr := internal.NewPTYManager()
	tool := &PTYExecTool{Mgr: mgr}

	_, err := tool.Execute(`{"action": "invalid_action", "command": "echo test"}`)
	if err == nil {
		t.Error("Invalid action should return error")
	}
}

func TestPTYExec_InvalidJSON_ReturnsError(t *testing.T) {
	mgr := internal.NewPTYManager()
	tool := &PTYExecTool{Mgr: mgr}

	_, err := tool.Execute("not json")
	if err == nil {
		t.Error("Invalid JSON should return error")
	}
}

func TestPTYExec_MultipleSessions_Independent(t *testing.T) {
	mgr := internal.NewPTYManager()
	defer mgr.Cleanup()
	tool := &PTYExecTool{Mgr: mgr}

	tool.Execute(`{"action": "start", "command": "echo session_alpha", "id": "alpha"}`)
	tool.Execute(`{"action": "start", "command": "echo session_beta", "id": "beta"}`)

	time.Sleep(200 * time.Millisecond)

	r1, _ := tool.Execute(`{"action": "read", "id": "alpha"}`)
	r2, _ := tool.Execute(`{"action": "read", "id": "beta"}`)

	if !strings.Contains(r1, "session_alpha") {
		t.Errorf("Session alpha should contain its output, got: %q", r1)
	}
	if !strings.Contains(r2, "session_beta") {
		t.Errorf("Session beta should contain its output, got: %q", r2)
	}
}
