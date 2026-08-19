// SPDX-License-Identifier: GPL-3.0-or-later

package openairesponses

import (
	"encoding/json"
	"sync"

	"github.com/pijalu/goa/internal/agentic/provider"
)

// turnStateHeader is the server-issued sticky-routing token header name.
// Mirrors Codex's X_CODEX_TURN_STATE_HEADER (client.rs:145).
const turnStateHeader = "x-codex-turn-state"

// turnStateStore holds the server-issued x-codex-turn-state token per session
// so it can be replayed on every subsequent request within the same turn.
// The registry is package-global (provider types are stateless), but each
// entry is strictly keyed by the session cache key — concurrent sessions never
// share a token. The token is treated as a session secret: it is never logged
// or included in diagnostics (invariant 4).
var turnStates = &turnStateRegistry{bySession: map[string]string{}}

type turnStateRegistry struct {
	mu        sync.Mutex
	bySession map[string]string
}

// turnStateSessionKey derives the registry key for a request, mirroring
// wsBaselineSessionKey: prefers the explicit prompt_cache_key (the Codex
// session id), falls back to SessionID. Empty means the request cannot be
// pinned to a session — capture and replay are skipped.
func turnStateSessionKey(opts provider.StreamOptions) string {
	return wsBaselineSessionKey(opts)
}

// captureTurnState records the server-issued token for sessionKey. An empty
// token or empty sessionKey is a no-op. The token replaces any previously
// stored token for the session (a new turn start issues a fresh token).
func captureTurnState(sessionKey, token string) {
	if sessionKey == "" || token == "" {
		return
	}
	turnStates.mu.Lock()
	defer turnStates.mu.Unlock()
	turnStates.bySession[sessionKey] = token
}

// turnState returns the stored token for sessionKey, or "" when none was
// captured. Callers receive the value read-only (the registry owns the map).
func turnState(sessionKey string) string {
	if sessionKey == "" {
		return ""
	}
	turnStates.mu.Lock()
	defer turnStates.mu.Unlock()
	return turnStates.bySession[sessionKey]
}

// clearTurnState discards the token for sessionKey. Called at turn boundary
// so a new turn starts with no stale token.
func clearTurnState(sessionKey string) {
	if sessionKey == "" {
		return
	}
	turnStates.mu.Lock()
	defer turnStates.mu.Unlock()
	delete(turnStates.bySession, sessionKey)
}

// resetTurnStates wipes the registry. Test helper only.
func resetTurnStates() {
	turnStates.mu.Lock()
	defer turnStates.mu.Unlock()
	turnStates.bySession = map[string]string{}
}

// resetTurnStateSessionState wipes the turn-state registry along with the
// baseline and fallback registries. Test helper so turn-state tests stay
// isolated.
func resetTurnStateSessionState() {
	resetTurnStates()
	resetWSSessionState()
}

// injectTurnStateMetadata adds the turn-state token to the request body's
// client_metadata map (mirroring Codex's WS path, client.rs:1624–1626).
// If the body already has a client_metadata map, the token is added to it;
// otherwise a new map is created. The token is never logged.
func injectTurnStateMetadata(bodyBytes []byte, token string) []byte {
	var body map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &body); err != nil {
		// Unparseable body: return unchanged rather than corrupting the request.
		return bodyBytes
	}
	meta, ok := body["client_metadata"].(map[string]interface{})
	if !ok {
		meta = map[string]interface{}{}
	}
	meta[turnStateHeader] = token
	body["client_metadata"] = meta
	out, err := json.Marshal(body)
	if err != nil {
		return bodyBytes
	}
	return out
}
