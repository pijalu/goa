// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

// Package tooltracker owns the lifecycle of tool-call widgets for one
// conversation stream.
//
// It exists to fix the "stuck on write" bug: when a provider streams a
// tool-call's arguments with an EMPTY ToolCallID (common — many providers
// ship the id only on the *completed* call) and then emits the completed
// call with the real id, the previous reconciliation logic created a SECOND
// widget and orphaned the first one in Pending state forever (its elapsed
// timer ran to infinity). The tracker is the SOLE creator of
// ToolExecutionComponents for its stream and guarantees exactly one widget
// per logical tool call via late-id adoption.
//
// The tracker has a single responsibility — widget identity/lifecycle — and
// depends only on an injected WidgetCreator so it can serve both the main
// (foreground) conversation path and the per-agent orchestrator path without
// knowing about *tui.ChatViewport.
package tooltracker

import (
	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/tui"
)

// WidgetCreator creates a fresh tool widget for a tool name + raw JSON input.
// Injected so the tracker has no dependency on the chat viewport and can be
// reused by the orchestrator path (which labels widgets per agent).
type WidgetCreator func(name, input string) *tui.ToolExecutionComponent

// Tracker tracks in-flight tool widgets for one conversation stream. It is
// not safe for concurrent use; the app drives it exclusively from the engine
// command loop.
type Tracker struct {
	create WidgetCreator
	// byID indexes widgets whose ToolCallID is known.
	byID map[string]*tui.ToolExecutionComponent
	// noID holds widgets registered while their ToolCallID was empty, in
	// insertion order (oldest first). Used for FIFO matching of empty-id
	// results and for late-id adoption.
	noID []*tui.ToolExecutionComponent
}

// New returns a Tracker that uses create to instantiate widgets.
func New(create WidgetCreator) *Tracker {
	if create == nil {
		create = func(string, string) *tui.ToolExecutionComponent { return nil }
	}
	return &Tracker{
		create: create,
		byID:   map[string]*tui.ToolExecutionComponent{},
	}
}

// OnCall processes an EventToolCall (delta or final). It returns the canonical
// widget for the call and created=true if a new widget was instantiated.
//
// Late-id adoption: when a final (non-delta) call arrives with a real id but
// the streaming widget was registered with an empty id, the existing widget
// is adopted (re-indexed under the real id) instead of creating a duplicate.
func (t *Tracker) OnCall(ev *agentic.OutputEvent) (*tui.ToolExecutionComponent, bool) {
	if ev == nil {
		return nil, false
	}
	if ev.IsDelta {
		return t.onCallDelta(ev)
	}
	return t.onCallFinal(ev)
}

// OnProgress processes an EventToolProgress: refreshes the widget's partial
// output without retiring it. Returns the widget, or nil if none matched.
func (t *Tracker) OnProgress(ev *agentic.OutputEvent) *tui.ToolExecutionComponent {
	if ev == nil {
		return nil
	}
	if tc, ok := t.byID[ev.ToolCallID]; ok && ev.ToolCallID != "" {
		t.applyProgress(tc, ev.Text)
		return tc
	}
	if tc := t.findRunningNoID(ev.ToolName); tc != nil {
		t.applyProgress(tc, ev.Text)
		return tc
	}
	return nil
}

// OnResult processes an EventToolResult: applies the result and retires the
// widget from tracking. Returns the widget, or nil if none matched. The error
// status is derived from the result text ("Error:"/"error:" prefix).
func (t *Tracker) OnResult(ev *agentic.OutputEvent) *tui.ToolExecutionComponent {
	return t.onResult(ev, isErrorResult(textOrEmpty(ev)))
}

// OnResultOK is like OnResult but uses an explicit success/error flag instead
// of the text heuristic. Used by paths that know the outcome authoritatively
// (e.g. the multi-agent orchestrator, which receives a structured ok bool).
func (t *Tracker) OnResultOK(ev *agentic.OutputEvent, ok bool) *tui.ToolExecutionComponent {
	return t.onResult(ev, !ok)
}

func (t *Tracker) onResult(ev *agentic.OutputEvent, isErr bool) *tui.ToolExecutionComponent {
	if ev == nil {
		return nil
	}
	tc := t.findForResult(ev.ToolCallID, ev.ToolName)
	if tc == nil {
		return nil
	}
	t.applyResult(tc, ev.Text, isErr)
	t.retire(tc)
	return tc
}

func textOrEmpty(ev *agentic.OutputEvent) string {
	if ev == nil {
		return ""
	}
	return ev.Text
}

// FailAll marks every still-tracked non-terminal widget as interrupted. Used
// at EventEnd so cancelled tools show ✗ instead of hanging in Running.
func (t *Tracker) FailAll() {
	for _, tc := range t.byID {
		t.failIfInflight(tc)
	}
	for _, tc := range t.noID {
		t.failIfInflight(tc)
	}
	t.byID = map[string]*tui.ToolExecutionComponent{}
	t.noID = nil
}

// Reset drops all tracking without touching the widgets. Used when a stream
// is torn down (session clear) and the widgets are discarded wholesale.
func (t *Tracker) Reset() {
	t.byID = map[string]*tui.ToolExecutionComponent{}
	t.noID = nil
}

// ── delta path ──

func (t *Tracker) onCallDelta(ev *agentic.OutputEvent) (*tui.ToolExecutionComponent, bool) {
	if ev.ToolCallID != "" {
		if tc := t.byID[ev.ToolCallID]; tc != nil {
			tc.SetArgsPartial(ev.ToolInput)
			return tc, false
		}
	}
	// Empty (or unknown) id: update the most recent still-streaming widget of
	// the same tool name, if one exists.
	if tc := t.findStreamingNoID(ev.ToolName); tc != nil {
		tc.SetArgsPartial(ev.ToolInput)
		return tc, false
	}
	// First partial for this call: create and register.
	tc := t.create(ev.ToolName, ev.ToolInput)
	if tc == nil {
		return nil, false
	}
	tc.SetArgsPartial(ev.ToolInput)
	t.register(tc, ev.ToolCallID)
	return tc, true
}

// ── final (non-delta) path ──

func (t *Tracker) onCallFinal(ev *agentic.OutputEvent) (*tui.ToolExecutionComponent, bool) {
	if ev.ToolCallID != "" {
		// Already tracked under this id: finalize in place.
		if tc := t.byID[ev.ToolCallID]; tc != nil {
			t.finalize(tc, ev.ToolInput)
			return tc, false
		}
		// Late-id adoption: a streaming widget exists under an empty id for the
		// same tool name. Re-index it under the real id instead of duplicating.
		if adopted := t.adoptStreamingNoID(ev.ToolName, ev.ToolCallID); adopted != nil {
			t.finalize(adopted, ev.ToolInput)
			return adopted, false
		}
		// No prior partial: brand-new id'd call.
		tc := t.create(ev.ToolName, ev.ToolInput)
		if tc == nil {
			return nil, false
		}
		t.finalize(tc, ev.ToolInput)
		t.byID[ev.ToolCallID] = tc
		return tc, true
	}
	// Empty-id final: complete the most recent streaming widget of this name,
	// else create one.
	if tc := t.findStreamingNoID(ev.ToolName); tc != nil {
		t.finalize(tc, ev.ToolInput)
		return tc, false
	}
	tc := t.create(ev.ToolName, ev.ToolInput)
	if tc == nil {
		return nil, false
	}
	t.finalize(tc, ev.ToolInput)
	t.noID = append(t.noID, tc)
	return tc, true
}

// ── registration / lookup helpers ──

func (t *Tracker) register(tc *tui.ToolExecutionComponent, id string) {
	if id != "" {
		t.byID[id] = tc
		return
	}
	t.noID = append(t.noID, tc)
}

// finalize marks a widget's args as complete and transitions it to Running.
func (t *Tracker) finalize(tc *tui.ToolExecutionComponent, input string) {
	tc.SetArgsJSON(input)
	tc.SetArgsComplete()
	tc.SetStatus(tui.ToolRunning)
}

// findStreamingNoID returns the NEWEST empty-id widget of name that is still
// streaming args (Pending, args incomplete). Newest-first because streaming
// deltas target the call currently being produced.
func (t *Tracker) findStreamingNoID(name string) *tui.ToolExecutionComponent {
	for i := len(t.noID) - 1; i >= 0; i-- {
		tc := t.noID[i]
		if tc.ToolName() == name && isStreaming(tc) {
			return tc
		}
	}
	return nil
}

// adoptStreamingNoID removes and returns the OLDEST empty-id widget of name
// that is still streaming (Pending, args incomplete), so it can be re-indexed
// under the late-arriving real id. Oldest-first matches provider call order.
func (t *Tracker) adoptStreamingNoID(name, id string) *tui.ToolExecutionComponent {
	for i, tc := range t.noID {
		if tc.ToolName() == name && isStreaming(tc) {
			t.noID = append(t.noID[:i], t.noID[i+1:]...)
			t.byID[id] = tc
			return tc
		}
	}
	return nil
}

// findRunningNoID returns the newest empty-id widget of name that has reached
// Running (args complete, awaiting/producing result). Used for progress.
func (t *Tracker) findRunningNoID(name string) *tui.ToolExecutionComponent {
	for i := len(t.noID) - 1; i >= 0; i-- {
		tc := t.noID[i]
		if tc.ToolName() == name && tc.Status() == tui.ToolRunning {
			return tc
		}
	}
	return nil
}

// findForResult resolves the widget a result applies to. Prefers an exact id
// match; otherwise falls back to the oldest non-terminal empty-id widget of
// the same name (best-effort for id-less providers). When the result carries
// neither id nor name, any inflight empty-id widget matches (FIFO) — mirroring
// the legacy findPendingTool behaviour for providers that omit both.
func (t *Tracker) findForResult(id, name string) *tui.ToolExecutionComponent {
	if id != "" {
		if tc := t.byID[id]; tc != nil {
			return tc
		}
	}
	for _, tc := range t.noID {
		if !isInflight(tc) {
			continue
		}
		if name == "" || tc.ToolName() == name {
			return tc
		}
	}
	return nil
}

func (t *Tracker) retire(tc *tui.ToolExecutionComponent) {
	for id, w := range t.byID {
		if w == tc {
			delete(t.byID, id)
			return
		}
	}
	for i, w := range t.noID {
		if w == tc {
			t.noID = append(t.noID[:i], t.noID[i+1:]...)
			return
		}
	}
}

// ── apply helpers ──

func (t *Tracker) applyProgress(tc *tui.ToolExecutionComponent, text string) {
	tc.SetOutput(text)
	tc.SetPartial(true)
}

func (t *Tracker) applyResult(tc *tui.ToolExecutionComponent, text string, isErr bool) {
	status := tui.ToolSuccess
	if isErr {
		status = tui.ToolError
	}
	tc.SetOutput(text)
	tc.SetStatus(status)
	tc.SetPartial(false)
}

func (t *Tracker) failIfInflight(tc *tui.ToolExecutionComponent) {
	if tc == nil || !isInflight(tc) {
		return
	}
	if !tc.ArgsComplete() {
		// Arguments never finished streaming: the tool call was canceled
		// before the tool executed — say so explicitly instead of implying
		// work happened and its output was lost.
		tc.SetOutput("(canceled before execution — the tool never ran)")
	} else {
		tc.SetOutput("(interrupted)")
	}
	tc.SetStatus(tui.ToolError)
	tc.SetPartial(false)
}

// ── predicates ──

// isStreaming reports whether a widget is still receiving streamed arguments
// (Pending and args not yet complete). These are the only widgets eligible for
// late-id adoption.
func isStreaming(tc *tui.ToolExecutionComponent) bool {
	return tc.Status() == tui.ToolPending && !tc.ArgsComplete()
}

// isInflight reports whether a widget has not yet reached a terminal state.
func isInflight(tc *tui.ToolExecutionComponent) bool {
	s := tc.Status()
	return s == tui.ToolPending || s == tui.ToolRunning
}

// isErrorResult best-effort classifies a tool result string as an error, so the
// widget shows ✗ instead of ✓. Mirrors the existing app-layer heuristic.
func isErrorResult(text string) bool {
	return text != "" && (startsWith(text, "Error:") || startsWith(text, "error:"))
}

func startsWith(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}
