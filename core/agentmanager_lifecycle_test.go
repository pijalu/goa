// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package core

import (
	"sync"
	"testing"
	"time"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/internal/agentic"
	agenticprovider "github.com/pijalu/goa/internal/agentic/provider"
	"github.com/pijalu/goa/multiagent"
)

// fakeLifecycleRegistry records dispatched lifecycle events.
type fakeLifecycleRegistry struct {
	events []struct {
		hook    string
		payload map[string]any
	}
}

func (f *fakeLifecycleRegistry) Dispatch(hook string, payload map[string]any) {
	f.events = append(f.events, struct {
		hook    string
		payload map[string]any
	}{hook: hook, payload: payload})
}

func TestAgentManager_LifecycleStartShutdown(t *testing.T) {
	cfg := &config.Config{}
	am := NewAgentManager(cfg, nil, nil, NewSessionState(internal.ModeState{}), nil, "")
	lr := &fakeLifecycleRegistry{}
	am.SetLifecycleRegistry(lr)

	if _, err := am.StartSession(agenticprovider.Model{}, agenticprovider.StreamOptions{}, "", nil, cfg); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	if len(lr.events) != 1 || lr.events[0].hook != "start" {
		t.Fatalf("expected start hook, got %+v", lr.events)
	}

	if err := am.StopSession(); err != nil {
		t.Fatalf("StopSession: %v", err)
	}
	if len(lr.events) != 2 || lr.events[1].hook != "shutdown" {
		t.Fatalf("expected shutdown hook, got %+v", lr.events)
	}
}

// stubStickyProvider supplies a fixed sticky instruction set.
type stubStickyProvider struct{ blocks []string }

func (s *stubStickyProvider) StickyInstructions() []string { return s.blocks }

// TestAgentManager_StickyProvider_InheritedBySession verifies the manager's
// sticky provider is wired into every session agent it builds.
func TestAgentManager_StickyProvider_InheritedBySession(t *testing.T) {
	cfg := &config.Config{}
	am := NewAgentManager(cfg, nil, nil, NewSessionState(internal.ModeState{}), nil, "")
	am.SetStickyProvider(&stubStickyProvider{blocks: []string{"ALWAYS ON"}})

	if _, err := am.StartSession(agenticprovider.Model{}, agenticprovider.StreamOptions{}, "", nil, cfg); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	got := am.ActiveAgentStickyBlocks()
	if len(got) != 1 || got[0] != "ALWAYS ON" {
		t.Errorf("ActiveAgentStickyBlocks = %v, want [ALWAYS ON]", got)
	}
}

func TestAgentManager_LifecycleModeEnter(t *testing.T) {
	cfg := &config.Config{}
	am := NewAgentManager(cfg, nil, nil, NewSessionState(internal.ModeState{}), nil, "")
	lr := &fakeLifecycleRegistry{}
	am.SetLifecycleRegistry(lr)

	am.SetMode(internal.ModeState{Major: internal.MajorCoder, Autonomy: internal.AutonomyYolo})
	if len(lr.events) != 1 || lr.events[0].hook != "mode_enter" {
		t.Fatalf("expected mode_enter hook, got %+v", lr.events)
	}
}

func TestAgentManager_LifecycleToolCallAndDone(t *testing.T) {
	cfg := &config.Config{}
	am := NewAgentManager(cfg, nil, nil, NewSessionState(internal.ModeState{}), nil, "")
	lr := &fakeLifecycleRegistry{}
	am.SetLifecycleRegistry(lr)

	if _, err := am.StartSession(agenticprovider.Model{}, agenticprovider.StreamOptions{}, "", nil, cfg); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	lr.events = nil

	am.OnEvent(agentic.OutputEvent{
		Type:       agentic.EventToolCall,
		ToolName:   "read",
		ToolInput:  `{"path":"x"}`,
		ToolCallID: "tc1",
	})
	am.OnEvent(agentic.OutputEvent{
		Type:       agentic.EventToolResult,
		ToolName:   "read",
		ToolCallID: "tc1",
		ToolResult: "contents",
	})

	if len(lr.events) != 2 {
		t.Fatalf("expected 2 hooks, got %+v", lr.events)
	}
	if lr.events[0].hook != "tool_call" {
		t.Errorf("expected tool_call hook, got %q", lr.events[0].hook)
	}
	if lr.events[1].hook != "tool_done" {
		t.Errorf("expected tool_done hook, got %q", lr.events[1].hook)
	}
	if lr.events[0].payload["tool"] != "read" {
		t.Errorf("tool_call payload.tool = %v, want read", lr.events[0].payload["tool"])
	}
	if lr.events[1].payload["call_id"] != "tc1" {
		t.Errorf("tool_done payload.call_id = %v, want tc1", lr.events[1].payload["call_id"])
	}
}

// TestAgentManager_NoEventDropUnderLoad guards against CORE-BUG-3: the previous
// `select { case am.events <- e: default: }` silently dropped TUI-bound events
// once the 100-slot buffer filled under streaming load, including EventEnd
// (leaving the viewport never marking the turn done). With backpressure, every
// event must reach the sink.
func TestAgentManager_NoEventDropUnderLoad(t *testing.T) {
	cfg := &config.Config{}
	// nil sessionStore: forwardEvent should still deliver to am.events.
	am := NewAgentManager(cfg, nil, nil, NewSessionState(internal.ModeState{}), nil, "")
	am.SetForwardInternalEvents(true)

	const contentN = 1500
	// One EventEnd every 100 content events + a final sentinel EventEnd.
	endEvery := 100
	expectedEnds := contentN/endEvery + 1 // i=0,100,...,1400 => 15, plus sentinel

	type received struct {
		contentText string
		endCount    int
	}
	got := &received{}
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		for ev := range am.events {
			switch ev.Type {
			case agentic.EventContent:
				got.contentText += ev.Text
			case agentic.EventEnd:
				got.endCount++
				if ev.Text == "__DONE__" {
					return
				}
			}
		}
	}()

	for i := 0; i < contentN; i++ {
		// OnEvent blocks until the reader drains — this is the backpressure.
		am.OnEvent(agentic.OutputEvent{Type: agentic.EventContent, Text: "x", Role: agentic.Assistant})
		if i%endEvery == 0 {
			am.OnEvent(agentic.OutputEvent{Type: agentic.EventEnd})
		}
	}
	am.OnEvent(agentic.OutputEvent{Type: agentic.EventEnd, Text: "__DONE__"})

	<-readerDone
	if len(got.contentText) != contentN {
		t.Errorf("content bytes received = %d, want %d (events were dropped)", len(got.contentText), contentN)
	}
	if got.endCount != expectedEnds {
		t.Errorf("EventEnd count = %d, want %d (lifecycle events were dropped)", got.endCount, expectedEnds)
	}
}

// TestAgentManager_BackpressureNoLoss proves the fix for CORE-BUG-3 holds under
// buffer saturation: with the reader deliberately stalled past the 100-slot
// buffer capacity, the sender must block (backpressure) and resume delivering
// every event once the reader catches up — never silently drop.
func TestAgentManager_BackpressureNoLoss(t *testing.T) {
	cfg := &config.Config{}
	am := NewAgentManager(cfg, nil, nil, NewSessionState(internal.ModeState{}), nil, "")
	am.SetForwardInternalEvents(true)

	release := make(chan struct{})
	const contentN = 500 // well beyond the 100-slot buffer

	res := make(chan backpressureResult, 1)
	go collectBackpressureEvents(am.events, release, res)
	go pumpBackpressureEvents(am, contentN)

	// Give the sender time to fill the buffer; it must then block (not drop).
	time.Sleep(100 * time.Millisecond)
	// Release the reader; everything must arrive.
	close(release)

	got := <-res
	if len(got.text) != contentN {
		t.Errorf("content bytes = %d, want %d (events dropped under saturation)", len(got.text), contentN)
	}
	if got.count != contentN+1 {
		t.Errorf("event count = %d, want %d", got.count, contentN+1)
	}
}

type backpressureResult struct {
	count int
	text  string
}

func collectBackpressureEvents(events <-chan agentic.OutputEvent, release <-chan struct{}, out chan<- backpressureResult) {
	<-release
	res := backpressureResult{}
	for ev := range events {
		res.count++
		if ev.Type == agentic.EventContent {
			res.text += ev.Text
		}
		if ev.Type == agentic.EventEnd && ev.Text == "__DONE__" {
			out <- res
			return
		}
	}
	out <- res
}

func pumpBackpressureEvents(am *AgentManager, contentN int) {
	for i := 0; i < contentN; i++ {
		am.OnEvent(agentic.OutputEvent{Type: agentic.EventContent, Text: "y", Role: agentic.Assistant})
	}
	am.OnEvent(agentic.OutputEvent{Type: agentic.EventEnd, Text: "__DONE__"})
}

// TestAgentManager_FieldRace guards CORE-BUG-2: SetForegroundOrchestrator and
// SetStateStore previously wrote their fields with no lock while the observer
// goroutine and command paths read them unlocked during a turn. Run under
// -race, this must be clean.
func TestAgentManager_FieldRace(t *testing.T) {
	cfg := &config.Config{}
	am := NewAgentManager(cfg, nil, nil, NewSessionState(internal.ModeState{}), nil, "")
	am.SetForwardInternalEvents(true)

	drainDone := startEventDrain(am.events)
	t.Cleanup(func() { _ = drainDone })

	pool := multiagent.NewAgentPool(agenticprovider.Model{}, agenticprovider.StreamOptions{}, nil)
	orchA := multiagent.NewForegroundOrchestrator(pool)
	orchB := multiagent.NewForegroundOrchestrator(pool)
	ss := NewStateStore(t.TempDir())

	const iterations = 800
	stop := make(chan struct{})
	var wg sync.WaitGroup

	wg.Add(3)
	go runForIterations(&wg, stop, iterations, func(i int) { swapOrchestrator(am, orchA, orchB, i) })
	go runForIterations(&wg, stop, iterations, func(i int) { swapStateStore(am, ss) })
	go runForIterations(&wg, stop, iterations, func(i int) {
		am.OnEvent(agentic.OutputEvent{Type: agentic.EventToolCall, ToolName: "read", ToolCallID: "c"})
	})

	wg.Wait()
	close(stop)
}

func startEventDrain(events <-chan agentic.OutputEvent) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range events {
		}
	}()
	return done
}

func runForIterations(wg *sync.WaitGroup, stop <-chan struct{}, iterations int, work func(int)) {
	defer wg.Done()
	for i := 0; i < iterations; i++ {
		select {
		case <-stop:
			return
		default:
		}
		work(i)
	}
}

func swapOrchestrator(am *AgentManager, orchA, orchB *multiagent.ForegroundOrchestrator, i int) {
	if i%2 == 0 {
		am.SetForegroundOrchestrator(orchA)
	} else {
		am.SetForegroundOrchestrator(orchB)
	}
}

func swapStateStore(am *AgentManager, ss *StateStore) {
	am.SetStateStore(ss)
	_ = am.GetInputHistory()
	_ = am.SetInputHistory([]string{"a", "b"})
}

// TestOverlayCompressionForModel_StrategiesAndCacheGate verifies per-model
// overrides of the 3-layer strategies and the cache gate merge over the
// global section, with inheritance for unset fields (compression
// directive: cache management configurable globally and per model).
func TestOverlayCompressionForModel_StrategiesAndCacheGate(t *testing.T) {
	ccEnabled := true
	cc := config.ContextCompressionConfig{
		Enabled:   &ccEnabled,
		CacheGate: "on",
		Strategies: config.CompressionLayerStrategiesConfig{
			Soft: "micro",
			Hard: "hybrid",
		},
		PerModel: map[string]config.ModelCompressionOverride{
			"local-model": {CacheGate: "off", Strategies: config.CompressionLayerStrategiesConfig{Hard: "selective"}},
		},
	}

	// Per-model override: cache gate off + hard selective; other fields inherit.
	ov := overlayCompressionForModel(cc, "local-model")
	if ov.cacheGate != "off" {
		t.Errorf("cacheGate = %q, want off (per-model override)", ov.cacheGate)
	}
	if ov.strategies.Hard != "selective" {
		t.Errorf("hard strategy = %q, want selective (per-model override)", ov.strategies.Hard)
	}
	if ov.strategies.Soft != "micro" {
		t.Errorf("soft = %q, want micro (inherited)", ov.strategies.Soft)
	}

	// No override: global values.
	ov2 := overlayCompressionForModel(cc, "other-model")
	if ov2.cacheGate != "on" || ov2.strategies.Hard != "hybrid" {
		t.Errorf("no-override overlay = gate %q hard %q, want on/hybrid (global)", ov2.cacheGate, ov2.strategies.Hard)
	}

	// The agentic mapping: soft/hard strategies map to the SDK type (no
	// trigger layer); the DisableCacheGate flag derives from the resolved cache
	// gate in buildCompressionConfig (DisableCacheGate: ov.cacheGate == "off").
	ag := agenticLayerStrategies(ov.strategies)
	if ag.Hard != agentic.CompressionSelective {
		t.Errorf("agentic hard = %q, want selective", ag.Hard)
	}
	if ag.Soft != agentic.CompressionMicro {
		t.Errorf("agentic soft = %q, want micro", ag.Soft)
	}
}

// TestBuildCompressionConfig_PerModelHardOverridesTheDefault verifies the
// per-model contract for the hard layer (user-confirmed, export
// goa-export-20260814-153156): per_model.<id>.thresholds.hard_percent and
// per_model.<id>.strategies.hard override the defaults (hard default-on at
// 95 with the summarize strategy), while zero fields inherit — including a
// negative hard_percent as an explicit per-model opt-out of the proactive
// hard tier (the reactive safety net still uses the default 95 via
// effectiveHard).
func TestBuildCompressionConfig_PerModelHardOverridesTheDefault(t *testing.T) {
	ccEnabled := true
	cfg := &config.Config{
		ContextCompression: config.ContextCompressionConfig{
			Enabled: &ccEnabled,
			PerModel: map[string]config.ModelCompressionOverride{
				"tight-model": {
					Thresholds: config.CompressionThresholdsConfig{HardPercent: 85},
					Strategies: config.CompressionLayerStrategiesConfig{Hard: "selective"},
				},
				"no-hard-model": {
					Thresholds: config.CompressionThresholdsConfig{HardPercent: -1},
				},
			},
		},
	}
	am := NewAgentManager(cfg, nil, nil, nil, nil, "")

	// Per-model hard override: ceiling 85 + hard strategy selective; the
	// other layers stay off (zero fields inherit the global zeros).
	cc := am.buildCompressionConfig(cfg, "tight-model", 32768)
	if cc.Thresholds.HardPercent != 85 {
		t.Errorf("HardPercent = %d, want 85 (per-model override)", cc.Thresholds.HardPercent)
	}
	if cc.Strategies.Hard != agentic.CompressionSelective {
		t.Errorf("hard strategy = %q, want selective (per-model override)", cc.Strategies.Hard)
	}
	if cc.Strategies.Soft != "" {
		t.Errorf("soft strategy = %q, want empty (inherited unset)", cc.Strategies.Soft)
	}
	if cc.Thresholds.SoftPercent != 0 {
		t.Errorf("soft threshold = %d, want 0 (inherited global zero, opt-in off)", cc.Thresholds.SoftPercent)
	}

	// Negative per-model hard_percent: an explicit opt-out of the proactive
	// hard tier (kept as a negative marker through the overlay).
	ccNeg := am.buildCompressionConfig(cfg, "no-hard-model", 32768)
	if ccNeg.Thresholds.HardPercent != -1 {
		t.Errorf("HardPercent = %d, want -1 (per-model explicit opt-out)", ccNeg.Thresholds.HardPercent)
	}

	// Unknown model: global zeros — the SDK resolves hard=0 to the default
	// 95 ceiling with the summarize strategy (default-on hard layer).
	ccDef := am.buildCompressionConfig(cfg, "other-model", 32768)
	if ccDef.Thresholds.HardPercent != 0 {
		t.Errorf("HardPercent = %d, want 0 (inherited global zero)", ccDef.Thresholds.HardPercent)
	}
	if ccDef.Strategies.Hard != "" {
		t.Errorf("hard strategy = %q, want empty (SDK default summarize applies)", ccDef.Strategies.Hard)
	}
}

// TestTimeContextRefreshInterval parses the time_context refresh interval
// string (CX6): empty and "0"-equivalent values parse to zero (inject every
// eligible step), a valid duration parses to the duration, and an unparseable
// value (rejected earlier by config validation) safely falls back to zero.
func TestTimeContextRefreshInterval(t *testing.T) {
	if got := timeContextRefreshInterval(""); got != 0 {
		t.Errorf("timeContextRefreshInterval(\"\") = %v, want 0", got)
	}
	if got := timeContextRefreshInterval("60s"); got != time.Minute {
		t.Errorf("timeContextRefreshInterval(\"60s\") = %v, want 1m0s", got)
	}
	if got := timeContextRefreshInterval("5m"); got != 5*time.Minute {
		t.Errorf("timeContextRefreshInterval(\"5m\") = %v, want 5m0s", got)
	}
	// Unparseable (config validation rejects it earlier; here we only ensure
	// the builder never panics and treats it as zero).
	if got := timeContextRefreshInterval("bogus"); got != 0 {
		t.Errorf("timeContextRefreshInterval(\"bogus\") = %v, want 0 (safe fallback)", got)
	}
}
