// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package provider

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/pijalu/goa/internal/agentic/provider/hooks"
	"github.com/pijalu/goa/internal/agentic/provider/protocol"
	"github.com/pijalu/goa/internal/agentic/provider/schema"
	"github.com/pijalu/goa/internal/agentic/provider/transport"
)

// GenericStream initiates a streaming LLM request using the generic provider
// pipeline. It resolves the variant profile, runs the hook pipeline, builds the
// wire request, executes it via the transport, and parses the response.
func GenericStream(model schema.Model, ctx schema.Context, opts schema.StreamOptions) (*schema.AssistantMessageEventStream, error) {
	profile := schema.ResolveProfile(model)
	profile = applyEnvOverrides(profile, model)

	pipeline := hooks.BuildPipeline(model)
	if err := pipeline.Init(profile); err != nil {
		return nil, fmt.Errorf("init pipeline: %w", err)
	}

	reqCtx := &hooks.RequestContext{
		Model:    model,
		Context:  ctx,
		Options:  opts,
		Profile:  profile,
		Headers:  make(map[string]string),
		Pipeline: pipeline,
	}
	if err := pipeline.ApplyRequest(reqCtx); err != nil {
		return nil, fmt.Errorf("apply request hooks: %w", err)
	}

	p := protocol.ForAPI(model.Api)
	if p == nil {
		return nil, fmt.Errorf("no protocol registered for API %q", model.Api)
	}

	body, err := p.BuildRequest(model, reqCtx.Context, reqCtx.Options, profile)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}

	for k, v := range p.RequestHeaders(model, profile) {
		reqCtx.Headers[k] = v
	}

	stream := schema.NewAssistantMessageEventStream(256)
	go CloseStreamOnCancel(ctx.GoContext(), stream)
	go executeRequest(ctx.GoContext(), p, reqCtx, body, stream, profile)

	return stream, nil
}

func applyEnvOverrides(profile schema.VariantProfile, model schema.Model) schema.VariantProfile {
	if model.BaseURL != "" {
		profile.Match.BaseURL = model.BaseURL
	}
	return profile
}

func executeRequest(
	goCtx context.Context,
	p protocol.Protocol,
	reqCtx *hooks.RequestContext,
	body []byte,
	stream *schema.AssistantMessageEventStream,
	profile schema.VariantProfile,
) {
	url := resolveURL(reqCtx.Model, profile)
	if url == "" {
		_ = p.ParseResponse(bytes.NewReader(nil), stream)
		return
	}

	tr := selectTransport(reqCtx.Options)

	req := &transport.TransportRequest{
		Method:  "POST",
		URL:     url,
		Headers: reqCtx.Headers,
		Body:    body,
	}
	if timeout := reqCtx.Options.Timeout; timeout > 0 {
		req.Timeout = int64(timeout / time.Millisecond)
	}
	if reqCtx.Options.Transport == schema.TransportWebSocket {
		req.Headers["X-Session-ID"] = reqCtx.Options.SessionID
	}

	resp, err := tr.Do(goCtx, req)
	if err != nil {
		errCtx := &hooks.ErrorContext{
			Model:      reqCtx.Model,
			Profile:    profile,
			Err:        err,
			StatusCode: 0,
		}
		_ = reqCtx.Pipeline.ApplyError(errCtx)
		stream.CloseWithError(errCtx.ToError())
		return
	}

	if reqCtx.Options.OnResponse != nil {
		reqCtx.Options.OnResponse(resp.StatusCode, resp.Headers)
	}

	if resp.StatusCode >= 400 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		errCtx := &hooks.ErrorContext{
			Model:      reqCtx.Model,
			Profile:    profile,
			StatusCode: resp.StatusCode,
			Body:       string(bodyBytes),
			Headers:    resp.Headers,
		}
		_ = reqCtx.Pipeline.ApplyError(errCtx)
		stream.CloseWithError(errCtx.ToError())
		return
	}

	reader := resp.Body
	if reqCtx.Options.Transport != schema.TransportWebSocket {
		idleTimeout := reqCtx.Options.IdleTimeout
		if idleTimeout <= 0 {
			idleTimeout = DefaultStreamIdleTimeout
		}
		reader = NewIdleTimeoutReader(reader, idleTimeout)
	}

	if err := p.ParseResponse(reader, stream); err != nil {
		stream.CloseWithError(err)
		return
	}
}

func selectTransport(opts schema.StreamOptions) transport.Transport {
	if opts.Transport == schema.TransportWebSocket {
		return &transport.WebSocketTransport{HeaderTimeout: 20 * time.Second}
	}
	return transport.Default()
}

func resolveURL(model schema.Model, profile schema.VariantProfile) string {
	baseURL := model.BaseURL
	if baseURL == "" {
		baseURL = defaultBaseURL(model, profile)
	}
	if baseURL == "" {
		return ""
	}
	return schema.ResolveURLTemplate(baseURL)
}

func defaultBaseURL(model schema.Model, profile schema.VariantProfile) string {
	switch model.Api {
	case schema.ApiOpenAICompletions:
		if isLocalProvider(model.Provider, model.BaseURL) {
			return ""
		}
		return "https://api.openai.com/v1/chat/completions"
	case schema.ApiOpenAIResponses:
		return "https://api.openai.com/v1/responses"
	case schema.ApiOpenAICodexResponses:
		return "https://api.openai.com/v1/responses/codex"
	case schema.ApiAzureOpenAIResponses:
		return ""
	case schema.ApiAnthropicMessages:
		return "https://api.anthropic.com/v1/messages"
	case schema.ApiGoogleGenerativeAI:
		return "https://generativelanguage.googleapis.com/v1beta/models/" + model.ID + ":streamGenerateContent?alt=sse&key={GOOGLE_API_KEY}"
	case schema.ApiGoogleVertex:
		return ""
	case schema.ApiMistralConversations:
		return "https://api.mistral.ai/v1/chat/completions"
	case schema.ApiBedrockConverse:
		return ""
	}
	if profile.Match.BaseURL != "" {
		return profile.Match.BaseURL
	}
	return ""
}

func isLocalProvider(prov schema.Provider, baseURL string) bool {
	p := strings.ToLower(string(prov))
	u := strings.ToLower(baseURL)
	return p == "lm-studio" || p == "ollama" ||
		strings.Contains(u, "localhost:1234") || strings.Contains(u, "127.0.0.1:1234") ||
		strings.Contains(u, "localhost:11434") || strings.Contains(u, "127.0.0.1:11434")
}
