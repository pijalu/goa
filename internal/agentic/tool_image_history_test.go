// SPDX-License-Identifier: GPL-3.0-or-later

package agentic

import (
	"encoding/json"
	"testing"
)

func TestToolImageAttachmentSurvivesSessionEventRoundTrip(t *testing.T) {
	original := OutputEvent{
		Type:       EventToolResult,
		ToolName:   "read",
		ToolCallID: "call_image",
		ToolResult: "image metadata",
		Images:     []string{"/tmp/screenshot.png"},
	}
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	var restored OutputEvent
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatal(err)
	}
	history := EventsToHistory([]OutputEvent{
		{Type: EventToolCall, ToolName: "read", ToolCallID: "call_image", ToolInput: `{"path":"screenshot.png","include_image":true}`},
		restored,
		{Type: EventEnd},
	})
	if len(history) != 2 {
		t.Fatalf("history length = %d, want 2", len(history))
	}
	if len(history[1].Images) != 1 || history[1].Images[0] != "/tmp/screenshot.png" {
		t.Errorf("restored tool Images = %q, want screenshot path", history[1].Images)
	}
}
