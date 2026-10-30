// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"testing"

	"github.com/pijalu/goa/internal/agentic"
)

func TestSessionStoreToolImageAttachmentRoundTrip(t *testing.T) {
	store := NewSessionStore(t.TempDir())
	sessionID := store.StartSession()
	store.WriteEvent(agentic.OutputEvent{
		Type: agentic.EventToolCall, ToolName: "read", ToolCallID: "call_image",
		ToolInput: `{"path":"screenshot.png","include_image":true}`,
	})
	store.WriteEvent(agentic.OutputEvent{
		Type: agentic.EventToolResult, ToolName: "read", ToolCallID: "call_image",
		ToolResult: "image metadata", Images: []string{"/tmp/screenshot.png"},
	})
	store.WriteEvent(agentic.OutputEvent{Type: agentic.EventEnd})
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	events, err := store.LoadSession(sessionID)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	history := agentic.EventsToHistory(events)
	if len(history) != 2 {
		t.Fatalf("restored history length = %d, want assistant call and tool result", len(history))
	}
	result := history[1]
	if result.Role != agentic.ToolRole || result.ToolCallID != "call_image" {
		t.Fatalf("restored tool result identity = %#v", result)
	}
	if len(result.Images) != 1 || result.Images[0] != "/tmp/screenshot.png" {
		t.Errorf("restored Images = %q, want screenshot path", result.Images)
	}
}
