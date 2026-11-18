// SPDX-License-Identifier: GPL-3.0-or-later

package agentic

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pijalu/goa/internal/agentic/provider"
	"github.com/pijalu/goa/internal/agentic/provider/protocol"
	"github.com/pijalu/goa/internal/agentic/provider/schema"
)

type hy4ProtocolProvider struct {
	api    provider.Api
	rounds []string
	calls  atomic.Int32
}

func (p *hy4ProtocolProvider) API() provider.Api { return p.api }

func (p *hy4ProtocolProvider) Stream(_ provider.Model, _ provider.Context, _ provider.StreamOptions) (*provider.AssistantMessageEventStream, error) {
	round := int(p.calls.Add(1)) - 1
	body := p.rounds[len(p.rounds)-1]
	if round < len(p.rounds) {
		body = p.rounds[round]
	}
	stream := provider.NewAssistantMessageEventStream(128)
	go func() {
		model := schema.Model{ID: "hy4-flash-free", Api: schema.ApiOpenAICompletions, Provider: schema.ProviderOpenCodeGo}
		parser := protocol.ForAPI(model.Api)
		if err := protocol.ParseResponseForModel(parser, strings.NewReader(body), stream, model, schema.ResolveProfile(model)); err != nil {
			stream.CloseWithError(err)
		}
	}()
	return stream, nil
}

func (p *hy4ProtocolProvider) StreamSimple(m provider.Model, c provider.Context, o provider.SimpleStreamOptions) (*provider.AssistantMessageEventStream, error) {
	return p.Stream(m, c, provider.BuildSimpleOptions(m, o))
}

func hy4AgentDelta(field, value string) string {
	chunk := map[string]any{"choices": []any{map[string]any{
		"index": 0,
		"delta": map[string]any{field: value},
	}}}
	data, _ := json.Marshal(chunk)
	return "data: " + string(data) + "\n\n"
}

func hy4AgentSSE(fieldValues ...[2]string) string {
	var body strings.Builder
	for _, item := range fieldValues {
		body.WriteString(hy4AgentDelta(item[0], item[1]))
	}
	body.WriteString("data: [DONE]\n\n")
	return body.String()
}

type hy4ReadTool struct {
	calls  atomic.Int32
	mu     sync.Mutex
	inputs []string
}

func (t *hy4ReadTool) Schema() ToolSchema {
	return ToolSchema{Name: "read", Description: "instrumented read", Schema: map[string]any{
		"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}},
	}}
}

func (t *hy4ReadTool) Execute(input string) (string, error) {
	t.calls.Add(1)
	t.mu.Lock()
	t.inputs = append(t.inputs, input)
	t.mu.Unlock()
	return "read", nil
}

func (t *hy4ReadTool) IsRetryable(error) bool { return false }

func runHy4ProtocolAgent(t *testing.T, tool Tool, firstRound string) (*hy4ProtocolProvider, *Agent, []OutputEvent) {
	t.Helper()
	p := &hy4ProtocolProvider{
		api: provider.Api(fmt.Sprintf("hy4-protocol-test-%d", testProviderCounter.Add(1))),
		rounds: []string{
			firstRound,
			hy4AgentSSE([2]string{"content", "done"}),
		},
	}
	provider.RegisterApiProvider(p)
	a := NewAgent(Config{
		Model:        testModel(p.API()),
		SystemPrompt: "test",
		Logger:       NewLogger(Error),
		Tools:        []Tool{tool},
	})
	obs := &mockEventObserver{}
	a.AddObserver(obs)
	drainCtx, stopDrain := context.WithCancel(context.Background())
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		for {
			select {
			case <-a.Output:
			case <-drainCtx.Done():
				return
			}
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := a.Run(ctx, "exercise Hy4"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	a.Stop()
	stopDrain()
	<-drained
	return p, a, obs.Events()
}

func TestAgentHy4Protocol_MixedNativeAndTaggedCallsExecuteExactlyOnce(t *testing.T) {
	const suffix = "6124c78e"
	tagged := "<tool_calls:" + suffix + "><tool_call:" + suffix + ">read" +
		"<arg_key:" + suffix + ">path</arg_key:" + suffix + ">" +
		"<arg_value:" + suffix + ">middle.txt</arg_value:" + suffix + ">" +
		"</tool_call:" + suffix + "></tool_calls:" + suffix + ">"
	native := func(index int, id, path string) string {
		return `data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":` + fmt.Sprint(index) +
			`,"id":"` + id + `","type":"function","function":{"name":"read","arguments":"{\"path\":\"` + path + `\"}"}}]}}]}` + "\n\n"
	}
	body := native(0, "native-first", "first.txt") + hy4AgentDelta("reasoning_content", "before "+tagged+" after") +
		native(1, "native-last", "last.txt") +
		"data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\n"

	tool := &hy4ReadTool{}
	p, a, events := runHy4ProtocolAgent(t, tool, body)
	if got := tool.calls.Load(); got != 3 {
		t.Fatalf("executions = %d, want native/tagged/native exactly once", got)
	}
	if got := p.calls.Load(); got != 2 {
		t.Fatalf("provider rounds = %d, want tool round + final answer", got)
	}
	assertHy4MixedPaths(t, tool)
	assertNoHy4HistoryMarkers(t, a.GetHistory())
	assertHy4EventsClean(t, events, suffix)
}

func assertHy4MixedPaths(t *testing.T, tool *hy4ReadTool) {
	t.Helper()
	tool.mu.Lock()
	inputs := append([]string(nil), tool.inputs...)
	tool.mu.Unlock()
	seen := map[string]int{}
	for _, input := range inputs {
		var args map[string]any
		if err := json.Unmarshal([]byte(input), &args); err != nil {
			t.Fatalf("tool input %q: %v", input, err)
		}
		seen[fmt.Sprint(args["path"])]++
	}
	for _, path := range []string{"first.txt", "middle.txt", "last.txt"} {
		if seen[path] != 1 {
			t.Errorf("path %s executions = %d, inputs=%v", path, seen[path], inputs)
		}
	}
}

func assertHy4EventsClean(t *testing.T, events []OutputEvent, suffix string) {
	t.Helper()
	starts := 0
	for _, event := range events {
		if event.Type == EventToolStart && event.ToolName == "read" {
			starts++
		}
		if strings.Contains(event.Text, suffix) || strings.Contains(event.ToolResult, suffix) {
			t.Errorf("visible event leaked Hy4 marker: %+v", event)
		}
	}
	if starts != 3 {
		t.Errorf("tool starts = %d, want 3", starts)
	}
}

func TestAgentHy4Protocol_IncompleteTaggedCallNeverExecutes(t *testing.T) {
	const suffix = "6124c78e"
	file := filepath.Join(t.TempDir(), "test.txt")
	tool := &nativeArgumentTool{file: file}
	// The inner call and its arguments are closed; only the outer wrapper is
	// truncated. That must still be non-executable.
	call := "<tool_calls:" + suffix + "><tool_call:" + suffix + ">edit" +
		"<arg_key:" + suffix + ">path</arg_key:" + suffix + "><arg_value:" + suffix + ">test.txt</arg_value:" + suffix + ">" +
		"<arg_key:" + suffix + ">new_content</arg_key:" + suffix + "><arg_value:" + suffix + ">CHANGED</arg_value:" + suffix + ">" +
		"</tool_call:" + suffix + ">"
	p, a, events := runHy4ProtocolAgent(t, tool, hy4AgentSSE([2]string{"reasoning_content", call}))
	if got := tool.calls.Load(); got != 0 {
		t.Fatalf("concrete executions = %d, want 0", got)
	}
	if got := p.calls.Load(); got != 2 {
		t.Fatalf("provider rounds = %d, want rejection + one recovery response", got)
	}
	var id string
	for _, msg := range a.GetHistory() {
		for _, call := range msg.ToolCalls {
			if call.Name == "edit" {
				id = call.ID
			}
		}
	}
	if id == "" {
		t.Fatal("incomplete tagged call was not retained for recovery")
	}
	assertNativeProtocolResult(t, a.GetHistory(), events, id, "{", "truncated_tool_arguments")
	assertNoHy4HistoryMarkers(t, a.GetHistory())
}

func assertNoHy4HistoryMarkers(t *testing.T, history []Message) {
	t.Helper()
	for _, msg := range history {
		if strings.Contains(msg.Content, "tool_calls:") || strings.Contains(msg.Content, "tool_call:") ||
			strings.Contains(msg.Content, "arg_key:") || strings.Contains(msg.Content, "arg_value:") ||
			strings.Contains(msg.Content, "think:") {
			t.Errorf("history retained Hy4 control bytes: %+v", msg)
		}
	}
}
