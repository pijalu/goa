// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package core

import (
	"testing"
	"time"

	"github.com/pijalu/goa/internal/agentic"
)

func TestTurnRecorder_Empty(t *testing.T) {
	tr := NewTurnRecorder()
	if got := tr.TurnHistory(); len(got) != 0 {
		t.Errorf("expected empty history, got %d", len(got))
	}
	if tr.LastTurn() != nil {
		t.Error("expected nil last turn")
	}
}

// TestTurnRecorder_FinalizeTurnTagsIdentity covers the main-agent path: the
// finalized record is tagged role=main and the supplied goal ID (bugs.md:
// /stats:cache must section per goal/agent).
func TestTurnRecorder_FinalizeTurnTagsIdentity(t *testing.T) {
	tr := NewTurnRecorder()
	tr.ResetTurn(time.Now())
	tr.RecordTokenStats(100, 10, 80, 20, 0, 0, 0, 0)
	rec := tr.FinalizeTurn(nil, "goal-7")
	if rec.AgentRole != "main" {
		t.Errorf("AgentRole = %q, want main", rec.AgentRole)
	}
	if rec.GoalID != "goal-7" {
		t.Errorf("GoalID = %q, want goal-7", rec.GoalID)
	}
	if rec.TokenUsage.CacheRead != 80 || rec.TokenUsage.CacheWrite != 20 {
		t.Errorf("TokenUsage cache = %+v, want read 80 write 20", rec.TokenUsage)
	}
}

// TestTurnRecorder_CurrentTurnCarriesIdentity pins that the in-progress turn
// snapshot is tagged with the agent role and goal of its calls (bugs.md
// 2026-08-30: an untagged snapshot grouped under the wrong /stats:cache
// section — "main" instead of "main · goal" — while its per-call entries
// grouped correctly).
func TestTurnRecorder_CurrentTurnCarriesIdentity(t *testing.T) {
	tr := NewTurnRecorder()
	tr.ResetTurn(time.Now())
	tr.RecordCompletion("main", "goal-42", TurnTokenUsage{PromptN: 10, CacheRead: 90}, 0)
	cur := tr.CurrentTurn()
	if cur == nil {
		t.Fatal("no current turn snapshot")
	}
	if cur.AgentRole != "main" || cur.GoalID != "goal-42" {
		t.Errorf("current turn identity = (%q, %q), want (main, goal-42)", cur.AgentRole, cur.GoalID)
	}
	// Finalizing the turn clears the snapshot identity with it.
	tr.FinalizeTurn(nil, "goal-42")
	if got := tr.CurrentTurn(); got != nil {
		t.Errorf("current turn after finalize = %+v, want nil", got)
	}
}

// TestTurnRecorder_RecordSubAgentTurn covers the sub-agent ingestion path:
// a companion/stage agent's final token stats land as a completed,
// identity-tagged turn continuing the shared numbering.
func TestTurnRecorder_RecordSubAgentTurn(t *testing.T) {
	tr := NewTurnRecorder()
	tr.ResetTurn(time.Now())
	main := tr.FinalizeTurn(nil, "g1")

	sub := tr.RecordSubAgentTurn("companion", "g1", TurnTokenUsage{
		PromptN: 1000, PredictedN: 50, CacheRead: 900, CacheWrite: 100,
	})
	if sub.Number != main.Number+1 {
		t.Errorf("sub turn Number = %d, want %d (shared sequence)", sub.Number, main.Number+1)
	}
	if sub.AgentRole != "companion" || sub.GoalID != "g1" {
		t.Errorf("sub turn identity = %q/%q, want companion/g1", sub.AgentRole, sub.GoalID)
	}
	if sub.TokenUsage.CacheRead != 900 || sub.TokensUsed != 1050 {
		t.Errorf("sub turn usage = %+v tokens=%d, want read 900 tokens 1050", sub.TokenUsage, sub.TokensUsed)
	}
	if got := tr.TurnHistory(); len(got) != 2 {
		t.Errorf("history = %d turns, want 2", len(got))
	}
}

// TestTurnRecorder_RecordSubAgentTurn_CompletionLog pins the sub-agent half
// of the bugs.md §1 fix: each sub-agent turn is also logged as one individual
// completion carrying the same identity and usage.
func TestTurnRecorder_RecordSubAgentTurn_CompletionLog(t *testing.T) {
	tr := NewTurnRecorder()
	tr.ResetTurn(time.Now())
	tr.FinalizeTurn(nil, "g1")

	sub := tr.RecordSubAgentTurn("companion", "g1", TurnTokenUsage{
		PromptN: 1000, PredictedN: 50, CacheRead: 900, CacheWrite: 100,
	})
	comps := tr.CompletionHistory()
	if len(comps) != 1 {
		t.Fatalf("completions = %d, want 1", len(comps))
	}
	c := comps[0]
	if c.TurnNumber != sub.Number || c.AgentRole != "companion" || c.GoalID != "g1" {
		t.Errorf("completion = %+v, want turn %d companion/g1", c, sub.Number)
	}
	if c.CacheRead != 900 || c.CacheWrite != 100 || c.PromptN != 1000 {
		t.Errorf("completion usage = %+v, want read 900 write 100 prompt 1000", c)
	}
}

// TestTurnRecorder_RecordCompletion covers the per-API-call log: every
// EventTokenStats of an in-progress main turn appends one completion keyed to
// that turn, and CompletionHistory returns a defensive copy.
func TestTurnRecorder_RecordCompletion(t *testing.T) {
	tr := NewTurnRecorder()
	tr.ResetTurn(time.Now())

	// Two LLM calls inside the same turn (tool loop round 1 and 2).
	tr.RecordCompletion("main", "goal-1", TurnTokenUsage{PromptN: 100, CacheRead: 0, CacheWrite: 200}, 0)
	tr.RecordCompletion("main", "goal-1", TurnTokenUsage{PromptN: 50, CacheRead: 250, CacheWrite: 0}, 0)

	comps := tr.CompletionHistory()
	if len(comps) != 2 {
		t.Fatalf("completions = %d, want 2 (one per API call)", len(comps))
	}
	// Both calls belong to the in-progress turn (number 1).
	for i, want := range []int{0, 250} {
		if comps[i].TurnNumber != 1 || comps[i].AgentRole != "main" || comps[i].GoalID != "goal-1" {
			t.Errorf("completion[%d] = %+v, want turn 1 main/goal-1", i, comps[i])
		}
		if comps[i].CacheRead != want {
			t.Errorf("completion[%d].CacheRead = %d, want %d (per-call, not flattened)", i, comps[i].CacheRead, want)
		}
	}

	// Defensive copy: mutating the returned slice must not affect the recorder.
	comps[0].CacheRead = 999999
	if got := tr.CompletionHistory()[0].CacheRead; got != 0 {
		t.Errorf("CompletionHistory must return a copy; mutation leaked (%d)", got)
	}
}

func TestTurnRecorder_RecordsToolCallsAndResults(t *testing.T) {
	tr := NewTurnRecorder()
	tr.ResetTurn(time.Now())
	tr.RecordToolCall("bash", `{"command":"echo hi"}`, "call1")
	tr.RecordToolResult("call1", "bash", "hi")

	record := tr.FinalizeTurn(nil, "")
	if len(record.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(record.ToolCalls))
	}
	if record.ToolCalls[0].Name != "bash" {
		t.Errorf("tool name = %q, want bash", record.ToolCalls[0].Name)
	}
	if len(record.ToolResults) != 1 {
		t.Fatalf("expected 1 tool result, got %d", len(record.ToolResults))
	}
	if record.ToolResults[0].Result != "hi" {
		t.Errorf("tool result = %q, want hi", record.ToolResults[0].Result)
	}
	if record.Number != 1 {
		t.Errorf("turn number = %d, want 1", record.Number)
	}
}

func TestTurnRecorder_MultipleTurns(t *testing.T) {
	tr := NewTurnRecorder()
	for i := 0; i < 3; i++ {
		tr.ResetTurn(time.Now())
		tr.RecordToolCall("bash", "", "call")
		tr.FinalizeTurn(nil, "")
	}

	hist := tr.TurnHistory()
	if len(hist) != 3 {
		t.Fatalf("expected 3 turns, got %d", len(hist))
	}
	for i, turn := range hist {
		if turn.Number != i+1 {
			t.Errorf("turn %d number = %d, want %d", i, turn.Number, i+1)
		}
	}
	if last := tr.LastTurn(); last == nil || last.Number != 3 {
		t.Errorf("last turn = %+v, want number 3", last)
	}
}

func TestTurnRecorder_FinalizeTurnCapturesHistory(t *testing.T) {
	agent := agentic.NewAgent(agentic.Config{SystemPrompt: "test"})
	agent.SetHistory([]agentic.Message{
		{Role: agentic.User, Content: "hello"},
		{Role: agentic.Assistant, Content: "world"},
	})

	tr := NewTurnRecorder()
	tr.ResetTurn(time.Now())
	record := tr.FinalizeTurn(agent, "")

	if record.RequestJSON == "" {
		t.Error("expected non-empty RequestJSON")
	}
	if record.ResponseJSON != "world" {
		t.Errorf("response = %q, want world", record.ResponseJSON)
	}
}

func TestTurnRecorder_ResetTurnClearsAccumulators(t *testing.T) {
	tr := NewTurnRecorder()
	tr.ResetTurn(time.Now())
	tr.RecordToolCall("bash", "", "c1")
	tr.ResetTurn(time.Now())
	record := tr.FinalizeTurn(nil, "")
	if len(record.ToolCalls) != 0 {
		t.Errorf("expected accumulators cleared, got %d tool calls", len(record.ToolCalls))
	}
}

func TestTurnRecorder_CurrentTurn_NoTurn(t *testing.T) {
	tr := NewTurnRecorder()
	if got := tr.CurrentTurn(); got != nil {
		t.Errorf("expected nil CurrentTurn with no active turn, got %+v", got)
	}
}

func TestTurnRecorder_CurrentTurn_InProgress(t *testing.T) {
	tr := NewTurnRecorder()
	tr.ResetTurn(time.Now())
	tr.RecordUserInput("hello")
	tr.RecordToolCall("bash", `{"command":"ls"}`, "c1")
	tr.RecordToolResult("c1", "bash", "file.txt")
	tr.RecordTokenStats(100, 50, 0, 0, 20.0, 0, 0, 0)

	cur := tr.CurrentTurn()
	if cur == nil {
		t.Fatal("expected non-nil CurrentTurn")
	}
	if cur.Number != 1 {
		t.Errorf("turn number = %d, want 1", cur.Number)
	}
	if cur.UserInput != "hello" {
		t.Errorf("user input = %q, want hello", cur.UserInput)
	}
	if cur.TokensUsed != 150 {
		t.Errorf("tokens used = %d, want 150", cur.TokensUsed)
	}
	if len(cur.ToolCalls) != 1 {
		t.Errorf("tool calls = %d, want 1", len(cur.ToolCalls))
	}
	if len(cur.ToolResults) != 1 {
		t.Errorf("tool results = %d, want 1", len(cur.ToolResults))
	}
	if cur.Timing.Total <= 0 {
		t.Errorf("expected positive elapsed time, got %f", cur.Timing.Total)
	}
}

func TestTurnRecorder_CurrentTurn_AfterFinalize(t *testing.T) {
	tr := NewTurnRecorder()
	tr.ResetTurn(time.Now())
	tr.RecordUserInput("hello")
	tr.FinalizeTurn(nil, "")

	// After finalize, CurrentTurn should return nil (no active turn).
	if got := tr.CurrentTurn(); got != nil {
		t.Errorf("expected nil CurrentTurn after FinalizeTurn, got %+v", got)
	}
}

func TestTurnRecorder_CurrentTurn_AfterReset(t *testing.T) {
	tr := NewTurnRecorder()
	tr.ResetTurn(time.Now())
	tr.RecordUserInput("hello")
	tr.FinalizeTurn(nil, "")

	// Start a new turn — CurrentTurn should reflect it.
	tr.ResetTurn(time.Now())
	tr.RecordUserInput("world")
	cur := tr.CurrentTurn()
	if cur == nil {
		t.Fatal("expected non-nil CurrentTurn after ResetTurn")
	}
	if cur.Number != 2 {
		t.Errorf("turn number = %d, want 2", cur.Number)
	}
	if cur.UserInput != "world" {
		t.Errorf("user input = %q, want world", cur.UserInput)
	}
}
