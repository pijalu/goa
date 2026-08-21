// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package provider

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/pijalu/goa/internal/agentic/provider/schema"
	"github.com/stretchr/testify/require"
)

// attributionJournal builds a journal with one recorded+completed request and
// returns a helper to add more. Bodies are valid JSON objects so affinity
// probing works; hint controls prompt_cache_key presence.
func attributionJournal(t *testing.T, cacheRead int, hint bool) *cacheForensicsJournal {
	t.Helper()
	j := newCacheForensicsJournal()
	recordAttributed(t, j, 1, cacheRead, hint, RequestFingerprint{}, time.Time{}, time.Time{})
	return j
}

// recordAttributed records one request on j with a chosen cache_read, prompt
// affinity-hint presence, fingerprint, send time and completion time, then
// completes it with that usage. Zero times mean "now".
func recordAttributed(t *testing.T, j *cacheForensicsJournal, bodyN, cacheRead int, hint bool, fp RequestFingerprint, sent, completed time.Time) {
	recordAttributedSession(t, j, "sess", bodyN, cacheRead, hint, fp, sent, completed)
}

func recordAttributedSession(t *testing.T, j *cacheForensicsJournal, session string, bodyN, cacheRead int, hint bool, fp RequestFingerprint, sent, completed time.Time) {
	t.Helper()
	body := map[string]any{"model": "m", "n": bodyN}
	if hint {
		body["prompt_cache_key"] = "goa-session-key"
	}
	raw, err := json.Marshal(body)
	require.NoError(t, err)

	rec := j.record(schema.Model{ID: "m", Provider: "p"}, session, "sys", "http://x/v1/chat/completions", raw, fp)
	entry := j.findBySeqLocked(rec.seq)
	require.NotNil(t, entry)
	if !sent.IsZero() {
		entry.Timestamp = sent
	}
	rec.complete(&schema.Usage{InputTokens: 10, OutputTokens: 5, CacheReadTokens: cacheRead})
	if !completed.IsZero() {
		// complete() stamps now; pin it for deterministic gap assertions.
		e := j.findBySeqLocked(rec.seq)
		require.NotNil(t, e)
		e.completedAt = completed
	}
}

// TestCacheForensicsAttribution_ServerEviction reproduces the live z.ai
// signature (exports 2026-08-19): a partial hit at an OLDER request's exact
// checkpoint with a ~400ms idle gap — routing/eviction, not TTL and not any
// client-side cause.
func TestCacheForensicsAttribution_ServerEviction(t *testing.T) {
	j := newCacheForensicsJournal()
	base := time.Now()
	// Requests 1..3: the climb (7872 is the checkpoint the miss falls to).
	recordAttributed(t, j, 1, 7872, true, RequestFingerprint{}, base, base.Add(2*time.Second))
	recordAttributed(t, j, 2, 23168, true, RequestFingerprint{}, base.Add(3*time.Second), base.Add(5*time.Second))
	recordAttributed(t, j, 3, 38144, true, RequestFingerprint{}, base.Add(6*time.Second), base.Add(8*time.Second))
	// Request 4: partial bust back to request 1's checkpoint, 400ms later.
	recordAttributed(t, j, 4, 7872, true, RequestFingerprint{}, base.Add(8*time.Second+400*time.Millisecond), base.Add(9*time.Second))

	reports := j.reportsSnapshot()
	require.Len(t, reports, 1)
	r := reports[0]
	require.Equal(t, LikelyCauseServerEviction, r.LikelyCause)
	require.Equal(t, int64(400), r.GapSincePrevResponseMs, "gap must be prev-completion -> miss-send")
	require.EqualValues(t, 1, r.PartialHitPrevSeq, "partial hit falls back to request 1's checkpoint (7872)")
	require.Equal(t, 10+38144, r.PrevTotalPromptTokens)
	require.True(t, r.AffinityHintSent, "body carried prompt_cache_key")
}

// TestCacheForensicsAttribution_TTLExpiry pins the idle signature: the same
// partial collapse but after a 120s gap reads as ttl_expiry.
func TestCacheForensicsAttribution_TTLExpiry(t *testing.T) {
	j := newCacheForensicsJournal()
	base := time.Now()
	recordAttributed(t, j, 1, 1000, false, RequestFingerprint{}, base, base.Add(2*time.Second))
	recordAttributed(t, j, 2, 40000, false, RequestFingerprint{}, base.Add(3*time.Second), base.Add(5*time.Second))
	recordAttributed(t, j, 3, 1000, false, RequestFingerprint{}, base.Add(5*time.Second+120*time.Second), base.Add(125*time.Second))

	reports := j.reportsSnapshot()
	require.Len(t, reports, 1)
	r := reports[0]
	require.Equal(t, LikelyCauseTTLExpiry, r.LikelyCause)
	require.Equal(t, int64(120_000), r.GapSincePrevResponseMs)
	require.False(t, r.AffinityHintSent, "no prompt_cache_key on the wire")
	require.EqualValues(t, 1, r.PartialHitPrevSeq)
}

// TestCacheForensicsAttribution_FullMiss pins the zero-read shapes: a full
// miss with a short gap attributes to server eviction (the provider lost the
// whole prefix); one with a long gap to ttl_expiry.
func TestCacheForensicsAttribution_FullMiss(t *testing.T) {
	t.Run("short gap is eviction", func(t *testing.T) {
		j := newCacheForensicsJournal()
		base := time.Now()
		recordAttributed(t, j, 1, 40000, false, RequestFingerprint{}, base, base.Add(2*time.Second))
		recordAttributed(t, j, 2, 0, false, RequestFingerprint{}, base.Add(3*time.Second), base.Add(4*time.Second))
		reports := j.reportsSnapshot()
		require.Len(t, reports, 1)
		require.Equal(t, LikelyCauseServerEviction, reports[0].LikelyCause)
		require.Zero(t, reports[0].PartialHitPrevSeq, "full miss has no checkpoint attribution")
	})
	t.Run("long gap is ttl expiry", func(t *testing.T) {
		j := newCacheForensicsJournal()
		base := time.Now()
		recordAttributed(t, j, 1, 40000, false, RequestFingerprint{}, base, base.Add(2*time.Second))
		recordAttributed(t, j, 2, 0, false, RequestFingerprint{}, base.Add(2*time.Second+90*time.Second), base.Add(92*time.Second))
		reports := j.reportsSnapshot()
		require.Len(t, reports, 1)
		require.Equal(t, LikelyCauseTTLExpiry, reports[0].LikelyCause)
	})
}

// TestCacheForensicsAttribution_ClientSideCauses pins the fingerprint-driven
// classifications: param_change (tools/thinking changed while appending) and
// replacement (deliberate context reset).
func TestCacheForensicsAttribution_ClientSideCauses(t *testing.T) {
	t.Run("param change", func(t *testing.T) {
		j := newCacheForensicsJournal()
		base := time.Now()
		recordAttributed(t, j, 1, 40000, false, RequestFingerprint{}, base, base.Add(2*time.Second))
		recordAttributed(t, j, 2, 0, false, RequestFingerprint{Classification: PrefixParamChange}, base.Add(3*time.Second), base.Add(4*time.Second))
		require.Equal(t, LikelyCauseParamChange, j.reportsSnapshot()[0].LikelyCause)
	})

	t.Run("tool policy transition", func(t *testing.T) {
		j := newCacheForensicsJournal()
		base := time.Now()
		recordAttributed(t, j, 1, 40000, false, RequestFingerprint{}, base, base.Add(2*time.Second))
		recordAttributed(t, j, 2, 0, false, RequestFingerprint{Classification: PrefixToolPolicyTransition}, base.Add(3*time.Second), base.Add(4*time.Second))
		require.Equal(t, LikelyCauseToolPolicyTransition, j.reportsSnapshot()[0].LikelyCause)
	})
	t.Run("replacement is identity change", func(t *testing.T) {
		j := newCacheForensicsJournal()
		base := time.Now()
		recordAttributed(t, j, 1, 40000, false, RequestFingerprint{}, base, base.Add(2*time.Second))
		recordAttributed(t, j, 2, 0, false, RequestFingerprint{Classification: PrefixReplacement}, base.Add(3*time.Second), base.Add(4*time.Second))
		require.Equal(t, LikelyCauseIdentityChange, j.reportsSnapshot()[0].LikelyCause)
	})
	// Client-side classification wins over the gap heuristic: a flagged
	// replacement after 120s idle is still an identity change.
	t.Run("classification beats gap", func(t *testing.T) {
		j := newCacheForensicsJournal()
		base := time.Now()
		recordAttributed(t, j, 1, 40000, false, RequestFingerprint{}, base, base.Add(2*time.Second))
		recordAttributed(t, j, 2, 0, false, RequestFingerprint{Classification: PrefixReplacement}, base.Add(2*time.Second+120*time.Second), base.Add(122*time.Second))
		require.Equal(t, LikelyCauseIdentityChange, j.reportsSnapshot()[0].LikelyCause)
	})
}

// TestCacheForensicsAttribution_SteadyClimbNoReport: a monotonic cache-read
// climb never produces a report (no attribution to derive).
func TestCacheForensicsAttribution_SteadyClimbNoReport(t *testing.T) {
	j := attributionJournal(t, 1000, true)
	base := time.Now()
	recordAttributed(t, j, 2, 2000, true, RequestFingerprint{}, base, base.Add(time.Second))
	recordAttributed(t, j, 3, 3000, true, RequestFingerprint{}, base.Add(2*time.Second), base.Add(3*time.Second))
	require.Empty(t, j.reportsSnapshot())
	require.Empty(t, j.takeNotices())
}

// TestCacheForensicsAttribution_RingEvictionDegradesToUnknown: when the only
// attributable predecessor has already left the ring (heavy interleave by
// other sequences), the cause degrades to unknown instead of guessing.
func TestCacheForensicsAttribution_RingEvictionDegradesToUnknown(t *testing.T) {
	j := newCacheForensicsJournal()
	base := time.Now()
	// Sequence A, request 1 (the would-be predecessor)...
	recordAttributed(t, j, 1, 40000, false, RequestFingerprint{}, base, base.Add(time.Second))
	// ...then six requests of OTHER sequences (ring capacity is 6, so the
	// sequence-A entry is evicted before its next request completes)...
	for i := 1; i <= 6; i++ {
		recordAttributedSession(t, j, "other", 100+i, 500, false, RequestFingerprint{},
			base.Add(time.Duration(i)*time.Second), base.Add(time.Duration(i+1)*time.Second))
	}
	// ...then sequence A's miss: its predecessor is no longer retained.
	recordAttributed(t, j, 2, 0, false, RequestFingerprint{}, base.Add(8*time.Second), base.Add(9*time.Second))

	reports := j.reportsSnapshot()
	require.Len(t, reports, 1)
	r := reports[0]
	require.Equal(t, LikelyCauseUnknown, r.LikelyCause)
	require.Zero(t, r.GapSincePrevResponseMs)
	require.Zero(t, r.PrevTotalPromptTokens)
	require.Len(t, r.Requests, 1, "only the miss itself is retained")
}

// TestCacheForensicsNoticeCarriesCause verifies the likely cause reaches the
// drainable notice (the agent log line's actionable tag).
func TestCacheForensicsNoticeCarriesCause(t *testing.T) {
	j := newCacheForensicsJournal()
	base := time.Now()
	recordAttributed(t, j, 1, 38144, true, RequestFingerprint{}, base, base.Add(time.Second))
	recordAttributed(t, j, 2, 7872, true, RequestFingerprint{}, base.Add(time.Second+300*time.Millisecond), base.Add(2*time.Second))

	notices := j.takeNotices()
	require.Len(t, notices, 1)
	require.Equal(t, LikelyCauseServerEviction, notices[0].LikelyCause)
	require.Equal(t, 38144, notices[0].PrevCacheRead)
	require.Equal(t, 7872, notices[0].CacheRead)
}

// TestCacheForensicsAttribution_ReplaysObservedSignatures replays the exact
// checkpoint ladders observed in the 2026-08-19 exports/live session and
// asserts the attribution machinery would have self-diagnosed them: every
// miss lands on an older retained checkpoint with a short gap.
func TestCacheForensicsAttribution_ReplaysObservedSignatures(t *testing.T) {
	// Live session 1787090484 requests 4..10, shortened to the ring's
	// capacity (5 climbs + the miss): 7872, 11456, 23168, 32448, 38144,
	// then the miss back to 7872 (the first request's prefix).
	ladder := []int{7872, 11456, 23168, 32448, 38144}
	j := newCacheForensicsJournal()
	base := time.Now()
	for i, read := range ladder {
		sent := base.Add(time.Duration(i) * 5 * time.Second)
		recordAttributed(t, j, i+1, read, true, RequestFingerprint{}, sent, sent.Add(2*time.Second))
	}
	// Miss: 400ms after the previous response completed.
	recordAttributed(t, j, 6, 7872, true, RequestFingerprint{}, base.Add(4*5*time.Second+2*time.Second+400*time.Millisecond), base.Add(25*time.Second))

	reports := j.reportsSnapshot()
	require.Len(t, reports, 1)
	r := reports[0]
	require.Equal(t, LikelyCauseServerEviction, r.LikelyCause)
	require.EqualValues(t, 1, r.PartialHitPrevSeq, "fell back to request 1's checkpoint (7872)")
	require.Equal(t, int64(400), r.GapSincePrevResponseMs)
}
