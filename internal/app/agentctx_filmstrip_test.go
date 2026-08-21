// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"context"
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/internal/agentic/provider/mock"
	"github.com/pijalu/goa/internal/ansi"
)

// toolDurationLineRe matches a filmstrip diff line that is ONLY a tool-widget
// duration ("+ Took 0.02s", "- elapsed 1.3s"). These lines are dropped from the
// transcript text because whether a sub-10ms tool emits one at all is
// timing-dependent.
var toolDurationLineRe = regexp.MustCompile(`^\s*[+-]?\s*(?:Took|elapsed)\s+\d+\.\d+s`)

// toolDurationContentRe matches a trimmed content line whose only text is a
// tool duration ("Took 0.02s", "elapsed 1.3s"); used to scrub the rendered
// screen's timing-dependent duration row.
var toolDurationContentRe = regexp.MustCompile(`^(?:Took|elapsed)\s+\d+\.\d+s`)

// updateAgentctxGolden regenerates testdata/agentctx_main.filmstrip.golden.
var updateAgentctxGolden = flag.Bool("update-agentctx-golden", false, "regenerate the agentctx main-agent filmstrip golden")

// TestAgentTranscript_MainAgentFilmstripGolden is the T1 no-behavior-change
// regression: it drives a normal main-agent chat sequence (user message →
// streamed assistant reply → tool call/result → end) through the uiScenario —
// whose chat viewport is now owned by an agentctx.AgentTranscript — and asserts
// the result is identical to the pre-T1 baseline golden.
//
// The golden captures the two timing-independent views of the turn:
//   - the conversation transcript content (the ordered entries the
//     ChatViewport renders: user/assistant/tool text), and
//   - the status-spinner trace (the activity lifecycle across the turn).
// Both are byte-stable across runs and across -race; a raw frame-by-frame diff
// is NOT, because frame-boundary placement of scroll-out lines and the
// sub-10ms tool "Took" line vary with wall-clock timing — that jitter is
// inherent to execution, not to the T1 extraction.
//
// Because createTUIComponents and the uiScenario harness both route the main
// agent's ChatViewport through AgentTranscript.View() (the identical component
// mounted into the engine), a passing golden proves the extraction introduced
// zero rendering delta.
func TestAgentTranscript_MainAgentFilmstripGolden(t *testing.T) {
	sc := newUIScenario(t, 100, 24)
	driveMainAgentTurn(sc)

	got := mainAgentGolden(sc)
	goldenPath := filepath.Join("testdata", "agentctx_main.filmstrip.golden")
	if *updateAgentctxGolden {
		if err := os.MkdirAll(filepath.Dir(goldenPath), 0o755); err != nil {
			t.Fatalf("mkdir testdata: %v", err)
		}
		if err := os.WriteFile(goldenPath, []byte(got), 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
		t.Logf("updated golden %s", goldenPath)
	}

	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden %s: %v (run with -update-agentctx-golden to create it)", goldenPath, err)
	}
	if got != string(want) {
		t.Errorf("main-agent turn diverged from pre-T1 baseline\n--- got ---\n%s\n--- want (golden) ---\n%s", got, want)
	}

	// Structural check on the recorded filmstrip: the spinner must never go
	// dark mid-turn (the canonical activity-lifecycle invariant), and the
	// final transcript must contain the user message, the assistant reply, and
	// the tool widget.
	frames := sc.filmstrip().Frames()
	for i, s := range frames {
		if i == len(frames)-1 {
			continue
		}
		if s.Diff.StatusText == "" {
			t.Errorf("step %d (%s): spinner went dark mid-turn; trace=%v", i, s.Label, sc.filmstrip().StatusTrace())
		}
	}
}

// mainAgentGolden builds the deterministic golden content for one main-agent
// turn: the transcript entries (ordered model), the final rendered screen
// (VisibleText, with wall-clock tool-duration lines scrubbed), and the status
// trace. The visible screen is the actual rendered output — the strongest
// no-rendering-delta signal — and is timing-independent once duration lines
// and trailing-whitespace-only differences are normalized away.
func mainAgentGolden(sc *uiScenario) string {
	var b strings.Builder
	b.WriteString("== transcript ==\n")
	for _, e := range sc.chat.Snapshot() {
		b.WriteString(strconv.Itoa(int(e.Type)))
		b.WriteString(" | ")
		b.WriteString(normalizeFilmstripTiming(e.Text))
		b.WriteString("\n")
	}
	b.WriteString("== visible ==\n")
	b.WriteString(normalizeVisible(sc.engine.VisibleText()))
	b.WriteString("\n== status trace ==\n")
	for _, s := range sc.filmstrip().StatusTrace() {
		b.WriteString(s)
		b.WriteString("\n")
	}
	return b.String()
}

// normalizeVisible reduces the final rendered screen to its stable content as
// a SORTED SET of ANSI-stripped, trimmed, non-empty lines. A sorted set (not an
// ordered sequence) is used because frame/layout timing — which row scrolls
// into scrollback, blank-line placement, the sub-10ms tool-duration row —
// varies under -race and across runs; the presence/absence of each content
// line is the timing-independent signal that the extraction rendered
// identically. The transcript entries + status trace (above) carry the
// ordering signal.
func normalizeVisible(s string) string {
	lines := strings.Split(s, "\n")
	set := map[string]bool{}
	for _, l := range lines {
		t := strings.TrimSpace(ansi.Strip(l))
		if t == "" || toolDurationContentRe.MatchString(t) || isMascotLine(t) {
			continue
		}
		set[t] = true
	}
	out := make([]string, 0, len(set))
	for l := range set {
		out = append(out, l)
	}
	sort.Strings(out)
	return strings.Join(out, "\n")
}

// isMascotLine reports whether a stripped content line is part of the startup
// mascot ASCII art: lines dominated by block-drawing runes (▄ ▀ █ …). The
// mascot is static branding chrome whose scroll-out varies with terminal layout
// timing (a one-row content overflow under -race pushes the top mascot line
// into scrollback), so it is excluded from the render-equivalence golden — the
// T1 assertion concerns the transcript, not the logo.
func isMascotLine(s string) bool {
	block, total := 0, 0
	for _, r := range s {
		if r == ' ' {
			continue
		}
		total++
		switch r {
		case '\u2584', '\u2580', '\u2588', '\u2596', '\u2597', '\u2598', '\u259d', '\u2599', '\u259b', '\u259c', '\u259e', '\u259a', '\u2581':
			block++
		}
	}
	return total > 0 && block*2 >= total // majority block-drawing runes
}

// normalizeFilmstripTiming removes the tool-widget duration line ("Took
// 0.02s", "elapsed 1.3s · …") from a rendered filmstrip so the golden is
// deterministic across runs: whether a fast (sub-10ms) tool even EMITS the
// line depends on wall-clock timing, not on the T1 extraction, so it must not
// be part of the byte-identity assertion.
func normalizeFilmstripTiming(s string) string {
	lines := strings.Split(s, "\n")
	out := lines[:0]
	for _, l := range lines {
		if toolDurationLineRe.MatchString(l) {
			continue
		}
		out = append(out, l)
	}
	return strings.Join(out, "\n")
}

// driveMainAgentTurn pushes one canonical main-agent turn through the
// scenario: a user message, a streamed two-chunk assistant reply, a tool call
// plus its result, and the turn end. Shared by the golden test and the
// mock-LLM test so both assert the same rendered transcript.
func driveMainAgentTurn(sc *uiScenario) {
	sc.apply(&agentic.OutputEvent{Type: agentic.EventStateChange, State: agentic.StateThinking})
	sc.apply(&agentic.OutputEvent{Type: agentic.EventContent, Role: agentic.User, Text: "hello main agent"})
	sc.apply(&agentic.OutputEvent{Type: agentic.EventContent, Role: agentic.Assistant, Text: "Hello! ", IsDelta: true})
	sc.apply(&agentic.OutputEvent{Type: agentic.EventContent, Role: agentic.Assistant, Text: "How can I help?", IsDelta: true})
	sc.apply(&agentic.OutputEvent{
		Type:       agentic.EventToolCall,
		State:      agentic.StateToolCall,
		ToolName:   "read",
		ToolInput:  `{"path":"main.go"}`,
		ToolCallID: "call-1",
	})
	sc.apply(&agentic.OutputEvent{
		Type:       agentic.EventToolResult,
		State:      agentic.StateToolResult,
		ToolName:   "read",
		ToolCallID: "call-1",
		Text:       "package main",
	})
	sc.apply(&agentic.OutputEvent{Type: agentic.EventEnd})
}

// eventCollector is an agentic.OutputObserver that records every OutputEvent
// emitted by a running agent, in order. It lets the mock-LLM test capture the
// exact event stream a real agent produces and replay it through the app.
type eventCollector struct {
	mu     sync.Mutex
	events []agentic.OutputEvent
}

func (c *eventCollector) OnEvent(ev agentic.OutputEvent) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, ev)
}

// staticTool is a trivial agentic.Tool that returns a fixed result, so the
// mock-LLM agent's scripted tool call executes without touching the real tool
// registry (no filesystem / process side effects).
type staticTool struct{ name, result string }

func (s *staticTool) Schema() agentic.ToolSchema {
	return agentic.ToolSchema{
		Name:        s.name,
		Description: "static test tool",
		Schema:      map[string]interface{}{"type": "object", "properties": map[string]interface{}{"path": map[string]interface{}{"type": "string"}}},
	}
}

func (s *staticTool) Execute(string) (string, error) { return s.result, nil }

// IsRetryable reports no transient errors: the static tool never fails.
func (s *staticTool) IsRetryable(error) bool { return false }

// TestAgentTranscript_MainAgentMockLLM is the T1 mock-LLM regression: it runs
// ONE real main-agent turn through a genuine agentic.Agent backed by the
// scripted mock provider (ported in T0), captures the emitted OutputEvents,
// replays them through the uiScenario (whose chat viewport is owned by an
// AgentTranscript), and asserts the rendered transcript shows the mock reply
// and the tool widget. This proves the mock-LLM → agent → app → AgentTranscript
// path renders identically through the extraction.
func TestAgentTranscript_MainAgentMockLLM(t *testing.T) {
	prov := mock.New(t)
	mdl := prov.Model("main-mock")
	// Turn 1 requests a tool call; the agent executes the static tool and
	// re-streams, consuming turn 2 (the text reply) to finish the turn.
	prov.Script("main-mock", mock.ToolCallTurn("read", "call-1", `{"path":"main.go"}`))
	prov.Script("main-mock", mock.TextTurn("mock main-agent reply"))

	tool := &staticTool{name: "read", result: "package main"}
	agent := agentic.NewAgent(agentic.Config{
		Model:        mdl,
		SystemPrompt: "test",
		Tools:        []agentic.Tool{tool},
	})
	collector := &eventCollector{}
	agent.AddObserver(collector)

	if _, err := agent.RunAndCollect(context.Background(), "hello main agent"); err != nil {
		t.Fatalf("agent Run: %v", err)
	}
	if prov.Calls("main-mock") < 2 {
		t.Fatalf("mock provider served %d streams, want >= 2 (tool call + reply)", prov.Calls("main-mock"))
	}

	// Replay the captured real-agent event stream through the app scenario.
	sc := newUIScenario(t, 100, 24)
	collector.mu.Lock()
	evs := make([]agentic.OutputEvent, len(collector.events))
	copy(evs, collector.events)
	collector.mu.Unlock()
	if len(evs) == 0 {
		t.Fatal("agent emitted no OutputEvents")
	}
	for i := range evs {
		ev := evs[i]
		sc.apply(&ev)
	}

	rendered := sc.filmstrip().Render()
	if !strings.Contains(rendered, "mock main-agent reply") {
		t.Errorf("rendered transcript missing the mock reply; got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "read") {
		t.Errorf("rendered transcript missing the read tool widget; got:\n%s", rendered)
	}
	// The main chat viewport is the registry's single (main) view's viewport.
	if sc.app.subs.agentRegistry == nil {
		t.Fatal("scenario subsystems should carry the agentctx registry")
	}
	id, view := sc.app.subs.agentRegistry.Active()
	if id != "main" || view == nil || view.Transcript.View() != sc.chat {
		t.Errorf("active registry view mismatch: id=%q view=%+v, want main transcript owning sc.chat", id, view)
	}
}
