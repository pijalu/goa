// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/multiagent"
	"github.com/pijalu/goa/tui"
)

// streamScript drives one orchestrator message through the forwarder.
type streamScript struct {
	from    string
	to      string
	kind    string
	content string
}

func runStreamScript(app *App, fwd *streamForwarder, script []streamScript) {
	for _, s := range script {
		app.handleOrchestratorStreamMsg(multiagent.OrchestratorMessage{
			From: s.from, To: s.to, Kind: s.kind, Content: s.content,
		}, fwd)
	}
}

// TestConcurrentRoleStreams_Isolated reproduces team UI bug RC-1: two delegates
// (planner + coder) streaming concurrently must each get their own section,
// buffers, and lifecycle — previously a single shared section/buffer set
// cross-wired chunks and stream_end between roles, leaving sections stuck on
// "thinking..." with the wrong role's content.
func TestConcurrentRoleStreams_Isolated(t *testing.T) {
	app := New(testSubsystems())
	app.subs.tuiEngine = tui.NewTUI(tui.NewProcessTerminal())
	// Wire a pool with both roles so setActiveAgentFooter resolves each
	// role's provider/model for the status bar (mirrors production).
	app.subs.agentPool = newMockPool(t)
	if _, err := app.subs.agentPool.GetOrCreate("planner"); err != nil {
		t.Fatalf("GetOrCreate planner: %v", err)
	}
	if _, err := app.subs.agentPool.GetOrCreate("coder"); err != nil {
		t.Fatalf("GetOrCreate coder: %v", err)
	}
	fwd := newStreamForwarder()

	// planner starts thinking; coder starts thinking BEFORE planner ends.
	runStreamScript(app, fwd, []streamScript{
		{from: "planner", kind: "thinking_start"},
		{from: "planner", kind: "thinking_chunk", content: "planner thinks A"},
		{from: "coder", kind: "thinking_start"},
		{from: "coder", kind: "thinking_chunk", content: "coder thinks B"},
		// Interleave more chunks — must land in their own sections.
		{from: "planner", kind: "thinking_chunk", content: " + more planner"},
		{from: "coder", kind: "thinking_chunk", content: " + more coder"},
	})

	pSec := fwd.stateFor("planner").section
	cSec := fwd.stateFor("coder").section
	assertThinkingIsolated(t, pSec, cSec)
	assertToolActivityIsolated(t, app, fwd, pSec, cSec)
	assertPlannerEndsCoderStaysLive(t, app, fwd, pSec, cSec)
	assertAllEndTitles(t, app, fwd, pSec, cSec)
}

// assertThinkingIsolated verifies each role's thinking chunks land only in
// that role's own section with its own cycle counter.
func assertThinkingIsolated(t *testing.T, pSec, cSec *tui.CompanionSectionComponent) {
	t.Helper()
	if pSec == nil || cSec == nil {
		t.Fatalf("expected both sections to exist (planner=%v coder=%v)", pSec != nil, cSec != nil)
	}
	if pSec == cSec {
		t.Fatal("planner and coder share one section — state cross-wired")
	}

	pRendered := strings.Join(pSec.Render(100), "\n")
	cRendered := strings.Join(cSec.Render(100), "\n")
	if !strings.Contains(pRendered, "planner thinks A + more planner") {
		t.Errorf("planner section missing its own thinking:\n%s", pRendered)
	}
	if strings.Contains(pRendered, "coder thinks") {
		t.Errorf("planner section contains coder thinking:\n%s", pRendered)
	}
	if !strings.Contains(cRendered, "coder thinks B + more coder") {
		t.Errorf("coder section missing its own thinking:\n%s", cRendered)
	}
	if strings.Contains(cRendered, "planner thinks") {
		t.Errorf("coder section contains planner thinking:\n%s", cRendered)
	}

	// Titles carry per-role cycle counters starting at 1.
	if !strings.Contains(pRendered, "planner · cycle 1") {
		t.Errorf("planner section title wrong:\n%s", pRendered)
	}
	if !strings.Contains(cRendered, "coder · cycle 1") {
		t.Errorf("coder section title wrong:\n%s", cRendered)
	}
}

// assertToolActivityIsolated verifies tool calls land only in the calling
// role's section (RC-2).
func assertToolActivityIsolated(t *testing.T, app *App, fwd *streamForwarder, pSec, cSec *tui.CompanionSectionComponent) {
	t.Helper()
	runStreamScript(app, fwd, []streamScript{
		{from: "coder", kind: "tool_call", content: "search"},
		{from: "coder", kind: "tool_result", content: "76 matches"},
	})
	cRendered := strings.Join(cSec.Render(100), "\n")
	if !strings.Contains(cRendered, "⚙ search") || !strings.Contains(cRendered, "✓ 76 matches") {
		t.Errorf("coder section missing tool activity:\n%s", cRendered)
	}
	pRendered := strings.Join(pSec.Render(100), "\n")
	if strings.Contains(pRendered, "⚙ search") {
		t.Errorf("planner section shows coder's tool activity:\n%s", pRendered)
	}
}

// assertPlannerEndsCoderStaysLive verifies the planner's stream_end closes
// only its own section while the coder stays live and keeps the footer busy
// on the coder (RC-1 footer fix).
func assertPlannerEndsCoderStaysLive(t *testing.T, app *App, fwd *streamForwarder, pSec, cSec *tui.CompanionSectionComponent) {
	t.Helper()
	runStreamScript(app, fwd, []streamScript{
		{from: "planner", to: "stream_start", kind: "content"},
		{from: "planner", to: "stream_chunk", kind: "content", content: "planner verdict"},
		{from: "planner", to: "stream_end", kind: "content", content: "planner verdict"},
	})
	if !pSec.Done() {
		t.Error("planner section not done after its stream_end")
	}
	if cSec.Done() {
		t.Error("coder section closed by planner's stream_end — cross-wired")
	}
	if !app.subs.footer.Data().CompanionBusy {
		t.Error("footer CompanionBusy=false while coder still streaming")
	}
	if got := app.subs.footer.Data().ActiveAgentRole; got != "coder" {
		t.Errorf("footer ActiveAgentRole = %q after planner end, want coder", got)
	}
	// coder section still streams its own message.
	runStreamScript(app, fwd, []streamScript{
		{from: "coder", to: "stream_start", kind: "content"},
		{from: "coder", to: "stream_chunk", kind: "content", content: "coder answer"},
	})
	cRendered := strings.Join(cSec.Render(100), "\n")
	if !strings.Contains(cRendered, "coder answer") {
		t.Errorf("coder section missing its message after planner ended:\n%s", cRendered)
	}
}

// assertAllEndTitles verifies the coder's stream_end reverts the footer to
// idle and both collapsed titles carry each role's own final message.
func assertAllEndTitles(t *testing.T, app *App, fwd *streamForwarder, pSec, cSec *tui.CompanionSectionComponent) {
	t.Helper()
	runStreamScript(app, fwd, []streamScript{
		{from: "coder", to: "stream_end", kind: "content", content: "coder answer"},
	})
	if !cSec.Done() {
		t.Error("coder section not done after its stream_end")
	}
	if app.subs.footer.Data().CompanionBusy {
		t.Error("footer CompanionBusy=true after all streams ended")
	}
	if got := app.subs.footer.Data().ActiveAgentRole; got != "" {
		t.Errorf("footer ActiveAgentRole = %q after all streams ended, want empty", got)
	}

	// Final collapsed titles carry each role's own final message.
	pFinal := strings.Join(pSec.Render(100), "\n")
	cFinal := strings.Join(cSec.Render(100), "\n")
	if !strings.Contains(pFinal, "planner · planner verdict") {
		t.Errorf("planner collapsed title wrong:\n%s", pFinal)
	}
	if !strings.Contains(cFinal, "coder · coder answer") {
		t.Errorf("coder collapsed title wrong:\n%s", cFinal)
	}
}

// TestStreamForwarder_RepeatedCyclesPerRole verifies per-role cycle counters:
// a role's second stream is "cycle 2" while another role is still on cycle 1.
func TestStreamForwarder_RepeatedCyclesPerRole(t *testing.T) {
	app := New(testSubsystems())
	app.subs.tuiEngine = tui.NewTUI(tui.NewProcessTerminal())
	fwd := newStreamForwarder()

	cycle := func(role, text string) {
		runStreamScript(app, fwd, []streamScript{
			{from: role, to: "stream_start", kind: "content"},
			{from: role, to: "stream_chunk", kind: "content", content: text},
			{from: role, to: "stream_end", kind: "content", content: text},
		})
	}
	cycle("planner", "p1")
	cycle("coder", "c1")
	cycle("planner", "p2")

	rendered := strings.Join(app.subs.chat.Render(100), "\n")
	// Sections collapse after stream_end; assert on collapsed titles.
	if !strings.Contains(rendered, "planner · p1") || !strings.Contains(rendered, "planner · p2") {
		t.Errorf("expected both planner cycles in chat:\n%s", rendered)
	}
	if !strings.Contains(rendered, "coder · c1") {
		t.Errorf("expected coder cycle in chat:\n%s", rendered)
	}
	if got := fwd.cycles["planner"]; got != 2 {
		t.Errorf("planner cycles = %d, want 2", got)
	}
	if got := fwd.cycles["coder"]; got != 1 {
		t.Errorf("coder cycles = %d, want 1", got)
	}
}
