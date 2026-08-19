// SPDX-License-Identifier: GPL-3.0-or-later

package agentic

import "github.com/pijalu/goa/internal/agentic/provider"

// drainCacheMissNotices flushes provider cache-miss notices into the agent
// log (goa.log and the always-on ring exported as logs/agent.log). The
// provider journal detects prefix-cache misses per conversation sequence and
// retains ONLY the complete requests that explain each miss (the bust and
// the preceding call); the notices point at those reports.
func (a *Agent) drainCacheMissNotices() {
	a.mu.Lock()
	key := a.activeCacheKey
	a.mu.Unlock()
	a.drainCacheMissNoticesForKey(key)
}

// drainCacheMissNoticesLocked is the variant for callers already holding
// a.mu (captureStreamResult): sync.Mutex is not reentrant, so the key is read
// directly instead of re-locking.
func (a *Agent) drainCacheMissNoticesLocked() {
	a.drainCacheMissNoticesForKey(a.activeCacheKey)
}

func (a *Agent) drainCacheMissNoticesForKey(key string) {
	if key == "" {
		// This agent never opened a stream, so no notice can be attributed to
		// it; draining everything here would steal other agents' notices (the
		// journal is global).
		return
	}
	notices := provider.TakeCacheMissNoticesFor(key)
	if len(notices) == 0 || a.cfg.Logger == nil {
		return
	}
	logCacheMissNotices(a.cfg.Logger, notices)
}

// logCacheMissNotices writes one log line per notice. The kind tag
// ([full]/[partial]/[tool-policy-transition]) and the likely cause
// (identity_change / server_eviction / ttl_expiry / param_change /
// tool_policy_transition / unknown) make the line actionable without opening
// the debug bundle.
func logCacheMissNotices(logger *Logger, notices []provider.CacheMissNotice) {
	for _, n := range notices {
		logger.Log(Warn, "provider cache miss #%d [%s]: model=%s cache_read %d -> %d tokens (likely cause: %s); complete API requests of the bust and the preceding call retained in the cache-forensics journal (debug bundle: logs/cache_miss_requests.json)",
			n.ReportID, cacheMissNoticeKind(n), n.Model, n.PrevCacheRead, n.CacheRead, n.LikelyCause)
	}
}

// cacheMissNoticeKind classifies a notice with the same rule the footer's
// CM part uses: a zero cache-read is a full miss (the entire prefix was
// recomputed), anything else a partial one (a suffix was recomputed).
func cacheMissNoticeKind(n provider.CacheMissNotice) string {
	if n.LikelyCause == provider.LikelyCauseToolPolicyTransition {
		return "tool-policy-transition"
	}
	if n.CacheRead == 0 {
		return "full"
	}
	return "partial"
}
