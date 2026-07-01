// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package protocol

import (
	"io"
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/agentic/provider/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func googleSSE(lines ...string) io.Reader {
	return strings.NewReader(strings.Join(lines, "\n\n") + "\n\n")
}

func TestGoogleParseText(t *testing.T) {
	p := ForAPI(schema.ApiGoogleGenerativeAI)
	require.NotNil(t, p)

	stream := schema.NewAssistantMessageEventStream(8)
	go p.ParseResponse(googleSSE(
		`data: {"candidates":[{"content":{"parts":[{"text":"Hello"}]}}]}`,
		`data: {"candidates":[{"content":{"parts":[{"text":" world"}]},"finishReason":"STOP"}]}`,
	), stream)

	result := stream.Result()
	require.NotNil(t, result)
	assert.Equal(t, schema.StopReasonEndTurn, result.StopReason)
	assert.Equal(t, "Hello world", textFromResult(result))
}

func TestGoogleParseFunctionCall(t *testing.T) {
	p := ForAPI(schema.ApiGoogleGenerativeAI)
	require.NotNil(t, p)

	stream := schema.NewAssistantMessageEventStream(8)
	go p.ParseResponse(googleSSE(
		`data: {"candidates":[{"content":{"parts":[{"functionCall":{"name":"weather","args":{"city":"NYC"}}}]}}]}`,
		`data: {"candidates":[{"finishReason":"STOP"}]}`,
	), stream)

	result := stream.Result()
	require.NotNil(t, result)
	require.Len(t, result.Content, 1)
	assert.Equal(t, schema.ContentBlockToolCall, result.Content[0].Type)
	assert.Equal(t, "weather", result.Content[0].ToolName)
	assert.Contains(t, result.Content[0].ToolArguments, "NYC")
}
