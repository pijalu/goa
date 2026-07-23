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

func TestTurnRecorder_RecordsToolCallsAndResults(t *testing.T) {
	tr := NewTurnRecorder()
	tr.ResetTurn(time.Now())
	tr.RecordToolCall("bash", `{"command":"echo hi"}`, "call1")
	tr.RecordToolResult("call1", "bash", "hi")

	record := tr.FinalizeTurn(nil)
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
		tr.FinalizeTurn(nil)
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
	record := tr.FinalizeTurn(agent)

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
	record := tr.FinalizeTurn(nil)
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
	tr.FinalizeTurn(nil)

	// After finalize, CurrentTurn should return nil (no active turn).
	if got := tr.CurrentTurn(); got != nil {
		t.Errorf("expected nil CurrentTurn after FinalizeTurn, got %+v", got)
	}
}

func TestTurnRecorder_CurrentTurn_AfterReset(t *testing.T) {
	tr := NewTurnRecorder()
	tr.ResetTurn(time.Now())
	tr.RecordUserInput("hello")
	tr.FinalizeTurn(nil)

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