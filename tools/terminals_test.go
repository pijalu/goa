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

// These tests exercise Goa's unified terminals tool end-to-end using only the
// TerminalsTool API. Any gaps in PTY functionality must be fixed in the
// tooling (internal/ptymgr.go + tools/terminals.go), not in tests.

func TestTerminals_Schema_HasAllSixActions(t *testing.T) {
	tool := &TerminalsTool{Mgr: internal.NewPTYManager()}
	schema := tool.Schema()
	if schema.Name != "terminals" {
		t.Errorf("schema.Name = %q, want %q", schema.Name, "terminals")
	}
	if schema.Description == "" {
		t.Error("schema.Description should not be empty")
	}
	props, ok := schema.Schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("schema properties missing")
	}
	action, ok := props["action"].(map[string]any)
	if !ok {
		t.Fatalf("action property missing")
	}
	enum, ok := action["enum"].([]string)
	if !ok {
		t.Fatalf("action enum missing")
	}
	want := []string{"open", "close", "list", "read", "send", "signal"}
	if len(enum) != len(want) {
		t.Fatalf("action enum = %v, want %v", enum, want)
	}
	for i := range want {
		if enum[i] != want[i] {
			t.Errorf("action enum[%d] = %q, want %q", i, enum[i], want[i])
		}
	}
	// dsh fields are present in the single schema.
	for _, field := range []string{"sessionId", "type", "name", "cwd", "offset", "count", "text", "submit", "run_in_background", "signal"} {
		if _, ok := props[field]; !ok {
			t.Errorf("schema missing property %q", field)
		}
	}
}

func TestTerminals_Open_RunsShell(t *testing.T) {
	mgr := internal.NewPTYManager()
	defer mgr.Cleanup()
	tool := &TerminalsTool{Mgr: mgr}

	result, err := tool.Execute(`{"action": "open", "type": "shell", "name": "test_open"}`)
	if err != nil {
		t.Fatalf("Execute open should succeed: %v", err)
	}
	if !strings.Contains(result, "test_open") {
		t.Errorf("Result should contain session ID, got: %q", result)
	}
	if !strings.Contains(result, "shell") {
		t.Errorf("Result should contain backend type, got: %q", result)
	}
}

func TestTerminals_Open_AutoID(t *testing.T) {
	mgr := internal.NewPTYManager()
	defer mgr.Cleanup()
	tool := &TerminalsTool{Mgr: mgr}

	result, err := tool.Execute(`{"action": "open"}`)
	if err != nil {
		t.Fatalf("Execute open should succeed: %v", err)
	}
	if !strings.Contains(result, "term-") {
		t.Errorf("Auto-generated ID should use term- prefix, got: %q", result)
	}
}

func TestTerminals_Open_UnknownBackend_ReturnsError(t *testing.T) {
	mgr := internal.NewPTYManager()
	defer mgr.Cleanup()
	tool := &TerminalsTool{Mgr: mgr}

	_, err := tool.Execute(`{"action": "open", "type": "gdb"}`)
	if err == nil {
		t.Error("Open with unknown backend should return error")
	}
}

func TestTerminals_Open_NoManager_ReturnsError(t *testing.T) {
	tool := &TerminalsTool{Mgr: nil}
	_, err := tool.Execute(`{"action": "open"}`)
	if err == nil {
		t.Error("Open with nil manager should return error")
	}
}

func TestTerminals_OpenThenRead_OutputCaptured(t *testing.T) {
	mgr := internal.NewPTYManager()
	defer mgr.Cleanup()
	tool := &TerminalsTool{Mgr: mgr}

	_, err := tool.Execute(`{"action": "open", "name": "cap1"}`)
	if err != nil {
		t.Fatalf("Open should succeed: %v", err)
	}

	_, err = tool.Execute(`{"action": "send", "sessionId": "cap1", "text": "echo HELLO_TERMINALS"}`)
	if err != nil {
		t.Fatalf("Send should succeed: %v", err)
	}

	time.Sleep(200 * time.Millisecond)

	result, err := tool.Execute(`{"action": "read", "sessionId": "cap1"}`)
	if err != nil {
		t.Fatalf("Read should succeed: %v", err)
	}
	if !strings.Contains(result, "HELLO_TERMINALS") {
		t.Errorf("Read output should contain sentinel, got: %q", result)
	}
}

func TestTerminals_Send_WithoutSession_Errors(t *testing.T) {
	mgr := internal.NewPTYManager()
	defer mgr.Cleanup()
	tool := &TerminalsTool{Mgr: mgr}

	_, err := tool.Execute(`{"action": "send", "text": "echo hi"}`)
	if err == nil {
		t.Error("Send without session should return error")
	}
}

func TestTerminals_Send_HealedRawArgument_DefaultsToSend(t *testing.T) {
	mgr := internal.NewPTYManager()
	defer mgr.Cleanup()
	tool := &TerminalsTool{Mgr: mgr}

	tool.Execute(`{"action": "open", "name": "heal1"}`)

	// A raw (healed) argument arrives as {"text": "..."} without an action.
	result, err := tool.Execute(`{"text": "echo HEALED_OK"}`)
	if err != nil {
		t.Fatalf("Healed send should succeed: %v", err)
	}
	if !strings.Contains(result, "HEALED_OK") {
		t.Errorf("Healed send should execute in the only session, got: %q", result)
	}
}

func TestTerminals_ReadNonexistent_ReturnsError(t *testing.T) {
	mgr := internal.NewPTYManager()
	tool := &TerminalsTool{Mgr: mgr}

	_, err := tool.Execute(`{"action": "read", "sessionId": "no-such-session"}`)
	if err == nil {
		t.Error("Read nonexistent session should return error")
	}
}

func TestTerminals_SendNonexistent_ReturnsError(t *testing.T) {
	mgr := internal.NewPTYManager()
	tool := &TerminalsTool{Mgr: mgr}

	_, err := tool.Execute(`{"action": "send", "sessionId": "no-such-session", "text": "data"}`)
	if err == nil {
		t.Error("Send nonexistent session should return error")
	}
}

func TestTerminals_Close_TerminatesSession(t *testing.T) {
	mgr := internal.NewPTYManager()
	defer mgr.Cleanup()
	tool := &TerminalsTool{Mgr: mgr}

	_, err := tool.Execute(`{"action": "open", "name": "st1"}`)
	if err != nil {
		t.Fatalf("Open should succeed: %v", err)
	}

	result, err := tool.Execute(`{"action": "close", "sessionId": "st1"}`)
	if err != nil {
		t.Fatalf("Close should succeed: %v", err)
	}
	if !strings.Contains(result, "st1") {
		t.Errorf("Close result should contain session ID, got: %q", result)
	}
}

func TestTerminals_CloseNonexistent_ReturnsError(t *testing.T) {
	mgr := internal.NewPTYManager()
	tool := &TerminalsTool{Mgr: mgr}

	_, err := tool.Execute(`{"action": "close", "sessionId": "no-such-session"}`)
	if err == nil {
		t.Error("Close nonexistent session should return error")
	}
}

func TestTerminals_List_ShowsSessions(t *testing.T) {
	mgr := internal.NewPTYManager()
	defer mgr.Cleanup()
	tool := &TerminalsTool{Mgr: mgr}

	tool.Execute(`{"action": "open", "name": "l1"}`)
	tool.Execute(`{"action": "open", "name": "l2"}`)

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

func TestTerminals_List_NoSessions(t *testing.T) {
	mgr := internal.NewPTYManager()
	tool := &TerminalsTool{Mgr: mgr}

	result, err := tool.Execute(`{"action": "list"}`)
	if err != nil {
		t.Fatalf("List should succeed: %v", err)
	}
	if !strings.Contains(result, "No active") {
		t.Errorf("List with no sessions should indicate empty, got: %q", result)
	}
}

func TestTerminals_Signal_InterruptsCommand(t *testing.T) {
	mgr := internal.NewPTYManager()
	defer mgr.Cleanup()
	tool := &TerminalsTool{Mgr: mgr}

	tool.Execute(`{"action": "open", "name": "sig1"}`)
	// Start a long-running foreground job in the shell.
	_, err := tool.Execute(`{"action": "send", "sessionId": "sig1", "text": "sleep 30", "timeout": 1}`)
	if err != nil {
		t.Fatalf("Send should succeed: %v", err)
	}

	result, err := tool.Execute(`{"action": "signal", "sessionId": "sig1", "signal": "SIGINT"}`)
	if err != nil {
		t.Fatalf("Signal should succeed: %v", err)
	}
	if !strings.Contains(result, "SIGINT") {
		t.Errorf("Signal result should mention signal, got: %q", result)
	}
}

func TestTerminals_Signal_SIGKILLRejected(t *testing.T) {
	mgr := internal.NewPTYManager()
	tool := &TerminalsTool{Mgr: mgr}

	_, err := tool.Execute(`{"action": "signal", "sessionId": "s", "signal": "SIGKILL"}`)
	if err == nil {
		t.Fatal("SIGKILL should be rejected")
	}
	te, ok := err.(*internal.ToolError)
	if !ok {
		t.Fatalf("expected ToolError, got %T", err)
	}
	if te.Type != "sigkill_rejected" {
		t.Errorf("type = %q, want sigkill_rejected", te.Type)
	}
}

func TestTerminals_Signal_InvalidSignal_ReturnsError(t *testing.T) {
	mgr := internal.NewPTYManager()
	tool := &TerminalsTool{Mgr: mgr}

	_, err := tool.Execute(`{"action": "signal", "sessionId": "s", "signal": "SIGBOGUS"}`)
	if err == nil {
		t.Error("Invalid signal should return error")
	}
}

func TestTerminals_InvalidAction_ReturnsError(t *testing.T) {
	mgr := internal.NewPTYManager()
	tool := &TerminalsTool{Mgr: mgr}

	_, err := tool.Execute(`{"action": "invalid_action"}`)
	if err == nil {
		t.Error("Invalid action should return error")
	}
}

func TestTerminals_MissingAction_ReturnsError(t *testing.T) {
	mgr := internal.NewPTYManager()
	tool := &TerminalsTool{Mgr: mgr}

	_, err := tool.Execute(`{}`)
	if err == nil {
		t.Error("Missing action should return error")
	}
}

func TestTerminals_InvalidJSON_ReturnsError(t *testing.T) {
	mgr := internal.NewPTYManager()
	tool := &TerminalsTool{Mgr: mgr}

	_, err := tool.Execute("not json")
	if err == nil {
		t.Error("Invalid JSON should return error")
	}
}

// TestTerminals_Read_StripsCarriageReturns: PTY streams carry termios ONLCR
// line endings ("alpha\r\r\n") and progress-style bare '\r' rewrites. Read
// output must contain no carriage returns, and a rewritten line must show its
// final visible state.
func TestTerminals_Read_StripsCarriageReturns(t *testing.T) {
	mgr := internal.NewPTYManager()
	defer mgr.Cleanup()
	tool := &TerminalsTool{Mgr: mgr}

	tool.Execute(`{"action": "open", "name": "cr1"}`)
	// Disable tty echo first so the interactive shell does not echo the typed
	// command text (which would contain the literal "progress 1" sentinel and
	// defeat the overwrite assertion).
	tool.Execute(`{"action": "send", "sessionId": "cr1", "text": "stty -echo"}`)
	time.Sleep(100 * time.Millisecond)
	tool.Execute(`{"action": "send", "sessionId": "cr1", "text": "printf 'alpha\\r\\nbeta\\r\\nprogress 1\\rprogress 2 done\\n'"}`)

	time.Sleep(300 * time.Millisecond)

	result, err := tool.Execute(`{"action": "read", "sessionId": "cr1"}`)
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

// TestTerminals_Read_SanitizesControlBytes: tool output must not leak
// terminal-corrupting control bytes (bell, backspace) into the renderer.
func TestTerminals_Read_SanitizesControlBytes(t *testing.T) {
	mgr := internal.NewPTYManager()
	defer mgr.Cleanup()
	tool := &TerminalsTool{Mgr: mgr}

	tool.Execute(`{"action": "open", "name": "cb1"}`)
	tool.Execute(`{"action": "send", "sessionId": "cb1", "text": "printf 'ding\\a back\\bspace\\n'"}`)

	time.Sleep(300 * time.Millisecond)

	result, err := tool.Execute(`{"action": "read", "sessionId": "cb1"}`)
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

// TestTerminals_Read_OffsetCount_Succeeds verifies the dsh read offset/count
// parameters are accepted end-to-end; precise paging semantics are unit-tested
// at the manager level (TestPTYManager_ReadRange_Paging).
func TestTerminals_Read_OffsetCount_Succeeds(t *testing.T) {
	mgr := internal.NewPTYManager()
	defer mgr.Cleanup()
	tool := &TerminalsTool{Mgr: mgr}

	tool.Execute(`{"action": "open", "name": "pg1"}`)
	tool.Execute(`{"action": "send", "sessionId": "pg1", "text": "echo first_line"}`)
	time.Sleep(100 * time.Millisecond)
	tool.Execute(`{"action": "send", "sessionId": "pg1", "text": "echo second_line"}`)
	time.Sleep(300 * time.Millisecond)

	result, err := tool.Execute(`{"action": "read", "sessionId": "pg1", "count": 1}`)
	if err != nil {
		t.Fatalf("Read with count should succeed: %v", err)
	}
	if result == "" {
		t.Error("Read with count returned empty result")
	}

	older, err := tool.Execute(`{"action": "read", "sessionId": "pg1", "count": 1, "offset": 1}`)
	if err != nil {
		t.Fatalf("Read with offset should succeed: %v", err)
	}
	if older == "" {
		t.Error("Read with offset returned empty result")
	}
}

// TestTerminals_PersistentShell_AcrossCalls verifies the persistent-shell
// mode: state survives between tool calls (cwd changes persist).
func TestTerminals_PersistentShell_AcrossCalls(t *testing.T) {
	mgr := internal.NewPTYManager()
	defer mgr.Cleanup()
	tool := &TerminalsTool{Mgr: mgr}

	tool.Execute(`{"action": "open", "name": "persist1"}`)
	// cd to /tmp in the shell; the state must persist to the next call.
	_, err := tool.Execute(`{"action": "send", "sessionId": "persist1", "text": "cd /tmp && pwd"}`)
	if err != nil {
		t.Fatalf("Send cd should succeed: %v", err)
	}
	time.Sleep(200 * time.Millisecond)

	result, err := tool.Execute(`{"action": "send", "sessionId": "persist1", "text": "pwd"}`)
	if err != nil {
		t.Fatalf("Send pwd should succeed: %v", err)
	}
	if !strings.Contains(result, "/tmp") {
		t.Errorf("Persistent shell should remember cwd, got: %q", result)
	}
}

// TestTerminals_BackgroundSend_ReturnsJobID verifies run_in_background
// returns immediately with a job id instead of waiting for output.
func TestTerminals_BackgroundSend_ReturnsJobID(t *testing.T) {
	mgr := internal.NewPTYManager()
	defer mgr.Cleanup()
	tool := &TerminalsTool{Mgr: mgr}

	tool.Execute(`{"action": "open", "name": "bg1"}`)
	result, err := tool.Execute(`{"action": "send", "sessionId": "bg1", "text": "echo background_job", "run_in_background": true}`)
	if err != nil {
		t.Fatalf("Background send should succeed: %v", err)
	}
	if !strings.Contains(result, "Job ID:") {
		t.Errorf("Background send should return a job id, got: %q", result)
	}
}

// TestTerminals_MultipleSessions_Independent verifies sessions do not share
// state.
func TestTerminals_MultipleSessions_Independent(t *testing.T) {
	mgr := internal.NewPTYManager()
	defer mgr.Cleanup()
	tool := &TerminalsTool{Mgr: mgr}

	tool.Execute(`{"action": "open", "name": "alpha"}`)
	tool.Execute(`{"action": "open", "name": "beta"}`)
	tool.Execute(`{"action": "send", "sessionId": "alpha", "text": "echo session_alpha"}`)
	tool.Execute(`{"action": "send", "sessionId": "beta", "text": "echo session_beta"}`)

	time.Sleep(300 * time.Millisecond)

	r1, _ := tool.Execute(`{"action": "read", "sessionId": "alpha"}`)
	r2, _ := tool.Execute(`{"action": "read", "sessionId": "beta"}`)

	if !strings.Contains(r1, "session_alpha") {
		t.Errorf("Session alpha should contain its output, got: %q", r1)
	}
	if !strings.Contains(r2, "session_beta") {
		t.Errorf("Session beta should contain its output, got: %q", r2)
	}
}

// TestTerminals_Send_SafetyAllowList verifies the preserved terminal safety
// allow-list: blocked commands are rejected before reaching the shell, and
// argument-position uses of blocked names pass.
func TestTerminals_Send_SafetyAllowList(t *testing.T) {
	mgr := internal.NewPTYManager()
	defer mgr.Cleanup()
	tool := &TerminalsTool{
		Mgr:     mgr,
		Blocked: []string{"curl"},
		Bypass:  false,
	}

	tool.Execute(`{"action": "open", "name": "safe1"}`)

	// Blocked command at command position → rejected.
	_, err := tool.Execute(`{"action": "send", "sessionId": "safe1", "text": "curl http://evil.example"}`)
	if err == nil {
		t.Fatal("Blocked command should be rejected")
	}
	te, ok := err.(*internal.ToolError)
	if !ok {
		t.Fatalf("expected ToolError, got %T", err)
	}
	if te.Type != "blocked_command" {
		t.Errorf("type = %q, want blocked_command", te.Type)
	}

	// Blocked name as an argument (not command position) passes.
	_, err = tool.Execute(`{"action": "send", "sessionId": "safe1", "text": "echo curl"}`)
	if err != nil {
		t.Fatalf("Argument-position blocked name should pass: %v", err)
	}
}

// TestTerminals_Send_AllowedListRestricts verifies the allow-list restricts
// commands when configured.
func TestTerminals_Send_AllowedListRestricts(t *testing.T) {
	mgr := internal.NewPTYManager()
	defer mgr.Cleanup()
	tool := &TerminalsTool{
		Mgr:     mgr,
		Allowed: []string{"echo"},
		Bypass:  false,
	}

	tool.Execute(`{"action": "open", "name": "alw1"}`)

	_, err := tool.Execute(`{"action": "send", "sessionId": "alw1", "text": "ls -la"}`)
	if err == nil {
		t.Fatal("Command not in allowed list should be rejected")
	}
	te, ok := err.(*internal.ToolError)
	if !ok {
		t.Fatalf("expected ToolError, got %T", err)
	}
	if te.Type != "command_not_allowed" {
		t.Errorf("type = %q, want command_not_allowed", te.Type)
	}

	_, err = tool.Execute(`{"action": "send", "sessionId": "alw1", "text": "echo ok"}`)
	if err != nil {
		t.Fatalf("Allowed command should pass: %v", err)
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
