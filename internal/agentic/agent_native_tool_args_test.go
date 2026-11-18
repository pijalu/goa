// SPDX-License-Identifier: GPL-3.0-or-later

package agentic

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pijalu/goa/internal/agentic/provider"
)

type nativeArgumentFixture struct {
	ID        string `json:"id"`
	Arguments string `json:"arguments"`
}

type nativeArgumentProvider struct {
	api    provider.Api
	rounds [][]provider.AssistantMessageEvent
	calls  atomic.Int32

	mu       sync.Mutex
	contexts []provider.Context
}

func (p *nativeArgumentProvider) API() provider.Api { return p.api }

func (p *nativeArgumentProvider) Stream(_ provider.Model, ctx provider.Context, _ provider.StreamOptions) (*provider.AssistantMessageEventStream, error) {
	round := int(p.calls.Add(1)) - 1
	p.mu.Lock()
	p.contexts = append(p.contexts, ctx)
	p.mu.Unlock()
	result := provider.NewAssistantMessageEventStream(64)
	go func() {
		events := nativeTextEvents("done")
		if round < len(p.rounds) {
			events = p.rounds[round]
		}
		for _, event := range events {
			result.Push(event)
		}
		if len(events) > 0 && events[len(events)-1].ToolCall != nil {
			result.End(&provider.AssistantMessage{StopReason: provider.StopReasonToolCall})
			return
		}
		result.End(&provider.AssistantMessage{
			Content:    []provider.ContentBlock{{Type: provider.ContentBlockText, Text: "done"}},
			StopReason: provider.StopReasonEndTurn,
		})
	}()
	return result, nil
}

func (p *nativeArgumentProvider) StreamSimple(m provider.Model, c provider.Context, o provider.SimpleStreamOptions) (*provider.AssistantMessageEventStream, error) {
	return p.Stream(m, c, provider.BuildSimpleOptions(m, o))
}

func nativeCallEvents(id, arguments string) []provider.AssistantMessageEvent {
	return []provider.AssistantMessageEvent{
		{Type: provider.EventThinkingDelta, Delta: "reasoning before the call"},
		{Type: provider.EventToolCallEnd, ToolCall: &provider.ContentBlock{
			Type: provider.ContentBlockToolCall, ToolCallID: id, ToolName: "edit", ToolArguments: arguments,
		}},
	}
}

func nativeTextEvents(text string) []provider.AssistantMessageEvent {
	return []provider.AssistantMessageEvent{{Type: provider.EventTextDelta, Delta: text}}
}

type nativeArgumentTool struct {
	calls atomic.Int32
	file  string

	mu     sync.Mutex
	inputs []string
}

func (t *nativeArgumentTool) Schema() ToolSchema {
	return ToolSchema{Name: "edit", Description: "instrumented edit", Schema: map[string]any{
		"type": "object", "properties": map[string]any{"new_content": map[string]any{"type": "string"}},
	}}
}

func (t *nativeArgumentTool) Execute(input string) (string, error) {
	t.calls.Add(1)
	t.mu.Lock()
	t.inputs = append(t.inputs, input)
	t.mu.Unlock()
	if err := os.WriteFile(t.file, []byte("EXECUTED"), 0o600); err != nil {
		return "", err
	}
	return "edited", nil
}

func (t *nativeArgumentTool) IsRetryable(error) bool { return false }

var nativeArgumentAPISequence atomic.Int32

func runNativeArgumentAgent(t *testing.T, rounds [][]provider.AssistantMessageEvent) (*nativeArgumentProvider, *nativeArgumentTool, *Agent, []OutputEvent, string) {
	t.Helper()
	file := filepath.Join(t.TempDir(), "test.txt")
	if err := os.WriteFile(file, []byte("ORIGINAL"), 0o600); err != nil {
		t.Fatal(err)
	}
	tool := &nativeArgumentTool{file: file}
	st := &nativeArgumentProvider{
		api:    provider.Api(fmt.Sprintf("native-argument-test-%d", nativeArgumentAPISequence.Add(1))),
		rounds: rounds,
	}
	provider.RegisterApiProvider(st)
	a := NewAgent(Config{
		Model:        provider.Model{ID: "hy4-flash-free", Api: st.API(), Provider: provider.ProviderCustom, InputTypes: []string{"text"}},
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
	cacheID, cacheGeneration := a.cacheContextID, a.cacheGeneration
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := a.Run(ctx, "edit the file")
	if a.cacheContextID != cacheID || a.cacheGeneration != cacheGeneration {
		t.Error("append-only native argument recovery changed cache identity")
	}
	a.Stop()
	stopDrain()
	<-drained
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return st, tool, a, obs.Events(), file
}

func loadNativeArgumentFixtures(t *testing.T) []nativeArgumentFixture {
	t.Helper()
	data, err := os.ReadFile("testdata/hy4_truncated_native_calls.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []nativeArgumentFixture
	if err := json.Unmarshal(data, &fixtures); err != nil {
		t.Fatal(err)
	}
	if len(fixtures) != 6 {
		t.Fatalf("fixture count = %d, want 6", len(fixtures))
	}
	return fixtures
}

func TestAgentNativeToolArguments_CapturedTruncationsNeverExecute(t *testing.T) {
	for _, fixture := range loadNativeArgumentFixtures(t) {
		t.Run(fixture.ID, func(t *testing.T) {
			st, tool, a, events, file := runNativeArgumentAgent(t, [][]provider.AssistantMessageEvent{
				nativeCallEvents(fixture.ID, fixture.Arguments), nativeTextEvents("done"),
			})
			if got := tool.calls.Load(); got != 0 {
				t.Fatalf("concrete edit executions = %d, want 0", got)
			}
			data, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			if string(data) != "ORIGINAL" {
				t.Fatalf("malformed edit changed file to %q", data)
			}
			if got := st.calls.Load(); got != 2 {
				t.Fatalf("provider rounds = %d, want malformed round + one recovery round", got)
			}
			assertNativeProtocolResult(t, a.GetHistory(), events, fixture.ID, fixture.Arguments, "truncated_tool_arguments")
		})
	}
}

func assertNativeProtocolResult(t *testing.T, history []Message, events []OutputEvent, id, rawArguments, code string) {
	t.Helper()
	assertNativeHistoryPair(t, history, id, rawArguments, code)
	assertNativeProtocolEvents(t, events, id, code)
}

func assertNativeHistoryPair(t *testing.T, history []Message, id, rawArguments, code string) {
	t.Helper()
	assistant, result := findNativeHistoryPair(history, id)
	if assistant == nil || result == nil {
		t.Fatalf("missing assistant/result pair for %s: %#v", id, history)
	}
	for _, call := range assistant.ToolCalls {
		if call.ID == id && call.Arguments != rawArguments {
			t.Errorf("history arguments were rewritten:\ngot  %q\nwant %q", call.Arguments, rawArguments)
		}
	}
	if !strings.Contains(result.Content, code) || !strings.Contains(result.Content, "not executed") {
		t.Errorf("protocol result = %q, want code %q and non-execution guidance", result.Content, code)
	}
	if result.Metadata[metaToolError] != "true" {
		t.Errorf("result error metadata = %q, want true", result.Metadata[metaToolError])
	}
}

func findNativeHistoryPair(history []Message, id string) (*Message, *Message) {
	var assistant *Message
	var result *Message
	for i := range history {
		for _, call := range history[i].ToolCalls {
			if call.ID == id {
				assistant = &history[i]
			}
		}
		if history[i].Role == ToolRole && history[i].ToolCallID == id {
			result = &history[i]
		}
	}
	return assistant, result
}

func assertNativeProtocolEvents(t *testing.T, events []OutputEvent, id, code string) {
	t.Helper()
	starts, results := 0, 0
	for _, event := range events {
		if event.ToolCallID != id {
			continue
		}
		switch event.Type {
		case EventToolStart:
			starts++
		case EventToolResult:
			results++
			if !strings.Contains(event.ToolResult, code) {
				t.Errorf("visible result = %q, want %q", event.ToolResult, code)
			}
		}
	}
	if starts != 0 || results != 1 {
		t.Errorf("events for %s: starts=%d results=%d, want 0/1", id, starts, results)
	}
}

func TestAgentNativeToolArguments_ValidReissueExecutesOnce(t *testing.T) {
	const valid = `{"path":"test.txt","old_string":"ORIGINAL","new_string":"CHANGED"}`
	st, tool, a, events, _ := runNativeArgumentAgent(t, [][]provider.AssistantMessageEvent{
		nativeCallEvents("truncated", `{"path":"test.txt","new_content":"unfinished`),
		nativeCallEvents("valid", valid),
		nativeTextEvents("done"),
	})
	if got := tool.calls.Load(); got != 1 {
		t.Fatalf("executions = %d, want one valid reissue", got)
	}
	if got := st.calls.Load(); got != 3 {
		t.Fatalf("provider rounds = %d, want 3", got)
	}
	assertNativeProtocolResult(t, a.GetHistory(), events, "truncated", `{"path":"test.txt","new_content":"unfinished`, "truncated_tool_arguments")
}

func TestAgentNativeToolArguments_RecoveryIsBounded(t *testing.T) {
	rounds := make([][]provider.AssistantMessageEvent, 8)
	for i := range rounds {
		rounds[i] = nativeCallEvents(fmt.Sprintf("truncated-%d", i), fmt.Sprintf(`{"path":"test.txt","new_content":"unfinished %d`, i))
	}
	st, tool, a, events, file := runNativeArgumentAgent(t, rounds)
	if got := tool.calls.Load(); got != 0 {
		t.Fatalf("executions = %d, want 0", got)
	}
	if got := st.calls.Load(); got != 3 {
		t.Fatalf("provider rounds = %d, want initial call + two reissues", got)
	}
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "ORIGINAL" {
		t.Fatalf("file changed to %q", data)
	}
	assertNativeProtocolResult(t, a.GetHistory(), events, "truncated-2", `{"path":"test.txt","new_content":"unfinished 2`, "recovery_limit")
}

func TestAgentNativeToolArguments_MixedBatchExecutesOnlyValidCall(t *testing.T) {
	const valid = `{"path":"test.txt","old_string":"ORIGINAL","new_string":"CHANGED"}`
	first := nativeCallEvents("truncated", `{"path":"test.txt","new_content":"unfinished`)
	second := nativeCallEvents("valid", valid)
	round := append(append([]provider.AssistantMessageEvent{}, first...), second[1])
	_, tool, a, events, _ := runNativeArgumentAgent(t, [][]provider.AssistantMessageEvent{round, nativeTextEvents("done")})
	if got := tool.calls.Load(); got != 1 {
		t.Fatalf("executions = %d, want valid sibling only", got)
	}
	assertNativeProtocolResult(t, a.GetHistory(), events, "truncated", `{"path":"test.txt","new_content":"unfinished`, "truncated_tool_arguments")
}

func TestAgentNativeToolArguments_NextRequestIsStrictAndCacheStable(t *testing.T) {
	fixture := loadNativeArgumentFixtures(t)[0]
	st, _, a, _, _ := runNativeArgumentAgent(t, [][]provider.AssistantMessageEvent{
		nativeCallEvents(fixture.ID, fixture.Arguments), nativeTextEvents("done"),
	})
	st.mu.Lock()
	defer st.mu.Unlock()
	if len(st.contexts) != 2 {
		t.Fatalf("captured contexts = %d, want 2", len(st.contexts))
	}
	assertNativeRecoveryRequest(t, st.contexts[1], fixture.ID)
	assertNativeHistoryPreserved(t, a.GetHistory(), fixture)
}

func assertNativeRecoveryRequest(t *testing.T, ctx provider.Context, id string) {
	t.Helper()
	var call, result *provider.ContentBlock
	for i := range ctx.Messages {
		for j := range ctx.Messages[i].Content {
			block := &ctx.Messages[i].Content[j]
			if block.ToolCallID != id {
				continue
			}
			switch block.Type {
			case provider.ContentBlockToolCall:
				call = block
			case provider.ContentBlockToolResult:
				result = block
			}
		}
	}
	if call == nil || result == nil {
		t.Fatalf("next request missing call/result pairing: call=%v result=%v", call != nil, result != nil)
	}
	if !json.Valid([]byte(provider.SafeToolArguments(call.ToolArguments))) {
		t.Errorf("request serializer cannot sanitize arguments: %q", call.ToolArguments)
	}
	if !result.IsError {
		t.Error("next request tool result is not marked as error")
	}
}

func assertNativeHistoryPreserved(t *testing.T, history []Message, fixture nativeArgumentFixture) {
	t.Helper()
	for _, msg := range history {
		for _, call := range msg.ToolCalls {
			if call.ID == fixture.ID && call.Arguments != fixture.Arguments {
				t.Error("strict serialization rewrote retained assistant history")
			}
		}
	}
}
