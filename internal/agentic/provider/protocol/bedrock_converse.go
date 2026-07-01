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
	Register(&bedrockConverse{})
}

type bedrockConverse struct{}

func (p *bedrockConverse) API() schema.Api {
	return schema.ApiBedrockConverse
}

func (p *bedrockConverse) RequestHeaders(model schema.Model, profile schema.VariantProfile) map[string]string {
	return nil
}

func (p *bedrockConverse) BuildRequest(model schema.Model, ctx schema.Context, opts schema.StreamOptions, profile schema.VariantProfile) ([]byte, error) {
	compat := openAICompletionsCompat{
		MaxTokensField: "max_tokens",
		ThinkingFormat: profile.Compat.ThinkingFormat,
	}
	messages := convertMessages(model, ctx.Messages, ctx.SystemPrompt, compat)
	body := map[string]any{
		"modelId":  model.ID,
		"messages": messages,
	}
	if opts.MaxTokens > 0 {
		body["inferenceConfig"] = map[string]any{"maxTokens": opts.MaxTokens}
	}
	if opts.Temperature != nil {
		if cfg, ok := body["inferenceConfig"].(map[string]any); ok {
			cfg["temperature"] = *opts.Temperature
		} else {
			body["inferenceConfig"] = map[string]any{"temperature": *opts.Temperature}
		}
	}
	return json.Marshal(body)
}

func (p *bedrockConverse) ParseResponse(reader io.Reader, stream *schema.AssistantMessageEventStream) error {
	parseOpenAIStream(reader, stream)
	return nil
}
