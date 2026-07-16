// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package swarm

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/internal/agentic/provider"
	"github.com/pijalu/goa/multiagent"
)

// swarmTestProvider is a deterministic mock provider that returns a fixed
// assistant response. It lets us exercise the full AgentSwarmTool execution path
// without a network call.
type swarmTestProvider struct {
	api   provider.Api
	text  string
	calls atomic.Int32
}

func (p *swarmTestProvider) API() provider.Api { return p.api }

func (p *swarmTestProvider) Stream(model provider.Model, ctx provider.Context, opts provider.StreamOptions) (*provider.AssistantMessageEventStream, error) {
	p.calls.Add(1)
	result := provider.NewAssistantMessageEventStream(64)
	go func() {
		result.Push(provider.AssistantMessageEvent{
			Type:  provider.EventTextStart,
			Delta: "",
		})
		result.Push(provider.AssistantMessageEvent{
			Type:  provider.EventTextDelta,
			Delta: p.text,
		})
		result.End(&provider.AssistantMessage{
			Content:    []provider.ContentBlock{{Type: provider.ContentBlockText, Text: p.text}},
			StopReason: provider.StopReasonEndTurn,
		})
	}()
	return result, nil
}

func (p *swarmTestProvider) StreamSimple(model provider.Model, ctx provider.Context, opts provider.SimpleStreamOptions) (*provider.AssistantMessageEventStream, error) {
	base := provider.BuildSimpleOptions(model, opts)
	return p.Stream(model, ctx, base)
}

func registerSwarmTestProvider(text string) *swarmTestProvider {
	api := provider.Api(fmt.Sprintf("test-swarm-%d", testProviderCounter.Add(1)))
	p := &swarmTestProvider{api: api, text: text}
	provider.RegisterApiProvider(p)
	return p
}

var testProviderCounter atomic.Int64

type recordingEmitter struct {
	mu      sync.Mutex
	entries []recordedEmit
}

type recordedEmit struct {
	from, to, content string
}

func (r *recordingEmitter) Emit(from, to, content string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries = append(r.entries, recordedEmit{from: from, to: to, content: content})
}

func (r *recordingEmitter) Entries() []recordedEmit {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]recordedEmit, len(r.entries))
	copy(out, r.entries)
	return out
}

func TestAgentSwarmTool_EmitsSubAgentActivity(t *testing.T) {
	provider.ClearApiProviders()
	defer provider.ClearApiProviders()

	p := registerSwarmTestProvider("explored item-a")
	model := provider.Model{
		ID:         "test-model",
		Name:       "test-model",
		Api:        p.api,
		Provider:   provider.ProviderCustom,
		InputTypes: []string{"text"},
	}
	pool := multiagent.NewAgentPool(model, provider.StreamOptions{}, nil)

	calledOnCreated := false
	pool.OnAgentCreated = func(role string, agent *agentic.Agent) {
		calledOnCreated = true
	}

	emitter := &recordingEmitter{}
	progress := &recordingProgressReporter{}
	tool := &AgentSwarmTool{
		Pool: pool,
		ModeResolver: &modeResolverStub{
			Body: "You are a test agent. Be concise.",
		},
		Emitter:          emitter,
		ProgressReporter: progress.Report,
	}

	result, err := tool.Execute(`{"task":"Explore items","items":["item-a","item-b"],"prompt_template":"Explore {{item}}"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if calledOnCreated {
		t.Error("swarm sub-agents should not trigger OnAgentCreated (companion observer leak)")
	}
	if !strings.Contains(result, "explored item-a") {
		t.Errorf("result missing sub-agent output: %s", result)
	}
	if p.calls.Load() != 2 {
		t.Errorf("expected 2 provider calls, got %d", p.calls.Load())
	}

	entries := emitter.Entries()
	started := 0
	completed := 0
	for _, e := range entries {
		if strings.Contains(e.content, "sub-agent started") {
			started++
		}
		if strings.Contains(e.content, "sub-agent completed") {
			completed++
		}
	}
	if started != 2 {
		t.Errorf("expected 2 start emits, got %d", started)
	}
	if completed != 2 {
		t.Errorf("expected 2 completed emits, got %d", completed)
	}
	if len(progress.Snapshots()) == 0 {
		t.Error("expected progress reporter snapshots")
	}
	last := progress.Snapshots()[len(progress.Snapshots())-1]
	if !strings.Contains(last, "completed: 2") {
		t.Errorf("expected final progress to show 2 completed, got: %s", last)
	}
}

type recordingProgressReporter struct {
	mu        sync.Mutex
	snapshots []string
}

func (r *recordingProgressReporter) Report(text string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.snapshots = append(r.snapshots, text)
}

func (r *recordingProgressReporter) Snapshots() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.snapshots))
	copy(out, r.snapshots)
	return out
}

type modeResolverStub struct {
	Body         string
	AllowedTools []string
	Temperature  float64
}

func (m *modeResolverStub) Resolve(major string) (multiagent.ModeSpec, error) {
	return multiagent.ModeSpec{
		Name:         major,
		Body:         m.Body,
		AllowedTools: m.AllowedTools,
		Temperature:  m.Temperature,
	}, nil
}
