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

// TurnRecorder captures completed agent turns, including tool calls and results.
// It is safe for concurrent use and is owned by AgentManager.
type TurnRecorder struct {
	mu                   sync.Mutex
	turnHistory          []TurnRecord
	turnToolCallsAccum   []TurnToolCall
	turnToolResultsAccum []TurnToolResult
	turnTokenUsage       TurnTokenUsage // accumulated usage for current turn
	turnStartTime        time.Time
	turnUserInput        string
	turnThinking         strings.Builder
	turnResponses        strings.Builder
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
// request/response JSON.
func (tr *TurnRecorder) FinalizeTurn(agent *agentic.Agent) TurnRecord {
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
		Number:       len(tr.turnHistory) + 1,
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
	}
	tr.turnHistory = append(tr.turnHistory, record)
	tr.turnToolCallsAccum = nil
	tr.turnToolResultsAccum = nil
	tr.turnTokenUsage = TurnTokenUsage{}
	tr.turnUserInput = ""
	tr.mu.Unlock()
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
