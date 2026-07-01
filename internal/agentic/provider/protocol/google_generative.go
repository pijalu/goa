// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package protocol

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/pijalu/goa/internal/agentic/provider/schema"
	"github.com/pijalu/goa/internal/agentic/provider/transport"
)

func init() {
	Register(&googleGenerative{})
}

type googleGenerative struct{}

func (p *googleGenerative) API() schema.Api {
	return schema.ApiGoogleGenerativeAI
}

func (p *googleGenerative) RequestHeaders(model schema.Model, profile schema.VariantProfile) map[string]string {
	return nil
}

func (p *googleGenerative) BuildRequest(model schema.Model, ctx schema.Context, opts schema.StreamOptions, profile schema.VariantProfile) ([]byte, error) {
	contents := convertGoogleMessages(ctx.Messages, ctx.SystemPrompt)
	body := map[string]any{
		"contents":         contents,
		"generationConfig": map[string]any{},
	}
	if ctx.SystemPrompt != "" {
		body["systemInstruction"] = map[string]any{
			"parts": []map[string]any{{"text": ctx.SystemPrompt}},
			"role":  "user",
		}
	}
	genConfig := body["generationConfig"].(map[string]any)
	if opts.MaxTokens > 0 {
		genConfig["maxOutputTokens"] = opts.MaxTokens
	}
	if opts.Temperature != nil {
		genConfig["temperature"] = *opts.Temperature
	}
	if len(ctx.Tools) > 0 {
		body["tools"] = convertGoogleTools(ctx.Tools)
	}
	if model.Reasoning {
		budget := resolveThinkingBudget(profile)
		if budget > 0 {
			body["thinkingConfig"] = map[string]any{"thinkingBudget": budget}
		}
	}
	return json.Marshal(body)
}

func (p *googleGenerative) ParseResponse(reader io.Reader, stream *schema.AssistantMessageEventStream) error {
	parseGoogleSSE(reader, stream)
	return nil
}

func convertGoogleMessages(messages []schema.Message, systemPrompt string) []map[string]any {
	var contents []map[string]any
	for _, msg := range messages {
		role := "user"
		if msg.Role == schema.RoleAssistant {
			role = "model"
		}
		if msg.Role == schema.RoleToolResult {
			contents = append(contents, map[string]any{
				"role": "function",
				"parts": []map[string]any{{
					"functionResponse": map[string]any{
						"name": extractToolName(msg.Content),
						"response": map[string]any{
							"name":    extractToolName(msg.Content),
							"content": extractText(msg.Content),
						},
					},
				}},
			})
			continue
		}
		parts := convertGoogleParts(msg.Content, msg.Role)
		if len(parts) > 0 {
			contents = append(contents, map[string]any{"role": role, "parts": parts})
		}
	}
	return contents
}

func convertGoogleParts(blocks []schema.ContentBlock, role schema.Role) []map[string]any {
	var parts []map[string]any
	for _, b := range blocks {
		switch b.Type {
		case schema.ContentBlockText:
			parts = append(parts, map[string]any{"text": b.Text})
		case schema.ContentBlockImage:
			parts = append(parts, map[string]any{
				"inlineData": map[string]any{
					"mimeType": b.ImageMimeType,
					"data":     b.ImageData,
				},
			})
		case schema.ContentBlockToolCall:
			parts = append(parts, map[string]any{
				"functionCall": map[string]any{
					"name": b.ToolName,
					"args": json.RawMessage(b.ToolArguments),
				},
			})
		}
	}
	return parts
}

func convertGoogleTools(tools []schema.ToolSchema) []map[string]any {
	out := make([]map[string]any, len(tools))
	for i, t := range tools {
		out[i] = map[string]any{
			"functionDeclarations": []map[string]any{{
				"name":        t.Name,
				"description": t.Description,
				"parameters":  t.InputSchema,
			}},
		}
	}
	return out
}

func extractText(blocks []schema.ContentBlock) string {
	for _, b := range blocks {
		if b.Type == schema.ContentBlockText {
			return b.Text
		}
	}
	return ""
}

func extractToolName(blocks []schema.ContentBlock) string {
	for _, b := range blocks {
		if b.Type == schema.ContentBlockToolResult {
			return b.ToolName
		}
	}
	return ""
}

type googleStreamAcc struct {
	stream     *schema.AssistantMessageEventStream
	contentBuf strings.Builder
	funcCalls  []schema.ContentBlock
	started    bool
	ended      bool
}

type googleFuncCall struct {
	Name string          `json:"name"`
	Args json.RawMessage `json:"args"`
}

type googleCandidateResponse struct {
	Content struct {
		Parts []struct {
			Text         string          `json:"text"`
			FunctionCall *googleFuncCall `json:"functionCall"`
		} `json:"parts"`
	} `json:"content"`
	FinishReason string `json:"finishReason"`
}

func parseGoogleSSE(body io.Reader, stream *schema.AssistantMessageEventStream) {
	defer closeIfCloser(body)
	gacc := &googleStreamAcc{stream: stream}
	var lastErr error
	transport.ParseSSE(body, func(ev transport.SSEEvent) bool {
		candidates, cErr := parseGoogleResponse([]byte(ev.Data))
		if cErr != nil {
			lastErr = cErr
			return false
		}
		if len(candidates) > 0 {
			gacc.processCandidate(candidates[0])
		}
		return true
	})
	if lastErr != nil {
		stream.CloseWithError(fmt.Errorf("google chunk decode failed: %w", lastErr))
		return
	}
	gacc.endIfOpen()
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
	g.stream.Push(schema.AssistantMessageEvent{Type: schema.EventStart, Partial: &schema.AssistantMessage{}})
}

func (g *googleStreamAcc) processCandidate(candidate googleCandidateResponse) {
	for _, part := range candidate.Content.Parts {
		if part.Text != "" {
			g.ensureStart()
			g.contentBuf.WriteString(part.Text)
			g.stream.Push(schema.AssistantMessageEvent{Type: schema.EventTextDelta, Delta: part.Text})
		}
		if part.FunctionCall != nil {
			g.ensureStart()
			args := ""
			if part.FunctionCall.Args != nil {
				args = string(part.FunctionCall.Args)
			}
			g.funcCalls = append(g.funcCalls, schema.ContentBlock{
				Type:          schema.ContentBlockToolCall,
				ToolCallID:    fmt.Sprintf("fc_%s", part.FunctionCall.Name),
				ToolName:      part.FunctionCall.Name,
				ToolArguments: args,
			})
		}
	}
	if candidate.FinishReason != "" && candidate.FinishReason != "NULL" {
		s := g.contentBuf.String()
		var blocks []schema.ContentBlock
		if s != "" {
			blocks = append(blocks, schema.ContentBlock{Type: schema.ContentBlockText, Text: s})
		}
		blocks = append(blocks, g.funcCalls...)
		g.stream.End(&schema.AssistantMessage{Content: blocks, StopReason: mapGoogleStopReason(candidate.FinishReason)})
		g.ended = true
	}
}

func (g *googleStreamAcc) endIfOpen() {
	if g.ended {
		return
	}
	g.ended = true
	if g.started {
		var blocks []schema.ContentBlock
		if s := g.contentBuf.String(); s != "" {
			blocks = append(blocks, schema.ContentBlock{Type: schema.ContentBlockText, Text: s})
		}
		g.stream.End(&schema.AssistantMessage{Content: blocks, StopReason: schema.StopReasonEndTurn})
		return
	}
	g.stream.End(&schema.AssistantMessage{})
}

func mapGoogleStopReason(reason string) schema.StopReason {
	switch reason {
	case "MAX_TOKENS":
		return schema.StopReasonMaxTokens
	case "SAFETY", "BLOCKLIST", "PROHIBITED_CONTENT", "RECITATION":
		return schema.StopReasonContentFiltered
	case "OTHER":
		return schema.StopReasonError
	default:
		return schema.StopReasonEndTurn
	}
}
