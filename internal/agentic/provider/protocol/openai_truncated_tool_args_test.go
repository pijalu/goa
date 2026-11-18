// SPDX-License-Identifier: GPL-3.0-or-later

package protocol

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"github.com/pijalu/goa/internal/agentic/provider/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOpenAICompletionsPreservesCapturedTruncatedArgumentsForAgentGate(t *testing.T) {
	data, err := os.ReadFile("../../testdata/hy4_truncated_native_calls.json")
	require.NoError(t, err)
	var fixtures []struct {
		ID        string `json:"id"`
		Arguments string `json:"arguments"`
	}
	require.NoError(t, json.Unmarshal(data, &fixtures))
	require.Len(t, fixtures, 6)

	for _, fixture := range fixtures {
		t.Run(fixture.ID, func(t *testing.T) {
			first := map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]any{
				"tool_calls": []any{map[string]any{"index": 0, "id": fixture.ID, "type": "function", "function": map[string]any{"name": "edit"}}},
			}}}}
			second := map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]any{
				"tool_calls": []any{map[string]any{"index": 0, "function": map[string]any{"arguments": fixture.Arguments}}},
			}}}}
			firstJSON, err := json.Marshal(first)
			require.NoError(t, err)
			secondJSON, err := json.Marshal(second)
			require.NoError(t, err)

			p := ForAPI(schema.ApiOpenAICompletions)
			stream := schema.NewAssistantMessageEventStream(16)
			go p.ParseResponse(sseBody(
				"data: "+string(firstJSON),
				"data: "+string(secondJSON),
				`data: {"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
				"data: [DONE]",
			), stream)
			result := stream.Result()
			require.NotNil(t, result)
			require.Len(t, result.Content, 1, fmt.Sprintf("fixture arguments length=%d", len(fixture.Arguments)))
			call := result.Content[0]
			assert.Equal(t, schema.ContentBlockToolCall, call.Type)
			assert.Equal(t, fixture.ID, call.ToolCallID)
			assert.Equal(t, fixture.Arguments, call.ToolArguments, "protocol must not repair arguments into executable input")
			assert.False(t, json.Valid([]byte(call.ToolArguments)))
		})
	}
}
