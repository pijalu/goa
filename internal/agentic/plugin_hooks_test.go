// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pijalu/goa/internal/agentic/provider"
	"github.com/pijalu/goa/internal/hooks"
)

// hookAction configures how a recordingPluginSink answers one point.
type hookAction struct {
	decision HookDecision
	result   map[string]any
	reason   string
}

// hookCall records one sink invocation; mode distinguishes Intercept from
// Notify and the payload is defensively copied.
type hookCall struct {
	mode    string
	point   HookPoint
	payload map[string]any
}

// recordingPluginSink is a PluginHookSink test double: scripted per-point
// decisions plus a call log. Safe for concurrent use because tool execution
// reaches these seams from scheduler goroutines.
type recordingPluginSink struct {
	mu      sync.Mutex
	actions map[HookPoint]hookAction
	calls   []hookCall
}

func newRecordingSink(actions map[HookPoint]hookAction) *recordingPluginSink {
	return &recordingPluginSink{actions: actions}
}

func (s *recordingPluginSink) Intercept(_ context.Context, point HookPoint, payload map[string]any) (HookDecision, map[string]any, string) {
	s.mu.Lock()
	act := s.actions[point]
	s.calls = append(s.calls, hookCall{mode: "intercept", point: point, payload: cloneHookPayload(payload)})
	s.mu.Unlock()
	return act.decision, act.result, act.reason
}

func (s *recordingPluginSink) Notify(point HookPoint, payload map[string]any) {
	s.mu.Lock()
	s.calls = append(s.calls, hookCall{mode: "notify", point: point, payload: cloneHookPayload(payload)})
	s.mu.Unlock()
}

func (s *recordingPluginSink) log() []hookCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]hookCall, len(s.calls))
	copy(out, s.calls)
	return out
}

func (s *recordingPluginSink) interceptCallsFor(point HookPoint) []hookCall {
	var out []hookCall
	for _, c := range s.log() {
		if c.mode == "intercept" && c.point == point {
			out = append(out, c)
		}
	}
	return out
}

func (s *recordingPluginSink) notifyCallsFor(point HookPoint) []hookCall {
	var out []hookCall
	for _, c := range s.log() {
		if c.mode == "notify" && c.point == point {
			out = append(out, c)
		}
	}
	return out
}

func cloneHookPayload(p map[string]any) map[string]any {
	out := make(map[string]any, len(p))
	for k, v := range p {
		out[k] = v
	}
	return out
}

func hookAgent(t *testing.T, p provider.ApiProvider, sink PluginHookSink) *Agent {
	t.Helper()
	return NewAgent(Config{
		Model:          testModel(p.API()),
		SystemPrompt:   "sys",
		Logger:         NewLogger(Error),
		PluginHookSink: sink,
	})
}

// runHookAgent runs one turn with the Output channel drained, as the standard
// loop tests do.
func runHookAgent(t *testing.T, a *Agent, input string) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	go func() {
		for range a.Output {
		}
	}()
	return a.Run(ctx, input)
}

func snapshotHistory(a *Agent) []Message {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]Message, len(a.history))
	copy(out, a.history)
	return out
}

// waitFor polls cond until it holds or the timeout expires.
func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("condition not met before timeout")
}

// eventKey is the golden-comparable projection of an OutputEvent.
type eventKey struct {
	Type EventType
	Role Role
	Text string
}

func projectEvents(events []OutputEvent) []eventKey {
	keys := make([]eventKey, 0, len(events))
	for _, e := range events {
		keys = append(keys, eventKey{Type: e.Type, Role: e.Role, Text: e.Text})
	}
	return keys
}

func assertEventsEqual(t *testing.T, want, got []eventKey) {
	t.Helper()
	if len(want) != len(got) {
		t.Fatalf("event count mismatch:\nwant(%d): %+v\ngot(%d):  %+v", len(want), want, len(got), got)
	}
	for i := range want {
		if want[i] != got[i] {
			t.Fatalf("event[%d] mismatch:\nwant %+v\ngot  %+v", i, want[i], got[i])
		}
	}
}

func assertHistoryEqual(t *testing.T, want, got []Message) {
	t.Helper()
	if len(want) != len(got) {
		t.Fatalf("history length mismatch: want %d got %d\nwant: %+v\ngot:  %+v", len(want), len(got), want, got)
	}
	for i := range want {
		if want[i].Role != got[i].Role || want[i].Content != got[i].Content {
			t.Fatalf("history[%d] mismatch: want {%v %q} got {%v %q}",
				i, want[i].Role, want[i].Content, got[i].Role, got[i].Content)
		}
	}
}

// TestPluginHooks_NilSinkUnchanged is the zero-behavior-change gate: for each
// seam scenario, wiring NO sink must produce identical emitted events and
// final history to wiring an all-Pass sink. Identical outputs prove the seams
// are pure pass-through when unused.
func TestPluginHooks_NilSinkUnchanged(t *testing.T) {
	scenarios := []struct {
		name string
		run  func(t *testing.T, sink PluginHookSink) ([]eventKey, []Message)
	}{
		{
			name: string(HookMessagePreSend),
			run: func(t *testing.T, sink PluginHookSink) ([]eventKey, []Message) {
				a := hookAgent(t, textEventProvider("reply"), sink)
				obs := &mockEventObserver{}
				a.AddObserver(obs)
				if err := runHookAgent(t, a, "hi"); err != nil {
					t.Fatalf("run: %v", err)
				}
				return projectEvents(obs.Events()), snapshotHistory(a)
			},
		},
		{
			name: string(HookReplyDelta),
			run: func(t *testing.T, sink PluginHookSink) ([]eventKey, []Message) {
				p := registerTestProvider("delta-events", []provider.AssistantMessageEvent{
					{Type: provider.EventTextStart, ContentIndex: 0},
					{Type: provider.EventTextDelta, ContentIndex: 0, Delta: "Hel"},
					{Type: provider.EventTextDelta, ContentIndex: 0, Delta: "lo"},
					{Type: provider.EventTextEnd, ContentIndex: 0},
				})
				a := hookAgent(t, p, sink)
				obs := &mockEventObserver{}
				a.AddObserver(obs)
				if err := runHookAgent(t, a, "hi"); err != nil {
					t.Fatalf("run: %v", err)
				}
				return projectEvents(obs.Events()), snapshotHistory(a)
			},
		},
		{
			name: string(HookReplyPre),
			run: func(t *testing.T, sink PluginHookSink) ([]eventKey, []Message) {
				a := hookAgent(t, textEventProvider("final reply"), sink)
				obs := &mockEventObserver{}
				a.AddObserver(obs)
				if err := runHookAgent(t, a, "hi"); err != nil {
					t.Fatalf("run: %v", err)
				}
				return projectEvents(obs.Events()), snapshotHistory(a)
			},
		},
		{
			name: string(HookToolCallPre) + "/post",
			run: func(t *testing.T, sink PluginHookSink) ([]eventKey, []Message) {
				a := NewAgent(Config{
					Tools:          []Tool{hookMockTool{}},
					Logger:         NewLogger(Error),
					PluginHookSink: sink,
				})
				res, err := a.executeToolWithResult(context.Background(), "hookmock", `{"x":1}`, "c1")
				if err != nil || res.Output != "ok" {
					t.Fatalf("tool exec: res=%+v err=%v", res, err)
				}
				return nil, nil
			},
		},
		{
			name: string(HookLLMError),
			run: func(t *testing.T, sink PluginHookSink) ([]eventKey, []Message) {
				p := registerFlakyStartProvider(1, errors.New("connection reset by peer"), []provider.AssistantMessageEvent{
					{Type: provider.EventTextStart, ContentIndex: 0},
					{Type: provider.EventTextDelta, ContentIndex: 0, Delta: "recovered"},
					{Type: provider.EventTextEnd, ContentIndex: 0},
				})
				a := hookAgent(t, p, sink)
				obs := &mockEventObserver{}
				a.AddObserver(obs)
				if err := runHookAgent(t, a, "hi"); err != nil {
					t.Fatalf("run: %v", err)
				}
				return projectEvents(obs.Events()), snapshotHistory(a)
			},
		},
	}

	for _, tc := range scenarios {
		t.Run(tc.name, func(t *testing.T) {
			goldEvents, goldHistory := tc.run(t, nil)
			passEvents, passHistory := tc.run(t, newRecordingSink(nil))
			// Each scenario asserts its own expected outcome inline; here we
			// require nil-sink and Pass-sink runs to be indistinguishable.
			assertEventsEqual(t, goldEvents, passEvents)
			assertHistoryEqual(t, goldHistory, passHistory)
		})
	}
}

// TestPluginHooks_MessagePreSend_Decisions covers the message:pre-send table:
// pass appends normally, modified replaces the pending input, denial appends
// NOTHING (append-only invariant) and surfaces a system rejection event.
func TestPluginHooks_MessagePreSend_Decisions(t *testing.T) {
	tests := []struct {
		name          string
		action        hookAction
		wantUser      string // expected user message content when one must exist
		wantNoUser    bool
		wantRejection string
	}{
		{
			name:     "pass",
			action:   hookAction{decision: HookPass},
			wantUser: "original",
		},
		{
			name:     "modified",
			action:   hookAction{decision: HookModified, result: map[string]any{"text": "rewritten"}},
			wantUser: "rewritten",
		},
		{
			name:          "denied",
			action:        hookAction{decision: HookDenied, reason: "not allowed"},
			wantNoUser:    true,
			wantRejection: "Input blocked by plugin hook: not allowed",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sink := newRecordingSink(map[HookPoint]hookAction{
				HookMessagePreSend: tc.action,
			})
			a := hookAgent(t, textEventProvider("reply"), sink)
			obs := &mockEventObserver{}
			a.AddObserver(obs)

			if err := runHookAgent(t, a, "original"); err != nil {
				t.Fatalf("run: %v", err)
			}

			hist := snapshotHistory(a)
			var userMsgs []Message
			for _, m := range hist {
				if m.Role == User {
					userMsgs = append(userMsgs, m)
				}
			}
			if tc.wantNoUser {
				// Append-only invariant: only the system prompt may exist.
				if len(hist) != 1 || hist[0].Role != System {
					t.Fatalf("denied input must leave no trace beyond the system prompt, got %+v", hist)
				}
				found := false
				for _, ev := range obs.Events() {
					if ev.Type == EventContent && ev.Role == System && strings.Contains(ev.Text, tc.wantRejection) {
						found = true
					}
				}
				if !found {
					t.Fatalf("expected rejection event containing %q, got %+v", tc.wantRejection, obs.Events())
				}
			} else if len(userMsgs) != 1 || userMsgs[0].Content != tc.wantUser {
				t.Fatalf("user messages = %+v, want exactly one with content %q", userMsgs, tc.wantUser)
			}

			calls := sink.interceptCallsFor(HookMessagePreSend)
			if len(calls) != 1 {
				t.Fatalf("expected exactly 1 pre-send intercept, got %d", len(calls))
			}
			if calls[0].payload["text"] != "original" {
				t.Errorf("payload text = %v, want original", calls[0].payload["text"])
			}
			if calls[0].payload["role"] != "user" {
				t.Errorf("payload role = %v, want user", calls[0].payload["role"])
			}
		})
	}
}

// gatedDenySink denies the first intercept (blocking until released), then
// passes everything else. Used to make queue-advance timing deterministic.
type gatedDenySink struct {
	gate  chan struct{}
	mu    sync.Mutex
	deny  bool
	calls int
}

func (s *gatedDenySink) Intercept(_ context.Context, _ HookPoint, _ map[string]any) (HookDecision, map[string]any, string) {
	s.mu.Lock()
	s.calls++
	shouldDeny := s.deny
	if shouldDeny {
		s.deny = false
	}
	s.mu.Unlock()
	if shouldDeny {
		// Wait OUTSIDE the mutex so callCount stays observable while held.
		<-s.gate
		return HookDenied, nil, "first denied"
	}
	return HookPass, nil, ""
}

func (s *gatedDenySink) Notify(HookPoint, map[string]any) {}

func (s *gatedDenySink) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

// TestPluginHooks_MessagePreSend_DeniedQueuedContinues verifies that denying
// one input while another is queued advances to the queued input instead of
// dropping or blocking.
func TestPluginHooks_MessagePreSend_DeniedQueuedContinues(t *testing.T) {
	sink := &gatedDenySink{gate: make(chan struct{}), deny: true}
	a := hookAgent(t, textEventProvider("reply"), sink)
	obs := &mockEventObserver{}
	a.AddObserver(obs)

	done := make(chan error, 1)
	go func() { done <- a.Run(context.Background(), "first") }()
	waitFor(t, time.Second, func() bool { return sink.callCount() == 1 })

	// The agent is processing ("first" is held in the gate): this call queues.
	if err := a.RunWithMetadata(context.Background(), "second", nil); err != nil {
		t.Fatalf("queueing run: %v", err)
	}

	close(sink.gate)
	if err := <-done; err != nil {
		t.Fatalf("run: %v", err)
	}

	hist := snapshotHistory(a)
	var texts []string
	for _, m := range hist {
		if m.Role == User {
			texts = append(texts, m.Content)
		}
	}
	if len(texts) != 1 || texts[0] != "second" {
		t.Fatalf("expected only 'second' to reach history, got %v", texts)
	}
	found := false
	for _, ev := range obs.Events() {
		if ev.Type == EventContent && ev.Role == System && strings.Contains(ev.Text, "first denied") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected rejection event for the denied input, got %+v", obs.Events())
	}
}

// recordingTool records every executed input.
type recordingTool struct {
	BaseTool
	mu         sync.Mutex
	execInputs []string
}

func (t *recordingTool) Schema() ToolSchema {
	return ToolSchema{Name: "recording", Schema: map[string]any{"type": "object"}}
}

func (t *recordingTool) Execute(input string) (string, error) {
	t.mu.Lock()
	t.execInputs = append(t.execInputs, input)
	t.mu.Unlock()
	return "ok", nil
}

func (t *recordingTool) inputs() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]string(nil), t.execInputs...)
}

// TestPluginHooks_ToolCallPre_Decisions covers tool-call:pre: denial vetoes
// before the tool runs (shell-veto style so the model sees the reason), and
// modification rewrites the input the tool receives.
func TestPluginHooks_ToolCallPre_Decisions(t *testing.T) {
	t.Run("denied_before_execution", func(t *testing.T) {
		tool := &recordingTool{}
		sink := newRecordingSink(map[HookPoint]hookAction{
			HookToolCallPre: {decision: HookDenied, reason: "aws forbidden"},
		})
		a := NewAgent(Config{Tools: []Tool{tool}, Logger: NewLogger(Error), PluginHookSink: sink})

		res, err := a.executeToolWithResult(context.Background(), "recording", `{}`, "c1")
		if err == nil {
			t.Fatal("expected veto error")
		}
		if !strings.Contains(err.Error(), `tool "recording" blocked by plugin hook: aws forbidden`) {
			t.Fatalf("veto error mismatched: %v", err)
		}
		if !strings.Contains(res.Output, "aws forbidden") {
			t.Fatalf("veto output must carry the reason for the model, got %q", res.Output)
		}
		if len(tool.inputs()) != 0 {
			t.Fatal("tool must NOT execute after a plugin denial")
		}
	})

	t.Run("modified_before_shell_hooks", func(t *testing.T) {
		tool := &recordingTool{}
		// Behavioral ordering proof: the shell veto rejects any input still
		// carrying the secret. The plugin seam rewrites BEFORE the shell hook
		// runs, so the shell sees only redacted JSON and lets the call pass —
		// exactly the documented reason plugin-pre sits upstream of shell.
		vetoIfSecret := hooks.NewEngine(
			&hooks.Config{Hooks: []hooks.Hook{{
				Event:          hooks.EventBeforeTool,
				Command:        "sh",
				Args:           []string{"-c", "grep -q AKIA && exit 1 || exit 0"},
				TimeoutSeconds: 5,
			}}},
			nil,
		)
		sink := newRecordingSink(map[HookPoint]hookAction{
			HookToolCallPre: {decision: HookModified, result: map[string]any{"input": `{"redacted":true}`}},
		})
		a := NewAgent(Config{
			Tools:          []Tool{tool},
			HookEngine:     vetoIfSecret,
			Logger:         NewLogger(Error),
			PluginHookSink: sink,
		})

		if _, err := a.executeToolWithResult(context.Background(), "recording", `{"secret":"AKIA1234"}`, "c1"); err != nil {
			t.Fatalf("exec after redaction must pass the shell veto: %v", err)
		}
		got := tool.inputs()
		if len(got) != 1 || got[0] != `{"redacted":true}` {
			t.Fatalf("tool saw %v, want mutated input", got)
		}
		pre := sink.interceptCallsFor(HookToolCallPre)
		if len(pre) != 1 || pre[0].payload["input"] != `{"secret":"AKIA1234"}` {
			t.Fatalf("pre payload mismatch: %+v", pre)
		}
		if len(vetoIfSecret.Store().Entries()) == 0 {
			t.Fatal("shell hook engine should have audited the call")
		}
	})
}

// TestPluginHooks_ToolCallPost_ModifiedBeforeAppend pins the ordering anchor:
// a post-hook rewrite is visible in BOTH the returned ToolResult and the
// tool-role message appended to history by the normal turn flow — proving the
// seam runs upstream of the history append (agent_tools.go resolve path).
func TestPluginHooks_ToolCallPost_ModifiedBeforeAppend(t *testing.T) {
	sink := newRecordingSink(map[HookPoint]hookAction{
		HookToolCallPost: {decision: HookModified, result: map[string]any{"output": "[REDACTED RESULT]"}},
	})
	p := &scriptedStreamProvider{
		api: provider.Api(fmt.Sprintf("test-post-hook-%d", testProviderCounter.Add(1))),
		steps: []scriptedStreamStep{
			{events: []provider.AssistantMessageEvent{
				{Type: provider.EventToolCallEnd, ContentIndex: 0, ToolCall: &provider.ContentBlock{
					Type: provider.ContentBlockToolCall, ToolCallID: "call_1", ToolName: "hookmock", ToolArguments: `{}`,
				}},
			}},
			{events: []provider.AssistantMessageEvent{
				{Type: provider.EventTextStart, ContentIndex: 0},
				{Type: provider.EventTextDelta, ContentIndex: 0, Delta: "done"},
				{Type: provider.EventTextEnd, ContentIndex: 0},
			}},
		},
	}
	provider.RegisterApiProvider(p)

	a := NewAgent(Config{
		Model:          testModel(p.API()),
		SystemPrompt:   "sys",
		Tools:          []Tool{hookMockTool{}},
		Logger:         NewLogger(Error),
		PluginHookSink: sink,
	})
	if err := runHookAgent(t, a, "use the tool"); err != nil {
		t.Fatalf("run: %v", err)
	}

	calls := sink.interceptCallsFor(HookToolCallPost)
	if len(calls) != 1 {
		t.Fatalf("expected 1 post intercept, got %d", len(calls))
	}
	if calls[0].payload["output"] != "ok" {
		t.Errorf("post payload output = %v, want raw tool output \"ok\"", calls[0].payload["output"])
	}
	if calls[0].payload["tool"] != "hookmock" || calls[0].payload["call_id"] != "call_1" {
		t.Errorf("post payload identity fields mismatch: %+v", calls[0].payload)
	}

	hist := snapshotHistory(a)
	var toolMsgs []Message
	for _, m := range hist {
		if m.Role == ToolRole {
			toolMsgs = append(toolMsgs, m)
		}
	}
	if len(toolMsgs) != 1 || toolMsgs[0].Content != "[REDACTED RESULT]" {
		t.Fatalf("history must carry the rewritten result (append happens after the hook), got %+v", toolMsgs)
	}
}

// TestPluginHooks_ToolCallPost_ErrorRewrite rebuilds the ToolResult from the
// returned map, including synthesizing an error from the "error" field.
func TestPluginHooks_ToolCallPost_ErrorRewrite(t *testing.T) {
	sink := newRecordingSink(map[HookPoint]hookAction{
		HookToolCallPost: {decision: HookModified, result: map[string]any{"output": "", "error": "synthetic failure"}},
	})
	a := NewAgent(Config{
		Tools:          []Tool{hookMockTool{}},
		Logger:         NewLogger(Error),
		PluginHookSink: sink,
	})
	res, err := a.executeToolWithResult(context.Background(), "hookmock", `{}`, "c1")
	if err == nil || err.Error() != "synthetic failure" {
		t.Fatalf("err = %v, want synthetic failure", err)
	}
	if res.Output != "" {
		t.Fatalf("output = %q, want empty", res.Output)
	}
}

// TestPluginHooks_ReplyDelta_ContentRewrite covers content-delta rewriting on
// the hot streaming path.
func TestPluginHooks_ReplyDelta_ContentRewrite(t *testing.T) {
	sink := newRecordingSink(map[HookPoint]hookAction{
		HookReplyDelta: {decision: HookModified, result: map[string]any{"delta": "[X]"}},
	})
	p := registerTestProvider("delta-mod", []provider.AssistantMessageEvent{
		{Type: provider.EventTextStart, ContentIndex: 0},
		{Type: provider.EventTextDelta, ContentIndex: 0, Delta: "secret"},
		{Type: provider.EventTextEnd, ContentIndex: 0},
	})
	a := hookAgent(t, p, sink)
	if err := runHookAgent(t, a, "hi"); err != nil {
		t.Fatalf("run: %v", err)
	}

	calls := sink.interceptCallsFor(HookReplyDelta)
	if len(calls) == 0 {
		t.Fatal("expected reply:delta intercept calls")
	}
	for _, c := range calls {
		if c.payload["state"] != "content" || c.payload["is_delta"] != true {
			t.Errorf("payload state/is_delta mismatch: %+v", c.payload)
		}
	}
	hist := snapshotHistory(a)
	last := hist[len(hist)-1]
	if last.Role != Assistant || last.Content != "[X]" {
		t.Fatalf("final assistant content = %q, want rewritten delta", last.Content)
	}
}

// TestPluginHooks_ReplyDelta_ThinkingNotifyOnly pins the documented
// restriction: thinking deltas reach the sink via Notify only — no Intercept
// call may ever fire for them, because rewriting reasoning risks breaking
// providers' reasoning-signature verification.
func TestPluginHooks_ReplyDelta_ThinkingNotifyOnly(t *testing.T) {
	sink := newRecordingSink(map[HookPoint]hookAction{
		HookReplyDelta: {decision: HookModified, result: map[string]any{"delta": "[TAMPERED]"}},
	})
	p := registerTestProviderEveryRound("thinking-events", []provider.AssistantMessageEvent{
		{Type: provider.EventThinkingDelta, Delta: "reasoning step"},
		{Type: provider.EventThinkingDelta, Delta: " more reasoning"},
	})
	a := hookAgent(t, p, sink)
	if err := runHookAgent(t, a, "hi"); err != nil {
		t.Fatalf("run: %v", err)
	}

	if calls := sink.interceptCallsFor(HookReplyDelta); len(calls) != 0 {
		t.Fatalf("thinking deltas must be notify-only, got %d intercept calls", len(calls))
	}
	notifies := sink.notifyCallsFor(HookReplyDelta)
	if len(notifies) == 0 {
		t.Fatal("expected reply:delta notify calls for thinking deltas")
	}
	for _, n := range notifies {
		if n.payload["state"] != "thinking" {
			t.Errorf("notify state = %v, want thinking", n.payload["state"])
		}
	}
}

// TestPluginHooks_ReplyPre_ModifiedAndDenied covers reply:pre: modified
// rewrites the finalized assistant message before its single history append;
// denial is NOT supported and degrades to pass-through.
func TestPluginHooks_ReplyPre_ModifiedAndDenied(t *testing.T) {
	t.Run("modified_before_append", func(t *testing.T) {
		sink := newRecordingSink(map[HookPoint]hookAction{
			HookReplyPre: {decision: HookModified, result: map[string]any{"text": "[REWRITTEN REPLY]"}},
		})
		a := hookAgent(t, textEventProvider("raw reply"), sink)
		if err := runHookAgent(t, a, "hi"); err != nil {
			t.Fatalf("run: %v", err)
		}

		calls := sink.interceptCallsFor(HookReplyPre)
		if len(calls) != 1 {
			t.Fatalf("expected exactly 1 reply:pre intercept, got %d", len(calls))
		}
		if calls[0].payload["text"] != "raw reply" {
			t.Errorf("reply:pre payload text = %v, want raw reply", calls[0].payload["text"])
		}
		hist := snapshotHistory(a)
		last := hist[len(hist)-1]
		if last.Role != Assistant || last.Content != "[REWRITTEN REPLY]" {
			t.Fatalf("history assistant message = {%v %q}, want rewritten text", last.Role, last.Content)
		}
	})

	t.Run("denied_is_pass_through", func(t *testing.T) {
		sink := newRecordingSink(map[HookPoint]hookAction{
			HookReplyPre: {decision: HookDenied, reason: "cannot happen"},
		})
		a := hookAgent(t, textEventProvider("raw reply"), sink)
		if err := runHookAgent(t, a, "hi"); err != nil {
			t.Fatalf("denial must degrade to pass-through, got error: %v", err)
		}
		hist := snapshotHistory(a)
		last := hist[len(hist)-1]
		if last.Role != Assistant || last.Content != "raw reply" {
			t.Fatalf("denied reply:pre must keep the original text, got {%v %q}", last.Role, last.Content)
		}
	})
}

// TestPluginHooks_LLMError_NotifyPayloads pins llm:error semantics: exactly
// one notification per failure episode, notify-mode only, carrying error /
// model / classified / will_retry (+ next_delay_ms when retrying).
func TestPluginHooks_LLMError_NotifyPayloads(t *testing.T) {
	t.Run("retryable_transport", func(t *testing.T) {
		p := registerFlakyStartProvider(1, errors.New("connection reset by peer"), []provider.AssistantMessageEvent{
			{Type: provider.EventTextStart, ContentIndex: 0},
			{Type: provider.EventTextDelta, ContentIndex: 0, Delta: "recovered"},
			{Type: provider.EventTextEnd, ContentIndex: 0},
		})
		sink := newRecordingSink(nil)
		a := hookAgent(t, p, sink)
		if err := runHookAgent(t, a, "hi"); err != nil {
			t.Fatalf("run: %v", err)
		}

		calls := sink.notifyCallsFor(HookLLMError)
		if len(calls) != 1 {
			t.Fatalf("expected exactly 1 llm:error notify per episode, got %d", len(calls))
		}
		if intercepts := sink.interceptCallsFor(HookLLMError); len(intercepts) != 0 {
			t.Fatal("llm:error is notify-only in v1; no intercept call expected")
		}
		payload := calls[0].payload
		if payload["error"] != "connection reset by peer" {
			t.Errorf("error = %v", payload["error"])
		}
		if payload["model"] != "test-model" {
			t.Errorf("model = %v, want test-model", payload["model"])
		}
		if payload["will_retry"] != true {
			t.Errorf("will_retry = %v, want true", payload["will_retry"])
		}
		if payload["classified"] != "transport" {
			t.Errorf("classified = %v, want transport", payload["classified"])
		}
		delay, ok := payload["next_delay_ms"].(int64)
		if !ok || delay < 0 {
			t.Errorf("next_delay_ms missing or invalid: %v", payload["next_delay_ms"])
		}
		if _, ok := payload["attempt"]; !ok {
			t.Error("attempt field missing from llm:error payload")
		}
	})

	t.Run("non_retryable", func(t *testing.T) {
		p := registerFlakyStartProvider(5, errors.New("400 invalid request: bad parameter"), nil)
		sink := newRecordingSink(nil)
		a := hookAgent(t, p, sink)
		if err := runHookAgent(t, a, "hi"); err == nil {
			t.Fatal("expected non-retryable failure to surface")
		}

		calls := sink.notifyCallsFor(HookLLMError)
		if len(calls) != 1 {
			t.Fatalf("expected exactly 1 llm:error notify, got %d", len(calls))
		}
		payload := calls[0].payload
		if payload["will_retry"] != false {
			t.Errorf("will_retry = %v, want false", payload["will_retry"])
		}
		if _, ok := payload["next_delay_ms"]; ok {
			t.Error("next_delay_ms must be absent when not retrying")
		}
	})
}

// TestClassifyLLMError locks the classification vocabulary surfaced to
// plugins (lowercased retry codes, unknown fallback).
func TestClassifyLLMError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"nil", nil, "unknown"},
		{"transport", errors.New("connection reset by peer"), "transport"},
		{"timeout", errors.New("request timed out"), "timeout"},
		{"server", errors.New("service unavailable"), "server"},
		{"empty response", errEmptyResponse, "empty_response"},
		{"unknown", errors.New("400 invalid request: bad parameter"), "unknown"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyLLMError(tc.err); got != tc.want {
				t.Fatalf("classifyLLMError(%v) = %q, want %q", tc.err, got, tc.want)
			}
		})
	}
}

// TestPluginHooks_ConcurrentToolCalls exercises the seam from parallel
// scheduler goroutines under -race (sink implementations must be
// concurrency-safe; the agent side must not corrupt shared state).
func TestPluginHooks_ConcurrentToolCalls(t *testing.T) {
	sink := newRecordingSink(map[HookPoint]hookAction{
		HookToolCallPost: {decision: HookModified, result: map[string]any{"output": "audited"}},
	})
	a := NewAgent(Config{
		Tools:          []Tool{hookMockTool{}},
		Logger:         NewLogger(Error),
		PluginHookSink: sink,
	})
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := a.executeToolWithResult(context.Background(), "hookmock", `{}`, fmt.Sprintf("call_%d", i))
			if err != nil {
				t.Errorf("exec %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()
	if got := len(sink.interceptCallsFor(HookToolCallPost)); got != 16 {
		t.Fatalf("expected 16 post intercepts, got %d", got)
	}
}
