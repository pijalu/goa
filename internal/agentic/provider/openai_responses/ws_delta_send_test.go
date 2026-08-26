// SPDX-License-Identifier: GPL-3.0-or-later

package openairesponses

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/gorilla/websocket"
	"github.com/pijalu/goa/internal/agentic/provider"
	"github.com/pijalu/goa/internal/agentic/provider/transport"
)

// wsCapturedRequest is one WS text frame the fake server received, decoded.
type wsCapturedRequest struct {
	Body map[string]interface{}
}

// fakeCodexWSServer upgrades every request to a WebSocket and answers each
// request frame with a minimal successful stream (text delta + completed).
// The response id and the echoed output derive from the request: a full send
// (no previous_response_id) gets a fresh id and echoes its input as the
// assistant answer; a delta send echoes the delta under the chained id.
type fakeCodexWSServer struct {
	t        *testing.T
	upgrader websocket.Upgrader

	// turnStateToken, when non-empty, is emitted as the x-codex-turn-state
	// value in the response.metadata event headers (mirroring the Codex WS
	// turn-state capture path).
	turnStateToken string

	mu       sync.Mutex
	requests []wsCapturedRequest
	respSeq  int
}

func newFakeCodexWSServer(t *testing.T) *fakeCodexWSServer {
	t.Helper()
	return &fakeCodexWSServer{t: t, upgrader: websocket.Upgrader{}}
}

func (f *fakeCodexWSServer) handler(w http.ResponseWriter, r *http.Request) {
	conn, err := f.upgrader.Upgrade(w, r, nil)
	if err != nil {
		f.t.Errorf("upgrade: %v", err)
		return
	}
	defer conn.Close()
	_, msg, err := conn.ReadMessage()
	if err != nil {
		return
	}
	f.record(msg)
	// One-shot semantics (matching the client transport): answer the request,
	// then close the connection so the client's reader sees the stream end.
	_ = conn.WriteMessage(websocket.TextMessage, []byte(f.responseFor(msg)))
}

func (f *fakeCodexWSServer) record(msg []byte) {
	var body map[string]interface{}
	if err := json.Unmarshal(msg, &body); err != nil {
		f.t.Errorf("request body is not JSON: %v", err)
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, wsCapturedRequest{Body: body})
}

// responseFor builds the NDJSON event stream for one request. The completed
// payload echoes an assistant message containing the last user text so the
// baseline's AddedItems extend the conversation exactly like the real backend.
func (f *fakeCodexWSServer) responseFor(msg []byte) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.respSeq++
	var body map[string]interface{}
	_ = json.Unmarshal(msg, &body)
	input, _ := body["input"].([]interface{})
	lastText := ""
	if len(input) > 0 {
		if item, ok := input[len(input)-1].(map[string]interface{}); ok {
			lastText, _ = item["content"].(string)
		}
	}
	var b strings.Builder
	// Emit response.metadata with the turn-state header when configured.
	if f.turnStateToken != "" {
		fmt.Fprintf(&b, "data: {\"type\":\"response.metadata\",\"headers\":{%q:%q}}\n\n", turnStateHeader, f.turnStateToken)
	}
	fmt.Fprintf(&b, "data: {\"type\":\"response.output_text.delta\",\"delta\":%q}\n\n", lastText)
	fmt.Fprintf(&b, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp-%d\",\"status\":\"completed\",\"usage\":{\"input_tokens\":1,\"output_tokens\":1},\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":%q}]}]}}\n\n", f.respSeq, lastText)
	return b.String()
}

func (f *fakeCodexWSServer) request(i int) map[string]interface{} {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.requests[i].Body
}

func (f *fakeCodexWSServer) numRequests() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.requests)
}

// wsTestOpts returns Codex WS options pinned to a fixed session key.
func wsTestOpts(cacheKey string) provider.StreamOptions {
	return provider.StreamOptions{
		Transport:      provider.TransportWebSocket,
		PromptCacheKey: cacheKey,
		APIKey:         "test-token",
	}
}

func codexWSModel(srv *httptest.Server) provider.Model {
	return provider.Model{ID: "gpt-5-codex", BaseURL: srv.URL, Provider: provider.ProviderOpenAI}
}

func userMsg(text string) provider.Message {
	return provider.NewUserMessage(text)
}

func assistantMsg(text string) provider.Message {
	return provider.NewAssistantMessage([]provider.ContentBlock{{Type: provider.ContentBlockText, Text: text}})
}

// drainWSStream consumes the stream to completion so the baseline recorder
// finishes before the next assertion/turn.
func drainWSStream(t *testing.T, stream *provider.AssistantMessageEventStream) {
	t.Helper()
	for range stream.Seq() {
	}
}

// sendCodexWSTurn issues one Codex WS turn and drains the resulting stream.
func sendCodexWSTurn(t *testing.T, model provider.Model, ctx provider.Context, opts provider.StreamOptions) {
	t.Helper()
	stream, err := streamResponses(model, ctx, opts, "codex")
	if err != nil {
		t.Fatalf("streamResponses: %v", err)
	}
	drainWSStream(t, stream)
}

// bodyInputTexts extracts the input item contents for assertions.
func bodyInputTexts(t *testing.T, body map[string]interface{}) []string {
	t.Helper()
	input, ok := body["input"].([]interface{})
	if !ok {
		t.Fatalf("body has no input array: %v", body)
	}
	out := make([]string, 0, len(input))
	for _, item := range input {
		m, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		text, _ := m["content"].(string)
		out = append(out, text)
	}
	return out
}

// assertWSTurnInput requires the captured request at index i to carry exactly
// the wanted input item texts (order and count).
func assertWSTurnInput(t *testing.T, fake *fakeCodexWSServer, i int, want ...string) {
	t.Helper()
	got := bodyInputTexts(t, fake.request(i))
	if !reflect.DeepEqual(got, want) {
		t.Errorf("turn %d input = %v, want %v", i+1, got, want)
	}
}

// assertWSChainedBy requires the body to chain by the given response id.
func assertWSChainedBy(t *testing.T, body map[string]interface{}, want string) {
	t.Helper()
	if got := body["previous_response_id"]; got != want {
		t.Errorf("previous_response_id = %v, want %v", got, want)
	}
}

// assertWSNotChained requires the body to omit previous_response_id entirely.
func assertWSNotChained(t *testing.T, body map[string]interface{}) {
	t.Helper()
	if _, chained := body["previous_response_id"]; chained {
		t.Error("must not carry previous_response_id")
	}
}

// assertWSCacheKeyStable requires every captured request to carry the given
// session-stable prompt cache key.
func assertWSCacheKeyStable(t *testing.T, fake *fakeCodexWSServer, turns int, key string) {
	t.Helper()
	for i := 0; i < turns; i++ {
		if got := fake.request(i)["prompt_cache_key"]; got != key {
			t.Errorf("turn %d prompt_cache_key = %v, want %s", i+1, got, key)
		}
	}
}

// TestWSCodexSendsDeltaChainedByPreviousResponseID drives a three-turn Codex
// WS conversation: turn 1 is a full send; turns 2 and 3 must send only the
// new tail as input chained by the previous response id, and the
// prompt_cache_key must stay identical across the whole sequence.
func TestWSCodexSendsDeltaChainedByPreviousResponseID(t *testing.T) {
	resetWSSessionState()
	defer resetWSSessionState()

	fake := newFakeCodexWSServer(t)
	srv := httptest.NewServer(http.HandlerFunc(fake.handler))
	defer srv.Close()

	model := codexWSModel(srv)
	opts := wsTestOpts("ws-sess-1")
	sys := provider.Context{SystemPrompt: "You are a coding agent."}

	// Turn 1: no baseline yet → full history.
	turn1 := sys
	turn1.Messages = []provider.Message{userMsg("u1")}
	sendCodexWSTurn(t, model, turn1, opts)

	// Turn 2: exact append (server answered "u1") → delta + previous_response_id.
	turn2 := sys
	turn2.Messages = []provider.Message{userMsg("u1"), assistantMsg("u1"), userMsg("u2")}
	sendCodexWSTurn(t, model, turn2, opts)

	// Turn 3: append again → delta chained by turn 2's response id.
	turn3 := sys
	turn3.Messages = []provider.Message{userMsg("u1"), assistantMsg("u1"), userMsg("u2"), assistantMsg("u2"), userMsg("u3")}
	sendCodexWSTurn(t, model, turn3, opts)

	if fake.numRequests() != 3 {
		t.Fatalf("server saw %d requests, want 3", fake.numRequests())
	}

	// Turn 1: full history, no chaining.
	assertWSTurnInput(t, fake, 0, "u1")
	assertWSNotChained(t, fake.request(0))
	// Turns 2/3: delta of one message chained by the prior response id.
	assertWSTurnInput(t, fake, 1, "u2")
	assertWSChainedBy(t, fake.request(1), "resp-1")
	assertWSTurnInput(t, fake, 2, "u3")
	assertWSChainedBy(t, fake.request(2), "resp-2")

	// prompt_cache_key must be identical (session-stable) across the sequence.
	assertWSCacheKeyStable(t, fake, 3, "ws-sess-1")
}

// TestWSCodexPropertyChangeForcesFullSend: when a matched property (here the
// model id) changes mid-session, the next request must be a full send even
// though the input is a strict append.
func TestWSCodexPropertyChangeForcesFullSend(t *testing.T) {
	resetWSSessionState()
	defer resetWSSessionState()

	fake := newFakeCodexWSServer(t)
	srv := httptest.NewServer(http.HandlerFunc(fake.handler))
	defer srv.Close()

	opts := wsTestOpts("ws-sess-prop")
	sys := provider.Context{SystemPrompt: "You are a coding agent."}

	model := codexWSModel(srv)
	turn1 := sys
	turn1.Messages = []provider.Message{userMsg("u1")}
	sendCodexWSTurn(t, model, turn1, opts)

	// Same session, exact append, but a different model id.
	model2 := codexWSModel(srv)
	model2.ID = "gpt-5-codex-mini"
	turn2 := sys
	turn2.Messages = []provider.Message{userMsg("u1"), assistantMsg("u1"), userMsg("u2")}
	sendCodexWSTurn(t, model2, turn2, opts)

	if fake.numRequests() != 2 {
		t.Fatalf("server saw %d requests, want 2", fake.numRequests())
	}
	b2 := fake.request(1)
	if got := bodyInputTexts(t, b2); len(got) != 3 {
		t.Errorf("turn 2 input = %v, want full history (3 items) after property change", got)
	}
	if _, chained := b2["previous_response_id"]; chained {
		t.Error("turn 2 must not chain by previous_response_id after property change")
	}
}

// TestWSCodexCompactionForcesFullSend: a compaction replaces the history, so
// the prefix check fails and the next request must be a full send with no
// chaining — never a delta across a generation boundary.
func TestWSCodexCompactionForcesFullSend(t *testing.T) {
	resetWSSessionState()
	defer resetWSSessionState()

	fake := newFakeCodexWSServer(t)
	srv := httptest.NewServer(http.HandlerFunc(fake.handler))
	defer srv.Close()

	model := codexWSModel(srv)
	opts := wsTestOpts("ws-sess-compact")
	sys := provider.Context{SystemPrompt: "You are a coding agent."}

	turn1 := sys
	turn1.Messages = []provider.Message{userMsg("u1")}
	sendCodexWSTurn(t, model, turn1, opts)

	// Compaction replaces history with a summary + retained tail: not a
	// strict append of the baseline conversation.
	turn2 := sys
	turn2.Messages = []provider.Message{userMsg("[summary of u1]"), userMsg("u2")}
	sendCodexWSTurn(t, model, turn2, opts)

	if fake.numRequests() != 2 {
		t.Fatalf("server saw %d requests, want 2", fake.numRequests())
	}
	b2 := fake.request(1)
	if got := bodyInputTexts(t, b2); len(got) != 2 || got[0] != "[summary of u1]" {
		t.Errorf("post-compaction input = %v, want full replaced history", got)
	}
	if _, chained := b2["previous_response_id"]; chained {
		t.Error("post-compaction request must not chain by previous_response_id")
	}
	// The session-stable prompt_cache_key survives compaction on the WS path.
	if got := b2["prompt_cache_key"]; got != "ws-sess-compact" {
		t.Errorf("post-compaction prompt_cache_key = %v, want ws-sess-compact", got)
	}
}

// TestWSCodexWSUnsupportedFallsBackToSSE: the endpoint rejects the WS upgrade
// with 426 Upgrade Required; the session must be marked, the request retried
// as full-history SSE, and subsequent requests for the same session go
// straight to SSE while other sessions keep using WS.
func TestWSCodexWSUnsupportedFallsBackToSSE(t *testing.T) {
	resetWSSessionState()
	defer resetWSSessionState()

	var mu sync.Mutex
	var sseBodies [][]byte
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
			w.WriteHeader(http.StatusUpgradeRequired)
			return
		}
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		sseBodies = append(sseBodies, body)
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"ok\"}\n\n")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n")
	})
	srv := httptest.NewServer(handler)
	defer srv.Close()

	model := codexWSModel(srv)
	ctx := provider.Context{
		SystemPrompt: "You are a coding agent.",
		Messages:     []provider.Message{userMsg("hello")},
	}

	// Session A: first request hits 426, falls back to SSE.
	stream, err := streamResponses(model, ctx, wsTestOpts("ws-sess-426"), "codex")
	if err != nil {
		t.Fatalf("streamResponses (session A): %v", err)
	}
	drainWSStream(t, stream)
	if got := stream.Result().Content[0].Text; got != "ok" {
		t.Fatalf("session A result = %q, want ok", got)
	}
	if !isWSFallback("ws-sess-426") {
		t.Error("session A not marked WS-unsupported after 426")
	}

	// Session A again: goes straight to SSE (no second WS attempt).
	stream, err = streamResponses(model, ctx, wsTestOpts("ws-sess-426"), "codex")
	if err != nil {
		t.Fatalf("streamResponses (session A retry): %v", err)
	}
	drainWSStream(t, stream)

	mu.Lock()
	nSSE := len(sseBodies)
	mu.Unlock()
	if nSSE != 2 {
		t.Errorf("SSE requests for session A = %d, want 2 (fallback + direct)", nSSE)
	}
	// Fallback SSE body is the full-history codex shape: no chaining.
	if strings.Contains(string(sseBodies[0]), "previous_response_id") {
		t.Error("fallback SSE request contains forbidden previous_response_id")
	}
	if !strings.Contains(string(sseBodies[0]), `"prompt_cache_key":"ws-sess-426"`) {
		t.Error("fallback SSE request lost the session cache key")
	}

	// Session B on the same endpoint is unaffected: it gets its own WS
	// attempt (rejected) before its own fallback.
	if isWSFallback("ws-sess-other") {
		t.Fatal("session B marked before its first request")
	}
	stream, err = streamResponses(model, ctx, wsTestOpts("ws-sess-other"), "codex")
	if err != nil {
		t.Fatalf("streamResponses (session B): %v", err)
	}
	drainWSStream(t, stream)
	if !isWSFallback("ws-sess-other") {
		t.Error("session B did not get its own fallback mark")
	}
}

// TestWSCodexFailedRequestKeepsBaseline: a WS stream that dies without a
// completed event must not advance the baseline; the next (retried) request
// must still chain against the pre-failure response id with a correct delta.
func TestWSCodexFailedRequestKeepsBaseline(t *testing.T) {
	resetWSSessionState()
	defer resetWSSessionState()

	var mu sync.Mutex
	failNext := false
	fake := newFakeCodexWSServer(t)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := fake.upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		_, msg, err := conn.ReadMessage()
		if err != nil {
			return
		}
		fake.record(msg)
		mu.Lock()
		fail := failNext
		failNext = false
		mu.Unlock()
		if fail {
			// Die mid-stream: a partial delta with no completed event.
			_ = conn.WriteMessage(websocket.TextMessage, []byte("data: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n\n"))
			return
		}
		_ = conn.WriteMessage(websocket.TextMessage, []byte(fake.responseFor(msg)))
	})
	srv := httptest.NewServer(handler)
	defer srv.Close()

	model := codexWSModel(srv)
	opts := wsTestOpts("ws-sess-fail")
	sys := provider.Context{SystemPrompt: "You are a coding agent."}

	// Turn 1: full send, baseline recorded (resp-1).
	turn1 := sys
	turn1.Messages = []provider.Message{userMsg("u1")}
	sendCodexWSTurn(t, model, turn1, opts)

	// Turn 2: the stream dies mid-flight → baseline must stay at resp-1.
	mu.Lock()
	failNext = true
	mu.Unlock()
	turn2 := sys
	turn2.Messages = []provider.Message{userMsg("u1"), assistantMsg("u1"), userMsg("u2")}
	stream, err := streamResponses(model, turn2, opts, "codex")
	if err != nil {
		t.Fatalf("streamResponses (failed turn): %v", err)
	}
	drainWSStream(t, stream)

	b := wsBaseline("ws-sess-fail")
	if b == nil || b.ResponseID != "resp-1" {
		t.Fatalf("baseline advanced on failure: %+v", b)
	}

	// Turn 3 (retry of turn 2's intent): the decision rebuilds from the
	// current baseline, so it chains by resp-1 again with the same delta tail.
	turn3 := sys
	turn3.Messages = []provider.Message{userMsg("u1"), assistantMsg("u1"), userMsg("u2"), assistantMsg("u2"), userMsg("u3")}
	sendCodexWSTurn(t, model, turn3, opts)

	if fake.numRequests() != 3 {
		t.Fatalf("server saw %d requests, want 3", fake.numRequests())
	}
	// Turn 2 was a delta chained by resp-1 (decision ran before the failure).
	if got := fake.request(1)["previous_response_id"]; got != "resp-1" {
		t.Errorf("turn 2 previous_response_id = %v, want resp-1", got)
	}
	// Turn 3: the conversation still matches the resp-1 baseline + appended
	// tail (u2, a2, u3), so the retry chains by resp-1 — never a stale id.
	b3 := fake.request(2)
	if got := b3["previous_response_id"]; got != "resp-1" {
		t.Errorf("turn 3 previous_response_id = %v, want resp-1 (retry rebuilt from current baseline)", got)
	}
	if got := bodyInputTexts(t, b3); len(got) != 3 || got[0] != "u2" {
		t.Errorf("turn 3 input = %v, want delta [u2 a2 u3]", got)
	}
}

// TestWSCodexSSEBodyUnchangedWhenWSPathExists pins the SSE invariant: the SSE
// request bytes are exactly the legacy codex shape (full history, cache key,
// store=false, no previous_response_id) regardless of the WS machinery.
func TestWSCodexSSEBodyUnchangedWhenWSPathExists(t *testing.T) {
	resetWSSessionState()
	defer resetWSSessionState()

	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n")
	}))
	defer srv.Close()

	model := codexWSModel(srv)
	ctx := provider.Context{
		SystemPrompt: "You are a coding agent.",
		Messages:     []provider.Message{userMsg("hi"), assistantMsg("there"), userMsg("again")},
	}
	stream, err := streamResponses(model, ctx, provider.StreamOptions{
		PromptCacheKey: "sse-key", APIKey: "tok",
	}, "codex") // no TransportWebSocket → SSE path
	if err != nil {
		t.Fatalf("streamResponses: %v", err)
	}
	drainWSStream(t, stream)

	body := string(gotBody)
	for _, want := range []string{`"prompt_cache_key":"sse-key"`, `"store":false`, `"instructions":"You are a coding agent."`} {
		if !strings.Contains(body, want) {
			t.Errorf("SSE body missing %s: %s", want, body)
		}
	}
	if strings.Contains(body, "previous_response_id") {
		t.Error("SSE body contains forbidden previous_response_id")
	}
	// Full history (all three messages) must be present.
	if !strings.Contains(body, `"hi"`) || !strings.Contains(body, `"there"`) || !strings.Contains(body, `"again"`) {
		t.Errorf("SSE body lost history items: %s", body)
	}
	// The SSE path must not record a WS baseline.
	if wsBaseline("sse-key") != nil {
		t.Error("SSE path recorded a WS baseline")
	}
}

// TestIsWSUnsupportedError covers the error classifier: a rejected upgrade
// (typed error or 426/upgrade text) is unsupported, other transport errors
// are not.
func TestIsWSUnsupportedError(t *testing.T) {
	cases := []struct {
		err  error
		want bool
	}{
		{nil, false},
		{fmt.Errorf("OpenAI Responses WebSocket: websocket connect failed: %w", &transport.UpgradeRequiredError{URL: "wss://x", StatusCode: 426}), true},
		{fmt.Errorf("wrap: %w", &transport.UpgradeRequiredError{URL: "wss://x", StatusCode: 404}), true},
		{fmt.Errorf("websocket connect failed: websocket: bad handshake: server returned status 426"), true},
		{fmt.Errorf("OpenAI Responses WebSocket: websocket connect failed: 426 Upgrade Required"), true},
		{fmt.Errorf("websocket connect failed: dial tcp: connection refused"), false},
		{fmt.Errorf("websocket write failed: broken pipe"), false},
	}
	for i, tc := range cases {
		if got := isWSUnsupportedError(tc.err); got != tc.want {
			t.Errorf("case %d: isWSUnsupportedError(%v) = %v, want %v", i, tc.err, got, tc.want)
		}
	}
}
