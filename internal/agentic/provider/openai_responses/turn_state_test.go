// SPDX-License-Identifier: GPL-3.0-or-later

package openairesponses

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gorilla/websocket"
	"github.com/pijalu/goa/internal/agentic/provider"
)

// --- SSE path tests ---

// TestTurnStateSSECaptureAndReplay verifies that the server-issued
// x-codex-turn-state token is captured at turn start and replayed as a
// request header on subsequent SSE requests within the same turn.
func TestTurnStateSSECaptureAndReplay(t *testing.T) {
	resetTurnStateSessionState()
	defer resetTurnStateSessionState()

	token := "ts-turn-1"
	var capturedHeaders []http.Header
	var mu sync.Mutex

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		capturedHeaders = append(capturedHeaders, r.Header.Clone())
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set(turnStateHeader, token)
		_, _ = io.WriteString(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"ok\"}\n\n")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n")
	}))
	defer srv.Close()

	ctx := provider.Context{
		Messages: []provider.Message{{Role: provider.RoleUser, Content: []provider.ContentBlock{{Type: provider.ContentBlockText, Text: "hi"}}}},
	}
	model := provider.Model{ID: "gpt-5-codex", BaseURL: srv.URL, Provider: provider.ProviderOpenAI}
	opts := provider.StreamOptions{
		PromptCacheKey: "ts-sess-1",
		APIKey:         "test-token",
	}

	// First request: no turn-state to replay (nothing captured yet).
	stream1, err := streamResponses(model, ctx, opts, "codex")
	if err != nil {
		t.Fatalf("first request: %v", err)
	}
	stream1.Result()

	// Second request: must carry the captured turn-state header.
	stream2, err := streamResponses(model, ctx, opts, "codex")
	if err != nil {
		t.Fatalf("second request: %v", err)
	}
	stream2.Result()

	if len(capturedHeaders) != 2 {
		t.Fatalf("expected 2 requests, got %d", len(capturedHeaders))
	}

	// First request must NOT have the turn-state header.
	if got := capturedHeaders[0].Get(turnStateHeader); got != "" {
		t.Errorf("first request must not carry %s, got %q", turnStateHeader, got)
	}

	// Second request MUST have the turn-state header.
	if got := capturedHeaders[1].Get(turnStateHeader); got != token {
		t.Errorf("second request %s = %q, want %q", turnStateHeader, got, token)
	}
}

// TestTurnStateSSEAbsentOnFirstRequest verifies the first request of a turn
// does not carry the turn-state header (nothing captured yet).
func TestTurnStateSSEAbsentOnFirstRequest(t *testing.T) {
	resetTurnStateSessionState()
	defer resetTurnStateSessionState()

	var gotHeader http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Clone()
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set(turnStateHeader, "ts-new")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n")
	}))
	defer srv.Close()

	ctx := provider.Context{
		Messages: []provider.Message{{Role: provider.RoleUser, Content: []provider.ContentBlock{{Type: provider.ContentBlockText, Text: "hi"}}}},
	}
	model := provider.Model{ID: "gpt-5-codex", BaseURL: srv.URL, Provider: provider.ProviderOpenAI}
	opts := provider.StreamOptions{PromptCacheKey: "ts-sess-first", APIKey: "test-token"}

	stream, err := streamResponses(model, ctx, opts, "codex")
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	stream.Result()

	if got := gotHeader.Get(turnStateHeader); got != "" {
		t.Errorf("first request must not carry %s, got %q", turnStateHeader, got)
	}
}

// TestTurnStateSSENotLeakedAcrossSessions verifies concurrent sessions each
// hold their own token and never cross-contaminate.
func TestTurnStateSSENotLeakedAcrossSessions(t *testing.T) {
	resetTurnStateSessionState()
	defer resetTurnStateSessionState()

	tokenA := "ts-session-A"
	tokenB := "ts-session-B"

	var mu sync.Mutex
	capturedBySession := map[string][]string{}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cacheKey := r.Header.Get("X-Session-ID")
		if cacheKey == "" {
			// Derive session from the request body prompt_cache_key for SSE.
			body, _ := io.ReadAll(r.Body)
			var parsed map[string]interface{}
			_ = json.Unmarshal(body, &parsed)
			cacheKey, _ = parsed["prompt_cache_key"].(string)
		}
		token := tokenA
		if strings.Contains(cacheKey, "sess-B") {
			token = tokenB
		}
		mu.Lock()
		capturedBySession[cacheKey] = append(capturedBySession[cacheKey], r.Header.Get(turnStateHeader))
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set(turnStateHeader, token)
		_, _ = io.WriteString(w, "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n")
	}))
	defer srv.Close()

	ctx := provider.Context{
		Messages: []provider.Message{{Role: provider.RoleUser, Content: []provider.ContentBlock{{Type: provider.ContentBlockText, Text: "hi"}}}},
	}
	model := provider.Model{ID: "gpt-5-codex", BaseURL: srv.URL, Provider: provider.ProviderOpenAI}

	// Session A: two requests (first captures, second replays).
	optsA := provider.StreamOptions{PromptCacheKey: "ts-sess-A", APIKey: "test-token"}
	s1, err := streamResponses(model, ctx, optsA, "codex")
	if err != nil {
		t.Fatalf("A first: %v", err)
	}
	s1.Result()
	s2, err := streamResponses(model, ctx, optsA, "codex")
	if err != nil {
		t.Fatalf("A second: %v", err)
	}
	s2.Result()

	// Session B: two requests (first captures, second replays).
	optsB := provider.StreamOptions{PromptCacheKey: "ts-sess-B", APIKey: "test-token"}
	s3, err := streamResponses(model, ctx, optsB, "codex")
	if err != nil {
		t.Fatalf("B first: %v", err)
	}
	s3.Result()
	s4, err := streamResponses(model, ctx, optsB, "codex")
	if err != nil {
		t.Fatalf("B second: %v", err)
	}
	s4.Result()

	// Session A's second request must carry tokenA, not tokenB.
	if got := capturedBySession["ts-sess-A"][1]; got != tokenA {
		t.Errorf("session A second request turn-state = %q, want %q", got, tokenA)
	}
	// Session B's second request must carry tokenB, not tokenA.
	if got := capturedBySession["ts-sess-B"][1]; got != tokenB {
		t.Errorf("session B second request turn-state = %q, want %q", got, tokenB)
	}
	// First requests must not carry any token.
	if got := capturedBySession["ts-sess-A"][0]; got != "" {
		t.Errorf("session A first request must not carry turn-state, got %q", got)
	}
	if got := capturedBySession["ts-sess-B"][0]; got != "" {
		t.Errorf("session B first request must not carry turn-state, got %q", got)
	}
}

// TestTurnStateSSENoReplayWhenAbsent verifies that when the server does not
// send the turn-state header, no replay happens on subsequent requests.
func TestTurnStateSSENoReplayWhenAbsent(t *testing.T) {
	resetTurnStateSessionState()
	defer resetTurnStateSessionState()

	var capturedHeaders []http.Header
	var mu sync.Mutex

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		capturedHeaders = append(capturedHeaders, r.Header.Clone())
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		// No turn-state header in the response.
		_, _ = io.WriteString(w, "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n")
	}))
	defer srv.Close()

	ctx := provider.Context{
		Messages: []provider.Message{{Role: provider.RoleUser, Content: []provider.ContentBlock{{Type: provider.ContentBlockText, Text: "hi"}}}},
	}
	model := provider.Model{ID: "gpt-5-codex", BaseURL: srv.URL, Provider: provider.ProviderOpenAI}
	opts := provider.StreamOptions{PromptCacheKey: "ts-sess-absent", APIKey: "test-token"}

	s1, err := streamResponses(model, ctx, opts, "codex")
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	s1.Result()
	s2, err := streamResponses(model, ctx, opts, "codex")
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	s2.Result()

	if len(capturedHeaders) != 2 {
		t.Fatalf("expected 2 requests, got %d", len(capturedHeaders))
	}
	for i, h := range capturedHeaders {
		if got := h.Get(turnStateHeader); got != "" {
			t.Errorf("request %d must not carry %s (server never sent it), got %q", i, turnStateHeader, got)
		}
	}
}

// TestTurnStateSSENonCodexFlavorUnchanged verifies non-Codex flavors never
// capture or replay the turn-state token.
func TestTurnStateSSENonCodexFlavorUnchanged(t *testing.T) {
	resetTurnStateSessionState()
	defer resetTurnStateSessionState()

	var capturedHeaders []http.Header
	var mu sync.Mutex

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		capturedHeaders = append(capturedHeaders, r.Header.Clone())
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set(turnStateHeader, "ts-should-be-ignored")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n")
	}))
	defer srv.Close()

	ctx := provider.Context{
		Messages: []provider.Message{{Role: provider.RoleUser, Content: []provider.ContentBlock{{Type: provider.ContentBlockText, Text: "hi"}}}},
	}
	model := provider.Model{ID: "gpt-5", BaseURL: srv.URL, Provider: provider.ProviderOpenAI}
	opts := provider.StreamOptions{APIKey: "test-token"}

	// Non-Codex flavor: first request.
	s1, err := streamResponses(model, ctx, opts, "")
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	s1.Result()
	// Non-Codex flavor: second request.
	s2, err := streamResponses(model, ctx, opts, "")
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	s2.Result()

	for i, h := range capturedHeaders {
		if got := h.Get(turnStateHeader); got != "" {
			t.Errorf("non-Codex request %d must not carry %s, got %q", i, turnStateHeader, got)
		}
	}
}

// TestTurnStateSSERedactedFromError verifies the turn-state token never
// appears in error messages or diagnostics.
func TestTurnStateSSERedactedFromError(t *testing.T) {
	resetTurnStateSessionState()
	defer resetTurnStateSessionState()

	token := "ts-secret-token-abc123"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set(turnStateHeader, token)
		_, _ = io.WriteString(w, "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n")
	}))
	defer srv.Close()

	ctx := provider.Context{
		Messages: []provider.Message{{Role: provider.RoleUser, Content: []provider.ContentBlock{{Type: provider.ContentBlockText, Text: "hi"}}}},
	}
	model := provider.Model{ID: "gpt-5-codex", BaseURL: srv.URL, Provider: provider.ProviderOpenAI}
	opts := provider.StreamOptions{PromptCacheKey: "ts-sess-redact", APIKey: "test-token"}

	// Capture the token.
	s1, err := streamResponses(model, ctx, opts, "codex")
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	s1.Result()

	// Force an error by pointing at a dead server.
	srv.Close()
	_, err = streamResponses(model, ctx, opts, "codex")
	if err == nil {
		t.Fatal("expected error from dead server")
	}
	if strings.Contains(err.Error(), token) {
		t.Errorf("error message contains turn-state token (redaction violation): %s", err.Error())
	}
}

// TestTurnStateSSEClearOnNewTurn verifies that a new turn (new token from
// server) replaces the old token, so the old token never leaks.
func TestTurnStateSSEClearOnNewTurn(t *testing.T) {
	resetTurnStateSessionState()
	defer resetTurnStateSessionState()

	oldToken := "ts-old-turn"
	newToken := "ts-new-turn"
	requestCount := 0
	var mu sync.Mutex
	var capturedHeaders []http.Header

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		capturedHeaders = append(capturedHeaders, r.Header.Clone())
		requestCount++
		token := oldToken
		if requestCount > 2 {
			token = newToken
		}
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set(turnStateHeader, token)
		_, _ = io.WriteString(w, "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n")
	}))
	defer srv.Close()

	ctx := provider.Context{
		Messages: []provider.Message{{Role: provider.RoleUser, Content: []provider.ContentBlock{{Type: provider.ContentBlockText, Text: "hi"}}}},
	}
	model := provider.Model{ID: "gpt-5-codex", BaseURL: srv.URL, Provider: provider.ProviderOpenAI}
	opts := provider.StreamOptions{PromptCacheKey: "ts-sess-turns", APIKey: "test-token"}

	// Turn 1: two requests (first captures oldToken, second replays it).
	s1, _ := streamResponses(model, ctx, opts, "codex")
	s1.Result()
	s2, _ := streamResponses(model, ctx, opts, "codex")
	s2.Result()

	// Turn 2: first request captures newToken (replaces old), second replays newToken.
	s3, _ := streamResponses(model, ctx, opts, "codex")
	s3.Result()
	s4, _ := streamResponses(model, ctx, opts, "codex")
	s4.Result()

	if len(capturedHeaders) != 4 {
		t.Fatalf("expected 4 requests, got %d", len(capturedHeaders))
	}

	// Turn 1, request 2: replays oldToken.
	if got := capturedHeaders[1].Get(turnStateHeader); got != oldToken {
		t.Errorf("turn 1 request 2 turn-state = %q, want %q", got, oldToken)
	}
	// Turn 2, request 1: replays oldToken (captured before new token arrives).
	// Note: request 3 is the first request of "turn 2" but the server still
	// issues the token on every response, so request 3 replays the old token
	// (captured from turn 1), and the response to request 3 captures the new
	// token. Request 4 then replays the new token.
	if got := capturedHeaders[2].Get(turnStateHeader); got != oldToken {
		t.Errorf("turn 2 request 1 turn-state = %q, want %q (still old from turn 1)", got, oldToken)
	}
	// Turn 2, request 2: replays newToken (captured from request 3's response).
	if got := capturedHeaders[3].Get(turnStateHeader); got != newToken {
		t.Errorf("turn 2 request 2 turn-state = %q, want %q (new token from turn 2)", got, newToken)
	}
}

// --- WS path tests ---

// TestTurnStateWSClientMetadataReplay verifies that the turn-state token is
// replayed in client_metadata on WS requests within the same turn.
func TestTurnStateWSClientMetadataReplay(t *testing.T) {
	resetTurnStateSessionState()
	defer resetTurnStateSessionState()

	token := "ts-ws-turn-1"
	fake := newFakeCodexWSServer(t)
	fake.turnStateToken = token

	srv := httptest.NewServer(http.HandlerFunc(fake.handler))
	defer srv.Close()

	model := codexWSModel(srv)
	opts := wsTestOpts("ts-ws-sess-1")
	sys := provider.Context{SystemPrompt: "You are a coding agent."}

	// First request: no client_metadata turn-state (nothing captured yet).
	ctx1 := sys
	ctx1.Messages = []provider.Message{userMsg("hello")}
	sendCodexWSTurn(t, model, ctx1, opts)

	// Second request: must carry client_metadata turn-state.
	ctx2 := sys
	ctx2.Messages = []provider.Message{userMsg("hello"), assistantMsg("hello"), userMsg("world")}
	sendCodexWSTurn(t, model, ctx2, opts)

	if fake.numRequests() != 2 {
		t.Fatalf("expected 2 requests, got %d", fake.numRequests())
	}

	// First request: no client_metadata.
	body1 := fake.request(0)
	meta1, _ := body1["client_metadata"].(map[string]interface{})
	if ts, _ := meta1[turnStateHeader].(string); ts != "" {
		t.Errorf("first WS request must not carry client_metadata turn-state, got %q", ts)
	}

	// Second request: must carry client_metadata turn-state.
	body2 := fake.request(1)
	meta2, ok := body2["client_metadata"].(map[string]interface{})
	if !ok {
		t.Fatalf("second WS request missing client_metadata: %v", body2)
	}
	if ts, _ := meta2[turnStateHeader].(string); ts != token {
		t.Errorf("second WS request client_metadata[%s] = %q, want %q", turnStateHeader, ts, token)
	}
}

// TestTurnStateWSNotLeakedAcrossSessions verifies concurrent WS sessions
// never share turn-state tokens.
func TestTurnStateWSNotLeakedAcrossSessions(t *testing.T) {
	resetTurnStateSessionState()
	defer resetTurnStateSessionState()

	tokenA := "ts-ws-A"
	tokenB := "ts-ws-B"

	var mu sync.Mutex
	requestsBySession := map[string][]map[string]interface{}{}

	upgrader := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		defer conn.Close()
		_, msg, err := conn.ReadMessage()
		if err != nil {
			return
		}
		var body map[string]interface{}
		_ = json.Unmarshal(msg, &body)
		cacheKey, _ := body["prompt_cache_key"].(string)

		token := tokenA
		if strings.Contains(cacheKey, "sess-B") {
			token = tokenB
		}

		mu.Lock()
		requestsBySession[cacheKey] = append(requestsBySession[cacheKey], body)
		mu.Unlock()

		// Send response with turn-state in metadata event.
		resp := fmt.Sprintf(
			"data: {\"type\":\"response.metadata\",\"headers\":{%q:%q}}\n\n"+
				"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp-1\",\"status\":\"completed\",\"usage\":{\"input_tokens\":1,\"output_tokens\":1},\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"ok\"}]}]}}\n\n",
			turnStateHeader, token)
		_ = conn.WriteMessage(websocket.TextMessage, []byte(resp))
	}))
	defer srv.Close()

	model := codexWSModel(srv)
	sys := provider.Context{SystemPrompt: "You are a coding agent."}

	// Session A: two requests.
	optsA := wsTestOpts("ts-ws-sess-A")
	ctxA := sys
	ctxA.Messages = []provider.Message{userMsg("a1")}
	sendCodexWSTurn(t, model, ctxA, optsA)
	ctxA.Messages = []provider.Message{userMsg("a1"), assistantMsg("ok"), userMsg("a2")}
	sendCodexWSTurn(t, model, ctxA, optsA)

	// Session B: two requests.
	optsB := wsTestOpts("ts-ws-sess-B")
	ctxB := sys
	ctxB.Messages = []provider.Message{userMsg("b1")}
	sendCodexWSTurn(t, model, ctxB, optsB)
	ctxB.Messages = []provider.Message{userMsg("b1"), assistantMsg("ok"), userMsg("b2")}
	sendCodexWSTurn(t, model, ctxB, optsB)

	// Session A second request must carry tokenA.
	reqsA := requestsBySession["ts-ws-sess-A"]
	if len(reqsA) < 2 {
		t.Fatalf("session A: expected 2 requests, got %d", len(reqsA))
	}
	metaA, _ := reqsA[1]["client_metadata"].(map[string]interface{})
	if ts, _ := metaA[turnStateHeader].(string); ts != tokenA {
		t.Errorf("session A second request client_metadata turn-state = %q, want %q", ts, tokenA)
	}

	// Session B second request must carry tokenB.
	reqsB := requestsBySession["ts-ws-sess-B"]
	if len(reqsB) < 2 {
		t.Fatalf("session B: expected 2 requests, got %d", len(reqsB))
	}
	metaB, _ := reqsB[1]["client_metadata"].(map[string]interface{})
	if ts, _ := metaB[turnStateHeader].(string); ts != tokenB {
		t.Errorf("session B second request client_metadata turn-state = %q, want %q", ts, tokenB)
	}

	// First requests must not carry any token.
	metaA1, _ := reqsA[0]["client_metadata"].(map[string]interface{})
	if ts, _ := metaA1[turnStateHeader].(string); ts != "" {
		t.Errorf("session A first request must not carry turn-state, got %q", ts)
	}
	metaB1, _ := reqsB[0]["client_metadata"].(map[string]interface{})
	if ts, _ := metaB1[turnStateHeader].(string); ts != "" {
		t.Errorf("session B first request must not carry turn-state, got %q", ts)
	}
}

// TestTurnStateWSRedactedFromError verifies the turn-state token never
// appears in WS error messages.
func TestTurnStateWSRedactedFromError(t *testing.T) {
	resetTurnStateSessionState()
	defer resetTurnStateSessionState()

	token := "ts-ws-secret-xyz789"
	fake := newFakeCodexWSServer(t)
	fake.turnStateToken = token

	srv := httptest.NewServer(http.HandlerFunc(fake.handler))

	model := codexWSModel(srv)
	opts := wsTestOpts("ts-ws-sess-redact")
	ctx := provider.Context{
		SystemPrompt: "You are a coding agent.",
		Messages:     []provider.Message{userMsg("hi")},
	}

	// Capture the token.
	sendCodexWSTurn(t, model, ctx, opts)

	// Force an error by closing the server.
	srv.Close()
	_, err := streamResponses(model, ctx, opts, "codex")
	if err == nil {
		t.Fatal("expected error from dead server")
	}
	if strings.Contains(err.Error(), token) {
		t.Errorf("error message contains turn-state token (redaction violation): %s", err.Error())
	}
}

// TestTurnStateStoreIsolation verifies the store is strictly per-session.
func TestTurnStateStoreIsolation(t *testing.T) {
	resetTurnStates()
	defer resetTurnStates()

	captureTurnState("sess-1", "token-1")
	captureTurnState("sess-2", "token-2")

	if got := turnState("sess-1"); got != "token-1" {
		t.Errorf("sess-1 = %q, want token-1", got)
	}
	if got := turnState("sess-2"); got != "token-2" {
		t.Errorf("sess-2 = %q, want token-2", got)
	}
	if got := turnState("sess-3"); got != "" {
		t.Errorf("sess-3 = %q, want empty", got)
	}

	// Replace token for sess-1.
	captureTurnState("sess-1", "token-1-new")
	if got := turnState("sess-1"); got != "token-1-new" {
		t.Errorf("sess-1 after replace = %q, want token-1-new", got)
	}
	// sess-2 unchanged.
	if got := turnState("sess-2"); got != "token-2" {
		t.Errorf("sess-2 after sess-1 replace = %q, want token-2", got)
	}

	// Clear sess-1.
	clearTurnState("sess-1")
	if got := turnState("sess-1"); got != "" {
		t.Errorf("sess-1 after clear = %q, want empty", got)
	}
	// sess-2 still intact.
	if got := turnState("sess-2"); got != "token-2" {
		t.Errorf("sess-2 after sess-1 clear = %q, want token-2", got)
	}
}

// TestTurnStateStoreEmptyKeys verifies empty keys/tokens are no-ops.
func TestTurnStateStoreEmptyKeys(t *testing.T) {
	resetTurnStates()
	defer resetTurnStates()

	captureTurnState("", "token")
	if got := turnState(""); got != "" {
		t.Errorf("empty key should not store, got %q", got)
	}
	captureTurnState("sess", "")
	if got := turnState("sess"); got != "" {
		t.Errorf("empty token should not store, got %q", got)
	}
	clearTurnState("")
}

// TestInjectTurnStateMetadata verifies the client_metadata injection helper.
func TestInjectTurnStateMetadata(t *testing.T) {
	// Body without client_metadata.
	body := []byte(`{"model":"gpt-5-codex","input":[],"stream":true}`)
	out := injectTurnStateMetadata(body, "ts-123")
	var parsed map[string]interface{}
	if err := json.Unmarshal(out, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	meta, ok := parsed["client_metadata"].(map[string]interface{})
	if !ok {
		t.Fatalf("client_metadata missing: %v", parsed)
	}
	if ts, _ := meta[turnStateHeader].(string); ts != "ts-123" {
		t.Errorf("client_metadata[%s] = %q, want ts-123", turnStateHeader, ts)
	}

	// Body with existing client_metadata.
	body2 := []byte(`{"model":"gpt-5-codex","input":[],"stream":true,"client_metadata":{"other":"value"}}`)
	out2 := injectTurnStateMetadata(body2, "ts-456")
	var parsed2 map[string]interface{}
	if err := json.Unmarshal(out2, &parsed2); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	meta2, _ := parsed2["client_metadata"].(map[string]interface{})
	if ts, _ := meta2[turnStateHeader].(string); ts != "ts-456" {
		t.Errorf("client_metadata[%s] = %q, want ts-456", turnStateHeader, ts)
	}
	if other, _ := meta2["other"].(string); other != "value" {
		t.Errorf("existing client_metadata.other = %q, want value", other)
	}

	// Unparseable body returns unchanged.
	bad := []byte(`not json`)
	out3 := injectTurnStateMetadata(bad, "ts-789")
	if string(out3) != string(bad) {
		t.Errorf("unparseable body should return unchanged, got %s", out3)
	}
}

// --- Fake WS server extension for turn-state ---
//
// The fakeCodexWSServer.turnStateToken field (added in ws_delta_send_test.go)
// emits the token in response.metadata events, enabling the WS turn-state
// tests above.
