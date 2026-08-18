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
	stream := provider.NewAssistantMessageEventStream(256)

	baseURL := model.BaseURL
	if baseURL == "" {
		switch flavor {
		case "codex":
			baseURL = codexBaseURL(opts)
		default:
			baseURL = "https://api.openai.com/v1/responses"
		}
	}

	body := buildResponsesBody(model, ctx, opts, flavor)
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	useWebSocket := opts.Transport == provider.TransportWebSocket
	if useWebSocket {
		return streamResponsesWebSocket(model, ctx, opts, bodyBytes)
	}

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
	if opts.MaxTokens > 0 {
		body["max_output_tokens"] = opts.MaxTokens
	}
	if opts.Temperature != nil {
		body["temperature"] = *opts.Temperature
	}
	cacheKey := opts.PromptCacheKey
	if cacheKey == "" {
		cacheKey = opts.SessionID
	}
	if opts.SessionID != "" || cacheKey != "" {
		// Codex carries session affinity via prompt_cache_key only; the SSE
		// backend rejects previous_response_id (HTTP 400). Other responses
		// flavors chain turns via previous_response_id.
		if isCodex {
			body["prompt_cache_key"] = cacheKey
		} else if opts.SessionID != "" {
			body["previous_response_id"] = opts.SessionID
		}
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
	defer body.Close()

	ctx := &responsesEventContext{
		stream: stream,
	}

	sseErr := provider.ParseSSE(body, func(chunk string) {
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
}

// WebSocket streaming for OpenAI Responses API
func streamResponsesWebSocket(model provider.Model, ctx provider.Context, opts provider.StreamOptions, bodyBytes []byte) (*provider.AssistantMessageEventStream, error) {
	return nil, fmt.Errorf("OpenAI Responses WebSocket: not yet implemented (needs gorilla/websocket)")
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
