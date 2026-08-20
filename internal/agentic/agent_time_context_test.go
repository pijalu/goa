// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

import (
	"context"
	"strings"
	"testing"
	"time"
)

// timeCtxTestAgent returns an agent with temporal-context injection enabled
// and a fixed display zone, ready for direct injectTimeContextIfDue calls.
func timeCtxTestAgent(interval time.Duration) *Agent {
	return NewAgent(Config{
		TimeContext: TimeContextConfig{
			Enabled:         true,
			TimeZone:        "Asia/Shanghai",
			RefreshInterval: interval,
		},
		Logger: NewLogger(Error),
	})
}

// timeCtxReadings returns the injected time-context messages currently in
// history, in order.
func timeCtxReadings(a *Agent) []Message {
	var out []Message
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, m := range a.history {
		if _, ok := m.Metadata[timeContextMetaKey]; ok {
			out = append(out, m)
		}
	}
	return out
}

// TestTimeContextMessageShapeSnapshot pins the exact message shape of the
// temporal-context reading (CX6 acceptance: message shape snapshot-tested).
// The shape adapts the dsh time-context README model-experience lines to
// Goa (no browser provenance: the display zone is configured or local).
func TestTimeContextMessageShapeSnapshot(t *testing.T) {
	zone, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatalf("LoadLocation(Asia/Shanghai): %v", err)
	}
	now := time.Date(2026, 7, 16, 10, 30, 0, 0, zone)

	// Step 1 with no prior model-visible message: elapsed unavailable.
	step1 := renderTimeContextMessage(now, zone, 1, 1, nil)
	wantStep1 := "Time sampled while preparing turn 1, step 1: 2026-07-16T10:30:00+08:00[Asia/Shanghai]\n" +
		"Time zone for this request: Asia/Shanghai. Interpret otherwise-unqualified dates and times in this zone.\n" +
		"Elapsed since the preceding model-visible message: unavailable."
	if step1 != wantStep1 {
		t.Errorf("step-1 snapshot mismatch:\n got: %q\nwant: %q", step1, wantStep1)
	}

	// Later step with a known elapsed baseline: "5m 12s".
	elapsed := 5*time.Minute + 12*time.Second
	step2 := renderTimeContextMessage(now, zone, 1, 2, &elapsed)
	wantStep2 := "Time sampled while preparing turn 1, step 2: 2026-07-16T10:30:00+08:00[Asia/Shanghai]\n" +
		"Time zone for this request: Asia/Shanghai. Interpret otherwise-unqualified dates and times in this zone.\n" +
		"Elapsed since the preceding step context: 5m 12s."
	if step2 != wantStep2 {
		t.Errorf("step-2 snapshot mismatch:\n got: %q\nwant: %q", step2, wantStep2)
	}

	// Long elapsed uses compact whole-second units with day/hour components.
	elapsedLong := 25*time.Hour + 61*time.Minute + 3*time.Second
	got := formatTimeContextDuration(elapsedLong)
	if got != "1d 2h 1m 3s" {
		t.Errorf("formatTimeContextDuration(25h61m3s) = %q, want %q", got, "1d 2h 1m 3s")
	}
}

// TestTimeContextDurationFormat pins the whole-second compact duration
// formatting (dsh parity) including the zero clamp on backward clock.
func TestTimeContextDurationFormat(t *testing.T) {
	tests := []struct {
		in   time.Duration
		want string
	}{
		{0, "0s"},
		{500 * time.Millisecond, "0s"},
		{5 * time.Second, "5s"},
		{time.Minute + 5*time.Second, "1m 5s"},
		{time.Hour + 2*time.Minute + 3*time.Second, "1h 2m 3s"},
		{25*time.Hour + 61*time.Minute + 4*time.Second, "1d 2h 1m 4s"},
		// Backward wall-clock movement clamps elapsed to zero.
		{-10 * time.Second, "0s"},
	}
	for _, tt := range tests {
		if got := formatTimeContextDuration(tt.in); got != tt.want {
			t.Errorf("formatTimeContextDuration(%v) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// TestTimeContextOffByDefault verifies the injection is disabled when the
// feature is not enabled (zero-value TimeContext).
func TestTimeContextOffByDefault(t *testing.T) {
	a := NewAgent(Config{Logger: NewLogger(Error)})
	if a.injectTimeContextIfDue(time.Now()) {
		t.Error("injectTimeContextIfDue must not inject when TimeContext is disabled")
	}
	if got := timeCtxReadings(a); len(got) != 0 {
		t.Errorf("expected no readings, got %d", len(got))
	}
}

// TestTimeContextInjectsDurableUserMessage verifies a due injection appends
// a User-role message carrying the marker metadata and the sampled time, and
// that the message is emitted to observers.
func TestTimeContextInjectsDurableUserMessage(t *testing.T) {
	a := timeCtxTestAgent(0)
	now := time.Date(2026, 7, 16, 10, 30, 0, 0, time.UTC)
	a.turnCounter = 1

	// First step of turn 1: injects.
	if !a.injectTimeContextIfDue(now) {
		t.Fatal("first step of turn 1 must inject")
	}
	readings := timeCtxReadings(a)
	if len(readings) != 1 {
		t.Fatalf("expected 1 reading, got %d", len(readings))
	}
	r := readings[0]
	if r.Role != User || r.Type != Content {
		t.Errorf("reading role/type = %v/%v, want User/Content", r.Role, r.Type)
	}
	if v, ok := r.Metadata[timeContextMetaKey]; !ok || v != now.Format(time.RFC3339Nano) {
		t.Errorf("reading metadata marker = %q (ok=%v), want %q", v, ok, now.Format(time.RFC3339Nano))
	}
	// The reading is durable: it must appear in buildProviderHistory output.
	hist := a.buildProviderHistory()
	found := false
	for _, m := range hist {
		if m.Role == "user" {
			for _, block := range m.Content {
				if strings.HasPrefix(block.Text, "Time sampled while preparing turn 1, step 1:") {
					found = true
				}
			}
		}
	}
	if !found {
		t.Error("reading must be visible in buildProviderHistory")
	}
}

// TestTimeContextIntervalSuppression verifies the refresh interval suppresses
// re-injection while the latest reading is younger than the interval, then
// injects again at the exact threshold (CX6 acceptance: interval suppression).
func TestTimeContextIntervalSuppression(t *testing.T) {
	a := timeCtxTestAgent(time.Minute)
	t0 := time.Date(2026, 7, 16, 10, 0, 0, 0, time.UTC)
	a.turnCounter = 1

	if !a.injectTimeContextIfDue(t0) {
		t.Fatal("first reading must inject")
	}
	if got := timeCtxReadings(a); len(got) != 1 {
		t.Fatalf("after first injection: %d readings, want 1", len(got))
	}

	// Same turn, next step within the interval: suppressed.
	a.injectTimeContextIfDue(t0.Add(30 * time.Second))
	if got := timeCtxReadings(a); len(got) != 1 {
		t.Fatalf("within interval: %d readings, want 1 (suppressed)", len(got))
	}

	// Exactly at the threshold: injects.
	if !a.injectTimeContextIfDue(t0.Add(time.Minute)) {
		t.Fatal("injection at the exact interval threshold must inject")
	}
	if got := timeCtxReadings(a); len(got) != 2 {
		t.Fatalf("at threshold: %d readings, want 2", len(got))
	}
}

// TestTimeContextIntervalSuppressionAcrossCompaction verifies the interval
// suppression re-derives from history (never a process-local cache), so it
// behaves correctly across a compaction: once compaction shadows the reading,
// the next eligible step injects a fresh one instead of wrongly suppressing
// on stale in-memory state (CX6 acceptance: works across compaction).
func TestTimeContextIntervalSuppressionAcrossCompaction(t *testing.T) {
	a := timeCtxTestAgent(time.Hour)
	t0 := time.Date(2026, 7, 16, 10, 0, 0, 0, time.UTC)
	a.turnCounter = 1

	if !a.injectTimeContextIfDue(t0) {
		t.Fatal("first reading must inject")
	}
	// Within the interval, a later step is suppressed.
	a.injectTimeContextIfDue(t0.Add(30 * time.Minute))
	if got := timeCtxReadings(a); len(got) != 1 {
		t.Fatalf("within interval before compaction: %d readings, want 1", len(got))
	}

	// Compact: history is replaced wholesale with the summary frame pair, the
	// reading is shadowed (this mirrors compressHistory's replacement).
	a.mu.Lock()
	a.history = []Message{
		{Type: Content, Role: User, Content: "[compacted]"},
		{Type: Content, Role: Assistant, Content: "summary"},
	}
	a.mu.Unlock()

	// Next turn, step 1, still inside the nominal interval: the history scan
	// finds no earlier reading, so a fresh one is injected (the compacted
	// model has no temporal context otherwise).
	a.turnCounter = 2
	a.turnStartHistoryLen = 2
	a.turnStep = 0 // prepareTurn resets the step counter at each turn.
	if !a.injectTimeContextIfDue(t0.Add(45 * time.Minute)) {
		t.Fatal("after compaction must inject a fresh reading")
	}
	readings := timeCtxReadings(a)
	if len(readings) != 1 {
		t.Fatalf("after compaction: %d readings, want 1 fresh", len(readings))
	}
	if !strings.Contains(readings[0].Content, "turn 2, step 1:") {
		t.Errorf("post-compaction reading must carry the new turn/step, got %q", readings[0].Content)
	}
}

// TestTimeContextBackwardClockInjectsAndClamps verifies that backward
// wall-clock movement forces an injection and clamps elapsed to 0s.
func TestTimeContextBackwardClockInjectsAndClamps(t *testing.T) {
	a := timeCtxTestAgent(time.Minute)
	t0 := time.Date(2026, 7, 16, 10, 0, 0, 0, time.UTC)
	a.turnCounter = 1

	if !a.injectTimeContextIfDue(t0) {
		t.Fatal("first reading must inject")
	}
	// Wall clock moves backward by 5s: forces an injection (dsh parity).
	if !a.injectTimeContextIfDue(t0.Add(-5 * time.Second)) {
		t.Fatal("backward wall-clock movement must inject")
	}
	readings := timeCtxReadings(a)
	if len(readings) != 2 {
		t.Fatalf("expected 2 readings, got %d", len(readings))
	}
	if !strings.Contains(readings[1].Content, "Elapsed since the preceding step context: 0s.") {
		t.Errorf("backward-clock reading must clamp elapsed to 0s, got %q", readings[1].Content)
	}
}

// TestTimeContextElapsedBaselines verifies the elapsed baseline selection:
// step 1 measures from the latest preceding reading (across turns), later
// steps measure from the preceding step context within the current turn.
func TestTimeContextElapsedBaselines(t *testing.T) {
	a := timeCtxTestAgent(0)
	t0 := time.Date(2026, 7, 16, 10, 0, 0, 0, time.UTC)
	a.turnCounter = 1
	a.turnStartHistoryLen = 0

	// Turn 1 step 1: no prior reading -> elapsed unavailable.
	a.injectTimeContextIfDue(t0)
	first := timeCtxReadings(a)[0]
	if !strings.Contains(first.Content, "Elapsed since the preceding model-visible message: unavailable.") {
		t.Errorf("first-step baseline must be unavailable, got %q", first.Content)
	}

	// Turn 1 step 2: baseline is the step-1 reading (5m later).
	a.injectTimeContextIfDue(t0.Add(5 * time.Minute))
	second := timeCtxReadings(a)[1]
	if !strings.Contains(second.Content, "Elapsed since the preceding step context: 5m 0s.") {
		t.Errorf("later-step baseline must measure from the step context, got %q", second.Content)
	}

	// Turn 2 step 1 (10m later than t0): baseline is the latest preceding
	// reading (turn 1 step 2, at t0+5m) -> elapsed 5m.
	a.turnCounter = 2
	a.turnStartHistoryLen = len(a.history)
	a.turnStep = 0 // prepareTurn resets the step counter at each turn.
	a.injectTimeContextIfDue(t0.Add(10 * time.Minute))
	third := timeCtxReadings(a)[2]
	if !strings.Contains(third.Content, "Elapsed since the preceding model-visible message: 5m 0s.") {
		t.Errorf("next-turn step-1 baseline must measure from the latest reading, got %q", third.Content)
	}
}

// TestTimeContextElapsedUnavailableForSuppressedStep verifies the dsh
// "shadowed injection" shape: when step 1 of a turn is suppressed by the
// interval, the first later-step injection reports an unavailable step
// context (no in-turn reading to measure from).
func TestTimeContextElapsedUnavailableForSuppressedStep(t *testing.T) {
	a := timeCtxTestAgent(time.Minute)
	t0 := time.Date(2026, 7, 16, 10, 0, 0, 0, time.UTC)
	a.turnCounter = 1

	// Turn 1 step 1 injects.
	a.injectTimeContextIfDue(t0)

	// Turn 2 step 1 is suppressed (999ms < 1m interval).
	a.turnCounter = 2
	a.turnStartHistoryLen = len(a.history)
	a.turnStep = 0 // prepareTurn resets the step counter at each turn.
	a.injectTimeContextIfDue(t0.Add(999 * time.Millisecond))
	if got := timeCtxReadings(a); len(got) != 1 {
		t.Fatalf("suppressed step 1 must not inject, got %d readings", len(got))
	}

	// Turn 2 step 2 at the threshold injects; no in-turn step context exists.
	a.injectTimeContextIfDue(t0.Add(time.Minute))
	readings := timeCtxReadings(a)
	if len(readings) != 2 {
		t.Fatalf("step 2 must inject, got %d readings", len(readings))
	}
	if !strings.Contains(readings[1].Content, "Elapsed since the preceding step context: unavailable.") {
		t.Errorf("suppressed-step baseline must be unavailable, got %q", readings[1].Content)
	}
}

// TestTimeContextHooksIntoStepPreparation verifies the step-preparation path
// (CX6) injects through the real turn flow: round 0 (prepareTurn) injects
// step 1 and a re-stream round injects step 2, with the reading durable in
// history.
func TestTimeContextHooksIntoStepPreparation(t *testing.T) {
	p := textEventProvider("hello")
	agent := NewAgent(Config{
		Model:        testModel(p.API()),
		SystemPrompt: "You are helpful",
		Logger:       NewLogger(Error),
		TimeContext: TimeContextConfig{
			Enabled:         true,
			TimeZone:        "UTC",
			RefreshInterval: 0,
		},
	})

	go func() {
		for range agent.Output {
		}
	}()

	ctx := context.Background()
	if err := agent.Run(ctx, "Hi"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	agent.Stop()

	readings := timeCtxReadings(agent)
	if len(readings) != 1 {
		t.Fatalf("expected 1 reading from the step-preparation path, got %d", len(readings))
	}
	if !strings.Contains(readings[0].Content, "turn 1, step 1:") {
		t.Errorf("reading must carry turn 1 step 1, got %q", readings[0].Content)
	}
	if !strings.Contains(readings[0].Content, "[UTC]") {
		t.Errorf("reading must carry the configured UTC zone, got %q", readings[0].Content)
	}
	// The reading is part of the outgoing provider context.
	hist := agent.buildProviderHistory()
	found := false
	for _, m := range hist {
		for _, b := range m.Content {
			if strings.HasPrefix(b.Text, "Time sampled while preparing turn 1, step 1:") {
				found = true
			}
		}
	}
	if !found {
		t.Error("reading must be visible in the outgoing provider context")
	}
}
