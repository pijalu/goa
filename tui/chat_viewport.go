// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"fmt"
	"strings"

	"github.com/pijalu/goa/internal/ansi"
)

// withSpacers wraps content with a leading and trailing spacer line.
// If bgHex is non-empty, the spacer lines are styled with that background color
// using padToWidthStyled (full-width background padding).
func withSpacers(lines []string, width int, bgHex string) []string {
	result := make([]string, 0, len(lines)+2)
	if bgHex != "" {
		bgAnsi := ansi.Bg(bgHex)
		result = append(result, padToWidthStyled("", width, bgAnsi))
	} else {
		result = append(result, "")
	}
	result = append(result, lines...)
	if bgHex != "" {
		bgAnsi := ansi.Bg(bgHex)
		result = append(result, padToWidthStyled("", width, bgAnsi))
	} else {
		result = append(result, "")
	}
	return result
}

// ChatMessage holds the data for a single chat message.
// Each message type gets rendered as a separate Component child of ChatViewport.
type ChatMessage struct {
	Type    ConsoleItemType
	Content string
	Meta    map[string]string
}

// ChatViewport is the View over a Conversation (the Model). It embeds the
// Conversation and exposes:
//   - generic, composable primitives (Append / UpdateLast / RemoveLast /
//     ForEach / Snapshot / LastView / LastWhere) — the Model API;
//   - thin typed factory helpers (AddUserMessage, AddAssistantMessage, …) that
//     compose a factory + Append, so new message kinds extend the system
//     without modifying this type (Open/Closed);
//   - Component rendering (Render / Invalidate / HandleInput).
//
// Model and View stay in sync by construction: every mutator writes a single
// MessageEntry (Data + View) through the Model.
type ChatViewport struct {
	*Conversation
}

// NewChatViewport creates a ChatViewport backed by a fresh Conversation.
func NewChatViewport() *ChatViewport {
	return &ChatViewport{Conversation: NewConversation()}
}

// Render draws every entry's view. Viewport clipping is handled by the
// Compositor handles viewport clipping.
func (cv *ChatViewport) Render(width int) []string {
	if width <= 0 {
		return nil
	}
	var allLines []string
	cv.ForEach(func(e MessageEntry) {
		if cl := e.View.Render(width); cl != nil {
			allLines = append(allLines, cl...)
		}
	})
	return allLines
}

// Invalidate propagates to every entry's view.
func (cv *ChatViewport) Invalidate() {
	cv.ForEach(func(e MessageEntry) { e.View.Invalidate() })
}

// HandleInput is a no-op: the chat viewport is never focused (input goes to the
// editor / overlays). Implementing it satisfies the Component interface.
func (cv *ChatViewport) HandleInput(string) {}

// ── Generic Model delegates (composable primitives) ──

// Append adds an entry to the conversation (single write path for Model+View).
func (cv *ChatViewport) Append(e MessageEntry) int { return cv.Conversation.Append(e) }

// Snapshot returns the pure-data view of the conversation for agents/controllers.
func (cv *ChatViewport) Snapshot() []MessageData { return cv.Conversation.Snapshot() }

// Children returns the views of all entries in order (read accessor).
func (cv *ChatViewport) Children() []Component {
	var views []Component
	cv.ForEach(func(e MessageEntry) { views = append(views, e.View) })
	return views
}

// ── Typed factory helpers (compose factory + Append) ──

// AddMessage appends a message built from a ChatMessage (legacy data shape).
func (cv *ChatViewport) AddMessage(msg *ChatMessage) {
	comp := cv.buildMessageComponent(msg)
	switch msg.Type {
	case ConsoleCompanionMessage:
		comp = &gutteredComponent{inner: comp, color: "#a371f7", kind: "companion"}
	case ConsoleCompanionThinkingBlock:
		comp = &gutteredComponent{inner: comp, color: "#6e7681", kind: "companion_thinking"}
	}
	cv.Append(MessageEntry{
		Data: MessageData{Type: msg.Type, Text: msg.Content, Meta: msg.Meta},
		View: comp,
	})
}

// AddUserMessage adds a user message (blue background, bright text).
func (cv *ChatViewport) AddUserMessage(text string) {
	cv.Append(MessageEntry{Data: MessageData{Type: ConsoleUserMessage, Text: text}, View: newUserMessage(text)})
}

// AddAssistantMessage adds an assistant message (markdown).
func (cv *ChatViewport) AddAssistantMessage(text string) {
	cv.Append(MessageEntry{Data: MessageData{Type: ConsoleAssistantMessage, Text: text}, View: newAssistantMessage(text)})
}

// AddSystemMessage adds a dim system message inside a bordered panel.
func (cv *ChatViewport) AddSystemMessage(text string) {
	cv.Append(MessageEntry{Data: MessageData{Type: ConsoleSystemMessage, Text: text}, View: newSystemMessage(text)})
}

// AddInfoMessage adds a plain informational message (no box/background).
func (cv *ChatViewport) AddInfoMessage(text string) {
	cv.Append(MessageEntry{Data: MessageData{Type: ConsoleInfoMessage, Text: text}, View: newInfoMessage(text)})
}

// AddThinkingBlock adds a thinking/reasoning block.
func (cv *ChatViewport) AddThinkingBlock(text string, expanded bool) {
	comp := newThinkingBlock(text)
	comp.expanded = expanded
	cv.Append(MessageEntry{Data: MessageData{Type: ConsoleThinkingBlock, Text: text}, View: comp})
}

// AddSystemMessagePreformatted adds a system message rendered as plain text
// line-by-line, skipping markdown parsing entirely.
func (cv *ChatViewport) AddSystemMessagePreformatted(text string) {
	cv.Append(MessageEntry{Data: MessageData{Type: ConsoleSystemMessage, Text: text}, View: newSystemMessagePreformatted(text)})
}

// AddToolCall adds a tool-call component (amber).
func (cv *ChatViewport) AddToolCall(name, args string) {
	content := fmt.Sprintf("◉ %s %s", name, args)
	cv.Append(MessageEntry{Data: MessageData{Type: ConsoleToolCall, Text: content}, View: newToolCall(content)})
}

// AddToolResult adds a tool-result component.
func (cv *ChatViewport) AddToolResult(text string) {
	cv.Append(MessageEntry{Data: MessageData{Type: ConsoleToolResult, Text: text}, View: newToolResult(text)})
}

// AddAgentMessage is defined in agent_message.go (factory + AddMessage).

// AddFlashMessage adds a transient flash (⚡ …). When the last entry is already
// a system flash of the same kind, it is updated in place instead of stacking.
func (cv *ChatViewport) AddFlashMessage(text string) {
	kind := flashKind(text)
	if kind != "" {
		if updated := cv.UpdateLast([]ConsoleItemType{ConsoleSystemMessage}, func(e *MessageEntry) {
			if sm, ok := e.View.(*systemMessage); ok && flashKind(e.Data.Text) == kind {
				sm.SetText(text)
				e.Data.Text = text
				return
			}
			// Mismatched kind: signal non-update by leaving Data untouched.
		}); updated {
			// Verify the kind actually matched; otherwise fall through to append.
			if last, ok := cv.Conversation.LastWhere(func(e MessageEntry) bool {
				return e.Data.Type == ConsoleSystemMessage && flashKind(e.Data.Text) == kind
			}); ok && last.Data.Text == text {
				return
			}
		}
	}
	cv.AddSystemMessage(text)
}

// flashKind returns the dedup key for a flash message.
func flashKind(text string) string {
	if text == "" || []rune(text)[0] != '⚡' {
		return ""
	}
	idx := strings.Index(text, ":")
	if idx < 0 {
		return ""
	}
	return strings.TrimRight(text[:idx], " ")
}

// AddComponent adds an arbitrary Component (e.g. goal markers) as a raw entry.
func (cv *ChatViewport) AddComponent(comp Component) {
	cv.Append(MessageEntry{Data: MessageData{Type: -1}, View: comp})
}

// AddToolExecution adds an interactive ToolExecutionComponent and returns it.
func (cv *ChatViewport) AddToolExecution(name, argsJSON string) *ToolExecutionComponent {
	tc := NewToolExecution(name, FormatToolArgs(name, argsJSON))
	tc.SetArgsJSON(argsJSON)
	cv.Append(MessageEntry{Data: MessageData{Type: ConsoleToolCall, Text: name}, View: tc})
	return tc
}

// ── Mutation primitives ──

// RemoveLastMessage removes and returns the last message's view (any type).
func (cv *ChatViewport) RemoveLastMessage() Component {
	e, ok := cv.Conversation.RemoveLast(nil)
	if !ok {
		return nil
	}
	return e.View
}

// RemoveLastMessageOfType removes the most recent message only if it matches one
// of types. Used to clean up partial assistant/thinking blocks after cancel.
func (cv *ChatViewport) RemoveLastMessageOfType(types ...ConsoleItemType) bool {
	_, ok := cv.Conversation.RemoveLast(types)
	return ok
}

// SetLastCompanionDone marks the most recent companion message as done/collapsed.
func (cv *ChatViewport) SetLastCompanionDone() {
	v := cv.LastView([]ConsoleItemType{ConsoleCompanionMessage})
	if g, ok := v.(*gutteredComponent); ok && g.kind == "companion" {
		if c, ok := g.inner.(*collapsibleComponent); ok {
			c.SetDone()
		}
	}
}

// LastAssistantText returns the most recent assistant message text (/copy).
func (cv *ChatViewport) LastAssistantText() string { return cv.Conversation.LastAssistantText() }

// UpdateLastMessage replaces the content of the last message matching msgType.
// Used for streaming: the single write path updates both Model data and View.
func (cv *ChatViewport) UpdateLastMessage(text string, msgType ConsoleItemType) {
	cv.UpdateLast([]ConsoleItemType{msgType}, func(e *MessageEntry) {
		e.Data.Text = text
		setViewText(e.View, text)
	})
}

// setViewText updates a view's text via the SetText interface. Using the
// interface (not a per-type switch) is Open/Closed: any present or future
// view that implements SetText is handled without modifying this function.
func setViewText(view Component, text string) {
	if s, ok := view.(interface{ SetText(string) }); ok {
		s.SetText(text)
	}
}

// Messages returns the conversation as ChatMessage objects (legacy shape),
// fulfilling the prior API from the Model snapshot.
func (cv *ChatViewport) Messages() []*ChatMessage {
	snap := cv.Snapshot()
	out := make([]*ChatMessage, len(snap))
	for i, d := range snap {
		out[i] = &ChatMessage{Type: d.Type, Content: d.Text, Meta: d.Meta}
	}
	return out
}

// buildMessageComponent creates the right Component for each message type.
func (cv *ChatViewport) buildMessageComponent(msg *ChatMessage) Component {
	switch msg.Type {
	case ConsoleUserMessage:
		return newUserMessage(msg.Content)
	case ConsoleAssistantMessage:
		return newAssistantMessage(msg.Content)
	case ConsoleCompanionMessage:
		return newCollapsibleComponent("companion", msg.Content)
	case ConsoleCompanionThinkingBlock:
		return newCompanionThinkingBlock(msg.Content)
	case ConsoleSystemMessage:
		return newSystemMessage(msg.Content)
	case ConsoleInfoMessage:
		return newInfoMessage(msg.Content)
	case ConsoleThinkingBlock:
		return newThinkingBlock(msg.Content)
	case ConsoleToolCall:
		return newToolCall(msg.Content)
	case ConsoleToolResult:
		return newToolResult(msg.Content)
	case ConsoleAgentMessage:
		agent := ""
		if msg.Meta != nil {
			agent = msg.Meta["agent"]
		}
		return newAgentMessage(msg.Content, agent)
	default:
		return NewText(msg.Content, 0, 0)
	}
}
