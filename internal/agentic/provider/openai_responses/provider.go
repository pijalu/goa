// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package openairesponses

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/pijalu/goa/internal/agentic/provider"
	"github.com/pijalu/goa/internal/agentic/provider/schema"
)

func init() {
	provider.RegisterApiProvider(&OpenAIResponsesProvider{})
	provider.RegisterApiProvider(&OpenAICodexResponsesProvider{})
	provider.RegisterApiProvider(&AzureOpenAIResponsesProvider{})
}

type OpenAIResponsesProvider struct{}
type OpenAICodexResponsesProvider struct{}
type AzureOpenAIResponsesProvider struct{}

func (p *OpenAIResponsesProvider) API() provider.Api      { return provider.ApiOpenAIResponses }
func (p *OpenAICodexResponsesProvider) API() provider.Api { return provider.ApiOpenAICodexResponses }
func (p *AzureOpenAIResponsesProvider) API() provider.Api { return provider.ApiAzureOpenAIResponses }

func (p *OpenAIResponsesProvider) Stream(model provider.Model, ctx provider.Context, opts provider.StreamOptions) (*provider.AssistantMessageEventStream, error) {
	return streamResponses(model, ctx, opts, "")
}
func (p *OpenAIResponsesProvider) StreamSimple(model provider.Model, ctx provider.Context, opts provider.SimpleStreamOptions) (*provider.AssistantMessageEventStream, error) {
	base := provider.BuildSimpleOptions(model, opts)
	return p.Stream(model, ctx, base)
}

func (p *OpenAICodexResponsesProvider) Stream(model provider.Model, ctx provider.Context, opts provider.StreamOptions) (*provider.AssistantMessageEventStream, error) {
	return streamResponses(model, ctx, opts, "codex")
}
func (p *OpenAICodexResponsesProvider) StreamSimple(model provider.Model, ctx provider.Context, opts provider.SimpleStreamOptions) (*provider.AssistantMessageEventStream, error) {
	base := provider.BuildSimpleOptions(model, opts)
	return p.Stream(model, ctx, base)
}

func (p *AzureOpenAIResponsesProvider) Stream(model provider.Model, ctx provider.Context, opts provider.StreamOptions) (*provider.AssistantMessageEventStream, error) {
	return streamAzureResponses(model, ctx, opts)
}
func (p *AzureOpenAIResponsesProvider) StreamSimple(model provider.Model, ctx provider.Context, opts provider.SimpleStreamOptions) (*provider.AssistantMessageEventStream, error) {
	base := provider.BuildSimpleOptions(model, opts)
	return p.Stream(model, ctx, base)
}

func streamResponses(model provider.Model, ctx provider.Context, opts provider.StreamOptions, flavor string) (*provider.AssistantMessageEventStream, error) {
	baseURL := streamResponsesBaseURL(model, opts, flavor)

	bodyBytes, err := buildResponsesBodyBytes(model, ctx, opts, flavor)
	if err != nil {
		return nil, err
	}

	if opts.Transport == provider.TransportWebSocket {
		return streamResponsesWebSocket(model, ctx, opts, bodyBytes, baseURL, flavor)
	}
	return sendResponsesSSE(ctx, opts, bodyBytes, baseURL, flavor)
}

// streamResponsesBaseURL resolves the endpoint for the request: the model's
// configured base URL, or the flavor default (Codex OAuth/API-key endpoints).
func streamResponsesBaseURL(model provider.Model, opts provider.StreamOptions, flavor string) string {
	if model.BaseURL != "" {
		return model.BaseURL
	}
	if flavor == "codex" {
		return codexBaseURL(opts)
	}
	return "https://api.openai.com/v1/responses"
}

// buildResponsesBodyBytes builds the request body and marshals it. This is the
// single body-build entry point: the WS full-history path and the SSE path use
// it unchanged, which keeps SSE request bytes byte-identical to today.
func buildResponsesBodyBytes(model provider.Model, ctx provider.Context, opts provider.StreamOptions, flavor string) ([]byte, error) {
	bodyBytes, err := json.Marshal(buildResponsesBody(model, ctx, opts, flavor))
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	return bodyBytes, nil
}

// sendResponsesSSE executes the full-history SSE POST and streams the parsed
// events. It is also the fallback transport for Codex sessions whose endpoint
// has rejected the WebSocket upgrade (per-session mark in ws_fallback.go): the
// request shape is identical to the regular SSE path, including headers.
func sendResponsesSSE(ctx provider.Context, opts provider.StreamOptions, bodyBytes []byte, baseURL string, flavor string) (*provider.AssistantMessageEventStream, error) {
	stream := provider.NewAssistantMessageEventStream(256)

	req, err := http.NewRequestWithContext(ctx.GoContext(), "POST", baseURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if opts.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+opts.APIKey)
	}
	for k, v := range opts.Headers {
		req.Header.Set(k, v)
	}
	// Codex OAuth subscription transport headers (after opts.Headers so the
	// account identity is always present and not user-overridable).
	if flavor == "codex" {
		applyCodexHeaders(req, opts)
	}

	// Replay the sticky-routing token captured at turn start (Codex only).
	// Absent token = first request of the turn or non-Codex flavor; nothing
	// to replay. The token is never logged or included in error diagnostics.
	sessionKey := ""
	if flavor == "codex" {
		sessionKey = turnStateSessionKey(opts)
		if ts := turnState(sessionKey); ts != "" {
			req.Header.Set(turnStateHeader, ts)
		}
	}

	client := provider.NewStreamingHTTPClient()

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		bodyErr, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("OpenAI Responses returned %d: %s", resp.StatusCode, string(bodyErr))
	}

	// Capture the server-issued turn-state token at turn start (Codex only).
	// This is the sticky-routing token that must be replayed on every
	// subsequent request within the same turn.
	if flavor == "codex" && sessionKey != "" {
		if ts := resp.Header.Get(turnStateHeader); ts != "" {
			captureTurnState(sessionKey, ts)
		}
	}

	go provider.CloseStreamOnCancel(ctx.GoContext(), stream)
	go parseResponsesSSE(resp.Body, stream)
	return stream, nil
}

// codexOAuthBaseURL is the subscription transport endpoint used when the
// credential is an OAuth token (ChatGPT Plus/Pro), per pi's implementation.
const codexOAuthBaseURL = "https://chatgpt.com/backend-api/codex/responses"

// codexAPIKeyBaseURL is the endpoint used with a plain OpenAI API key.
const codexAPIKeyBaseURL = "https://api.openai.com/v1/responses/codex"

// codexBaseURL selects the codex endpoint based on credential kind.
func codexBaseURL(opts provider.StreamOptions) string {
	if opts.CodexAccountID != "" {
		return codexOAuthBaseURL
	}
	return codexAPIKeyBaseURL
}

// applyCodexHeaders sets the Codex identity headers. The OAuth transport adds
// the ChatGPT account id and beta flag; both transports tag the originator.
func applyCodexHeaders(req *http.Request, opts provider.StreamOptions) {
	if req.Header.Get("originator") == "" {
		req.Header.Set("originator", "goa")
	}
	if opts.CodexAccountID == "" {
		return
	}
	req.Header.Set("chatgpt-account-id", opts.CodexAccountID)
	if req.Header.Get("OpenAI-Beta") == "" {
		req.Header.Set("OpenAI-Beta", "responses=experimental")
	}
	if req.Header.Get("accept") == "" {
		req.Header.Set("accept", "text/event-stream")
	}
}

func buildResponsesBody(model provider.Model, ctx provider.Context, opts provider.StreamOptions, flavor string) map[string]interface{} {
	isCodex := flavor == "codex"
	body := map[string]interface{}{
		"model":  model.ID,
		"input":  convertResponsesInput(ctx.Messages, systemPromptFor(ctx, isCodex)),
		"stream": true,
	}
	if isCodex {
		// ChatGPT Codex subscription transport (mirrors Pi/opencode): the system
		// prompt rides in the dedicated instructions field, the store must be
		// false (the subscription rejects store=true), and tool calls run auto /
		// parallel by default.
		instructions := ctx.SystemPrompt
		if instructions == "" {
			instructions = "You are a helpful assistant."
		}
		body["instructions"] = instructions
		body["store"] = false
		body["parallel_tool_calls"] = true
		body["tool_choice"] = "auto"
	}
	if ctx.NoTools {
		// Final-step collapse (P7): the model must answer text-only.
		body["tool_choice"] = "none"
		delete(body, "parallel_tool_calls")
	} else {
		body["tools"] = convertResponsesTools(ctx.Tools)
	}
	if opts.MaxTokens > 0 && !isCodex {
		// The ChatGPT Codex subscription backend rejects max_output_tokens with
		// a 400 ("Unsupported parameter") — the same class of rejection that
		// already forces store=false and prompt_cache_key-only session affinity
		// above. Omit the field on the codex flavor regardless of caller.
		body["max_output_tokens"] = opts.MaxTokens
	}
	if opts.Temperature != nil {
		body["temperature"] = *opts.Temperature
	}
	cacheKey := opts.PromptCacheKey
	if cacheKey == "" {
		cacheKey = opts.SessionID
	}
	// Session affinity rides prompt_cache_key on every responses flavor
	// (opencode parity): previous_response_id must reference a server-issued
	// resp_* object, and the Goa session ID there is a hard HTTP 400 on
	// strict upstreams (opencode Zen, 2026-09-02 — the same rejection class
	// the codex carve-out above already handled).
	if cacheKey != "" {
		body["prompt_cache_key"] = cacheKey
	}
	return body
}

// systemPromptFor returns the system prompt to inline as a leading input
// message; codex omits it because the prompt rides in the instructions field.
func systemPromptFor(ctx provider.Context, isCodex bool) string {
	if isCodex {
		return ""
	}
	return ctx.SystemPrompt
}

func convertResponsesInput(messages []provider.Message, systemPrompt string) []map[string]interface{} {
	var input []map[string]interface{}
	if systemPrompt != "" {
		input = append(input, map[string]interface{}{
			"role":    "system",
			"content": systemPrompt,
		})
	}
	for _, msg := range messages {
		switch msg.Role {
		case provider.RoleUser:
			input = append(input, map[string]interface{}{
				"role":    "user",
				"content": extractResponsesText(msg.Content),
			})
		case provider.RoleAssistant:
			input = append(input, map[string]interface{}{
				"role":    "assistant",
				"content": extractResponsesText(msg.Content),
			})
		case provider.RoleToolResult:
			tcID, tcName, text := extractToolCallInfo(msg.Content)
			input = append(input, map[string]interface{}{
				"role":         "tool",
				"tool_call_id": tcID,
				"content":      text,
				"name":         tcName,
			})
		}
	}
	return input
}

func convertResponsesTools(tools []provider.ToolSchema) []map[string]interface{} {
	out := make([]map[string]interface{}, len(tools))
	for i, t := range tools {
		out[i] = map[string]interface{}{
			"type":        "function",
			"name":        t.Name,
			"description": t.Description,
			"parameters":  t.InputSchema,
			"strict":      false,
		}
	}
	return out
}

func extractResponsesText(blocks []provider.ContentBlock) string {
	for _, b := range blocks {
		if b.Type == provider.ContentBlockText {
			return b.Text
		}
	}
	return ""
}

func extractToolCallInfo(blocks []provider.ContentBlock) (id, name, text string) {
	for _, b := range blocks {
		if b.Type == provider.ContentBlockToolResult {
			return b.ToolCallID, b.ToolName, b.Text
		}
	}
	return "", "", ""
}

// chunkRegistry maps SSE event type strings to their handlers.
// Using a registry avoids a single large switch, keeping cognitive complexity
// per handler ≤ 15 (AGENTS.md budget).
type responsesEventContext struct {
	contentBuf string
	outputText string
	started    bool
	ended      bool
	decodeErr  error
	stream     *provider.AssistantMessageEventStream
}

type responsesEventHandler func(ctx *responsesEventContext, chunk string)

var responsesEventHandlers = map[string]responsesEventHandler{
	"response.output_text.delta": handleResponsesTextDelta,
	"response.output_item.added": handleResponsesOutputItemAdded,
	"response.completed":         handleResponsesCompleted,
}

func handleResponsesTextDelta(ctx *responsesEventContext, chunk string) {
	var delta struct {
		Delta string `json:"delta"`
	}
	if err := json.Unmarshal([]byte(chunk), &delta); err != nil {
		ctx.decodeErr = fmt.Errorf("decode output_text.delta chunk: %w", err)
		return
	}
	if !ctx.started {
		ctx.started = true
		ctx.stream.Push(provider.AssistantMessageEvent{Type: provider.EventStart, Partial: &provider.AssistantMessage{}})
	}
	ctx.outputText += delta.Delta
	ctx.contentBuf += delta.Delta
	ctx.stream.Push(provider.AssistantMessageEvent{Type: provider.EventTextDelta, Delta: delta.Delta})
}

func handleResponsesOutputItemAdded(ctx *responsesEventContext, chunk string) {
	var item struct {
		Item struct {
			Type   string          `json:"type"`
			ID     string          `json:"id"`
			Name   string          `json:"name"`
			Status string          `json:"status"`
			Input  json.RawMessage `json:"input"`
		} `json:"item"`
	}
	if err := json.Unmarshal([]byte(chunk), &item); err != nil {
		ctx.decodeErr = fmt.Errorf("decode output_item.added chunk: %w", err)
		return
	}
	if item.Item.Type == "function_call" {
		if !ctx.started {
			ctx.started = true
			ctx.stream.Push(provider.AssistantMessageEvent{Type: provider.EventStart, Partial: &provider.AssistantMessage{}})
		}
		ctx.stream.Push(provider.AssistantMessageEvent{
			Type: provider.EventToolCallEnd,
			ToolCall: &provider.ContentBlock{
				Type:          provider.ContentBlockToolCall,
				ToolCallID:    item.Item.ID,
				ToolName:      item.Item.Name,
				ToolArguments: string(item.Item.Input),
			},
		})
	}
}

func handleResponsesCompleted(ctx *responsesEventContext, chunk string) {
	var resp struct {
		Response struct {
			Status string `json:"status"`
			Usage  struct {
				InputTokens  int `json:"input_tokens"`
				OutputTokens int `json:"output_tokens"`
			} `json:"usage"`
		} `json:"response"`
	}
	if err := json.Unmarshal([]byte(chunk), &resp); err != nil {
		ctx.decodeErr = fmt.Errorf("decode response.completed chunk: %w", err)
		return
	}
	var blocks []provider.ContentBlock
	if ctx.contentBuf != "" {
		blocks = append(blocks, provider.ContentBlock{Type: provider.ContentBlockText, Text: ctx.contentBuf})
	}
	stopReason := provider.StopReasonEndTurn
	if resp.Response.Status == "incomplete" {
		stopReason = provider.StopReasonMaxTokens
	}
	ctx.stream.End(&provider.AssistantMessage{
		Content:    blocks,
		StopReason: stopReason,
		Usage: &provider.Usage{
			InputTokens:  resp.Response.Usage.InputTokens,
			OutputTokens: resp.Response.Usage.OutputTokens,
		},
	})
	ctx.ended = true
}

func parseResponsesSSE(body io.ReadCloser, stream *provider.AssistantMessageEventStream) {
	parseResponsesSSEWithHook(body, stream, nil)
}

// parseResponsesSSEWithHook parses the Responses event stream, invoking hook
// for each raw chunk (rawChunk, endedOK=false) and once at the end
// ("", endedOK) reporting whether the stream completed cleanly. A nil hook
// behaves exactly like parseResponsesSSE.
func parseResponsesSSEWithHook(body io.ReadCloser, stream *provider.AssistantMessageEventStream, hook func(rawChunk string, endedOK bool)) {
	defer body.Close()

	ctx := &responsesEventContext{
		stream: stream,
	}

	sseErr := provider.ParseSSE(body, func(chunk string) {
		if hook != nil {
			hook(chunk, false)
		}
		var event struct {
			Type string           `json:"type"`
			Data *json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal([]byte(chunk), &event); err != nil {
			ctx.decodeErr = fmt.Errorf("decode responses event chunk: %w", err)
			return
		}

		if handler, ok := responsesEventHandlers[event.Type]; ok {
			handler(ctx, chunk)
		}
	})

	if sseErr != nil {
		stream.CloseWithError(fmt.Errorf("responses SSE error: %w", sseErr))
		return
	}
	if ctx.decodeErr != nil {
		stream.CloseWithError(fmt.Errorf("responses chunk decode failed: %w", ctx.decodeErr))
		return
	}
	if !ctx.ended {
		// No completion event arrived. If content was streamed, synthesize a
		// graceful end so consumers never block forever (mirrors AGENT-B3).
		var blocks []provider.ContentBlock
		if ctx.contentBuf != "" {
			blocks = append(blocks, provider.ContentBlock{Type: provider.ContentBlockText, Text: ctx.contentBuf})
		}
		stream.End(&provider.AssistantMessage{Content: blocks, StopReason: provider.StopReasonEndTurn})
	}
	if hook != nil {
		hook("", ctx.ended)
	}
}

// parseResponsesSSEWithBaseline behaves like parseResponsesSSE but, on a
// response.completed event, records the WS session baseline for sessionKey.
// The baseline is recorded the moment the completed chunk is captured — before
// the handler calls stream.End() — so a consumer that drains and immediately
// issues the next (chained) turn always observes it. lastInput is the
// already-deep-copied request input captured at send time; fingerprint is the
// property fingerprint of the request that opened the baseline. On any failure
// the baseline is left untouched (a failed request never advances it).
func parseResponsesSSEWithBaseline(body io.ReadCloser, stream *provider.AssistantMessageEventStream, sessionKey string, lastInput []provider.Message, fingerprint requestFingerprint) {
	cap := &baselineCapture{}
	parseResponsesSSEWithHook(body, stream, func(rawChunk string, endedOK bool) {
		if cap.capture(rawChunk) {
			recordWSBaseline(sessionKey, lastInput, cap.responseID, cap.addedItems, fingerprint)
		}
	})
}

// baselineCapture accumulates the response id + added output items from the
// response.completed chunk for the WS session baseline.
type baselineCapture struct {
	responseID string
	addedItems []provider.Message
}

// capture folds one raw event chunk into the accumulator, returning true
// when the chunk is a response.completed with a parseable response payload.
func (c *baselineCapture) capture(rawChunk string) bool {
	var event struct {
		Type string `json:"type"`
	}
	if json.Unmarshal([]byte(rawChunk), &event) != nil || event.Type != "response.completed" {
		return false
	}
	var resp struct {
		Response completedResponse `json:"response"`
	}
	if json.Unmarshal([]byte(rawChunk), &resp) != nil {
		return false
	}
	c.responseID = resp.Response.ID
	c.addedItems = resp.Response.toAddedItems()
	return true
}

// Azure OpenAI Responses
func streamAzureResponses(model provider.Model, ctx provider.Context, opts provider.StreamOptions) (*provider.AssistantMessageEventStream, error) {
	stream := provider.NewAssistantMessageEventStream(256)

	baseURL := model.BaseURL
	if baseURL == "" {
		return nil, fmt.Errorf("azure OpenAI requires endpoint URL in model.BaseURL")
	}

	apiKey := opts.APIKey
	if apiKey == "" {
		var err error
		apiKey, err = provider.GetEnvAPIKey(provider.ProviderAzure)
		if err != nil {
			return nil, err
		}
	}
	if apiKey == "" {
		return nil, &schema.MissingCredentialError{
			Provider: string(provider.ProviderAzure),
			Sources:  provider.EnvVarsForProvider(provider.ProviderAzure),
		}
	}

	body := buildResponsesBody(model, ctx, opts, "")
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx.GoContext(), "POST", baseURL+"/v1/responses", bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("api-key", apiKey)
	for k, v := range opts.Headers {
		req.Header.Set(k, v)
	}

	client := provider.NewStreamingHTTPClient()

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("azure request failed: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		bodyErr, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("azure returned %d: %s", resp.StatusCode, string(bodyErr))
	}

	go provider.CloseStreamOnCancel(ctx.GoContext(), stream)
	go parseResponsesSSE(resp.Body, stream)
	return stream, nil
}
