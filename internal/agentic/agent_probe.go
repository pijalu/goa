// SPDX-License-Identifier: GPL-3.0-or-later

package agentic

import (
	"context"
	"fmt"

	"github.com/pijalu/goa/internal/agentic/provider"
)

// tryProbeWireFormat implements the lazy wire-format auto-probe (Option B).
//
// Trigger: a stream failed with a classified SERVER (5xx) error on a
// multi-surface gateway (OpenCode Zen/Go) whose model's wire API is unpinned
// (no explicit user `api:` and no curated model_overrides.yaml entry). Such a
// gateway answers a wire-format mismatch with a generic HTTP 500 — handler.ts
// throws instead of converting — so the configured surface may simply be the
// wrong one for this model, and no amount of retrying it will succeed.
//
// When the error is SERVER-classified (and eligible under the retry policy's
// codes list, so a code-filtered config is never second-guessed), this probes
// the gateway's other surfaces once per model per session with minimal real
// requests that reuse per-API auth and session headers. The first surface
// whose stream OPENS is definitive proof it accepts the model's wire format.
//
// On success it returns a copy of the model rerouted to the winning surface
// (Api + BaseURL swapped) with ok=true; the caller retries the original
// request against it through the normal budgeted path and pins the session
// model so later turns keep the discovered API. On all-fail (or when probing
// does not apply) it returns ok=false and the caller falls through to the
// normal retry path unchanged.
func (a *Agent) tryProbeWireFormat(ctx context.Context, streamErr error, model provider.Model, opts provider.StreamOptions) (provider.Model, bool) {
	// Only a classified SERVER (5xx) failure justifies probing: a wire-format
	// mismatch surfaces as a generic gateway 500, not a 4xx (auth/format),
	// rate-limit, timeout, or transport blip.
	if retryCodeOf(streamErr) != provider.RetryCodeServer {
		return model, false
	}
	// Respect an explicit retry-policy codes filter: if the operator scoped
	// retries to a subset that excludes SERVER, do not second-guess the
	// failure — fall through to the normal (non-retrying) path. A nil policy
	// (direct callers bypassing handleStreamFailure) imposes no restriction.
	if policy := opts.RetryPolicy; policy != nil && !policyAllowsCode(streamErr, policy.Codes) {
		return model, false
	}
	// Resolve the alternative surfaces. This is nil for pinned models
	// (ApiSource "user"/"curated"), non-gateway providers, and when the
	// current URL carries no known surface route — probing never applies.
	candidates := provider.ProbeWireFormatCandidates(model)
	if len(candidates) == 0 {
		return model, false
	}
	// At most one probe round per model per session: after the first episode
	// the model is either rerouted (a.cfg.Model carries the new Api, so this
	// model ID never re-probes) or cached negative, so a repeat 500 goes
	// straight to the normal retry path without re-burning probe requests.
	a.mu.Lock()
	if a.probedWireFormats == nil {
		a.probedWireFormats = make(map[string]struct{})
	}
	if _, seen := a.probedWireFormats[model.ID]; seen {
		a.mu.Unlock()
		return model, false
	}
	a.probedWireFormats[model.ID] = struct{}{}
	a.mu.Unlock()

	a.cfg.Logger.Log(Warn, "stream failed with SERVER on multi-surface gateway; probing alternative wire formats for model %q (current api %s)", model.ID, model.Api)

	rerouted, ok := provider.ProbeWireFormat(ctx, model, opts, candidates)
	if !ok {
		a.cfg.Logger.Log(Warn, "wire-format probe found no working surface for model %q; falling through to normal retry", model.ID)
		return model, false
	}

	a.cfg.Logger.Log(Info, "wire-format probe rerouted model %q: %s -> %s (%s)", model.ID, model.Api, rerouted.Api, rerouted.BaseURL)
	a.emitWireFormatReroute(model, rerouted)

	// Pin the session model so every later turn keeps the discovered API
	// (prepareTurn mints `model := a.cfg.Model` per turn). The immediate
	// retry uses the returned rerouted copy; this mutation carries it forward.
	a.mu.Lock()
	a.cfg.Model.Api = rerouted.Api
	a.cfg.Model.BaseURL = rerouted.BaseURL
	a.mu.Unlock()

	return rerouted, true
}

// emitWireFormatReroute surfaces a probe reroute as a durable system
// notification, including a hint to pin the discovered `api:` in config so
// the routing survives the session (the reroute itself is in-memory only —
// goa never writes routing facts into committable config).
func (a *Agent) emitWireFormatReroute(from, to provider.Model) {
	a.emitEvent(OutputEvent{
		Type: EventContent,
		Role: System,
		Text: fmt.Sprintf("Detected wire-format mismatch for %q: %s returned a server error, but the model answers on %s. Rerouted for this session. To make it permanent, set `api: %s` on this model in your config.", from.ID, from.Api, to.Api, to.Api),
		Metadata: map[string]string{
			"category":          "system-notification",
			"wire_format_probe": "rerouted",
			"from_api":          string(from.Api),
			"to_api":            string(to.Api),
		},
	})
}
