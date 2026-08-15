// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package provider

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/agentic/provider/schema"
	"github.com/pijalu/goa/internal/agentic/provider/transport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runCaptured runs one protocol-backed stream through the canonical chain
// against the capture transport and returns the transport request that was
// actually sent. Every GenericStream call is tagged by the canonical
// PurposeHeadersInterceptor, so the captured request carries the attribution
// headers this suite asserts.
func runCaptured(t *testing.T, model schema.Model, opts schema.StreamOptions) *transport.TransportRequest {
	t.Helper()
	old := transport.Default()
	defer transport.SetDefault(old)
	capt := &captureTransport{}
	transport.SetDefault(capt)

	stream, err := GenericStream(model,
		schema.Context{Messages: []schema.Message{schema.NewUserMessage("hi")}},
		opts)
	require.NoError(t, err)
	require.NoError(t, stream.Err())
	_ = stream.Result()
	require.NotNil(t, capt.req, "transport should have received a request")
	return capt.req
}

// deepSeekModel is a DeepSeek-compat route: DeepSeek provider + endpoint and
// a deepseek model id.
var deepSeekModel = schema.Model{
	ID:       "deepseek-v4-flash",
	Api:      schema.ApiOpenAICompletions,
	Provider: schema.ProviderDeepSeek,
	BaseURL:  "https://api.deepseek.com/v1/chat/completions",
}

var openAIModel = schema.Model{
	ID:       "gpt-4o",
	Api:      schema.ApiOpenAICompletions,
	Provider: schema.ProviderOpenAI,
	BaseURL:  "http://example.com/v1/chat/completions",
}

// TestPurposeHeaders_UserIDOnAllCalls is the P13 acceptance core: every
// provider call carries x-goa-user-id with the stable anonymous id.
func TestPurposeHeaders_UserIDOnAllCalls(t *testing.T) {
	req := runCaptured(t, openAIModel, schema.StreamOptions{MaxTokens: 10, APIKey: "sk-test", Headers: map[string]string{}})

	uid := req.Headers[HeaderGoaUserID]
	require.NotEmpty(t, uid, "every provider call must carry x-goa-user-id")
	// The value is the stable anonymous id from internal/idgen (UUID v4).
	assert.Regexp(t, `^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`, uid)
	// Stable across calls within the process.
	req2 := runCaptured(t, openAIModel, schema.StreamOptions{MaxTokens: 10, APIKey: "sk-test", Headers: map[string]string{}})
	assert.Equal(t, uid, req2.Headers[HeaderGoaUserID], "anonymous id must be stable across calls")
}

// TestPurposeHeaders_SessionIDHeaderPresentAndExact verifies the session
// correlation header: present with the exact SessionID value when set, and
// omitted when no session is attached (mirrors dsh).
func TestPurposeHeaders_SessionIDHeaderPresentAndExact(t *testing.T) {
	req := runCaptured(t, openAIModel, schema.StreamOptions{MaxTokens: 10, APIKey: "sk-test", SessionID: "sess-abc-123"})
	assert.Equal(t, "sess-abc-123", req.Headers[HeaderGoaSessionID], "x-goa-session-id must carry the exact SessionID")

	reqNoSession := runCaptured(t, openAIModel, schema.StreamOptions{MaxTokens: 10, APIKey: "sk-test"})
	_, ok := reqNoSession.Headers[HeaderGoaSessionID]
	assert.False(t, ok, "a call without a session must omit x-goa-session-id")
}

// TestPurposeHeaders_CompactOnCompactionDeepSeek verifies x-goa-compact: 1 is
// emitted on compaction calls over DeepSeek-compat routes.
func TestPurposeHeaders_CompactOnCompactionDeepSeek(t *testing.T) {
	req := runCaptured(t, deepSeekModel, schema.StreamOptions{
		MaxTokens: 10, APIKey: "sk-test", Purpose: schema.PurposeCompaction,
	})
	assert.Equal(t, "1", req.Headers[HeaderGoaCompact], "compaction over DeepSeek-compat route must carry x-goa-compact: 1")
}

// TestPurposeHeaders_NoCompactOffDeepSeekRoute verifies the route gating: the
// same compaction call over a non-DeepSeek route omits the compact header.
func TestPurposeHeaders_NoCompactOffDeepSeekRoute(t *testing.T) {
	req := runCaptured(t, openAIModel, schema.StreamOptions{
		MaxTokens: 10, APIKey: "sk-test", Purpose: schema.PurposeCompaction,
	})
	_, ok := req.Headers[HeaderGoaCompact]
	assert.False(t, ok, "compaction over a non-DeepSeek route must omit x-goa-compact")
}

// TestPurposeHeaders_NoCompactOnConversation verifies compaction gating:
// conversation calls never carry x-goa-compact, even on DeepSeek routes.
func TestPurposeHeaders_NoCompactOnConversation(t *testing.T) {
	req := runCaptured(t, deepSeekModel, schema.StreamOptions{MaxTokens: 10, APIKey: "sk-test"})
	_, ok := req.Headers[HeaderGoaCompact]
	assert.False(t, ok, "conversation calls must not carry x-goa-compact")

	reqSessionTitle := runCaptured(t, deepSeekModel, schema.StreamOptions{
		MaxTokens: 10, APIKey: "sk-test", Purpose: schema.PurposeSessionTitle,
	})
	_, ok = reqSessionTitle.Headers[HeaderGoaCompact]
	assert.False(t, ok, "session-title calls must not carry x-goa-compact")
}

// TestDefaultMaxTokens_WireCarriesExplicitValue is the P21 (DS2) acceptance
// core at the wire level: on the DeepSeek route with no explicit max_tokens,
// the transport request carries the catalog default 256000 in the max_tokens
// field; an explicit request value always wins.
func TestDefaultMaxTokens_WireCarriesExplicitValue(t *testing.T) {
	req := runCaptured(t, deepSeekModel, schema.StreamOptions{APIKey: "sk-test"})
	var body map[string]any
	require.NoError(t, json.Unmarshal(req.Body, &body))
	assert.Equal(t, float64(256000), body["max_tokens"],
		"DeepSeek wire request must materialize the catalog default max_tokens")

	reqExplicit := runCaptured(t, deepSeekModel, schema.StreamOptions{APIKey: "sk-test", MaxTokens: 42})
	require.NoError(t, json.Unmarshal(reqExplicit.Body, &body))
	assert.Equal(t, float64(42), body["max_tokens"],
		"explicit max_tokens must always win on the wire")
}

// TestPurposeHeaders_NoIDInRequestBody is the P13 acceptance guard: the ids
// ride only in transport headers — never in the request body or
// model-visible content.
func TestPurposeHeaders_NoIDInRequestBody(t *testing.T) {
	req := runCaptured(t, deepSeekModel, schema.StreamOptions{
		MaxTokens: 10, APIKey: "sk-test", SessionID: "sess-secret-9",
		Purpose: schema.PurposeCompaction,
	})

	uid := req.Headers[HeaderGoaUserID]
	body := string(req.Body)
	assert.NotContains(t, body, uid, "anonymous id must never appear in the request body")
	assert.NotContains(t, body, "sess-secret-9", "session id must never appear in the request body")

	// The body must not expose any x-goa-* attribution key either.
	var parsed map[string]any
	require.NoError(t, json.Unmarshal([]byte(body), &parsed))
	for k := range parsed {
		assert.NotContains(t, strings.ToLower(k), "goa", "attribution must stay in headers, got body key %q", k)
	}
}

// TestPurposeHeaders_RegisteredInCanonicalChain proves the interceptor is a
// canonical consumer: a plain GenericStream call (no explicit interceptor)
// already carries the headers.
func TestPurposeHeaders_RegisteredInCanonicalChain(t *testing.T) {
	found := false
	for _, fn := range StreamInterceptors() {
		if reflect.ValueOf(fn).Pointer() == reflect.ValueOf(PurposeHeadersInterceptor).Pointer() {
			found = true
			break
		}
	}
	assert.True(t, found, "PurposeHeadersInterceptor must be in the canonical chain")
}

// TestPurposeHeaders_DeepSeekModelProxiedUnderOtherProvider keeps the
// isDeepSeekRoute model-id fallback honest: a deepseek model served under a
// generic provider is still a DeepSeek-compat route.
func TestPurposeHeaders_DeepSeekModelProxiedUnderOtherProvider(t *testing.T) {
	model := schema.Model{
		ID:       "deepseek-v4-flash-free",
		Api:      schema.ApiOpenAICompletions,
		Provider: schema.ProviderOpenCode,
		BaseURL:  "https://opencode.ai/v1",
	}
	req := runCaptured(t, model, schema.StreamOptions{
		MaxTokens: 10, APIKey: "sk-test", Purpose: schema.PurposeCompaction,
	})
	assert.Equal(t, "1", req.Headers[HeaderGoaCompact],
		"a deepseek model under another provider is still DeepSeek-compat")
}
