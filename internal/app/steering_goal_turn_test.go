// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/internal/agentic/provider"
	"github.com/pijalu/goa/internal/agentic/provider/schema"
	"github.com/pijalu/goa/internal/event"
	"github.com/pijalu/goa/tui"
)

var goalSteeringProviderCounter atomic.Int64

func goalSteeringAPI(name string) provider.Api {
	return provider.Api(name + "-" + string(rune('a'+goalSteeringProviderCounter.Add(1))) + "-" + time.Now().Format("150405.000000000"))
}

func goalSteeringModel(api provider.Api) provider.Model {
	return provider.Model{
		ID:         "test-model",
		Name:       "test-model",
		Api:        api,
		Provider:   provider.ProviderCustom,
		InputTypes: []string{"text"},
	}
}

// goalSteeringBlockingProvider holds its stream open until released, so the
// test can simulate an in-flight goal continuation turn: the goal driver runs
// agent.Run directly (never flipping AgentManager.running), leaving the agent
// processing while AgentManager.IsRunning reports false.
type goalSteeringBlockingProvider struct {
	api       provider.Api
	started   chan struct{}
	release   chan struct{}
	startOnce sync.Once
}

func newGoalSteeringBlockingProvider() *goalSteeringBlockingProvider {
	return &goalSteeringBlockingProvider{
		api:     goalSteeringAPI("test-goal-steering-block"),
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (p *goalSteeringBlockingProvider) API() provider.Api { return p.api }

func (p *goalSteeringBlockingProvider) Stream(model provider.Model, ctx provider.Context, opts provider.StreamOptions) (*provider.AssistantMessageEventStream, error) {
	result := provider.NewAssistantMessageEventStream(64)
	p.startOnce.Do(func() { close(p.started) })
	go func() {
		<-p.release
		result.Push(provider.AssistantMessageEvent{Type: provider.EventTextStart, ContentIndex: 0})
		result.Push(provider.AssistantMessageEvent{Type: provider.EventTextDelta, ContentIndex: 0, Delta: "goal turn done"})
		result.Push(provider.AssistantMessageEvent{Type: provider.EventTextEnd, ContentIndex: 0})
		result.End(&provider.AssistantMessage{
			Content:    []provider.ContentBlock{{Type: provider.ContentBlockText, Text: "goal turn done"}},
			StopReason: provider.StopReasonEndTurn,
		})
	}()
	return result, nil
}

func (p *goalSteeringBlockingProvider) StreamSimple(model provider.Model, ctx provider.Context, opts provider.SimpleStreamOptions) (*provider.AssistantMessageEventStream, error) {
	return p.Stream(model, ctx, provider.StreamOptions{})
}

// TestMaybeSteerAgent_DuringGoalTurn_QueuesSteering is the regression test for
// requirement 1 of the goal-steering bug: steering typed while a goal
// continuation turn owns the agent must be queued as steering (so the agent's
// between-round drain weaves it into the executing turn) and must show the
// pending bubble. Before the fix the routing gated on AgentManager.IsRunning,
// which goal turns never set, so the text bypassed the steering queue entirely
// and was dispatched as a phantom normal message (never woven mid-turn, and
// stranded in the agent's internal queue if the in-flight turn errored).
func TestMaybeSteerAgent_DuringGoalTurn_QueuesSteering(t *testing.T) {
	p := newGoalSteeringBlockingProvider()
	provider.RegisterApiProvider(p)

	app, subs := testAppWithAgent(t)
	if _, err := subs.agentMgr.StartSession(goalSteeringModel(p.api), provider.StreamOptions{}, "sys", nil, subs.cfg); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	sc := tui.NewSteeringChrome()
	subs.steeringChrome = sc
	chat := tui.NewChatViewport()
	engine := tui.NewTUI(&testTerminal{w: 80, h: 24})

	// Drive a goal continuation turn the way GoalDriver does: agent.Run
	// directly, without AgentManager.SendUserInput.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	turnDone := make(chan struct{})
	go func() {
		_ = subs.agentMgr.CurrentAgent().Run(ctx, core.ContinuationPrompt)
		close(turnDone)
	}()

	select {
	case <-p.started:
	case <-time.After(2 * time.Second):
		t.Fatal("goal turn did not reach the provider stream")
	}
	if subs.agentMgr.IsRunning() {
		t.Fatal("precondition violated: goal turns run without marking the manager running")
	}

	steered := app.maybeSteerAgent(engine, chat, "steer during goal turn")

	if !steered {
		t.Error("maybeSteerAgent = false during a goal turn; steering was not queued (requirement 1 violated)")
	}
	if got := subs.agentMgr.SteeringQueue().Len(); got != 1 {
		t.Errorf("steering queue length = %d, want 1", got)
	}
	if !sc.HasPending() {
		t.Error("steering bubble should be visible while steering is pending")
	}

	close(p.release)
	select {
	case <-turnDone:
	case <-time.After(2 * time.Second):
		t.Fatal("goal turn did not finish after release")
	}
}

// goalSteeringCaptureProvider answers immediately with a plain-text round and
// records every request, so the test can assert the leftover steering was
// dispatched as a follow-up user turn. onFirstStream fires synchronously
// inside Stream, letting the test append steering during the turn's only
// round (too late for the between-round drain — the leftover case).
type goalSteeringCaptureProvider struct {
	api           provider.Api
	mu            sync.Mutex
	requests      [][]schema.Message
	onFirstStream func()
}

func (p *goalSteeringCaptureProvider) API() provider.Api { return p.api }

func (p *goalSteeringCaptureProvider) Stream(model provider.Model, ctx provider.Context, opts provider.StreamOptions) (*provider.AssistantMessageEventStream, error) {
	p.mu.Lock()
	req := make([]schema.Message, len(ctx.Messages))
	copy(req, ctx.Messages)
	p.requests = append(p.requests, req)
	first := len(p.requests) == 1
	p.mu.Unlock()

	if first && p.onFirstStream != nil {
		p.onFirstStream()
	}

	result := provider.NewAssistantMessageEventStream(64)
	go func() {
		result.Push(provider.AssistantMessageEvent{Type: provider.EventTextStart, ContentIndex: 0})
		result.Push(provider.AssistantMessageEvent{Type: provider.EventTextDelta, ContentIndex: 0, Delta: "done"})
		result.Push(provider.AssistantMessageEvent{Type: provider.EventTextEnd, ContentIndex: 0})
		result.End(&provider.AssistantMessage{
			Content:    []provider.ContentBlock{{Type: provider.ContentBlockText, Text: "done"}},
			StopReason: provider.StopReasonEndTurn,
		})
	}()
	return result, nil
}

func (p *goalSteeringCaptureProvider) StreamSimple(model provider.Model, ctx provider.Context, opts provider.SimpleStreamOptions) (*provider.AssistantMessageEventStream, error) {
	return p.Stream(model, ctx, provider.StreamOptions{})
}

func (p *goalSteeringCaptureProvider) requestCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.requests)
}

func (p *goalSteeringCaptureProvider) lastRequestLastUserText() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.requests) == 0 {
		return ""
	}
	msgs := p.requests[len(p.requests)-1]
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role != schema.RoleUser {
			continue
		}
		var b strings.Builder
		for _, c := range msgs[i].Content {
			if c.Type == schema.ContentBlockText {
				b.WriteString(c.Text)
			}
		}
		return b.String()
	}
	return ""
}

// TestAgentManagerRunner_DispatchesLeftoverSteeringAfterGoalTurn is the
// regression test for requirement 2 of the goal-steering bug: steering still
// pending when a goal continuation turn ends (typed during the final round,
// after the last between-round drain) must be dispatched as a follow-up user
// turn, and the app must be notified via SteeringInjected so the pending
// bubble clears. Before the fix, goal turns never passed through
// runAgentTurn's leftover flush, so the text stayed queued (bubble stuck)
// until some unrelated future turn happened to drain it.
func TestAgentManagerRunner_DispatchesLeftoverSteeringAfterGoalTurn(t *testing.T) {
	p := &goalSteeringCaptureProvider{api: goalSteeringAPI("test-goal-steering-leftover")}
	provider.RegisterApiProvider(p)

	cfg := &config.Config{}
	bus := event.MakeBus(100, 100, 100, 100)
	am := core.NewAgentManager(cfg, nil, nil, nil, bus, "")
	if _, err := am.StartSession(goalSteeringModel(p.api), provider.StreamOptions{}, "sys", nil, cfg); err != nil {
		t.Fatalf("StartSession: %v", err)
	}

	p.onFirstStream = func() {
		// Steering typed during the goal turn's final round: the between-round
		// drain never runs for it, so it is leftover at turn end.
		am.SteeringQueue().Append("leftover steering")
	}

	runner := &agentManagerRunner{agentMgr: am}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := runner.Run(ctx, core.ContinuationPrompt); err != nil {
		t.Fatalf("goal turn Run: %v", err)
	}

	// The follow-up turn runs on its own goroutine; wait for its request.
	deadline := time.Now().Add(2 * time.Second)
	for p.requestCount() < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := p.requestCount(); got < 2 {
		t.Fatalf("requests = %d, want >= 2: leftover steering was not dispatched as a follow-up turn", got)
	}
	if got := p.lastRequestLastUserText(); !strings.Contains(got, "leftover steering") {
		t.Errorf("follow-up turn last user message = %q, want it to contain the leftover steering", got)
	}
	if got := am.SteeringQueue().Len(); got != 0 {
		t.Errorf("steering queue length = %d after dispatch, want 0", got)
	}

	// The app clears the pending bubble on SteeringInjected; it must have been
	// emitted with the dispatched text.
	var injected *event.SteeringInput
	drain := time.After(500 * time.Millisecond)
loop:
	for {
		select {
		case ev := <-bus.Chat:
			if ev.SteeringInjected != nil {
				injected = ev.SteeringInjected
			}
		case <-drain:
			break loop
		}
	}
	if injected == nil {
		t.Error("no SteeringInjected event emitted; the pending steering bubble would not clear (requirement 2 violated)")
	} else if !strings.Contains(injected.Text, "leftover steering") {
		t.Errorf("SteeringInjected text = %q, want it to contain the leftover steering", injected.Text)
	}
}
