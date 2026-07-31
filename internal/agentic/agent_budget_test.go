// SPDX-License-Identifier: GPL-3.0-or-later

package agentic

import (
	"testing"

	"github.com/pijalu/goa/internal/agentic/provider"
)

func TestIsGuardrailResult(t *testing.T) {
	tests := []struct {
		name string
		text string
		want bool
	}{
		{"budget exceeded", "[goa-system] Tool call budget exceeded", true},
		{"duplicate hint", "[goa-system] This exact tool call (same tool with same arguments) was already executed this turn", true},
		{"loop guardrail", "[goa-system] Loop guardrail: repeated too many times", true},
		{"real tool result", "written", false},
		{"error result", "Error: file not found", false},
		{"whitespace budget", "  [goa-system] Tool call budget exceeded  ", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsGuardrailResult(tt.text); got != tt.want {
				t.Errorf("IsGuardrailResult(%q) = %v, want %v", tt.text, got, tt.want)
			}
		})
	}
}

// TestSynthesizeAssistantBuffer_PromotesThinkingClearsThinkingField verifies
// that when content is empty but thinking is present (e.g., DeepSeek sends
// reasoning_content but no content before a tool call), the thinking is
// promoted to content AND the thinking field is cleared. Without clearing,
// migrateMessage creates BOTH a text block and a thinking block with the same
// text — the OpenAI completions protocol then maps those to both `content` and
// `reasoning_content` in the API request, so DeepSeek sees its reasoning echoed
// as regular content and re-produces it, causing visible duplication.
func TestSynthesizeAssistantBuffer_PromotesThinkingClearsThinkingField(t *testing.T) {
	agent := NewAgent(Config{
		SystemPrompt: "test",
		Logger:       NewLogger(Error),
	})

	// Simulate a thinking-only round (DeepSeek reasoning_content, no content).
	agent.thinkingBuf.WriteString("I need to check the test file.")
	// contentBuf stays empty.

	msg := agent.synthesizeAssistantBuffer()

	// Content should be promoted from thinking.
	if msg.Content != "I need to check the test file." {
		t.Errorf("expected promoted content, got %q", msg.Content)
	}

	// Thinking MUST be cleared to prevent migrateMessage from creating a
	// duplicate thinking block alongside the promoted text block.
	if msg.Thinking != "" {
		t.Errorf("expected thinking cleared after promotion, got %q", msg.Thinking)
	}

	// Verify migrateMessage produces only ONE block with the text (no
	// duplicate thinking block).
	pm := migrateMessage(msg)
	var textBlocks, thinkingBlocks int
	for _, b := range pm.Content {
		switch b.Type {
		case provider.ContentBlockText:
			textBlocks++
		case provider.ContentBlockThinking:
			thinkingBlocks++
		}
	}
	if textBlocks != 1 {
		t.Errorf("expected exactly 1 text block, got %d", textBlocks)
	}
	if thinkingBlocks != 0 {
		t.Errorf("expected 0 thinking blocks (cleared on promotion), got %d", thinkingBlocks)
	}
}

// TestSynthesizeAssistantBuffer_KeepsThinkingWhenContentExists verifies that
// when both content and thinking are present, thinking is NOT cleared — only
// the promotion path (empty content) clears it.
func TestSynthesizeAssistantBuffer_KeepsThinkingWhenContentExists(t *testing.T) {
	agent := NewAgent(Config{
		SystemPrompt: "test",
		Logger:       NewLogger(Error),
	})

	agent.contentBuf.WriteString("Here is the answer.")
	agent.thinkingBuf.WriteString("Let me think about this.")

	msg := agent.synthesizeAssistantBuffer()

	if msg.Content != "Here is the answer." {
		t.Errorf("expected original content, got %q", msg.Content)
	}
	if msg.Thinking != "Let me think about this." {
		t.Errorf("expected thinking preserved, got %q", msg.Thinking)
	}
}
