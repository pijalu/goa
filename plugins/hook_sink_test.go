// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package plugins

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/pijalu/goa/internal/agentic"
)

// newTestSink builds a sink with a fresh registry and no-op logging.
// Handlers are pure Go here — the JS boundary is covered by
// hooks_bridge_test.go.
func newTestSink(t *testing.T) (*HookSink, *HookRegistry) {
	t.Helper()
	reg := NewHookRegistry(nil)
	sink := NewHookSink(reg, LoggerAPI{Warn: func(string) {}, Debug: func(string) {}})
	t.Cleanup(sink.Close)
	return sink, reg
}

func TestHookSinkFoldModified(t *testing.T) {
	sink, reg := newTestSink(t)
	mustRegister(t, reg, HookSpec{PluginID: "p", Name: "a", Point: "message:pre-send", Mode: HookIntercept, Priority: 0},
		func(p map[string]any) map[string]any {
			p["text"] = "A"
			return map[string]any{"text": "A"}
		})
	mustRegister(t, reg, HookSpec{PluginID: "p", Name: "b", Point: "message:pre-send", Mode: HookIntercept, Priority: 10},
		func(p map[string]any) map[string]any { return map[string]any{"turn": 7} })

	decision, result, reason := sink.Intercept(context.Background(), agentic.HookMessagePreSend,
		map[string]any{"text": "original"})
	if decision != agentic.HookModified {
		t.Fatalf("decision = %v, want Modified", decision)
	}
	if reason != "" {
		t.Fatalf("reason = %q on Modified", reason)
	}
	if got := result["text"]; got != "A" {
		t.Fatalf("folded text = %v (handler b must receive handler a's output)", got)
	}
	if got := result["turn"]; got != 7 {
		// Pure-Go handler output merges as-is (int); the JS bridge path
		// delivers JSON-typed values (float64). Both are documented.
		t.Fatalf("merged turn = %v (%T), want 7", got, got)
	}
}

func TestHookSinkUndefinedPassThrough(t *testing.T) {
	sink, reg := newTestSink(t)
	calls := 0
	mustRegister(t, reg, HookSpec{PluginID: "p", Name: "noop", Point: "reply:pre", Mode: HookIntercept},
		func(p map[string]any) map[string]any {
			calls++
			return nil // undefined ⇒ keep
		})
	decision, result, _ := sink.Intercept(context.Background(), agentic.HookReplyPre, map[string]any{"text": "x"})
	if decision != agentic.HookPass || result != nil {
		t.Fatalf("decision/result = %v/%v, want Pass/nil", decision, result)
	}
	if calls != 1 {
		t.Fatalf("handler calls = %d", calls)
	}
}

func TestHookSinkDenyShortCircuits(t *testing.T) {
	sink, reg := newTestSink(t)
	secondRan := false
	mustRegister(t, reg, HookSpec{PluginID: "p", Name: "gate", Point: "tool-call:pre", Mode: HookIntercept, Priority: 0},
		func(p map[string]any) map[string]any {
			return map[string]any{"deny": true, "reason": "no destructive commands"}
		})
	mustRegister(t, reg, HookSpec{PluginID: "p", Name: "after", Point: "tool-call:pre", Mode: HookIntercept, Priority: 10},
		func(p map[string]any) map[string]any {
			secondRan = true
			return nil
		})
	decision, result, reason := sink.Intercept(context.Background(), agentic.HookToolCallPre,
		map[string]any{"tool": "bash", "input": "rm -rf /"})
	if decision != agentic.HookDenied {
		t.Fatalf("decision = %v, want Denied", decision)
	}
	if reason != "no destructive commands" {
		t.Fatalf("reason = %q", reason)
	}
	if result != nil {
		t.Fatalf("denied result must be nil, got %v", result)
	}
	if secondRan {
		t.Fatal("deny must short-circuit remaining interceptors")
	}
}

func TestHookSinkPanicRecovery(t *testing.T) {
	sink, reg := newTestSink(t)
	secondRan := false
	mustRegister(t, reg, HookSpec{PluginID: "p", Name: "bomb", Point: "tool-call:post", Mode: HookIntercept, Priority: 0},
		func(p map[string]any) map[string]any { panic("boom") })
	mustRegister(t, reg, HookSpec{PluginID: "p", Name: "after", Point: "tool-call:post", Mode: HookIntercept, Priority: 10},
		func(p map[string]any) map[string]any {
			secondRan = true
			return nil
		})
	decision, _, _ := sink.Intercept(context.Background(), agentic.HookToolCallPost, map[string]any{"output": "ok"})
	if decision != agentic.HookPass {
		t.Fatalf("decision = %v, want Pass (panic degrades to pass-through)", decision)
	}
	if !secondRan {
		t.Fatal("chain must continue after a panicking handler")
	}
}

func TestHookSinkTimeoutSkipsRemaining(t *testing.T) {
	sink, reg := newTestSink(t)
	skippedRan := false
	mustRegister(t, reg, HookSpec{PluginID: "p", Name: "first", Point: "reply:delta", Mode: HookIntercept},
		func(p map[string]any) map[string]any { return nil })
	mustRegister(t, reg, HookSpec{PluginID: "p", Name: "never", Point: "reply:delta", Mode: HookIntercept, Priority: 10},
		func(p map[string]any) map[string]any {
			skippedRan = true
			return nil
		})
	// 1ns budget ⇒ the deadline is already in the past when the chain starts
	// (handlerTimeout treats <=0 as the production default, so use a
	// positive-but-useless value).
	sink.perHandler = time.Nanosecond
	decision, _, _ := sink.Intercept(context.Background(), agentic.HookReplyDelta,
		map[string]any{"delta": "d", "is_delta": true, "state": "content"})
	if decision != agentic.HookPass {
		t.Fatalf("decision = %v, want Pass when budget is gone", decision)
	}
	if skippedRan {
		t.Fatal("expired budget must skip handlers")
	}
	st := sink.Stats()[agentic.HookReplyDelta]
	if st.TimedOut == 0 {
		t.Fatal("TimedOut stat must record the violation")
	}
}

func TestHookSinkJSONRoundTripFidelity(t *testing.T) {
	sink, reg := newTestSink(t)
	var seenCopy map[string]any
	mustRegister(t, reg, HookSpec{PluginID: "p", Name: "identity+", Point: "tool-call:post", Mode: HookIntercept},
		func(p map[string]any) map[string]any {
			seenCopy = p
			// Mutating the delivered copy must NOT leak into the caller's map.
			p["output"] = "mutated-in-handler"
			return nil
		})
	payload := map[string]any{
		"point":  "tool-call:post",
		"output": "line1\nline2",
		"stop":   false,
		"count":  42,
		"nested": map[string]any{"a": []any{1, 2, 3}, "b": "x", "c": map[string]any{"deep": true}},
		"arr":    []any{"m", "n"},
	}
	decision, _, _ := sink.Intercept(context.Background(), agentic.HookToolCallPost, payload)
	if decision != agentic.HookPass {
		t.Fatalf("decision = %v, want Pass", decision)
	}
	// Nested structure survived the JSON round trip intact. Compare through
	// JSON: the copy carries JSON-typed numbers (float64) where the literal
	// payload has ints, so reflect.DeepEqual would false-fail on types.
	if mustJSON(t, seenCopy["nested"]) != mustJSON(t, payload["nested"]) {
		t.Fatalf("nested fidelity lost:\n got %s\nwant %s", mustJSON(t, seenCopy["nested"]), mustJSON(t, payload["nested"]))
	}
	if mustJSON(t, seenCopy["arr"]) != mustJSON(t, payload["arr"]) {
		t.Fatalf("array fidelity lost: %s vs %s", mustJSON(t, seenCopy["arr"]), mustJSON(t, payload["arr"]))
	}
	// Caller-owned payload untouched despite in-handler mutation.
	if payload["output"] != "line1\nline2" {
		t.Fatalf("caller payload mutated: %v", payload["output"])
	}
}

func TestHookSinkNotifyAsyncDeliversSnapshot(t *testing.T) {
	sink, reg := newTestSink(t)
	delivered := make(chan map[string]any, 4)
	mustRegister(t, reg, HookSpec{PluginID: "p", Name: "audit", Point: "tool-call:post", Mode: HookNotify},
		func(p map[string]any) map[string]any {
			delivered <- p
			return nil
		})
	payload := map[string]any{"tool": "bash", "output": "done"}
	done := make(chan struct{})
	go func() {
		defer close(done)
		sink.Notify(agentic.HookToolCallPost, payload)
	}()
	select {
	case got := <-delivered:
		if got["tool"] != "bash" {
			t.Fatalf("notify payload = %v", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("async notify not delivered within 5s")
	}
	<-done
}

func TestHookSinkLLMErrorDowngrade(t *testing.T) {
	sink, reg := newTestSink(t)
	var notifySaw string
	gotNotify := make(chan struct{})
	mustRegister(t, reg, HookSpec{PluginID: "p", Name: "advise", Point: "llm:error", Mode: HookIntercept},
		func(p map[string]any) map[string]any {
			// Intercept-mode at llm:error runs, but only `note` survives.
			return map[string]any{"note": "you have 2 resets left", "error": "HACK-ATTEMPT"}
		})
	mustRegister(t, reg, HookSpec{PluginID: "p", Name: "audit", Point: "llm:error", Mode: HookNotify},
		func(p map[string]any) map[string]any {
			e, _ := p["error"].(string)
			notifySaw = e
			close(gotNotify)
			return nil
		})
	sink.Notify(agentic.HookLLMError, map[string]any{
		"error": "rate limit exceeded", "classified": "rate_limit", "will_retry": true,
	})
	select {
	case <-gotNotify:
	case <-time.After(5 * time.Second):
		t.Fatal("llm:error notify not delivered")
	}
	want := "rate limit exceeded\n[plugin] you have 2 resets left"
	if notifySaw != want {
		t.Fatalf("downgraded error text = %q, want %q", notifySaw, want)
	}
}

func TestHookSinkCircuitBreakerBypassesAndResets(t *testing.T) {
	sink, reg := newTestSink(t)
	var calls int64
	mustRegister(t, reg, HookSpec{PluginID: "p", Name: "delta", Point: "reply:delta", Mode: HookIntercept},
		func(p map[string]any) map[string]any {
			calls++
			return map[string]any{"delta": "X"}
		})
	// An intercept-capable point other than reply:delta — its interception
	// ends the current breaker episode (turn-boundary heuristic). A
	// notify-only registration would not reach the breaker (by design it
	// consumes no intercept budget).
	mustRegister(t, reg, HookSpec{PluginID: "p", Name: "msg", Point: "message:pre-send", Mode: HookIntercept},
		func(p map[string]any) map[string]any { return nil })
	ctx := context.Background()
	// One burst of max+10 deltas: only maxInterceptsPerTurn reach the handler.
	for i := 0; i < maxInterceptsPerTurn+10; i++ {
		d, _, _ := sink.Intercept(ctx, agentic.HookReplyDelta, map[string]any{"delta": "d", "state": "content"})
		if i < maxInterceptsPerTurn && d != agentic.HookModified {
			t.Fatalf("call %d: decision %v, want Modified", i, d)
		}
		if i >= maxInterceptsPerTurn && d != agentic.HookPass {
			t.Fatalf("call %d past breaker: decision %v, want Pass", i, d)
		}
	}
	if calls != maxInterceptsPerTurn {
		t.Fatalf("handler invocations = %d, want %d", calls, maxInterceptsPerTurn)
	}
	// Intercepting the other hooked point resets the burst window.
	sink.Intercept(ctx, agentic.HookMessagePreSend, map[string]any{"text": "next turn"})
	d, _, _ := sink.Intercept(ctx, agentic.HookReplyDelta, map[string]any{"delta": "fresh", "state": "content"})
	if d != agentic.HookModified || calls != maxInterceptsPerTurn+1 {
		t.Fatalf("breaker did not reset after point switch: d=%v calls=%d", d, calls)
	}
}

func TestHookSinkZeroHooksFastPath(t *testing.T) {
	sink, _ := newTestSink(t)
	decision, result, reason := sink.Intercept(context.Background(), agentic.HookReplyDelta,
		map[string]any{"delta": "d"})
	if decision != agentic.HookPass || result != nil || reason != "" {
		t.Fatalf("zero-hook Intercept = %v/%v/%q, want Pass/nil/\"\"", decision, result, reason)
	}
	sink.Notify(agentic.HookLLMError, map[string]any{"error": "e"}) // no entries ⇒ no scheduling
}

func TestHookPointMapExhaustive(t *testing.T) {
	all := []agentic.HookPoint{
		agentic.HookMessagePreSend,
		agentic.HookToolCallPre,
		agentic.HookToolCallPost,
		agentic.HookReplyPre,
		agentic.HookReplyDelta,
		agentic.HookLLMError,
	}
	if len(hookPointIDs) != len(all) {
		t.Fatalf("hookPointIDs has %d entries for %d constants — update the adapter map when adding a point", len(hookPointIDs), len(all))
	}
	for _, c := range all {
		id := string(c)
		if hookPointIDs[id] != c {
			t.Errorf("constant %q missing/wrong in hookPointIDs — renaming a constant must break this test, not production", id)
		}
		if !isValidHookPoint(id) {
			t.Errorf("isValidHookPoint(%q) = false", id)
		}
	}
	valid := ValidHookPoints()
	if len(valid) != len(all) {
		t.Fatalf("ValidHookPoints length = %d, want %d", len(valid), len(all))
	}
	for i := 1; i < len(valid); i++ {
		if valid[i-1] >= valid[i] {
			t.Fatalf("ValidHookPoints not sorted: %v", valid)
		}
	}
	if isValidHookPoint("totally-bogus") {
		t.Fatal("unknown point accepted")
	}
}

func TestSchedulerOneShotSelfDeregisters(t *testing.T) {
	sch := NewScheduler()
	t.Cleanup(sch.Stop)
	done := make(chan struct{})
	// setTimeout(0) must NOT be clamped by minInterval (that clamp is for
	// repeating intervals only).
	sch.SetTimeout(func() { close(done) }, 0)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("zero-delay one-shot callback did not run")
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if sch.Count() == 0 {
			return // one-shot deregistered itself (leak fix)
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("fired one-shot still registered: count = %d", sch.Count())
}

// mustRegister registers or fails the test.
func mustRegister(t *testing.T, reg *HookRegistry, spec HookSpec, h HookHandler) {
	t.Helper()
	if err := reg.Register(spec, h); err != nil {
		t.Fatalf("register %+v: %v", spec, err)
	}
}

// mustJSON marshals v or fails the test (used for type-insensitive
// comparisons of JSON round-tripped values).
func mustJSON(t *testing.T, v any) string {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal %v: %v", v, err)
	}
	return string(data)
}
