// SPDX-License-Identifier: GPL-3.0-or-later

package provider

import (
	"context"
	"strings"
	"time"

	"github.com/pijalu/goa/internal/agentic/provider/schema"
)

// probeTimeout bounds one wire-format probe request. The failing request it
// follows already burned ~0.5s, so probes stay cheap: a gateway answers a
// format mismatch in well under a second, and a healthy surface streams its
// first event quickly. 15s only caps pathological hangs.
const probeTimeout = 15 * time.Second

// probeMaxTokens keeps probe generations minimal. 16 satisfies the Responses
// surface's minimum max_output_tokens on strict upstreams.
const probeMaxTokens = 16

// probeSurfaces lists, in preference order, the wire surfaces a multi-surface
// gateway may serve a model on. Today only the OpenCode Zen/Go gateways are
// known to behave this way (generic HTTP 500 on a wire-format mismatch —
// handler.ts throws instead of converting). Order matters only when several
// surfaces accept the same model; the first 200 wins, deterministically.
var probeSurfaces = []schema.Api{
	schema.ApiOpenAICompletions,
	schema.ApiAnthropicMessages,
	schema.ApiOpenAIResponses,
}

// probeSurfacePaths maps a wire API to the route appended to the gateway base
// URL, mirroring modelEndpointURL in the outer provider package.
func probeSurfacePath(api schema.Api) string {
	switch api {
	case schema.ApiOpenAICompletions:
		return "/chat/completions"
	case schema.ApiAnthropicMessages:
		return "/messages"
	case schema.ApiOpenAIResponses:
		return "/responses"
	}
	return ""
}

// ProbeWireFormatCandidate is one alternative wire surface to try.
type ProbeWireFormatCandidate struct {
	Api     schema.Api
	BaseURL string
}

// ProbeWireFormatCandidates returns the alternative surfaces worth probing
// for model, or nil when probing does not apply:
//   - the model must terminate at a known multi-surface gateway (OpenCode
//     Zen/Go — the only gateways that 500 on wire-format mismatch);
//   - its wire API must be unpinned (ApiSource "": no curated override, no
//     explicit user `api:` — those choices are authoritative);
//   - its BaseURL must carry a known surface route to swap against.
//
// The current (just-failed) surface is excluded: re-probing it would only
// repeat the failure the caller is recovering from.
func ProbeWireFormatCandidates(model schema.Model) []ProbeWireFormatCandidate {
	if model.ApiSource != "" {
		return nil
	}
	if !isMultiSurfaceGateway(model) {
		return nil
	}
	base := probeGatewayBase(model.BaseURL)
	if base == "" {
		return nil
	}
	var out []ProbeWireFormatCandidate
	for _, api := range probeSurfaces {
		if api == model.Api {
			continue
		}
		out = append(out, ProbeWireFormatCandidate{Api: api, BaseURL: base + probeSurfacePath(api)})
	}
	return out
}

// isMultiSurfaceGateway reports whether the model's traffic terminates at a
// gateway known to serve several wire formats under one provider identity.
// Mirrors hooks.isOpenCodeGateway (kept separate to avoid a provider→hooks
// dependency for a pure schema predicate).
func isMultiSurfaceGateway(model schema.Model) bool {
	if model.Provider == schema.ProviderOpenCode || model.Provider == schema.ProviderOpenCodeGo {
		return true
	}
	def := schema.MatchProviderByNameOrURL(model.Provider, model.BaseURL)
	if def == nil {
		return false
	}
	return def.Provider == schema.ProviderOpenCode || def.Provider == schema.ProviderOpenCodeGo
}

// probeGatewayBase strips a known surface route off a full wire URL, leaving
// the gateway base (e.g. "https://opencode.ai/zen/go/v1"). Returns "" when
// the URL carries no known surface route.
func probeGatewayBase(baseURL string) string {
	u := strings.TrimRight(baseURL, "/")
	for _, api := range probeSurfaces {
		if suffix := probeSurfacePath(api); suffix != "" && strings.HasSuffix(u, suffix) {
			return strings.TrimSuffix(u, suffix)
		}
	}
	return ""
}

// ProbeWireFormat tries each candidate surface with a minimal real request
// through the normal streaming runtime — so per-API auth (Bearer vs
// x-api-key), session-affinity headers (x-opencode-session), and profile
// compat quirks are applied exactly as for a conversation request. The first
// surface whose stream OPENS (HTTP 2xx) wins: a gateway that 500s on format
// mismatch fails at request time, so an opened stream is definitive proof the
// surface accepts this model's wire format. On success it returns the model
// rerouted to the winning surface (Api + BaseURL swapped, everything else
// preserved). When every candidate fails it returns ok=false and the caller
// falls through to the normal retry path.
//
// Positive and negative outcomes are both cheap: candidates are tried
// sequentially, each bounded by probeTimeout, and the winning stream is
// canceled after the first event so no full generation is billed.
func ProbeWireFormat(ctx context.Context, model schema.Model, opts schema.StreamOptions, candidates []ProbeWireFormatCandidate) (schema.Model, bool) {
	for _, cand := range candidates {
		probeModel := model
		probeModel.Api = cand.Api
		probeModel.BaseURL = cand.BaseURL
		if probeSurfaceOpens(ctx, probeModel, opts) {
			return probeModel, true
		}
	}
	return model, false
}

// probeSurfaceOpens issues one minimal streaming request and reports whether
// the surface accepted it: the stream opened (no open-time error) and either
// produced a first event or closed cleanly. An error event carrying an HTTP
// status (the gateway's format-mismatch 500) is a definitive negative; any
// other outcome on an opened stream counts as acceptance — subsequent
// mid-stream hiccups are transient by definition and do not indict the
// surface.
func probeSurfaceOpens(ctx context.Context, model schema.Model, opts schema.StreamOptions) bool {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	probeOpts := opts
	probeOpts.MaxTokens = probeMaxTokens
	// A probe is not the conversation: it must not extend the provider's
	// prompt-cache lineage, carry tool state, or reuse cache keys.
	probeOpts.PromptCacheKey = ""
	probeOpts.ToolChoice = ""
	probeOpts.OnPayload = nil
	probeOpts.OnResponse = nil

	pctx := schema.Context{
		Context:  ctx,
		Messages: []schema.Message{schema.NewUserMessage("ping")},
	}

	stream, err := stream(model, pctx, probeOpts)
	if err != nil {
		return false
	}
	// The stream opened: drain one event to catch error frames the runtime
	// defers past open, then stop. Context cancel releases the connection.
	for event := range stream.Seq() {
		if event.Type == schema.EventError {
			return false
		}
		return true
	}
	// Clean EOF with no events: the surface accepted the request format.
	return stream.Err() == nil
}
