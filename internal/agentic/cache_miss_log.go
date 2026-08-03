// SPDX-License-Identifier: GPL-3.0-or-later

package agentic

import "github.com/pijalu/goa/internal/agentic/provider"

// drainCacheMissNotices flushes provider cache-miss notices into the agent
// log (goa.log and the always-on ring exported as logs/agent.log). The
// provider journal detects prefix-cache misses per conversation sequence and
// retains ONLY the complete requests that explain each miss (the bust and
// the preceding call); the notices point at those reports.
func (a *Agent) drainCacheMissNotices() {
	notices := provider.TakeCacheMissNotices()
	if len(notices) == 0 || a.cfg.Logger == nil {
		return
	}
	logCacheMissNotices(a.cfg.Logger, notices)
}

// logCacheMissNotices writes one log line per notice.
func logCacheMissNotices(logger *Logger, notices []provider.CacheMissNotice) {
	for _, n := range notices {
		logger.Log(Warn, "provider cache miss #%d: model=%s cache_read %d -> %d tokens; complete API requests of the bust and the preceding call retained in the cache-forensics journal (debug bundle: logs/cache_miss_requests.json)",
			n.ReportID, n.Model, n.PrevCacheRead, n.CacheRead)
	}
}
