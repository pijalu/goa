// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/internal/ansi"
	"github.com/pijalu/goa/multiagent"
	"github.com/pijalu/goa/skills"
	"github.com/pijalu/goa/tui"
)

// testTerminal implements tui.Terminal for testing.

func TestFormatContextUsage(t *testing.T) {
	cases := []struct {
		name     string
		estimate int
		max      int
		wantSub  string
	}{
		{"low", 30, 100, "30.0%/100"},
		{"warning", 75, 100, "75.0%/100"},
		{"critical", 95, 100, "95.0%/100"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := formatContextUsage(tc.estimate, tc.max)
			if !strings.Contains(got, tc.wantSub) {
				t.Errorf("formatContextUsage(%d,%d) = %q, want substring %q", tc.estimate, tc.max, got, tc.wantSub)
			}
		})
	}
}

func TestFormatFooterStats(t *testing.T) {
	stats := formatFooterStats(sessionStats{
		PromptN:         1500,
		PredictedN:      800,
		ContextEstimate: 2500,
		ContextMax:      10000,
	})
	if !strings.Contains(stats, "↑1.5K") {
		t.Errorf("expected prompt token indicator, got %q", stats)
	}
	if !strings.Contains(stats, "↓800") {
		t.Errorf("expected predicted token indicator, got %q", stats)
	}
	if !strings.Contains(stats, "25.0%/10.0K") {
		t.Errorf("expected context usage, got %q", stats)
	}
}

// TestFormatFooterStats_ShowsProjectedContext is P20/CX8 acceptance criterion
// 2: the footer's occupancy display reads the projected figure (the
// provider-anchored next-request projection), not the stale estimate. The
// estimate and the projection are deliberately different here: the footer must
// show the projection's percentage.
func TestFormatFooterStats_ShowsProjectedContext(t *testing.T) {
	stats := formatFooterStats(sessionStats{
		PromptN:          1500,
		PredictedN:       800,
		ContextEstimate:  2500, // stale estimate: 25%
		ContextProjected: 8000, // projection: 80%
		ContextMax:       10000,
	})
	if !strings.Contains(stats, "80.0%/10.0K") {
		t.Errorf("expected projected context usage (80.0%%/10.0K), got %q", stats)
	}
	if strings.Contains(stats, "25.0%") {
		t.Errorf("footer shows the stale estimate (25.0%%) instead of the projection, got %q", stats)
	}
}

// TestFormatFooterStats_ProjectedFallsBackToEstimate verifies the footer
// falls back to the estimate when no projection has been recorded (they are
// equal then anyway).
func TestFormatFooterStats_ProjectedFallsBackToEstimate(t *testing.T) {
	stats := formatFooterStats(sessionStats{
		ContextEstimate:  2500,
		ContextProjected: 0,
		ContextMax:       10000,
	})
	if !strings.Contains(stats, "25.0%/10.0K") {
		t.Errorf("expected fallback context usage (25.0%%/10.0K), got %q", stats)
	}
}

func TestFormatFooterStats_ToolCalls(t *testing.T) {
	stats := formatFooterStats(sessionStats{
		PromptN:         1500,
		PredictedN:      800,
		ContextEstimate: 2500,
		ContextMax:      10000,
		ToolCalls:       7,
	})
	if !strings.Contains(stats, "TC:7") {
		t.Errorf("expected tool call indicator, got %q", stats)
	}
}

func TestFormatFooterStats_NoToolCalls_OmitsTC(t *testing.T) {
	stats := formatFooterStats(sessionStats{
		PromptN:         1500,
		PredictedN:      800,
		ContextEstimate: 2500,
		ContextMax:      10000,
		ToolCalls:       0,
	})
	if strings.Contains(stats, "TC:") {
		t.Errorf("expected no tool call indicator for zero calls, got %q", stats)
	}
}

func TestFormatFooterStats_CacheHitPercentage(t *testing.T) {
	stats := formatFooterStats(sessionStats{
		PromptN:         1000,
		PredictedN:      500,
		CacheReadTotal:  300,
		CacheWriteTotal: 200,
		LastCacheHit:    CacheHitTrend{Pct: 60, Seen: true, GlobalPct: 60},
		ContextEstimate: 2000,
		ContextMax:      10000,
	})
	// 300 / (300+200) = 60% (cache hit = reads / (reads + writes)).
	// Format: CH:<global>%▸<last>% — single observation, global=last=60.
	if !strings.Contains(stats, "CH:60.0%") {
		t.Errorf("expected CH avg 60%%, got %q", stats)
	}
	if !strings.Contains(stats, "▸60.0%") {
		t.Errorf("expected last cache hit 60%%, got %q", stats)
	}
	// Cache hit is shown even when PromptN is 0, as long as cache ops exist.
	noPrompt := formatFooterStats(sessionStats{
		PromptN:         0,
		CacheReadTotal:  300,
		CacheWriteTotal: 200,
		LastCacheHit:    CacheHitTrend{Pct: 60, Seen: true, GlobalPct: 60},
	})
	if !strings.Contains(noPrompt, "CH:60.0%") {
		t.Errorf("expected CH avg 60%% when PromptN is 0, got %q", noPrompt)
	}
	if !strings.Contains(noPrompt, "▸60.0%") {
		t.Errorf("expected last cache hit 60%% when PromptN is 0, got %q", noPrompt)
	}
	// No cache ops at all should not show a cache-hit rate.
	noCache := formatFooterStats(sessionStats{
		PromptN:    1000,
		PredictedN: 500,
	})
	if strings.Contains(noCache, "▸") {
		t.Errorf("expected no cache hit display when no cache ops, got %q", noCache)
	}
}

// TestFormatFooterStats_LastCacheHit locks the status-bar cache-hit contract:
// the format is CH:<global>%▸<last>% where global is the token-weighted
// session-wide level and last is the most recent per-completion rate.
func TestFormatFooterStats_LastCacheHit(t *testing.T) {
	withLast := formatFooterStats(sessionStats{
		LastCacheHit: CacheHitTrend{Pct: 41.9, Seen: true, GlobalPct: 41.9},
	})
	if !strings.Contains(withLast, "CH:41.9%") {
		t.Errorf("expected CH avg, got %q", withLast)
	}
	if !strings.Contains(withLast, "▸41.9%") {
		t.Errorf("expected per-completion rate, got %q", withLast)
	}
	// No per-completion observation → no CH/▸ part.
	noLast := formatFooterStats(sessionStats{
		CacheReadTotal:  900,
		CacheWriteTotal: 100,
	})
	if strings.Contains(noLast, "▸") {
		t.Errorf("expected no per-completion rate without observation, got %q", noLast)
	}
	if strings.Contains(noLast, "CH:") {
		t.Errorf("expected no CH without observation, got %q", noLast)
	}
}

// TestFormatCacheHitPart_Colors locks the CH evolution coloring:
// bold green growing / green stable or minor change (<5pts drop) / red
// significant drop (>=5pts); first observation (no baseline) is green.
// The prefix checked is the AVG color (first element in the output).
// TestFormatCacheHitPart_Colors locks the CH evolution coloring:
// bold green growing / green stable or minor change (<5pts drop) / red
// significant drop (>=5pts); first observation (no baseline) is green.
// The prefix checked is the GLOBAL color (first element in the output).
func TestFormatCacheHitPart_Colors(t *testing.T) {
	const (
		green = "\x1b[38;2;63;185;80m" // ansi.Fg("#3fb950")
		red   = "\x1b[38;2;248;81;73m" // ansi.Fg("#f85149")
	)
	cases := []struct {
		name string
		tr   CacheHitTrend
		want string // SGR prefix (global color, first element)
	}{
		{"first observation is stable green", CacheHitTrend{Pct: 50, Seen: true, GlobalPct: 50}, green},
		{"growing is bold green", CacheHitTrend{Pct: 52, PrevPct: 50, Seen: true, HasPrev: true, GlobalPct: 52, GlobalPrevPct: 50, GlobalHasPrev: true}, ansi.Bold + green},
		{"stable is green", CacheHitTrend{Pct: 50, PrevPct: 50, Seen: true, HasPrev: true, GlobalPct: 50, GlobalPrevPct: 50, GlobalHasPrev: true}, green},
		{"slight grow is green", CacheHitTrend{Pct: 50.5, PrevPct: 50, Seen: true, HasPrev: true, GlobalPct: 50.5, GlobalPrevPct: 50, GlobalHasPrev: true}, green},
		{"minor drop <5pts stays green", CacheHitTrend{Pct: 47, PrevPct: 50, Seen: true, HasPrev: true, GlobalPct: 47, GlobalPrevPct: 50, GlobalHasPrev: true}, green},
		{"drop of exactly 5pts is red", CacheHitTrend{Pct: 45, PrevPct: 50, Seen: true, HasPrev: true, GlobalPct: 45, GlobalPrevPct: 50, GlobalHasPrev: true}, red},
		{"drop >5pts is red", CacheHitTrend{Pct: 10, PrevPct: 50, Seen: true, HasPrev: true, GlobalPct: 10, GlobalPrevPct: 50, GlobalHasPrev: true}, red},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := formatLastCacheHitPart(tc.tr)
			if !strings.HasPrefix(got, tc.want) {
				t.Errorf("formatLastCacheHitPart(%+v) = %q, want prefix %q", tc.tr, got, tc.want)
			}
		})
	}
}

// TestFormatCacheHitPart_PerElementColors verifies that the global and last
// elements are colored independently: the global level reflects its own
// evolution and the last rate its own, each with the >=5pt threshold.
func TestFormatCacheHitPart_PerElementColors(t *testing.T) {
	const (
		green = "\x1b[38;2;63;185;80m" // ansi.Fg("#3fb950")
		red   = "\x1b[38;2;248;81;73m" // ansi.Fg("#f85149")
		reset = "\x1b[0m"
	)
	cases := []struct {
		name       string
		tr         CacheHitTrend
		wantGlobal string // expected SGR for the CH:<global>% element
		wantLast   string // expected SGR for the ▸<last>% element
	}{
		{
			name:       "global minor drop, last significant drop",
			tr:         CacheHitTrend{Pct: 40, PrevPct: 50, Seen: true, HasPrev: true, GlobalPct: 47, GlobalPrevPct: 50, GlobalHasPrev: true},
			wantGlobal: green, // global delta -3 (< 5)
			wantLast:   red,   // last: 40 vs prev 50, delta = -10 (>= 5)
		},
		{
			name:       "global significant drop, last stable",
			tr:         CacheHitTrend{Pct: 50, PrevPct: 50, Seen: true, HasPrev: true, GlobalPct: 70, GlobalPrevPct: 80, GlobalHasPrev: true},
			wantGlobal: red,   // global delta -10 (>= 5)
			wantLast:   green, // last: 50 vs prev 50, delta = 0
		},
		{
			name:       "both stable",
			tr:         CacheHitTrend{Pct: 75, PrevPct: 75, Seen: true, HasPrev: true, GlobalPct: 75, GlobalPrevPct: 75, GlobalHasPrev: true},
			wantGlobal: green,
			wantLast:   green,
		},
		{
			name:       "both significant drop",
			tr:         CacheHitTrend{Pct: 10, PrevPct: 50, Seen: true, HasPrev: true, GlobalPct: 37, GlobalPrevPct: 50, GlobalHasPrev: true},
			wantGlobal: red, // global delta -13 (>= 5)
			wantLast:   red,
		},
		{
			name:       "minor fluctuation stays green",
			tr:         CacheHitTrend{Pct: 73, PrevPct: 75, Seen: true, HasPrev: true, GlobalPct: 74.5, GlobalPrevPct: 75, GlobalHasPrev: true},
			wantGlobal: green, // delta -0.5 (< 5)
			wantLast:   green, // delta -2 (< 5)
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := formatLastCacheHitPart(tc.tr)
			// Format: <gColor>CH:<global>%<reset><lastColor>▸<last>%<reset>
			// Find the reset between global and last to split them.
			idx := strings.Index(got, reset)
			if idx < 0 {
				t.Fatalf("formatLastCacheHitPart missing reset: %q", got)
			}
			globalPart := got[:idx+len(reset)]
			lastPart := got[idx+len(reset):]
			if !strings.HasPrefix(globalPart, tc.wantGlobal) {
				t.Errorf("global part = %q, want prefix %q", globalPart, tc.wantGlobal)
			}
			if !strings.HasPrefix(lastPart, tc.wantLast) {
				t.Errorf("last part = %q, want prefix %q", lastPart, tc.wantLast)
			}
		})
	}
}

func TestLogTurnStats_UsesPerTurnCounts(t *testing.T) {
	app := New(testSubsystems())
	app.lastTurnPromptN = 100
	app.lastTurnPredictedN = 50
	app.lastTurnSpeed = 12.5
	app.tokenSessionMax = 10000
	app.tokenSessionEstimate = 150
	app.turnCount = 1
	app.turnStatsSeen = true // simulate a turn that emitted token stats

	logger := agentic.NewLogger(agentic.Info)
	logPath := filepath.Join(t.TempDir(), "stats.log")
	file, err := os.Create(logPath)
	if err != nil {
		t.Fatalf("create log: %v", err)
	}
	file.Close()

	logFile, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatalf("open log: %v", err)
	}
	logger.SetOutput(logFile)
	app.subs.logger = logger

	app.logTurnStats(&agentic.OutputEvent{Type: agentic.EventEnd})
	logFile.Close()

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	content := string(data)
	want := "[stats] turn 1: in=100 out=50 speed=12.5 ctx=1.5%/10000"
	if !strings.Contains(content, want) {
		t.Errorf("log line mismatch\nwant substring: %q\ngot: %q", want, content)
	}
}

func TestHandleOrchestratorStreamMsg_CompanionSection(t *testing.T) {
	app := New(testSubsystems())
	app.subs.tuiEngine = tui.NewTUI(tui.NewProcessTerminal())

	fwd := newStreamForwarder()

	app.handleOrchestratorStreamMsg(multiagent.OrchestratorMessage{Kind: "thinking_start"}, fwd)
	app.handleOrchestratorStreamMsg(multiagent.OrchestratorMessage{Kind: "thinking_chunk", Content: "reasoning..."}, fwd)
	app.handleOrchestratorStreamMsg(multiagent.OrchestratorMessage{Kind: "thinking_end"}, fwd)

	rendered := strings.Join(app.subs.chat.Render(80), "\n")
	if !strings.Contains(rendered, "reasoning...") {
		t.Errorf("expected thinking text while expanded, got:\n%s", rendered)
	}

	app.handleOrchestratorStreamMsg(multiagent.OrchestratorMessage{Kind: "content", To: "stream_start"}, fwd)
	app.handleOrchestratorStreamMsg(multiagent.OrchestratorMessage{Kind: "content", To: "stream_chunk", Content: "review"}, fwd)
	app.handleOrchestratorStreamMsg(multiagent.OrchestratorMessage{Kind: "content", To: "stream_end"}, fwd)

	if fwd.stateFor("companion").section != nil {
		t.Error("expected section to be cleared after stream_end")
	}

	rendered = strings.Join(app.subs.chat.Render(80), "\n")
	companionCount := strings.Count(rendered, "companion ·")
	if companionCount != 1 {
		t.Errorf("expected exactly one companion section, got %d in:\n%s", companionCount, rendered)
	}
}

func TestHandleOrchestratorStreamMsg_TwoCyclesTwoSections(t *testing.T) {
	app := New(testSubsystems())
	app.subs.tuiEngine = tui.NewTUI(tui.NewProcessTerminal())

	fwd := newStreamForwarder()

	runCycle := func(n int) {
		app.handleOrchestratorStreamMsg(multiagent.OrchestratorMessage{Kind: "thinking_start"}, fwd)
		app.handleOrchestratorStreamMsg(multiagent.OrchestratorMessage{Kind: "thinking_chunk", Content: fmt.Sprintf("think%d", n)}, fwd)
		app.handleOrchestratorStreamMsg(multiagent.OrchestratorMessage{Kind: "thinking_end"}, fwd)
		app.handleOrchestratorStreamMsg(multiagent.OrchestratorMessage{Kind: "content", To: "stream_start"}, fwd)
		app.handleOrchestratorStreamMsg(multiagent.OrchestratorMessage{Kind: "content", To: "stream_chunk", Content: fmt.Sprintf("msg%d", n)}, fwd)
		app.handleOrchestratorStreamMsg(multiagent.OrchestratorMessage{Kind: "content", To: "stream_end"}, fwd)
	}

	runCycle(1)
	runCycle(2)

	rendered := strings.Join(app.subs.chat.Render(80), "\n")
	companionCount := strings.Count(rendered, "companion ·")
	if companionCount != 2 {
		t.Errorf("expected two companion sections, got %d in:\n%s", companionCount, rendered)
	}
}

func TestToolStatusFromResult(t *testing.T) {
	cases := []struct {
		name string
		text string
		want tui.ToolStatus
	}{
		{"error prefix", "Error: oops", tui.ToolError},
		{"error with whitespace", "  Error: oops", tui.ToolError},
		{"budget exceeded", agentic.ToolBudgetResultPrefix, tui.ToolError},
		{"success", "ok", tui.ToolSuccess},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			app := New(testSubsystems())
			got := app.toolStatusFromResult(tc.text)
			if got != tc.want {
				t.Errorf("toolStatusFromResult(%q) = %v, want %v", tc.text, got, tc.want)
			}
		})
	}
}

func TestTelegramSkillEmbedded(t *testing.T) {
	reg := skills.NewSkillRegistry(nil)
	reg.SetEmbeddedFS(skills.EmbeddedSkillsFS)
	if err := reg.LoadAll(); err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	skill, ok := reg.Get("telegram")
	if !ok {
		t.Fatal("telegram skill not found in embedded skills")
	}
	if skill.Meta.Command != "telegram" {
		t.Errorf("telegram skill missing 'command: telegram' frontmatter, got %q", skill.Meta.Command)
	}
	if !skill.Meta.Inline {
		t.Errorf("telegram skill should be inline")
	}
}

func TestSetupEventHandlers_ClosesDoneWhenEngineStops(t *testing.T) {
	term := &testTerminal{w: 80, h: 24}
	engine := tui.NewTUI(term)

	if err := engine.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	// Stop engine at end so the goroutines exit cleanly.
	defer engine.Stop()

	subs := testSubsystems()
	app := New(subs)

	chat := tui.NewChatViewport()
	inp := tui.NewEditor()

	done := app.setupEventHandlers(engine, chat, inp)

	// Engine is running — done must NOT be closed yet.
	select {
	case <-done:
		t.Fatal("done channel closed before engine.Stop()")
	default:
	}

	// Stop the engine (simulates Ctrl+C).
	engine.Stop()

	// done must be closed promptly after engine stops.
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("done channel not closed within 1s after engine.Stop()")
	}
}

func TestSetupEventHandlers_DoneNotClosedBeforeEngineStop(t *testing.T) {
	term := &testTerminal{w: 80, h: 24}
	engine := tui.NewTUI(term)

	if err := engine.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer engine.Stop()

	subs := testSubsystems()
	app := New(subs)

	chat := tui.NewChatViewport()
	inp := tui.NewEditor()

	done := app.setupEventHandlers(engine, chat, inp)

	// The goroutine must block until engine.Stop() — done must NOT be closed.
	select {
	case <-done:
		t.Fatal("done channel closed before engine.Stop()")
	default:
	}
}

// TestLogTurnStats_NoStatsTurnAnnotated is the regression test for the
// identical-stats anomaly (runaway-loop entry): turns that never
// reached the LLM (guardrail latch, connection error) must log a distinct
// "no LLM call" line instead of re-logging the previous turn's stale,
// byte-identical token counts.
func TestLogTurnStats_NoStatsTurnAnnotated(t *testing.T) {
	app := New(testSubsystems())
	app.turnCount = 7
	// turnStatsSeen deliberately false: no EventTokenStats arrived this turn.

	logger := agentic.NewLogger(agentic.Info)
	logPath := filepath.Join(t.TempDir(), "stats.log")
	logFile, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatalf("open log: %v", err)
	}
	logger.SetOutput(logFile)
	app.subs.logger = logger

	app.logTurnStats(&agentic.OutputEvent{Type: agentic.EventEnd})
	logFile.Close()

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	content := string(data)
	want := "[stats] turn 7: no LLM call (no token stats this turn)"
	if !strings.Contains(content, want) {
		t.Errorf("log line mismatch\nwant substring: %q\ngot: %q", want, content)
	}
}

// TestLogTurnStats_StatsSeenResetsAfterTurnEnd verifies the per-turn stats
// flag lifecycle: a turn WITH token stats logs the normal line and the flag
// resets, so the following stats-less turn is annotated instead of re-logging
// identical numbers.
func TestLogTurnStats_StatsSeenResetsAfterTurnEnd(t *testing.T) {
	app := New(testSubsystems())
	app.tokenSessionMax = 10000
	app.turnCount = 1

	logger := agentic.NewLogger(agentic.Info)
	logPath := filepath.Join(t.TempDir(), "stats.log")
	logFile, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatalf("open log: %v", err)
	}
	logger.SetOutput(logFile)
	app.subs.logger = logger
	defer logFile.Close()

	// Turn 1: stats arrive, then the turn ends.
	app.handleTokenStats(&agentic.OutputEvent{
		Type:    agentic.EventTokenStats,
		Timings: &agentic.TokenTimings{PromptN: 100, PredictedN: 50},
	})
	app.logTurnStats(&agentic.OutputEvent{Type: agentic.EventEnd})

	// Turn 2: no stats arrive (e.g. latched guardrail turn).
	app.turnCount = 2
	app.logTurnStats(&agentic.OutputEvent{Type: agentic.EventEnd})
	logFile.Close()

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "[stats] turn 1: in=100 out=50") {
		t.Errorf("turn 1 line missing normal stats: %q", content)
	}
	if !strings.Contains(content, "[stats] turn 2: no LLM call") {
		t.Errorf("turn 2 line should be annotated as stats-less, got: %q", content)
	}
	if strings.Count(content, "in=100 out=50") > 1 {
		t.Errorf("stale stats re-logged for the stats-less turn: %q", content)
	}
}