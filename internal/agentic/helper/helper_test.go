// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package helper

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/agentic"
)

func TestIntegration_FullConversation(t *testing.T) {
	bus := NewOutputBus()

	var consoleBuf bytes.Buffer
	console := NewConsoleObserver(WithConsoleWriter(&consoleBuf))
	bus.AddObserver(console)

	logObs := NewMessageLogObserver()
	bus.AddObserver(logObs)

	simulateConversation(bus)
	bus.Close()

	assertConsoleOutput(t, consoleBuf.String())
	history := assertLogHistory(t, logObs, 6)
	assertToolResultAttached(t, history[3], "2")
	assertJSONSerializes(t, logObs, 6)
}

func simulateConversation(bus *OutputBus) {
	bus.Send(agentic.Message{Type: agentic.Content, Role: agentic.System, Content: "You are helpful", Delta: false})
	bus.Send(agentic.Message{Type: agentic.Content, Role: agentic.User, Content: "Calculate 1+1", Delta: false})
	bus.Send(agentic.Message{Type: agentic.Content, Role: agentic.Assistant, Thinking: "Let me think...", Delta: true})
	bus.Send(agentic.Message{Type: agentic.Content, Role: agentic.Assistant, Content: "I'll use the calculator", Delta: false})
	bus.Send(agentic.Message{
		Type:       agentic.ToolCall,
		ToolName:   "calculator",
		ToolInput:  `{"a":1,"b":1,"op":"+"}`,
		ToolCallID: "call_1",
	})
	bus.Send(agentic.Message{
		Type:       agentic.Content,
		Role:       agentic.ToolRole,
		Content:    "2",
		ToolCallID: "call_1",
		Delta:      false,
	})
	bus.Send(agentic.Message{Type: agentic.Content, Role: agentic.Assistant, Content: "The answer is 2", Delta: false})
}

func assertConsoleOutput(t *testing.T, consoleOut string) {
	t.Helper()
	wantSubstrings := []string{"[thinking]", "[content]", "[tool_call] calculator", "[tool_result]", "[end]"}
	for _, want := range wantSubstrings {
		if !strings.Contains(consoleOut, want) {
			t.Errorf("console missing %q: %q", want, consoleOut)
		}
	}
}

func assertLogHistory(t *testing.T, logObs *MessageLogObserver, wantLen int) []StructuredMessage {
	t.Helper()
	history := logObs.History()
	if len(history) != wantLen {
		t.Fatalf("expected %d messages, got %d: %+v", wantLen, len(history), history)
	}
	roles := []string{"system", "user", "assistant", "assistant", "tool", "assistant"}
	for i, want := range roles {
		if history[i].Role != want {
			t.Errorf("history[%d].Role = %s, want %s", i, history[i].Role, want)
		}
	}
	return history
}

func assertToolResultAttached(t *testing.T, toolCallMsg StructuredMessage, wantResult string) {
	t.Helper()
	if len(toolCallMsg.Elements) != 1 || toolCallMsg.Elements[0].Type != "tool_call" {
		t.Fatal("expected tool_call element")
	}
	if toolCallMsg.Elements[0].ToolResult != wantResult {
		t.Errorf("tool result not attached: got %s", toolCallMsg.Elements[0].ToolResult)
	}
}

func assertJSONSerializes(t *testing.T, logObs *MessageLogObserver, wantLen int) {
	t.Helper()
	data, err := logObs.JSON()
	if err != nil {
		t.Fatalf("JSON() error: %v", err)
	}
	var result []StructuredMessage
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("JSON unmarshal error: %v", err)
	}
	if len(result) != wantLen {
		t.Errorf("JSON had %d messages, want %d", len(result), wantLen)
	}
}

func TestIntegration_MultipleObserversSameEvents(t *testing.T) {
	bus := NewOutputBus()

	var buf1, buf2 bytes.Buffer
	obs1 := NewConsoleObserver(WithConsoleWriter(&buf1))
	obs2 := NewConsoleObserver(WithConsoleWriter(&buf2))
	bus.AddObserver(obs1)
	bus.AddObserver(obs2)

	bus.Send(agentic.Message{Type: agentic.Content, Role: agentic.Assistant, Content: "Hello", Delta: false})
	bus.Close()

	if buf1.String() != buf2.String() {
		t.Errorf("observers received different output:\nobs1: %q\nobs2: %q", buf1.String(), buf2.String())
	}
}
