// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package google

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/pijalu/goa/internal/agentic/provider"
)

func init() {
	provider.RegisterApiProvider(&GoogleProvider{})
	provider.RegisterApiProvider(&VertexProvider{})
}

type GoogleProvider struct{}

func (p *GoogleProvider) API() provider.Api { return provider.ApiGoogleGenerativeAI }

func (p *GoogleProvider) Stream(model provider.Model, ctx provider.Context, opts provider.StreamOptions) (*provider.AssistantMessageEventStream, error) {
	return streamGoogle(model, ctx, opts)
}

func (p *GoogleProvider) StreamSimple(model provider.Model, ctx provider.Context, opts provider.SimpleStreamOptions) (*provider.AssistantMessageEventStream, error) {
	base := provider.BuildSimpleOptions(model, opts)
	return p.Stream(model, ctx, base)
}

type VertexProvider struct{}

func (p *VertexProvider) API() provider.Api { return provider.ApiGoogleVertex }

func (p *VertexProvider) Stream(model provider.Model, ctx provider.Context, opts provider.StreamOptions) (*provider.AssistantMessageEventStream, error) {
	// Vertex AI uses the same GenAI API but with ADC auth and different endpoint.
	return streamVertex(model, ctx, opts)
}

func (p *VertexProvider) StreamSimple(model provider.Model, ctx provider.Context, opts provider.SimpleStreamOptions) (*provider.AssistantMessageEventStream, error) {
	base := provider.BuildSimpleOptions(model, opts)
	return p.Stream(model, ctx, base)
}

// ---------------------------------------------------------------------------
// Google Generative AI (api.generativeai.google.com)
// ---------------------------------------------------------------------------

func streamGoogle(model provider.Model, ctx provider.Context, opts provider.StreamOptions) (*provider.AssistantMessageEventStream, error) {
	stream := provider.NewAssistantMessageEventStream(256)

	apiKey := opts.APIKey
	if apiKey == "" {
		apiKey = provider.GetEnvAPIKey(provider.ProviderGoogle)
	}
	if apiKey == "" {
		return nil, fmt.Errorf("google API key required: set GEMINI_API_KEY or GOOGLE_API_KEY")
	}

	modelID := model.ID
	if modelID == "" {
		modelID = "gemini-2.0-flash"
	}

	endpoint := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/%s:streamGenerateContent?alt=sse&key=%s", modelID, apiKey)

	body := buildGoogleBody(model, ctx, opts)
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx.GoContext(), "POST", endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range opts.Headers {
		req.Header.Set(k, v)
	}

	client := provider.NewStreamingHTTPClient()

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		bodyErr, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("google returned %d: %s", resp.StatusCode, string(bodyErr))
	}

	go provider.CloseStreamOnCancel(ctx.GoContext(), stream)
	go parseGoogleSSE(resp.Body, stream)
	return stream, nil
}

func buildGoogleBody(model provider.Model, ctx provider.Context, opts provider.StreamOptions) map[string]interface{} {
	contents := convertGoogleMessages(ctx.Messages, ctx.SystemPrompt)

	body := map[string]interface{}{
		"contents":         contents,
		"generationConfig": map[string]interface{}{},
	}

	if ctx.SystemPrompt != "" {
		body["systemInstruction"] = map[string]interface{}{
			"parts": []map[string]interface{}{{"text": ctx.SystemPrompt}},
			"role":  "user",
		}
	}

	genConfig := body["generationConfig"].(map[string]interface{})
	if opts.MaxTokens > 0 {
		genConfig["maxOutputTokens"] = opts.MaxTokens
	}
	if opts.Temperature != nil {
		genConfig["temperature"] = *opts.Temperature
	}

	if len(ctx.Tools) > 0 {
		body["tools"] = convertGoogleTools(ctx.Tools)
	}

	return body
}

func convertGoogleMessages(messages []provider.Message, systemPrompt string) []map[string]interface{} {
	var contents []map[string]interface{}

	for _, msg := range messages {
		role := "user"
		if msg.Role == provider.RoleAssistant {
			role = "model"
		}
		if msg.Role == provider.RoleToolResult {
			// Function response - sent as "function" role
			contents = append(contents, map[string]interface{}{
				"role": "function",
				"parts": []map[string]interface{}{
					{
						"functionResponse": map[string]interface{}{
							"name": extractToolName(msg.Content),
							"response": map[string]interface{}{
								"name":    extractToolName(msg.Content),
								"content": extractText(msg.Content),
							},
						},
					},
				},
			})
			continue
		}

		parts := convertParts(msg.Content, msg.Role)
		if len(parts) > 0 {
			contents = append(contents, map[string]interface{}{
				"role":  role,
				"parts": parts,
			})
		}
	}

	return contents
}

func convertParts(blocks []provider.ContentBlock, role provider.Role) []map[string]interface{} {
	var parts []map[string]interface{}
	for _, b := range blocks {
		switch b.Type {
		case provider.ContentBlockText:
			parts = append(parts, map[string]interface{}{"text": b.Text})
		case provider.ContentBlockImage:
			parts = append(parts, map[string]interface{}{
				"inlineData": map[string]interface{}{
					"mimeType": b.ImageMimeType,
					"data":     b.ImageData,
				},
			})
		case provider.ContentBlockToolCall:
			parts = append(parts, map[string]interface{}{
				"functionCall": map[string]interface{}{
					"name": b.ToolName,
					"args": json.RawMessage(b.ToolArguments),
				},
			})
		}
	}
	return parts
}

func convertGoogleTools(tools []provider.ToolSchema) []map[string]interface{} {
	out := make([]map[string]interface{}, len(tools))
	for i, t := range tools {
		out[i] = map[string]interface{}{
			"functionDeclarations": []map[string]interface{}{
				{
					"name":        t.Name,
					"description": t.Description,
					"parameters":  t.InputSchema,
				},
			},
		}
	}
	return out
}

func extractText(blocks []provider.ContentBlock) string {
	for _, b := range blocks {
		if b.Type == provider.ContentBlockText {
			return b.Text
		}
	}
	return ""
}

func extractToolName(blocks []provider.ContentBlock) string {
	for _, b := range blocks {
		if b.Type == provider.ContentBlockToolResult {
			return b.ToolName
		}
	}
	return ""
}

// parseGoogleSSE reads Google's streaming format.
// Google returns SSE events with `data: {"candidates": [...], ...}`
func parseGoogleSSE(body io.ReadCloser, stream *provider.AssistantMessageEventStream) {
	defer body.Close()

	gacc := &googleStreamAcc{stream: stream}
	var lastErr error

	err := provider.ParseSSE(body, func(chunk string) {
		candidates, cErr := parseGoogleResponse([]byte(chunk))
		if cErr != nil {
			lastErr = cErr
			return
		}
		if len(candidates) == 0 {
			return
		}
		gacc.processCandidate(candidates[0])
	})
	if err != nil {
		stream.CloseWithError(fmt.Errorf("google SSE error: %w", err))
		return
	}
	if lastErr != nil {
		stream.CloseWithError(fmt.Errorf("google chunk decode failed: %w", lastErr))
		return
	}

	// Always terminate the stream. processCandidate may have already called
	// stream.End when a FinishReason was observed (End is idempotent via
	// terminate()). If content was streamed but the connection closed without
	// a FinishReason, synthesize a graceful end so the consumer never blocks
	// forever (see AGENT-B3).
	gacc.endIfOpen()
}

type googleStreamAcc struct {
	stream     *provider.AssistantMessageEventStream
	contentBuf strings.Builder
	started    bool
	ended      bool
}

type googleFuncCall struct {
	Name string          `json:"name"`
	Args json.RawMessage `json:"args"`
}

// googleCandidateResponse wraps a Google candidate from the streaming response.
// The FinishReason is a top-level field on the candidate, while parts are
// nested under content.
type googleCandidateResponse struct {
	Content struct {
		Parts []struct {
			Text         string          `json:"text"`
			FunctionCall *googleFuncCall `json:"functionCall"`
		} `json:"parts"`
	} `json:"content"`
	FinishReason string `json:"finishReason"`
}

func parseGoogleResponse(raw []byte) ([]googleCandidateResponse, error) {
	var resp struct {
		Candidates []googleCandidateResponse `json:"candidates"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("decode google chunk: %w", err)
	}
	return resp.Candidates, nil
}

func (g *googleStreamAcc) ensureStart() {
	if g.started {
		return
	}
	g.started = true
	g.stream.Push(provider.AssistantMessageEvent{Type: provider.EventStart, Partial: &provider.AssistantMessage{}})
}

func (g *googleStreamAcc) processCandidate(candidate googleCandidateResponse) {
	for _, part := range candidate.Content.Parts {
		if part.Text != "" {
			g.ensureStart()
			g.contentBuf.WriteString(part.Text)
			g.stream.Push(provider.AssistantMessageEvent{Type: provider.EventTextDelta, Delta: part.Text})
		}
		if part.FunctionCall != nil {
			g.ensureStart()
			args := ""
			if part.FunctionCall.Args != nil {
				args = string(part.FunctionCall.Args)
			}
			g.stream.Push(provider.AssistantMessageEvent{
				Type: provider.EventToolCallEnd,
				ToolCall: &provider.ContentBlock{
					Type:          provider.ContentBlockToolCall,
					ToolCallID:    fmt.Sprintf("fc_%s", part.FunctionCall.Name),
					ToolName:      part.FunctionCall.Name,
					ToolArguments: args,
				},
			})
		}
	}

	if candidate.FinishReason != "" && candidate.FinishReason != "NULL" {
		s := g.contentBuf.String()
		var blocks []provider.ContentBlock
		if s != "" {
			blocks = append(blocks, provider.ContentBlock{Type: provider.ContentBlockText, Text: s})
		}
		g.stream.End(&provider.AssistantMessage{
			Content: blocks, StopReason: mapGoogleStopReason(candidate.FinishReason),
		})
		g.ended = true
	}
}

// endIfOpen terminates the stream if processCandidate did not already do so.
// If content was streamed without a FinishReason, synthesize a graceful end
// (StopReasonEndTurn) so the consumer never blocks forever. End is idempotent.
func (g *googleStreamAcc) endIfOpen() {
	if g.ended {
		return
	}
	g.ended = true
	if g.started {
		var blocks []provider.ContentBlock
		if s := g.contentBuf.String(); s != "" {
			blocks = append(blocks, provider.ContentBlock{Type: provider.ContentBlockText, Text: s})
		}
		g.stream.End(&provider.AssistantMessage{
			Content:    blocks,
			StopReason: provider.StopReasonEndTurn,
		})
		return
	}
	g.stream.End(&provider.AssistantMessage{})
}

func mapGoogleStopReason(reason string) provider.StopReason {
	switch reason {
	case "STOP":
		return provider.StopReasonEndTurn
	case "MAX_TOKENS":
		return provider.StopReasonMaxTokens
	case "SAFETY", "BLOCKLIST", "PROHIBITED_CONTENT":
		return provider.StopReasonContentFiltered
	case "RECITATION":
		return provider.StopReasonContentFiltered
	case "OTHER":
		return provider.StopReasonError
	default:
		return provider.StopReasonEndTurn
	}
}

// ---------------------------------------------------------------------------
// Google Vertex AI
// ---------------------------------------------------------------------------

func streamVertex(model provider.Model, ctx provider.Context, opts provider.StreamOptions) (*provider.AssistantMessageEventStream, error) {
	endpoint, err := buildVertexEndpoint(model, opts)
	if err != nil {
		return nil, err
	}

	req, err := buildVertexRequest(ctx, model, opts, endpoint)
	if err != nil {
		return nil, err
	}

	stream := provider.NewAssistantMessageEventStream(256)
	client := provider.NewStreamingHTTPClient()

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("vertex request failed: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		bodyErr, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("vertex returned %d: %s", resp.StatusCode, string(bodyErr))
	}

	go provider.CloseStreamOnCancel(ctx.GoContext(), stream)
	go parseGoogleSSE(resp.Body, stream)
	return stream, nil
}

func buildVertexEndpoint(model provider.Model, opts provider.StreamOptions) (string, error) {
	project := opts.Headers["x-vertex-project"]
	location := opts.Headers["x-vertex-location"]
	if location == "" {
		location = "us-central1"
	}
	if project == "" {
		return "", fmt.Errorf("google Vertex AI requires project: set GOOGLE_CLOUD_PROJECT or x-vertex-project header")
	}

	modelID := model.ID
	if modelID == "" {
		modelID = "gemini-2.0-flash"
	}

	return fmt.Sprintf("https://%s-aiplatform.googleapis.com/v1/projects/%s/locations/%s/publishers/google/models/%s:streamGenerateContent",
		location, project, location, modelID), nil
}

func buildVertexRequest(ctx provider.Context, model provider.Model, opts provider.StreamOptions, endpoint string) (*http.Request, error) {
	token := opts.APIKey
	if token == "" {
		token = getGoogleAccessToken()
	}

	body := buildGoogleBody(model, ctx, opts)
	body["safetySettings"] = vertexSafetySettings()

	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx.GoContext(), "POST", endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for k, v := range opts.Headers {
		if k != "x-vertex-project" && k != "x-vertex-location" {
			req.Header.Set(k, v)
		}
	}
	return req, nil
}

func vertexSafetySettings() []map[string]interface{} {
	return []map[string]interface{}{
		{"category": "HARM_CATEGORY_HARASSMENT", "threshold": "BLOCK_ONLY_HIGH"},
		{"category": "HARM_CATEGORY_HATE_SPEECH", "threshold": "BLOCK_ONLY_HIGH"},
		{"category": "HARM_CATEGORY_SEXUALLY_EXPLICIT", "threshold": "BLOCK_ONLY_HIGH"},
		{"category": "HARM_CATEGORY_DANGEROUS_CONTENT", "threshold": "BLOCK_ONLY_HIGH"},
	}
}

// getGoogleAccessToken retrieves a GCP access token from the metadata server
// or environment variable.
func getGoogleAccessToken() string {
	// Check env first
	if token := provider.GetEnvAPIKey("google"); token != "" {
		return token
	}

	// Try GCP metadata server
	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequest("GET", "http://metadata.google.internal/computeMetadata/v1/instance/service-accounts/default/token?scopes=https://www.googleapis.com/auth/cloud-platform", nil)
	if err != nil {
		return ""
	}
	req.Header.Set("Metadata-Flavor", "Google")

	resp, err := client.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return ""
	}

	body, _ := io.ReadAll(resp.Body)
	var tokenResp struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return ""
	}
	return tokenResp.AccessToken
}
