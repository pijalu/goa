// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pijalu/goa/internal/agentic/provider"
	"github.com/pijalu/goa/internal/agentic/provider/schema"
)

// TestProbeWireFormat_RetryUsesReroutedSurface is the end-to-end proof from
// the goal's completion criterion, driven over real HTTP through the generic
// protocol runtime (the canonical wire APIs always resolve to a registered
// protocol, so only an httptest gateway exercises the true probe path):
//
//   - the configured chat/completions surface answers a generic 500 (the
//     gateway's wire-format-mismatch signature);
//   - handleStreamFailure classifies it SERVER, fires the lazy probe;
//   - the probe finds the anthropic /messages surface streams a valid reply,
//     reroutes the session model, and pins it for later turns;
//   - the budgeted retry then streams successfully from /messages, recovering
//     the turn with no fatal error and only the normal retry budget consumed.
func TestProbeWireFormat_RetryUsesReroutedSurface(t *testing.T) {
	var completionsHits, messagesHits atomic.Int32
	mux := http.NewServeMux()
	// Misconfigured surface: 500 on every request (wire-format mismatch).
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		completionsHits.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `{"type":"error","error":{"type":"error","message":"Internal server error"}}`)
	})
	// Working surface: a well-formed anthropic SSE stream.
	mux.HandleFunc("/v1/messages", func(w http.ResponseWriter, r *http.Request) {
		messagesHits.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"m\",\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\n")
		fmt.Fprint(w, "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n")
		fmt.Fprint(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"Recovered\"}}\n\n")
		fmt.Fprint(w, "event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n")
		fmt.Fprint(w, "event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1}}\n\n")
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	broken := srv.URL + "/v1/chat/completions"
	working := srv.URL + "/v1/messages"

	model := provider.Model{
		ID:         "union-alpha",
		Name:       "union-alpha",
		Api:        schema.ApiOpenAICompletions,
		Provider:   schema.ProviderOpenCodeGo,
		BaseURL:    broken, // unpinned (ApiSource ""), multi-surface gateway
		InputTypes: []string{"text"},
	}

	agent := NewAgent(Config{
		Model:        model,
		SystemPrompt: "test",
		Logger:       NewLogger(Error),
		StreamOptions: provider.StreamOptions{
			APIKey: "test-key",
			// Minimal backoff so the single budgeted retry is fast.
			RetryPolicy: &provider.RetryPolicy{
				Mode:       provider.RetryModeNormal,
				MaxRetries: 3,
				Backoff:    provider.RetryBackoff{InitialDelay: time.Millisecond, MaxDelay: 5 * time.Millisecond},
			},
		},
	})
	obs := &mockEventObserver{}
	agent.AddObserver(obs)
	go func() {
		for range agent.Output {
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	handled, err := agent.handleStreamFailure(ctx, serverErr(), model, agent.cfg.StreamOptions)
	if err != nil {
		t.Fatalf("expected recovery via rerouted retry, got error: %v (completions=%d messages=%d)", err, completionsHits.Load(), messagesHits.Load())
	}
	if !handled {
		t.Fatal("handleStreamFailure should report handled after successful retry")
	}

	// The probe discovered the working surface and the retry streamed from it.
	if messagesHits.Load() < 1 {
		t.Errorf("expected anthropic-messages surface to be probed+retried, hits=%d", messagesHits.Load())
	}
	// The session model is pinned to the discovered surface for later turns.
	agent.mu.Lock()
	cfgAPI, cfgBase := agent.cfg.Model.Api, agent.cfg.Model.BaseURL
	agent.mu.Unlock()
	if cfgAPI != schema.ApiAnthropicMessages || cfgBase != working {
		t.Errorf("session model not pinned: api=%q base=%q, want %q %q", cfgAPI, cfgBase, schema.ApiAnthropicMessages, working)
	}

	// Recovery surfaced a reroute notification, not a fatal error.
	var sawReroute bool
	for _, e := range obs.Events() {
		if e.Type == EventEnd && e.Text != "" {
			t.Errorf("expected turn recovery, but EventEnd carried an error: %q", e.Text)
		}
		if e.Type == EventContent && e.Role == System && e.Metadata != nil && e.Metadata["wire_format_probe"] == "rerouted" {
			sawReroute = true
		}
	}
	if !sawReroute {
		t.Error("expected a wire_format_probe=rerouted system notification")
	}
}
