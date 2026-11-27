// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/internal/agentic/provider"
)

type idleManagerRequest struct {
	purpose       provider.Purpose
	messages      []provider.Message
	promptCacheID string
}

type idleSessionStopProvider struct {
	api             provider.Api
	compactionCalls atomic.Int32
	compactionSeen  chan struct{}
	mu              sync.Mutex
	requests        []idleManagerRequest
}

func (p *idleSessionStopProvider) API() provider.Api { return p.api }

func (p *idleSessionStopProvider) Stream(_ provider.Model, ctx provider.Context, opts provider.StreamOptions) (*provider.AssistantMessageEventStream, error) {
	p.mu.Lock()
	p.requests = append(p.requests, idleManagerRequest{
		purpose: opts.Purpose, messages: append([]provider.Message(nil), ctx.Messages...), promptCacheID: opts.PromptCacheKey,
	})
	p.mu.Unlock()
	if opts.Purpose == provider.PurposeCompaction {
		p.compactionCalls.Add(1)
		select {
		case p.compactionSeen <- struct{}{}:
		default:
		}
	}
	answer := "answer"
	if opts.Purpose == provider.PurposeCompaction {
		answer = "manager idle checkpoint"
	}
	stream := provider.NewAssistantMessageEventStream(4)
	go func() {
		stream.Push(provider.AssistantMessageEvent{Type: provider.EventTextDelta, Delta: answer})
		stream.End(&provider.AssistantMessage{StopReason: provider.StopReasonEndTurn})
	}()
	return stream, nil
}

func (p *idleSessionStopProvider) StreamSimple(model provider.Model, ctx provider.Context, opts provider.SimpleStreamOptions) (*provider.AssistantMessageEventStream, error) {
	return p.Stream(model, ctx, provider.BuildSimpleOptions(model, opts))
}

func (p *idleSessionStopProvider) snapshot() []idleManagerRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]idleManagerRequest(nil), p.requests...)
}

func idleManagerRequestText(req idleManagerRequest) string {
	var b strings.Builder
	for _, msg := range req.messages {
		for _, block := range msg.Content {
			b.WriteString(block.Text)
			b.WriteByte('\n')
		}
	}
	return b.String()
}

func TestAgentManagerStopSessionCancelsIdleSummarize(t *testing.T) {
	p := &idleSessionStopProvider{
		api:            provider.Api(fmt.Sprintf("idle-session-stop-%d", time.Now().UnixNano())),
		compactionSeen: make(chan struct{}, 4),
	}
	provider.RegisterApiProvider(p)
	model := provider.Model{ID: "idle-stop-model", Api: p.api, Provider: provider.ProviderCustom, ContextWindow: 10_000}
	agent := agentic.NewAgent(agentic.Config{
		Model: model, SystemPrompt: "test", Logger: agentic.NewLogger(agentic.Error),
		ContextCompression: agentic.ContextCompressionConfig{
			IdleSummarize: agentic.IdleSummarizeConfig{TriggerPercent: 0, Delay: 15 * time.Millisecond},
		},
	})
	agent.SetHistory([]agentic.Message{
		{Type: agentic.Content, Role: agentic.User, Content: strings.Repeat("old request ", 100)},
		{Type: agentic.Content, Role: agentic.Assistant, Content: strings.Repeat("old answer ", 100)},
	})
	am := NewAgentManager(&config.Config{}, nil, nil, nil, nil, "")
	am.activeAgent = agent
	if err := agent.Run(context.Background(), "finish exchange"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if err := am.StopSession(); err != nil {
		t.Fatalf("StopSession: %v", err)
	}

	select {
	case <-p.compactionSeen:
		t.Fatal("idle summarize ran after its owning session stopped")
	case <-time.After(75 * time.Millisecond):
	}
	if got := p.compactionCalls.Load(); got != 0 {
		t.Fatalf("compaction calls after StopSession = %d, want zero", got)
	}
}

type idleManagerE2EFixture struct {
	agent           *agentic.Agent
	provider        *idleSessionStopProvider
	originalRequest string
	originalAnswer  string
}

func newIdleManagerE2EFixture(t *testing.T) *idleManagerE2EFixture {
	p := &idleSessionStopProvider{
		api: provider.Api(fmt.Sprintf("idle-manager-e2e-%d", time.Now().UnixNano())), compactionSeen: make(chan struct{}, 4),
	}
	provider.RegisterApiProvider(p)
	trigger, delay := 10, 1
	cfg := &config.Config{ContextCompression: config.ContextCompressionConfig{
		IdleSummarize: config.IdleSummarizeSettings{TriggerPercent: &trigger, DelaySeconds: &delay},
	}}
	am := NewAgentManager(cfg, nil, nil, nil, nil, "")
	t.Cleanup(am.Close)
	t.Cleanup(func() { _ = am.StopSession() })
	model := provider.Model{ID: "idle-manager-e2e-model", Api: p.api, Provider: provider.ProviderCustom, ContextWindow: 4_000}
	if _, err := am.StartSession(model, provider.StreamOptions{}, "test", nil, cfg); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	f := &idleManagerE2EFixture{
		agent: am.CurrentAgent(), provider: p,
		originalRequest: "MANAGER ORIGINAL REQUEST " + strings.Repeat("old-request-payload ", 100),
		originalAnswer:  "MANAGER ORIGINAL ANSWER " + strings.Repeat("old-answer-payload ", 100),
	}
	f.agent.SetHistory([]agentic.Message{
		{Type: agentic.Content, Role: agentic.System, Content: "test"},
		{Type: agentic.Content, Role: agentic.User, Content: f.originalRequest},
		{Type: agentic.Content, Role: agentic.Assistant, Content: f.originalAnswer},
	})
	return f
}

func (f *idleManagerE2EFixture) runFirstExchange(t *testing.T) string {
	if err := f.agent.Run(context.Background(), "manager first exchange"); err != nil {
		t.Fatalf("first Run: %v", err)
	}
	before := f.provider.snapshot()
	if len(before) != 1 || before[0].purpose == provider.PurposeCompaction {
		t.Fatalf("requests before idle delay = %+v, want one conversation request", before)
	}
	return before[0].promptCacheID
}

func (f *idleManagerE2EFixture) waitForCompaction(t *testing.T) {
	select {
	case <-f.provider.compactionSeen:
	case <-time.After(3 * time.Second):
		t.Fatal("configured one-second idle timer did not issue summarize")
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		history := f.agent.GetHistory()
		if len(history) == 2 && strings.Contains(history[0].Content, "manager idle checkpoint") {
			break
		}
		time.Sleep(time.Millisecond)
	}
	history := f.agent.GetHistory()
	if len(history) != 2 || !strings.Contains(history[0].Content, "manager idle checkpoint") {
		t.Fatalf("history after manager idle summarize = %+v, want checkpoint pair", history)
	}
}

func (f *idleManagerE2EFixture) assertNextUsesCheckpoint(t *testing.T, cacheIDBefore string) {
	if err := f.agent.Run(context.Background(), "manager next exchange"); err != nil {
		t.Fatalf("next Run: %v", err)
	}
	requests := f.provider.snapshot()
	if len(requests) != 3 || requests[1].purpose != provider.PurposeCompaction || requests[2].purpose == provider.PurposeCompaction {
		t.Fatalf("manager request sequence = %+v, want conversation → summarize → conversation", requests)
	}
	nextText := idleManagerRequestText(requests[2])
	if !strings.Contains(nextText, "manager idle checkpoint") || !strings.Contains(nextText, "manager next exchange") {
		t.Errorf("next manager request lacks checkpoint or new input:\n%s", nextText)
	}
	if strings.Contains(nextText, f.originalRequest) || strings.Contains(nextText, f.originalAnswer) {
		t.Error("next manager request retained the full pre-compaction transcript")
	}
	if requests[2].promptCacheID == "" || requests[2].promptCacheID == cacheIDBefore {
		t.Errorf("next manager cache ID = %q, want rotation from %q", requests[2].promptCacheID, cacheIDBefore)
	}
}

func TestAgentManagerIdleSummarizeEndToEnd(t *testing.T) {
	f := newIdleManagerE2EFixture(t)
	cacheIDBefore := f.runFirstExchange(t)
	f.waitForCompaction(t)
	f.assertNextUsesCheckpoint(t, cacheIDBefore)
}

func TestAgentManagerIdleSummarizePerModelEndToEnd(t *testing.T) {
	p := &idleSessionStopProvider{
		api: provider.Api(fmt.Sprintf("idle-manager-per-model-%d", time.Now().UnixNano())), compactionSeen: make(chan struct{}, 4),
	}
	provider.RegisterApiProvider(p)
	trigger, globalDelay, disabledDelay, enabledDelay := 0, 1, 0, 1
	cfg := &config.Config{ContextCompression: config.ContextCompressionConfig{
		IdleSummarize: config.IdleSummarizeSettings{TriggerPercent: &trigger, DelaySeconds: &globalDelay},
		PerModel: map[string]config.ModelCompressionOverride{
			"idle-disabled": {IdleSummarize: config.IdleSummarizeSettings{DelaySeconds: &disabledDelay}},
			"idle-enabled":  {IdleSummarize: config.IdleSummarizeSettings{DelaySeconds: &enabledDelay}},
		},
	}}
	am := NewAgentManager(cfg, nil, nil, nil, nil, "")
	t.Cleanup(am.Close)
	t.Cleanup(func() { _ = am.StopSession() })
	disabled := provider.Model{ID: "idle-disabled", Api: p.api, Provider: provider.ProviderCustom, ContextWindow: 4_000}
	if _, err := am.StartSession(disabled, provider.StreamOptions{}, "test", nil, cfg); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	agent := am.CurrentAgent()
	agent.SetHistory([]agentic.Message{
		{Type: agentic.Content, Role: agentic.System, Content: "test"},
		{Type: agentic.Content, Role: agentic.User, Content: strings.Repeat("old request ", 100)},
		{Type: agentic.Content, Role: agentic.Assistant, Content: strings.Repeat("old answer ", 100)},
	})
	if err := agent.Run(context.Background(), "disabled model exchange"); err != nil {
		t.Fatalf("disabled model Run: %v", err)
	}
	select {
	case <-p.compactionSeen:
		t.Fatal("zero-delay model unexpectedly ran idle summarize")
	case <-time.After(1200 * time.Millisecond):
	}

	am.SetModel(provider.Model{ID: "idle-enabled", Api: p.api, Provider: provider.ProviderCustom, ContextWindow: 4_000})
	if err := agent.Run(context.Background(), "enabled model exchange"); err != nil {
		t.Fatalf("enabled model Run: %v", err)
	}
	select {
	case <-p.compactionSeen:
	case <-time.After(3 * time.Second):
		t.Fatal("enabled model did not run its configured idle summarize")
	}
	if got := p.compactionCalls.Load(); got != 1 {
		t.Fatalf("compaction calls = %d, want exactly one from enabled model", got)
	}
}

func TestBuildCompressionConfigMapsIdleSummarize(t *testing.T) {
	trigger, delay := 37, 125
	cfg := &config.Config{ContextCompression: config.ContextCompressionConfig{
		IdleSummarize: config.IdleSummarizeSettings{TriggerPercent: &trigger, DelaySeconds: &delay},
	}}
	am := NewAgentManager(cfg, nil, nil, nil, nil, "")
	got := am.buildCompressionConfig(cfg, "some-model", 32_768)
	if got.IdleSummarize.TriggerPercent != 37 || got.IdleSummarize.Delay != 125*time.Second {
		t.Fatalf("IdleSummarize = %+v, want trigger 37 and delay 125s", got.IdleSummarize)
	}
}

func TestBuildCompressionConfigIdleSummarizePerModel(t *testing.T) {
	trigger, delay, zero, customTrigger, customDelay := 35, 120, 0, 65, 15
	cfg := &config.Config{
		Models: []config.ModelConfig{{ID: "stable-id", Model: "wire/shared-model"}},
		ContextCompression: config.ContextCompressionConfig{
			IdleSummarize: config.IdleSummarizeSettings{TriggerPercent: &trigger, DelaySeconds: &delay},
			PerModel: map[string]config.ModelCompressionOverride{
				"stable-id": {IdleSummarize: config.IdleSummarizeSettings{TriggerPercent: &zero}},
				"disabled":  {IdleSummarize: config.IdleSummarizeSettings{DelaySeconds: &zero}},
				"custom": {IdleSummarize: config.IdleSummarizeSettings{
					TriggerPercent: &customTrigger, DelaySeconds: &customDelay,
				}},
			},
		},
	}
	am := NewAgentManager(cfg, nil, nil, nil, nil, "")
	for _, tc := range []struct {
		model       string
		wantTrigger int
		wantDelay   time.Duration
	}{
		{"wire/shared-model", 0, 120 * time.Second}, // stable config ID, partial inheritance
		{"disabled", 35, 0},                         // explicit model-only opt-out
		{"custom", 65, 15 * time.Second},
		{"unconfigured", 35, 120 * time.Second},
	} {
		t.Run(tc.model, func(t *testing.T) {
			got := am.buildCompressionConfig(cfg, tc.model, 32_768).IdleSummarize
			if got.TriggerPercent != tc.wantTrigger || got.Delay != tc.wantDelay {
				t.Fatalf("IdleSummarize = %+v, want trigger %d and delay %v", got, tc.wantTrigger, tc.wantDelay)
			}
		})
	}
}

func TestAgentManagerIdleSummarizePerModelSwitchAndRefresh(t *testing.T) {
	trigger, firstDelay, secondDelay, refreshedDelay := 20, 30, 60, 45
	cfg := &config.Config{ContextCompression: config.ContextCompressionConfig{
		IdleSummarize: config.IdleSummarizeSettings{TriggerPercent: &trigger, DelaySeconds: &firstDelay},
		PerModel: map[string]config.ModelCompressionOverride{
			"second": {IdleSummarize: config.IdleSummarizeSettings{DelaySeconds: &secondDelay}},
		},
	}}
	am := NewAgentManager(cfg, nil, nil, nil, nil, "")
	first := provider.Model{ID: "first", Api: provider.ApiOpenAICompletions, ContextWindow: 32_768}
	if _, err := am.StartSession(first, provider.StreamOptions{}, "sys", nil, cfg); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	t.Cleanup(func() { _ = am.StopSession() })
	if got := am.CurrentAgent().CompressionConfig().IdleSummarize.Delay; got != 30*time.Second {
		t.Fatalf("first model delay = %v, want 30s", got)
	}

	am.SetModel(provider.Model{ID: "second", Api: provider.ApiOpenAICompletions, ContextWindow: 32_768})
	if got := am.CurrentAgent().CompressionConfig().IdleSummarize.Delay; got != time.Minute {
		t.Fatalf("second model delay = %v, want 1m", got)
	}
	ov := cfg.ContextCompression.PerModel["second"]
	ov.IdleSummarize.DelaySeconds = &refreshedDelay
	cfg.ContextCompression.PerModel["second"] = ov
	am.RefreshContextCompression()
	if got := am.CurrentAgent().CompressionConfig().IdleSummarize.Delay; got != 45*time.Second {
		t.Fatalf("delay after live refresh = %v, want 45s", got)
	}

	am.SetModel(first)
	if got := am.CurrentAgent().CompressionConfig().IdleSummarize.Delay; got != 30*time.Second {
		t.Fatalf("delay after switching back = %v, want global 30s", got)
	}
}

func TestBuildCompressionConfigIdleSummarizeDefaultsAndDisable(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		cfg := &config.Config{}
		am := NewAgentManager(cfg, nil, nil, nil, nil, "")
		got := am.buildCompressionConfig(cfg, "some-model", 32_768).IdleSummarize
		if got.TriggerPercent != 10 || got.Delay != 4*time.Minute {
			t.Fatalf("IdleSummarize = %+v, want defaults 10%% and 4m", got)
		}
	})

	t.Run("explicit zero delay", func(t *testing.T) {
		zero := 0
		cfg := &config.Config{ContextCompression: config.ContextCompressionConfig{
			IdleSummarize: config.IdleSummarizeSettings{DelaySeconds: &zero},
		}}
		am := NewAgentManager(cfg, nil, nil, nil, nil, "")
		if got := am.buildCompressionConfig(cfg, "some-model", 32_768).IdleSummarize.Delay; got != 0 {
			t.Fatalf("Delay = %v, want explicit zero to disable", got)
		}
	})

	t.Run("compression disabled", func(t *testing.T) {
		disabled := false
		cfg := &config.Config{ContextCompression: config.ContextCompressionConfig{Enabled: &disabled}}
		am := NewAgentManager(cfg, nil, nil, nil, nil, "")
		if got := am.buildCompressionConfig(cfg, "some-model", 32_768).IdleSummarize.Delay; got != 0 {
			t.Fatalf("Delay = %v with compression disabled, want zero", got)
		}
	})

	t.Run("per-model compression disabled", func(t *testing.T) {
		disabled, trigger, delay := false, 0, 30
		cfg := &config.Config{ContextCompression: config.ContextCompressionConfig{
			PerModel: map[string]config.ModelCompressionOverride{
				"some-model": {Enabled: &disabled, IdleSummarize: config.IdleSummarizeSettings{
					TriggerPercent: &trigger, DelaySeconds: &delay,
				}},
			},
		}}
		am := NewAgentManager(cfg, nil, nil, nil, nil, "")
		if got := am.buildCompressionConfig(cfg, "some-model", 32_768).IdleSummarize.Delay; got != 0 {
			t.Fatalf("Delay = %v with per-model compression disabled, want zero", got)
		}
	})
}
