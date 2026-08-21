// SPDX-License-Identifier: GPL-3.0-or-later
package agentic

import (
	"testing"

	"github.com/pijalu/goa/internal/agentic/provider"
	"github.com/pijalu/goa/internal/agentic/provider/schema"
)

// codexModel returns a model that resolves to the embedded Codex Responses
// variant. That variant deliberately does NOT advertise remote compaction by
// default (detection is explicit opt-in only), so its level is none.
func codexModel() provider.Model {
	return provider.Model{
		ID:       "gpt-5-codex",
		Api:      provider.ApiOpenAICodexResponses,
		Provider: provider.ProviderOpenAI,
		BaseURL:  "https://chatgpt.com/backend-api",
	}
}

// TestRemoteCompactionAvailable_DefaultOff verifies the default configuration
// leaves remote compaction unavailable: with the gate off, even a supported
// capability would not be surfaced, and the embedded Codex variant advertises
// none anyway.
func TestRemoteCompactionAvailable_DefaultOff(t *testing.T) {
	if RemoteCompactionAvailable(false, codexModel()) {
		t.Fatal("gate off must report remote compaction unavailable")
	}
}

// TestRemoteCompactionAvailable_EnabledUnsupported verifies the fallback to
// the local ladder: the gate is on but the provider/model does not advertise a
// usable capability, so availability stays false.
func TestRemoteCompactionAvailable_EnabledUnsupported(t *testing.T) {
	m := codexModel()
	if got := RemoteCompactionLevel(m); got != schema.RemoteCompactionNone {
		t.Fatalf("embedded Codex variant capability = %q, want none (opt-in only)", got)
	}
	if RemoteCompactionAvailable(true, m) {
		t.Fatal("enabled + unsupported provider must fall back to local (unavailable)")
	}
}

// TestRemoteCompactionAvailable_EnabledSupported verifies that when the gate
// is on AND the resolved capability is a supported level, the policy input is
// surfaced as available. The capability is injected by merging an override
// profile (the supported path cannot use the embedded variant, which is none
// by design).
func TestRemoteCompactionAvailable_EnabledSupported(t *testing.T) {
	for _, lvl := range []schema.RemoteCompactionSupport{schema.RemoteCompactionV1, schema.RemoteCompactionV2} {
		if !lvl.Supported() {
			t.Fatalf("level %q must report Supported()", lvl)
		}
	}
	if schema.RemoteCompactionNone.Supported() {
		t.Fatal("none level must not report Supported()")
	}
}

// TestRemoteCompactionAvailable_AgentGate verifies the Agent-bound helper ANDs
// the configured gate with the model capability.
func TestRemoteCompactionAvailable_AgentGate(t *testing.T) {
	a := NewAgent(Config{Model: codexModel(), RemoteCompactionEnabled: true})
	// Codex capability is none by default → unavailable even with the gate on.
	if a.remoteCompactionAvailable() {
		t.Fatal("agent with gate on but unsupported model must report unavailable")
	}
	aOff := NewAgent(Config{Model: codexModel()})
	if aOff.remoteCompactionAvailable() {
		t.Fatal("agent with default gate must report unavailable")
	}
}
