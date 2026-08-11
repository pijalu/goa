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

// TestPTYExec_Read_StripsCarriageReturns: PTY streams carry termios ONLCR
// line endings ("alpha\r\r\n") and progress-style bare '\r' rewrites. A '\r'
// surviving into the tool result corrupts the TUI tool box (cursor returns
// to column 0, padding overwrites the line start) — the "pty garbage" bug.
// Read output must contain no carriage returns, and a rewritten line must
// show its final visible state.
func TestPTYExec_Read_StripsCarriageReturns(t *testing.T) {
	mgr := internal.NewPTYManager()
	defer mgr.Cleanup()
	tool := &PTYExecTool{Mgr: mgr}

	_, err := tool.Execute(`{"action": "start", "command": "printf 'alpha\\r\\nbeta\\r\\nprogress 1\\rprogress 2 done\\n'", "id": "cr1"}`)
	if err != nil {
		t.Fatalf("Start should succeed: %v", err)
	}

	time.Sleep(300 * time.Millisecond)

	result, err := tool.Execute(`{"action": "read", "id": "cr1"}`)
	if err != nil {
		t.Fatalf("Read should succeed: %v", err)
	}
	if strings.Contains(result, "\r") {
		t.Errorf("Read output must not contain carriage returns, got: %q", result)
	}
	if !strings.Contains(result, "alpha\nbeta\n") {
		t.Errorf("Read output should keep both lines, got: %q", result)
	}
	if !strings.Contains(result, "progress 2 done") {
		t.Errorf("Read output should show the final rewritten line, got: %q", result)
	}
	if strings.Contains(result, "progress 1") {
		t.Errorf("Overwritten progress prefix should be resolved away, got: %q", result)
	}
}

// TestPTYExec_Read_SanitizesControlBytes: like bash/python/verify, tool
// output must not leak terminal-corrupting control bytes (bell, backspace)
// into the renderer.
func TestPTYExec_Read_SanitizesControlBytes(t *testing.T) {
	mgr := internal.NewPTYManager()
	defer mgr.Cleanup()
	tool := &PTYExecTool{Mgr: mgr}

	_, err := tool.Execute(`{"action": "start", "command": "printf 'ding\\a back\\bspace\\n'", "id": "cb1"}`)
	if err != nil {
		t.Fatalf("Start should succeed: %v", err)
	}

	time.Sleep(300 * time.Millisecond)

	result, err := tool.Execute(`{"action": "read", "id": "cb1"}`)
	if err != nil {
		t.Fatalf("Read should succeed: %v", err)
	}
	for _, r := range result {
		if r == '\a' || r == '\b' {
			t.Errorf("Read output contains raw control byte %q: %q", r, result)
		}
	}
	if !strings.Contains(result, "ding") || !strings.Contains(result, "space") {
		t.Errorf("Read output should keep printable content, got: %q", result)
	}
}

// TestNormalizePTYOutput covers the pure stream-to-text conversion: ONLCR
// collapsing, progress rewrites, shorter overwrites leaving a tail, and
// text without '\r' passing through untouched.
func TestNormalizePTYOutput(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"no carriage returns", "plain\ntext\n", "plain\ntext\n"},
		{"crlf collapses", "alpha\r\nbeta\r\n", "alpha\nbeta\n"},
		{"doubled onlcr cr collapses", "alpha\r\r\nbeta\r\r\n", "alpha\nbeta\n"},
		{"progress rewrite keeps final", "progress 1\rprogress 2 done", "progress 2 done"},
		{"shorter overwrite leaves tail", "longer text\rshort", "shortr text"},
		{"multiple rewrites keep last state", "aaa\rb\rcc", "cca"},
		{"trailing cr alone", "text\r", "text"},
		{"empty input", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := normalizePTYOutput(tc.in); got != tc.want {
				t.Errorf("normalizePTYOutput(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
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
