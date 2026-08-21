// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/pijalu/goa/internal/agentic/provider"
)

func TestThinkingStall_ContinuousDeltasNotStalled(t *testing.T) {
	const stop = 120 * time.Millisecond
	a := NewAgent(Config{
		Model:             testModel(provider.ApiOpenAICompletions),
		Logger:            NewLogger(Error),
		ThinkingStallWarn: 40 * time.Millisecond,
		ThinkingStallStop: stop,
	})
	go func() {
		for range a.Output {
		}
	}()

	// Drip deltas every stop/4 for ~4x the stop threshold: cumulative
	// thinking-phase duration far exceeds the budget, but no gap does.
	for i := 0; i < 16; i++ {
		a.handleThinkingDelta(provider.AssistantMessageEvent{Type: provider.EventThinkingDelta, Delta: "reasoning"})
		a.mu.Lock()
		stalled := a.thinkingStalled
		a.mu.Unlock()
		if stalled {
			t.Fatalf("delta %d: thinkingStalled set while deltas were still arriving (gap < stop threshold)", i)
		}
		time.Sleep(stop / 4)
	}
	waitForThinkingStall(t, a, false, 2*stop, "active thinking stream longer than the stop threshold")
}

// A true hang: thinking deltas stop arriving for longer than the stop
// threshold. No further deltas arrive to re-evaluate the per-delta check,
// so the stall must be detected by the re-armed stall timer alone.
func TestThinkingStall_NoDeltaGapStops(t *testing.T) {
	const stop = 60 * time.Millisecond
	a := NewAgent(Config{
		Model:             testModel(provider.ApiOpenAICompletions),
		Logger:            NewLogger(Error),
		ThinkingStallStop: stop,
	})
	go func() {
		for range a.Output {
		}
	}()

	a.handleThinkingDelta(provider.AssistantMessageEvent{Type: provider.EventThinkingDelta, Delta: "some reasoning"})
	// No more deltas arrive — the timer must fire on its own.
	waitForThinkingStall(t, a, true, 5*stop, "no thinking deltas for longer than the stop threshold")
	a.mu.Lock()
	elapsed := a.thinkingStallElapsed
	a.mu.Unlock()
	if elapsed < stop {
		t.Errorf("thinkingStallElapsed = %v, want >= %v (the silence gap)", elapsed, stop)
	}

	done, _, err := a.handleStreamEvent(context.Background(), nil, provider.AssistantMessageEvent{Type: provider.EventThinkingDelta, Delta: "x"})
	if !done || err == nil {
		t.Fatalf("handleStreamEvent = (done=%v, err=%v), want done=true with a stall error", done, err)
	}
	if !strings.Contains(err.Error(), "thinking stalled") {
		t.Errorf("stall error = %v, want it to mention 'thinking stalled'", err)
	}
}

// The stall error must still be reported under its own name and must not
// leak into the stream-loop guard.
func TestThinkingStall_SeparateFlagAndError(t *testing.T) {
	a := NewAgent(Config{
		Model:             testModel(provider.ApiOpenAICompletions),
		Logger:            NewLogger(Error),
		ThinkingStallStop: 50 * time.Millisecond,
	})
	go func() {
		for range a.Output {
		}
	}()
	// Simulate a model whose reasoning stream has gone silent for far longer
	// than the configured stop duration: the last delta arrived 10 minutes
	// ago, so this new delta lands after a gap that exceeds the threshold.
	a.thinkingStallStart = time.Now().Add(-10 * time.Minute)
	a.handleThinkingDelta(provider.AssistantMessageEvent{Type: provider.EventThinkingDelta, Delta: "still reasoning"})

	a.mu.Lock()
	stalled := a.thinkingStalled
	looped := a.streamLoopDetected
	a.mu.Unlock()
	if !stalled {
		t.Fatal("thinkingStalled not set after a reasoning-only silence gap beyond the stop threshold")
	}
	if looped {
		t.Error("streamLoopDetected set by the thinking-stall watchdog — the guards must stay separate")
	}

	done, _, err := a.handleStreamEvent(context.Background(), nil, provider.AssistantMessageEvent{Type: provider.EventThinkingDelta, Delta: "x"})
	if !done || err == nil {
		t.Fatalf("handleStreamEvent = (done=%v, err=%v), want done=true with a stall error", done, err)
	}
	if !strings.Contains(err.Error(), "thinking stalled") {
		t.Errorf("stall error = %v, want it to mention 'thinking stalled'", err)
	}
	if strings.Contains(err.Error(), "stream loop detected") {
		t.Errorf("stall error = %v, must NOT be misreported as a stream loop", err)
	}
}

// The warn progress event fires when thinking has been silent past the warn
// threshold, and the stop timer then declares the stall after the stop
// threshold of silence. Once stalled the decision is final: a late content
// delta disarms the timers (forward progress) but must NOT resurrect a turn
// that is already being stopped with the stall error.
func TestThinkingStall_WarnOnSilenceThenStallIsFinal(t *testing.T) {
	const warn = 30 * time.Millisecond
	const stop = 90 * time.Millisecond
	a := NewAgent(Config{
		Model:             testModel(provider.ApiOpenAICompletions),
		Logger:            NewLogger(Error),
		ThinkingStallWarn: warn,
		ThinkingStallStop: stop,
	})
	progress := make(chan string, 8)
	a.AddObserver(OutputObserverFunc(func(ev OutputEvent) {
		if ev.Type == EventProgress {
			select {
			case progress <- ev.Text:
			default:
			}
		}
	}))
	go func() {
		for range a.Output {
		}
	}()

	a.handleThinkingDelta(provider.AssistantMessageEvent{Type: provider.EventThinkingDelta, Delta: "reasoning"})
	select {
	case text := <-progress:
		if !strings.Contains(text, "without producing output") {
			t.Errorf("warn progress text = %q, want it to mention missing output", text)
		}
		// warn emitted after the warn threshold of silence
	case <-time.After(5 * stop):
		t.Fatal("no warn progress event after thinking silence exceeded the warn threshold")
	}

	waitForThinkingStall(t, a, true, 5*stop, "thinking silence past the stop threshold")

	// A content delta arrives after the stall was declared: forward progress
	// disarms the timers and clears the phase clock, but the stall flag is
	// sticky — the turn is already being stopped with the stall error.
	a.handleTextDelta(provider.AssistantMessageEvent{Type: provider.EventTextDelta, Delta: "answer"})
	a.mu.Lock()
	start := a.thinkingStallStart
	stalled := a.thinkingStalled
	a.mu.Unlock()
	if !start.IsZero() {
		t.Error("thinkingStallStart not cleared by content progress")
	}
	if !stalled {
		t.Error("thinkingStalled must stay set once declared — the stall stop is final")
	}
	// The stall error still surfaces on the next handled event.
	done, _, err := a.handleStreamEvent(context.Background(), nil, provider.AssistantMessageEvent{Type: provider.EventTextDelta, Delta: "x"})
	if !done || err == nil || !strings.Contains(err.Error(), "thinking stalled") {
		t.Errorf("handleStreamEvent = (done=%v, err=%v), want done=true with a 'thinking stalled' error", done, err)
	}
}

// Stall timers must not survive a stream round: after resetStreamRoundState
// a fresh reasoning phase starts with a clean clock.
func TestThinkingStall_TimersStopAtRoundBoundary(t *testing.T) {
	const stop = 60 * time.Millisecond
	a := NewAgent(Config{
		Model:             testModel(provider.ApiOpenAICompletions),
		Logger:            NewLogger(Error),
		ThinkingStallStop: stop,
	})
	go func() {
		for range a.Output {
		}
	}()

	a.handleThinkingDelta(provider.AssistantMessageEvent{Type: provider.EventThinkingDelta, Delta: "reasoning"})
	a.resetStreamRoundState()
	a.mu.Lock()
	start := a.thinkingStallStart
	a.mu.Unlock()
	if !start.IsZero() {
		t.Fatal("thinkingStallStart must be cleared at the round boundary")
	}
	waitForThinkingStall(t, a, false, 3*stop, "stale stall timer after round reset")
}

func TestThinkingStall_DisabledByHook(t *testing.T) {
	disabled := true
	a := NewAgent(Config{
		Model:                 testModel(provider.ApiOpenAICompletions),
		Logger:                NewLogger(Error),
		ThinkingStallStop:     50 * time.Millisecond,
		ThinkingStallDisabled: func() bool { return disabled },
	})
	go func() {
		for range a.Output {
		}
	}()
	a.thinkingStallStart = time.Now().Add(-10 * time.Minute)
	a.handleThinkingDelta(provider.AssistantMessageEvent{Type: provider.EventThinkingDelta, Delta: "still reasoning"})
	a.mu.Lock()
	stalled := a.thinkingStalled
	a.mu.Unlock()
	if stalled {
		t.Fatal("thinkingStalled set while the watchdog was disabled")
	}
	// No timer may be armed while disabled, so nothing can fire later either.
	waitForThinkingStall(t, a, false, 5*50*time.Millisecond, "watchdog disabled")

	// Re-enable mid-stream: the same over-long silence gap must now stop.
	disabled = false
	a.thinkingStallStart = time.Now().Add(-10 * time.Minute)
	a.handleThinkingDelta(provider.AssistantMessageEvent{Type: provider.EventThinkingDelta, Delta: "more reasoning"})
	a.mu.Lock()
	stalled = a.thinkingStalled
	a.mu.Unlock()
	if !stalled {
		t.Error("thinkingStalled not set after re-enabling the watchdog on an over-long silence gap")
	}
}

// TestStreamLoop_TUIRepetitionSampleDetected is the exact flood from the
// TUI shows unexpected repetition on normal messages:entry: the
// model repeated one accusation with walking casing/punctuation variants
// ("is never called —" / "is NEVER called!" / "is NEVER CALLED."). After
// case folding and punctuation stripping the copies are byte-exact, so the
// chain detector must fire — this class previously reached the TUI
// unchecked.
func TestStreamLoop_TUIRepetitionSampleDetected(t *testing.T) {
	unit := "isIgnoreableConflict is never called — the error comes from a different path. Let me find all callers of checkConstraints:"
	variants := []string{
		"isIgnoreableConflict is never called — the error comes from a different path. Let me find all callers of checkConstraints:",
		"isIgnoreableConflict is NEVER called! The error must come from a different path. Let me find all callers of checkConstraints:",
		"isIgnoreableConflict is NEVER CALLED. The error must come from a different path. Let me find all callers of checkConstraints:",
		"isIgnoreableConflict is never called — the error comes from a different path. Let me find all callers of checkConstraints:",
		"isIgnoreableConflict is NEVER CALLED! The error must come from a different path. Let me find all callers of checkConstraints:",
		"isIgnoreableConflict is NEVER called. The error must come from a different path. Let me find all callers of checkConstraints:",
	}
	var b strings.Builder
	b.WriteString("Wait — the insert (4,1) goes through execInsert → insertRow. insertRow line 116: checkConstraints → error → isIgnoreableConflict (debug would print). But NO debug print — so either: ")
	for _, v := range variants {
		b.WriteString(v)
	}
	_ = unit
	if !streamLoopWouldDetect(b.String(), 3) {
		t.Error("TUI repetition sample (casing/punctuation variants of one accusation) must trip the detector")
	}
}

// TestStreamLoop_NoFalsePositiveOnStructuredHeaders is the false-positive
// validation for the runaway-loop visibility bug (2026-08-03): a
// long structured report whose sections share markdown headers and table
// separators — but whose per-section content is genuinely varied — is
// legitimate near-repetition, not a loop, and must never trip the detector
// (neither the byte-exact chain nor the shingle-coverage path).
func TestStreamLoop_NoFalsePositiveOnStructuredHeaders(t *testing.T) {
	// Varied per-section analysis lines: shared headers, distinct content.
	analysis := []string{
		"The tokenizer now accepts nested brackets, which fixed the precedence regression seen in the previous pass.",
		"Coverage dipped slightly because the new guard clause short-circuits before the metrics hook runs.",
		"A flaky timing assertion was replaced with a fake clock, so the scheduler tests are deterministic again.",
		"The exporter batch size was raised after profiling showed the flush dominated total runtime.",
		"Retry backoff now caps at thirty seconds; longer waits hid genuine outages in staging last week.",
		"Index compaction moved off the request path, removing the p99 latency spike under write bursts.",
		"The schema migrator gained a dry-run mode so reviewers can inspect the plan before applying it.",
		"Connection pooling was reconfigured to match the database's actual max_connections setting.",
		"A bounds check in the ring buffer prevented the overwrite that corrupted metrics every few hours.",
		"The cache key now includes the locale, fixing the wrong-language snippets served to some users.",
		"Log sampling was tuned to keep error traces while dropping routine health-check chatter.",
		"The rollout finished without incident; error rates stayed flat across all twelve availability zones.",
	}
	var b strings.Builder
	for i, line := range analysis {
		fmt.Fprintf(&b, "## Test Results — Iteration %d\n", i+1)
		b.WriteString("| Metric | Value |\n| --- | --- |\n")
		fmt.Fprintf(&b, "| coverage | %d.%d%% |\n| duration | %dms |\n\n", 75+i, i, 90+i*13)
		b.WriteString(line + "\n\n")
	}
	text := b.String()
	if streamLoopWouldDetect(text, 5) {
		t.Error("false positive: structured report with repeated headers detected as a loop")
	}
	// No prefix may trip either (the detector runs per delta).
	var buf strings.Builder
	const fragSize = 31
	for pos := 0; pos < len(text); pos += fragSize {
		end := pos + fragSize
		if end > len(text) {
			end = len(text)
		}
		buf.WriteString(text[pos:end])
		if streamLoopWouldDetect(buf.String(), 5) {
			t.Fatalf("false positive mid-stream at byte %d of structured report", end)
		}
	}
}

// TestExactChainSample covers the evidence window extraction: a sample
// starting on a word boundary is returned as-is; a mid-word cut (Detector A
// fires at the smallest qualifying period) snaps back to the nearest space,
// bounded by streamLoopSampleSnap, so the displayed repeat reads as full
// words.
func TestExactChainSample(t *testing.T) {
	tests := []struct {
		name   string
		tail   string
		period int
		want   string
	}{
		{"word boundary unchanged", "the quick brown", 5, "brown"},
		{"mid-word snaps to previous space", "see sentence repeats", 14, "sentence repeats"},
		{"no space within snap window keeps start", "aaaaaaaaaaaaaaaaaaaa b", 2, " b"},
		{"whole tail", "whole", 5, "whole"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := exactChainSample(tt.tail, tt.period); got != tt.want {
				t.Errorf("exactChainSample(%q, %d) = %q, want %q", tt.tail, tt.period, got, tt.want)
			}
		})
	}
}
