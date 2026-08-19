// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"encoding/json"
	"fmt"
	"strings"
)

func (cv *ChatViewport) Snapshot() []MessageData { return cv.Conversation.Snapshot() }

// Children returns the views of all entries in order (read accessor).
func (cv *ChatViewport) Children() []Component {
	var views []Component
	cv.ForEach(func(e MessageEntry) { views = append(views, e.View) })
	return views
}

// IsScrolledOff reports whether component c's rendered rows lie entirely
// above the currently visible window (scrolled into terminal scrollback).
// The compositor's scroll watermark never repaints committed rows
// ("canvas rows are immutable"), so a later state change of c is INVISIBLE
// on screen — the symptom behind Issue 6 (a tool completing after
// its running-state rows scrolled off leaves a frozen "◉" ghost). The app
// uses this to append a compact completion echo for such tools.
//
// The visible window is the compositor's transcript band: terminal height
// (viewportH) minus the fixed bottom chrome (bottomChromeH). It must NOT be
// derived from the layout-allocated height: that budget also excludes the
// header/mascot stacked above the transcript, which SCROLLS with the
// transcript and therefore does not shrink its visible band. Using the
// budget over-reports by the header height and appends spurious echoes for
// tools whose completion is plainly visible on screen. Unknown geometry
// (no layout pass, never rendered, stale offset) reports false — never a
// spurious echo.
func (cv *ChatViewport) IsScrolledOff(c Component) bool {
	if c == nil {
		return false
	}
	visibleH := cv.viewportH - cv.bottomChromeH
	if visibleH <= 0 {
		return false // no layout geometry yet: never a spurious echo
	}
	total := len(cv.renderCache.lines)
	if total <= visibleH {
		return false // the whole transcript fits the visible band
	}
	visibleStart := total - visibleH
	for i := range cv.entries {
		e := &cv.entries[i]
		if e.View != c {
			continue
		}
		if e.renderedLines == nil || e.lineOffset < 0 || e.lineOffset > total {
			return false // unknown geometry: no spurious echo
		}
		return e.lineOffset+len(e.renderedLines) <= visibleStart
	}
	return false
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
	switch msg.Type {
	case ConsoleAssistantMessage, ConsoleThinkingBlock, ConsoleAgentMessage,
		ConsoleCompanionMessage, ConsoleCompanionThinkingBlock:
	default:
	}
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
// If argsJSON contains incomplete/partial JSON (during streaming), args
// parsing is skipped but the tool name/header are still set.
func (cv *ChatViewport) AddToolExecution(name, argsJSON string) *ToolExecutionComponent {
	tc := NewToolExecution(name, FormatToolArgs(name, argsJSON))
	// Attempt to parse args; partial JSON during streaming will fail silently.
	if err := json.Unmarshal([]byte(argsJSON), &tc.args); err != nil {
		// Partial/incomplete args during streaming: keep args nil,
		// the renderer will handle ArgsComplete=false via RenderContext.
		tc.argsComplete = false
	} else {
		// Centralized transition: stamps the "waiting" clock (Bug W).
		tc.markArgsComplete()
	}
	tc.SetOnInvalidate(func() {
		for i := range cv.entries {
			if cv.entries[i].View == tc {
				cv.entries[i].dirty = true
				cv.generation++
				return
			}
		}
	})
	// Track running-tool count for the render loop's live ticker (B002).
	tc.onStatusChange = func(old, new ToolStatus) {
		if old == ToolRunning {
			cv.runningToolCount.Add(-1)
		}
		if new == ToolRunning {
			cv.runningToolCount.Add(1)
		}
	}
	// Attach the global tool-view policy so the widget honours the config
	// default and live Ctrl+O toggles from its first render.
	tc.SetToolViewPolicy(cv)
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
	// Stamp the meta entry so the per-agent filter can attribute this tool to
	// the agent. UpdateLast modifies the real entry in place; LastWhere
	// returned a copy that would not persist.
	cv.UpdateLast([]ConsoleItemType{ConsoleToolCall}, func(e *MessageEntry) {
		if e.View == tc {
			if e.Data.Meta == nil {
				e.Data.Meta = map[string]string{"agent": label}
			} else {
				e.Data.Meta["agent"] = label
			}
		}
	})
	return tc
}

// InvalidateRunningToolWidgets requests an in-place update of every running
// tool widget on the next Render call. The actual cache patch happens in
// Render so all shared state mutations stay on the render goroutine.
func (cv *ChatViewport) InvalidateRunningToolWidgets() {
	cv.toolWidgetsDirty.Store(true)
}

// HasRunningToolWidgets reports whether any tool widget is currently in
// ToolRunning state. Safe for cross-goroutine reads (uses an atomic counter
// maintained by SetStatus on the commandLoop). Used by the render loop to
// decide whether to keep the live refresh ticker alive (B002).
func (cv *ChatViewport) HasRunningToolWidgets() bool {
	return cv.runningToolCount.Load() > 0
}

// patchRunningToolWidgets updates the spinner frame for every live tool
// widget without marking the whole conversation dirty. The per-entry rendered
// lines and the frame cache are patched in place, so the compositor never has
// to reprocess the full chat history on every spinner tick. Live means
// Running (elapsed ticking) OR Pending with complete args (the "waiting Ns…"
// display of a queued call, Bug W — it must tick too).
func (cv *ChatViewport) patchRunningToolWidgets(width int) {
	if width == 0 || cv.renderCache.lines == nil {
		return
	}
	for i := range cv.entries {
		tc, ok := cv.entries[i].View.(*ToolExecutionComponent)
		if !ok || !isLiveToolWidget(tc) {
			continue
		}
		tc.updateBox()
		tc.Invalidate()
		cv.updateEntryInCache(i, width)
	}
}

// isLiveToolWidget reports whether a tool widget has a ticking timer:
// Running (elapsed) or args-complete Pending (waiting, Bug W).
func isLiveToolWidget(tc *ToolExecutionComponent) bool {
	return tc.Status() == ToolRunning ||
		(tc.Status() == ToolPending && tc.ArgsComplete())
}

// updateEntryInCache re-renders a single entry and patches its lines into the
// full frame cache at the stored lineOffset. If the entry's line count
// changed or its offset is stale, the caches are invalidated so the next
// Render performs a full rebuild.
func (cv *ChatViewport) updateEntryInCache(i, width int) {
	e := &cv.entries[i]
	oldLen := len(e.renderedLines)
	newLines := e.View.Render(width)
	e.renderedLines = newLines
	e.renderedWidth = width
	e.dirty = false

	if cv.renderCache.lines == nil {
		return
	}
	if len(newLines) != oldLen {
		cv.renderCache.lines = nil
		return
	}
	start := e.lineOffset
	if start < 0 || start+oldLen > len(cv.renderCache.lines) {
		cv.renderCache.lines = nil
		return
	}
	copy(cv.renderCache.lines[start:start+oldLen], newLines)
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
	case ConsoleSystemMessage:
		return newSystemMessage(msg.Content)
	case ConsoleInfoMessage:
		return newInfoMessage(msg.Content)
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
	case ConsoleCompanionMessage, ConsoleCompanionThinkingBlock, ConsoleThinkingBlock:
		return cv.buildSpecialMessageComponent(msg)
	default:
		return NewText(msg.Content, 0, 0)
	}
}

func (cv *ChatViewport) buildSpecialMessageComponent(msg *ChatMessage) Component {
	switch msg.Type {
	case ConsoleCompanionMessage:
		return newCollapsibleComponent("companion", msg.Content)
	case ConsoleCompanionThinkingBlock:
		return newCompanionThinkingBlock(msg.Content)
	case ConsoleThinkingBlock:
		return newThinkingBlock(msg.Content)
	}
	return NewText(msg.Content, 0, 0)
}
