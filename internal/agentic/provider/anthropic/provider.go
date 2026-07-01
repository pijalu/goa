// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package anthropic

import (
	"github.com/pijalu/goa/internal/agentic/provider"
)

func init() {
	provider.RegisterApiProvider(&AnthropicProvider{})
}

type AnthropicProvider struct{}

func (p *AnthropicProvider) API() provider.Api { return provider.ApiAnthropicMessages }

func (p *AnthropicProvider) Stream(model provider.Model, ctx provider.Context, opts provider.StreamOptions) (*provider.AssistantMessageEventStream, error) {
	return streamAnthropic(model, ctx, opts)
}

func (p *AnthropicProvider) StreamSimple(model provider.Model, ctx provider.Context, opts provider.SimpleStreamOptions) (*provider.AssistantMessageEventStream, error) {
	base := provider.BuildSimpleOptions(model, opts)
	return p.Stream(model, ctx, base)
}
