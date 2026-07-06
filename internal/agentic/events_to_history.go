// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

// EventsToHistory converts a sequence of OutputEvents (as persisted in session
// JSONL files) into a conversation history suitable for SetHistory. Streaming
// deltas are collapsed into complete messages; system events, progress events,
// and other non-message event types are skipped.
//
// The reconstruction handles these patterns:
//   - User messages become Message{Role: User, Type: Content}
//   - Assistant content+thinking deltas are accumulated into one Message per turn
//   - Tool calls are embedded in the preceding assistant message's ToolCalls field
//   - Tool results become separate Message{Role: ToolRole} entries
//   - EventEnd flushes any pending assistant message
func EventsToHistory(events []OutputEvent) []Message {
	history := &historyBuilder{}
	for _, ev := range events {
		history.handleEvent(ev)
	}
	history.flush()
	return history.messages
}

// historyBuilder accumulates OutputEvents into a Message history.
type historyBuilder struct {
	messages []Message
	cur      *messageAccum
}

// messageAccum holds a partially built assistant message.
type messageAccum struct {
	content   string
	thinking  string
	toolCalls []ToolCallInfo
}

func (b *historyBuilder) handleEvent(ev OutputEvent) {
	switch ev.Type {
	case EventContent:
		b.handleContent(ev)
	case EventToolCall:
		b.handleToolCall(ev)
	case EventToolResult:
		b.handleToolResult(ev)
	case EventEnd:
		b.flush()
	}
}

func (b *historyBuilder) handleContent(ev OutputEvent) {
	switch ev.Role {
	case User:
		b.flush()
		if ev.Text != "" {
			b.messages = append(b.messages, Message{
				Type: Content, Role: User, Content: ev.Text,
			})
		}
	case Assistant:
		b.ensureAccum()
		if ev.State == StateThinking {
			b.cur.thinking += ev.Text
		} else {
			b.cur.content += ev.Text
		}
	case System:
		// System messages from event replay are skipped; the agent
		// re-injects the system prompt via SetHistory when needed.
	}
}

func (b *historyBuilder) handleToolCall(ev OutputEvent) {
	b.ensureAccum()
	b.cur.toolCalls = append(b.cur.toolCalls, ToolCallInfo{
		ID: ev.ToolCallID, Type: "function",
		Name: ev.ToolName, Arguments: ev.ToolInput,
	})
}

func (b *historyBuilder) handleToolResult(ev OutputEvent) {
	b.flush()
	if ev.ToolResult != "" || ev.ToolCallID != "" {
		b.messages = append(b.messages, Message{
			Type: Content, Role: ToolRole,
			Content: ev.ToolResult, ToolCallID: ev.ToolCallID,
			ToolName: ev.ToolName,
		})
	}
}

func (b *historyBuilder) ensureAccum() {
	if b.cur == nil {
		b.cur = &messageAccum{}
	}
}

func (b *historyBuilder) flush() {
	if b.cur == nil {
		return
	}
	msg := Message{
		Type:      Content,
		Role:      Assistant,
		Content:   b.cur.content,
		Thinking:  b.cur.thinking,
		ToolCalls: b.cur.toolCalls,
	}
	if msg.Content != "" || msg.Thinking != "" || len(msg.ToolCalls) > 0 {
		b.messages = append(b.messages, msg)
	}
	b.cur = nil
}
