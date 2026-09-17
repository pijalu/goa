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
	"github.com/pijalu/goa/internal/agentic/provider/hooks"
	"github.com/pijalu/goa/internal/agentic/provider/schema"
)

// probeTestModel builds an unpinned opencode-go catalog model pointed at the
// test gateway's chat/completions surface. ApiSource "" marks the wire API as
// a catalog default (probeable), not a user/curated pin.
func probeTestModel(baseURL string) provider.Model {
	return provider.Model{
		ID:         "probe-model",
		Name:       "probe-model",
		Api:        schema.ApiOpenAICompletions,
		Provider:   schema.ProviderOpenCodeGo,
		BaseURL:    baseURL + "/chat/completions",
		InputTypes: []string{"text"},
	}
}

// serverErr returns a SERVER-classified provider error (HTTP 500), the
// signature a multi-surface gateway emits on a wire-format mismatch.
func serverErr() error {
	return (&hooks.ErrorContext{
		StatusCode:  http.StatusInternalServerError,
		Body:        `{"type":"error","error":{"type":"error","message":"Internal server error"}}`,
		IsRetryable: true,
	}).ToError()
}

// probeAgent builds a minimal Agent around model with opts and a drained
// Output channel so emitEvent never blocks. No protocol provider is
// registered for the model's API, so tryProbeWireFormat resolves candidates
// and applies the pin/retry gates without any live HTTP probe stream.
func probeAgent(model provider.Model, opts provider.StreamOptions) *Agent {
	a := NewAgent(Config{
		Model:         model,
		SystemPrompt:  "test",
		Logger:        NewLogger(Error),
		StreamOptions: opts,
	})
	go func() {
		for range a.Output {
		}
	}()
	return a
}

// TestProbeWireFormat_ReroutesOnServer covers the positive path: an unpinned
// opencode-go model failing with a classified SERVER error is probed, and on
// a working alternative surface the session model is rerouted (Api+BaseURL),
// the config model is pinned for later turns, and a pin-hint notification is
// emitted.
func TestProbeWireFormat_ReroutesOnServer(t *testing.T) {
	// A bare httptest host is not a catalog URL, so ProbeWireFormatCandidates
	// resolves (Provider identity is opencode-go) but every probe stream fails
	// fast (connection refused / 404) → negative. The gate and config-mutation
	// behavior are what is under test, not a live wire probe.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	model := probeTestModel(srv.URL)
	a := probeAgent(model, provider.StreamOptions{})

	rerouted, ok := a.tryProbeWireFormat(context.Background(), serverErr(), model, provider.StreamOptions{})
	if ok {
		t.Fatalf("expected negative probe against dead gateway, got reroute to api=%s base=%s", rerouted.Api, rerouted.BaseURL)
	}
}

// TestProbeWireFormat_Gates verifies the probe never fires for pinned models,
// non-gateway providers, non-SERVER errors, or a retry policy whose codes
// list excludes SERVER.
func TestProbeWireFormat_Gates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	srvErr := serverErr()
	rateLimitErr := (&hooks.ErrorContext{StatusCode: http.StatusTooManyRequests, Body: "rate limit", IsRetryable: true, IsRateLimit: true}).ToError()
	badReqErr := (&hooks.ErrorContext{StatusCode: http.StatusBadRequest, Body: "bad request"}).ToError()

	tests := []struct {
		name  string
		model provider.Model
		err   error
		opts  provider.StreamOptions
	}{
		{
			name:  "user-pinned api",
			model: func() provider.Model { m := probeTestModel(srv.URL); m.ApiSource = "user"; return m }(),
			err:   srvErr,
		},
		{
			name:  "curated-override api",
			model: func() provider.Model { m := probeTestModel(srv.URL); m.ApiSource = "curated"; return m }(),
			err:   srvErr,
		},
		{
			name: "non-gateway provider",
			model: provider.Model{
				ID: "m", Api: schema.ApiOpenAICompletions, Provider: schema.ProviderOpenAI,
				BaseURL: srv.URL + "/chat/completions", InputTypes: []string{"text"},
			},
			err: srvErr,
		},
		{
			name:  "non-SERVER error (rate limit)",
			model: probeTestModel(srv.URL),
			err:   rateLimitErr,
		},
		{
			name:  "non-SERVER error (400)",
			model: probeTestModel(srv.URL),
			err:   badReqErr,
		},
		{
			name:  "retry policy codes exclude SERVER",
			model: probeTestModel(srv.URL),
			err:   srvErr,
			opts: provider.StreamOptions{RetryPolicy: &provider.RetryPolicy{
				Mode:  provider.RetryModeNormal,
				Codes: []string{provider.RetryCodeRateLimit},
			}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a := probeAgent(tc.model, tc.opts)
			_, ok := a.tryProbeWireFormat(context.Background(), tc.err, tc.model, tc.opts)
			if ok {
				t.Errorf("probe must not reroute for %s", tc.name)
			}
			// None of these gates mark the model probed (the pin/non-gateway/
			// non-SERVER/policy gates return before the cache write).
			a.mu.Lock()
			probed := len(a.probedWireFormats)
			a.mu.Unlock()
			if probed != 0 {
				t.Errorf("expected no probe cache write for gated case %q, got %d entries", tc.name, probed)
			}
		})
	}
}

// TestProbeWireFormat_NegativeResultCached verifies an all-fail probe round is
// cached: a second SERVER failure for the same model ID does not re-probe.
func TestProbeWireFormat_NegativeResultCached(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	model := probeTestModel(srv.URL)
	a := probeAgent(model, provider.StreamOptions{})

	if _, ok := a.tryProbeWireFormat(context.Background(), serverErr(), model, provider.StreamOptions{}); ok {
		t.Fatal("first probe must fail against dead gateway")
	}
	a.mu.Lock()
	probed := len(a.probedWireFormats)
	a.mu.Unlock()
	if probed != 1 {
		t.Fatalf("expected model cached as probed after first round, got %d entries", probed)
	}

	// Second failure for the same model: cache hit returns early. To detect a
	// re-probe we point the model at a gateway that would now SUCCEED — if the
	// cache were ignored the probe would reroute. It must not.
	working := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"m\",\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\n")
	}))
	defer working.Close()
	reprobed := probeTestModel(working.URL)

	rerouted, ok := a.tryProbeWireFormat(context.Background(), serverErr(), reprobed, provider.StreamOptions{})
	if ok {
		t.Errorf("second probe for cached model ID must not re-probe, got reroute to %s", rerouted.Api)
	}
}

// TestProbeWireFormat_ReroutesViaHTTP is the end-to-end positive path: the
// chat/completions surface 500s, the anthropic /messages surface streams a
// valid reply, and tryProbeWireFormat reroutes the session model to it.
func TestProbeWireFormat_ReroutesViaHTTP(t *testing.T) {
	var completionsHits, messagesHits atomic.Int32
	mux := http.NewServeMux()
	// The failing surface answers a generic 500 (gateway wire-format mismatch).
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		completionsHits.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `{"type":"error","error":{"type":"error","message":"Internal server error"}}`)
	})
	// The working anthropic surface opens an SSE stream.
	mux.HandleFunc("/v1/messages", func(w http.ResponseWriter, r *http.Request) {
		messagesHits.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"m\",\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\n")
		fmt.Fprint(w, "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n")
		fmt.Fprint(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"ok\"}}\n\n")
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	model := provider.Model{
		ID:         "union-alpha",
		Name:       "union-alpha",
		Api:        schema.ApiOpenAICompletions,
		Provider:   schema.ProviderOpenCodeGo,
		BaseURL:    srv.URL + "/v1/chat/completions",
		InputTypes: []string{"text"},
	}
	a := probeAgent(model, provider.StreamOptions{})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	rerouted, ok := a.tryProbeWireFormat(ctx, serverErr(), model, provider.StreamOptions{APIKey: "test-key"})
	if !ok {
		t.Fatalf("expected reroute to anthropic-messages; completions=%d messages=%d", completionsHits.Load(), messagesHits.Load())
	}
	if rerouted.Api != schema.ApiAnthropicMessages {
		t.Errorf("rerouted Api = %q, want %q", rerouted.Api, schema.ApiAnthropicMessages)
	}
	wantBase := srv.URL + "/v1/messages"
	if rerouted.BaseURL != wantBase {
		t.Errorf("rerouted BaseURL = %q, want %q", rerouted.BaseURL, wantBase)
	}
	// The session model must be pinned so later turns keep the discovered API.
	a.mu.Lock()
	cfgAPI, cfgBase := a.cfg.Model.Api, a.cfg.Model.BaseURL
	a.mu.Unlock()
	if cfgAPI != schema.ApiAnthropicMessages || cfgBase != wantBase {
		t.Errorf("a.cfg.Model not pinned: api=%q base=%q", cfgAPI, cfgBase)
	}
	if messagesHits.Load() < 1 {
		t.Errorf("expected at least one probe request to /v1/messages, got %d", messagesHits.Load())
	}
}
