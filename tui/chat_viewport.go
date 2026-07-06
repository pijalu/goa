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
//
// Render uses a per-entry cache so that only changed entries are re-rendered.
// The total frame cache is updated incrementally when the last entry is the
// only dirty one (the common streaming case), and rebuilt from the per-entry
// caches when entries elsewhere change.
type ChatViewport struct {
	*Conversation

	// suppressed hides the viewport during orchestration mode so the
	// persistent AgentContent region can take its place without double-rendering.
	// Set on the command loop via SetSuppressed.
	suppressed bool

	// renderCache holds the concatenated output of the last Render call.
	renderCache struct {
		width int
		lines []string
	}
	// agentFilter, when non-empty, hides every entry whose Meta["agent"]
	// differs, producing a per-agent view (TabAgent) without duplicating the
	// streaming widgets. Empty shows the whole conversation (Conversation tab).
	agentFilter string

	// lastRenderFilter is the filter used to build renderCache; a change forces
	// a full rebuild even when no entry is dirty (filter-only view switch).
	lastRenderFilter string

	// generation increments on every mutation (append, update, invalidate).
	// Render compares it to lastRenderGen: when they match and the cache is
	// valid, it skips the O(n) dirtyIndices scan entirely. This avoids
	// scanning all entries on every frame when only the input editor changes
	// (the common typing scenario).
	generation    int
	lastRenderGen int
}

// NewChatViewport creates a ChatViewport backed by a fresh Conversation.
func NewChatViewport() *ChatViewport {
	return &ChatViewport{Conversation: NewConversation()}
}

// SetAgentFilter scopes the viewport to one agent's blocks (label as stamped
// in Meta["agent"]). An empty label shows the whole conversation. Invalidates
// the render cache. Used by the per-agent TabAgent to isolate a worker's
// output without duplicating widgets.
func (cv *ChatViewport) SetAgentFilter(label string) {
	if cv.agentFilter == label {
		return
	}
	cv.agentFilter = label
	cv.generation++
}

// AgentFilter returns the active per-agent filter label ("" = show all).
func (cv *ChatViewport) AgentFilter() string { return cv.agentFilter }

// SetSuppressed toggles whether the viewport hides itself. While suppressed,
// Render returns nil so the orchestration AgentContent region replaces it.
func (cv *ChatViewport) SetSuppressed(b bool) {
	cv.suppressed = b
	cv.generation++
}

// IsSuppressed reports whether the viewport is currently hidden.
func (cv *ChatViewport) IsSuppressed() bool { return cv.suppressed }

// Render draws every entry's view. Per-entry caches avoid re-rendering
// unchanged entries; the total frame cache is updated incrementally when only
// the last entry changed, which is the common case during streaming.
func (cv *ChatViewport) Render(width int) []string {
	if cv.suppressed {
		return nil
	}
	if width <= 0 {
		return nil
	}
	if width != cv.renderCache.width {
		cv.resetRenderCaches(width)
	}
	// Fast path: when no mutations have occurred since the last render and
	// the frame cache is valid, return it immediately without scanning all
	// entries for dirty indices. The common typing scenario (only the input
	// editor changes) hits this path.
	if cv.generation == cv.lastRenderGen && cv.renderCache.lines != nil {
		return cv.renderCache.lines
	}
	cv.lastRenderGen = cv.generation
	if cv.agentFilter != cv.lastRenderFilter {
		// A filter switch (entering OR leaving a per-agent tab) changes which
		// entries are visible, so bypass the dirty fast paths entirely.
		cv.fullRebuild(width)
		return cv.renderCache.lines
	}
	dirty := cv.dirtyIndices()
	// While a per-agent filter is active, visibility can change per entry, so
	// always fully rebuild instead of using the single-entry fast paths.
	if cv.agentFilter != "" {
		cv.fullRebuild(width)
		return cv.renderCache.lines
	}
	if len(dirty) == 0 && cv.renderCache.lines != nil {
		return cv.renderCache.lines
	}
	if cv.renderCache.lines == nil {
		cv.fullRebuild(width)
		return cv.renderCache.lines
	}
	if len(dirty) == 1 && dirty[0] == len(cv.entries)-1 {
		cv.updateLastEntry(width)
		return cv.renderCache.lines
	}
	cv.fullRebuild(width)
	return cv.renderCache.lines
}

// Invalidate propagates to every entry's view and clears the render caches.
func (cv *ChatViewport) Invalidate() {
	cv.renderCache.width = 0
	cv.renderCache.lines = nil
	cv.generation++
	for i := range cv.entries {
		cv.entries[i].View.Invalidate()
		cv.entries[i].renderedWidth = 0
		cv.entries[i].renderedLines = nil
		cv.entries[i].lineOffset = 0
		cv.entries[i].dirty = true
	}
}

// HandleInput is a no-op: the chat viewport is never focused (input goes to the
// editor / overlays). Implementing it satisfies the Component interface.
func (cv *ChatViewport) HandleInput(string) {}

// Clear removes all entries and invalidates the render cache.
func (cv *ChatViewport) Clear() {
	cv.Conversation.Clear()
	cv.renderCache.width = 0
	cv.renderCache.lines = nil
	cv.generation++
}

// ── Generic Model delegates (composable primitives) ──

// Append adds an entry to the conversation and marks the new entry dirty.
func (cv *ChatViewport) Append(e MessageEntry) int {
	e.dirty = true
	e.renderedWidth = 0
	e.renderedLines = nil
	// Compute lineOffset: total line count of all existing entries.
	// Use the render cache when available (O(1)), fall back to O(n) scan.
	if cv.renderCache.lines != nil {
		e.lineOffset = len(cv.renderCache.lines)
	} else {
		e.lineOffset = 0
		for _, existing := range cv.entries {
			e.lineOffset += len(existing.renderedLines)
		}
	}
	cv.generation++
	return cv.Conversation.Append(e)
}

// UpdateLast applies fn to the most recent entry matching types and marks
// that entry dirty so the next Render only re-renders the changed entry.
func (cv *ChatViewport) UpdateLast(types []ConsoleItemType, fn func(*MessageEntry)) bool {
	wrapped := func(e *MessageEntry) {
		fn(e)
		e.dirty = true
	}
	if cv.Conversation.UpdateLast(types, wrapped) {
		cv.generation++
		return true
	}
	return false
}

// RemoveLast removes the most recent entry matching types and clears the
// frame cache so the next Render rebuilds it.
func (cv *ChatViewport) RemoveLast(types []ConsoleItemType) (MessageEntry, bool) {
	if e, ok := cv.Conversation.RemoveLast(types); ok {
		cv.renderCache.width = 0
		cv.renderCache.lines = nil
		cv.generation++
		return e, true
	}
	return MessageEntry{}, false
}

// resetRenderCaches invalidates every entry's cache and clears the frame cache.
func (cv *ChatViewport) resetRenderCaches(width int) {
	cv.renderCache.width = width
	cv.renderCache.lines = nil
	cv.generation++
	for i := range cv.entries {
		cv.entries[i].renderedWidth = 0
		cv.entries[i].renderedLines = nil
		cv.entries[i].lineOffset = 0
		cv.entries[i].dirty = true
	}
}

// dirtyIndices returns the indices of entries that need to be re-rendered.
func (cv *ChatViewport) dirtyIndices() []int {
	var idx []int
	for i := range cv.entries {
		e := &cv.entries[i]
		if e.renderedWidth != cv.renderCache.width || e.dirty || e.renderedLines == nil {
			idx = append(idx, i)
		}
	}
	return idx
}

// fullRebuild re-renders all dirty entries and concatenates the per-entry
// caches into the frame cache. Also recomputes lineOffsets for all entries so
// that updateLastEntry can find the replacement range in O(1).
func (cv *ChatViewport) fullRebuild(width int) {
	cv.renderCache.width = width
	cv.lastRenderFilter = cv.agentFilter
	cv.renderCache.lines = cv.renderCache.lines[:0]
	offset := 0
	for i := range cv.entries {
		e := &cv.entries[i]
		if cv.agentFilter != "" {
			agent := ""
			if e.Data.Meta != nil {
				agent = e.Data.Meta["agent"]
			}
			if agent != cv.agentFilter {
				continue
			}
		}
		if e.renderedWidth != width || e.dirty || e.renderedLines == nil {
			e.renderedLines = e.View.Render(width)
			e.renderedWidth = width
			e.dirty = false
		}
		e.lineOffset = offset
		cv.renderCache.lines = append(cv.renderCache.lines, e.renderedLines...)
		offset += len(e.renderedLines)
	}
}

// updateLastEntry re-renders the last entry and replaces its block in the
// frame cache. This is the fast path for streaming appends and updates.
func (cv *ChatViewport) updateLastEntry(width int) {
	idx := len(cv.entries) - 1
	e := &cv.entries[idx]
	newLines := e.View.Render(width)

	start := e.lineOffset
	cv.renderCache.lines = cv.renderCache.lines[:start]
	cv.renderCache.lines = append(cv.renderCache.lines, newLines...)

	e.renderedLines = newLines
	e.renderedWidth = width
	e.dirty = false
	cv.renderCache.width = width
}


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

// AddClarifyCard appends a clarification card (from the ask_user_question tool)
// into the conversation viewport. The card is display-only; the answer is
// captured on the main input line by the host.
func (cv *ChatViewport) AddClarifyCard(card *ClarifyCard) {
	if card == nil {
		return
	}
	cv.Append(MessageEntry{Data: MessageData{Type: -1}, View: card})
}

// AddToolExecution adds an interactive tool component and returns it.
func (cv *ChatViewport) AddToolExecution(name, argsJSON string) *ToolExecutionComponent {
	tc := NewToolExecution(name, FormatToolArgs(name, argsJSON))
	tc.SetArgsJSON(argsJSON)
	tc.SetOnInvalidate(func() {
		for i := range cv.entries {
			if cv.entries[i].View == tc {
				cv.entries[i].dirty = true
				cv.generation++
				return
			}
		}
	})
	cv.Append(MessageEntry{Data: MessageData{Type: ConsoleToolCall, Text: name}, View: tc})
	return tc
}

// AddAgentThinkingBlock appends a thinking block labeled with the agent's
// display name. Used by the orchestrator conversation path so each agent's
// thinking is rendered in its own distinct, in-place-updating block.
func (cv *ChatViewport) AddAgentThinkingBlock(label, text string, expanded bool) *thinkingBlock {
	comp := newThinkingBlock(text)
	comp.expanded = expanded
	comp.agentLabel = label
	cv.Append(MessageEntry{
		Data: MessageData{Type: ConsoleThinkingBlock, Text: text, Meta: map[string]string{"agent": label}},
		View: comp,
	})
	return comp
}

// UpdateAgentThinking updates the most recent agent-labeled thinking block for
// label with the accumulated text. Returns true if a matching block was found.
func (cv *ChatViewport) UpdateAgentThinking(label, text string) bool {
	idx := cv.lastAgentEntryIndex(label, ConsoleThinkingBlock)
	if idx < 0 {
		return false
	}
	e := &cv.entries[idx]
	e.Data.Text = text
	if tb, ok := e.View.(*thinkingBlock); ok {
		tb.SetText(text)
	}
	e.dirty = true
	cv.generation++
	return true
}

// AddAgentContent appends an assistant message from a specific agent.
func (cv *ChatViewport) AddAgentContent(label, text string) Component {
	msg := newAgentMessage(text, label)
	cv.Append(MessageEntry{
		Data: MessageData{Type: ConsoleAgentMessage, Text: text, Meta: map[string]string{"agent": label}},
		View: msg,
	})
	return msg
}

// UpdateAgentContent updates the most recent agent-labeled content block for
// label with the accumulated text. Returns true if a matching block was found.
func (cv *ChatViewport) UpdateAgentContent(label, text string) bool {
	idx := cv.lastAgentEntryIndex(label, ConsoleAgentMessage)
	if idx < 0 {
		return false
	}
	e := &cv.entries[idx]
	e.Data.Text = text
	setViewText(e.View, text)
	e.dirty = true
	cv.generation++
	return true
}

// lastAgentEntryIndex returns the index of the most recent entry whose meta
// agent matches label and whose type is one of types (or any type if types is
// empty).
func (cv *ChatViewport) lastAgentEntryIndex(label string, types ...ConsoleItemType) int {
	for i := len(cv.entries) - 1; i >= 0; i-- {
		if e := cv.entries[i]; e.Data.Meta != nil && e.Data.Meta["agent"] == label {
			if len(types) == 0 {
				return i
			}
			for _, t := range types {
				if e.Data.Type == t {
					return i
				}
			}
		}
	}
	return -1
}

// AddAgentToolExecution adds an agent-labeled tool widget and returns it.
func (cv *ChatViewport) AddAgentToolExecution(label, name, argsJSON string) *ToolExecutionComponent {
	tc := cv.AddToolExecution(name, argsJSON)
	tc.SetAgentLabel(label)
	// Stamp the meta entry so later updates can attribute this tool to the agent.
	if last, ok := cv.Conversation.LastWhere(func(e MessageEntry) bool {
		_, is := e.View.(*ToolExecutionComponent)
		return is
	}); ok {
		if last.Data.Meta == nil {
			last.Data.Meta = map[string]string{"agent": label}
		} else {
			last.Data.Meta["agent"] = label
		}
	}
	return tc
}

// InvalidateRunningToolWidgets marks tool widgets that are still running as
// dirty so the next render re-renders them. Used by the status spinner to
// keep the shared animation frame in sync across the chat viewport.
func (cv *ChatViewport) InvalidateRunningToolWidgets() {
	for i := range cv.entries {
		if tc, ok := cv.entries[i].View.(*ToolExecutionComponent); ok && tc.Status() == ToolRunning {
			tc.updateBox()
			tc.Invalidate()
			cv.entries[i].dirty = true
			cv.generation++
		}
	}
}

// ── Mutation primitives ──

// RemoveLastMessage removes and returns the last message's view (any type).
func (cv *ChatViewport) RemoveLastMessage() Component {
	e, ok := cv.RemoveLast(nil) // use override that invalidates cache
	if !ok {
		return nil
	}
	return e.View
}

// RemoveLastMessageOfType removes the most recent message only if it matches one
// of types. Used to clean up partial assistant/thinking blocks after cancel.
func (cv *ChatViewport) RemoveLastMessageOfType(types ...ConsoleItemType) bool {
	_, ok := cv.RemoveLast(types) // use override that invalidates cache
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
