// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package provider

import (
	"context"
	"errors"
)

// ErrRemoteCompactUnsupported is returned by a provider that does not
// implement server-side conversation compaction (POST /responses/compact).
// The agent treats it as "capability absent" and falls back to the local
// compression ladder without surfacing an error to the operator.
var ErrRemoteCompactUnsupported = errors.New("provider does not support remote compaction")

// CompactRequest is the canonical input to a provider's server-side
// conversation-compaction call. It mirrors exactly what the normal streaming
// request path already sends — model, conversation context (system prompt +
// messages + tools), and the stream options that carry session/cache identity
// and sampling controls — so the remote compact request is byte-for-byte the
// same prefix the conversation turns use (invariant: forensic redaction and
// cache parity are preserved by reusing the same builder inputs).
type CompactRequest struct {
	// Model is the active model (ID, provider, base URL, reasoning flag).
	Model Model
	// Context carries the conversation: SystemPrompt, Messages, Tools.
	Context Context
	// Options carries session/cache identity (PromptCacheKey, SessionID),
	// credentials (APIKey, CodexAccountID), sampling (ServiceTier, Reasoning),
	// and per-request timeouts used to bound the unary call.
	Options StreamOptions
}

// CompactResponse is the canonical result of a server-side compaction: the
// replacement conversation transcript (already condensed by the server) plus
// the provider-reported usage of the compact call when one was emitted.
type CompactResponse struct {
	// Messages is the replacement conversation, oldest-first, in canonical
	// form. It is a non-prefix rewrite of the prior history, so the caller
	// must advance its cache generation before the next request.
	Messages []Message
	// Usage is the provider-reported token usage of the compact request, or
	// nil when the endpoint emitted none.
	Usage *Usage
}

// RemoteCompactor is implemented by providers that expose server-side
// conversation compaction (Codex Phase 2b, POST /responses/compact). The
// agent discovers the capability via a type assertion on the resolved
// ApiProvider and only calls Compact after the operator gate and the
// provider/model capability both allow it (RemoteCompactionAvailable).
//
// Implementations must honor forensic redaction (invariant 4): diagnostics
// and errors carry bounded hashes/status codes only — never raw session keys,
// prompts, OAuth tokens, tool arguments, or the turn-state token.
type RemoteCompactor interface {
	// Compact performs a unary (non-streaming) server-side compaction of the
	// given conversation and returns the replacement transcript. It must use
	// its own bounded timeout and must not mutate the caller's Context.
	Compact(ctx context.Context, req CompactRequest) (*CompactResponse, error)
}
