// SPDX-License-Identifier: GPL-3.0-or-later

package provider

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/pijalu/goa/internal/agentic/provider/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenericStream_Hy4UsesResolvedResponseDialect(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"reasoning_content\":\"think <tool_calls:6124c78e><tool_call:6124c78e>read<arg_key:6124c78e>path</arg_key:6124c78e><arg_value:6124c78e>README.md</arg_value:6124c78e></tool_call:6124c78e></tool_calls:6124c78e> after\"}}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\".</think:6124c78e>clean\"}}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	model := schema.Model{
		ID: "hy4-flash-free", Api: schema.ApiOpenAICompletions,
		Provider: schema.ProviderOpenCodeGo, BaseURL: server.URL + "/v1",
	}
	stream, err := GenericStream(model, schema.Context{Messages: []schema.Message{schema.NewUserMessage("hi")}}, schema.StreamOptions{
		APIKey: "test-key",
	})
	require.NoError(t, err)
	result := stream.Result()
	require.NotNil(t, result)
	require.Len(t, result.Content, 3)
	assert.Equal(t, "think  after", result.Content[0].Thinking)
	assert.Equal(t, ".clean", result.Content[1].Text)
	assert.Equal(t, "read", result.Content[2].ToolName)
	assert.JSONEq(t, `{"path":"README.md"}`, result.Content[2].ToolArguments)
}
