// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"

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

	// pendingSteering is the index of the ConsoleSteeringPending entry, or -1
	// if none is present. It is kept so new messages are inserted above the
	// pending bubble instead of pushing it up.
	pendingSteering int

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

	// viewportH is a viewport-height hint set by the host via SetViewportHeight.
	// Render does NOT truncate to it: culling of off-screen rows happens at the
	// compositor level (placeLayer only iterates the visible subrange of a
	// layer's content), and returning a tail here would starve the
	// scrollback-population paths (emitFirstScroll/emitLargeScroll) that read
	// historical canvas rows. The field is retained so the host can advertise
	// the viewport size and so a future line-addressable history can use it.
	viewportH int

	// generation increments on every mutation (append, update, invalidate).
	// Render compares it to lastRenderGen: when they match and the cache is
	// valid, it skips the O(n) dirtyIndices scan entirely. This avoids
	// scanning all entries on every frame when only the input editor changes
	// (the common typing scenario).
	generation    int
	lastRenderGen int

	// allocatedHeight is the vertical budget the layout (buildScene) reserves
	// for this viewport (terminal height minus chrome). Render bottom-anchors
	// the content within it: when the content is shorter than the budget, blank
	// lines are PREPENDED so the content sits just above the status line and
	// the input/footer stay pinned at the screen bottom. When the content
	// exceeds the budget, it is rendered in full so the compositor can scroll
	// the oldest lines into terminal scrollback. Zero (tests / no layout) means
	// no anchoring — render the content at its natural height.
	allocatedHeight int
	lastRenderWidth int

	// Tool view policy (Summary/Full + preview line count). toolsDefaultExpanded
	// comes from config (tui.tools.view == "full"); toolsExpandOverride is set
	// by Ctrl+O to flip all blocks for the running session (nil = follow config).
	// toolsPreviewLines is the configured Summary line count (default 10).
	// showRead controls read tool output visibility (default false = silent).
	toolsDefaultExpanded bool
	toolsExpandOverride   *bool
	toolsPreviewLines     int
	showRead             bool

	// toolWidgetsDirty is set by the animation ticker to request an in-place
	// update of running tool widgets on the next Render call. It is an atomic
	// flag so the ticker (which may run on a different goroutine) can safely
	// request the patch without mutating shared render caches directly.
	toolWidgetsDirty atomic.Bool

	// runningToolCount tracks how many tool widgets are currently in
	// ToolRunning state. Updated by SetStatus (on the commandLoop) and read
	// by the renderLoop's live ticker via HasRunningToolWidgets. Atomic so
	// both goroutines can access it without a lock (B002).
	runningToolCount atomic.Int64
}

// SetViewportHeight records the terminal viewport height. It is called by
// the host each frame. ChatViewport does NOT truncate its rendered output to
// this height: off-screen culling is handled by the compositor (placeLayer
// iterates only the visible subrange of a layer's content), and returning a
// tail would starve scrollback-population paths. The hint is retained for a
// future line-addressable history and is a no-op today.
func (cv *ChatViewport) SetViewportHeight(h int) {
	if h < 0 {
		h = 0
	}
	cv.viewportH = h
}

// TotalHeight returns the total number of lines in the full frame cache, or 0
// when the cache has not been built yet. This lets the compositor place the
// visible tail at the correct absolute Y in the virtual buffer.
func (cv *ChatViewport) TotalHeight() int {
	return len(cv.renderCache.lines)
}

// NewChatViewport creates a ChatViewport backed by a fresh Conversation.
func NewChatViewport() *ChatViewport {
	return &ChatViewport{Conversation: NewConversation(), pendingSteering: -1}
}

// SetToolsConfig applies the configured tool display policy: the default
// expand mode, the Summary preview line count, and read tool visibility.
// Called once from the app layer after the config is loaded. Zero PreviewLines
// is normalized to the default (10).
func (cv *ChatViewport) SetToolsConfig(expanded bool, previewLines int, showRead bool) {
	if previewLines <= 0 {
		previewLines = defaultToolPreviewLines
	}
	changed := cv.toolsDefaultExpanded != expanded || cv.toolsPreviewLines != previewLines || cv.showRead != showRead
	cv.toolsDefaultExpanded = expanded
	cv.toolsPreviewLines = previewLines
	cv.showRead = showRead
	if changed {
		cv.invalidateAllToolWidgets()
	}
}

// ToggleAllToolsView flips every tool block between Summary and Full for the
// running session (Ctrl+O). Subsequent widgets inherit the override too.
func (cv *ChatViewport) ToggleAllToolsView() {
	nowExpanded := !cv.EffectiveToolsExpanded()
	cv.toolsExpandOverride = &nowExpanded
	cv.invalidateAllToolWidgets()
}

// EffectiveToolsExpanded reports whether tool blocks render fully expanded,
// honouring a Ctrl+O override over the config default.
func (cv *ChatViewport) EffectiveToolsExpanded() bool {
	if cv.toolsExpandOverride != nil {
		return *cv.toolsExpandOverride
	}
	return cv.toolsDefaultExpanded
}

// EffectivePreviewLines returns the configured Summary line count (default 10).
func (cv *ChatViewport) EffectivePreviewLines() int {
	if cv.toolsPreviewLines <= 0 {
		return defaultToolPreviewLines
	}
	return cv.toolsPreviewLines
}

// ShowReadContent reports whether the read tool's file output should be
// rendered in the chat viewport. When false (the default), read output is
// hidden even in Expanded/Full view.
func (cv *ChatViewport) ShowReadContent() bool {
	return cv.showRead
}

// invalidateAllToolWidgets forces every tool widget to rebuild on the next
// render so a global view-mode change (config load or Ctrl+O) takes effect
// immediately.
func (cv *ChatViewport) invalidateAllToolWidgets() {
	for i := range cv.entries {
		if tc, ok := cv.entries[i].View.(*ToolExecutionComponent); ok {
			tc.ClearExplicitExpand() // global toggle-all overrides per-widget toggles
			tc.SetToolViewPolicy(cv)
		}
	}
	cv.generation++
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

// SetAllocatedHeight is called by the layout pass (buildScene) with the
// vertical budget reserved for this viewport. See HeightAllocated.
func (cv *ChatViewport) SetAllocatedHeight(height int) { cv.allocatedHeight = height }

// Render draws the conversation. Per-entry caches avoid re-rendering
// unchanged entries; the total frame cache is updated incrementally when only
// the last entry changed. The rendered content is finally bottom-anchored to
// the allocated height so the input/footer stay pinned.
func (cv *ChatViewport) Render(width int) []string {
	if cv.suppressed {
		return nil
	}
	if width <= 0 {
		return nil
	}
	if width != cv.lastRenderWidth {
		cv.lastRenderWidth = width
		cv.resetRenderCaches(width)
	}
	// Fast path: when no mutations have occurred since the last render, the
	// frame cache is valid, and no tool animation is pending, return it
	// immediately without scanning all entries.
	if cv.generation == cv.lastRenderGen && cv.renderCache.lines != nil && !cv.toolWidgetsDirty.Load() {
		return cv.bottomAlign(cv.renderCache.lines)
	}
	cv.lastRenderGen = cv.generation
	cv.rebuildFrame(width)
	return cv.bottomAlign(cv.renderCache.lines)
}

// bottomAlign prepends blank lines so the content sits at the bottom of the
// allocated region. This keeps every component below the viewport (status,
// input, footer) pinned at the same screen row regardless of whether the
// conversation is empty or full, and makes growth scroll the oldest content
// into scrollback instead of pushing the footer down. Content taller than the
// budget is returned unchanged so the compositor's scrollback handles it.
func (cv *ChatViewport) bottomAlign(lines []string) []string {
	if cv.allocatedHeight <= 0 || len(lines) >= cv.allocatedHeight {
		return lines
	}
	pad := cv.allocatedHeight - len(lines)
	out := make([]string, 0, cv.allocatedHeight)
	for i := 0; i < pad; i++ {
		out = append(out, "")
	}
	return append(out, lines...)
}

// rebuildFrame chooses between full and incremental rebuilds and applies any
// pending tool-widget animation patches.
func (cv *ChatViewport) rebuildFrame(width int) {
	if cv.agentFilter != "" || cv.agentFilter != cv.lastRenderFilter || cv.renderCache.lines == nil {
		cv.fullRebuild(width)
		return
	}
	dirty := cv.dirtyIndices()
	if len(dirty) == 0 && cv.renderCache.lines != nil && !cv.toolWidgetsDirty.Load() {
		return
	}
	if len(dirty) == 1 && dirty[0] == len(cv.entries)-1 && cv.lastEntryAtBottom() {
		cv.updateLastEntry(width)
	} else {
		cv.fullRebuild(width)
	}
	if cv.toolWidgetsDirty.CompareAndSwap(true, false) {
		cv.patchRunningToolWidgets(width)
	}
}

// lastEntryAtBottom reports whether the last entry renders at the bottom of
// the frame. With chronological ordering (no active-zone sort), the last entry
// is always at the bottom.
func (cv *ChatViewport) lastEntryAtBottom() bool {
	return true
}

// Invalidate propagates to every entry's view and clears the render caches.
func (cv *ChatViewport) Invalidate() {
	cv.renderCache.width = 0
	cv.renderCache.lines = nil
	cv.generation++
	cv.pendingSteering = -1
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
	cv.pendingSteering = -1
	cv.renderCache.width = 0
	cv.renderCache.lines = nil
	cv.generation++
}

// ── Generic Model delegates (composable primitives) ──

// Append adds an entry to the conversation and marks the new entry dirty.
// If a steering-pending entry is present, the new entry is inserted directly
// above it so the pending bubble stays at the bottom until it is consumed.
func (cv *ChatViewport) Append(e MessageEntry) int {
	if cv.pendingSteering >= 0 && e.Data.Type != ConsoleSteeringPending {
		pending := cv.entries[cv.pendingSteering]
		cv.pendingSteering = -1
		cv.RemoveLast([]ConsoleItemType{ConsoleSteeringPending})
		id := cv.Append(e)
		cv.Append(pending)
		return id
	}
	if e.Data.Type == ConsoleSteeringPending {
		cv.pendingSteering = len(cv.entries)
	}
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
		if e.Data.Type == ConsoleSteeringPending {
			cv.pendingSteering = -1
		} else if cv.pendingSteering >= len(cv.entries) {
			cv.pendingSteering = -1
		}
		cv.renderCache.width = 0
		cv.renderCache.lines = nil
		cv.generation++
		return e, true
	}
	return MessageEntry{}, false
}

// AddSteeringPending adds or updates a pending steering bubble that stays at
// the bottom of the chat until ClearSteeringPending is called.
func (cv *ChatViewport) AddSteeringPending(text string) {
	cv.ClearSteeringPending()
	cv.Append(MessageEntry{Data: MessageData{Type: ConsoleSteeringPending, Text: text}, View: newSteeringPending(text)})
}

// ClearSteeringPending removes the pending steering bubble, if any.
func (cv *ChatViewport) ClearSteeringPending() {
	if cv.pendingSteering < 0 {
		return
	}
	cv.RemoveLast([]ConsoleItemType{ConsoleSteeringPending})
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
//
// All entries render in chronological order. Previously running/pending tools
// were sorted to a separate "active" zone at the bottom; that caused open tool
// calls to stay pinned to the bottom while newer messages/thinking accumulated
// above them. The FIFO layout requested in bugs.md keeps every entry in the
// order it occurred, so a running tool moves up as newer content is appended.
func (cv *ChatViewport) fullRebuild(width int) {
	cv.renderCache.width = width
	cv.lastRenderFilter = cv.agentFilter
	cv.renderCache.lines = cv.renderCache.lines[:0]
	offset := 0
	for i := range cv.entries {
		offset += cv.appendEntry(&cv.entries[i], width, offset)
	}
}

// appendEntry renders entry e (re-rendering only when stale) into the frame
// cache at the given offset, recording its lineOffset. It returns the number
// of lines appended (0 when the agent filter excludes e). Extracted from
// fullRebuild to keep both under the complexity budget.
func (cv *ChatViewport) appendEntry(e *MessageEntry, width, offset int) int {
	if cv.agentFilter != "" {
		agent := ""
		if e.Data.Meta != nil {
			agent = e.Data.Meta["agent"]
		}
		if agent != cv.agentFilter {
			return 0
		}
	}
	if e.renderedWidth != width || e.dirty || e.renderedLines == nil {
		e.renderedLines = e.View.Render(width)
		e.renderedWidth = width
		e.dirty = false
	}
	e.lineOffset = offset
	cv.renderCache.lines = append(cv.renderCache.lines, e.renderedLines...)
	return len(e.renderedLines)
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
		tc.argsComplete = true
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

// patchRunningToolWidgets updates the spinner frame for every running tool
// widget without marking the whole conversation dirty. The per-entry rendered
// lines and the frame cache are patched in place, so the compositor never has
// to reprocess the full chat history on every spinner tick.
func (cv *ChatViewport) patchRunningToolWidgets(width int) {
	if width == 0 || cv.renderCache.lines == nil {
		return
	}
	for i := range cv.entries {
		tc, ok := cv.entries[i].View.(*ToolExecutionComponent)
		if !ok || tc.Status() != ToolRunning {
			continue
		}
		tc.updateBox()
		tc.Invalidate()
		cv.updateEntryInCache(i, width)
	}
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
	case ConsoleCompanionMessage, ConsoleCompanionThinkingBlock, ConsoleSteeringPending, ConsoleThinkingBlock:
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
	case ConsoleSteeringPending:
		return newSteeringPending(msg.Content)
	case ConsoleThinkingBlock:
		return newThinkingBlock(msg.Content)
	}
	return NewText(msg.Content, 0, 0)
}
