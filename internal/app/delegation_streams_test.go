// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/multiagent"
	"github.com/pijalu/goa/tui"
	"github.com/pijalu/goa/tui/agentctx"
)

// newDelegationTestApp builds a minimal app whose subs carry a per-agent
// registry with just the main agent mounted (mirrors createTUIComponents),
// plus a config with thinking visible — enough to drive the delegation
// routing on the command loop (no engine: handlers tolerate nil tuiEngine).
func newDelegationTestApp(t *testing.T) *App {
	t.Helper()
	reg := agentctx.NewAgentViewRegistry()
	mainTr := agentctx.NewAgentTranscript(agentctx.MainAgentID)
	reg.Add(agentctx.MainAgentID, &agentctx.AgentView{Transcript: mainTr, Compositor: mainTr.Compositor()})
	mainTr.Mount()
	a := &App{}
	a.subs = &subsystems{
		agentRegistry: reg,
		chat:          mainTr.View(),
	}
	return a
}

// feedDelegationMsg drives one OrchestratorMessage through the exact T4
// routing entry point used by the forwarder.
func feedDelegationMsg(t *testing.T, a *App, msg multiagent.OrchestratorMessage) {
	t.Helper()
	a.handleOrchestratorStreamMsg(msg, newStreamForwarder())
}

// delegationSnapshot returns the delegation transcript's snapshot text joined
// for substring assertions; fails the test when no view exists.
func delegationSnapshot(t *testing.T, a *App, id string) string {
	t.Helper()
	v, ok := a.subs.agentRegistry.Get(id)
	if !ok {
		t.Fatalf("no registry view for %q (views: %v)", id, a.subs.agentRegistry.IDs())
	}
	var b strings.Builder
	for _, m := range v.Transcript.Snapshot() {
		b.WriteString(m.Text)
		b.WriteByte('\n')
	}
	return b.String()
}

// feedDelegationStates opens one delegation per id (running state), proving
// tabs spawn on creation.
func feedDelegationStates(t *testing.T, a *App, ids ...string) {
	t.Helper()
	for _, id := range ids {
		feedDelegationMsg(t, a, multiagent.OrchestratorMessage{
			From: roleOfID(id), To: "delegation", Kind: "delegation_state",
			Content: "running|", DelegationID: id,
		})
	}
}

// assertTranscriptIsolation proves each delegation transcript holds ONLY its
// own delegation's text.
func assertTranscriptIsolation(t *testing.T, a *App) {
	t.Helper()
	coderOne := delegationSnapshot(t, a, "dlg-coder-01")
	if !strings.Contains(coderOne, "CODER-ONE-OUT") || strings.Contains(coderOne, "CODER-TWO-OUT") || strings.Contains(coderOne, "PLANNER-THINK") {
		t.Errorf("dlg-coder-01 transcript contaminated:\n%s", coderOne)
	}
	coderTwo := delegationSnapshot(t, a, "dlg-coder-02")
	if !strings.Contains(coderTwo, "CODER-TWO-OUT") || strings.Contains(coderTwo, "CODER-ONE-OUT") {
		t.Errorf("dlg-coder-02 transcript contaminated:\n%s", coderTwo)
	}
	planner := delegationSnapshot(t, a, "dlg-planner-01")
	if !strings.Contains(planner, "PLANNER-THINK") {
		t.Errorf("dlg-planner-01 transcript missing its thinking:\n%s", planner)
	}
}

// assertMainHasNoInterleave proves the MAIN transcript holds no delegation
// content and no companion-section entries.
func assertMainHasNoInterleave(t *testing.T, a *App, markers ...string) {
	t.Helper()
	main := a.subs.chat.Snapshot()
	for _, marker := range markers {
		if snapshotContains(main, marker) {
			t.Errorf("main transcript contains delegation marker %q — interleave not retired", marker)
		}
	}
	for _, m := range main {
		if m.Type == tui.ConsoleCompanionMessage {
			t.Error("a companion section entry was added to the main transcript for delegation traffic — the interleave must be bypassed")
		}
	}
}

// T4 core: delegation streams route by DelegationID into per-delegation
// transcripts. Two concurrent same-role delegations stay isolated, and the
// main transcript never sees delegation content (no interleave).
func TestDelegationRouting_IsolatesPerDelegationTranscripts(t *testing.T) {
	a := newDelegationTestApp(t)

	// Two concurrent coder delegations + a planner, each streaming content.
	feedDelegationStates(t, a, "dlg-coder-01", "dlg-coder-02", "dlg-planner-01")

	// Tabs spawned on creation (registry holds main + 3 delegations).
	if got := a.subs.agentRegistry.Len(); got != 4 {
		t.Fatalf("registry views = %d, want 4 (main + 3 delegations)", got)
	}
	// Main stays active: the new tabs do not steal focus.
	if active, _ := a.subs.agentRegistry.Active(); active != agentctx.MainAgentID {
		t.Errorf("active view = %q, want main (delegation tabs must not steal focus)", active)
	}

	feedDelegationMsg(t, a, multiagent.OrchestratorMessage{
		From: "coder", To: "stream_chunk", Kind: "content", Content: "CODER-ONE-OUT", DelegationID: "dlg-coder-01",
	})
	feedDelegationMsg(t, a, multiagent.OrchestratorMessage{
		From: "coder", To: "stream_chunk", Kind: "content", Content: "CODER-TWO-OUT", DelegationID: "dlg-coder-02",
	})
	feedDelegationMsg(t, a, multiagent.OrchestratorMessage{
		From: "planner", To: "stream_chunk", Kind: "thinking_chunk", Content: "PLANNER-THINK", DelegationID: "dlg-planner-01",
	})

	assertTranscriptIsolation(t, a)
	assertMainHasNoInterleave(t, a, "CODER-ONE-OUT", "CODER-TWO-OUT", "PLANNER-THINK")
}

// T4: the terminal stream_end reconciles the delegation's content to the
// authoritative full text (the chunk fanout is lossy under back-pressure).
func TestDelegationRouting_StreamEndReconcilesFullText(t *testing.T) {
	a := newDelegationTestApp(t)
	id := "dlg-coder-01"

	feedDelegationMsg(t, a, multiagent.OrchestratorMessage{
		From: "coder", To: "delegation", Kind: "delegation_state", Content: "running|", DelegationID: id,
	})
	feedDelegationMsg(t, a, multiagent.OrchestratorMessage{
		From: "coder", To: "stream_chunk", Kind: "content", Content: "partial-", DelegationID: id,
	})
	// The terminal frame carries the FULL text; it must REPLACE the partial
	// accumulation, not append to it.
	feedDelegationMsg(t, a, multiagent.OrchestratorMessage{
		From: "coder", To: "stream_end", Kind: "content", Content: "FULL-FINAL-TEXT", DelegationID: id,
	})

	s := delegationSnapshot(t, a, id)
	if !strings.Contains(s, "FULL-FINAL-TEXT") {
		t.Errorf("delegation transcript missing reconciled full text:\n%s", s)
	}
	if strings.Contains(s, "partial-FULL-FINAL-TEXT") {
		t.Errorf("stream_end appended instead of reconciling:\n%s", s)
	}
}

// T4 (bug-2): a FAILED delegation leaves a marked tab (registry error state)
// and an error card inside its transcript; a completed one is marked terminal
// without the error state.
func TestDelegationRouting_FailureMarksTabWithErrorCard(t *testing.T) {
	a := newDelegationTestApp(t)

	feedDelegationMsg(t, a, multiagent.OrchestratorMessage{
		From: "coder", To: "delegation", Kind: "delegation_state", Content: "running|", DelegationID: "dlg-coder-01",
	})
	feedDelegationMsg(t, a, multiagent.OrchestratorMessage{
		From: "coder", To: "delegation", Kind: "delegation_state",
		Content: "failed|provider 400: max_output_tokens", DelegationID: "dlg-coder-01",
	})
	feedDelegationMsg(t, a, multiagent.OrchestratorMessage{
		From: "planner", To: "delegation", Kind: "delegation_state", Content: "running|", DelegationID: "dlg-planner-01",
	})
	feedDelegationMsg(t, a, multiagent.OrchestratorMessage{
		From: "planner", To: "delegation", Kind: "delegation_state", Content: "completed|", DelegationID: "dlg-planner-01",
	})

	// FAILED tab: registry error state set + error card in ITS transcript.
	_, errFlag := a.subs.agentRegistry.Badges("dlg-coder-01")
	if !errFlag {
		t.Error("failed delegation did not set the tab error state")
	}
	s := delegationSnapshot(t, a, "dlg-coder-01")
	if !strings.Contains(s, "FAILED") || !strings.Contains(s, "provider 400: max_output_tokens") {
		t.Errorf("failed delegation transcript missing the error card:\n%s", s)
	}

	// Completed tab: no error state, terminal marker present.
	if _, errFlag := a.subs.agentRegistry.Badges("dlg-planner-01"); errFlag {
		t.Error("completed delegation must not carry the error state")
	}
	if s := delegationSnapshot(t, a, "dlg-planner-01"); !strings.Contains(s, "completed") {
		t.Errorf("completed delegation transcript missing terminal marker:\n%s", s)
	}
}

// roleOfID extracts the role from a dlg-<role>-<NN> id for test messages.
func roleOfID(id string) string {
	rest := strings.TrimPrefix(id, "dlg-")
	return rest[:strings.LastIndex(rest, "-")]
}
