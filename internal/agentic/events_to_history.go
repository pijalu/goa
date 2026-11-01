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
//
// Completed compaction transactions (bugs.md B44) are honored: a
// compaction_summary event carrying a known strategy plus its replay payload
// replaces the shadowed pre-compaction transcript with the checkpoint the
// live compaction landed (summarize: checkpoint pair; fresh_window: the
// preserved tail; remote_compact: the persisted replacement), so a restored
// session matches the live post-compaction history instead of silently
// growing back to the full pre-compaction size. Legacy triples without the
// replay fields and failed attempts (compaction_end with an error, no
// summary) leave the transcript untouched.
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
	case EventCompactionSummary:
		b.handleCompactionSummary(ev)
	case EventEnd:
		b.flush()
	}
}

// handleCompactionSummary applies a completed compaction transaction to the
// replayed transcript. Only transactions with a known strategy AND their
// replay payload act; anything else (legacy sessions, unknown future
// strategies) is ignored so the pre-B44 restore behavior is preserved.
func (b *historyBuilder) handleCompactionSummary(ev OutputEvent) {
	tx := ev.CompactionTx
	if tx == nil {
		return
	}
	b.flush()
	switch tx.Strategy {
	case string(CompressionSummarize):
		// The checkpoint pair the live compaction landed: the framed summary
		// as the user continuation point, the raw summary as the assistant
		// turn. ShadowedStart is always 0 today (whole history shadowed);
		// anything else is treated as unknown and ignored.
		if tx.Summary == "" || tx.ShadowedStart != 0 {
			return
		}
		b.messages = []Message{
			{Type: Content, Role: User, Content: frameCompactedSummary(tx.Summary)},
			{Type: Content, Role: Assistant, Content: tx.Summary},
		}
	case string(CompressionFreshWindow):
		// The live reset kept the system message plus the chain-safe tail
		// ending at the last user turn; replay rebuilds the same tail (the
		// agent re-injects the system prompt on SetHistory).
		if len(b.messages) == 0 {
			return
		}
		preserve := tx.PreserveTurns
		if preserve <= 0 {
			preserve = 2
		}
		b.messages = append([]Message(nil), b.messages[findCompressionBoundary(b.messages, preserve):]...)
	case string(CompressionRemoteCompact):
		// The server-produced replacement transcript is persisted on the
		// summary event; replay it verbatim.
		if len(tx.Replacement) == 0 {
			return
		}
		b.messages = append([]Message(nil), tx.Replacement...)
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
			ToolName: ev.ToolName, Images: ev.Images,
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
