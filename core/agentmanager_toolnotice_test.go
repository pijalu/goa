// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package core

import (
	"strings"
	"testing"
	"time"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/internal/agentic/provider"
)

// noticeProbeTool is a minimal tool for SetTools diff tests.
type noticeProbeTool struct {
	agentic.BaseTool
	name string
}

func (t *noticeProbeTool) Schema() agentic.ToolSchema {
	return agentic.ToolSchema{Name: t.name, Description: "probe", Schema: map[string]interface{}{"type": "object"}}
}

func (t *noticeProbeTool) Execute(input string) (string, error) { return "ok", nil }

// noticeTestProvider satisfies the provider registry the same way
// busyBlockingProvider does, but never streams (SetTools must not need it).
type noticeTestProvider struct{ api provider.Api }

func (p *noticeTestProvider) API() provider.Api { return p.api }

func (p *noticeTestProvider) Stream(model provider.Model, ctx provider.Context, opts provider.StreamOptions) (*provider.AssistantMessageEventStream, error) {
	result := provider.NewAssistantMessageEventStream(8)
	result.End(&provider.AssistantMessage{
		Content:    []provider.ContentBlock{{Type: provider.ContentBlockText, Text: "done"}},
		StopReason: provider.StopReasonEndTurn,
	})
	return result, nil
}

func (p *noticeTestProvider) StreamSimple(model provider.Model, ctx provider.Context, opts provider.SimpleStreamOptions) (*provider.AssistantMessageEventStream, error) {
	return p.Stream(model, ctx, provider.StreamOptions{})
}

// startNoticeSession opens a session seeded with tools alpha and beta.
func startNoticeSession(t *testing.T) *AgentManager {
	t.Helper()
	p := &noticeTestProvider{api: provider.Api("test-toolnotice-" + time.Now().Format("150405.000000000"))}
	provider.RegisterApiProvider(p)
	cfg := &config.Config{}
	am := NewAgentManager(cfg, nil, nil, nil, nil, "")
	mdl := provider.Model{ID: "test-model", Name: "test-model", Api: p.api, Provider: provider.ProviderCustom}
	base := []agentic.Tool{&noticeProbeTool{name: "alpha"}, &noticeProbeTool{name: "beta"}}
	if _, err := am.StartSession(mdl, provider.StreamOptions{}, "sys", base, cfg); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	return am
}

// collectNotices drains user-role messages carrying MetaToolsetNotice.
func collectNotices(agent *agentic.Agent) []string {
	var notices []string
	for _, msg := range agent.GetHistory() {
		if msg.Role == agentic.User && msg.Metadata[agentic.MetaToolsetNotice] == "true" {
			notices = append(notices, msg.Content)
		}
	}
	return notices
}

// Toggling tools emits exactly ONE batched user-role notice listing the
// enabled/disabled names (bugs.md 2026-08-26).
func TestAgentManager_SetTools_EmitsBatchedNotice(t *testing.T) {
	am := startNoticeSession(t)
	next := []agentic.Tool{
		&noticeProbeTool{name: "beta"},  // kept
		&noticeProbeTool{name: "gamma"}, // added
		&noticeProbeTool{name: "delta"}, // added
	}
	if err := am.SetTools(next); err != nil {
		t.Fatalf("SetTools: %v", err)
	}

	notices := collectNotices(am.CurrentAgent())
	if len(notices) != 1 {
		t.Fatalf("got %d notices, want exactly 1: %v", len(notices), notices)
	}
	n := notices[0]
	if !strings.Contains(n, "[goa-tools]") {
		t.Errorf("notice missing [goa-tools] marker: %q", n)
	}
	for _, want := range []string{"Enabled:", "delta, gamma", "Disabled: alpha"} {
		if !strings.Contains(n, want) {
			t.Errorf("notice missing %q:\n%s", want, n)
		}
	}
}

// Disabling a tool is announced too — previously only enable notified.
func TestAgentManager_SetTools_NoticesDisable(t *testing.T) {
	am := startNoticeSession(t)
	if err := am.SetTools([]agentic.Tool{&noticeProbeTool{name: "beta"}}); err != nil {
		t.Fatalf("SetTools: %v", err)
	}
	n := collectNotices(am.CurrentAgent())
	if len(n) != 1 || !strings.Contains(n[0], "Disabled: alpha") {
		t.Fatalf("want one notice disabling alpha, got %v", n)
	}
}

// A SetTools call with an identical set is silent: no notice spam.
func TestAgentManager_SetTools_NoChangeNoNotice(t *testing.T) {
	am := startNoticeSession(t)
	same := []agentic.Tool{&noticeProbeTool{name: "alpha"}, &noticeProbeTool{name: "beta"}}
	if err := am.SetTools(same); err != nil {
		t.Fatalf("SetTools: %v", err)
	}
	if n := collectNotices(am.CurrentAgent()); len(n) != 0 {
		t.Errorf("identical toolset produced notices: %v", n)
	}
}
