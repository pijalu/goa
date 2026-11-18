// SPDX-License-Identifier: GPL-3.0-or-later

package protocol

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/agentic/provider/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const hy4Suffix = "6124c78e"

func hy4Model() schema.Model {
	return schema.Model{ID: "hy4-flash-free", Api: schema.ApiOpenAICompletions, Provider: schema.ProviderOpenCodeGo}
}

func hy4SSEDelta(field, value string) string {
	chunk := map[string]any{"choices": []any{map[string]any{
		"index": 0,
		"delta": map[string]any{field: value},
	}}}
	data, _ := json.Marshal(chunk)
	return "data: " + string(data)
}

func parseHy4Deltas(t *testing.T, profile schema.VariantProfile, deltas ...string) ([]schema.AssistantMessageEvent, *schema.AssistantMessage) {
	t.Helper()
	lines := make([]string, 0, len(deltas)+2)
	for _, delta := range deltas {
		lines = append(lines, hy4SSEDelta("content", delta))
	}
	lines = append(lines,
		`data: {"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		"data: [DONE]",
	)
	stream := schema.NewAssistantMessageEventStream(256)
	p := ForAPI(schema.ApiOpenAICompletions)
	require.NotNil(t, p)
	go func() {
		_ = ParseResponseForModel(p, sseBody(lines...), stream, hy4Model(), profile)
	}()
	var events []schema.AssistantMessageEvent
	for event := range stream.Seq() {
		events = append(events, event)
	}
	require.NoError(t, stream.Err())
	return events, stream.Result()
}

func parseHy4Profile(t *testing.T, deltas ...string) ([]schema.AssistantMessageEvent, *schema.AssistantMessage) {
	t.Helper()
	return parseHy4Deltas(t, schema.ResolveProfile(hy4Model()), deltas...)
}

func TestHy4Completions_ThinkMarkerEveryByteBoundary(t *testing.T) {
	for _, suffix := range []string{hy4Suffix, "a09F"} {
		marker := "</think:" + suffix + ">"
		input := "Need edit." + marker + "Need add grounded,"
		for split := 0; split <= len(input); split++ {
			t.Run(fmt.Sprintf("suffix_%s_split_%d", suffix, split), func(t *testing.T) {
				events, result := parseHy4Profile(t, input[:split], input[split:])
				require.NotNil(t, result)
				assert.Equal(t, "Need edit.Need add grounded,", textFromResult(result))
				for _, event := range events {
					assert.NotContains(t, event.Delta, "think:")
				}
			})
		}
	}
}

func TestHy4Completions_NormalizesContentAndThinkingWithoutDroppingProse(t *testing.T) {
	lines := []string{
		hy4SSEDelta("reasoning_content", "before <tool_calls:"+hy4Suffix+">"),
		hy4SSEDelta("reasoning_content", "<tool_call:"+hy4Suffix+">bash<arg_key:"+hy4Suffix+">command</arg_key:"+hy4Suffix+"><arg_value:"+hy4Suffix+">"),
		hy4SSEDelta("reasoning_content", "bash scripts/check-jargon.sh\n HEAD</arg_value:"+hy4Suffix+"><arg_key:"+hy4Suffix+">timeout</arg_key:"+hy4Suffix+"><arg_value:"+hy4Suffix+">60</arg_value:"+hy4Suffix+">"),
		hy4SSEDelta("reasoning_content", "</tool_call:"+hy4Suffix+"></tool_calls:"+hy4Suffix+"> after"),
		hy4SSEDelta("content", ".</think:"+hy4Suffix+">Need add grounded,"),
		`data: {"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
		"data: [DONE]",
	}
	stream := schema.NewAssistantMessageEventStream(64)
	go func() {
		_ = ParseResponseForModel(ForAPI(schema.ApiOpenAICompletions), sseBody(lines...), stream, hy4Model(), schema.ResolveProfile(hy4Model()))
	}()
	var events []schema.AssistantMessageEvent
	for event := range stream.Seq() {
		events = append(events, event)
		assert.NotContains(t, event.Delta, hy4Suffix)
	}
	result := stream.Result()
	require.NotNil(t, result)
	require.Len(t, result.Content, 3)
	assert.Equal(t, "before  after", result.Content[0].Thinking)
	assert.Equal(t, ".Need add grounded,", result.Content[1].Text)
	call := result.Content[2]
	assert.Equal(t, "bash", call.ToolName)
	assert.JSONEq(t, `{"command":"bash scripts/check-jargon.sh\n HEAD","timeout":60}`, call.ToolArguments)
	assert.NotEmpty(t, call.ToolCallID)
}

func TestHy4Completions_TaggedCallEveryByteSplit(t *testing.T) {
	wrapped := "<tool_calls:" + hy4Suffix + "><tool_call:" + hy4Suffix + ">read" +
		"<arg_key:" + hy4Suffix + ">path</arg_key:" + hy4Suffix + ">" +
		"<arg_value:" + hy4Suffix + ">docs/COMMANDS.md</arg_value:" + hy4Suffix + ">" +
		"</tool_call:" + hy4Suffix + "></tool_calls:" + hy4Suffix + ">"
	for split := 0; split <= len(wrapped); split++ {
		t.Run(fmt.Sprintf("split_%d", split), func(t *testing.T) {
			lines := []string{
				hy4SSEDelta("reasoning_content", wrapped[:split]),
				hy4SSEDelta("reasoning_content", wrapped[split:]),
				`data: {"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
				"data: [DONE]",
			}
			stream := schema.NewAssistantMessageEventStream(32)
			go func() {
				_ = ParseResponseForModel(ForAPI(schema.ApiOpenAICompletions), sseBody(lines...), stream, hy4Model(), schema.ResolveProfile(hy4Model()))
			}()
			for range stream.Seq() {
			}
			result := stream.Result()
			require.NotNil(t, result)
			require.Len(t, result.Content, 1)
			assert.Equal(t, "read", result.Content[0].ToolName)
			assert.JSONEq(t, `{"path":"docs/COMMANDS.md"}`, result.Content[0].ToolArguments)
		})
	}
}

func TestHy4Completions_MixedNativeAndTaggedCallsKeepOrderAndIndexes(t *testing.T) {
	tagged := "<tool_calls:" + hy4Suffix + "><tool_call:" + hy4Suffix + ">read<arg_key:" + hy4Suffix + ">path</arg_key:" + hy4Suffix + "><arg_value:" + hy4Suffix + ">middle.txt</arg_value:" + hy4Suffix + "></tool_call:" + hy4Suffix + "></tool_calls:" + hy4Suffix + ">"
	lines := []string{
		`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"native-first","type":"function","function":{"name":"read","arguments":"{\"path\":\"first.txt\"}"}}]}}]}`,
		hy4SSEDelta("reasoning_content", tagged),
		`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":1,"id":"native-last","type":"function","function":{"name":"read","arguments":"{\"path\":\"last.txt\"}"}}]}}]}`,
		`data: {"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
		"data: [DONE]",
	}
	stream := schema.NewAssistantMessageEventStream(64)
	go func() {
		_ = ParseResponseForModel(ForAPI(schema.ApiOpenAICompletions), sseBody(lines...), stream, hy4Model(), schema.ResolveProfile(hy4Model()))
	}()
	var events []schema.AssistantMessageEvent
	for event := range stream.Seq() {
		events = append(events, event)
	}
	result := stream.Result()
	require.NotNil(t, result)
	require.Len(t, result.Content, 3)
	assert.Equal(t, "native-first", result.Content[0].ToolCallID)
	assert.Equal(t, "read", result.Content[1].ToolName)
	assert.Equal(t, "native-last", result.Content[2].ToolCallID)
	assert.JSONEq(t, `{"path":"first.txt"}`, result.Content[0].ToolArguments)
	assert.JSONEq(t, `{"path":"middle.txt"}`, result.Content[1].ToolArguments)
	assert.JSONEq(t, `{"path":"last.txt"}`, result.Content[2].ToolArguments)
	seenIndexes := map[int]bool{}
	for _, event := range events {
		if event.Type == schema.EventToolCallStart {
			assert.False(t, seenIndexes[event.ContentIndex], "duplicate content index %d", event.ContentIndex)
			seenIndexes[event.ContentIndex] = true
		}
	}
	assert.True(t, seenIndexes[0])
	assert.True(t, seenIndexes[1])
}

func TestHy4Completions_TaggedBeforeNativeAndAdjacentTaggedCalls(t *testing.T) {
	taggedCall := func(path string) string {
		return "<tool_call:" + hy4Suffix + ">read<arg_key:" + hy4Suffix + ">path</arg_key:" + hy4Suffix + ">" +
			"<arg_value:" + hy4Suffix + ">" + path + "</arg_value:" + hy4Suffix + "></tool_call:" + hy4Suffix + ">"
	}
	lines := []string{
		hy4SSEDelta("reasoning_content", "<tool_calls:"+hy4Suffix+">"+taggedCall("first.txt")+taggedCall("middle.txt")+"</tool_calls:"+hy4Suffix+">"),
		`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"native-last","type":"function","function":{"name":"read","arguments":"{\"path\":\"last.txt\"}"}}]}}]}`,
		`data: {"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
		"data: [DONE]",
	}
	stream := schema.NewAssistantMessageEventStream(64)
	go func() {
		_ = ParseResponseForModel(ForAPI(schema.ApiOpenAICompletions), sseBody(lines...), stream, hy4Model(), schema.ResolveProfile(hy4Model()))
	}()
	var started []string
	for event := range stream.Seq() {
		if event.Type == schema.EventToolCallStart {
			require.NotNil(t, event.Partial)
			require.Len(t, event.Partial.Content, 1)
			started = append(started, event.Partial.Content[0].ToolArguments)
		}
	}
	result := stream.Result()
	require.NotNil(t, result)
	require.Len(t, result.Content, 3)
	assert.Equal(t, []string{`{"path":"first.txt"}`, `{"path":"middle.txt"}`, `{"path":"last.txt"}`}, started)
	assert.Equal(t, "native-last", result.Content[2].ToolCallID)
	for i, path := range []string{"first.txt", "middle.txt", "last.txt"} {
		assert.JSONEq(t, `{"path":"`+path+`"}`, result.Content[i].ToolArguments)
	}
}

func TestHy4Completions_IncompleteWrapperProducesOnlyRejectedShape(t *testing.T) {
	incomplete := "<tool_calls:" + hy4Suffix + "><tool_call:" + hy4Suffix + ">edit<arg_key:" + hy4Suffix + ">path</arg_key:" + hy4Suffix + "><arg_value:" + hy4Suffix + ">test.txt</arg_value:" + hy4Suffix + "></tool_call:" + hy4Suffix + ">"
	_, result := parseHy4Profile(t, "before "+incomplete)
	require.NotNil(t, result)
	require.Len(t, result.Content, 2)
	assert.Equal(t, "before ", result.Content[0].Text)
	call := result.Content[1]
	assert.Equal(t, "edit", call.ToolName)
	assert.False(t, json.Valid([]byte(call.ToolArguments)), "incomplete tagged call must reach the existing non-executing argument gate; arguments=%q", call.ToolArguments)
	assert.NotContains(t, textFromResult(result), hy4Suffix)
}

func TestHy4Completions_MissingArgumentValueCannotBorrowNextPair(t *testing.T) {
	input := "<tool_calls:" + hy4Suffix + "><tool_call:" + hy4Suffix + ">edit" +
		"<arg_key:" + hy4Suffix + ">path</arg_key:" + hy4Suffix + ">" +
		"<arg_key:" + hy4Suffix + ">timeout</arg_key:" + hy4Suffix + ">" +
		"<arg_value:" + hy4Suffix + ">60</arg_value:" + hy4Suffix + ">" +
		"</tool_call:" + hy4Suffix + "></tool_calls:" + hy4Suffix + ">"
	_, result := parseHy4Profile(t, input)
	require.NotNil(t, result)
	require.Len(t, result.Content, 1)
	assert.False(t, json.Valid([]byte(result.Content[0].ToolArguments)),
		"a key without a value must not borrow the next key's value: %q", result.Content[0].ToolArguments)
}

func TestHy4Completions_ControlsRetainStandardBehavior(t *testing.T) {
	input := "xml <note>keep</note> <tool_call:not-hex>keep</tool_call:not-hex> </think:" + hy4Suffix + ">"
	t.Run("legacy ParseResponse", func(t *testing.T) {
		stream := schema.NewAssistantMessageEventStream(16)
		go ForAPI(schema.ApiOpenAICompletions).ParseResponse(sseBody(hy4SSEDelta("content", input), "data: [DONE]"), stream)
		assert.Equal(t, input, textFromResult(stream.Result()))
	})
	t.Run("explicit none", func(t *testing.T) {
		profile := schema.ResolveProfile(hy4Model())
		profile.Compat.ResponseDialect = schema.ResponseDialectNone
		_, result := parseHy4Deltas(t, profile, input)
		assert.Equal(t, input, textFromResult(result))
	})
	t.Run("Hy4 preserves unrelated XML and nonhex tags", func(t *testing.T) {
		_, result := parseHy4Profile(t, input)
		assert.Equal(t, "xml <note>keep</note> <tool_call:not-hex>keep</tool_call:not-hex> ", textFromResult(result))
	})
}

func TestHy4Completions_AwareParserLeavesNonHy4AndDeepSeekFieldsUnchanged(t *testing.T) {
	model := schema.Model{ID: "deepseek-v4-flash", Api: schema.ApiOpenAICompletions, Provider: schema.ProviderDeepSeek}
	profile := schema.ResolveProfile(model)
	const marker = "</think:6124c78e>"
	stream := schema.NewAssistantMessageEventStream(16)
	go func() {
		_ = ParseResponseForModel(ForAPI(model.Api), sseBody(
			hy4SSEDelta("reasoning_content", "reason"+marker),
			hy4SSEDelta("content", "answer"+marker),
			"data: [DONE]",
		), stream, model, profile)
	}()
	result := stream.Result()
	require.NotNil(t, result)
	require.Len(t, result.Content, 2)
	assert.Equal(t, "reason"+marker, result.Content[0].Thinking)
	assert.Equal(t, "answer"+marker, result.Content[1].Text)

	// Even a stale/manual Hy4 capability cannot broaden activation to another
	// model or API; the model-aware entry point checks the exact model again.
	model.ID = "hy4-flash-free-preview"
	profile.Compat.ResponseDialect = schema.ResponseDialectHy4
	stream = schema.NewAssistantMessageEventStream(8)
	go func() {
		_ = ParseResponseForModel(ForAPI(model.Api), sseBody(hy4SSEDelta("content", "x"+marker), "data: [DONE]"), stream, model, profile)
	}()
	assert.Equal(t, "x"+marker, textFromResult(stream.Result()))
}

func TestHy4Completions_TypedAndMultilineArguments(t *testing.T) {
	call := "<tool_call:" + hy4Suffix + ">edit"
	for _, item := range [][2]string{
		{"new_content", "first line\n<second-line>"},
		{"replace_all", "true"},
		{"count", "2"},
		{"options", `{"depth":1}`},
	} {
		call += "<arg_key:" + hy4Suffix + ">" + item[0] + "</arg_key:" + hy4Suffix + ">" +
			"<arg_value:" + hy4Suffix + ">" + item[1] + "</arg_value:" + hy4Suffix + ">"
	}
	call += "</tool_call:" + hy4Suffix + ">"
	_, result := parseHy4Profile(t, "<tool_calls:"+hy4Suffix+">"+call+"</tool_calls:"+hy4Suffix+">")
	require.NotNil(t, result)
	require.Len(t, result.Content, 1)
	assert.JSONEq(t, `{"new_content":"first line\n<second-line>","replace_all":true,"count":2,"options":{"depth":1}}`, result.Content[0].ToolArguments)
}

func TestHy4Completions_OneByteDeltasNeverLeakMarkers(t *testing.T) {
	input := "a</think:" + hy4Suffix + ">b"
	deltas := make([]string, 0, len(input))
	for _, r := range []byte(input) {
		deltas = append(deltas, string([]byte{r}))
	}
	events, result := parseHy4Profile(t, deltas...)
	assert.Equal(t, "ab", textFromResult(result))
	var streamed strings.Builder
	for _, event := range events {
		if event.Type == schema.EventTextDelta {
			streamed.WriteString(event.Delta)
			assert.NotContains(t, event.Delta, "<")
		}
	}
	assert.Equal(t, "ab", streamed.String())
}
