// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

import (
	"github.com/pijalu/goa/internal/agentic/provider"
	"github.com/pijalu/goa/internal/agentic/provider/schema"
)

// RemoteCompactionAvailable reports whether server-side conversation
// compaction (Codex Phase 2b, POST /responses/compact) is available for the
// given model under the given operator gate. It is a pure primitive: true only
// when the operator has opted in (gate on) AND the provider/model's resolved
// capability advertises a supported remote-compaction level (v1 or v2). The
// zero gate (false) and the zero capability (none) both yield false, so the
// default configuration keeps the local compression ladder unchanged.
//
// This is detection and gating only — it performs no request and selects no
// strategy (2b.2 consumes this signal to prefer the remote strategy).
func RemoteCompactionAvailable(gateEnabled bool, model provider.Model) bool {
	if !gateEnabled {
		return false
	}
	return RemoteCompactionLevel(model).Supported()
}

// RemoteCompactionLevel resolves the provider/model-scoped remote-compaction
// capability for the given model, independent of any operator gate. It returns
// schema.RemoteCompactionNone when the endpoint does not advertise support.
func RemoteCompactionLevel(model provider.Model) schema.RemoteCompactionSupport {
	return provider.ResolveProfile(model).Compat.RemoteCompaction
}

// remoteCompactionAvailable is the Agent-bound form of
// RemoteCompactionAvailable, reading the configured gate and the active model.
// The result is memoized at construction (gate + model are immutable), so this
// is cheap and safe to call under a.mu on every compaction-policy pass.
func (a *Agent) remoteCompactionAvailable() bool {
	if a.remoteCompactAvailableFn == nil {
		// Defensive: agents built outside NewAgent (zero-value Agent in tests)
		// resolve directly without memoization.
		return RemoteCompactionAvailable(a.cfg.RemoteCompactionEnabled, a.cfg.Model)
	}
	return a.remoteCompactAvailableFn()
}
