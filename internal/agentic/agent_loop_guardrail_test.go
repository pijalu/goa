// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pijalu/goa/internal/agentic/provider"
)

// loopScriptProvider returns scripted per-call text responses, one entry per
// Stream call. Calls beyond the script get a default tail response.
type loopScriptProvider struct {
	api    provider.Api
	mu     sync.Mutex
	script []string
	calls  int
}

func (p *loopScriptProvider) API() provider.Api { return p.api }

func (p *loopScriptProvider) Stream(model provider.Model, ctx provider.Context, opts provider.StreamOptions) (*provider.AssistantMessageEventStream, error) {
	p.mu.Lock()
	idx := p.calls
	p.calls++
	p.mu.Unlock()
	text := "script exhausted"
	if idx < len(p.script) {
		text = p.script[idx]
	}
	result := provider.NewAssistantMessageEventStream(64)
	go func() {
		result.Push(provider.AssistantMessageEvent{Type: provider.EventTextDelta, Delta: text})
		result.End(&provider.AssistantMessage{
			Content:    []provider.ContentBlock{{Type: provider.ContentBlockText, Text: text}},
			StopReason: provider.StopReasonEndTurn,
		})
	}()
	return result, nil
}

func (p *loopScriptProvider) StreamSimple(model provider.Model, ctx provider.Context, opts provider.SimpleStreamOptions) (*provider.AssistantMessageEventStream, error) {
	return p.Stream(model, ctx, provider.BuildSimpleOptions(model, opts))
}

func (p *loopScriptProvider) Calls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

func newLoopScriptAgent(t *testing.T, script []string) (*Agent, *loopScriptProvider) {
	t.Helper()
	p := &loopScriptProvider{
		api:    provider.Api(fmt.Sprintf("test-loopscript-%d", testProviderCounter.Add(1))),
		script: script,
	}
	provider.RegisterApiProvider(p)
	return NewAgent(Config{Model: testModel(p.api), SystemPrompt: "test"}), p
}

// TestRunawayLoopLatch_ResetLoopStopRecovers is the regression test for the
// bricked session: after the guardrail latches, a genuine new user input
// (ResetLoopStop, called by AgentManager for human turns and by the goal
// driver for runaway resumes) must clear the latch so the session proceeds.
func TestRunawayLoopLatch_ResetLoopStopRecovers(t *testing.T) {
	agent, p := newLoopScriptAgent(t, []string{
		"same answer", "same answer", "same answer", "recovery answer",
	})
	ctx := context.Background()

	if err := agent.Run(ctx, "turn 1"); err != nil {
		t.Fatalf("turn 1: %v", err)
	}
	if err := agent.Run(ctx, "turn 2"); err != nil {
		t.Fatalf("turn 2 (first repeat = warning only): %v", err)
	}
	if err := agent.Run(ctx, "turn 3"); err == nil || !strings.Contains(err.Error(), "runaway loop detected") {
		t.Fatalf("turn 3 = %v, want runaway loop detected error", err)
	}

	// Latched: the next turn is rejected WITHOUT contacting the provider.
	if err := agent.Run(ctx, "turn 4"); err == nil || !strings.Contains(err.Error(), "session stopped due to a runaway loop") {
		t.Fatalf("latched turn = %v, want session-stopped error", err)
	}
	if got := p.Calls(); got != 3 {
		t.Fatalf("provider calls = %d, want 3 (latched turn must not call the LLM)", got)
	}

	// A genuine new user message resets the latch: the session recovers.
	agent.ResetLoopStop()
	out, err := agent.RunAndCollect(ctx, "fresh prompt")
	if err != nil {
		t.Fatalf("turn after ResetLoopStop: %v", err)
	}
	if !strings.Contains(out, "recovery answer") {
		t.Fatalf("output = %q, want %q", out, "recovery answer")
	}
}

// TestRunawayLoopLatch_AutoExpires verifies the cooldown backstop: a latch
// older than loopStopCooldown no longer rejects turns, even without an
// explicit ResetLoopStop.
func TestRunawayLoopLatch_AutoExpires(t *testing.T) {
	agent, p := newLoopScriptAgent(t, []string{
		"same", "same", "same", "after expiry",
	})
	ctx := context.Background()

	for i := 0; i < 2; i++ {
		if err := agent.Run(ctx, fmt.Sprintf("turn %d", i+1)); err != nil {
			t.Fatalf("turn %d: %v", i+1, err)
		}
	}
	if err := agent.Run(ctx, "turn 3"); err == nil || !strings.Contains(err.Error(), "runaway loop detected") {
		t.Fatalf("turn 3 = %v, want runaway loop detected error", err)
	}

	// Backdate the latch beyond the cooldown.
	agent.mu.Lock()
	agent.loopStoppedAt = time.Now().Add(-loopStopCooldown - time.Minute)
	agent.mu.Unlock()

	out, err := agent.RunAndCollect(ctx, "later prompt")
	if err != nil {
		t.Fatalf("turn after cooldown expiry: %v", err)
	}
	if !strings.Contains(out, "after expiry") {
		t.Fatalf("output = %q, want %q", out, "after expiry")
	}
	if got := p.Calls(); got != 4 {
		t.Fatalf("provider calls = %d, want 4", got)
	}
	agent.mu.Lock()
	if agent.loopStopped {
		t.Fatal("latch still set after cooldown expiry")
	}
	if agent.assistantRepeatCount != 0 {
		t.Fatalf("repeat count = %d after expiry+progress, want 0", agent.assistantRepeatCount)
	}
	agent.mu.Unlock()
}

// TestCheckProgressLoop_SkipsStaleAssistantMessage is the regression test for
// the false strike: a turn that produced NO new assistant message (stream
// error, retry, pause) must not compare the stale last message against
// itself. Only messages appended at/after turnStartHistoryLen count.
func TestCheckProgressLoop_SkipsStaleAssistantMessage(t *testing.T) {
	agent := NewAgent(Config{SystemPrompt: "test"})

	seed := []Message{
		{Type: Content, Role: System, Content: "sys"},
		{Type: Content, Role: User, Content: "q"},
		{Type: Content, Role: Assistant, Content: "same"},
	}
	agent.mu.Lock()
	agent.history = append([]Message(nil), seed...)
	agent.lastAssistantHash = agent.hashAssistantMessage(seed[2])
	// Turn starts AFTER the last assistant message and appends nothing
	// (e.g. the stream errored): the stale message must not score a strike.
	agent.turnStartHistoryLen = len(agent.history)
	agent.mu.Unlock()

	if err := agent.checkProgressLoop(); err != nil {
		t.Fatalf("stale assistant message scored a strike: %v", err)
	}
	agent.mu.Lock()
	if agent.assistantRepeatCount != 0 {
		t.Fatalf("repeat count = %d after stale check, want 0", agent.assistantRepeatCount)
	}
	if agent.loopStopped {
		t.Fatal("latch set on stale message")
	}
	agent.mu.Unlock()

	// A NEW identical message appended during the turn DOES count (hint).
	agent.mu.Lock()
	agent.turnStartHistoryLen = len(agent.history)
	agent.history = append(agent.history, Message{Type: Content, Role: Assistant, Content: "same"})
	agent.mu.Unlock()
	if err := agent.checkProgressLoop(); err != nil {
		t.Fatalf("first genuine repeat = %v, want warning hint only", err)
	}
	agent.mu.Lock()
	if agent.assistantRepeatCount != 1 {
		t.Fatalf("repeat count = %d after first genuine repeat, want 1", agent.assistantRepeatCount)
	}
	agent.mu.Unlock()

	// A second NEW identical message latches.
	agent.mu.Lock()
	agent.turnStartHistoryLen = len(agent.history)
	agent.history = append(agent.history, Message{Type: Content, Role: Assistant, Content: "same"})
	agent.mu.Unlock()
	err := agent.checkProgressLoop()
	if err == nil || !strings.Contains(err.Error(), "runaway loop detected") {
		t.Fatalf("second genuine repeat = %v, want runaway loop detected", err)
	}
	agent.mu.Lock()
	if !agent.loopStopped {
		t.Fatal("latch not set after second genuine repeat")
	}
	if agent.loopStoppedAt.IsZero() {
		t.Fatal("loopStoppedAt not recorded at latch time")
	}
	agent.mu.Unlock()
}

// TestClear_ResetsLoopGuardrail ensures starting a new conversation also
// clears the runaway-loop latch and counters.
func TestClear_ResetsLoopGuardrail(t *testing.T) {
	agent := NewAgent(Config{SystemPrompt: "test"})
	agent.mu.Lock()
	agent.loopStopped = true
	agent.loopStoppedAt = time.Now()
	agent.assistantRepeatCount = 2
	agent.lastAssistantHash = "abc"
	agent.mu.Unlock()

	agent.Clear()

	agent.mu.Lock()
	defer agent.mu.Unlock()
	if agent.loopStopped || !agent.loopStoppedAt.IsZero() || agent.assistantRepeatCount != 0 || agent.lastAssistantHash != "" {
		t.Fatal("Clear did not reset runaway-loop guardrail state")
	}
}

// runGuardrailTurn simulates one completed assistant turn: appends the given
// messages to history, marks them as new-since-turn-start, and runs the
// detector exactly as Agent.Run does after processTurnWithStream.
func runGuardrailTurn(t *testing.T, agent *Agent, msgs ...Message) error {
	t.Helper()
	agent.mu.Lock()
	agent.turnStartHistoryLen = len(agent.history)
	agent.history = append(agent.history, msgs...)
	agent.mu.Unlock()
	return agent.checkProgressLoop()
}

// toolResult builds one tool-result message for callID. Results whose
// content starts with "Error:" carry the live metaToolError="true" marker,
// mirroring appendToolResults; strip Metadata manually to simulate history
// reloaded from a persisted session.
func toolResult(callID, name, content string) Message {
	res := Message{
		Type: Content, Role: ToolRole, Content: content,
		ToolName: name, ToolCallID: callID,
	}
	if strings.HasPrefix(content, "Error:") {
		res.Metadata = map[string]string{metaToolError: "true"}
	}
	return res
}

// toolTurn builds an assistant turn that only issues tool calls — empty
// content/thinking, like goal-mode turns — followed by one successful result
// per call. Each entry is {callID, toolName, arguments}.
func toolTurn(pairs ...[3]string) []Message {
	msgs := make([]Message, 0, len(pairs)*2)
	for _, p := range pairs {
		id := p[0]
		msgs = append(msgs,
			Message{Type: Content, Role: Assistant,
				ToolCalls: []ToolCallInfo{{ID: id, Type: "function", Name: p[1], Arguments: p[2]}}},
			toolResult(id, p[1], "ok"),
		)
	}
	return msgs
}

// singleCallTurn builds a one-call turn with an explicit result content.
func singleCallTurn(callID, name, args, result string) []Message {
	return []Message{
		{Type: Content, Role: Assistant,
			ToolCalls: []ToolCallInfo{{ID: callID, Type: "function", Name: name, Arguments: args}}},
		toolResult(callID, name, result),
	}
}

// TestHashAssistantMessage_ToolFingerprints verifies the fingerprint scheme:
// different tools or arguments must produce different hashes (the old
// count-only scheme collapsed every tool turn into the same hash), while
// semantically equal JSON arguments — differing only in key order or
// whitespace — must stay equal.
func TestHashAssistantMessage_ToolFingerprints(t *testing.T) {
	mk := func(content string, calls ...ToolCallInfo) Message {
		return Message{Type: Content, Role: Assistant, Content: content, ToolCalls: calls}
	}
	call := func(name, args string) ToolCallInfo {
		return ToolCallInfo{ID: "id-" + name + args, Type: "function", Name: name, Arguments: args}
	}

	tests := []struct {
		name      string
		a, b      Message
		wantEqual bool
	}{
		{
			name: "same text, different tool names differ",
			a:    mk("go", call("read", `{"path":"a.go"}`)),
			b:    mk("go", call("bash", `{"cmd":"ls"}`)),
		},
		{
			name: "same tool, different args differ",
			a:    mk("go", call("read", `{"path":"a.go"}`)),
			b:    mk("go", call("read", `{"path":"b.go"}`)),
		},
		{
			name:      "same tool, reordered JSON keys equal",
			a:         mk("go", call("write", `{"path":"x.go","content":"hi"}`)),
			b:         mk("go", call("write", `{"content":"hi","path":"x.go"}`)),
			wantEqual: true,
		},
		{
			name:      "same everything equal",
			a:         mk("go", call("read", `{"path":"a.go"}`)),
			b:         mk("go", call("read", `{"path":"a.go"}`)),
			wantEqual: true,
		},
		{
			name: "one vs two calls of same tool differ",
			a:    mk("", call("bash", `{"cmd":"ls"}`)),
			b:    mk("", call("bash", `{"cmd":"ls"}`), call("bash", `{"cmd":"pwd"}`)),
		},
		{
			name:      "empty vs empty-object args equal",
			a:         mk("", call("list", "")),
			b:         mk("", call("list", `{}`)),
			wantEqual: true,
		},
		{
			name: "invalid-JSON args fall back to raw text",
			a:    mk("", call("legacy", `path=a.go`)),
			b:    mk("", call("legacy", `path=b.go`)),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			agent := NewAgent(Config{SystemPrompt: "test"})
			got := agent.hashAssistantMessage(tt.a) == agent.hashAssistantMessage(tt.b)
			if got != tt.wantEqual {
				t.Fatalf("hashes equal=%v, want equal=%v\n  a=%q\n  b=%q", got, tt.wantEqual,
					agent.hashAssistantMessage(tt.a), agent.hashAssistantMessage(tt.b))
			}
		})
	}
}

// TestCheckProgressLoop_EmptyToolTurnsWithDifferentToolsSurvive is the core
// regression for the reported false positives: a healthy agent running tools
// every turn with little or no prose must never accumulate strikes.
func TestCheckProgressLoop_EmptyToolTurnsWithDifferentToolsSurvive(t *testing.T) {
	agent := NewAgent(Config{SystemPrompt: "test"})
	// Four consecutive goal-mode turns: each ran a DIFFERENT tool with an
	// empty assistant response. The old count-only fingerprint latched on
	// turn 3 here (see the recreation against pre-fix HEAD).
	turns := [][]Message{
		toolTurn([3]string{"c1", "read", `{"path":"a_test.go"}`}),
		toolTurn([3]string{"c2", "bash", `{"cmd":"go test ./internal/agentic/"}`}),
		toolTurn([3]string{"c3", "grep", `{"pattern":"fingerprint"}`}),
		toolTurn([3]string{"c4", "edit", `{"path":"a.go"}`}),
	}
	for i, turn := range turns {
		if err := runGuardrailTurn(t, agent, turn...); err != nil {
			t.Fatalf("turn %d: unexpected guardrail error: %v", i+1, err)
		}
	}
	assertNoStrikes(t, agent)
}

// TestCheckProgressLoop_IdenticalFingerprintWithSuccessfulResult verifies the
// strike gate: same tool, same args, successful-but-different output each
// time (e.g. polling a file that grows) — the fingerprint repeats but the
// world changed, so every turn counts as progress.
func TestCheckProgressLoop_IdenticalFingerprintWithSuccessfulResult(t *testing.T) {
	agent := NewAgent(Config{SystemPrompt: "test"})
	for i := 0; i < 4; i++ {
		turn := singleCallTurn("poll", "read", `{"path":"counter.txt"}`,
			fmt.Sprintf("count=%d", i+1))
		if err := runGuardrailTurn(t, agent, turn...); err != nil {
			t.Fatalf("turn %d: unexpected guardrail error: %v", i+1, err)
		}
	}
	assertNoStrikes(t, agent)
}

// TestCheckProgressLoop_IdenticalProsePlusDifferentTools covers plan rule 1:
// identical prose plus different tool calls per turn scores no strike.
func TestCheckProgressLoop_IdenticalProsePlusDifferentTools(t *testing.T) {
	agent := NewAgent(Config{SystemPrompt: "test"})
	for i, name := range []string{"read", "bash", "grep"} {
		turn := toolTurn([3]string{fmt.Sprintf("c%d", i), name, fmt.Sprintf(`{"step":%d}`, i)})
		turn[0].Content = "Working on it." // identical prose every turn
		if err := runGuardrailTurn(t, agent, turn...); err != nil {
			t.Fatalf("turn %d (%s): unexpected guardrail error: %v", i+1, name, err)
		}
	}
	assertNoStrikes(t, agent)
}

// TestCheckProgressLoop_SuccessResultWinsOverErrorPrefix verifies that the
// live metaToolError="false" marker wins over text sniffing: a command whose
// OUTPUT starts with "Error:" but exited fine is not a failure, so repeats
// stay progress.
func TestCheckProgressLoop_SuccessResultWinsOverErrorPrefix(t *testing.T) {
	agent := NewAgent(Config{SystemPrompt: "test"})
	for i := 0; i < 3; i++ {
		turn := singleCallTurn("meta", "bash", `{"cmd":"./check"}`, "Error: none, all good")
		turn[1].Metadata = map[string]string{metaToolError: "false"} // live success marker
		if err := runGuardrailTurn(t, agent, turn...); err != nil {
			t.Fatalf("turn %d: unexpected guardrail error: %v", i+1, err)
		}
	}
	assertNoStrikes(t, agent)
}

// TestCheckProgressLoop_FailedToolTurnsStillStrike guards the gating
// boundary: identical turns whose tool calls ALL fail are a real stall and
// keep the existing warn-then-latch behavior.
func TestCheckProgressLoop_FailedToolTurnsStillStrike(t *testing.T) {
	agent := NewAgent(Config{SystemPrompt: "test"})
	failing := func() []Message {
		return singleCallTurn("f", "bash", `{"cmd":"make test"}`, "Error: exit 1")
	}

	// Turn 1 establishes the baseline fingerprint.
	if err := runGuardrailTurn(t, agent, failing()...); err != nil {
		t.Fatalf("turn 1: %v", err)
	}

	// Turn 2: identical failing call => first strike, warning hint appended.
	if err := runGuardrailTurn(t, agent, failing()...); err != nil {
		t.Fatalf("turn 2 (first repeat) = %v, want warning only", err)
	}
	agent.mu.Lock()
	count := agent.assistantRepeatCount
	hinted := false
	for _, m := range agent.history {
		if m.Role == System && strings.Contains(m.Content, "[goa-system] Your last response was identical") {
			hinted = true
		}
	}
	agent.mu.Unlock()
	if count != 1 || !hinted {
		t.Fatalf("after first repeat: count=%d hinted=%v, want count=1 hinted=true", count, hinted)
	}

	// Turn 3: third identical failing response => latch.
	err := runGuardrailTurn(t, agent, failing()...)
	if err == nil || !strings.Contains(err.Error(), "runaway loop detected") {
		t.Fatalf("turn 3 = %v, want runaway loop detected", err)
	}
	agent.mu.Lock()
	latched := agent.loopStopped
	agent.mu.Unlock()
	if !latched {
		t.Fatal("latch not set after repeated failing tool turns")
	}
}

// TestCheckProgressLoop_LegacyPrefixFallbackStrikes covers reloaded history:
// persisted sessions drop Metadata, so classification falls back to the
// conventional "Error:" content prefix — identical failing turns still strike.
func TestCheckProgressLoop_LegacyPrefixFallbackStrikes(t *testing.T) {
	agent := NewAgent(Config{SystemPrompt: "test"})
	legacy := func() []Message {
		turn := singleCallTurn("f", "bash", `{"cmd":"make test"}`, "Error: exit 1")
		turn[1].Metadata = nil // simulate history reloaded from a saved session
		return turn
	}
	if err := runGuardrailTurn(t, agent, legacy()...); err != nil {
		t.Fatalf("turn 1: %v", err)
	}
	if err := runGuardrailTurn(t, agent, legacy()...); err != nil {
		t.Fatalf("turn 2 (first repeat) = %v, want warning only", err)
	}
	err := runGuardrailTurn(t, agent, legacy()...)
	if err == nil || !strings.Contains(err.Error(), "runaway loop detected") {
		t.Fatalf("turn 3 = %v, want runaway loop detected", err)
	}
}

// TestCheckProgressLoop_EmptyNoToolTurnsLatch verifies plan rule 3: strikes
// for "(empty response)" apply only when Content+Thinking are empty AND the
// turn carried zero tool calls — three such turns still latch.
func TestCheckProgressLoop_EmptyNoToolTurnsLatch(t *testing.T) {
	agent := NewAgent(Config{SystemPrompt: "test"})

	empty := func() []Message {
		return []Message{{Type: Content, Role: Assistant}}
	}
	if err := runGuardrailTurn(t, agent, empty()...); err != nil {
		t.Fatalf("empty turn 1: %v", err)
	}
	if err := runGuardrailTurn(t, agent, empty()...); err != nil {
		t.Fatalf("empty turn 2 (first repeat) = %v, want warning only", err)
	}
	err := runGuardrailTurn(t, agent, empty()...)
	if err == nil || !strings.Contains(err.Error(), "runaway loop detected") {
		t.Fatalf("empty turn 3 = %v, want runaway loop detected", err)
	}
	agent.mu.Lock()
	defer agent.mu.Unlock()
	if !agent.loopStopped {
		t.Fatal("latch not set after three truly-empty turns")
	}
}

// TestCheckProgressLoop_ProseOnlyRepeatsWarnThenLatch covers plan rule 4 for
// text-only repetition: unchanged behavior — first repeat warns with visible
// hint, second latches.
func TestCheckProgressLoop_ProseOnlyRepeatsWarnThenLatch(t *testing.T) {
	agent := NewAgent(Config{SystemPrompt: "test"})
	prose := func() []Message {
		return []Message{{Type: Content, Role: Assistant, Content: "I will now do the thing."}}
	}
	if err := runGuardrailTurn(t, agent, prose()...); err != nil {
		t.Fatalf("prose turn 1: %v", err)
	}
	if err := runGuardrailTurn(t, agent, prose()...); err != nil {
		t.Fatalf("prose turn 2 (first repeat) = %v, want warning only", err)
	}
	agent.mu.Lock()
	count := agent.assistantRepeatCount
	agent.mu.Unlock()
	if count != 1 {
		t.Fatalf("repeat count after warning = %d, want 1", count)
	}
	if err := runGuardrailTurn(t, agent, prose()...); err == nil || !strings.Contains(err.Error(), "runaway loop detected") {
		t.Fatalf("prose turn 3 = %v, want runaway loop detected", err)
	}
}

// assertNoStrikes fails when the guardrail recorded any strike or hint.
func assertNoStrikes(t *testing.T, agent *Agent) {
	t.Helper()
	agent.mu.Lock()
	defer agent.mu.Unlock()
	if agent.assistantRepeatCount != 0 {
		t.Fatalf("assistantRepeatCount = %d, want 0 (no strikes expected)", agent.assistantRepeatCount)
	}
	if agent.loopStopped {
		t.Fatal("loopStopped latch unexpectedly set")
	}
	for _, m := range agent.history {
		if m.Role == System && strings.Contains(m.Content, "Your last response was identical") {
			t.Fatal("stall hint injected although the turn made tool progress")
		}
	}
}
