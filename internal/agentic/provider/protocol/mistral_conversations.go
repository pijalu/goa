// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package protocol

import (
	"encoding/json"
	"io"

	"github.com/pijalu/goa/internal/agentic/provider/schema"
)

func init() {
	Register(&mistralConversations{})
}

type mistralConversations struct{}

func (p *mistralConversations) API() schema.Api {
	return schema.ApiMistralConversations
}

func (p *mistralConversations) RequestHeaders(model schema.Model, profile schema.VariantProfile) map[string]string {
	headers := make(map[string]string)
	if profile.CachePolicy.AffinityHeader != "" {
		headers[profile.CachePolicy.AffinityHeader] = model.VariantID
	}
	return headers
}

func (p *mistralConversations) BuildRequest(model schema.Model, ctx schema.Context, opts schema.StreamOptions, profile schema.VariantProfile) ([]byte, error) {
	compat := openAICompletionsCompat{
		MaxTokensField: "max_tokens",
		ThinkingFormat: profile.Compat.ThinkingFormat,
	}
	messages := convertMessages(model, ctx.Messages, ctx.SystemPrompt, compat)
	tools := convertTools(ctx.Tools)

	body := map[string]any{
		"model":    model.ID,
		"stream":   true,
		"messages": messages,
	}
	if opts.MaxTokens > 0 {
		body["max_tokens"] = opts.MaxTokens
	}
	if opts.Temperature != nil {
		body["temperature"] = *opts.Temperature
	}
	if len(tools) > 0 {
		body["tools"] = tools
		if opts.ToolChoice != "" {
			body["tool_choice"] = opts.ToolChoice
		}
	}
	applyThinking(body, model, opts, schema.VariantProfile{Compat: schema.CompatFlags{ThinkingFormat: compat.ThinkingFormat}}, compat)
	return json.Marshal(body)
}

func (p *mistralConversations) ParseResponse(reader io.Reader, stream *schema.AssistantMessageEventStream) error {
	parseOpenAIStream(reader, stream)
	return nil
}
