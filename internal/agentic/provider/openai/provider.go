// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package openai

import (
	"fmt"
	"net/http"

	"github.com/pijalu/goa/internal/agentic/provider"
)

// OpenAIProvider implements provider.ApiProvider for OpenAI-compatible completions APIs.
type OpenAIProvider struct{}

func (p *OpenAIProvider) API() provider.Api {
	return provider.ApiOpenAICompletions
}

func (p *OpenAIProvider) Stream(model provider.Model, ctx provider.Context, opts provider.StreamOptions) (*provider.AssistantMessageEventStream, error) {
	compat := provider.ResolveOpenAICompat(model)
	return streamOpenAICompletions(model, ctx, opts, compat)
}

func (p *OpenAIProvider) StreamSimple(model provider.Model, ctx provider.Context, opts provider.SimpleStreamOptions) (*provider.AssistantMessageEventStream, error) {
	base := provider.BuildSimpleOptions(model, opts)
	return p.Stream(model, ctx, base)
}

func init() {
	provider.RegisterApiProvider(&OpenAIProvider{})
}

// ---------------------------------------------------------------------------
// HTTP client helpers
// ---------------------------------------------------------------------------

func buildHTTPClient() *http.Client {
	return provider.NewStreamingHTTPClient()
}

// ---------------------------------------------------------------------------
// Error helpers
// ---------------------------------------------------------------------------

type errResponse struct {
	Status int
	Body   string
}

func (e *errResponse) Error() string {
	return fmt.Sprintf("LLM endpoint returned %d: %s", e.Status, e.Body)
}

func (e *errResponse) StatusCode() int    { return e.Status }
func (e *errResponse) ResponseBody() string { return e.Body }
