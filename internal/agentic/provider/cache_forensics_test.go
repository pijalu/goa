// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package provider

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/pijalu/goa/internal/agentic/provider/schema"
	"github.com/pijalu/goa/internal/agentic/provider/transport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCacheSeqStateObserve is the detection matrix mirroring the app-layer
// cache-bust rules: cold starts are not misses, established caches that go
// cold or collapse beyond the wobble tolerance are.
func TestCacheSeqStateObserve(t *testing.T) {
	tests := []struct {
		name      string
		reads     []int
		wantMiss  []bool
	}{
		{"cold start zero is not a miss", []int{0}, []bool{false}},
		{"first establishment", []int{100}, []bool{false}},
		{"stable hot cache", []int{100, 100, 100}, []bool{false, false, false}},
		{"growing prefix", []int{100, 200, 300}, []bool{false, false, false}},
		{"zero after established", []int{100, 0}, []bool{false, true}},
		{"zero without establishment", []int{0, 0, 0}, []bool{false, false, false}},
		{"wobble within tolerance", []int{5000, 5000 - cacheForensicsDropToleranceTokens}, []bool{false, false}},
		{"drop beyond tolerance", []int{5000, 5000 - cacheForensicsDropToleranceTokens - 1}, []bool{false, true}},
		{"recovery re-arms", []int{100, 0, 50, 50}, []bool{false, true, false, false}},
		{"miss then growth ok", []int{8000, 100, 200}, []bool{false, true, false}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := &cacheSeqState{}
			require.Len(t, tt.wantMiss, len(tt.reads))
			for i, read := range tt.reads {
				if got := st.observe(read); got != tt.wantMiss[i] {
					t.Errorf("step %d (read=%d): miss=%v, want %v", i, read, got, tt.wantMiss[i])
				}
			}
		})
	}
}

// forensicsUsage is a shorthand for building usage with cache reads.
func forensicsUsage(cacheRead int) *schema.Usage {
	return &schema.Usage{InputTokens: 10, OutputTokens: 5, CacheReadTokens: cacheRead}
}

// recordPair records one request on j and completes it with cacheRead.
func recordPair(t *testing.T, j *cacheForensicsJournal, model schema.Model, sessionID, sysPrompt string, bodyN, cacheRead int) {
	t.Helper()
	rec := j.record(model, sessionID, sysPrompt, "http://x/v1/chat/completions", []byte(fmt.Sprintf(`{"model":%q,"n":%d}`, model.ID, bodyN)))
	rec.complete(forensicsUsage(cacheRead))
}

func TestCacheForensicsReportCapturesMissAndPredecessor(t *testing.T) {
	j := newCacheForensicsJournal()
	model := schema.Model{ID: "m1", Provider: schema.ProviderCustom}

	recordPair(t, j, model, "s1", "sys", 1, 100) // establishes
	recordPair(t, j, model, "s1", "sys", 2, 0)   // miss

	reports := j.reportsSnapshot()
	require.Len(t, reports, 1)
	rep := reports[0]
	assert.Equal(t, int64(1), rep.ID)
	assert.Equal(t, "m1", rep.Model)
	assert.Equal(t, 100, rep.PrevCacheRead)
	assert.Equal(t, 0, rep.CacheRead)

	require.Len(t, rep.Requests, 2, "report must hold the request before the miss and the miss")
	assert.Contains(t, string(rep.Requests[0].Body), `"n":1`)
	assert.Contains(t, string(rep.Requests[1].Body), `"n":2`)
	assert.Less(t, rep.Requests[0].Seq, rep.Requests[1].Seq)
	require.NotNil(t, rep.Requests[0].Usage)
	require.NotNil(t, rep.Requests[1].Usage)
	assert.Equal(t, 100, rep.Requests[0].Usage.CacheReadTokens)
	assert.Equal(t, 0, rep.Requests[1].Usage.CacheReadTokens)

	// The complete bodies survive marshaling as nested JSON (not escaped).
	raw, err := json.Marshal(rep)
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"body":{"model":"m1","n":1}`)

	notices := j.takeNotices()
	require.Len(t, notices, 1)
	assert.Equal(t, rep.ID, notices[0].ReportID)
	assert.Equal(t, "m1", notices[0].Model)
	assert.Nil(t, j.takeNotices(), "notices are drained by the first take")
}

func TestCacheForensicsSequenceIsolation(t *testing.T) {
	j := newCacheForensicsJournal()
	m1 := schema.Model{ID: "m1", Provider: schema.ProviderCustom}
	m2 := schema.Model{ID: "m2", Provider: schema.ProviderCustom}

	recordPair(t, j, m1, "s1", "sys-a", 1, 100) // s1 establishes
	recordPair(t, j, m2, "s2", "sys-b", 2, 200) // s2 establishes
	recordPair(t, j, m1, "s1", "sys-a", 3, 0)   // s1 misses

	reports := j.reportsSnapshot()
	require.Len(t, reports, 1, "other sequences must not interfere")
	rep := reports[0]
	require.Len(t, rep.Requests, 2)
	assert.Contains(t, string(rep.Requests[0].Body), `"n":1`,
		"predecessor must be the same sequence's request, not the interleaved other-sequence one")
	assert.Equal(t, 100, rep.PrevCacheRead)

	// A rotated SessionID is a NEW sequence: its cold start is not a miss.
	recordPair(t, j, m1, "s1-rotated", "sys-a", 4, 0)
	assert.Len(t, j.reportsSnapshot(), 1, "fresh conversation cold start must not report")
}

func TestCacheForensicsColdStartAndEmptyUsage(t *testing.T) {
	j := newCacheForensicsJournal()
	model := schema.Model{ID: "m1", Provider: schema.ProviderCustom}

	// Providers reporting no cache stats never establish a baseline.
	rec := j.record(model, "s1", "sys", "http://x", []byte(`{"n":1}`))
	rec.complete(&schema.Usage{})
	rec = j.record(model, "s1", "sys", "http://x", []byte(`{"n":2}`))
	rec.complete(nil)
	recordPair(t, j, model, "s1", "sys", 3, 0) // zero reads, never established
	assert.Empty(t, j.reportsSnapshot())

	// Once stats appear, establishment + bust work.
	recordPair(t, j, model, "s1", "sys", 4, 80)
	recordPair(t, j, model, "s1", "sys", 5, 0)
	assert.Len(t, j.reportsSnapshot(), 1)
}

func TestCacheForensicsBaselineReset(t *testing.T) {
	j := newCacheForensicsJournal()
	model := schema.Model{ID: "m1", Provider: schema.ProviderCustom}

	recordPair(t, j, model, "s1", "sys", 1, 100) // establish
	j.resetBaseline()                            // /clear or context reset
	recordPair(t, j, model, "s1", "sys", 2, 0)   // cold start of the new conversation
	assert.Empty(t, j.reportsSnapshot(), "re-armed baseline must not flag the cold start")
}

func TestCacheForensicsPrevSkipsRequestsWithoutUsage(t *testing.T) {
	j := newCacheForensicsJournal()
	model := schema.Model{ID: "m1", Provider: schema.ProviderCustom}

	recordPair(t, j, model, "s1", "sys", 1, 100) // establish
	// A failed request between: recorded but never completed with usage.
	_ = j.record(model, "s1", "sys", "http://x", []byte(`{"n":2}`))
	recordPair(t, j, model, "s1", "sys", 3, 0) // miss

	reports := j.reportsSnapshot()
	require.Len(t, reports, 1)
	require.Len(t, reports[0].Requests, 2)
	assert.Contains(t, string(reports[0].Requests[0].Body), `"n":1`,
		"predecessor must be the last request WITH usage (failed request carries no cache signal)")
}

func TestCacheForensicsRingEviction(t *testing.T) {
	j := newCacheForensicsJournal()
	model := schema.Model{ID: "m1", Provider: schema.ProviderCustom}

	// Overflow the ring: 6 slots, record 8. The first two are evicted.
	recs := make([]*cacheForensicsRecord, 8)
	for i := range recs {
		recs[i] = j.record(model, "s1", "sys", "http://x", []byte(fmt.Sprintf(`{"n":%d}`, i)))
	}
	assert.Equal(t, cacheForensicsRingCapacity, j.count)

	// Completing an evicted record is a no-op (no panic, no detection state).
	recs[0].complete(forensicsUsage(100))
	recs[1].complete(forensicsUsage(200))
	assert.Empty(t, j.seqState, "evicted entries must not create baselines")

	// Live records still detect: establish on #6, miss on #7.
	recs[6].complete(forensicsUsage(100))
	recs[7].complete(forensicsUsage(0))
	reports := j.reportsSnapshot()
	require.Len(t, reports, 1)
	// The predecessor (entry #6) is still in the ring.
	require.Len(t, reports[0].Requests, 2)
	assert.Contains(t, string(reports[0].Requests[0].Body), `"n":6`)
}

func TestCacheForensicsReportCapacityEvictsOldest(t *testing.T) {
	j := newCacheForensicsJournal()
	model := schema.Model{ID: "m1", Provider: schema.ProviderCustom}

	for cycle := 0; cycle < cacheForensicsReportCapacity+1; cycle++ {
		recordPair(t, j, model, "s1", "sys", cycle*2, 100)
		recordPair(t, j, model, "s1", "sys", cycle*2+1, 0)
		j.resetBaseline() // re-arm so each cycle's zero is a fresh miss
	}
	reports := j.reportsSnapshot()
	assert.Len(t, reports, cacheForensicsReportCapacity, "reports are capped")
	ids := make([]int64, 0, len(reports))
	for _, r := range reports {
		ids = append(ids, r.ID)
	}
	assert.Equal(t, []int64{2, 3, 4, 5}, ids, "oldest reports are evicted first")
}

func TestCacheForensicsInvalidJSONBody(t *testing.T) {
	j := newCacheForensicsJournal()
	model := schema.Model{ID: "m1", Provider: schema.ProviderCustom}

	rec := j.record(model, "s1", "sys", "http://x", []byte("not-json"))
	rec.complete(forensicsUsage(100))
	rec = j.record(model, "s1", "sys", "http://x", []byte("not-json"))
	rec.complete(forensicsUsage(0))

	reports := j.reportsSnapshot()
	require.Len(t, reports, 1)
	raw, err := json.Marshal(reports[0])
	require.NoError(t, err, "non-JSON bodies must be quoted so report marshaling cannot break")
	assert.Contains(t, string(raw), `"body":"not-json"`)
}

// TestCacheForensicsEndToEnd drives the full generic runtime (protocol +
// transport + journal) twice and expects exactly one miss report holding the
// two COMPLETE wire requests.
func TestCacheForensicsEndToEnd(t *testing.T) {
	ResetCacheForensics()
	defer ResetCacheForensics()
	old := transport.Default()
	defer transport.SetDefault(old)

	sse := func(cachedTokens int) string {
		return `data: {"choices":[{"index":0,"delta":{"content":"ok"},"finish_reason":"stop"}]}` + "\n\n" +
			fmt.Sprintf(`data: {"choices":[{"index":0,"delta":{}}],"usage":{"prompt_tokens":200,"completion_tokens":5,"prompt_tokens_details":{"cached_tokens":%d}}}`, cachedTokens) + "\n\n" +
			`data: [DONE]` + "\n\n"
	}
	transport.SetDefault(&mockTransport{
		status: 200,
		header: map[string]string{"Content-Type": "text/event-stream"},
		body:   sse(100),
	})

	model := schema.Model{
		ID:       "kimi-k2",
		Api:      schema.ApiOpenAICompletions,
		Provider: schema.ProviderKimiCode,
		BaseURL:  "http://example.com/v1/chat/completions",
	}
	call := func() {
		stream, err := GenericStream(model,
			schema.Context{SystemPrompt: "sys", Messages: []schema.Message{schema.NewUserMessage("hi")}},
			schema.StreamOptions{SessionID: "sess-1"})
		require.NoError(t, err)
		require.NoError(t, stream.Err())
		require.NotNil(t, stream.Result())
	}

	call() // establishes the cache baseline (cache_read 100)

	transport.SetDefault(&mockTransport{
		status: 200,
		header: map[string]string{"Content-Type": "text/event-stream"},
		body:   sse(0), // cache bust
	})
	call()

	// The journal attaches usage just after the stream closes; wait for it.
	require.Eventually(t, func() bool {
		return len(CacheForensicsReports()) == 1
	}, 2*time.Second, 5*time.Millisecond, "expected exactly one cache-miss report")

	reports := CacheForensicsReports()
	rep := reports[0]
	assert.Equal(t, "kimi-k2", rep.Model)
	assert.Equal(t, 100, rep.PrevCacheRead)
	assert.Equal(t, 0, rep.CacheRead)
	require.Len(t, rep.Requests, 2, "the miss and the request before it")
	for _, req := range rep.Requests {
		assert.Contains(t, string(req.Body), `"messages"`, "complete wire request, not a truncated tail")
		assert.Equal(t, "http://example.com/v1/chat/completions", req.URL)
	}
	// Both requests are complete and distinct rounds of the same body shape.
	assert.Equal(t, rep.Requests[0].Body, rep.Requests[1].Body,
		"same conversation resent; only the provider-side cache state changed")

	notices := TakeCacheMissNotices()
	require.Len(t, notices, 1)
	assert.Equal(t, rep.ID, notices[0].ReportID)

	// Global baseline reset re-arms: a third cold call reports nothing new.
	ResetCacheForensicsBaseline()
	transport.SetDefault(&mockTransport{
		status: 200,
		header: map[string]string{"Content-Type": "text/event-stream"},
		body:   sse(0),
	})
	call()
	assert.Len(t, CacheForensicsReports(), 1)
}
