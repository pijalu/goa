package tui

import "testing"

func TestOrchestratorContentStreamingDoesNotRepeat(t *testing.T) {
	cv := NewChatViewport()

	// Simulate orchestrator agent label and thinking/content deltas.
	label := "orchestrator"

	// Add thinking block.
	cv.AddAgentThinkingBlock(label, "The user wants...", true)
	cv.UpdateAgentThinking(label, "The user wants me to act as an orchestrator agent.")

	// Add first content delta.
	text1 := "As the orchestrator, my first step is to understand the scope of the request."
	cv.AddAgentContent(label, text1)
	cv.UpdateAgentContent(label, text1+" The objective is to provide a clear summary.")

	// Continue with more deltas.
	fullText := text1 + " The objective is to provide a clear summary of this project."
	cv.UpdateAgentContent(label, fullText)

	// Count how many agent message entries exist.
	msgs := cv.Messages()
	agentMsgCount := 0
	for _, m := range msgs {
		if m.Type == ConsoleAgentMessage {
			agentMsgCount++
		}
	}
	if agentMsgCount != 1 {
		t.Errorf("expected exactly 1 agent message entry, got %d", agentMsgCount)
	}
}
