// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package plugins

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/pijalu/goa/internal/agentic"
)

// hookPointIDs maps every agentic.HookPoint constant to the plugin-facing id
// string used by goa.registerHook({point: ...}). The agentic constants' VALUES
// are already the wire ids, so the map is an identity by construction — but it
// is spelled out and exhaustively tested (TestHookPointMapExhaustive) so
// renaming a constant or adding a point breaks a test, not production
// silently.
var hookPointIDs = map[string]agentic.HookPoint{
	string(agentic.HookMessagePreSend): agentic.HookMessagePreSend,
	string(agentic.HookToolCallPre):    agentic.HookToolCallPre,
	string(agentic.HookToolCallPost):   agentic.HookToolCallPost,
	string(agentic.HookReplyPre):       agentic.HookReplyPre,
	string(agentic.HookReplyDelta):     agentic.HookReplyDelta,
	string(agentic.HookLLMError):       agentic.HookLLMError,
}

// ValidHookPoints returns the sorted hook point ids accepted by
// goa.registerHook. Used for validation error messages and tests.
func ValidHookPoints() []string {
	out := make([]string, 0, len(hookPointIDs))
	for id := range hookPointIDs {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// isValidHookPoint reports whether id names a known interception point.
func isValidHookPoint(id string) bool {
	_, ok := hookPointIDs[id]
	return ok
}

const (
	// defaultPerHandlerTimeout bounds each intercept handler's share of the
	// chain budget. Goja calls cannot be preempted, so this is enforced by
	// skipping handlers that would start after expiry — never by interrupting
	// a running one (availability beats enforcement; these are user-installed
	// plugins, not a security boundary).
	defaultPerHandlerTimeout = 100 * time.Millisecond
	// minHandlerBudget: never START a new handler with less wall-time budget
	// left than this; the marginal handler could not finish meaningfully.
	minHandlerBudget = 5 * time.Millisecond
	// maxInterceptsPerTurn trips the reply-delta circuit breaker (§3.9):
	// more synchronous intercept invocations at one point within a single
	// turn bypass hooks for the remainder of that turn, protecting turn
	// latency from pathological JS.
	maxInterceptsPerTurn = 500
	// burstEpisodeGap approximates a turn boundary for the circuit breaker:
	// intercept invocations of ANY point separated by more than this are a
	// new episode (deltas inside one burst arrive milliseconds apart; turns
	// are seconds apart). Needed because the other points between two delta
	// bursts usually carry no hooks and would otherwise never reset the
	// window.
	burstEpisodeGap = time.Second
)

// HookStat accumulates per-point perf instrumentation (§3.6).
type HookStat struct {
	Invocations int64 // Intercept calls that reached folding
	Denied      int64 // chains ending in a denial
	TimedOut    int64 // chains cut short by budget/cancellation checks
	MaxNanos    int64 // rolling max single-handler wall time
}

// foldState accumulates the outcome of one intercept chain.
type foldState struct {
	working  map[string]any // evolving payload copy
	denied   bool
	reason   string
	modified bool
	timedOut bool
}

// HookSink adapts the plugin HookRegistry to agentic.PluginHookSink. One
// long-lived sink exists from boot; agents hold it via their config while
// plugins register into the live registry later — a swap-free design where
// registrations become visible to agents immediately.
//
// Concurrency: tool execution and sub-agents can call Intercept/Notify
// concurrently; internal counters are mutex-guarded and handler execution
// serializes on the global VM lock inside bridge-created closures. Payloads
// are deep-copied through JSON before any handler sees them, honoring the
// PluginHookSink ownership contract ("treat payload maps as owned by the
// caller after the call returns").
//
// Notify delivery runs on ONE dedicated worker goroutine (FIFO), not the
// plugin timer Scheduler. Root-caused during M2: every scheduler callback is
// invoked UNDER vmMu by invokeSafe, while bridge-created hook handlers MUST
// acquire vmMu themselves (the buildToolWrapper discipline — the intercept
// path calls them from agent goroutines that hold no lock). Routing handlers
// through the scheduler re-acquired the non-reentrant VM lock on the same
// goroutine and deadlocked permanently. The dedicated worker keeps the
// plan's intent — one background goroutine total, never one per
// notification — while letting handlers self-lock.
type HookSink struct {
	registry *HookRegistry
	logger   LoggerAPI

	tasks     chan func() // notify delivery tasks; drained by the worker goroutine
	done      chan struct{}
	closeOnce sync.Once

	mu         sync.Mutex
	perHandler time.Duration              // test injection; <=0 → defaultPerHandlerTimeout
	burstPoint agentic.HookPoint          // last intercepted point (turn heuristic)
	burstLast  time.Time                  // last intercept invocation (episode gap)
	burstCount map[agentic.HookPoint]int  // intercept invocations since last episode reset
	burstWarn  map[agentic.HookPoint]bool // one warn per breaker episode
	budgetWarn map[agentic.HookPoint]bool // one warn per budget-exhaustion episode
	stats      map[agentic.HookPoint]*HookStat
}

// hookTaskQueueDepth bounds queued notify deliveries. The circuit breaker
// caps a single burst at maxInterceptsPerTurn, so this leaves headroom;
// beyond it notifications are dropped (observers are best-effort).
const hookTaskQueueDepth = 1024

// NewHookSink builds the adapter and starts its delivery worker. Close (via
// Cleanup in tests) stops the worker; production sinks live for the process.
func NewHookSink(registry *HookRegistry, logger LoggerAPI) *HookSink {
	s := &HookSink{
		registry:   registry,
		logger:     logger,
		tasks:      make(chan func(), hookTaskQueueDepth),
		done:       make(chan struct{}),
		burstCount: make(map[agentic.HookPoint]int),
		burstWarn:  make(map[agentic.HookPoint]bool),
		budgetWarn: make(map[agentic.HookPoint]bool),
		stats:      make(map[agentic.HookPoint]*HookStat),
	}
	go s.drainTasks()
	return s
}

// drainTasks executes queued notify deliveries in FIFO order until Close.
func (s *HookSink) drainTasks() {
	for {
		select {
		case <-s.done:
			return
		case fn := <-s.tasks:
			fn()
		}
	}
}

// enqueueTask queues one delivery, dropping with a debug note when the sink
// is closed or the queue is saturated (notify observers are best-effort).
func (s *HookSink) enqueueTask(fn func()) {
	select {
	case <-s.done:
		return
	default:
	}
	select {
	case s.tasks <- fn:
	default:
		s.debugf("hook notify queue full — dropping one delivery")
	}
}

// Close stops the background delivery worker. Idempotent; safe for tests.
func (s *HookSink) Close() {
	s.closeOnce.Do(func() { close(s.done) })
}

// Intercept implements agentic.PluginHookSink: it runs the point's intercept
// chain synchronously in priority order, folds handler outputs into a working
// copy of the payload, then dispatches the notify chain against the FINAL
// payload asynchronously — observers audit what actually took effect.
//
// With zero registered hooks this costs one empty-map lookup (zero-overhead
// rule); the circuit breaker path schedules notifies untouched.
func (s *HookSink) Intercept(ctx context.Context, point agentic.HookPoint, payload map[string]any) (agentic.HookDecision, map[string]any, string) {
	chain := s.registry.Snapshot(string(point))
	if len(chain) == 0 {
		return agentic.HookPass, nil, ""
	}
	intercepts := filterMode(chain, HookIntercept)
	notifies := filterMode(chain, HookNotify)
	switch {
	case len(intercepts) == 0 && len(notifies) == 0:
		return agentic.HookPass, nil, ""
	case len(intercepts) == 0:
		// Notify-only point: no working copy is folded, so observers audit
		// the untouched payload with a single marshal. This also keeps the
		// hot delta path free of the fold clone when only auditors listen.
		s.recordPassStats(point)
		s.scheduleNotifies(point, payload, notifies)
		return agentic.HookPass, nil, ""
	case s.breakerTripped(point):
		// Bypassed for this turn; observers still audit the untouched payload.
		s.recordPassStats(point)
		s.scheduleNotifies(point, payload, notifies)
		return agentic.HookPass, nil, ""
	}

	fold := s.runInterceptChain(ctx, point, intercepts, payload)
	if fold.working == nil {
		// Payload not JSON-marshalable: payloads are built agent-side and
		// always JSON-safe, so this is an upstream bug. Degrade to
		// pass-through rather than crash the loop.
		s.warnf("hook %s: payload not JSON-marshalable — passing through unchanged", point)
		return agentic.HookPass, nil, ""
	}
	s.recordStats(point, &fold)
	s.scheduleNotifies(point, fold.working, notifies)
	switch {
	case fold.denied:
		return agentic.HookDenied, nil, fold.reason
	case fold.modified:
		return agentic.HookModified, fold.working, ""
	default:
		return agentic.HookPass, nil, ""
	}
}

// runInterceptChain folds every intercept entry into a working copy of the
// payload under the per-chain deadline. The deadline cannot preempt a running
// goja call; it gates STARTING further handlers (skip-after-expiry), and each
// handler's wall time is checked against the remaining budget before
// invocation (<5ms left ⇒ don't start it).
func (s *HookSink) runInterceptChain(ctx context.Context, point agentic.HookPoint, entries []HookEntry, payload map[string]any) foldState {
	var fold foldState
	if !fold.load(payload) {
		return fold
	}
	deadline := time.Now().Add(s.handlerTimeout() * time.Duration(len(entries)))
	for i, entry := range entries {
		if err := ctx.Err(); err != nil {
			fold.timedOut = true
			s.debugf("plugin hook %s: canceled before handler %d/%d", point, i, len(entries))
			return fold
		}
		if remaining := time.Until(deadline); remaining < minHandlerBudget {
			fold.timedOut = true
			s.warnOnce(s.budgetWarn, point,
				"plugin hook %s: intercept budget exhausted after %d/%d handlers — skipping the rest of this call", point, i, len(entries))
			return fold
		}
		handlerStart := time.Now()
		out := s.invoke(entry, clonePayload(fold.working))
		s.updateMaxNanos(point, time.Since(handlerStart))
		if out == nil {
			continue // failed/panicking/no-op handler ⇒ pass-through
		}
		if applyOne(&fold, out) {
			break // denial short-circuits the remaining chain
		}
	}
	return fold
}

// load deep-copies the incoming payload into the fold's working state.
// Returns false when the payload cannot be marshaled (caller degrades to
// pass-through).
func (f *foldState) load(payload map[string]any) bool {
	f.working = clonePayload(payload)
	return f.working != nil
}

// applyOne folds ONE handler output into the fold state: object ⇒
// shallow-merge into the working map (decision becomes Modified),
// {deny:true,...} ⇒ short-circuit with the recorded reason. Extracted to keep
// the fold loop inside the complexity budget. Returns true when folding must
// stop (denial).
func applyOne(fold *foldState, out map[string]any) bool {
	if deniedBy(out) {
		fold.denied = true
		fold.reason, _ = out["reason"].(string)
		return true
	}
	for k, v := range out {
		fold.working[k] = v
	}
	fold.modified = true
	return false
}

// deniedBy reports whether a handler output vetoes the action. Only a real
// boolean true counts — truthy strings/numbers do not deny.
func deniedBy(out map[string]any) bool {
	deny, _ := out["deny"].(bool)
	return deny
}

// invoke runs one hook entry defensively: panics are contained (warn with
// plugin/name, treat as pass-through for that handler) so a broken plugin
// cannot take down the calling goroutine. A nil payload yields no-op.
func (s *HookSink) invoke(entry HookEntry, payload map[string]any) (out map[string]any) {
	if payload == nil {
		return nil
	}
	defer func() {
		if r := recover(); r != nil {
			s.warnf("plugin hook %s/%s panicked: %v — treating as pass-through", entry.Spec.PluginID, entry.Spec.Name, r)
			out = nil
		}
	}()
	return entry.Handler(payload)
}

// Notify implements agentic.PluginHookSink: fire-and-forget delivery of a
// payload snapshot to the notify chain. Never blocks the caller beyond one
// registry snapshot + JSON marshal.
//
// Two deliberate deviations from "notify-mode only":
//   - llm:error (§3.8 downgrade): intercept-mode registrations run too, but
//     only their returned `note` survives, appended to the delivered error
//     text (mirrors shell hooks' additionalContext). Mutating retry policy is
//     explicitly deferred (§8).
//   - reply:delta thinking deltas carry state:"thinking"; intercept handlers
//     at reply:delta are content rewriters and do NOT receive notify
//     deliveries (rewriting reasoning risks breaking reasoning-signature
//     verification). This falls out naturally: Notify never runs intercept
//     entries outside llm:error.
func (s *HookSink) Notify(point agentic.HookPoint, payload map[string]any) {
	chain := s.registry.Snapshot(string(point))
	if len(chain) == 0 {
		return
	}
	var entries []HookEntry
	for _, e := range chain {
		switch {
		case e.Spec.Mode == HookNotify:
			entries = append(entries, e)
		case e.Spec.Mode == HookIntercept && point == agentic.HookLLMError:
			entries = append(entries, e)
		}
	}
	s.scheduleNotifies(point, payload, entries)
}

// scheduleNotifies hands the final payload snapshot to the delivery worker as
// ONE queued task; the worker delivers a fresh copy to each handler so
// handlers cannot interfere through shared state. See the HookSink doc
// comment for why this is a dedicated goroutine rather than the Scheduler.
func (s *HookSink) scheduleNotifies(point agentic.HookPoint, final map[string]any, entries []HookEntry) {
	if len(entries) == 0 || final == nil {
		return
	}
	snap, err := json.Marshal(final)
	if err != nil {
		s.warnf("hook %s: notify payload not marshalable — dropped", point)
		return
	}
	s.enqueueTask(func() { s.dispatchNotifyTask(point, entries, snap) })
}

// dispatchNotifyTask runs on the delivery worker goroutine (NOT holding the
// VM lock; bridge-created handlers acquire it themselves).
func (s *HookSink) dispatchNotifyTask(point agentic.HookPoint, entries []HookEntry, snap []byte) {
	if point == agentic.HookLLMError {
		s.dispatchLLMErrorTask(entries, snap)
		return
	}
	for _, entry := range entries {
		payload := unmarshalSnapshot(snap)
		if payload == nil {
			return
		}
		_ = s.invoke(entry, payload) // result intentionally discarded
	}
}

// dispatchLLMErrorTask applies the §3.8 downgrade rule: intercept entries run
// first but only their `note` fields survive; collected notes are appended to
// the model-visible error text, and notify entries then receive the enriched
// delivery.
func (s *HookSink) dispatchLLMErrorTask(entries []HookEntry, snap []byte) {
	base := unmarshalSnapshot(snap)
	if base == nil {
		return
	}
	var notes []string
	for _, entry := range entries {
		if entry.Spec.Mode != HookIntercept {
			continue
		}
		out := s.invoke(entry, unmarshalSnapshot(snap))
		if out == nil {
			continue
		}
		if note, _ := out["note"].(string); note != "" {
			notes = append(notes, note)
		}
	}
	if len(notes) > 0 {
		errText, _ := base["error"].(string)
		base["error"] = strings.TrimSpace(errText + "\n[plugin] " + strings.Join(notes, "\n[plugin] "))
	}
	for _, entry := range entries {
		if entry.Spec.Mode == HookNotify {
			_ = s.invoke(entry, clonePayload(base))
		}
	}
}

// breakerTripped implements the reply-delta circuit breaker (§3.9). An
// "episode" (≈ one turn) ends when interception moves to a DIFFERENT point or
// pauses longer than burstEpisodeGap — between two delta bursts of a turn the
// loop always passes other points, and separate turns are seconds apart.
// Returns true when the current point exceeded maxInterceptsPerTurn within an
// episode (hooks bypassed, one warn logged per episode).
func (s *HookSink) breakerTripped(point agentic.HookPoint) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	if point != s.burstPoint || now.Sub(s.burstLast) > burstEpisodeGap {
		s.burstPoint = point
		clear(s.burstCount)
		clear(s.burstWarn)
		clear(s.budgetWarn)
	}
	s.burstLast = now
	s.burstCount[point]++
	if s.burstCount[point] <= maxInterceptsPerTurn {
		return false
	}
	if !s.burstWarn[point] {
		s.burstWarn[point] = true
		s.warnLocked(fmt.Sprintf("hook circuit breaker: >%d intercept invocations at %s within one turn — bypassing plugin hooks until the next turn", maxInterceptsPerTurn, point))
	}
	return true
}

// recordStats updates the per-point instrumentation counters (§3.6).
func (s *HookSink) recordStats(point agentic.HookPoint, fold *foldState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.statFor(point)
	st.Invocations++
	if fold.denied {
		st.Denied++
	}
	if fold.timedOut {
		st.TimedOut++
	}
}

// recordPassStats counts a pass-through Intercept call (notify-only point or
// breaker bypass) without touching the fold counters.
func (s *HookSink) recordPassStats(point agentic.HookPoint) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.statFor(point).Invocations++
}

// updateMaxNanos records one handler's wall time into the rolling max.
func (s *HookSink) updateMaxNanos(point agentic.HookPoint, d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.statFor(point)
	if n := d.Nanoseconds(); n > st.MaxNanos {
		st.MaxNanos = n
	}
}

// statFor returns (creating if needed) the stat bucket for a point.
// Caller must hold s.mu.
func (s *HookSink) statFor(point agentic.HookPoint) *HookStat {
	st, ok := s.stats[point]
	if !ok {
		st = &HookStat{}
		s.stats[point] = st
	}
	return st
}

// Stats returns a copy of the per-point instrumentation snapshot (debug
// command surface, §3.6).
func (s *HookSink) Stats() map[agentic.HookPoint]HookStat {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[agentic.HookPoint]HookStat, len(s.stats))
	for p, st := range s.stats {
		out[p] = *st
	}
	return out
}

// handlerTimeout returns the effective per-handler budget share.
func (s *HookSink) handlerTimeout() time.Duration {
	if s.perHandler > 0 {
		return s.perHandler
	}
	return defaultPerHandlerTimeout
}

// warnOnce logs warn at most once per point per burst episode. Caller passes
// one of the mutex-guarded seen-maps; the lock is taken here so callers stay
// lock-free.
func (s *HookSink) warnOnce(seen map[agentic.HookPoint]bool, point agentic.HookPoint, format string, args ...any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if seen[point] {
		return
	}
	seen[point] = true
	s.warnLocked(fmt.Sprintf(format, args...))
}

// warnLocked emits a warning; caller holds s.mu (or owns exclusivity).
func (s *HookSink) warnLocked(msg string) {
	if s.logger.Warn != nil {
		s.logger.Warn(msg)
	}
}

func (s *HookSink) warnf(format string, args ...any) {
	if s.logger.Warn != nil {
		s.logger.Warn(fmt.Sprintf(format, args...))
	}
}

func (s *HookSink) debugf(format string, args ...any) {
	if s.logger.Debug != nil {
		s.logger.Debug(fmt.Sprintf(format, args...))
	}
}

// filterMode keeps entries matching mode. The input is already a private
// snapshot copy, so an all-match (or single-entry) chain is returned as-is
// without a second allocation — the common case for one-hook plugins.
func filterMode(chain []HookEntry, mode HookMode) []HookEntry {
	n := 0
	for _, e := range chain {
		if e.Spec.Mode == mode {
			n++
		}
	}
	if n == 0 {
		return nil
	}
	if n == len(chain) {
		return chain
	}
	out := make([]HookEntry, 0, n)
	for _, e := range chain {
		if e.Spec.Mode == mode {
			out = append(out, e)
		}
	}
	return out
}

// clonePayload deep-copies a payload through JSON so handlers can never alias
// caller-owned maps and only JSON-safe data ever crosses into a VM. Returns
// nil when marshaling fails (callers degrade to pass-through).
func clonePayload(payload map[string]any) map[string]any {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil
	}
	out := make(map[string]any)
	if err := json.Unmarshal(data, &out); err != nil {
		return nil
	}
	return out
}

// unmarshalSnapshot materializes one private copy of a notify snapshot.
func unmarshalSnapshot(snap []byte) map[string]any {
	out := make(map[string]any)
	if err := json.Unmarshal(snap, &out); err != nil {
		return nil
	}
	return out
}
