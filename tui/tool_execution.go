// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"encoding/json"
	"fmt"
	"log"
	"regexp"
	"runtime/debug"
	"strconv"
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
const defaultToolPreviewLines = 10

// partialStringFieldRe extracts quoted string fields from incomplete JSON
// objects during streaming tool-call argument display.
var partialStringFieldRe = regexp.MustCompile(`"([^"]+)":"((?:\\.|[^"\\])*)`)

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
	box          *toolBox
	toolName     string
	toolArgs     string
	args         map[string]any
	output       string
	expanded     bool
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
	startTime    time.Time

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
}

func (b *toolBox) Render(width int) []string {
	if b.rendered == nil {
		b.rendered = b.build(width)
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
func (tc *ToolExecutionComponent) renderDuration() {
	const minDuration = 10 * time.Millisecond // 0.01s
	elapsed := time.Since(tc.startTime)
	if elapsed <= minDuration {
		tc.box.duration = ""
		return
	}
	d := formatDuration(elapsed)
	switch tc.status {
	case ToolSuccess, ToolError:
		// Cache the final duration so repeated renders stay stable once the
		// tool has finished.
		if tc.duration == "" {
			tc.duration = d
		}
		tc.box.duration = "Took " + tc.duration
	case ToolPending, ToolRunning:
		tc.box.duration = "elapsed " + d + tc.progressSuffix()
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
// This triggers the renderer to show the final header (no longer streaming).
func (tc *ToolExecutionComponent) SetArgsComplete() {
	tc.argsComplete = true
	tc.invalidateBody()
	tc.updateBox()
	tc.Invalidate()
}

// SetArgsPartial updates the header display with partial tool call
// arguments during streaming. Unlike SetArgsJSON, this does NOT attempt
// json.Unmarshal (partial JSON would fail). The renderer handles
// incomplete JSON via the ArgsComplete field in RenderContext.
func (tc *ToolExecutionComponent) SetArgsPartial(args string) {
	tc.toolArgs = args
	tc.updatePartialArgs(args)
	tc.invalidateBody()
	tc.updateBox()
	tc.Invalidate()
	if tc.onInvalidate != nil {
		tc.onInvalidate()
	}
}

// couldBeCompleteJSON reports whether raw might be a complete JSON object:
// it ends with '}' after optional whitespace. Mid-stream args end inside a
// string value (a quote, escape, or content byte), so this filters out every
// incomplete delta cheaply, in O(trailing whitespace) time.
func couldBeCompleteJSON(raw string) bool {
	for i := len(raw) - 1; i >= 0; i-- {
		switch raw[i] {
		case ' ', '\t', '\n', '\r':
			continue
		case '}':
			return true
		default:
			return false
		}
	}
	return false
}

// updatePartialArgs merges best-effort parsed fields from a partial JSON
// argument string into tc.args so renderers can display streaming content
// (e.g. write/edit content) before the full JSON is complete.
//
// Streaming args arrive as a growing accumulated JSON prefix, one delta per
// token. A naive re-scan of the whole document per delta is O(n^2). This
// scanner is incremental: it consumes each field once its value terminates,
// keeps partialPos at the first unconsumed field, and for the single still-open
// field decodes only the raw value tail. Per-delta work is proportional to the
// new text plus one linear pass over the open field's value, not the document.
func (tc *ToolExecutionComponent) updatePartialArgs(raw string) {
	// Attempt the real parser only when the document could be complete: the
	// last non-space byte is '}'. Mid-stream the string ends inside a value,
	// so this cheap check avoids a full O(n) json.Unmarshal that always fails
	// (and always re-parses from byte 0) on every delta. SetArgsJSON does the
	// authoritative parse at completion.
	if couldBeCompleteJSON(raw) {
		var parsed map[string]any
		if err := json.Unmarshal([]byte(raw), &parsed); err == nil {
			tc.args = parsed
			return
		}
	}

	// Reset when the args string is replaced rather than grown.
	if !strings.HasPrefix(raw, tc.partialRaw) {
		tc.partialPos, tc.partialKey, tc.partialVFrom = 0, "", 0
		tc.partialVDone, tc.partialValue = 0, ""
		tc.args = nil
	}
	tc.partialRaw = raw
	if tc.args == nil {
		tc.args = make(map[string]any)
	}

	// Consume completed fields starting at partialPos. A field is complete
	// once its value's closing quote is present; consumeField advances
	// partialPos past it and records the decoded value.
	for tc.partialPos < len(raw) {
		next, done := tc.consumePartialField(raw)
		if !done {
			break // tail is an incomplete field; handled below
		}
		tc.partialPos = next
	}
}

// consumePartialField processes input starting at tc.partialPos. It consumes
// every completed `"key":"value"` field and, for the single still-open field at
// the tail, decodes its (growing) raw value into tc.args. It uses a hand-rolled
// scan — not a regexp over the tail — so per-delta work is proportional to the
// newly arrived bytes rather than the accumulated document size.
func (tc *ToolExecutionComponent) consumePartialField(raw string) (next int, done bool) {
	for tc.partialPos < len(raw) {
		key, vStart, vEnd, closed, ok := scanPartialField(raw, tc.partialPos)
		if !ok {
			return tc.partialPos, false // no complete field boundary yet
		}
		if key != tc.partialKey {
			// Moved to a new field: restart its append-only decode.
			tc.partialKey, tc.partialVFrom = key, vStart
			tc.partialVDone, tc.partialValue = 0, ""
		}
		if !closed {
			// Open (still-growing) field: decode only the newly arrived raw
			// suffix and append it, keeping per-delta work O(new bytes) instead
			// of re-decoding the whole value each delta. partialPos stays at
			// the field start so the (now larger) raw range is re-read next
			// delta — but only the unread suffix is decoded.
			tc.appendOpenValue(raw[vStart:vEnd])
			tc.args[key] = tc.partialValue
			return tc.partialPos, false
		}
		// Completed field: decode the full raw value once.
		value := raw[vStart:vEnd]
		if u, err := strconv.Unquote(`"` + value + `"`); err == nil {
			value = u
		}
		tc.args[key] = value
		tc.partialPos = vEnd + 1 // past the closing quote
	}
	return tc.partialPos, true
}

// appendOpenValue decodes the not-yet-consumed raw suffix of the open field's
// value and appends it to partialValue, advancing partialVDone. Decoding only
// the new suffix keeps streaming a large value O(1) per delta rather than
// O(value) per delta. strconv.Unquote needs a balanced escape at the cut
// point, so a trailing incomplete escape is left for the next delta.
func (tc *ToolExecutionComponent) appendOpenValue(rawVal string) {
	if tc.partialVDone >= len(rawVal) {
		return // nothing new (or value shrank; the next full decode corrects it)
	}
	suffix := rawVal[tc.partialVDone:]
	// Don't split a backslash escape at the cut point: if the new suffix ends
	// mid-escape, hold the trailing partial sequence for the next delta.
	if n := trailingBackslashes(suffix); n%2 == 1 {
		suffix = suffix[:len(suffix)-1]
	}
	if suffix == "" {
		return
	}
	decoded := suffix
	if u, err := strconv.Unquote(`"` + suffix + `"`); err == nil {
		decoded = u
	}
	tc.partialValue += decoded
	tc.partialVDone += len(suffix)
}

// trailingBackslashes reports how many backslashes end s.
func trailingBackslashes(s string) int {
	n := 0
	for i := len(s) - 1; i >= 0 && s[i] == '\\'; i-- {
		n++
	}
	return n
}

// scanPartialField scans raw starting at from for the next `"key":"value"`
// field. It returns the key, the value's raw [vStart,vEnd) range, whether the
// value's closing quote was seen (closed), and ok=false when no field boundary
// is found in the remaining input. The value range excludes surrounding quotes.
func scanPartialField(raw string, from int) (key string, vStart, vEnd int, closed, ok bool) {
	keyOpen := nextQuote(raw, from)
	if keyOpen < 0 {
		return "", 0, 0, false, false
	}
	keyClose := nextQuote(raw, keyOpen+1)
	if keyClose < 0 {
		return "", 0, 0, false, false // key not yet terminated
	}
	valOpen := nextQuote(raw, keyClose+1)
	if valOpen < 0 {
		return "", 0, 0, false, false // value opening quote not yet present
	}
	valClose := nextUnescapedQuote(raw, valOpen+1)
	if valClose < 0 {
		// Value runs to end-of-input: still open/growing.
		return raw[keyOpen+1 : keyClose], valOpen + 1, len(raw), false, true
	}
	return raw[keyOpen+1 : keyClose], valOpen + 1, valClose, true, true
}

// nextQuote returns the index of the next `"` at or after pos, or -1.
func nextQuote(raw string, pos int) int {
	for i := pos; i < len(raw); i++ {
		if raw[i] == '"' {
			return i
		}
	}
	return -1
}

// nextUnescapedQuote returns the index of the next `"` not preceded by an odd
// run of backslashes (i.e. a real string terminator), or -1.
func nextUnescapedQuote(raw string, pos int) int {
	for i := pos; i < len(raw); i++ {
		switch raw[i] {
		case '\\':
			i++ // skip the escaped character
		case '"':
			return i
		}
	}
	return -1
}

// SetArgs parses and stores the structured arguments for renderer use.
func (tc *ToolExecutionComponent) SetArgs(args map[string]any) {
	tc.args = args
	tc.invalidateBody()
	tc.updateBox()
	tc.Invalidate()
}

// SetArgsJSON parses JSON arguments and stores them for the renderer.
// When the JSON is successfully parsed, args are marked as complete.
func (tc *ToolExecutionComponent) SetArgsJSON(argsJSON string) {
	tc.partialRaw, tc.partialPos, tc.partialKey, tc.partialVFrom = "", 0, "", 0
	var args map[string]any
	if err := json.Unmarshal([]byte(argsJSON), &args); err == nil {
		tc.args = args
		tc.argsComplete = true
	}
	tc.toolArgs = FormatToolArgs(tc.toolName, argsJSON)
	tc.invalidateBody()
	tc.updateBox()
	tc.Invalidate()
}

// SetOnInvalidate registers a callback invoked whenever the component's
// internal state changes, allowing the owning viewport to invalidate its
// render cache.
func (tc *ToolExecutionComponent) SetOnInvalidate(fn func()) {
	tc.onInvalidate = fn
}

// SetToolViewPolicy attaches the global tool-view policy (effective expand
// mode + preview line count) from the owning ChatViewport. Must be called
// before the first render so the widget honours the config/Ctrl+O state.
func (tc *ToolExecutionComponent) SetToolViewPolicy(p ToolViewPolicy) {
	tc.viewPolicy = p
	tc.invalidateBody()
	tc.updateBox()
	tc.Invalidate()
	if tc.onInvalidate != nil {
		tc.onInvalidate()
	}
}

// SetAgentLabel sets the display label prefix for the tool widget.
func (tc *ToolExecutionComponent) SetAgentLabel(label string) {
	tc.agentLabel = label
	tc.updateBox()
	tc.Invalidate()
	if tc.onInvalidate != nil {
		tc.onInvalidate()
	}
}

// SetOutput sets the tool's output text.
func (tc *ToolExecutionComponent) SetOutput(output string) {
	tc.output = output
	tc.outputBytes = len(output)
	tc.outputLines = strings.Count(output, "\n")
	if output != "" && !strings.HasSuffix(output, "\n") {
		tc.outputLines++ // final partial line
	}
	tc.invalidateBody()
	tc.updateBox()
	tc.Invalidate()
	if tc.onInvalidate != nil {
		tc.onInvalidate()
	}
}

// Status returns the current execution status.
func (tc *ToolExecutionComponent) Status() ToolStatus {
	return tc.status
}

// ToolName returns the name of the tool being executed.
func (tc *ToolExecutionComponent) ToolName() string {
	return tc.toolName
}

// ArgsComplete returns whether all tool call arguments have been received.
func (tc *ToolExecutionComponent) ArgsComplete() bool {
	return tc.argsComplete
}

// IsPartial reports whether the widget is still streaming/running and its
// output is a partial snapshot (e.g. streamed progress from a long-running
// tool). The final result clears it.
func (tc *ToolExecutionComponent) IsPartial() bool {
	return tc.isPartial
}

// SetStatus changes the execution status.
func (tc *ToolExecutionComponent) SetStatus(status ToolStatus) {
	tc.status = status
	if status == ToolSuccess || status == ToolError {
		tc.isPartial = false
	}
	tc.invalidateBody()
	tc.updateBox()
	tc.Invalidate()
	if tc.onInvalidate != nil {
		tc.onInvalidate()
	}
}

// SetPartial marks the component as still streaming/running.
func (tc *ToolExecutionComponent) SetPartial(partial bool) {
	tc.isPartial = partial
	tc.invalidateBody()
	tc.updateBox()
	tc.Invalidate()
	if tc.onInvalidate != nil {
		tc.onInvalidate()
	}
}

// SetDuration sets the execution duration string (e.g., "0.04s").
func (tc *ToolExecutionComponent) SetDuration(d string) {
	tc.duration = d
	tc.updateBox()
	tc.Invalidate()
	if tc.onInvalidate != nil {
		tc.onInvalidate()
	}
}

// ── Rendering (delegated to Container which renders spacer + box children) ──

// Invalidate clears cached rendering state.
func (tc *ToolExecutionComponent) Invalidate() {
	tc.Container.Invalidate()
}

// Render renders the tool execution widget. While the tool is running, the
// elapsed duration is recomputed on every frame so the user sees live timing.
func (tc *ToolExecutionComponent) Render(width int) []string {
	if tc.status == ToolPending || tc.status == ToolRunning {
		if !tc.startTime.IsZero() {
			elapsed := fmt.Sprintf("elapsed %s", formatDuration(time.Since(tc.startTime)))
			if tc.box.duration != elapsed {
				// Rebuild the whole box so the spinner icon and duration both
				// refresh on the next render cycle.
				tc.updateBox()
			}
		}
	}
	return tc.Container.Render(width)
}

// ── Helpers ──

func (tc *ToolExecutionComponent) bgColor() string {
	switch tc.status {
	case ToolPending, ToolRunning:
		return TheTheme.ColorHex("tool_pending_bg")
	case ToolSuccess:
		return TheTheme.ColorHex("tool_success_bg")
	case ToolError:
		return TheTheme.ColorHex("tool_error_bg")
	default:
		return ""
	}
}

func (tc *ToolExecutionComponent) statusIcon() (icon string, color string) {
	switch tc.status {
	case ToolPending:
		return "◉", TheTheme.ColorHex("tool_running")
	case ToolRunning:
		if frame := CurrentSpinnerFrame(); frame != "" {
			return frame, TheTheme.ColorHex("tool_running")
		}
		return "⟳", TheTheme.ColorHex("tool_running")
	case ToolSuccess:
		return "✓", TheTheme.ColorHex("tool_success")
	case ToolError:
		return "✗", TheTheme.ColorHex("tool_error")
	default:
		return "·", TheTheme.ColorHex("system_msg")
	}
}

// effectiveExpanded returns the effective expanded state for the widget,
// considering both the per-widget toggle (tc.expanded) and the global view
// policy. For read tools, the showRead policy prevents global expansion when
// false so read output stays silent by default, while the per-widget toggle
// (Ctrl+O/Enter on the block) still works.
func (tc *ToolExecutionComponent) effectiveExpanded() bool {
	// An explicit per-widget toggle wins over the global policy in both
	// directions, and persists across streaming re-renders.
	if tc.expandedSet {
		return tc.expanded
	}
	if tc.viewPolicy == nil {
		return tc.expanded
	}
	if !tc.viewPolicy.EffectiveToolsExpanded() {
		return tc.expanded
	}
	if tc.toolName == "read" && !tc.viewPolicy.ShowReadContent() {
		return tc.expanded
	}
	return true
}

func (tc *ToolExecutionComponent) bgANSI() string {
	bgHex := tc.bgColor()
	if bgHex == "" {
		return ""
	}
	return ansi.Bg(bgHex)
}

// HandleInput processes key events for expand/collapse.
func (tc *ToolExecutionComponent) HandleInput(data string) {
	if matchesKey(data, "ctrl+o") || matchesKey(data, "enter") {
		tc.setExpandedExplicit(!tc.effectiveExpanded())
	}
}

// ── ToolArgs formatting ──

// FormatToolArgs formats tool arguments for display.
func FormatToolArgs(name string, argsJSON string) string {
	switch name {
	case "read":
		return formatReadFileArgs(argsJSON)
	case "write":
		return extractJSONField(argsJSON, "path")
	case "edit":
		path := extractJSONField(argsJSON, "path")
		op := extractJSONField(argsJSON, "operation")
		if op != "" {
			return fmt.Sprintf("%s (%s)", path, op)
		}
		return path
	case "search":
		pattern := extractJSONField(argsJSON, "pattern")
		path := extractJSONField(argsJSON, "path")
		if path != "" {
			return fmt.Sprintf("%s in %s", pattern, path)
		}
		return pattern
	case "bash":
		cmd := extractJSONField(argsJSON, "command")
		if len(cmd) > 60 {
			cmd = cmd[:57] + "..."
		}
		return cmd
	default:
		fields := []string{"path", "command", "name", "pattern", "id"}
		for _, f := range fields {
			if v := extractJSONField(argsJSON, f); v != "" {
				return v
			}
		}
		return ""
	}
}

// extractJSONField extracts a string field from a JSON string using proper JSON parsing.
func extractJSONField(raw, field string) string {
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return ""
	}
	if v, ok := m[field].(string); ok {
		return v
	}
	return ""
}

// formatReadFileArgs formats read arguments as path[:start][:end|+max].
func formatReadFileArgs(argsJSON string) string {
	path := extractJSONField(argsJSON, "path")
	start := extractJSONIntField(argsJSON, "start_line")
	end := extractJSONIntField(argsJSON, "end_line")
	maxLines := extractJSONIntField(argsJSON, "max_lines")
	if start == "" && end == "" && maxLines == "" {
		return path
	}
	parts := []string{path}
	if start != "" {
		parts = append(parts, start)
	} else {
		parts = append(parts, "1")
	}
	if end != "" {
		parts = append(parts, end)
	} else if maxLines != "" {
		parts = append(parts, "+"+maxLines)
	}
	return strings.Join(parts, ":")
}

// extractJSONIntField extracts an integer field from a JSON string.
func extractJSONIntField(raw, field string) string {
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return ""
	}
	switch v := m[field].(type) {
	case float64:
		if v == float64(int(v)) {
			return fmt.Sprintf("%d", int(v))
		}
		return fmt.Sprintf("%g", v)
	case string:
		return v
	}
	return ""
}

// formatDuration returns a concise human-readable duration string.
// Sub-second values show two decimals; seconds and up show one decimal.
func formatDuration(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%.2fs", d.Seconds())
	}
	return fmt.Sprintf("%.1fs", d.Seconds())
}
