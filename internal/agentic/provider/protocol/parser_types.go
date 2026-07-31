// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package protocol

// parserMessageType categorizes parsed SSE messages.
type parserMessageType string

const (
	parserContent  parserMessageType = "content"
	parserToolCall parserMessageType = "tool_call"
	parserEnd      parserMessageType = "end"
)

// parserRole identifies the sender of a parsed message.
type parserRole string

const (
	parserRoleAssistant parserRole = "assistant"
)

// parserTimings holds token generation performance metrics.
type parserTimings struct {
	PromptN            int
	PredictedN         int
	PromptMs           float64
	PredictedMs        float64
	PromptPerSecond    float64
	PredictedPerSecond float64
	CacheReadTokens    int
	CacheWriteTokens   int
}

// parserMessage is the core unit parsed from an SSE stream.
type parserMessage struct {
	Type          parserMessageType
	Role          parserRole
	Content       string
	Thinking      string
	Delta         bool
	ToolName      string
	ToolInput     string
	ToolCallID    string
	ToolCallIndex int
	Timings       *parserTimings
	// FinishReason carries the raw provider finish_reason ("stop", "length",
	// "tool_calls", ...) on parserEnd messages so the stream accumulator can
	// map it to a schema.StopReason instead of flattening every end to
	// EndTurn. The agent needs "length" to detect window-edge truncation.
	FinishReason string
}
