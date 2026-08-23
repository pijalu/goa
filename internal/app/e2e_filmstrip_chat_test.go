//go:build e2e

// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

// Local-LM end-to-end validation of a plain chat turn through the REAL TUI,
// asserted entirely on the Filmstrip (structured UI data, never raw ANSI).
//
// This is the missing basic case between the unit-level tui-test scenarios
// (scripted events, no model) and the multi-agent local-LM e2e tests: one
// genuine turn against the live LM Studio server, its actual OutputEvent
// stream replayed through the production App.handleAgentOutputEvent path,
// and the resulting UI evolution validated as data:
//
//   - every replayed event produces a Filmstrip snapshot
//   - exactly one EventEnd per turn (the canonical mid-turn-EventEnd bug)
//   - the status spinner is visible during the whole active phase and
//     cleared from the EventEnd snapshot onward
//   - the assistant reply actually reached the rendered screen
//   - Filmstrip.Render() is ANSI-free and the editor is present/focused
//
// Requires an OpenAI-compatible server at http://localhost:1234 (LMStudio /
// llama.cpp). Skips when the server is unreachable.
//
// Run: go test -count=1 -tags e2e -run TestE2E_FilmstripChatTurn ./internal/app/
package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/provider"
	"github.com/pijalu/goa/tui"
)

// TestE2E_FilmstripChatTurn_LocalModel runs one real LM turn through the
// production event path and validates the TUI evolution on the Filmstrip.
func TestE2E_FilmstripChatTurn_LocalModel(t *testing.T) {
	skipIfNoReplayLLM(t)

	events := runLiveLMTurn(t, "Reply with exactly: FILMSTRIP-E2E-OK")
	t.Logf("live turn produced %d events", len(events))

	sc := newUIScenario(t, 100, 24)
	endIdx := replayThroughProductionPath(t, sc, events)
	frames := sc.filmstrip().Frames()
	trace := sc.filmstrip().StatusTrace()
	writeFilmstripArtifacts(t, sc)

	assertOneEventEndPerTurn(t, events, endIdx, len(frames))
	assertSpinnerLifecycle(t, events, frames, trace, endIdx)
	if sc.status.IsVisible() {
		t.Error("spinner still visible after the final render")
	}
	reply := assertReplyRendered(t, sc, events)
	assertANSIFreeAndEditorFocused(t, sc)

	t.Logf("filmstrip e2e OK: %d steps, %d status transitions, reply=%d chars",
		len(frames), len(trace), len(reply))
}

// runLiveLMTurn runs one genuine turn against the live local LM. Keep the
// prompt trivial so the slow model stays within runLiveAgent's timeout.
func runLiveLMTurn(t *testing.T, prompt string) []agentic.OutputEvent {
	t.Helper()
	pm := provider.NewProviderManager(replayE2EConfig())
	mdl, err := pm.ResolveActiveModel()
	if err != nil {
		t.Fatalf("ResolveActiveModel: %v", err)
	}
	events := runLiveAgent(t, mdl, pm.BuildStreamOptions(), prompt)
	if len(events) == 0 {
		t.Fatal("live LM emitted no events")
	}
	return events
}

// replayThroughProductionPath feeds the real event stream through the
// production component tree; returns the index of the (single) EventEnd or
// -1 when the stream never ends.
func replayThroughProductionPath(t *testing.T, sc *uiScenario, events []agentic.OutputEvent) int {
	t.Helper()
	endIdx := -1
	for i := range events {
		if events[i].Type == agentic.EventEnd {
			if endIdx >= 0 {
				t.Fatalf("mid-turn EventEnd at event %d (first at %d): canonical spinner-kill bug", i, endIdx)
			}
			endIdx = i
		}
		sc.apply(&events[i])
	}
	return endIdx
}

// assertOneEventEndPerTurn pins the exactly-one-EventEnd-per-turn invariant
// and the one-snapshot-per-event filmstrip contract.
func assertOneEventEndPerTurn(t *testing.T, events []agentic.OutputEvent, endIdx, frameCount int) {
	t.Helper()
	if endIdx < 0 {
		t.Fatalf("turn never ended: no EventEnd among %d events", len(events))
	}
	if frameCount != len(events) {
		t.Fatalf("filmstrip steps = %d, want %d (one snapshot per event)", frameCount, len(events))
	}
}

// assertSpinnerLifecycle validates the status line as data: from the first
// activity-bearing event until just before EventEnd it must never go dark;
// from the EventEnd snapshot onward it must be cleared. Pre-activity
// bookkeeping events (token/context stats, clear) legitimately carry no
// status yet — nothing is running.
func assertSpinnerLifecycle(t *testing.T, events []agentic.OutputEvent, frames []tui.Snapshot, trace []string, endIdx int) {
	t.Helper()
	firstActive := firstActiveEventIndex(events)
	for i := firstActive; i < endIdx; i++ {
		if frames[i].Diff.StatusText == "" {
			t.Errorf("step %d (%s): spinner went dark mid-turn; status trace=%v", i, frames[i].Label, trace)
			break // one precise error beats a wall of duplicates
		}
	}
	for i := endIdx; i < len(frames); i++ {
		if frames[i].Diff.StatusText != "" {
			t.Errorf("step %d (%s): spinner still visible after EventEnd (%q)", i, frames[i].Label, frames[i].Diff.StatusText)
			break
		}
	}
}

// assertReplyRendered fails unless the assistant reply reached the rendered
// screen, matched by content word: the viewport wraps and re-flows text, so
// long substrings are layout-brittle.
func assertReplyRendered(t *testing.T, sc *uiScenario, events []agentic.OutputEvent) string {
	t.Helper()
	reply := assistantReplyText(events)
	if strings.TrimSpace(reply) == "" {
		t.Fatal("assistant produced no content deltas; nothing to validate on screen")
	}
	screen := strings.Join(sc.engine.AgentFrame().Visible, "\n")
	if !screenShowsReplyWord(screen, reply) {
		t.Errorf("final screen does not show any word of the assistant reply %q\n--- visible screen ---\n%s",
			truncateForLog(reply, 300), truncateForLog(screen, 1500))
	}
	return reply
}

// assertANSIFreeAndEditorFocused checks the agent-view invariants: the
// Filmstrip render stays ANSI-free and the editor remains focused after the
// turn.
func assertANSIFreeAndEditorFocused(t *testing.T, sc *uiScenario) {
	t.Helper()
	if strings.Contains(sc.filmstrip().Render(), "\x1b") {
		t.Error("Filmstrip.Render() contains raw ANSI escape bytes")
	}
	final := sc.engine.AgentFrame()
	if final.FindNode("Editor") == nil {
		t.Error("Editor node missing from the final frame DOM")
	}
	if focused := final.FocusedNode(); focused == nil || focused.Name != "Editor" {
		name := "<nil>"
		if focused != nil {
			name = focused.Name
		}
		t.Errorf("focused node after turn = %q, want \"Editor\"", name)
	}
}

// assistantReplyText concatenates the streamed answer deltas (Role=Assistant,
// State=StateContent). Thinking deltas are excluded: they render separately
// and are not the reply itself.
func assistantReplyText(events []agentic.OutputEvent) string {
	var b strings.Builder
	for _, ev := range events {
		if ev.Type == agentic.EventContent && ev.Role == agentic.Assistant && ev.State == agentic.StateContent {
			b.WriteString(ev.Text)
		}
	}
	return b.String()
}

// firstActiveEventIndex returns the index of the first event that represents
// real turn activity (state change, stream content, tool lifecycle, or
// progress). Bookkeeping-only prefixes (stats/clear) do not count: the
// spinner is legitimately dark before the turn becomes visible.
func firstActiveEventIndex(events []agentic.OutputEvent) int {
	for i, ev := range events {
		switch ev.Type {
		case agentic.EventStateChange, agentic.EventContent,
			agentic.EventToolCall, agentic.EventToolStart, agentic.EventToolResult,
			agentic.EventToolProgress, agentic.EventProgress:
			return i
		}
	}
	return 0
}

// screenShowsReplyWord reports whether the rendered screen contains at least
// one substantial word (>=4 letters after stripping markdown punctuation)
// from the reply.
func screenShowsReplyWord(screen, reply string) bool {
	for _, w := range strings.Fields(reply) {
		w = strings.Trim(w, "*`_#>-.!?,;:()[]{}\"'")
		if len([]rune(w)) < 4 {
			continue
		}
		if strings.Contains(screen, w) {
			return true
		}
	}
	return false
}

// writeFilmstripArtifacts persists the human/agent-readable filmstrip next to
// the test output so failures can be inspected offline.
func writeFilmstripArtifacts(t *testing.T, sc *uiScenario) {
	t.Helper()
	dir := filepath.Join(os.TempDir(), fmt.Sprintf("goa-e2e-filmstrip-%s", time.Now().Format("20060102-150405")))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Logf("artifacts dir unavailable (%v); skipping dump", err)
		return
	}
	files := map[string]string{
		"render.txt":       sc.filmstrip().Render(),
		"status-trace.txt": strings.Join(sc.filmstrip().StatusTrace(), "\n"),
		"final-frame.txt":  sc.engine.AgentFrame().Dump(),
	}
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Logf("artifact %s: %v", path, err)
			continue
		}
		t.Logf("artifact: %s", path)
	}
}

// truncateForLog bounds s for failure messages.
func truncateForLog(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
