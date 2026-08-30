// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package core

import (
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/pijalu/goa/internal/agentic"
)

// CompletionRecord captures the usage of ONE LLM API call (completion),
// recorded at the moment its EventTokenStats arrives. Unlike a TurnRecord —
// which flattens every call of a turn into a single usage snapshot — the
// completion log keeps per-call granularity so views like /stats:cache's
// "last completions" window can show each API call's own cache hit rate.
type CompletionRecord struct {
	TurnNumber int    // turn the call belongs to (1-based, shared sequence)
	PromptN    int    // uncached input tokens of this call
	CacheRead  int    // cache-read tokens of this call
	CacheWrite int    // cache-write tokens of this call
	AgentRole  string // ""/"main" for the primary agent, else the multiagent role
	GoalID     string // active goal at call time ("" = none)
}

// TurnRecorder captures completed agent turns, including tool calls and results.
// It is safe for concurrent use and is owned by AgentManager.
type TurnRecorder struct {
	mu                   sync.Mutex
	turnHistory          []TurnRecord
	completions          []CompletionRecord // session-scoped per-API-call log
	turnToolCallsAccum   []TurnToolCall
	turnToolResultsAccum []TurnToolResult
	turnTokenUsage       TurnTokenUsage // accumulated usage for current turn
	turnStartTime        time.Time
	turnUserInput        string
	turnThinking         strings.Builder
	turnResponses        strings.Builder
	curRole, curGoal     string // identity of the in-progress turn's calls
}

// NewTurnRecorder creates an empty turn recorder.
func NewTurnRecorder() *TurnRecorder {
	return &TurnRecorder{}
}

// ResetTurn clears per-turn accumulators and records the turn start time.
func (tr *TurnRecorder) ResetTurn(start time.Time) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	tr.turnStartTime = start
	tr.turnToolCallsAccum = nil
	tr.turnToolResultsAccum = nil
	tr.turnTokenUsage = TurnTokenUsage{}
	tr.turnUserInput = ""
	tr.turnThinking.Reset()
	tr.turnResponses.Reset()
	tr.curRole, tr.curGoal = "", ""
}

// RecordUserInput captures the user message that started this turn.
func (tr *TurnRecorder) RecordUserInput(input string) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if tr.turnUserInput != "" {
		tr.turnUserInput += "\n"
	}
	tr.turnUserInput += input
}

// RecordThinkingDelta accumulates a thinking token delta for the current turn.
func (tr *TurnRecorder) RecordThinkingDelta(text string) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	tr.turnThinking.WriteString(text)
}

// RecordAssistantDelta accumulates an assistant content token delta.
func (tr *TurnRecorder) RecordAssistantDelta(text string) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	tr.turnResponses.WriteString(text)
}

// RecordTokenStats captures token usage for the current turn.
func (tr *TurnRecorder) RecordTokenStats(promptN, predictedN, cacheRead, cacheWrite int, speed, cost float64, ctxEstimate, ctxMax int) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	tr.turnTokenUsage = TurnTokenUsage{
		PromptN:         promptN,
		PredictedN:      predictedN,
		CacheRead:       cacheRead,
		CacheWrite:      cacheWrite,
		SpeedTokPerSec:  speed,
		CostUSD:         cost,
		ContextEstimate: ctxEstimate,
		ContextMax:      ctxMax,
	}
}

// RecordContextStats updates only the context window stats without overwriting
// token counts, speed, or cost. This is called from EventContextStats to avoid
// losing the token data already set by an earlier EventTokenStats call.
func (tr *TurnRecorder) RecordContextStats(ctxEstimate, ctxMax int) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	tr.turnTokenUsage.ContextEstimate = ctxEstimate
	tr.turnTokenUsage.ContextMax = ctxMax
}

// RecordToolCall appends a tool call to the current turn.
func (tr *TurnRecorder) RecordToolCall(name, input, callID string) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	tr.turnToolCallsAccum = append(tr.turnToolCallsAccum, TurnToolCall{
		Name: name, Input: input, CallID: callID,
	})
}

// RecordToolResult appends a tool result to the current turn.
func (tr *TurnRecorder) RecordToolResult(callID, name, result string) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	tr.turnToolResultsAccum = append(tr.turnToolResultsAccum, TurnToolResult{
		CallID: callID, Name: name, Result: result,
	})
}

// FinalizeTurn builds a TurnRecord from the accumulated state, appends it to
// history, and returns the record. The active agent is used to capture the
// request/response JSON. goalID tags the record with the goal active at
// finalize time ("" = none).
func (tr *TurnRecorder) FinalizeTurn(agent *agentic.Agent, goalID string) TurnRecord {
	requestJSON, responseJSON := buildTurnJSON(agent)

	tr.mu.Lock()
	totalTime := time.Since(tr.turnStartTime)

	// Compute tokens used from accumulated stats
	tokensUsed := tr.turnTokenUsage.PromptN + tr.turnTokenUsage.PredictedN

	thinking := splitNonEmpty(tr.turnThinking.String())
	tr.turnThinking.Reset()
	responses := splitNonEmpty(tr.turnResponses.String())
	tr.turnResponses.Reset()

	record := TurnRecord{
		Number:       tr.currentTurnNumberLocked(),
		RequestJSON:  requestJSON,
		ResponseJSON: responseJSON,
		TokensUsed:   tokensUsed,
		TokenUsage:   tr.turnTokenUsage,
		Timing: TurnTiming{
			Total:  totalTime.Seconds(),
			TTFT:   0,
			Phases: make(map[string]float64),
		},
		ToolCalls:          append([]TurnToolCall(nil), tr.turnToolCallsAccum...),
		ToolResults:        append([]TurnToolResult(nil), tr.turnToolResultsAccum...),
		UserInput:          tr.turnUserInput,
		Thinking:           thinking,
		AssistantResponses: responses,
		AgentRole:          "main",
		GoalID:             goalID,
	}
	tr.turnHistory = append(tr.turnHistory, record)
	tr.turnToolCallsAccum = nil
	tr.turnToolResultsAccum = nil
	tr.turnTokenUsage = TurnTokenUsage{}
	tr.turnUserInput = ""
	tr.curRole, tr.curGoal = "", ""
	tr.turnStartTime = time.Time{} // mark no active turn
	tr.mu.Unlock()
	return record
}

// currentTurnNumberLocked returns the number the next history append will
// carry. Callers must hold tr.mu.
func (tr *TurnRecorder) currentTurnNumberLocked() int {
	return len(tr.turnHistory) + 1
}

// RecordCompletion appends one LLM API call's usage to the session-scoped
// completion log. Called for every EventTokenStats observed on the main agent
// (one per streaming round) and for every sub-agent stats callback, so a
// multi-call turn keeps its per-call granularity. turnNumber of 0 resolves to
// the current in-progress turn number, whose (role, goal) identity the
// recorder remembers so CurrentTurn snapshots carry the same tags as the
// call log (an untagged snapshot grouped under the wrong /stats:cache
// section).
func (tr *TurnRecorder) RecordCompletion(role, goalID string, u TurnTokenUsage, turnNumber int) CompletionRecord {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if turnNumber <= 0 {
		turnNumber = tr.currentTurnNumberLocked()
		tr.curRole, tr.curGoal = role, goalID
	}
	rec := CompletionRecord{
		TurnNumber: turnNumber,
		PromptN:    u.PromptN,
		CacheRead:  u.CacheRead,
		CacheWrite: u.CacheWrite,
		AgentRole:  role,
		GoalID:     goalID,
	}
	tr.completions = append(tr.completions, rec)
	return rec
}

// CompletionHistory returns a copy of the per-API-call completion log in
// chronological order.
func (tr *TurnRecorder) CompletionHistory() []CompletionRecord {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	result := make([]CompletionRecord, len(tr.completions))
	copy(result, tr.completions)
	return result
}

// RecordSubAgentTurn appends a completed sub-agent turn (companion, workflow
// stage, team member) directly to history. Sub-agent turns are observed
// end-of-turn through the multiagent pool's per-agent observer — they are
// never accumulated incrementally like the main agent's — so this takes the
// final token usage as a whole. Numbering continues the shared sequence so
// the cache view can interleave main and sub-agent turns chronologically.
// Each sub-agent turn is also logged as one completion so the per-call view
// covers sub-agents alongside the main agent.
func (tr *TurnRecorder) RecordSubAgentTurn(role, goalID string, u TurnTokenUsage) TurnRecord {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	num := tr.currentTurnNumberLocked()
	record := TurnRecord{
		Number:     num,
		TokensUsed: u.PromptN + u.PredictedN,
		TokenUsage: u,
		Timing:     TurnTiming{Phases: make(map[string]float64)},
		AgentRole:  role,
		GoalID:     goalID,
	}
	tr.turnHistory = append(tr.turnHistory, record)
	tr.completions = append(tr.completions, CompletionRecord{
		TurnNumber: num,
		PromptN:    u.PromptN,
		CacheRead:  u.CacheRead,
		CacheWrite: u.CacheWrite,
		AgentRole:  role,
		GoalID:     goalID,
	})
	return record
}

// TurnHistory returns a copy of all completed turns.
func (tr *TurnRecorder) TurnHistory() []TurnRecord {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	result := make([]TurnRecord, len(tr.turnHistory))
	copy(result, tr.turnHistory)
	return result
}

// LastTurn returns the most recent completed turn, or nil if none.
func (tr *TurnRecorder) LastTurn() *TurnRecord {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if len(tr.turnHistory) == 0 {
		return nil
	}
	record := tr.turnHistory[len(tr.turnHistory)-1]
	return &record
}

// CurrentTurn returns a snapshot of the in-progress turn, or nil if no turn
// is active. The returned record has Number set to len(history)+1, Timing
// computed from the turn start time, and the agent/goal identity of the
// turn's calls (see RecordCompletion).
func (tr *TurnRecorder) CurrentTurn() *TurnRecord {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if tr.turnStartTime.IsZero() {
		return nil
	}
	tokensUsed := tr.turnTokenUsage.PromptN + tr.turnTokenUsage.PredictedN
	return &TurnRecord{
		Number:             len(tr.turnHistory) + 1,
		TokensUsed:         tokensUsed,
		TokenUsage:         tr.turnTokenUsage,
		Timing:             TurnTiming{Total: time.Since(tr.turnStartTime).Seconds()},
		ToolCalls:          append([]TurnToolCall(nil), tr.turnToolCallsAccum...),
		ToolResults:        append([]TurnToolResult(nil), tr.turnToolResultsAccum...),
		UserInput:          tr.turnUserInput,
		Thinking:           splitNonEmpty(tr.turnThinking.String()),
		AssistantResponses: splitNonEmpty(tr.turnResponses.String()),
		AgentRole:          tr.curRole,
		GoalID:             tr.curGoal,
	}
}

// splitNonEmpty splits s into non-empty lines/paragraphs.
func splitNonEmpty(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, "\n")
	var out []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// buildTurnJSON captures the full conversation history and last assistant response.
func buildTurnJSON(agent *agentic.Agent) (requestJSON, responseJSON string) {
	if agent == nil {
		return "", ""
	}
	history := agent.GetHistory()
	if data, err := json.MarshalIndent(history, "", "  "); err == nil {
		requestJSON = string(data)
	}
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].Role == agentic.Assistant && history[i].Content != "" {
			responseJSON = history[i].Content
			break
		}
	}
	return
}
