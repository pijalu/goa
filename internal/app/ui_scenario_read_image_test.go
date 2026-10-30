// SPDX-License-Identifier: GPL-3.0-or-later

package app

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/agentic"
)

func TestFilmstripReadToolResultImageMetadata(t *testing.T) {
	t.Run("text", func(t *testing.T) {
		scenario := newUIScenario(t, 100, 20)
		scenario.chat.SetToolsConfig(true, 10, true)
		scenario.apply(&agentic.OutputEvent{
			Type:       agentic.EventToolCall,
			State:      agentic.StateToolCall,
			ToolName:   "read",
			ToolCallID: "read-text",
			ToolInput:  `{"path":"/tmp/note.txt"}`,
		})
		scenario.apply(&agentic.OutputEvent{
			Type:       agentic.EventToolResult,
			State:      agentic.StateToolResult,
			ToolName:   "read",
			ToolCallID: "read-text",
			Text:       "1: hello\n",
		})
		frame := scenario.filmstrip().Render()
		t.Logf("text Read filmstrip:\n%s", frame)
		if !strings.Contains(frame, "read /tmp/note.txt") {
			t.Fatalf("filmstrip missing Read call:\n%s", frame)
		}
		body := renderToolBody(t, scenario)
		t.Logf("expanded text Read body:\n%s", body)
		if !strings.Contains(body, "1: hello") {
			t.Fatalf("Read body changed:\n%s", body)
		}
	})

	t.Run("image", func(t *testing.T) {
		scenario := newUIScenario(t, 100, 20)
		scenario.chat.SetToolsConfig(true, 10, true)
		scenario.apply(&agentic.OutputEvent{
			Type:       agentic.EventToolCall,
			State:      agentic.StateToolCall,
			ToolName:   "read",
			ToolCallID: "read-image",
			ToolInput:  `{"path":"/tmp/pixel.png","include_image":true}`,
		})
		scenario.apply(&agentic.OutputEvent{
			Type:       agentic.EventToolResult,
			State:      agentic.StateToolResult,
			ToolName:   "read",
			ToolCallID: "read-image",
			Text:       "Image content\nMIME type: image/png\nSize: 4 bytes",
			Images:     []string{"/tmp/pixel.png"},
		})
		frame := scenario.filmstrip().Render()
		t.Logf("image Read filmstrip:\n%s", frame)
		if !strings.Contains(frame, "read /tmp/pixel.png") {
			t.Fatalf("filmstrip missing Read call:\n%s", frame)
		}
		body := renderToolBody(t, scenario)
		t.Logf("expanded image Read body:\n%s", body)
		for _, want := range []string{"Image content", "MIME type: image/png", "Size: 4 bytes"} {
			if !strings.Contains(body, want) {
				t.Fatalf("Read body missing %q:\n%s", want, body)
			}
		}
		if strings.Contains(body, "iVBORw0KGgo") {
			t.Fatalf("Read body rendered encoded image bytes:\n%s", body)
		}
	})
}
