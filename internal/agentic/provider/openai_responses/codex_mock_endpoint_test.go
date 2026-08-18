package openairesponses

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/agentic/provider"
)

// TestCodexMockEndpointRequestSnapshot exercises the same SSE/body contract as
// the exported real exchange: instructions, store=false, cache affinity, and a
// streaming text delta. The handler copies request bytes before the caller can
// mutate its source context, proving the wire snapshot is stable.
func TestCodexMockEndpointRequestSnapshot(t *testing.T) {
	var gotBody []byte
	var gotHeaders http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		gotHeaders = r.Header.Clone()
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"ok\"}\n\n")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n")
	}))
	defer srv.Close()

	ctx := provider.Context{
		SystemPrompt: "You are a coding agent.",
		Messages:     []provider.Message{{Role: provider.RoleUser, Content: []provider.ContentBlock{{Type: provider.ContentBlockText, Text: "inspect"}}}},
	}
	model := provider.Model{ID: "gpt-5-codex", BaseURL: srv.URL, Provider: provider.ProviderOpenAI}
	stream, err := streamResponses(model, ctx, provider.StreamOptions{
		PromptCacheKey: "goa_mock_cache_key",
		APIKey:         "test-token",
		CodexAccountID: "acct-test",
	}, "codex")
	if err != nil {
		t.Fatalf("streamResponses: %v", err)
	}
	// Mutating the caller's context after request construction must not affect
	// the already captured request or the eventual response.
	ctx.Messages[0].Content[0].Text = "mutated-after-send"
	result := stream.Result()
	if result == nil || len(result.Content) == 0 || result.Content[0].Text != "ok" {
		t.Fatalf("unexpected SSE result: %#v", result)
	}

	body := string(gotBody)
	for _, want := range []string{`"instructions":"You are a coding agent."`, `"store":false`, `"prompt_cache_key":"goa_mock_cache_key"`} {
		if !strings.Contains(body, want) {
			t.Errorf("request missing %s: %s", want, body)
		}
	}
	if strings.Contains(body, "previous_response_id") {
		t.Error("Codex SSE request contains forbidden previous_response_id")
	}
	if gotHeaders.Get("originator") != "goa" || gotHeaders.Get("accept") != "text/event-stream" {
		t.Errorf("unexpected Codex headers: originator=%q accept=%q", gotHeaders.Get("originator"), gotHeaders.Get("accept"))
	}
	if strings.Contains(body, "mutated-after-send") {
		t.Error("wire request changed after caller mutated context")
	}
}
