// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package orchestrator

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/core/orchestrator"
	"github.com/pijalu/goa/internal/ansi"
)

// TestPanel_RendersLifecycle drives a full event sequence through the panel
// (the same sequence a real run emits) and asserts the rendered output at each
// stage reflects the state — without spinning a live model or parsing ANSI.
// This is the tui-test skill's data-driven approach applied to a component
// that has no agent-event-bus coupling.
func TestPanel_RendersLifecycle(t *testing.T) {
	p := NewPanel()
	width := 64

	// 1. Run starts.
	p.ApplyEvent(orchestrator.Event{Type: orchestrator.EventRunStarted,
		Payload: map[string]any{"topology": "hub", "objective": "ship the thing"}})
	lines := p.Render(width)
	if !containsPlain(lines[0], "orchestration · hub") || !containsPlain(lines[0], "running") {
		t.Errorf("header after start = %q", stripANSI(lines[0]))
	}
	if !containsAnyPlain(lines, "no agents yet") {
		t.Errorf("expected 'no agents yet' before any agent starts")
	}

	// 2. Orchestrator agent starts.
	p.ApplyEvent(orchestrator.Event{Type: orchestrator.EventAgentStarted,
		AgentID: "orchestrator-1", Role: "orchestrator", Model: "gpt-x"})
	// 3. Coder delegated to and starts.
	p.ApplyEvent(orchestrator.Event{Type: orchestrator.EventAgentStarted,
		AgentID: "coder-1", Role: "coder", Model: "haiku"})
	// 4. Stats arrive — simulate via SetRows (the app forwarder path).
	p.SetRows([]Row{
		{ID: "orchestrator-1", Role: "orchestrator", Model: "gpt-x", Status: "running", TokensIn: 40, TokensOut: 12},
		{ID: "coder-1", Role: "coder", Model: "haiku", Status: "running", TokensIn: 18, TokensOut: 9},
	})
	lines = p.Render(width)
	joined := strings.Join(mapStr(lines, stripANSI), "\n")
	if !strings.Contains(joined, "orchestrator") || !strings.Contains(joined, "coder") {
		t.Errorf("table missing agents:\n%s", joined)
	}
	if !strings.Contains(joined, "haiku") {
		t.Errorf("table missing model column:\n%s", joined)
	}

	// 5. Coder finishes; orchestrator still running.
	p.ApplyEvent(orchestrator.Event{Type: orchestrator.EventAgentFinished,
		AgentID: "coder-1", Role: "coder", Payload: map[string]any{"outcome": "ok"}})
	lines = p.Render(width)
	joined = strings.Join(mapStr(lines, stripANSI), "\n")
	if !strings.Contains(joined, "finished") {
		t.Errorf("coder finish not reflected:\n%s", joined)
	}

	// 6. Run finishes successfully.
	p.ApplyEvent(orchestrator.Event{Type: orchestrator.EventRunFinished,
		Payload: map[string]any{"ok": true}})
	lines = p.Render(width)
	if !containsPlain(lines[0], "complete") {
		t.Errorf("header after finish = %q, want 'complete'", stripANSI(lines[0]))
	}

	// 7. Failure path colors the header 'failed'.
	p2 := NewPanel()
	p2.SetHeader("fanout", "obj")
	p2.ApplyEvent(orchestrator.Event{Type: orchestrator.EventRunFinished, Payload: map[string]any{"ok": false}})
	if h := stripANSI(p2.Render(width)[0]); !strings.Contains(h, "failed") {
		t.Errorf("failure header = %q, want 'failed'", h)
	}
}

func TestPanel_NarrowWidthSafe(t *testing.T) {
	p := NewPanel()
	p.SetHeader("fanout", "objective that is reasonably long")
	p.SetRows([]Row{{Role: "coder", Model: "m", Status: "running", TokensIn: 1, TokensOut: 2}})
	for _, w := range []int{20, 40, 80} {
		lines := p.Render(w)
		for _, l := range lines {
			if visibleLen(l) > w+2 { // allow border slack
				t.Errorf("width %d: line visible len %d > %d: %q", w, visibleLen(l), w, stripANSI(l))
			}
		}
	}
}

func TestPanel_SetHeaderResetState(t *testing.T) {
	p := NewPanel()
	p.ApplyEvent(orchestrator.Event{Type: orchestrator.EventRunFinished, Payload: map[string]any{"ok": true}})
	p.SetHeader("hub", "new obj")
	if h := stripANSI(p.Render(60)[0]); !strings.Contains(h, "running") {
		t.Errorf("SetHeader did not reset finished state: %q", h)
	}
}

// --- helpers ---

func stripANSI(s string) string {
	var b strings.Builder
	inEsc := false
	for _, r := range s {
		if r == '\x1b' {
			inEsc = true
			continue
		}
		if inEsc {
			if r == 'm' {
				inEsc = false
			}
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func containsPlain(line, want string) bool { return strings.Contains(stripANSI(line), want) }

func containsAnyPlain(lines []string, want string) bool {
	for _, l := range lines {
		if containsPlain(l, want) {
			return true
		}
	}
	return false
}

func mapStr(lines []string, fn func(string) string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = fn(l)
	}
	return out
}

// keep the ansi import used even if helpers evolve.
var _ = ansi.Reset
