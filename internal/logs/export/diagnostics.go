// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package export

import (
	"strings"

	"github.com/pijalu/goa/internal/agentic/provider/transport"
)

// llmTrace is an agent-friendly, compact timeline of the most recent LLM
// requests plus automatically detected anomalies. It lets a reader diagnose
// issues such as "a tool result was never sent back to the model" or "the
// model was truncated mid-reasoning" without scanning thousands of events.
type llmTrace struct {
	Requests  []llmTraceRequest `json:"requests"`
	Anomalies []string          `json:"anomalies,omitempty"`
}

type llmTraceRequest struct {
	Seq             int     `json:"seq"`
	Timestamp       string  `json:"timestamp"`
	Model           string  `json:"model,omitempty"`
	StatusCode      int     `json:"statusCode,omitempty"`
	DurationMs      int64   `json:"durationMs"`
	MessageCount    int     `json:"messageCount"`
	LastRole        string  `json:"lastRole,omitempty"`
	LastIsToolResult bool   `json:"lastIsToolResult"`
	ToolCallBlocks  int     `json:"toolCallBlocks"`
	ToolResultBlocks int    `json:"toolResultBlocks"`
	Roles           []string `json:"roles,omitempty"`
	FinishReason    string  `json:"finishReason,omitempty"`
	Error           string  `json:"error,omitempty"`
}

// buildLLMTrace derives a compact diagnostic timeline from the HTTP log
// snapshot and flags patterns that commonly explain a silent stop.
func buildLLMTrace(entries []transport.HTTPLogEntry) llmTrace {
	trace := llmTrace{Requests: make([]llmTraceRequest, 0, len(entries))}
	if len(entries) == 0 {
		return trace
	}

	for i, e := range entries {
		req := llmTraceRequest{
			Seq:        i + 1,
			Timestamp:  e.Timestamp,
			StatusCode: e.StatusCode,
			DurationMs: e.DurationMs,
			FinishReason: e.FinishReason,
			Error:      e.Error,
		}
		if e.RequestSummary != nil {
			req.Model = e.RequestSummary.Model
			req.MessageCount = e.RequestSummary.MessageCount
			req.LastRole = e.RequestSummary.LastRole
			req.LastIsToolResult = e.RequestSummary.LastIsToolResult
			req.ToolCallBlocks = e.RequestSummary.ToolCallBlocks
			req.ToolResultBlocks = e.RequestSummary.ToolResultBlocks
			req.Roles = e.RequestSummary.Roles
		}
		trace.Requests = append(trace.Requests, req)
	}

	trace.Anomalies = detectLLMAnomalies(trace.Requests)
	return trace
}

// detectLLMAnomalies inspects the request timeline for patterns that commonly
// explain a session that silently stopped. Each detector is a small function so
// the composite stays within the complexity budget.
func detectLLMAnomalies(reqs []llmTraceRequest) []string {
	if len(reqs) == 0 {
		return nil
	}
	var flags []string
	flags = append(flags, lastRequestAnomaly(reqs)...)
	flags = append(flags, toolResultForwardAnomaly(reqs)...) // returns at most one
	flags = append(flags, perRequestAnomalies(reqs)...)
	return flags
}

// lastRequestAnomaly flags the final request when it ended after a tool result
// was sent (possible silent stop) or was truncated by a length cap.
func lastRequestAnomaly(reqs []llmTraceRequest) []string {
	last := reqs[len(reqs)-1]
	var flags []string
	if last.LastIsToolResult {
		flags = append(flags, "last request sent a tool result back to the model (lastRole=tool); " +
			"verify whether the model actually responded — if no later request exists, " +
			"the tool result may not have been followed up (possible silent stop)")
	}
	if strings.EqualFold(last.FinishReason, "length") {
		flags = append(flags, "last response finished with finish_reason=length: the model was truncated " +
			"(likely hit a max-tokens/reasoning cap) and may not have produced content or a tool call")
	}
	return flags
}

// toolResultForwardAnomaly detects a tool round-trip where the next request did
// not append the freshly executed tool result (toolResultBlocks unchanged).
func toolResultForwardAnomaly(reqs []llmTraceRequest) []string {
	for i := 1; i < len(reqs); i++ {
		prev, cur := reqs[i-1], reqs[i]
		if cur.ToolResultBlocks == prev.ToolResultBlocks &&
			cur.MessageCount > prev.MessageCount && prev.FinishReason != "" {
			return []string{"request " + itoa(cur.Seq) + " did not increase toolResultBlocks over request " +
				itoa(prev.Seq) + " (a tool result may not have been forwarded)"}
		}
	}
	return nil
}

// perRequestAnomalies flags errors and non-2xx status codes across requests.
func perRequestAnomalies(reqs []llmTraceRequest) []string {
	var flags []string
	for _, r := range reqs {
		if r.Error != "" {
			flags = append(flags, "request "+itoa(r.Seq)+" errored: "+r.Error)
		}
		if r.StatusCode != 0 && (r.StatusCode < 200 || r.StatusCode >= 300) {
			flags = append(flags, "request "+itoa(r.Seq)+" returned HTTP "+itoa(r.StatusCode))
		}
	}
	return flags
}

// itoa is a strconv-free int->string helper to keep imports minimal.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
