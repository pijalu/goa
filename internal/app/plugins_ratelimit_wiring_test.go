// SPDX-License-Identifier: GPL-3.0-or-later

package app

import (
	"testing"
	"time"

	"github.com/pijalu/goa/internal/agentic"
)

// TestEmitRateLimitToPlugins_BusReceivesRateLimitExceeded proves the §6 step 3
// wiring: an agentic EventRateLimit forwarded through the app subscriber lands
// on the plugin event bus as "rate_limit_exceeded" with the full payload.
func TestEmitRateLimitToPlugins_BusReceivesRateLimitExceeded(t *testing.T) {
	s := newPluginTestSubsystems(t)
	loadEnabledPlugins(s)
	if s.pluginRT == nil {
		t.Fatal("pluginRT not set after load")
	}

	type captured struct {
		name    string
		payload interface{}
	}
	got := make(chan captured, 8)
	s.pluginRT.bus.On("*", func(name string, payload interface{}) {
		got <- captured{name: name, payload: payload}
	})

	EmitRateLimitToPlugins(s, &agentic.OutputEvent{
		Type: agentic.EventRateLimit,
		RateLimit: &agentic.RateLimitInfo{
			Provider:     "openai",
			Model:        "gpt-5-codex",
			Attempt:      1,
			RetryAfterMS: 1500,
			Classified:   "rate_limit",
			WillRetry:    true,
		},
	})

	select {
	case c := <-got:
		if c.name != "rate_limit_exceeded" {
			t.Fatalf("expected rate_limit_exceeded on the bus, got %q", c.name)
		}
		m, ok := c.payload.(map[string]interface{})
		if !ok {
			t.Fatalf("payload not a map: %T (%v)", c.payload, c.payload)
		}
		want := map[string]interface{}{
			"provider":       "openai",
			"model":          "gpt-5-codex",
			"attempt":        1,
			"retry_after_ms": int64(1500),
			"classified":     "rate_limit",
			"will_retry":     true,
		}
		for k, v := range want {
			if m[k] != v {
				t.Errorf("payload[%q] = %v (%T), want %v (%T)", k, m[k], m[k], v, v)
			}
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no event reached the plugin bus")
	}
}

// TestEmitRateLimitToPlugins_IgnoresNonRateLimitEvents verifies the forwarder
// is selective: other agent events and malformed rate-limit events must not
// reach the bus, and nil subsystems/events are safe no-ops.
func TestEmitRateLimitToPlugins_IgnoresNonRateLimitEvents(t *testing.T) {
	s := newPluginTestSubsystems(t)
	loadEnabledPlugins(s)
	if s.pluginRT == nil {
		t.Fatal("pluginRT not set after load")
	}

	count := 0
	s.pluginRT.bus.On("*", func(name string, payload interface{}) { count++ })

	EmitRateLimitToPlugins(s, &agentic.OutputEvent{Type: agentic.EventContent})
	EmitRateLimitToPlugins(s, &agentic.OutputEvent{Type: agentic.EventRateLimit})
	EmitRateLimitToPlugins(nil, &agentic.OutputEvent{Type: agentic.EventRateLimit})
	EmitRateLimitToPlugins(s, nil)

	if count != 0 {
		t.Errorf("expected zero bus emissions, got %d", count)
	}
}
