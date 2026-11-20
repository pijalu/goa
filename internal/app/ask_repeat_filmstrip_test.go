// SPDX-License-Identifier: GPL-3.0-or-later

package app

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/internal/agentic/provider/mock"
	"github.com/pijalu/goa/tools/ask"
)

const (
	askValidExample = `{"questions":[{"question":"Which option?","options":["A","B"]}]}`
	askRecoveryText = "Recovered without repeating the invalid question."
)

type askRepeatFilmstripCase struct {
	name      string
	input     string
	errorCode string
}

// TestAskUserQuestionRepeatRecoveryFilmstrip replays the two malformed native
// payloads from the exported sessions through a real Agent and the real ask
// tool, then renders their actual OutputEvents through the production app
// handler. The provider's fourth stream represents the bounded text-only
// recovery response; a fifth stream would mean the loop was not terminated.
func TestAskUserQuestionRepeatRecoveryFilmstrip(t *testing.T) {
	tests := []askRepeatFilmstripCase{
		{name: "empty questions", input: `{"questions":[]}`, errorCode: "missing_questions"},
		{name: "missing question", input: `{"questions":[{}]}`, errorCode: "missing_question"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) { runAskRepeatFilmstripCase(t, tt) })
	}
}

func runAskRepeatFilmstripCase(t *testing.T, tt askRepeatFilmstripCase) {
	t.Helper()
	events := captureAskRepeatEvents(t, tt.input)
	assertAskRepeatEvents(t, events, tt.errorCode)

	sc := newUIScenario(t, 240, 45)
	for i := range events {
		sc.apply(&events[i])
	}
	rendered := sc.filmstrip().Render()
	t.Logf("filmstrip for %s:\n%s", tt.input, rendered)
	assertAskRepeatFilmstrip(t, rendered, tt.errorCode)
}

func captureAskRepeatEvents(t *testing.T, input string) []agentic.OutputEvent {
	t.Helper()
	prov := mock.New(t)
	model := prov.Model("ask-repeat")
	for i := 0; i < 3; i++ {
		prov.Script("ask-repeat", mock.ToolCallTurn("ask_user_question", fmt.Sprintf("call-%d", i+1), input))
	}
	prov.Script("ask-repeat", mock.TextTurn(askRecoveryText))

	tool := &ask.AskUserQuestionTool{Clarify: func(_, _, _ string, _ []string, _, _ int) (string, bool) {
		t.Error("clarify must not be called for malformed input")
		return "", false
	}}
	agent := agentic.NewAgent(agentic.Config{
		Model:                    model,
		SystemPrompt:             "test",
		Tools:                    []agentic.Tool{tool},
		MaxToolRepeatConsecutive: 2,
	})
	collector := &eventCollector{}
	agent.AddObserver(collector)
	if _, err := agent.RunAndCollect(context.Background(), "reproduce malformed question loop"); err != nil {
		t.Fatalf("RunAndCollect: %v", err)
	}
	if got := prov.Calls("ask-repeat"); got != 4 {
		t.Fatalf("provider streams = %d, want 3 ordinary rounds + 1 recovery", got)
	}

	collector.mu.Lock()
	defer collector.mu.Unlock()
	return append([]agentic.OutputEvent(nil), collector.events...)
}

func assertAskRepeatEvents(t *testing.T, events []agentic.OutputEvent, errorCode string) {
	t.Helper()
	counts := struct{ toolCalls, realErrors, hardLoops int }{}
	for i := range events {
		ev := &events[i]
		if ev.Type == agentic.EventToolCall {
			counts.toolCalls++
			continue
		}
		if ev.Type != agentic.EventToolResult {
			continue
		}
		if strings.Contains(ev.Text, errorCode) && strings.Contains(ev.Text, askValidExample) {
			counts.realErrors++
		}
		if strings.HasPrefix(strings.TrimSpace(ev.Text), agentic.ToolLoopMessagePrefix) {
			counts.hardLoops++
		}
	}
	want := struct{ toolCalls, realErrors, hardLoops int }{3, 2, 1}
	if counts != want {
		t.Fatalf("events: tool calls=%d real errors=%d hard loops=%d; want 3, 2, 1",
			counts.toolCalls, counts.realErrors, counts.hardLoops)
	}
}

func assertAskRepeatFilmstrip(t *testing.T, rendered, errorCode string) {
	t.Helper()
	compact := strings.Join(strings.Fields(rendered), " ")
	if !strings.Contains(compact, askValidExample) {
		t.Errorf("filmstrip does not show complete valid payload example %s", askValidExample)
	}
	if !strings.Contains(rendered, errorCode) {
		t.Errorf("filmstrip does not show %s validation error", errorCode)
	}
	if got := strings.Count(rendered, agentic.ToolLoopMessagePrefix); got != 1 {
		t.Errorf("visible hard-loop guardrails = %d, want 1", got)
	}
	if !strings.Contains(rendered, askRecoveryText) {
		t.Errorf("filmstrip does not show recovered assistant response %q", askRecoveryText)
	}
}
