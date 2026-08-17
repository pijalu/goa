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
//
// The request runs through the canonical StreamInterceptor chain
// (StreamInterceptors), so every protocol-backed call is observable and
// modifiable at the seam before the transport executes.
func GenericStream(model schema.Model, ctx schema.Context, opts schema.StreamOptions) (*schema.AssistantMessageEventStream, error) {
	return streamWithInterceptors(model, ctx, opts, StreamInterceptors()...)
}

// streamWithInterceptors is the generic streaming pipeline with an explicit
// interceptor chain (waterfall: first = outermost) applied around the terminal
// transport handler. GenericStream uses the canonical chain; tests and future
// consumers can pass an explicit chain to observe or modify a call.
func streamWithInterceptors(model schema.Model, ctx schema.Context, opts schema.StreamOptions, interceptors ...StreamInterceptor) (*schema.AssistantMessageEventStream, error) {
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
		Headers:  cloneStringMap(opts.Headers),
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

	req := &StreamRequest{
		Model:   model,
		Context: reqCtx.Context,
		Options: reqCtx.Options,
		Profile: profile,
		Headers: reqCtx.Headers,
		Body:    body,
		URL:     resolveURL(model, profile),
	}

	handler := ApplyStreamInterceptors(func(goCtx context.Context, r *StreamRequest) (*schema.AssistantMessageEventStream, error) {
		stream := schema.NewAssistantMessageEventStream(256)
		go CloseStreamOnCancel(goCtx, stream)
		go executeRequest(goCtx, r, pipeline, stream)
		return stream, nil
	}, interceptors...)
	return handler(ctx.GoContext(), req)
}

func applyEnvOverrides(profile schema.VariantProfile, model schema.Model) schema.VariantProfile {
	if model.BaseURL != "" {
		profile.Match.BaseURL = model.BaseURL
	}
	return profile
}

func cloneStringMap(src map[string]string) map[string]string {
	if len(src) == 0 {
		return make(map[string]string)
	}
	dst := make(map[string]string, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func executeRequest(
	goCtx context.Context,
	req *StreamRequest,
	pipeline *hooks.Pipeline,
	stream *schema.AssistantMessageEventStream,
) {
	url := req.URL
	if url == "" {
		if p := protocol.ForAPI(req.Model.Api); p != nil {
			_ = p.ParseResponse(bytes.NewReader(nil), stream)
		}
		return
	}

	tr := selectTransport(req.Options)

	tReq := &transport.TransportRequest{
		Method:  "POST",
		URL:     url,
		Headers: req.Headers,
		Body:    req.Body,
	}
	if timeout := req.Options.Timeout; timeout > 0 {
		tReq.Timeout = int64(timeout / time.Millisecond)
	}
	if req.Options.Transport == schema.TransportWebSocket {
		tReq.Headers["X-Session-ID"] = req.Options.SessionID
	}

	resp, err := tr.Do(goCtx, tReq)
	if err != nil {
		errCtx := &hooks.ErrorContext{
			Model:      req.Model,
			Profile:    req.Profile,
			Err:        err,
			StatusCode: 0,
		}
		_ = pipeline.ApplyError(errCtx)
		stream.CloseWithError(errCtx.ToError())
		return
	}

	if req.OnResponse != nil {
		req.OnResponse(resp.StatusCode, resp.Headers)
	}

	if resp.StatusCode >= 400 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		errCtx := &hooks.ErrorContext{
			Model:      req.Model,
			Profile:    req.Profile,
			StatusCode: resp.StatusCode,
			Body:       string(bodyBytes),
			Headers:    resp.Headers,
		}
		_ = pipeline.ApplyError(errCtx)
		stream.CloseWithError(errCtx.ToError())
		return
	}

	reader := resp.Body
	if req.Options.Transport != schema.TransportWebSocket {
		idleTimeout := req.Options.IdleTimeout
		if idleTimeout <= 0 {
			idleTimeout = DefaultStreamIdleTimeout
		}
		reader = NewIdleTimeoutReader(reader, idleTimeout)
	}

	if p := protocol.ForAPI(req.Model.Api); p != nil {
		if err := p.ParseResponse(reader, stream); err != nil {
			stream.CloseWithError(err)
			return
		}
	}
}

// streamResultUsage returns the final usage of a terminated stream, or nil
// when the stream produced no result (transport/HTTP failures). It is only
// called once the stream is closed, so Result never blocks.
func streamResultUsage(stream *schema.AssistantMessageEventStream) *schema.Usage {
	res := stream.Result()
	if res == nil {
		return nil
	}
	return res.Usage
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
	if model.Api == schema.ApiOpenAICodexResponses {
		baseURL = codexResponsesURL(baseURL)
	}
	return schema.ResolveURLTemplate(baseURL)
}

// codexResponsesURL normalizes a Codex base URL to the responses endpoint,
// mirroring Pi's resolveCodexUrl: the ChatGPT subscription transport serves
// the responses API at {base}/codex/responses (the base
// https://chatgpt.com/backend-api has no bare responses route and 404s).
func codexResponsesURL(baseURL string) string {
	u := strings.TrimRight(baseURL, "/")
	if strings.HasSuffix(u, "/codex/responses") {
		return u
	}
	if strings.HasSuffix(u, "/codex") {
		return u + "/responses"
	}
	return u + "/codex/responses"
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
