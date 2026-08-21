// SPDX-License-Identifier: GPL-3.0-or-later

package openairesponses

import (
	"errors"
	"strings"
	"sync"

	"github.com/pijalu/goa/internal/agentic/provider/transport"
)

// wsFallbackRegistry tracks sessions whose endpoint has rejected the WebSocket
// upgrade (HTTP 426 Upgrade Required) or otherwise proved WS-unsupported. Once
// marked, subsequent requests for that session are routed to the SSE transport
// with a full-history resend. The fallback is deliberately per-session — never
// global — so one session's rejection cannot degrade unrelated sessions.
var wsFallback = &wsFallbackStore{bySession: map[string]bool{}}

type wsFallbackStore struct {
	mu        sync.Mutex
	bySession map[string]bool
}

// markWSFallback records that sessionKey must fall back to SSE. An empty key
// is ignored (no session to pin the fallback to).
func markWSFallback(sessionKey string) {
	if sessionKey == "" {
		return
	}
	wsFallback.mu.Lock()
	defer wsFallback.mu.Unlock()
	wsFallback.bySession[sessionKey] = true
}

// isWSFallback reports whether sessionKey has been marked WS-unsupported.
func isWSFallback(sessionKey string) bool {
	if sessionKey == "" {
		return false
	}
	wsFallback.mu.Lock()
	defer wsFallback.mu.Unlock()
	return wsFallback.bySession[sessionKey]
}

// resetWSFallbacks wipes the registry. Test helper only.
func resetWSFallbacks() {
	wsFallback.mu.Lock()
	defer wsFallback.mu.Unlock()
	wsFallback.bySession = map[string]bool{}
}

// isWSUnsupportedError reports whether a WS transport error means the endpoint
// does not support the WebSocket transport (vs. a transient network failure).
// The shared transport preserves a rejected handshake's HTTP status as
// *transport.UpgradeRequiredError; any such status means the upgrade itself
// was refused (426 Upgrade Required, 404, 501, ...), so the session must fall
// back to SSE. The string fallback keeps compatibility with transports that
// still surface the raw gorilla error text.
func isWSUnsupportedError(err error) bool {
	if err == nil {
		return false
	}
	var upErr *transport.UpgradeRequiredError
	if errors.As(err, &upErr) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "426") || strings.Contains(msg, "upgrade required")
}
