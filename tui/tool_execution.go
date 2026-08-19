// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"fmt"
	"log"
	"runtime/debug"
	"strings"
	"time"

	"github.com/pijalu/goa/internal/ansi"
)

// ToolViewPolicy is implemented by the ChatViewport to supply the global
// tool-view state (effective expand mode + preview line count + read visibility)
// to every tool widget. Keeping it an interface lets widgets be unit-tested
// without a real viewport and centralizes the config/runtime policy in one place.
type ToolViewPolicy interface {
	// EffectiveToolsExpanded reports whether tool blocks should render fully
	// expanded (Full view), either because the config default is "full" or the
	// user toggled all blocks on with Ctrl+O.
	EffectiveToolsExpanded() bool
	// EffectivePreviewLines returns the configured Summary line count.
	EffectivePreviewLines() int
	// ShowReadContent reports whether the read tool's file output should be
	// rendered. When false, read output is hidden even in Expanded/Full view.
	ShowReadContent() bool
}

// defaultToolPreviewLines is the fallback Summary line count when no view
// policy is attached (e.g. in isolated component tests). Production widgets
// always receive the configured value (default 10) via ToolViewPolicy.

// ToolStatus represents the execution state of a tool call.
type ToolStatus int

const (
	ToolPending ToolStatus = iota
	ToolRunning
	ToolSuccess
	ToolError
)

//── ToolExecutionComponent (Container: Box) ──
//
// Architecture: Box(1,1,bg) → renders [topPad, header, body..., bottomPad] with bg

// ToolExecutionComponent displays a single tool call with expand/collapse,
// status colors, and visual truncation.
type ToolExecutionComponent struct {
	Container
	box      *toolBox
	toolName string
	toolArgs string
	args     map[string]any
	output   string
	expanded bool
	// expandedSet is true once the user has explicitly toggled this block
	// (Enter/Ctrl+O on the focused widget). An explicit choice wins over the
	// global view policy (Ctrl+O-all / config default) in BOTH directions — so
	// collapsing one block while the rest are expanded, or expanding one while
	// the rest are collapsed, sticks across streaming re-renders.
	expandedSet  bool
	status       ToolStatus
	duration     string
	isPartial    bool
	argsComplete bool
	// Incremental streaming-args parse state. Providers deliver tool args as a
	// growing accumulated JSON prefix, one delta per token. Re-scanning the
	// whole document per delta is O(n^2) (a regexp + strconv.Unquote over the
	// full text per token) and starves the command loop on large writes — the
	// "100% CPU stuck write" bug. We instead consume each completed field once
	// and only decode the single still-open tail field per delta.
	partialRaw   string // last raw args string seen by updatePartialArgs
	partialPos   int    // offset of the start of the not-yet-fully-parsed field
	partialKey   string // key of the currently-open (growing) field, once known
	partialVFrom int    // offset where the open field's raw value starts
	partialVDone int    // raw value bytes already decoded into partialValue
	partialValue string // decoded value accumulated for the open field so far
	renderer     ToolRenderer
	generic      genericRenderer
	// startTime is the EXECUTION start used for the live "elapsed" display.
	// It is set at widget creation and reset on the transition into
	// ToolRunning so the displayed elapsed measures execution only — matching
	// the tool timeout bound (widget showed "elapsed 213s" for a
	// "timeout 120s" call because streaming/approval time was counted).
	startTime time.Time
	// waitStart stamps args completion: the "waiting Ns…" display for a
	// queued (Pending + args-complete) call counts from here, so argument
	// streaming time is not misreported as queue wait (Bug W).
	waitStart time.Time

	// bodyVersion is bumped whenever a body-input changes (output, args,
	// status, isPartial, argsComplete, expanded, view policy). buildBody
	// memoizes its (expensive) result on (bodyVersion, effectiveExpanded,
	// previewLines) so that per-frame spinner patches and snapshot builds —
	// which rebuild the box without changing body inputs — do not re-split
	// and re-highlight large tool output on every tick. Without this a
	// running tool with large content (write/edit/bash) starves the command
	// loop, freezing the TUI and blocking the result event and Esc/Ctrl-C.
	bodyVersion uint64
	bodyCache   string
	bodyCacheAt bodyCacheKey

	// onInvalidate is called when internal state changes (output, status,
	// duration) so the owning ChatViewport can invalidate its render cache.
	onInvalidate func()

	// onStatusChange is called when the tool's status transitions. The
	// viewport uses it to maintain the running-tool counter (B002).
	onStatusChange func(old, new ToolStatus)

	// agentLabel is the owning agent's display label (e.g. "coder"). When set,
	// it is rendered as a colored prefix on the tool header so multiple agents'
	// tool calls are distinguishable in the chat viewport.
	agentLabel string

	// outputBytes and outputLines track the size of the tool's output so far,
	// for the live-progress footer ("elapsed 12.3s · 1.2 KB · 84 lines").
	// Updated by SetOutput (which is called both for partial progress and for
	// the final result). Only shown while the tool is still running.
	outputBytes int
	outputLines int

	// viewPolicy supplies the global tool-view state (expand mode + preview
	// line count). When nil, the widget falls back to its own expanded flag and
	// a default preview count.
	viewPolicy ToolViewPolicy
}

// toolBox renders the tool header, body, and trailing blank with the
// appropriate background color.
type toolBox struct {
	header   string
	body     string
	duration string
	bgAnsi   string
	rendered []string
	// renderedWidth keys the memoized render: every line embeds its width
	// (background-painted padding), so a terminal resize MUST rebuild —
	// returning lines from the old width would stop the background at the
	// old column count.
	renderedWidth int
}

func (b *toolBox) Render(width int) []string {
	if b.rendered == nil || b.renderedWidth != width {
		b.rendered = b.build(width)
		b.renderedWidth = width
	}
	return b.rendered
}
func (b *toolBox) HandleInput(string) {}
func (b *toolBox) Invalidate()        { b.rendered = nil }

func (b *toolBox) build(width int) []string {
	if width <= 0 {
		return nil
	}
	var lines []string

	// Top padding (like Box paddingY=1)
	lines = append(lines, b.bgLine("", width))

	// Header
	lines = append(lines, b.bgLine(b.header, width))

	// Body
	if b.body != "" {
		for _, line := range strings.Split(b.body, "\n") {
			lines = append(lines, b.bgLine(line, width))
		}
	}

	// Duration
	if b.duration != "" {
		lines = append(lines, b.bgLine(ansiMuted(b.duration), width))
	}

	// Bottom padding (like Box paddingY=1)
	lines = append(lines, b.bgLine("", width))

	return lines
}

func (b *toolBox) bgLine(s string, width int) string {
	return padToWidthStyled(" "+s, width, b.bgAnsi)
}

// ── Construction ──

// NewToolExecution creates a new tool execution component.
func NewToolExecution(toolName, toolArgs string) *ToolExecutionComponent {
	tc := &ToolExecutionComponent{
		toolName:  toolName,
		toolArgs:  toolArgs,
		status:    ToolPending,
		isPartial: true,
		renderer:  GetToolRenderer(toolName),
		box:       &toolBox{},
		startTime: time.Now(),
	}
	tc.updateBox()
	tc.AddChild(tc.box)
	return tc
}

// updateBox rebuilds the box header and body from current state.
func (tc *ToolExecutionComponent) updateBox() {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("goa: ToolExecutionComponent.updateBox panic (tool=%s): %v\n%s", tc.toolName, r, debug.Stack())
			// Leave a minimal visible header so the widget does not vanish.
			tc.box.header = ansi.Fg(TheTheme.ColorHex("tool_running")) + "·" + " " + ansiBoldToolTitle(tc.toolName)
			tc.box.body = ""
			tc.box.duration = ""
			tc.box.bgAnsi = tc.bgANSI()
			tc.box.Invalidate()
		}
	}()

	renderer := tc.renderer
	if renderer == nil {
		renderer = tc.generic
	}

	expanded := tc.effectiveExpanded()
	previewLines := defaultToolPreviewLines
	if tc.viewPolicy != nil {
		if n := tc.viewPolicy.EffectivePreviewLines(); n > 0 {
			previewLines = n
		}
	}

	ctx := RenderContext{
		Expanded:     expanded,
		IsPartial:    tc.isPartial,
		IsError:      tc.status == ToolError,
		ArgsComplete: tc.argsComplete,
		Args:         tc.args,
		PreviewLines: previewLines,
	}

	// Build header
	icon, iconColor := tc.statusIcon()
	call := renderer.RenderCall(tc.args, ctx)
	if call == "" {
		call = ansiBoldToolTitle(tc.toolName)
		if tc.toolArgs != "" {
			call += " " + ansiToolOutput(tc.toolArgs)
		}
	}
	if tc.agentLabel != "" {
		call = ansi.Fg(hashColor(tc.agentLabel)) + "[" + tc.agentLabel + "]" + ansi.Reset + " " + call
	}
	tc.box.header = ansi.Fg(iconColor) + icon + " " + ansi.FgReset + call

	// Build body
	tc.box.body = tc.buildBody(renderer, ctx)

	// Duration
	tc.renderDuration()

	// Background: bash/terminal renderers request the default background so
	// the output looks like raw shell output rather than a colored box.
	if dbr, ok := renderer.(interface{ DefaultBackground() bool }); ok && dbr.DefaultBackground() {
		tc.box.bgAnsi = ""
	} else {
		tc.box.bgAnsi = tc.bgANSI()
	}

	tc.box.Invalidate()
}

// bodyCacheKey captures every input buildBody's result depends on: the
// widget-state version plus the view-policy-derived expand/preview values
// (which can change globally, e.g. via Ctrl+O, without a widget setter).
type bodyCacheKey struct {
	ver      uint64
	expanded bool
	preview  int
}

// invalidateBody marks the cached body stale. Called by every setter that
// changes a body input so the next buildBody recomputes.
func (tc *ToolExecutionComponent) invalidateBody() {
	tc.bodyVersion++
}

// buildBody chooses the right renderer path for the tool body, memoized on
// its inputs. When the tool has produced output, RenderResult is used. While
// arguments are still streaming, a StreamingRenderer gets its RenderPartial
// hook; otherwise the legacy RenderResult("", partial) path is used.
//
// The memoization is what lets a Running tool with large content coexist
// with the 60fps spinner/snapshot rebuilds: the body is recomputed only when
// its inputs change (new streamed content, status change, view toggle), not
// on every animation tick. Streaming content still reaches the user — each
// SetOutput/SetArgsPartial invalidates the cache, so the next build shows it.
func (tc *ToolExecutionComponent) buildBody(renderer ToolRenderer, ctx RenderContext) string {
	key := bodyCacheKey{ver: tc.bodyVersion, expanded: ctx.Expanded, preview: ctx.PreviewLines}
	if key == tc.bodyCacheAt && tc.bodyCache != "" {
		return tc.bodyCache
	}
	body := tc.computeBody(renderer, ctx)
	tc.bodyCache = body
	tc.bodyCacheAt = key
	return body
}

// computeBody is the uncached body-render path.
func (tc *ToolExecutionComponent) computeBody(renderer ToolRenderer, ctx RenderContext) string {
	if tc.output != "" {
		return renderer.RenderResult(tc.output, ctx)
	}
	if !tc.isPartial {
		return ""
	}
	if sr, ok := renderer.(StreamingRenderer); ok {
		return sr.RenderPartial(tc.args, ctx)
	}
	return renderer.RenderResult("", ctx)
}

// renderDuration computes the duration string based on current status and
// elapsed time. It keeps the mutable duration state out of updateBox so the
// latter stays within the complexity budget. The stored value is the full
// display line ("elapsed X.XXs" or "Took X.XXs"); the box builder uses it
// directly. Durations of 0.01s or less are hidden to avoid noisy flicker for
// instantaneous tools.
//
// Pending splits in two (Bug W): args still streaming shows the
// stream age ("elapsed"), but a finalized call QUEUED behind the scheduler
// shows "waiting Ns…" — the elapsed clock must measure execution only and
// starts at the Running transition (true execution start).
func (tc *ToolExecutionComponent) renderDuration() {
	const minDuration = 10 * time.Millisecond // 0.01s
	switch tc.status {
	case ToolSuccess, ToolError:
		elapsed := time.Since(tc.startTime)
		if elapsed <= minDuration {
			tc.box.duration = ""
			return
		}
		// Cache the final duration so repeated renders stay stable once the
		// tool has finished.
		if tc.duration == "" {
			tc.duration = formatDuration(elapsed)
		}
		tc.box.duration = "Took " + tc.duration
	case ToolRunning:
		elapsed := time.Since(tc.startTime)
		if elapsed <= minDuration {
			tc.box.duration = ""
			return
		}
		tc.box.duration = "elapsed " + formatDuration(elapsed) + tc.progressSuffix()
	case ToolPending:
		if tc.argsComplete {
			// Queued: the waiting clock runs from args completion, not from
			// widget creation (streaming time is not queue wait).
			wait := time.Since(tc.waitStart)
			if tc.waitStart.IsZero() || wait <= minDuration {
				tc.box.duration = ""
				return
			}
			tc.box.duration = "waiting " + formatDuration(wait) + "…"
			return
		}
		elapsed := time.Since(tc.startTime)
		if elapsed <= minDuration {
			tc.box.duration = ""
			return
		}
		tc.box.duration = "elapsed " + formatDuration(elapsed) + tc.progressSuffix()
	default:
		tc.box.duration = tc.duration
	}
}

// progressSuffix returns the live-progress segment appended to the duration
// line while a tool is running: " · 1.2 KB · 84 lines". Returns "" when no
// output has been produced yet so the footer stays clean for fast tools.
func (tc *ToolExecutionComponent) progressSuffix() string {
	if tc.outputBytes == 0 {
		return ""
	}
	return " · " + formatByteSize(tc.outputBytes) + " · " + formatLineCount(tc.outputLines)
}

// formatByteSize returns a human-readable byte count (e.g. "1.2 KB").
func formatByteSize(n int) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%d B", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.1f KB", float64(n)/1024)
	default:
		return fmt.Sprintf("%.1f MB", float64(n)/(1024*1024))
	}
}

// formatLineCount returns a human-readable line count (e.g. "84 lines").
func formatLineCount(n int) string {
	if n == 1 {
		return "1 line"
	}
	return fmt.Sprintf("%d lines", n)
}

// ── Setters ──

// SetExpanded toggles between preview and full output.
func (tc *ToolExecutionComponent) SetExpanded(expanded bool) {
	tc.expanded = expanded
	tc.invalidateBody()
	tc.updateBox()
	tc.Invalidate()
}

// setExpandedExplicit records a user-initiated toggle (Enter/Ctrl+O on the
// focused block): the choice becomes an explicit override that wins over the
// global view policy until ClearExplicitExpand is called (e.g. by the global
// Ctrl+O toggle-all).
func (tc *ToolExecutionComponent) setExpandedExplicit(expanded bool) {
	tc.expandedSet = true
	tc.SetExpanded(expanded)
}

// ClearExplicitExpand drops the per-widget override so the widget follows the
// global view policy again. Called by the global toggle-all so a fresh Ctrl+O
// flips every block uniformly regardless of earlier per-widget toggles.
func (tc *ToolExecutionComponent) ClearExplicitExpand() {
	tc.expandedSet = false
}

// SetToolArgs sets the formatted arguments string.
func (tc *ToolExecutionComponent) SetToolArgs(args string) {
	tc.toolArgs = args
	tc.updateBox()
	tc.Invalidate()
}

// SetArgsComplete marks the tool call arguments as fully received.
// This triggers the renderer to show the final header (no longer streaming)
// and starts the "waiting" clock shown while the call queues behind the
// scheduler (Bug W).
func (tc *ToolExecutionComponent) SetArgsComplete() {
	tc.markArgsComplete()
	tc.invalidateBody()
	tc.updateBox()
	tc.Invalidate()
}

// markArgsComplete centralizes the args-complete transition: it stamps the
// waiting clock exactly once. All paths that conclude args are complete must
// go through it (SetArgsComplete, SetArgsJSON, AddToolExecution) — a direct
// field assignment would leave waitStart zero and the "waiting Ns…" display
// would count argument-streaming time as queue wait.
