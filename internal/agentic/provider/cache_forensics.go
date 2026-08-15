// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"sync"
	"time"

	"github.com/pijalu/goa/internal/agentic/provider/schema"
)

// Cache-forensics journal
// ------------------------
//
// The journal keeps a small rolling buffer of COMPLETE serialized API
// requests so a provider prefix-cache miss can be investigated after the
// fact. Complete requests are large, so they are never logged eagerly: the
// journal detects the miss itself (per conversation sequence, mirroring the
// app-layer cache-bust rules in internal/app/stats.go) and only then retains
// the requests that explain it — the one that missed and the same sequence's
// request before it — as a CacheMissReport. Reports surface two ways:
//
//   - TakeCacheMissNotices: drained by the agent into the agent log (goa.log
//     and the always-on ring exported as logs/agent.log);
//   - CacheForensicsReports: exported by the debug bundle as
//     logs/cache_miss_requests.json.
//
// A conversation sequence is keyed by (SessionID, provider, model, system
// prompt): the SessionID rotates on fresh-context begins (RunFresh →
// ResetConversationID), so a new conversation's cold start is naturally not
// a miss; Agent.Clear and EmitContextReset call ResetCacheForensicsBaseline
// for the resets where the id does not rotate.

const (
	// cacheForensicsRingCapacity is the number of complete API request
	// bodies retained across all sequences. Bodies are large (a full
	// context is hundreds of KB), so the ring stays small: it only needs to
	// hold the missing request and its predecessor per active sequence,
	// plus slack for interleaved agents.
	cacheForensicsRingCapacity = 6
	// cacheForensicsReportCapacity bounds retained miss reports.
	cacheForensicsReportCapacity = 4
	// cacheForensicsNoticeCapacity bounds undrained notices. Notices are
	// drained after every stream round; a full queue means the consumer is
	// stuck, so the oldest are dropped.
	cacheForensicsNoticeCapacity = 16
	// cacheForensicsDropToleranceTokens mirrors cacheBustDropToleranceTokens
	// (internal/app/stats.go): providers report cached tokens at block
	// granularity, so dips below this wobble are reporting noise, not a
	// bust.
	cacheForensicsDropToleranceTokens = 1024
)

// CacheForensicsEntry is one complete recorded API request. Usage is nil
// until the response stream has been fully parsed (and stays nil for failed
// requests); a nil Usage entry never participates in miss detection.
type CacheForensicsEntry struct {
	Seq       int64           `json:"seq"`
	Timestamp time.Time       `json:"timestamp"`
	Provider  string          `json:"provider,omitempty"`
	Model     string          `json:"model,omitempty"`
	URL       string          `json:"url,omitempty"`
	SessionID string          `json:"session_id,omitempty"`
	Body      json.RawMessage `json:"body"`
	Usage     *schema.Usage   `json:"usage,omitempty"`

	// seqKey groups entries of one conversation's provider-cache sequence;
	// internal only (never serialized).
	seqKey string
}

// CacheMissReport bundles the complete requests that explain one detected
// cache miss: the same sequence's request before the miss (when available)
// followed by the request that missed — oldest first.
type CacheMissReport struct {
	ID            int64                 `json:"id"`
	Timestamp     time.Time             `json:"timestamp"`
	Model         string                `json:"model,omitempty"`
	PrevCacheRead int                   `json:"prev_cache_read_tokens"`
	CacheRead     int                   `json:"cache_read_tokens"`
	Requests      []CacheForensicsEntry `json:"requests"`
}

// CacheMissNotice is a lightweight signal that a report was captured. The
// agent drains notices into the agent log; the report carrying the complete
// requests stays in the journal for the debug bundle.
type CacheMissNotice struct {
	ReportID      int64
	Model         string
	PrevCacheRead int
	CacheRead     int
}

// cacheSeqState is the per-sequence miss-detection baseline.
type cacheSeqState struct {
	established   bool
	lastCacheRead int
}

// observe folds one response's cache-read count into the baseline and
// reports whether it constitutes a cache miss. The rules mirror the
// app-layer cache-bust detector (internal/app/stats.go handleTokenStats):
// a zero read after the cache was established, or a collapse beyond the
// block-quantization wobble tolerance.
func (st *cacheSeqState) observe(cacheRead int) bool {
	prev := st.lastCacheRead
	miss := (cacheRead == 0 && st.established) ||
		(prev > 0 && cacheRead+cacheForensicsDropToleranceTokens < prev)
	if cacheRead > 0 {
		st.established = true
	}
	st.lastCacheRead = cacheRead
	return miss
}

// cacheForensicsRecord is the handle for one in-flight request: recorded at
// send time, completed with the response usage once the stream ends.
type cacheForensicsRecord struct {
	journal *cacheForensicsJournal
	seq     int64
	seqKey  string
}

// complete attaches the response usage to the recorded request and runs
// miss detection for its sequence. Nil or all-zero usage (providers that
// report no cache stats) never triggers detection.
func (r *cacheForensicsRecord) complete(usage *schema.Usage) {
	if r == nil || usage == nil || usageEmpty(usage) {
		return
	}
	j := r.journal
	j.mu.Lock()
	defer j.mu.Unlock()
	entry := j.findBySeqLocked(r.seq)
	if entry == nil {
		return // evicted before the response landed (heavy interleave)
	}
	entry.Usage = usage
	st := j.seqState[r.seqKey]
	if st == nil {
		st = &cacheSeqState{}
		j.seqState[r.seqKey] = st
	}
	prevCacheRead := st.lastCacheRead
	if !st.observe(usage.CacheReadTokens) {
		return
	}
	j.addReportLocked(r.seqKey, prevCacheRead, usage.CacheReadTokens, entry)
}

// cacheForensicsJournal is the thread-safe rolling buffer + detector.
type cacheForensicsJournal struct {
	mu        sync.Mutex
	entries   []*CacheForensicsEntry // ring of complete requests, all sequences
	pos       int
	count     int
	seq       int64
	seqState  map[string]*cacheSeqState
	reports   []CacheMissReport
	reportSeq int64
	notices   []CacheMissNotice
}

func newCacheForensicsJournal() *cacheForensicsJournal {
	return &cacheForensicsJournal{
		entries:  make([]*CacheForensicsEntry, cacheForensicsRingCapacity),
		seqState: make(map[string]*cacheSeqState),
	}
}

// cacheForensics is the global journal fed by the generic streaming runtime.
var cacheForensics = newCacheForensicsJournal()

// record adds one complete API request to the ring and returns the handle to
// complete with the response usage.
func (j *cacheForensicsJournal) record(model schema.Model, sessionID, systemPrompt, url string, body []byte) *cacheForensicsRecord {
	if !json.Valid(body) {
		// Bodies come from the protocol serializers (always valid JSON); if a
		// future transport sends something else, keep it as a JSON string so
		// report marshaling can never break.
		if quoted, err := json.Marshal(string(body)); err == nil {
			body = quoted
		}
	}
	entry := &CacheForensicsEntry{
		Timestamp: time.Now(),
		Provider:  string(model.Provider),
		Model:     model.ID,
		URL:       url,
		SessionID: sessionID,
		Body:      json.RawMessage(body),
		seqKey:    cacheSeqKey(model, sessionID, systemPrompt),
	}
	j.mu.Lock()
	j.seq++
	entry.Seq = j.seq
	j.entries[j.pos] = entry
	j.pos = (j.pos + 1) % cacheForensicsRingCapacity
	if j.count < cacheForensicsRingCapacity {
		j.count++
	}
	j.mu.Unlock()
	return &cacheForensicsRecord{journal: j, seq: entry.Seq, seqKey: entry.seqKey}
}

// findBySeqLocked locates a ring entry by its sequence number.
func (j *cacheForensicsJournal) findBySeqLocked(seq int64) *CacheForensicsEntry {
	for i := 0; i < j.count; i++ {
		e := j.entries[(j.pos-1-i+cacheForensicsRingCapacity)%cacheForensicsRingCapacity]
		if e != nil && e.Seq == seq {
			return e
		}
	}
	return nil
}

// prevEntryLocked returns the most recent ring entry of the same sequence
// strictly before beforeSeq, preferring entries whose usage was parsed (the
// failed requests in between carry no cache signal). Newest first wins.
func (j *cacheForensicsJournal) prevEntryLocked(seqKey string, beforeSeq int64) *CacheForensicsEntry {
	var fallback *CacheForensicsEntry
	for i := 0; i < j.count; i++ {
		e := j.entries[(j.pos-1-i+cacheForensicsRingCapacity)%cacheForensicsRingCapacity]
		if e == nil || e.seqKey != seqKey || e.Seq >= beforeSeq {
			continue
		}
		if e.Usage != nil {
			return e
		}
		if fallback == nil {
			fallback = e
		}
	}
	return fallback
}

// addReportLocked retains a miss report (evicting the oldest at capacity)
// and queues its notice.
func (j *cacheForensicsJournal) addReportLocked(seqKey string, prevCacheRead, cacheRead int, miss *CacheForensicsEntry) {
	requests := make([]CacheForensicsEntry, 0, 2)
	if prev := j.prevEntryLocked(seqKey, miss.Seq); prev != nil {
		requests = append(requests, *prev)
	}
	requests = append(requests, *miss)
	j.reportSeq++
	report := CacheMissReport{
		ID:            j.reportSeq,
		Timestamp:     time.Now(),
		Model:         miss.Model,
		PrevCacheRead: prevCacheRead,
		CacheRead:     cacheRead,
		Requests:      requests,
	}
	if len(j.reports) >= cacheForensicsReportCapacity {
		copy(j.reports, j.reports[1:])
		j.reports = j.reports[:cacheForensicsReportCapacity-1]
	}
	j.reports = append(j.reports, report)
	if len(j.notices) >= cacheForensicsNoticeCapacity {
		copy(j.notices, j.notices[1:])
		j.notices = j.notices[:cacheForensicsNoticeCapacity-1]
	}
	j.notices = append(j.notices, CacheMissNotice{
		ReportID:      report.ID,
		Model:         report.Model,
		PrevCacheRead: prevCacheRead,
		CacheRead:     cacheRead,
	})
}

func (j *cacheForensicsJournal) reportsSnapshot() []CacheMissReport {
	j.mu.Lock()
	defer j.mu.Unlock()
	out := make([]CacheMissReport, len(j.reports))
	copy(out, j.reports)
	return out
}

func (j *cacheForensicsJournal) takeNotices() []CacheMissNotice {
	j.mu.Lock()
	defer j.mu.Unlock()
	if len(j.notices) == 0 {
		return nil
	}
	out := make([]CacheMissNotice, len(j.notices))
	copy(out, j.notices)
	j.notices = j.notices[:0]
	return out
}

// resetBaselineLocked re-arms miss detection for all sequences; recorded
// requests and reports are kept.
func (j *cacheForensicsJournal) resetBaseline() {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.seqState = make(map[string]*cacheSeqState)
}

// reset wipes the whole journal. Test-only.
func (j *cacheForensicsJournal) reset() {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.entries = make([]*CacheForensicsEntry, cacheForensicsRingCapacity)
	j.pos = 0
	j.count = 0
	j.seq = 0
	j.seqState = make(map[string]*cacheSeqState)
	j.reports = nil
	j.reportSeq = 0
	j.notices = nil
}

// cacheSeqKey identifies one conversation's provider-cache sequence. The
// SessionID drives the provider cache key (prompt_cache_key / session
// affinity) and rotates on fresh-context begins; provider, model, and the
// system-prompt hash keep interleaved agents (main / companion / subagents)
// from sharing a baseline when they coincide on an empty SessionID.
func cacheSeqKey(model schema.Model, sessionID, systemPrompt string) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(systemPrompt))
	return fmt.Sprintf("%s|%s|%s|%08x", sessionID, model.Provider, model.ID, h.Sum32())
}

// usageEmpty reports whether a usage carries no token counts at all; some
// providers attach an all-zero usage object, which must not be mistaken for
// "cache stats reported as zero".
func usageEmpty(u *schema.Usage) bool {
	return u.InputTokens == 0 && u.OutputTokens == 0 &&
		u.CacheReadTokens == 0 && u.CacheCreationTokens == 0
}

// recordCacheForensicsRequest adds one complete API request to the global
// journal; the returned record must be completed with the response usage so
// miss detection can run.
func recordCacheForensicsRequest(model schema.Model, sessionID, systemPrompt, url string, body []byte) *cacheForensicsRecord {
	return cacheForensics.record(model, sessionID, systemPrompt, url, body)
}

// CacheForensicsReports returns the retained cache-miss reports (oldest
// first). Each report carries the complete request that missed and the
// sequence's request before it. The slice is a snapshot safe for the caller
// to marshal.
func CacheForensicsReports() []CacheMissReport {
	return cacheForensics.reportsSnapshot()
}

// TakeCacheMissNotices drains the pending cache-miss notices (oldest first).
// One notice is queued per retained report.
func TakeCacheMissNotices() []CacheMissNotice {
	return cacheForensics.takeNotices()
}

// ResetCacheForensicsBaseline re-arms miss detection for ALL sequences: the
// next requests are treated as cold starts and cannot trigger a report.
// Called on /clear (Agent.Clear) and on in-place context resets
// (EmitContextReset), mirroring the app-layer detector re-arm.
func ResetCacheForensicsBaseline() {
	cacheForensics.resetBaseline()
}

// ResetCacheForensics wipes the entire journal (requests, baselines,
// reports, notices). Primarily for tests.
func ResetCacheForensics() {
	cacheForensics.reset()
}

// CacheForensicsInterceptor records each complete wire request in the
// cache-forensics journal before the transport round-trip and completes it
// with the response usage once the stream terminates. It preserves the
// historical inline behavior of the generic runtime: requests with no
// resolved URL are never recorded, and failed requests remain in the ring
// without usage (so they never trigger miss detection). It is the "caching"
// consumer of the StreamInterceptor seam (dsh's `llm/stream` waterfall).
func CacheForensicsInterceptor(next StreamHandler) StreamHandler {
	return func(ctx context.Context, req *StreamRequest) (*schema.AssistantMessageEventStream, error) {
		if req.URL == "" {
			return next(ctx, req)
		}
		rec := recordCacheForensicsRequest(req.Model, req.Options.SessionID, req.Context.SystemPrompt, req.URL, req.Body)
		stream, err := next(ctx, req)
		if err != nil || stream == nil {
			return stream, err
		}
		go func() {
			rec.complete(streamResultUsage(stream))
		}()
		return stream, nil
	}
}
