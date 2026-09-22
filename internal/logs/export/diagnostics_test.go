// SPDX-License-Identifier: GPL-3.0-or-later

package export

import (
	"testing"

	"github.com/pijalu/goa/internal/agentic/provider/transport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func summaryRef(s transport.RequestSummary) *transport.RequestSummary {
	return &s
}

// TestBuildLLMTrace_ToolResultFollowedUp verifies that a healthy tool round
// trip produces NO "tool result not forwarded" anomaly.
func TestBuildLLMTrace_ToolResultFollowedUp(t *testing.T) {
	entries := []transport.HTTPLogEntry{
		{
			Timestamp:    "t1",
			StatusCode:   200,
			FinishReason: "tool_calls",
			RequestSummary: summaryRef(transport.RequestSummary{
				MessageCount: 5, LastRole: "user", Roles: []string{"system", "user"},
			}),
		},
		{
			Timestamp:    "t2",
			StatusCode:   200,
			FinishReason: "stop",
			RequestSummary: summaryRef(transport.RequestSummary{
				MessageCount: 7, LastRole: "assistant",
				ToolCallBlocks:   1,
				ToolResultBlocks: 1, // increased => tool result was forwarded
				Roles:            []string{"assistant", "tool"},
			}),
		},
	}

	trace := buildLLMTrace(entries)
	assert.Len(t, trace.Requests, 2)
	for _, a := range trace.Anomalies {
		assert.NotContains(t, a, "did not increase toolResultBlocks")
	}
}

// TestBuildLLMTrace_ToolResultNotForwarded reproduces the user's hypothesis:
// the model called a tool, but the next request did not include the tool
// result (toolResultBlocks unchanged) -> anomaly flagged.
func TestBuildLLMTrace_ToolResultNotForwarded(t *testing.T) {
	entries := []transport.HTTPLogEntry{
		{
			StatusCode:   200,
			FinishReason: "tool_calls",
			RequestSummary: summaryRef(transport.RequestSummary{
				MessageCount: 5, ToolResultBlocks: 0,
			}),
		},
		{
			StatusCode:   200,
			FinishReason: "stop",
			RequestSummary: summaryRef(transport.RequestSummary{
				MessageCount:     6, // grew, but...
				ToolResultBlocks: 0, // ...the tool result was NOT appended
			}),
		},
	}

	trace := buildLLMTrace(entries)
	found := false
	for _, a := range trace.Anomalies {
		if contains(a, "did not increase toolResultBlocks") {
			found = true
		}
	}
	assert.True(t, found, "expected a 'tool result not forwarded' anomaly")
}

// TestBuildLLMTrace_LastRequestWasToolResult flags a turn that ended right
// after a tool result was sent to the model (possible silent stop).
func TestBuildLLMTrace_LastRequestWasToolResult(t *testing.T) {
	entries := []transport.HTTPLogEntry{
		{
			StatusCode:   200,
			FinishReason: "tool_calls",
			RequestSummary: summaryRef(transport.RequestSummary{
				MessageCount: 6, LastRole: "tool", LastIsToolResult: true,
				ToolResultBlocks: 1,
			}),
		},
	}
	trace := buildLLMTrace(entries)
	found := false
	for _, a := range trace.Anomalies {
		if contains(a, "lastRole=tool") {
			found = true
		}
	}
	assert.True(t, found)
}

// TestBuildLLMTrace_FinishReasonLength flags a truncated response.
func TestBuildLLMTrace_FinishReasonLength(t *testing.T) {
	entries := []transport.HTTPLogEntry{
		{StatusCode: 200, FinishReason: "length",
			RequestSummary: summaryRef(transport.RequestSummary{MessageCount: 3})},
	}
	trace := buildLLMTrace(entries)
	found := false
	for _, a := range trace.Anomalies {
		if contains(a, "finish_reason=length") {
			found = true
		}
	}
	assert.True(t, found)
}

// TestBuildLLMTrace_Empty has no entries and no anomalies.
func TestBuildLLMTrace_Empty(t *testing.T) {
	trace := buildLLMTrace(nil)
	assert.Empty(t, trace.Requests)
	assert.Empty(t, trace.Anomalies)
}

// TestBuildLLMTrace_PendingLastRequest reproduces the luna stuck-session
// export: the final request never finalized (stream still open). The trace
// must carry the pending marker AND an anomaly calling it out — without it an
// open-but-stalled request looks like a healthy completed one.
func TestBuildLLMTrace_PendingLastRequest(t *testing.T) {
	entries := []transport.HTTPLogEntry{
		{Timestamp: "t1", StatusCode: 200, FinishReason: "tool_calls",
			RequestSummary: summaryRef(transport.RequestSummary{MessageCount: 5, LastRole: "tool", LastIsToolResult: true, ToolResultBlocks: 1})},
		{Timestamp: "t2", StatusCode: 200, Pending: true,
			RequestSummary: summaryRef(transport.RequestSummary{MessageCount: 7})},
	}
	trace := buildLLMTrace(entries)
	require.Len(t, trace.Requests, 2)
	assert.True(t, trace.Requests[1].Pending, "pending marker must reach the trace")
	found := false
	for _, a := range trace.Anomalies {
		if contains(a, "still in flight at export time") {
			found = true
		}
	}
	assert.True(t, found, "expected a 'still in flight' anomaly for a pending last request")
}

// TestBuildLLMTrace_NoPendingAnomalyWhenFinalized guards against the pending
// flag leaking onto healthy completed timelines.
func TestBuildLLMTrace_NoPendingAnomalyWhenFinalized(t *testing.T) {
	entries := []transport.HTTPLogEntry{
		{Timestamp: "t1", StatusCode: 200, FinishReason: "stop",
			RequestSummary: summaryRef(transport.RequestSummary{MessageCount: 3})},
	}
	trace := buildLLMTrace(entries)
	for _, a := range trace.Anomalies {
		assert.NotContains(t, a, "still in flight")
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
