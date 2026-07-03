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
	var history []Message

	// current assistant message accumulator
	type accum struct {
		content   string
		thinking  string
		toolCalls []ToolCallInfo
	}
	var cur *accum

	flushAccum := func() {
		if cur == nil {
			return
		}
		msg := Message{
			Type:      Content,
			Role:      Assistant,
			Content:   cur.content,
			Thinking:  cur.thinking,
			ToolCalls: cur.toolCalls,
		}
		if msg.Content != "" || msg.Thinking != "" || len(msg.ToolCalls) > 0 {
			history = append(history, msg)
		}
		cur = nil
	}

	ensureAccum := func() {
		if cur == nil {
			cur = &accum{}
		}
	}

	for _, ev := range events {
		switch ev.Type {
		case EventContent:
			switch ev.Role {
			case User:
				flushAccum()
				if ev.Text != "" {
					history = append(history, Message{
						Type: Content, Role: User, Content: ev.Text,
					})
				}

			case Assistant:
				ensureAccum()
				if ev.State == StateThinking {
					cur.thinking += ev.Text
				} else {
					cur.content += ev.Text
				}

			case System:
				// System messages from event replay are skipped; the agent
				// re-injects the system prompt via SetHistory when needed.
			}

		case EventToolCall:
			ensureAccum()
			cur.toolCalls = append(cur.toolCalls, ToolCallInfo{
				ID: ev.ToolCallID, Type: "function",
				Name: ev.ToolName, Arguments: ev.ToolInput,
			})

		case EventToolResult:
			flushAccum()
			if ev.ToolResult != "" || ev.ToolCallID != "" {
				history = append(history, Message{
					Type: Content, Role: ToolRole,
					Content: ev.ToolResult, ToolCallID: ev.ToolCallID,
					ToolName: ev.ToolName,
				})
			}

		case EventEnd:
			flushAccum()

		// EventTokenStats, EventContextStats, EventCompact, EventClear,
		// EventProgress, EventStateChange — all skipped, they carry no
		// conversation content.
		}
	}

	// Flush any trailing message if the event stream lacks an EventEnd.
	flushAccum()

	return history
}
