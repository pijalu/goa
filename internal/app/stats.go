// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/internal/agentic/provider"
	"github.com/pijalu/goa/internal/agentic/provider/models"
	"github.com/pijalu/goa/internal/ansi"
	"github.com/pijalu/goa/internal/metrics"
	"github.com/pijalu/goa/internal/tooltracker"
	"github.com/pijalu/goa/internal/usage"
	"github.com/pijalu/goa/tui"
)

// streamState tracks the current streaming context for LLM output.
// Decoupled from content type so thinking segments break correctly on
// any non-thinking event (tool call, tool result, content, idle, end).
type streamState struct {
	kind     tui.ConsoleItemType
	text     strings.Builder
	isActive bool
}

func (s *streamState) begin(kind tui.ConsoleItemType) {
	s.kind = kind
	s.text.Reset()
	s.isActive = true
}

func (s *streamState) end() {
	s.isActive = false
	s.text.Reset()
}

func (s *streamState) is(kind tui.ConsoleItemType) bool {
	return s.isActive && s.kind == kind
}

func (s *streamState) active() bool {
	return s.isActive
}

// ToolCallLevel indicates the severity of tool call loop detection for
// color-coding the TC:N display in the footer.
type ToolCallLevel int

const (
	ToolCallNormal  ToolCallLevel = 0 // green — all good
	ToolCallWarning ToolCallLevel = 1 // orange — duplicate/repeat detected
	ToolCallStopped ToolCallLevel = 2 // red — budget exceeded, force-stopped
)

// cacheHitWindowSize is the number of recent cache-hit rates kept for the
// rolling average shown in the footer CH:<avg>% segment.
const cacheHitWindowSize = 10

// CacheHitTrend bundles a cache-hit rate with its previous value so the
// footer can color it by evolution: bold green when growing, green when
// stable/slightly growing, orange on a slight drop (< 5 points), red on a
// drop (>= 5 points). Seen gates display (no cache activity yet); HasPrev
// gates delta coloring (first observation has no baseline and renders as
// stable).
//
// The trend also maintains a rolling window of the last cacheHitWindowSize
// rates for the average (CH:<avg>%) — the avg and last values are colored
// independently, each only shifting to orange/red on a >= 5-point change.
type CacheHitTrend struct {
	Pct     float64   // last completion's cache-hit rate
	PrevPct float64   // previous value (for delta coloring)
	Seen    bool      // at least one cache-active round observed
	HasPrev bool      // at least two observations (delta coloring armed)
	window  []float64 // rolling window of recent rates (max cacheHitWindowSize)
}

// observe folds one new cache-hit rate into the trend: the current value
// becomes the previous baseline and pct becomes current. The rate is also
// appended to the rolling window (capped at cacheHitWindowSize).
func (t *CacheHitTrend) observe(pct float64) {
	t.PrevPct, t.HasPrev = t.Pct, t.Seen
	t.Pct, t.Seen = pct, true
	t.window = append(t.window, pct)
	if len(t.window) > cacheHitWindowSize {
		t.window = t.window[len(t.window)-cacheHitWindowSize:]
	}
}

// AvgPct returns the rolling average of the last cacheHitWindowSize
// cache-hit rates. Returns 0 when no observations exist.
func (t *CacheHitTrend) AvgPct() float64 {
	if len(t.window) == 0 {
		return 0
	}
	sum := 0.0
	for _, v := range t.window {
		sum += v
	}
	return sum / float64(len(t.window))
}

// AvgPrevPct returns the average of the window *before* the most recent
// observation — the baseline for delta coloring the average. Returns 0 when
// fewer than 2 observations exist.
func (t *CacheHitTrend) AvgPrevPct() float64 {
	if len(t.window) < 2 {
		return 0
	}
	// Compute avg of window[0:len-1] (exclude the latest).
	prev := t.window[:len(t.window)-1]
	sum := 0.0
	for _, v := range prev {
		sum += v
	}
	return sum / float64(len(prev))
}

// cacheHitTrendFromTotals builds a display-only trend from aggregate token
// counters, for construction sites that have no evolving baseline
// (orchestrator role rows, headless stats): Seen gates display, HasPrev
// stays false so the value renders as stable green.
func cacheHitTrendFromTotals(read, write, prompt int) CacheHitTrend {
	if read == 0 && write == 0 {
		return CacheHitTrend{}
	}
	return CacheHitTrend{Pct: metrics.CacheHitPct(read, write, prompt), Seen: true}
}

// sessionStats holds cumulative + last-turn statistics for footer display.
type sessionStats struct {
	PromptN          int
	PredictedN       int
	CacheReadTotal   int
	CacheWriteTotal  int
	CacheMisses      int     // cache-bust count: zero-cache-read requests after the cache was established
	SpeedTokPerSec   float64 // last turn output tok/s
	ContextEstimate  int
	ContextProjected int
	ContextMax       int
	CostUSD          float64
	ShowCost         bool
	ToolCalls        int
	ToolCallLevel    ToolCallLevel // 0=normal, 1=warning, 2=stopped
	MicroCompacts    int
	Compacts         int
	// LastCacheHit is the most recent completion's cache-hit trend —
	// rendered as CH:<avg>%▸<last>% where <avg> is the rolling average
	// of the last 10 observations and <last> is the most recent rate.
	// Each element is colored independently by its own evolution.
	LastCacheHit CacheHitTrend
	// Compactions documents each completed compression round (strategy,
	// before/after %, freed tokens, removed messages, time). The aggregate
	// MicroCompacts/Compacts counters above feed the footer; this per-round
	// record makes the session stats self-documenting ("context
	// compressions are invisible").
	Compactions []CompactionRound
}

// CompactionRound documents one completed compression pass in the session.
type CompactionRound struct {
	Strategy    string    `json:"strategy"` // elision|selective|micro|summarize|hybrid|ceiling|overflow|truncation
	BeforePct   int       `json:"before_pct"`
	AfterPct    int       `json:"after_pct"`
	FreedTokens int       `json:"freed_tokens,omitempty"`
	Removed     int       `json:"removed,omitempty"`
	At          time.Time `json:"at"` // when the round completed
}

func (a *App) handleAgentOutputEvent(ev *agentic.OutputEvent) {
	if a.streamCapture != nil {
		a.streamCapture.record(ev)
	}
	switch ev.Type {
	case agentic.EventContent:
		a.handleStreamContent(ev)
	case agentic.EventToolResult:
		a.handleToolResult(ev)
	case agentic.EventEnd:
		a.handleSessionEnd(ev)
	case agentic.EventStateChange:
		a.handleStateChange(ev)
	case agentic.EventToolCall:
		a.handleToolCall(ev)
	case agentic.EventToolStart:
		a.handleToolStart(ev)
	case agentic.EventToolProgress:
		a.handleToolProgress(ev)
	case agentic.EventProgress:
		a.handleProgressEvent(ev)
	default:
		a.handleAgentStatsEvent(ev)
	}
}

// handleAgentStatsEvent routes the stats/lifecycle branch of
// handleAgentOutputEvent: token/context stats, compaction bookkeeping, and
// the clear/reset signals that re-arm or wipe session counters. Extracted so
// the event switch stays within the gocyclo budget as cases grow.
func (a *App) handleAgentStatsEvent(ev *agentic.OutputEvent) {
	switch ev.Type {
	case agentic.EventClear:
		a.clearStats()
		a.handleTokenStats(ev)
	case agentic.EventContextReset:
		a.resetCacheBustBaseline()
	case agentic.EventCompact:
		a.recordCompact(ev)
		a.showCompactionBubble(ev)
	default:
		a.handleTokenStats(ev)
	}
}

// compactionStrategy extracts the strategy label from an EventCompact: the
// structured Compaction payload wins, falling back to the free-text Text
// label for events emitted by paths that predate the payload.
func compactionStrategy(ev *agentic.OutputEvent) string {
	if ev.Compaction != nil && ev.Compaction.Strategy != "" {
		return ev.Compaction.Strategy
	}
	return ev.Text
}

// isMicroCompaction reports whether a compression strategy label counts
// toward the footer's micro bucket (the m in c:Xm-Y) rather than the
// full-compact bucket.
func isMicroCompaction(strategy string) bool {
	return strategy == string(agentic.CompressionMicro)
}

// recordCompact counts one completed compression pass and appends its
// per-round record to the session stats.
func (a *App) recordCompact(ev *agentic.OutputEvent) {
	strategy := compactionStrategy(ev)
	a.statsMu.Lock()
	defer a.statsMu.Unlock()
	if isMicroCompaction(strategy) {
		a.microCompacts++
	} else {
		a.compacts++
	}
	a.compactions = append(a.compactions, compactionRoundFromEvent(ev, strategy))
}

// compactionRoundFromEvent builds the per-round session-stats record from an
// EventCompact. Structured fields come from the Compaction payload; the time
// is stamped now (the event carries no timestamp).
func compactionRoundFromEvent(ev *agentic.OutputEvent, strategy string) CompactionRound {
	r := CompactionRound{Strategy: strategy, At: time.Now()}
	if ev.Compaction != nil {
		r.BeforePct = ev.Compaction.BeforePct
		r.AfterPct = ev.Compaction.AfterPct
		r.FreedTokens = ev.Compaction.FreedTokens
		r.Removed = ev.Compaction.Removed
	}
	return r
}

// showCompactionBubble renders a dedicated conversation element for a
// completed compression pass so the user sees the drop instead of an
// unexplained context reset (context compressions are invisible).
// AddFlashMessage dedups a repeated same-strategy pass (a reactive ceiling
// enforcer firing several turns in a row) by updating the last bubble in
// place instead of stacking. It runs on the commandLoop via apply (the chat
// single-owner invariant), guarded for headless/tests.
func (a *App) showCompactionBubble(ev *agentic.OutputEvent) {
	if a.subs == nil || a.subs.chat == nil {
		return
	}
	a.subs.chat.AddFlashMessage(formatCompactionBubble(ev))
}

// formatCompactionBubble renders the one-line compaction bubble text. The ⚡
// prefix + "Context compacted (<strategy>):" shape is the flash-dedup key
// (flashKind), so repeated passes of the same strategy update in place.
func formatCompactionBubble(ev *agentic.OutputEvent) string {
	strategy := ev.Text
	var ci *agentic.CompactionInfo
	if ev.Compaction != nil {
		ci = ev.Compaction
		if ci.Strategy != "" {
			strategy = ci.Strategy
		}
	}
	if strategy == "" {
		strategy = "unknown"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "⚡ Context compacted (%s)", strategy)
	if ci != nil {
		fmt.Fprintf(&b, ": %d%% → %d%%", ci.BeforePct, ci.AfterPct)
		if ci.Removed > 0 {
			fmt.Fprintf(&b, " · %d messages dropped", ci.Removed)
		}
		if ci.FreedTokens > 0 {
			fmt.Fprintf(&b, " · ~%d tokens freed", ci.FreedTokens)
		}
		if ci.Detail != "" {
			fmt.Fprintf(&b, "\n%s", ci.Detail)
		}
	}
	return b.String()
}

func (a *App) clearStats() {
	a.statsMu.Lock()
	defer a.statsMu.Unlock()
	a.tokenPromptTotal = 0
	a.tokenPredictedTotal = 0
	a.tokenCacheReadTotal = 0
	a.tokenCacheWriteTotal = 0
	a.tokenCacheMisses = 0
	a.cacheReadEstablished = false
	a.lastTurnPromptN = 0
	a.lastTurnPredictedN = 0
	a.lastTurnCacheRead = 0
	a.lastTurnCacheWrite = 0
	a.tokenSessionMax = 0
	a.tokenSessionEstimate = 0
	a.tokenSessionProjected = 0
	a.lastTurnSpeed = 0
	a.turnCount = 0
	a.turnStatsSeen = false
	a.microCompacts = 0
	a.compacts = 0
	a.compactions = nil
	a.toolCallsTotal = 0
	a.toolCallWarningLevel = ToolCallNormal
	a.lastCacheHit = CacheHitTrend{}
}

// resetCacheBustBaseline re-arms the cache-bust detector after an in-place
// context reset (EventContextReset — a fresh-context goal begin): subsequent
// token stats belong to a NEW conversation on a fresh provider cache key,
// whose cold start (zero or tiny cache reads) is not a bust. Unlike
// clearStats (user /clear), session totals (CH/CW) and the CM counter itself
// survive — only the per-conversation detector baseline resets.
func (a *App) resetCacheBustBaseline() {
	a.statsMu.Lock()
	defer a.statsMu.Unlock()
	a.lastTurnCacheRead = 0
	a.cacheReadEstablished = false
}

func (a *App) handleProgressEvent(ev *agentic.OutputEvent) {
	// Show prompt-processing progress while waiting for the first token,
	// or reconnection/status messages.
	if ev.PromptProgress != nil {
		a.setWaitingForReplyStatus(ev.PromptProgress)
		return
	}
	if ev.Text != "" {
		a.subs.statusMsg.Show(ev.Text)
		a.subs.tuiEngine.RequestRender()
		return
	}
	// Empty text is the agent's progress-clear signal (emitted by
	// finishProcessing on every turn-exit path and by the stream retry
	// logic). Without this branch the "Sending request..." spinner would
	// linger on any exit path that skips EventEnd.
	a.subs.statusMsg.Clear()
	a.subs.tuiEngine.RequestRender()
}

func (a *App) handleStreamContent(ev *agentic.OutputEvent) {
	if ev.Role == agentic.User || ev.Role == agentic.System {
		a.handleUserOrSystemContent(ev)
		return
	}
	if ev.State == agentic.StateThinking {
		a.handleThinkingContent(ev)
		return
	}
	if ev.Text != "" {
		a.handleAssistantContent(ev)
	}
}

// handleUserOrSystemContent renders user/system-role stream events. Live user
// content is suppressed (the submit handler already rendered it) except for
// replayed history and mid-turn drained steering, both of which must appear.
func (a *App) handleUserOrSystemContent(ev *agentic.OutputEvent) {
	if ev.Role == agentic.User && ev.Text != "" {
		if isSteeringDrained(ev) {
			// Mid-turn steering was drained from the queue and woven into the
			// conversation: clear the pending bubble and show the consumed text
			// as a user message (the bubble's whole point was "this will send").
			if a.subs.steeringChrome != nil {
				a.subs.steeringChrome.Clear()
			}
			a.subs.chat.AddUserMessage(ev.Text)
		} else if isReplay(ev) {
			a.subs.chat.AddUserMessage(ev.Text)
		}
		return
	}
	if ev.Role == agentic.System && ev.Text != "" && isSystemNotification(ev) {
		a.endCurrentStream()
		// A stream-retry notification means the agent reset its content buffer
		// and will re-stream the answer from scratch. Retract the orphaned
		// in-progress assistant bubble so the partial pre-retry text does not
		// linger next to the re-streamed bubble (Issue 4 duplicates).
		if isStreamRetry(ev) {
			a.subs.chat.RemoveLastMessageOfType(tui.ConsoleAssistantMessage, tui.ConsoleThinkingBlock)
		}
		a.subs.chat.AddSystemMessage(ev.Text)
	}
}

// isStreamRetry reports whether ev is the agent's stream-retry notification,
// which signals that the in-progress assistant stream is being discarded and
// re-generated from the start.
func isStreamRetry(ev *agentic.OutputEvent) bool {
	return ev.Metadata != nil && ev.Metadata["stream_retry"] == "true"
}

// isSteeringDrained reports whether ev is a user message the agent wove into
// the turn from the mid-turn steering queue. The pending steering bubble must
// be cleared when this arrives — the queue is now empty, so there is nothing
// left "to send", and leaving the bubble up would show stale steering.
func isSteeringDrained(ev *agentic.OutputEvent) bool {
	return ev.Metadata != nil && ev.Metadata["steering_drained"] == "true"
}

// isSystemNotification reports whether ev is a UI-only system message (e.g.
// a retry notification) that should be rendered as a chat bubble.
func isSystemNotification(ev *agentic.OutputEvent) bool {
	return ev.Metadata != nil && ev.Metadata["category"] == "system-notification"
}

// isReplay reports whether ev is a restored session event, as opposed to a
// live event. During replay, stored user content events are rendered so the
// chat viewport reconstructs the full conversation; live user content is
// already added by the submit handler and must stay suppressed.
func isReplay(ev *agentic.OutputEvent) bool {
	return ev.Metadata != nil && ev.Metadata["replay"] == "true"
}

func (a *App) handleThinkingContent(ev *agentic.OutputEvent) {
	if a.subs.cfg != nil && !a.subs.cfg.TUI.Transparency.ShowThinking {
		return
	}
	a.endStreamIfDifferent(agentic.StateThinking)
	if !a.stream.is(tui.ConsoleThinkingBlock) {
		a.stream.begin(tui.ConsoleThinkingBlock)
		expanded := a.subs.cfg == nil || !a.subs.cfg.TUI.Transparency.ThinkingCollapsed
		a.subs.chat.AddThinkingBlock("", expanded)
	}
	a.stream.text.WriteString(ev.Text)
	a.subs.chat.UpdateLastMessage(a.stream.text.String(), tui.ConsoleThinkingBlock)
	a.subs.statusMsg.Show("Thinking...")
}

func (a *App) handleAssistantContent(ev *agentic.OutputEvent) {
	a.endStreamIfDifferent(agentic.StateContent)
	// Ensure the activity spinner is visible — the model may emit EventContent
	// without a preceding EventStateChange (e.g., subsequent turns after the first).
	// Show() is idempotent: if already spinning with same text, it returns early.
	a.setStreamingStatus()
	if !a.stream.is(tui.ConsoleAssistantMessage) {
		a.stream.begin(tui.ConsoleAssistantMessage)
		a.subs.chat.AddAssistantMessage("")
	}
	a.stream.text.WriteString(ev.Text)
	a.subs.chat.UpdateLastMessage(a.stream.text.String(), tui.ConsoleAssistantMessage)
}

// endCurrentStream stops any active streaming segment so the next content
// event of a different type starts a new block.
func (a *App) endCurrentStream() {
	a.stream.end()
}

// endStreamIfDifferent ends the current streaming block when the new agent
// state corresponds to a different block type. This prevents a thinking block
// from being reused for later assistant content (or vice-versa) after a state
// transition or tool call.
func (a *App) endStreamIfDifferent(state agentic.OutputState) {
	if !a.stream.active() {
		return
	}
	switch state {
	case agentic.StateThinking:
		if a.stream.kind != tui.ConsoleThinkingBlock {
			a.endCurrentStream()
		}
	case agentic.StateContent:
		if a.stream.kind != tui.ConsoleAssistantMessage {
			a.endCurrentStream()
		}
	case agentic.StateToolCall, agentic.StateToolResult, agentic.StateIdle:
		a.endCurrentStream()
	}
}

func (a *App) handleToolResult(ev *agentic.OutputEvent) {
	// Ensure any leftover stream is closed before processing a tool result.
	a.endStreamIfDifferent(agentic.StateToolResult)

	// Normalize: the dedicated ToolResult field is an alias for Text. The
	// agent emits results in Text; tolerate emitters/tests using ToolResult.
	if ev.Text == "" && ev.ToolResult != "" {
		ev.Text = ev.ToolResult
	}

	a.statsMu.Lock()
	a.toolResultsSeen++
	// Track tool call warning level for color-coding the TC:N footer display.
	// Synthetic budget/repeat results start with "[goa-system]"; detect the
	// severity from the message content.
	if strings.HasPrefix(ev.Text, "[goa-system]") {
		switch {
		case strings.Contains(ev.Text, "budget exceeded"):
			a.toolCallWarningLevel = maxToolCallLevel(a.toolCallWarningLevel, ToolCallStopped)
		case strings.Contains(ev.Text, "Loop guardrail"),
			strings.Contains(ev.Text, "identical to the previous"):
			a.toolCallWarningLevel = maxToolCallLevel(a.toolCallWarningLevel, ToolCallWarning)
			// All other [goa-system] messages are informational (repeated call hint,
			// round limit reached, truncated result) — keep ToolCallNormal.
		}
	}
	a.statsMu.Unlock()

	// Restore terminal title when a bash command completes
	if a.subs.tuiEngine != nil {
		cwdBase := ""
		if a.subs.projectDir != "" {
			cwdBase = filepath.Base(a.subs.projectDir)
		}
		a.setBaseTitle(titleBrand + " - " + cwdBase)
	}

	if tc := a.toolTracker().OnResult(ev); tc != nil {
		a.echoScrolledOffToolResult(tc, ev)
		a.clearToolBusy()
		return
	}
	// No tracked widget matched (e.g. a result for a call whose widget was
	// already retired or never seen): render a plain tool-result entry.
	a.subs.chat.AddToolResult(ev.Text)
	a.clearToolBusy()
}

// echoScrolledOffToolResult appends a compact completion echo when a tool
// finishes while its widget is fully scrolled into terminal scrollback. The
// compositor never repaints committed rows, so the widget's ✓/✗ transition
// would be invisible and the frozen running rows would read as "still
// ongoing" (Issue 6: the first series of a parallel cancel batch
// "stayed blue"). The echo renders the tool renderer's own summary (e.g.
// "✓ Cancelled silky.nyala: G05 — …"), capped at a few lines, ANSI-free.
func (a *App) echoScrolledOffToolResult(tc *tui.ToolExecutionComponent, ev *agentic.OutputEvent) {
	if a.subs.chat == nil || !a.subs.chat.IsScrolledOff(tc) {
		return
	}
	isErr := a.toolStatusFromResult(ev.Text) == tui.ToolError
	icon := "✓"
	if isErr {
		icon = "✗"
	}
	body := ""
	if r := tui.GetToolRenderer(ev.ToolName); r != nil {
		body = r.RenderResult(ev.Text, tui.RenderContext{ArgsComplete: true, IsError: isErr})
	}
	if body == "" {
		body = ev.Text
	}
	lines := strings.Split(strings.TrimRight(ansi.Strip(body), "\n"), "\n")
	if len(lines) > 3 {
		lines = append(lines[:3], "…")
	}
	a.subs.chat.AddToolResult(icon + " " + strings.Join(lines, "\n"))
}

// handleToolStart flips a tool widget from waiting (⧖, queued) to running
// (elapsed) at the TRUE execution start: the scheduler started the task
// (EventToolStart). Until this arrives a finalized call stays Pending and
// shows "waiting Ns…" — never a fake elapsed that includes queue time
// (Bug W).
func (a *App) handleToolStart(ev *agentic.OutputEvent) {
	a.toolTracker().OnStart(ev)
}

// handleToolProgress renders partial output emitted by a still-running tool
// (EventToolProgress, e.g. streamed bash stdout) into its widget without
// completing it. The widget stays in the Running state with its live elapsed
// timer; only the displayed output is refreshed so the user sees progress
// instead of a frozen spinner. The tracker resolves the widget without
// retiring it (so the eventual EventToolResult still resolves).
func (a *App) handleToolProgress(ev *agentic.OutputEvent) {
	if tc := a.toolTracker().OnProgress(ev); tc != nil {
		return
	}
}

// failPendingTools marks every tool widget the tracker still considers
// in-flight as interrupted (✗). This is the safety net at EventEnd for tools
// cancelled mid-run; with the tracker it should rarely fire.
// setStreamingStatus shows the most informative status label for the current
// phase. When a tool batch is in progress it shows "Tool calling (X/Y)",
// otherwise "Answering...".
func (a *App) setStreamingStatus() {
	if a.subs.agentMgr != nil {
		if agent := a.subs.agentMgr.CurrentAgent(); agent != nil && agent.BufferedToolCallCount() > 0 {
			a.subs.statusMsg.Show(a.toolCallProgressLabel())
			return
		}
	}
	a.subs.statusMsg.Show("Answering...")
}

// failPendingTools walks all tool widgets in the chat viewport and marks any
// that are still in Running or Pending state as interrupted (ToolError).
// This catches stragglers from EVERY path (foreground tracker, orchestrator
// agent streams, or any orphan) so cancelled tools show ✗ instead of hanging.
// A widget whose arguments never finished streaming was canceled BEFORE the
// tool executed — it is labeled accordingly so the user does not think work
// happened and its output was lost ("Tool call start a review but
// no output of work done").
// The foreground tracker is reset so the next turn starts clean.
func (a *App) failPendingTools() {
	a.subs.toolTracker = nil
	if a.subs.chat == nil {
		return
	}
	interrupted := 0
	for _, c := range a.subs.chat.Children() {
		tc, ok := c.(*tui.ToolExecutionComponent)
		if !ok {
			continue
		}
		if tc.Status() == tui.ToolPending || tc.Status() == tui.ToolRunning {
			if !tc.ArgsComplete() {
				tc.SetOutput("(canceled before execution — the tool never ran)")
			} else {
				tc.SetOutput("(interrupted)")
			}
			tc.SetStatus(tui.ToolError)
			tc.SetPartial(false)
			interrupted++
		}
	}
	if interrupted > 0 && a.subs.tuiEngine != nil {
		a.subs.tuiEngine.RequestRender()
	}
}

func (a *App) handleSessionEnd(ev *agentic.OutputEvent) {
	streamKind := a.stream.kind
	hadActiveStream := a.stream.active()
	a.endCurrentStream()
	a.stream = streamState{} // full reset

	// Mark any tool widgets still in Running/Pending state as interrupted.
	// Without this, tools interrupted by cancellation or error would stay
	// in "⟳ running" state forever, giving no visible indication of failure.
	a.failPendingTools()

	a.statsMu.Lock()
	a.sessionActive = false
	a.toolResultsSeen = 0
	a.turnCount++
	a.toolCallWarningLevel = ToolCallNormal // reset per-turn so TC color doesn't persist across turns
	stats := a.buildFooterStatsLocked()
	a.statsMu.Unlock()

	subs := a.subs
	subs.toolTracker = nil // fresh tracker for the next turn

	// Check if the event carries an error (agentmanager.go emits EventEnd with
	// non-empty Text on stream/connection errors). Surface it to the user with
	// a clear explanation and actionable hint. User-initiated cancellation is
	// marked with Metadata["cancelled"] and is treated as a graceful stop.
	if ev != nil && ev.Text != "" {
		hint := friendlyConnectionHint(ev.Text)
		subs.chat.AddSystemMessage(hint)
	} else if ev != nil && ev.Metadata["cancelled"] == "true" {
		if hadActiveStream {
			subs.chat.RemoveLastMessageOfType(streamKind)
		}
		subs.chat.AddSystemMessage("Generation stopped by user.")
	}

	subs.footer.SetData(tui.FooterData{
		Workdir:                subs.projectDir,
		Model:                  activeModelDisplay(subs),
		Profile:                string(subs.effectiveModeState().Major),
		Mode:                   string(subs.effectiveModeState().Autonomy),
		Stats:                  formatFooterStats(stats),
		Activity:               "",
		MainActivity:           "",
		CompanionModel:         companionModelDisplay(subs),
		Provider:               subs.cfg.ActiveProvider,
		ThinkingLevel:          mainThinkingLevel(subs),
		CompanionThinkingLevel: companionThinkingLevel(subs),
	})
	subs.footer.SetModelBusy(false)
	subs.statusMsg.SessionEnd()
	if subs.tuiEngine != nil {
		subs.tuiEngine.RequestRender()
	}

	// Log turn stats
	a.logTurnStats(ev)
}

// logTurnStats writes a structured stats line to the agent log on each EventEnd.
func (a *App) logTurnStats(ev *agentic.OutputEvent) {
	if a.subs.logger == nil {
		return
	}
	a.statsMu.Lock()
	modelCfg := a.subs.cfg.GetModelByID(a.subs.cfg.ActiveModel)
	ctxPct := 0.0
	if a.tokenSessionMax > 0 {
		// The log's context figure is the projected occupancy (CX8/P20), the
		// same provider-anchored figure the footer shows.
		ctxTokens := a.tokenSessionProjected
		if ctxTokens <= 0 {
			ctxTokens = a.tokenSessionEstimate
		}
		ctxPct = float64(ctxTokens) / float64(a.tokenSessionMax) * 100
	}
	turn := a.turnCount
	promptN := a.lastTurnPromptN
	predictedN := a.lastTurnPredictedN
	speed := a.lastTurnSpeed
	ctxMax := a.tokenSessionMax
	tokenTotalPrompt := a.tokenPromptTotal
	tokenTotalPredicted := a.tokenPredictedTotal
	tokenCacheRead := a.tokenCacheReadTotal
	tokenCacheWrite := a.tokenCacheWriteTotal
	cacheMisses := a.tokenCacheMisses
	statsSeen := a.turnStatsSeen
	a.turnStatsSeen = false // next turn starts with no stats observed
	a.statsMu.Unlock()

	// A turn that never reached the LLM (guardrail latch, connection error,
	// early rejection) emits no EventTokenStats; re-logging the previous
	// turn's stale numbers produced byte-identical [stats] lines across
	// consecutive turns that looked like impossible zero-progress repeats
	// (runaway-loop identical-stats anomaly). Say what happened.
	if !statsSeen {
		a.subs.logger.Log(agentic.Info, fmt.Sprintf("[stats] turn %d: no LLM call (no token stats this turn)", turn))
		return
	}

	line := fmt.Sprintf("[stats] turn %d: in=%d out=%d speed=%.1f ctx=%.1f%%/%d",
		turn, promptN, predictedN, speed, ctxPct, ctxMax)
	if cacheMisses > 0 {
		line += fmt.Sprintf(" cm=%d", cacheMisses)
	}

	if modelCfg != nil && modelCfg.Pricing != nil {
		cost := computeCost(tokenTotalPrompt, tokenTotalPredicted, tokenCacheRead, tokenCacheWrite, modelCfg.Pricing)
		line += fmt.Sprintf(" cost=$%.4f", cost)
	}

	a.subs.logger.Log(agentic.Info, line)
}

func (a *App) handleStateChange(ev *agentic.OutputEvent) {
	// A spurious mid-turn EventEnd arms the status spinner's session-ended
	// guard, which would silently drop every subsequent Show() and leave the
	// spinner dark for the rest of the turn. A transition to an active state
	// proves the turn is still alive, so reset the guard before updating the
	// status label.
	if ev.State != agentic.StateIdle {
		a.subs.statusMsg.Reset()
	}

	// Break any active streaming block when the agent moves to a different
	// output state, so thinking/content/tool segments stay in separate blocks.
	a.endStreamIfDifferent(ev.State)

	activity := ""
	mainActivity := ""
	switch ev.State {
	case agentic.StateThinking:
		activity = "thinking"
		mainActivity = "thinking"
		a.subs.statusMsg.Show("Thinking...")
	case agentic.StateContent:
		activity = "streaming"
		mainActivity = "streaming"
		a.subs.statusMsg.Show("Answering...")
	case agentic.StateToolCall:
		activity = "tool calling"
		// Keep the chat spinner busy, but the footer model spinner is not
		// the model generating — it is a tool running. The tool progress is
		// shown in the chat status spinner instead.
		mainActivity = ""
		a.subs.statusMsg.Show(a.toolCallProgressLabel())
	case agentic.StateToolResult:
		// The harness is sending tool results back to the LLM. Keep the
		// spinner active with the most accurate label: tool progress if
		// more calls are still pending, otherwise "Sending request...".
		activity = ""
		mainActivity = ""
		if a.subs.agentMgr != nil {
			if agent := a.subs.agentMgr.CurrentAgent(); agent != nil && agent.BufferedToolCallCount() > 0 {
				a.subs.statusMsg.Show(a.toolCallProgressLabel())
				break
			}
		}
		a.subs.statusMsg.Show("Sending request...")
	}
	subs := a.subs
	subs.footer.SetData(tui.FooterData{
		Workdir:                subs.projectDir,
		Model:                  activeModelDisplay(subs),
		Profile:                string(subs.effectiveModeState().Major),
		Mode:                   string(subs.effectiveModeState().Autonomy),
		Activity:               activity,
		MainActivity:           mainActivity,
		CompanionModel:         companionModelDisplay(subs),
		Provider:               subs.cfg.ActiveProvider,
		ThinkingLevel:          mainThinkingLevel(subs),
		CompanionThinkingLevel: companionThinkingLevel(subs),
	})
	// Only the main model spinner for actual model generation states;
	// tool calls are surfaced by the chat status spinner, not the model
	// spinner in the footer.
	subs.footer.SetModelBusy(mainActivity != "")
}

// toolTracker returns the foreground conversation's tool-call tracker,
// lazily binding it to the chat viewport. All tool widgets for the main
// agent are created exclusively through it, which guarantees exactly one
// widget per logical tool call (late-id adoption) and prevents the
// "stuck on write" orphan bug.
func (a *App) toolTracker() *tooltracker.Tracker {
	if a.subs.toolTracker == nil {
		chat := a.subs.chat
		a.subs.toolTracker = tooltracker.New(func(name, input string) *tui.ToolExecutionComponent {
			if chat == nil {
				return nil
			}
			return chat.AddToolExecution(name, input)
		})
	}
	return a.subs.toolTracker
}

func (a *App) handleToolCall(ev *agentic.OutputEvent) {
	oldText := a.subs.statusMsg.Text()
	// Finalize any active thinking/content stream before rendering a tool call.
	a.endStreamIfDifferent(agentic.StateToolCall)

	tc, created := a.toolTracker().OnCall(ev)
	if tc == nil {
		return
	}

	// Only the first appearance of a tool call counts toward the session
	// total; streaming deltas and late-id adoptions reuse the existing widget.
	if created {
		a.statsMu.Lock()
		a.toolCallsTotal++
		a.statsMu.Unlock()
	}

	label := a.toolCallProgressLabel()
	if ev.IsDelta {
		// Streaming partial: keep a descriptive label until the call completes.
		label = "Calling " + ev.ToolName + "..."
	}
	// E5 (ENHANCE.md): collapse identical re-emissions. A streaming tool call
	// fires one EventToolCall per delta, each re-entering here with the same
	// label; the status spinner dedupes internally, but the footer update and
	// the status log still ran per delta (2026-08-05 export: 13 identical
	// "Calling bash..." log lines in 0.4s). When the label is unchanged and
	// the call is not newly created, the emission is a no-op: skip it.
	unchanged := !created && label == oldText
	if unchanged {
		return
	}
	// Start the shared status spinner first so that the tool widget and
	// footer observe a non-empty CurrentSpinnerFrame when they render.
	a.subs.statusMsg.Show(label)
	// NOTE: the widget is deliberately NOT stamped Running here. Stamping at
	// args-complete starts every widget of a batch at the same instant, so
	// queued (conflict-serialized) calls display a fake ticking "elapsed"
	// (Multi-tool calling and timeout+ Bug W). The Running
	// transition happens on EventToolStart (true scheduler execution start)
	// or, as a backstop, the call's first progress event.

	// The footer model spinner is not used during a tool call; only the chat
	// status spinner shows the tool's progress.
	a.setToolCallingFooter(label)
	a.setBashTitle(ev.ToolName, ev.ToolInput)

	if a.subs.logger != nil {
		a.subs.logger.Log(agentic.Info, "[status] handleToolCall: tool=%s oldText=%q newText=%q visible=%v",
			ev.ToolName, oldText, a.subs.statusMsg.Text(), a.subs.statusMsg.IsVisible())
	}
}

// (handleStreamingToolCallUpdate / findActiveToolWidget / createStreamingToolWidget
// were folded into the ToolCallTracker, which owns widget identity for both
// delta and final tool-call events.)

func (a *App) setWaitingForReplyStatus(pp *agentic.PromptProgress) {
	subs := a.subs
	label := "Sending request..."
	if pp.Total > 0 {
		pct := pp.Processed * 100 / pp.Total
		label = fmt.Sprintf("Processing... %d%%", pct)
	}
	subs.statusMsg.Show(label)
	if pp.Total > 0 {
		subs.footer.SetData(tui.FooterData{
			Workdir:                subs.projectDir,
			Model:                  activeModelDisplay(subs),
			Profile:                string(subs.effectiveModeState().Major),
			Mode:                   string(subs.effectiveModeState().Autonomy),
			Activity:               "wait",
			MainActivity:           label,
			CompanionModel:         companionModelDisplay(subs),
			Provider:               subs.cfg.ActiveProvider,
			ThinkingLevel:          mainThinkingLevel(subs),
			CompanionThinkingLevel: companionThinkingLevel(subs),
		})
		subs.footer.SetModelBusy(true)
	}
	subs.tuiEngine.RequestRender()
}

// cacheBustDropToleranceTokens is the wobble absorbed before a drop in cache
// reads counts as a cache bust: providers report cached tokens at block
// granularity (e.g. 256-token blocks on kimi), so tiny dips between requests
// are reporting noise, not invalidation. Real busts (compaction truncation,
// TTL expiry) collapse reads by orders of magnitude more.
const cacheBustDropToleranceTokens = 1024

// turnStatsFingerprint identifies one applied round of token statistics so a
// byte-identical re-emission within the same turn can be skipped.
type turnStatsFingerprint struct {
	promptN    int
	predictedN int
	cacheRead  int
	cacheWrite int
}

func (a *App) handleTokenStats(ev *agentic.OutputEvent) {
	a.statsMu.Lock()
	// Extract token counts from timings
	appliedStats := false
	if ev.Timings != nil {
		a.turnStatsSeen = true
		// Dedupe: emitTurnStats re-emits the unchanged providerUsage on
		// consecutive round ends (its provider-usage path never sets
		// turnStatsEmitted), delivering the SAME TokenTimings twice per turn.
		// Skip the duplicate so session totals, the bust counter and the
		// usage.db record count each round once. Identical values in a NEW turn
		// (turnCount advanced) are a different turn and must count again.
		fp := turnStatsFingerprint{
			promptN:    ev.Timings.PromptN,
			predictedN: ev.Timings.PredictedN,
			cacheRead:  ev.Timings.CacheReadTokens,
			cacheWrite: ev.Timings.CacheWriteTokens,
		}
		isDuplicate := a.lastStatsDedupSet &&
			a.lastStatsDedupTurn == a.turnCount &&
			a.lastStatsDedup == fp
		if !isDuplicate {
			a.lastStatsDedup = fp
			a.lastStatsDedupSet = true
			a.lastStatsDedupTurn = a.turnCount
			a.applyTokenTimingsLocked(ev.Timings)
			appliedStats = true
		}
	}

	// Extract context window usage
	if ev.ContextStats != nil {
		a.tokenSessionMax = ev.ContextStats.MaxTokens
		a.tokenSessionEstimate = ev.ContextStats.EstimatedTokens
		a.tokenSessionProjected = ev.ContextStats.ProjectedTokens
	}

	// Record per-turn usage to the global store (best-effort, non-fatal).
	// Only for genuinely new stats — a duplicate re-emission would append the
	// same usage.db row twice.
	if appliedStats {
		a.recordTurnUsageLocked()
	}

	// Compute cost from active model's pricing config
	stats := a.buildFooterStatsLocked()
	a.statsMu.Unlock()

	subs := a.subs
	subs.footer.SetData(tui.FooterData{
		Workdir:                subs.projectDir,
		Model:                  activeModelDisplay(subs),
		Profile:                string(subs.effectiveModeState().Major),
		Mode:                   string(subs.effectiveModeState().Autonomy),
		Stats:                  formatFooterStats(stats),
		CompanionModel:         companionModelDisplay(subs),
		Provider:               subs.cfg.ActiveProvider,
		ThinkingLevel:          mainThinkingLevel(subs),
		CompanionThinkingLevel: companionThinkingLevel(subs),
	})
}

// applyTokenTimingsLocked folds one round of provider token statistics into
// the session accumulators, last-turn fields, and the cache-bust counter.
// Called only for non-duplicate emissions (see the dedupe guard in
// handleTokenStats). Requires a.statsMu to be held.
func (a *App) applyTokenTimingsLocked(timings *agentic.TokenTimings) {
	a.lastTurnPromptN = timings.PromptN
	a.lastTurnPredictedN = timings.PredictedN
	a.tokenPromptTotal += timings.PromptN
	a.tokenPredictedTotal += timings.PredictedN

	// Track cache tokens
	prevCacheRead := a.lastTurnCacheRead
	a.lastTurnCacheRead = timings.CacheReadTokens
	a.lastTurnCacheWrite = timings.CacheWriteTokens
	a.tokenCacheReadTotal += timings.CacheReadTokens
	a.tokenCacheWriteTotal += timings.CacheWriteTokens

	// Cache-hit rate trend: the per-completion rate (pi-style, from THIS
	// round's numbers) — the status bar shows only this, no cumulative
	// session rate. Only rounds with cache activity feed the trend — a
	// cache-less round (or provider) must not drag the rate to 0 and trip
	// the drop coloring.
	if timings.CacheReadTokens > 0 || timings.CacheWriteTokens > 0 {
		a.lastCacheHit.observe(metrics.CacheHitPct(timings.CacheReadTokens, timings.CacheWriteTokens, timings.PromptN))
	}
	// Count cache busts two ways:
	//  1. Zero cache reads AFTER the cache was established (provider TTL
	//     expiry reports 0). The first request(s) of a session — or of a
	//     fresh-context conversation (EventContextReset re-arms
	//     cacheReadEstablished) — are cold by nature and not counted; a
	//     provider reporting no cache stats never establishes, so the
	//     counter stays hidden there. Establishment is tracked in
	//     cacheReadEstablished rather than tokenCacheReadTotal because
	//     the total is a session-level CH figure that must survive
	//     mid-session context resets.
	//  2. A significant DROP in cache reads: in an append-only
	//     conversation the cached prefix grows monotonically, so a
	//     collapse means the prefix was invalidated — e.g. in-place
	//     history mutation (micro compaction) leaves a PARTIAL hit
	//     (5,376 of ~113k tokens in the 2026-08-02 session export),
	//     which the zero-read rule never catches. A tolerance absorbs
	//     block-quantization wobble in provider reporting.
	if timings.CacheReadTokens > 0 {
		a.cacheReadEstablished = true
	}
	if (timings.CacheReadTokens == 0 && a.cacheReadEstablished) ||
		(prevCacheRead > 0 && timings.CacheReadTokens+cacheBustDropToleranceTokens < prevCacheRead) {
		a.tokenCacheMisses++
	}

	// Capture last-turn output speed
	a.lastTurnSpeed = timings.PredictedPerSecond
	if a.lastTurnSpeed == 0 && timings.PredictedMs > 0 {
		a.lastTurnSpeed = float64(timings.PredictedN) / (timings.PredictedMs / 1000.0)
	}
}

// recordTurnUsageLocked appends the just-completed turn's token usage to the
// global usage store for /usage. It is best-effort: store errors never break
// the session. Requires a.statsMu to be held (called from handleTokenStats,
// after last-turn fields are updated).
func (a *App) recordTurnUsageLocked() {
	if a.lastTurnPromptN == 0 && a.lastTurnPredictedN == 0 {
		return // nothing recorded for this turn
	}
	st, err := a.usageStoreOpen()
	if err != nil || st == nil {
		return
	}
	subs := a.subs
	_ = st.Add(usage.Record{
		Project:    subs.projectDir,
		Provider:   subs.cfg.ActiveProvider,
		Model:      activeModelName(subs),
		PromptN:    a.lastTurnPromptN,
		PredictedN: a.lastTurnPredictedN,
		CacheRead:  a.lastTurnCacheRead,
		CacheWrite: a.lastTurnCacheWrite,
	})
}

// usageStoreOpen lazily opens the global usage store, caching the result.
func (a *App) usageStoreOpen() (*usage.Store, error) {
	if a.usageStore != nil {
		return a.usageStore, nil
	}
	if a.usageStoreTried {
		return nil, nil // already failed once; don't retry every turn
	}
	a.usageStoreTried = true
	p, err := usage.DefaultPath()
	if err != nil {
		return nil, err
	}
	st, err := usage.Open(p)
	if err != nil {
		return nil, err
	}
	a.usageStore = st
	return st, nil
}

// buildFooterStatsLocked requires a.statsMu to be held by the caller.
func (a *App) buildFooterStatsLocked() sessionStats {
	st := sessionStats{
		PromptN:          a.tokenPromptTotal,
		PredictedN:       a.tokenPredictedTotal,
		CacheReadTotal:   a.tokenCacheReadTotal,
		CacheWriteTotal:  a.tokenCacheWriteTotal,
		SpeedTokPerSec:   a.lastTurnSpeed,
		ContextEstimate:  a.tokenSessionEstimate,
		ContextProjected: a.tokenSessionProjected,
		ContextMax:       a.tokenSessionMax,
		ToolCalls:        a.toolCallsTotal,
		ToolCallLevel:    a.toolCallWarningLevel,
	}
	applyPricing(&st, a.subs.cfg, a.subs.cfg.ActiveModel)
	st.MicroCompacts = a.microCompacts
	st.Compacts = a.compacts
	st.CacheMisses = a.tokenCacheMisses
	st.LastCacheHit = a.lastCacheHit
	st.Compactions = append([]CompactionRound(nil), a.compactions...)
	return st
}

// applyPricing computes cost and pricing-related visibility flags for the
// given session stats using the model identified by activeModelID.
//
// Pricing resolution order (first match wins):
//  1. User-configured pricing on the model's config entry (YAML) — explicit
//     override, always honored.
//  2. The built-in model registry's cost data (models.go), keyed by the
//     config entry's real model name (ModelConfig.Model), then by the config
//     ID itself. This is the bridge that makes cache-aware cost work out of
//     the box for known models, without requiring YAML cache rates.
func applyPricing(st *sessionStats, cfg *config.Config, activeModelID string) {
	pricing := resolvePricing(cfg, activeModelID)
	if pricing == nil {
		return
	}
	st.CostUSD = computeCost(st.PromptN, st.PredictedN, st.CacheReadTotal, st.CacheWriteTotal, pricing)
	if st.CostUSD > 0 || pricing.InputPer1M > 0 || pricing.OutputPer1M > 0 {
		st.ShowCost = true
	}
}

// resolvePricing returns the effective per-1M pricing for a model: the user's
// config override when present, otherwise the built-in registry cost converted
// from per-token to per-1M. Returns nil when no pricing is known.
func resolvePricing(cfg *config.Config, activeModelID string) *config.PricingConfig {
	modelCfg := cfg.GetModelByID(activeModelID)
	if modelCfg != nil && modelCfg.Pricing != nil {
		return modelCfg.Pricing // explicit user override
	}
	// Built-in registry fallback: try the real API model name, then the config ID.
	var names []string
	if modelCfg != nil && modelCfg.Model != "" {
		names = append(names, modelCfg.Model)
	}
	names = append(names, activeModelID)
	for _, name := range names {
		if m := models.GetModel(name); m != nil && hasBuiltinCost(m.Cost) {
			p := builtinCostToPricing(m.Cost)
			return &p
		}
	}
	return nil
}

// hasBuiltinCost reports whether a registry cost entry carries any non-zero rate.
func hasBuiltinCost(c provider.ModelPricing) bool {
	return c.Input != 0 || c.Output != 0 || c.CacheRead != 0 || c.CacheWrite != 0
}

// builtinCostToPricing maps the registry's ModelPricing onto the per-1M
// PricingConfig used by computeCost. Registry cost values (models.go and
// models_generated.go alike) are per-token rates; PricingConfig is per 1M
// tokens, so the rates scale by 1e6.
func builtinCostToPricing(c provider.ModelPricing) config.PricingConfig {
	return config.PricingConfig{
		InputPer1M:      c.Input * 1e6,
		OutputPer1M:     c.Output * 1e6,
		CacheReadPer1M:  c.CacheRead * 1e6,
		CacheWritePer1M: c.CacheWrite * 1e6,
	}
}

// computeCost computes cumulative cost from token totals and the model's pricing config.
// friendlyConnectionHint translates a raw connection error into a user-friendly
// message with an actionable hint.
func friendlyConnectionHint(raw string) string {
	if raw == "" {
		return ""
	}
	switch {
	case strings.Contains(raw, "SSE stream ended prematurely"),
		strings.Contains(raw, "finish_reason"):
		return "[connection error] The LLM stream ended unexpectedly before the response was complete.\n" +
			"  • This may be a temporary server hiccup — goa will retry automatically\n" +
			"  • If the problem persists, check your LLM server logs and network connection"
	case strings.Contains(raw, "context deadline exceeded"),
		strings.Contains(raw, "timeout"),
		strings.Contains(raw, "Client.Timeout"):
		return "[connection error] The request timed out — the LLM server is taking too long to respond.\n" +
			"  • goa will retry automatically, but if this persists check that your local LLM server (LM Studio, llama.cpp, etc.) is running\n" +
			"  • The model may still be loading — wait and try again\n" +
			"  • Try a smaller/faster model if this persists"
	case strings.Contains(raw, "connection refused"),
		strings.Contains(raw, "connect: connection refused"):
		return "[connection error] Could not connect to the LLM server.\n" +
			"  • Make sure the server is running and the URL/port is correct\n" +
			"  • Check your provider configuration with /config"
	case strings.Contains(raw, "connection reset"),
		strings.Contains(raw, "reset by peer"),
		strings.Contains(raw, "broken pipe"),
		strings.Contains(raw, "unexpected EOF"),
		strings.Contains(raw, "connection lost"),
		strings.Contains(raw, "EOF"):
		return "[connection error] The connection to the LLM server was interrupted.\n" +
			"  • This is usually a temporary network or server hiccup — goa retries automatically\n" +
			"  • If it persists, check your LLM server logs and network connection"
	case strings.Contains(raw, "no such host"),
		strings.Contains(raw, "lookup"):
		return "[connection error] Could not resolve the LLM server hostname.\n" +
			"  • Check your network connection\n" +
			"  • Verify the provider URL in your configuration"
	case strings.Contains(raw, "401"),
		strings.Contains(raw, "unauthorized"),
		strings.Contains(raw, "invalid API key"):
		return "[connection error] Authentication failed.\n" +
			"  • Check your API key in the provider configuration\n" +
			"  • Run /config to update your credentials"
	default:
		// The default must NOT claim "connection lost": the raw text may carry a
		// structured non-connection error (e.g. "Error: 404 - model 'x' not
		// found", a 400 malformed request, a schema error). Mislabeling those as
		// a connection problem sends the user chasing the wrong fix. Detect the
		// structured "Error: <status> - <message>" shape produced by
		// formatFatalStreamMessage/formatRetryMessage and surface it verbatim;
		// only fall back to the generic connection line when nothing better is
		// available.
		if cause := extractHTTPErrorCause(raw); cause != "" {
			return "[request error] " + cause + "\n" +
				"  • This is not a connection problem — the server rejected the request\n" +
				"  • Check the model name and provider configuration with /config"
		}
		return fmt.Sprintf("[error] The LLM request failed.\n  %s", raw)
	}
}

// extractHTTPErrorCause pulls the human-readable "<status> - <message>" cause
// out of a structured stream-error string produced by formatStreamMessage
// ("Error: 404 - model 'x' not found (code)"). The raw text may prefix it with
// wrapping context ("LLM request failed (not retryable): Error: 404 - ..."),
// so we locate the "Error: " marker and return from there. Returns "" when the
// text does not carry an HTTP-status-style error, so callers can fall back.
func extractHTTPErrorCause(raw string) string {
	const marker = "Error: "
	idx := strings.Index(raw, marker)
	if idx < 0 {
		return ""
	}
	cause := strings.TrimSpace(raw[idx+len(marker):])
	// Must start with a 3-digit HTTP status to qualify (avoids matching
	// arbitrary "Error: ..." prose that is not an HTTP rejection).
	if len(cause) < 3 || cause[0] < '0' || cause[0] > '9' || cause[1] < '0' || cause[1] > '9' || cause[2] < '0' || cause[2] > '9' {
		return ""
	}
	// Strip a trailing " - retrying" suffix so the shown cause is the bare error.
	cause = strings.TrimSuffix(cause, " - retrying")
	return cause
}

// computeCost computes cumulative cost from token totals and the model's
// pricing config. Each bucket is charged at its own rate: fresh input at
// InputPer1M, output at OutputPer1M, cache reads at the (much cheaper)
// CacheReadPer1M, and cache writes at the CacheWritePer1M premium.
//
// Bucket semantics are per-provider but the formula is correct for both:
//   - OpenAI-style: computePromptN subtracts cached tokens from PromptN, so
//     cacheRead is added back here at the cheap cache-read rate (not omitted,
//     and not double-charged at the full input rate).
//   - Anthropic-style: input/cache buckets are non-overlapping on the wire, so
//     each is charged independently at its own rate.
func computeCost(promptN, predictedN, cacheRead, cacheWrite int, pricing *config.PricingConfig) float64 {
	if pricing == nil {
		return 0
	}
	cost := float64(promptN)/1e6*pricing.InputPer1M +
		float64(predictedN)/1e6*pricing.OutputPer1M +
		float64(cacheRead)/1e6*pricing.CacheReadPer1M +
		float64(cacheWrite)/1e6*pricing.CacheWritePer1M
	return cost
}

func formatTokenCount(n int) string {
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	if n < 1000000 {
		// Show as K with one decimal
		k := float64(n) / 1000
		return fmt.Sprintf("%.1fK", k)
	}
	m := float64(n) / 1000000
	return fmt.Sprintf("%.1fM", m)
}

func formatFooterStats(s sessionStats) string {
	parts := buildFooterStatParts(s)
	return strings.Join(parts, " ")
}

// formatFooterStatsPlain returns the same textual stats as formatFooterStats
// but with any ANSI escape sequences removed so the output is suitable for
// --plain headless mode or other consumers that must not receive color codes.
func formatFooterStatsPlain(s sessionStats) string {
	parts := buildFooterStatParts(s)
	for i, p := range parts {
		parts[i] = ansi.Strip(p)
	}
	return strings.Join(parts, " ")
}

func buildFooterStatParts(s sessionStats) []string {
	var parts []string
	if s.PromptN > 0 {
		parts = append(parts, "\u2191"+formatTokenCount(s.PromptN))
	}
	if s.PredictedN > 0 {
		parts = append(parts, "\u2193"+formatTokenCount(s.PredictedN))
	}
	if s.SpeedTokPerSec > 0 {
		parts = append(parts, fmt.Sprintf("%.1f tok/s", s.SpeedTokPerSec))
	}
	// Cache hit percentage: CH:<avg>%▸<last>% where <avg> is the rolling
	// average of the last 10 cache-hit observations and <last> is the most
	// recent per-completion rate. See CacheHitPct for the formula; each
	// element carries its own previous baseline for delta coloring.
	if s.LastCacheHit.Seen {
		parts = append(parts, formatLastCacheHitPart(s.LastCacheHit))
	}
	// Cache-miss counter, next to CH and only when non-zero (a miss means the
	// established cache was bypassed — compression, TTL expiry, prefix churn).
	if s.CacheMisses > 0 {
		parts = append(parts, formatCacheMissPart(s.CacheMisses))
	}
	if s.ToolCalls > 0 {
		parts = append(parts, formatToolCallPart(s.ToolCalls, s.ToolCallLevel))
	}
	if s.ShowCost && s.CostUSD > 0 {
		parts = append(parts, fmt.Sprintf("$%.4f", s.CostUSD))
	}
	if s.ContextMax > 0 {
		parts = append(parts, formatContextUsage(footerContextTokens(s), s.ContextMax))
	}
	// Show compression counters when non-zero.
	if s.MicroCompacts > 0 || s.Compacts > 0 {
		parts = append(parts, fmt.Sprintf("c:%dm-%d", s.MicroCompacts, s.Compacts))
	}
	return parts
}

// footerContextTokens resolves the token figure the footer's occupancy display
// renders: the projected next-request cost when recorded, else the estimate
// (CX8/P20 — occupancy displays read the projection; the fallback only applies
// before any provider usage has been recorded, when they are equal anyway).
func footerContextTokens(s sessionStats) int {
	if s.ContextProjected > 0 {
		return s.ContextProjected
	}
	return s.ContextEstimate
}

// formatContextUsage renders context usage as "52.3%/128k". The
// auto-detected-window and soft-compression-layer parenthetical ("(auto+micro)")
// was removed from the status bar as noise (user request); ContextAutoMax is
// still tracked for other surfaces.
func formatContextUsage(estimate, max int) string {
	if max <= 0 {
		return "?"
	}
	pct := float64(estimate) / float64(max) * 100
	value := fmt.Sprintf("%.1f%%/%s", pct, formatTokenCount(max))
	color := tui.TheTheme.ColorHex("status_bar_fg")
	switch {
	case pct > 90:
		color = tui.TheTheme.ColorHex("token_critical")
	case pct > 70:
		color = tui.TheTheme.ColorHex("token_warning")
	}
	return ansi.Fg(color) + value + ansi.Reset
}

// Cache-hit evolution thresholds, in percentage points of delta from the
// previous value. Colors only shift on significant changes (>=5pt drop):
// minor fluctuations stay green to avoid alarm fatigue.
const (
	cacheHitGrowDelta = 1.0  // >= this: growing (bold green)
	cacheHitDropDelta = -5.0 // <= this: significant drop (red); between 0 and this: stable (green)
)

// formatLastCacheHitPart renders the cache hit rate segment of the status
// bar: CH:<avg>%▸<last>% where <avg> is the rolling average of the last
// cacheHitWindowSize observations and <last> is the most recent one.
//
// Each element is colored independently based on its evolution from its
// own previous baseline (significant changes only — >=5pt drop for red):
//   - Growing (>=+1pt):           bold green (#3fb950)
//   - Stable / minor change:      green (#3fb950) — any delta > -5pts
//   - Significant drop (>=5pts):  red (#f85149)
//
// The first observation (no previous baseline) renders as stable green.
func formatLastCacheHitPart(t CacheHitTrend) string {
	avg := t.AvgPct()
	avgPrev := t.AvgPrevPct()
	avgColor := cacheHitColorFor(avg, avgPrev, t.HasPrev)
	lastColor := cacheHitColorFor(t.Pct, t.PrevPct, t.HasPrev)
	return fmt.Sprintf("%sCH:%.1f%%%s%s▸%.1f%%%s",
		avgColor, avg, ansi.Reset,
		lastColor, t.Pct, ansi.Reset)
}

// cacheHitColorFor resolves the SGR prefix (color + optional bold) for a
// cache-hit element (avg or last) based on its delta from the previous
// baseline. hasPrev=false renders as stable green (no baseline).
//
// Color scheme (per the bug report: emphasize significant changes, not minor
// fluctuations):
//   - Growing (>=+1pt):        bold green (#3fb950)
//   - Stable / minor change:   green (#3fb950) — any delta > -5pts
//   - Significant drop:        red (#f85149) — delta <= -5pts
func cacheHitColorFor(pct, prevPct float64, hasPrev bool) string {
	const (
		green = "#3fb950"
		red   = "#f85149"
	)
	delta := pct - prevPct
	switch {
	case !hasPrev:
		// No baseline yet — first observation reads as stable.
		return ansi.Fg(green)
	case delta >= cacheHitGrowDelta:
		return ansi.Bold + ansi.Fg(green)
	case delta > cacheHitDropDelta:
		return ansi.Fg(green)
	default:
		return ansi.Fg(red)
	}
}

// formatCacheMissPart renders the cache-miss counter in warning orange:
// misses mean the established prefix cache was bypassed, so they are always
// worth noticing (and hidden at zero).
func formatCacheMissPart(misses int) string {
	return ansi.Fg("#d29922") + fmt.Sprintf("CM:%d", misses) + ansi.Reset
}

// formatToolCallPart renders the TC:N display with color coding:
//   - green (token_completion):   all good
//   - orange (token_warning):     duplicate/repeat detected
//   - red (token_critical):       budget exceeded, force-stopped
func formatToolCallPart(count int, level ToolCallLevel) string {
	colorHex := tui.TheTheme.ColorHex("status_bar_fg")
	switch level {
	case ToolCallWarning:
		colorHex = tui.TheTheme.ColorHex("token_warning")
	case ToolCallStopped:
		colorHex = tui.TheTheme.ColorHex("token_critical")
	}
	return ansi.Fg(colorHex) + fmt.Sprintf("TC:%d", count) + ansi.Reset
}
