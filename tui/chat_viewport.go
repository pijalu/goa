// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
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

	// scrollWatermark is the compositor's scrollback watermark: the count of
	// canvas rows already committed to terminal scrollback. Those rows are
	// immutable (the window top is clamped to the watermark, so they are never
	// repainted), making it the exact ground truth for IsScrolledOff's
	// "completion would be invisible" check — replacing the former
	// viewportH−bottomChromeH band formula, which re-derived the band from
	// layout measurements that could be stale relative to the committed
	// frame (the spurious-echo class of bugs). Written from the render
	// goroutine after each committed frame; read on the command loop.
	scrollWatermark atomic.Int64

	// transcriptOrigin is the canvas row at which this viewport's rendered
	// content starts (the rows stacked above it — header/mascot — plus any
	// bottom-align padding), published by the layout pass on the command
	// loop. IsScrolledOff adds it to an entry's lineOffset to obtain canvas
	// rows comparable to scrollWatermark.
	transcriptOrigin int

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
	toolsExpandOverride  *bool
	toolsPreviewLines    int
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

// SetScrollWatermark records the compositor's scrollback watermark (the
// count of canvas rows committed to terminal scrollback). It is pushed after
// each committed frame — potentially from the render goroutine — hence the
// atomic store; IsScrolledOff reads it on the command loop.
func (cv *ChatViewport) SetScrollWatermark(wm int) {
	if wm < 0 {
		wm = 0
	}
	cv.scrollWatermark.Store(int64(wm))
}

// setTranscriptOrigin records the canvas row at which this viewport's
// rendered content starts (the header/mascot rows stacked above it plus any
// bottom-align padding). Called by the layout pass (buildBaseLayers) on the
// command loop — the same goroutine that reads it via IsScrolledOff, so a
// plain store suffices.
func (cv *ChatViewport) setTranscriptOrigin(y int) {
	if y < 0 {
		y = 0
	}
	cv.transcriptOrigin = y
}

// TotalHeight returns the total number of lines in the full frame cache, or 0
// when the cache has not been built yet. This lets the compositor place the
// visible tail at the correct absolute Y in the virtual buffer.
func (cv *ChatViewport) TotalHeight() int {
	return len(cv.renderCache.lines)
}

// NewChatViewport creates a ChatViewport backed by a fresh Conversation.
func NewChatViewport() *ChatViewport {
	return &ChatViewport{Conversation: NewConversation()}
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

// Generation returns the mutation generation: it increments on every chat
// mutation (append, update, invalidate). The TUI stamps it into
// Scene.MutationGen so the compositor can detect when the conversation has
// settled (no mutation since the last frame).
func (cv *ChatViewport) Generation() int { return cv.generation }

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
	// rebuildFrame's tool-widget patch (updateEntryInCache) invalidates the
	// frame cache (sets renderCache.lines = nil) when a running tool widget's
	// rendered height changes. Returning bottomAlign(nil) here would render
	// the whole transcript as blank lines for this one frame — collapsing
	// TotalHeight() and pulling the off-screen header back onto the visible
	// screen (the mascot flash during a running bash tool). Rebuild now so the
	// returned frame is always consistent.
	if cv.renderCache.lines == nil {
		cv.fullRebuild(width)
	}
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
// The transcript is strictly append-only: once an entry is appended it is
// never re-ordered or re-appended, which is what keeps the compositor's
// scrollback watermark consistent (exactly-once emission). Transient UI such
// as the pending steering bubble is rendered as bottom chrome (SteeringChrome),
// not as a transcript entry, so it never perturbs this invariant.
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
//
// All entries render in chronological order. Previously running/pending tools
// were sorted to a separate "active" zone at the bottom; that caused open tool
// calls to stay pinned to the bottom while newer messages/thinking accumulated
// above them. The FIFO layout requested in keeps every entry in the
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
//
// It trusts e.lineOffset to locate the entry's block: the cache is truncated
// to [0, lineOffset) and the freshly rendered lines appended. That trust is
// only valid when lineOffset still points at the end of the preceding
// content. If it is stale — larger than the live cache, because entries above
// were removed or shrank without a full rebuild (e.g. a thinking block
// finalized into fewer lines via a separate update path) — truncating to it
// would either panic (slice out of range) or silently drop the tail of the
// transcript for one frame, collapsing TotalHeight() and pulling the
// scrollback watermark's off-screen header back onto the visible screen (the
// mascot-redraw bug). Guard the offset and fall back to a full rebuild when it
// does not line up with the cache.
func (cv *ChatViewport) updateLastEntry(width int) {
	idx := len(cv.entries) - 1
	e := &cv.entries[idx]

	start := e.lineOffset
	if start < 0 || start > len(cv.renderCache.lines) {
		// Stale offset: the incremental assumption is violated. Rebuild the
		// whole frame so every entry's offset is recomputed from truth.
		cv.fullRebuild(width)
		return
	}

	newLines := e.View.Render(width)
	cv.renderCache.lines = cv.renderCache.lines[:start]
	cv.renderCache.lines = append(cv.renderCache.lines, newLines...)

	e.renderedLines = newLines
	e.renderedWidth = width
	e.dirty = false
	cv.renderCache.width = width
}

// Snapshot returns the pure-data view of the conversation for agents/controllers.
