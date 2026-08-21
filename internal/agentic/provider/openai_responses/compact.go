// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package openairesponses

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/pijalu/goa/internal/agentic/provider"
)

// compactEndpointPath is the unary server-side conversation-compaction route
// (Codex Phase 2b). The base URL is the same Responses endpoint the
// conversation turns use; the path suffix selects the compact verb.
const compactEndpointPath = "/responses/compact"

// compactRequestTimeoutIdleMultiplier mirrors Codex
// COMPACT_REQUEST_TIMEOUT_IDLE_MULTIPLIER: the compact call's overall timeout
// is the configured idle (per-byte) timeout scaled up, because a unary
// compaction reprocesses the whole transcript and legitimately takes far
// longer than a single streamed byte gap.
const compactRequestTimeoutIdleMultiplier = 4

// compactionInput mirrors the Codex ApiCompactionInput wire body: only the
// fields the normal request path already sends, never stream/store/
// tool_choice/previous_response_id/max_output_tokens. Optional fields are
// omitted when empty so the body stays minimal.
type compactionInput struct {
	Model             string                   `json:"model"`
	Input             []map[string]interface{} `json:"input"`
	Instructions      string                   `json:"instructions,omitempty"`
	Tools             []map[string]interface{} `json:"tools,omitempty"`
	ParallelToolCalls bool                     `json:"parallel_tool_calls"`
	Reasoning         map[string]interface{}   `json:"reasoning,omitempty"`
	ServiceTier       string                   `json:"service_tier,omitempty"`
	PromptCacheKey    string                   `json:"prompt_cache_key,omitempty"`
	Text              map[string]interface{}   `json:"text,omitempty"`
}

// compactHistoryResponse mirrors the Codex CompactHistoryResponse envelope:
// the replacement transcript rides in output as a list of ResponseItems.
type compactHistoryResponse struct {
	Output []compactResponseItem `json:"output"`
}

// compactResponseItem is the subset of a Codex ResponseItem Goa needs to
// rebuild a canonical transcript: the item discriminator, its role, its text
// content parts, and the function-call/result correlation fields.
type compactResponseItem struct {
	Type    string `json:"type"`
	Role    string `json:"role,omitempty"`
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content,omitempty"`
	// function_call fields.
	Name      string `json:"name,omitempty"`
	CallID    string `json:"call_id,omitempty"`
	Arguments string `json:"arguments,omitempty"`
	// function_call_output field (output may be a string or a structured body).
	Output json.RawMessage `json:"output,omitempty"`
}

// Compact implements provider.RemoteCompactor for the ChatGPT Codex
// subscription transport.
func (p *OpenAICodexResponsesProvider) Compact(ctx context.Context, req provider.CompactRequest) (*provider.CompactResponse, error) {
	return compactConversation(ctx, req, "codex")
}

// Compact implements provider.RemoteCompactor for the plain OpenAI Responses
// transport, which also exposes /responses/compact.
func (p *OpenAIResponsesProvider) Compact(ctx context.Context, req provider.CompactRequest) (*provider.CompactResponse, error) {
	return compactConversation(ctx, req, "")
}

// compactConversation POSTs the compaction input to the compact endpoint and
// decodes the returned replacement item list into canonical messages. It is a
// unary (non-streaming) call with its own bounded timeout.
func compactConversation(ctx context.Context, req provider.CompactRequest, flavor string) (*provider.CompactResponse, error) {
	if len(req.Context.Messages) == 0 {
		// Mirror Codex: an empty input compacts to an empty replacement.
		return &provider.CompactResponse{}, nil
	}

	bodyBytes, err := buildCompactBodyBytes(req, flavor)
	if err != nil {
		return nil, err
	}

	baseURL := compactBaseURL(req.Model, req.Options, flavor)
	timeout := compactRequestTimeout(req.Options)

	httpReq, err := http.NewRequestWithContext(ctx, "POST", baseURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("create compact request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if req.Options.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+req.Options.APIKey)
	}
	for k, v := range req.Options.Headers {
		httpReq.Header.Set(k, v)
	}
	if flavor == "codex" {
		applyCodexHeaders(httpReq, req.Options)
	}

	client := provider.NewHTTPClientWithTimeout(timeout)
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("send compact request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// Drain a bounded slice for diagnostics; the body may carry a provider
		// error string but never raw session keys or prompts (invariant 4).
		bodyErr, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("responses compact returned %d: %s", resp.StatusCode, string(bodyErr))
	}

	var parsed compactHistoryResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("decode compact response: %w", err)
	}

	return &provider.CompactResponse{Messages: compactItemsToMessages(parsed.Output)}, nil
}

// buildCompactBodyBytes marshals the compaction input for the wire.
func buildCompactBodyBytes(req provider.CompactRequest, flavor string) ([]byte, error) {
	bodyBytes, err := json.Marshal(buildCompactBody(req, flavor))
	if err != nil {
		return nil, fmt.Errorf("marshal compact request: %w", err)
	}
	return bodyBytes, nil
}

// buildCompactBody constructs the compaction input, reusing the same input
// conversion the conversation turns use so the compact request carries the
// identical transcript prefix.
func buildCompactBody(req provider.CompactRequest, flavor string) compactionInput {
	isCodex := flavor == "codex"
	ctx := req.Context

	instructions := ""
	if isCodex {
		instructions = ctx.SystemPrompt
		if instructions == "" {
			instructions = "You are a helpful assistant."
		}
	}

	body := compactionInput{
		Model:        req.Model.ID,
		Input:        convertResponsesInput(ctx.Messages, systemPromptFor(ctx, isCodex)),
		Instructions: instructions,
	}
	if !ctx.NoTools && len(ctx.Tools) > 0 {
		body.Tools = convertResponsesTools(ctx.Tools)
		body.ParallelToolCalls = true
	}
	if req.Options.ServiceTier != "" {
		body.ServiceTier = req.Options.ServiceTier
	}
	// Reasoning/text mirror the normal Codex request shape for reasoning
	// models: the reasoning summary block and a low-verbosity text control.
	if req.Model.Reasoning {
		body.Reasoning = map[string]interface{}{"summary": "auto"}
		body.Text = map[string]interface{}{"verbosity": "low"}
	}
	// Codex carries session affinity via prompt_cache_key only.
	if key := compactCacheKey(req.Options); key != "" {
		body.PromptCacheKey = key
	}
	return body
}

// compactCacheKey resolves the session-affinity key for the compact request,
// preferring the opaque prompt cache key over the session ID.
func compactCacheKey(opts provider.StreamOptions) string {
	if opts.PromptCacheKey != "" {
		return opts.PromptCacheKey
	}
	return opts.SessionID
}

// compactBaseURL resolves the compact endpoint: the model's configured base
// URL (already the .../responses route for the conversation path) with the
// path swapped to the compact verb, or the flavor default.
func compactBaseURL(model provider.Model, opts provider.StreamOptions, flavor string) string {
	base := streamResponsesBaseURL(model, opts, flavor)
	return replaceResponsesPath(base, compactEndpointPath)
}

// replaceResponsesPath swaps the trailing Responses route of a base URL for
// the compact verb. A base that already ends in the responses path gets the
// suffix replaced; any other base gets the compact path appended.
func replaceResponsesPath(base, compactPath string) string {
	for _, suffix := range []string{"/responses/codex", "/responses"} {
		if n := len(base); n >= len(suffix) && base[n-len(suffix):] == suffix {
			return base[:n-len(suffix)] + compactPath
		}
	}
	return base + compactPath
}

// compactRequestTimeout bounds the unary compact call. It scales the
// configured idle timeout by the Codex multiplier; with no idle timeout
// configured it falls back to a scaled default so the call is always bounded.
func compactRequestTimeout(opts provider.StreamOptions) time.Duration {
	idle := opts.IdleTimeout
	if idle <= 0 {
		idle = provider.DefaultStreamIdleTimeout
	}
	return idle * compactRequestTimeoutIdleMultiplier
}

// compactItemsToMessages converts the returned ResponseItems into a canonical
// transcript. Message items become user/assistant text; function_call items
// attach a tool call to the current assistant message; function_call_output
// items become tool results. Reasoning and other opaque items are dropped —
// they do not round-trip into Goa's text history.
func compactItemsToMessages(items []compactResponseItem) []provider.Message {
	var messages []provider.Message
	for i := range items {
		item := items[i]
		switch item.Type {
		case "message":
			messages = append(messages, compactMessageToProvider(item))
		case "function_call":
			messages = append(messages, provider.NewAssistantMessage([]provider.ContentBlock{{
				Type:          provider.ContentBlockToolCall,
				ToolCallID:    item.CallID,
				ToolName:      item.Name,
				ToolArguments: item.Arguments,
			}}))
		case "function_call_output":
			messages = append(messages, provider.NewToolResultMessage(
				item.CallID, "", compactOutputText(item.Output), false))
		}
	}
	return messages
}

// compactMessageToProvider maps a message item to a canonical message,
// concatenating its text content parts. An assistant role stays assistant;
// everything else (user, and any system the server returns) maps to user so a
// strict chat template never sees a non-leading system message.
func compactMessageToProvider(item compactResponseItem) provider.Message {
	var text string
	for _, c := range item.Content {
		if c.Type == "output_text" || c.Type == "input_text" || c.Type == "text" {
			text += c.Text
		}
	}
	if item.Role == "assistant" {
		return provider.NewAssistantMessage([]provider.ContentBlock{{
			Type: provider.ContentBlockText, Text: text,
		}})
	}
	return provider.NewUserMessage(text)
}

// compactOutputText extracts a plain string from a function_call_output body,
// which Codex may return either as a bare string or as a structured payload.
func compactOutputText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	// Structured body: keep the raw JSON so no information is lost.
	return string(raw)
}
