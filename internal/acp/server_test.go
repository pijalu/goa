// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package acp

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// TestACPServer_Initialize verifies the initialize handshake.
func TestACPServer_Initialize(t *testing.T) {
	var stdout bytes.Buffer
	stdin := strings.NewReader(
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"clientVersion":"1.0"}}` + "\n",
	)

	server := NewACPServer(stdin, &stdout)
	server.Start()
	<-server.Done()

	var resp JSONRPCResponse
	if err := json.Unmarshal(stdout.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v\noutput: %s", err, stdout.String())
	}

	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	if resp.Result == nil {
		t.Fatal("expected result, got nil")
	}

	var result InitializeResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if result.AgentInfo.Name != "goa" {
		t.Errorf("agent name = %q, want %q", result.AgentInfo.Name, "goa")
	}
	if !result.AgentCapabilities.PromptCapabilities.Image {
		t.Error("expected image capability")
	}
	// List should be non-nil (advertised as supported).
	if result.AgentCapabilities.SessionCapabilities.List == nil {
		t.Error("expected non-nil SessionCapabilities.List")
	}
}

// TestACPServer_SessionNewAndList verifies session creation and listing.
func TestACPServer_SessionNewAndList(t *testing.T) {
	var stdout bytes.Buffer
	stdin := strings.NewReader(
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}` + "\n" +
			`{"jsonrpc":"2.0","id":2,"method":"session/new","params":{"cwd":"/test"}}` + "\n" +
			`{"jsonrpc":"2.0","id":3,"method":"session/list","params":{}}` + "\n",
	)

	server := NewACPServer(stdin, &stdout)
	server.Start()
	<-server.Done()

	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if len(lines) < 3 {
		t.Fatalf("expected at least 3 response lines, got %d", len(lines))
	}

	// Second response should be session/new result.
	var newResp JSONRPCResponse
	json.Unmarshal([]byte(lines[1]), &newResp)
	if newResp.Error != nil {
		t.Fatalf("session/new error: %+v", newResp.Error)
	}

	var newResult NewSessionResult
	json.Unmarshal(newResp.Result, &newResult)
	if newResult.SessionID == "" {
		t.Error("expected non-empty session ID")
	}

	// Third response should be session/list.
	var listResp JSONRPCResponse
	json.Unmarshal([]byte(lines[2]), &listResp)
	if listResp.Error != nil {
		t.Fatalf("session/list error: %+v", listResp.Error)
	}
}

// TestACPServer_UnknownMethod verifies unknown methods return an error.
func TestACPServer_UnknownMethod(t *testing.T) {
	var stdout bytes.Buffer
	stdin := strings.NewReader(
		`{"jsonrpc":"2.0","id":1,"method":"nonexistent","params":{}}` + "\n",
	)

	server := NewACPServer(stdin, &stdout)
	server.Start()
	<-server.Done()

	var resp JSONRPCResponse
	json.Unmarshal(stdout.Bytes(), &resp)
	if resp.Error == nil {
		t.Fatal("expected error for unknown method")
	}
	if resp.Error.Code != ErrMethodNotFound {
		t.Errorf("error code = %d, want %d", resp.Error.Code, ErrMethodNotFound)
	}
}

// TestACPServer_InvalidJSON verifies parse errors.
func TestACPServer_InvalidJSON(t *testing.T) {
	var stdout bytes.Buffer
	stdin := strings.NewReader("not json\n")

	server := NewACPServer(stdin, &stdout)
	server.Start()
	<-server.Done()

	var resp JSONRPCResponse
	json.Unmarshal(stdout.Bytes(), &resp)
	if resp.Error == nil {
		t.Fatal("expected error for invalid JSON")
	}
	if resp.Error.Code != ErrParse {
		t.Errorf("error code = %d, want %d", resp.Error.Code, ErrParse)
	}
}

// TestACPServer_SessionPrompt sends a prompt and verifies a notification.
func TestACPServer_SessionPrompt(t *testing.T) {
	var stdout bytes.Buffer
	reqs := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","id":2,"method":"session/new","params":{"cwd":"/test"}}`,
		`{"jsonrpc":"2.0","id":3,"method":"session/prompt","params":{"sessionId":"acp-1","content":[{"type":"text","text":"hello"}]}}`,
	}
	stdin := strings.NewReader(strings.Join(reqs, "\n") + "\n")

	server := NewACPServer(stdin, &stdout)
	server.Start()
	<-server.Done()

	output := stdout.String()
	if !strings.Contains(output, "Received: hello") {
		t.Errorf("expected 'Received: hello' in output, got: %s", output)
	}
}

// TestACPServer_CancelSession verifies session cancellation.
func TestACPServer_CancelSession(t *testing.T) {
	var stdout bytes.Buffer
	reqs := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","id":2,"method":"session/new","params":{"cwd":"/test"}}`,
		`{"jsonrpc":"2.0","id":3,"method":"session/cancel","params":{"sessionId":"acp-1"}}`,
	}
	stdin := strings.NewReader(strings.Join(reqs, "\n") + "\n")

	server := NewACPServer(stdin, &stdout)
	server.Start()
	<-server.Done()

	// Should not error — cancel on existing session is fine.
	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	var cancelResp JSONRPCResponse
	json.Unmarshal([]byte(lines[2]), &cancelResp)
	if cancelResp.Error != nil {
		t.Fatalf("cancel error: %+v", cancelResp.Error)
	}
}

// TestACPServer_SessionLoadNonExistent verifies error for missing session.
func TestACPServer_SessionLoadNonExistent(t *testing.T) {
	var stdout bytes.Buffer
	reqs := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","id":2,"method":"session/load","params":{"sessionId":"nonexistent"}}`,
	}
	stdin := strings.NewReader(strings.Join(reqs, "\n") + "\n")

	server := NewACPServer(stdin, &stdout)
	server.Start()
	<-server.Done()

	var loadResp JSONRPCResponse
	json.Unmarshal([]byte(strings.Split(stdout.String(), "\n")[1]), &loadResp)
	if loadResp.Error == nil {
		t.Fatal("expected error for non-existent session load")
	}
}
