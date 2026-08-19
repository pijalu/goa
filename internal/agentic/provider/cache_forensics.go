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
//   - TakeCacheMissNoticesFor: drained by the agent into the agent log
//     (goa.log and the always-on ring exported as logs/agent.log), scoped to
//     the agent's own cache session key so concurrent agents never steal
//     each other's notices; TakeCacheMissNotices remains as the drain-all
//     variant for diagnostics and tests;
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
	Seq         int64              `json:"seq"`
	Timestamp   time.Time          `json:"timestamp"`
	Provider    string             `json:"provider,omitempty"`
	Model       string             `json:"model,omitempty"`
	URL         string             `json:"url,omitempty"`
	SessionID   string             `json:"session_id,omitempty"`
	Body        json.RawMessage    `json:"body"`
	Usage       *schema.Usage      `json:"usage,omitempty"`
	Fingerprint RequestFingerprint `json:"fingerprint,omitempty"`

	// completedAt is when the response usage landed (stream end); internal
	// only (never serialized) — the idle-gap attribution anchors on it.
	completedAt time.Time

	// seqKey groups entries of one conversation's provider-cache sequence;
	// internal only (never serialized).
	seqKey string
}

// CacheMissReport bundles the complete requests that explain one detected
// cache miss: the same sequence's request before the miss (when available)
// followed by the request that missed — oldest first. The attribution
// fields answer, without external data, what most likely caused the miss:
// how long after the previous response completed it fired (TTL vs eviction),
// which earlier checkpoint a partial hit matches (routing/eviction), the
// previous request's total prompt size, and whether an affinity hint
// (prompt_cache_key) was on the wire.
type CacheMissReport struct {
	ID            int64                 `json:"id"`
	Timestamp     time.Time             `json:"timestamp"`
	Model         string                `json:"model,omitempty"`
	PrevCacheRead int                   `json:"prev_cache_read_tokens"`
	CacheRead     int                   `json:"cache_read_tokens"`
	Requests      []CacheForensicsEntry `json:"requests"`

	// GapSincePrevResponseMs is the idle between the previous request's
	// stream completing (its usage landing) and the miss request being sent.
	// 0 means unknown (no attributable predecessor in the ring).
	GapSincePrevResponseMs int64 `json:"gap_since_prev_response_ms,omitempty"`
	// PrevTotalPromptTokens is the predecessor's total prompt size
	// (input + cache-read + cache-write) — the prefix that would have been
	// cache-served without the miss.
	PrevTotalPromptTokens int `json:"prev_total_prompt_tokens,omitempty"`
	// PartialHitPrevSeq attributes a partial hit: the newest earlier request
	// of the sequence whose cache_read equals this miss's — the checkpoint
	// the provider fell back to. 0 when the miss is full (cache_read 0) or
	// the checkpoint entry is no longer in the ring.
	PartialHitPrevSeq int64 `json:"partial_hit_prev_seq,omitempty"`
	// AffinityHintSent reports whether the miss request carried an explicit
	// cache-affinity identity (prompt_cache_key) on the wire — distinguishing
	// "no hint available" from "hint ignored".
	AffinityHintSent bool `json:"affinity_hint_sent"`
	// LikelyCause is the derived most-probable origin of the miss.
	LikelyCause LikelyCause `json:"likely_cause"`
}

// LikelyCause classifies the most probable origin of a detected cache miss,
// derived at report time from the evidence the journal holds.
type LikelyCause string

const (
	// LikelyCauseIdentityChange: the request was flagged as a deliberate
	// context replacement (fingerprint classification "replacement") — a new
	// cache identity, so the miss is expected, not provider-side.
	LikelyCauseIdentityChange LikelyCause = "identity_change"
	// LikelyCauseServerEviction: the provider lost (part of) the cached
	// prefix despite a short idle — e.g. per-node caches behind a load
	// balancer, or tail-block eviction pressure. Partial hits attributed to an
	// older request's checkpoint carry that seq in PartialHitPrevSeq.
	LikelyCauseServerEviction LikelyCause = "server_eviction"
	// LikelyCauseTTLExpiry: the miss fired after a long idle gap, the classic
	// provider TTL expiry signature (cache_read collapsing to zero or an
	// older checkpoint after minutes of inactivity).
	LikelyCauseTTLExpiry LikelyCause = "ttl_expiry"
	// LikelyCauseParamChange: the request changed a cache-relevant parameter
	// (tools/thinking) while appending history (fingerprint classification
	// "param_change") — some providers key the cache on the full request.
	LikelyCauseParamChange LikelyCause = "param_change"
	// LikelyCauseUnknown: not enough evidence to attribute (no attributable
	// predecessor retained in the ring).
	LikelyCauseUnknown LikelyCause = "unknown"
)

// cacheForensicsTTLExpiryGapMs is the idle threshold at or above which a miss
// reads as ttl_expiry rather than server_eviction. It must sit well below
// real provider TTLs (single-digit minutes) while covering normal
// intra-turn pacing (a tool round trip is seconds), so the observed 60s
// default separates "conversation paced along" from "user went away".
const cacheForensicsTTLExpiryGapMs = 60_000

// CacheMissNotice is a lightweight signal that a report was captured. The
// agent drains notices into the agent log; the report carrying the complete
// requests stays in the journal for the debug bundle. LikelyCause makes the
// log line actionable without opening the bundle.
type CacheMissNotice struct {
	ReportID      int64
	Model         string
	PrevCacheRead int
	CacheRead     int
	LikelyCause   LikelyCause
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
	usageSnapshot := *usage
	entry.Usage = &usageSnapshot
	entry.completedAt = time.Now()
	j.metrics.CacheReadTokens += int64(usage.CacheReadTokens)
	j.metrics.CacheWriteTokens += int64(usage.CacheCreationTokens)
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
type CacheForensicsMetrics struct {
	Requests         int64              `json:"requests"`
	SerializedBytes  int64              `json:"serialized_bytes"`
	CacheReadTokens  int64              `json:"cache_read_tokens"`
	CacheWriteTokens int64              `json:"cache_write_tokens"`
	CompactionCount  int64              `json:"compaction_count"`
	CacheKeyChanges  int64              `json:"cache_key_changes"`
	ToolSchemaHash   string             `json:"tool_schema_hash,omitempty"`
	LastFingerprint  RequestFingerprint `json:"last_fingerprint,omitempty"`
}

type cacheForensicsJournal struct {
	mu          sync.Mutex
	entries     []*CacheForensicsEntry // ring of complete requests, all sequences
	pos         int
	count       int
	seq         int64
	seqState    map[string]*cacheSeqState
	reports     []CacheMissReport
	reportSeq   int64
	notices     []cacheMissNoticeEntry
	metrics     CacheForensicsMetrics
	lastKeyHash string
}

// cacheMissNoticeEntry pairs a drainable notice with the cache session key
// of the sequence that produced it, so concurrent agents can drain only
// their own notices instead of racing to steal each other's.
type cacheMissNoticeEntry struct {
	notice     CacheMissNotice
	sessionKey string
}

func newCacheForensicsJournal() *cacheForensicsJournal {
	return &cacheForensicsJournal{
		entries:  make([]*CacheForensicsEntry, cacheForensicsRingCapacity),
		seqState: make(map[string]*cacheSeqState),
	}
}

// cacheForensics is the global journal fed by the generic streaming runtime.
var cacheForensics = newCacheForensicsJournal()

func (j *cacheForensicsJournal) previousRequest(model schema.Model, sessionID, systemPrompt string) []byte {
	key := cacheSeqKey(model, sessionID, systemPrompt)
	j.mu.Lock()
	defer j.mu.Unlock()
	for i := 0; i < j.count; i++ {
		entry := j.entries[(j.pos-1-i+cacheForensicsRingCapacity)%cacheForensicsRingCapacity]
		if entry != nil && entry.seqKey == key {
			return append([]byte(nil), entry.Body...)
		}
	}
	return nil
}

// record adds one complete API request to the ring and returns the handle to
// complete with the response usage.
func (j *cacheForensicsJournal) record(model schema.Model, sessionID, systemPrompt, url string, body []byte, fingerprints ...RequestFingerprint) *cacheForensicsRecord {
	var fingerprint RequestFingerprint
	if len(fingerprints) > 0 {
		fingerprint = fingerprints[0]
	}
	if !json.Valid(body) {
		// Bodies come from the protocol serializers (always valid JSON); if a
		// future transport sends something else, keep it as a JSON string so
		// report marshaling can never break.
		if quoted, err := json.Marshal(string(body)); err == nil {
			body = quoted
		}
	}
	// Own the serialized bytes: protocol buffers may be reused or mutated by
	// the caller after recording. Reports must remain an immutable snapshot of
	// the wire request, not an alias into a later request's backing array.
	bodySnapshot := append([]byte(nil), body...)
	entry := &CacheForensicsEntry{
		Timestamp:   time.Now(),
		Provider:    string(model.Provider),
		Model:       model.ID,
		URL:         url,
		SessionID:   sessionID,
		Body:        json.RawMessage(bodySnapshot),
		Fingerprint: fingerprint,
		seqKey:      cacheSeqKey(model, sessionID, systemPrompt),
	}
	j.mu.Lock()
	j.seq++
	entry.Seq = j.seq
	j.metrics.Requests++
	j.metrics.SerializedBytes += int64(len(bodySnapshot))
	if fingerprint.SessionKeyHash != "" && j.lastKeyHash != "" && fingerprint.SessionKeyHash != j.lastKeyHash {
		j.metrics.CacheKeyChanges++
	}
	if fingerprint.SessionKeyHash != "" {
		j.lastKeyHash = fingerprint.SessionKeyHash
	}
	j.metrics.LastFingerprint = fingerprint
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
	prev := j.prevEntryLocked(seqKey, miss.Seq)
	attribution := j.attributionLocked(seqKey, miss, prev, cacheRead)

	requests := make([]CacheForensicsEntry, 0, 2)
	if prev != nil {
		requests = append(requests, *prev)
	}
	requests = append(requests, *miss)
	j.reportSeq++
	report := CacheMissReport{
		ID:                     j.reportSeq,
		Timestamp:              time.Now(),
		Model:                  miss.Model,
		PrevCacheRead:          prevCacheRead,
		CacheRead:              cacheRead,
		Requests:               requests,
		GapSincePrevResponseMs: attribution.gapMs,
		PrevTotalPromptTokens:  attribution.prevTotalTokens,
		PartialHitPrevSeq:      attribution.partialHitPrevSeq,
		AffinityHintSent:       attribution.affinityHintSent,
		LikelyCause:            attribution.cause,
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
	j.notices = append(j.notices, cacheMissNoticeEntry{
		notice: CacheMissNotice{
			ReportID:      report.ID,
			Model:         report.Model,
			PrevCacheRead: prevCacheRead,
			CacheRead:     cacheRead,
			LikelyCause:   report.LikelyCause,
		},
		// miss.SessionID carries the cache session key (PromptCacheKey, or the
		// transport SessionID when no cache key was set) — the attribution key
		// for scoped draining.
		sessionKey: miss.SessionID,
	})
}

// missAttribution carries the derived evidence for one miss report.
type missAttribution struct {
	gapMs             int64
	prevTotalTokens   int
	partialHitPrevSeq int64
	affinityHintSent  bool
	cause             LikelyCause
}

// attributionLocked derives the report's attribution fields: idle gap since
// the previous response completed, the predecessor's total prompt size, the
// checkpoint attribution of a partial hit, affinity-hint presence, and the
// resulting likely cause.
func (j *cacheForensicsJournal) attributionLocked(seqKey string, miss *CacheForensicsEntry, prev *CacheForensicsEntry, cacheRead int) missAttribution {
	attr := missAttribution{cause: LikelyCauseUnknown}
	if prev == nil {
		// No attributable predecessor retained (heavy interleave/ring churn):
		// cause stays unknown rather than guessing.
		return attr
	}
	// Client-side causes first — the fingerprint already classified the
	// request's relation to its predecessor.
	switch miss.Fingerprint.Classification {
	case PrefixParamChange:
		attr.cause = LikelyCauseParamChange
	case PrefixReplacement:
		attr.cause = LikelyCauseIdentityChange
	}
	if prev.Usage != nil {
		attr.prevTotalTokens = prev.Usage.TotalInputTokens()
	}
	// Idle gap: previous stream completion → miss request send. Fall back to
	// send-to-send when the predecessor never completed (its usage arrived
	// only as a fallback entry), which overestimates by its stream duration.
	anchor := prev.completedAt
	if anchor.IsZero() {
		anchor = prev.Timestamp
	}
	if gap := miss.Timestamp.Sub(anchor).Milliseconds(); gap > 0 {
		attr.gapMs = gap
	}
	if attr.cause == LikelyCauseUnknown && attr.gapMs >= cacheForensicsTTLExpiryGapMs {
		attr.cause = LikelyCauseTTLExpiry
	}
	// Partial-hit checkpoint attribution: which earlier request's cached
	// prefix did the provider fall back to.
	if cacheRead > 0 {
		attr.partialHitPrevSeq = j.partialHitPrevSeqLocked(seqKey, miss.Seq, cacheRead)
	}
	if attr.cause == LikelyCauseUnknown {
		// Short idle, no client-side cause, cache still (partially) warm or
		// fully cold: the provider lost it — eviction/routing.
		attr.cause = LikelyCauseServerEviction
	}
	attr.affinityHintSent = bodyHasAffinityHint(miss.Body)
	return attr
}

// partialHitPrevSeqLocked returns the newest earlier entry of the sequence
// whose cache_read equals cacheRead — the checkpoint a partial hit fell back
// to — or 0 when none is retained in the ring.
func (j *cacheForensicsJournal) partialHitPrevSeqLocked(seqKey string, beforeSeq int64, cacheRead int) int64 {
	for i := 0; i < j.count; i++ {
		e := j.entries[(j.pos-1-i+cacheForensicsRingCapacity)%cacheForensicsRingCapacity]
		if e == nil || e.seqKey != seqKey || e.Seq >= beforeSeq || e.Usage == nil {
			continue
		}
		if e.Usage.CacheReadTokens == cacheRead {
			return e.Seq
		}
	}
	return 0
}

// bodyHasAffinityHint reports whether a serialized request body carries an
// explicit cache-affinity identity (OpenAI-style prompt_cache_key).
func bodyHasAffinityHint(body json.RawMessage) bool {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(body, &probe); err != nil {
		return false
	}
	_, ok := probe["prompt_cache_key"]
	return ok
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
	for i, e := range j.notices {
		out[i] = e.notice
	}
	j.notices = j.notices[:0]
	return out
}

// takeNoticesFor drains only the notices recorded under sessionKey, leaving
// other sequences' notices queued for their own owners.
func (j *cacheForensicsJournal) takeNoticesFor(sessionKey string) []CacheMissNotice {
	j.mu.Lock()
	defer j.mu.Unlock()
	if sessionKey == "" || len(j.notices) == 0 {
		return nil
	}
	var out []CacheMissNotice
	kept := j.notices[:0]
	for _, e := range j.notices {
		if e.sessionKey == sessionKey {
			out = append(out, e.notice)
			continue
		}
		kept = append(kept, e)
	}
	j.notices = kept
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
	j.metrics = CacheForensicsMetrics{}
	j.lastKeyHash = ""
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
func recordCacheForensicsRequest(model schema.Model, sessionID, systemPrompt, url string, body []byte, fingerprint ...RequestFingerprint) *cacheForensicsRecord {
	return cacheForensics.record(model, sessionID, systemPrompt, url, body, fingerprint...)
}

// CacheForensicsReports returns the retained cache-miss reports (oldest
// first). Each report carries the complete request that missed and the
// sequence's request before it. The slice is a snapshot safe for the caller
// to marshal.

func CacheForensicsReports() []CacheMissReport {
	return cacheForensics.reportsSnapshot()
}

// RecordCompaction records one completed history compaction in baseline metrics.
func RecordCompaction() {
	cacheForensics.mu.Lock()
	defer cacheForensics.mu.Unlock()
	cacheForensics.metrics.CompactionCount++
}

// RecordToolSchemaHash stores the current non-sensitive tool-schema digest.
func RecordToolSchemaHash(hash string) {
	cacheForensics.mu.Lock()
	defer cacheForensics.mu.Unlock()
	cacheForensics.metrics.ToolSchemaHash = hash
}

// CacheForensicsMetricsSnapshot returns deterministic aggregate observations
// for baseline tests and debug telemetry. The returned value is a snapshot.
func CacheForensicsMetricsSnapshot() CacheForensicsMetrics {
	cacheForensics.mu.Lock()
	defer cacheForensics.mu.Unlock()
	return cacheForensics.metrics
}

// TakeCacheMissNotices drains ALL pending cache-miss notices (oldest first).
// One notice is queued per retained report. Intended for diagnostics and
// tests; concurrent agents should use TakeCacheMissNoticesFor so each drains
// only its own sequence's notices.
func TakeCacheMissNotices() []CacheMissNotice {
	return cacheForensics.takeNotices()
}

// TakeCacheMissNoticesFor drains only the notices recorded under the given
// cache session key — the PromptCacheKey the requests carried (falling back
// to the transport SessionID when none was set). Notices of other sequences
// stay queued for their owners.
func TakeCacheMissNoticesFor(sessionKey string) []CacheMissNotice {
	return cacheForensics.takeNoticesFor(sessionKey)
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
		sessionKey := req.Options.PromptCacheKey
		if sessionKey == "" {
			sessionKey = req.Options.SessionID
		}
		previous := cacheForensics.previousRequest(req.Model, sessionKey, req.Context.SystemPrompt)
		fingerprint := BuildRequestFingerprint(string(req.Model.Provider), req.Model.ID, sessionKey, previous, req.Body, 0, 0, string(req.Options.Transport), "", false)
		rec := recordCacheForensicsRequest(req.Model, sessionKey, req.Context.SystemPrompt, req.URL, req.Body, fingerprint)
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
