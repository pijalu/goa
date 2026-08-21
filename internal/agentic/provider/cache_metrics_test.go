package provider

import (
	"testing"

	"github.com/pijalu/goa/internal/agentic/provider/schema"
	"github.com/stretchr/testify/require"
)

func TestCacheForensicsMetricsSnapshot(t *testing.T) {
	j := newCacheForensicsJournal()
	model := schema.Model{Provider: schema.ProviderCustom, ID: "mock"}
	firstBody := []byte(`{"input":"one"}`)
	firstFP := BuildRequestFingerprint("custom", "mock", "session-a", nil, firstBody, 2, 3, "sse", "turn-1", false)
	first := j.record(model, "session-a", "system", "https://example.test", firstBody, firstFP)
	first.complete(&schema.Usage{InputTokens: 8, CacheReadTokens: 10, CacheCreationTokens: 4})
	secondBody := []byte(`{"input":"two"}`)
	secondFP := BuildRequestFingerprint("custom", "mock", "session-b", firstBody, secondBody, 4, 5, "sse", "turn-2", true)
	second := j.record(model, "session-b", "system", "https://example.test", secondBody, secondFP)
	second.complete(&schema.Usage{InputTokens: 9, CacheReadTokens: 0, CacheCreationTokens: 6})

	metrics := j.metrics
	require.Equal(t, int64(2), metrics.Requests)
	require.Equal(t, int64(len(firstBody)+len(secondBody)), metrics.SerializedBytes)
	require.Equal(t, int64(10), metrics.CacheReadTokens)
	require.Equal(t, int64(10), metrics.CacheWriteTokens)
	require.Equal(t, int64(1), metrics.CacheKeyChanges)
	require.Equal(t, secondFP, metrics.LastFingerprint)

	ResetCacheForensics()
	RecordCompaction()
	RecordToolSchemaHash("schema-hash")
	global := CacheForensicsMetricsSnapshot()
	require.Equal(t, int64(1), global.CompactionCount)
	require.Equal(t, "schema-hash", global.ToolSchemaHash)
}

func TestCacheForensicsRequestSnapshotIsImmutable(t *testing.T) {
	j := newCacheForensicsJournal()
	model := schema.Model{Provider: schema.ProviderCustom, ID: "mock"}
	body := []byte(`{"input":"original"}`)
	rec := j.record(model, "s", "sys", "https://example.test", body)
	body[10] = 'X'
	entry := j.findBySeqLocked(rec.seq)
	require.NotNil(t, entry)
	require.Equal(t, `{"input":"original"}`, string(entry.Body))
}

func TestCacheForensicsUsageSnapshotIsImmutable(t *testing.T) {
	j := newCacheForensicsJournal()
	rec := j.record(schema.Model{ID: "mock"}, "s", "sys", "https://example.test", []byte(`{"x":1}`))
	usage := &schema.Usage{InputTokens: 2, CacheReadTokens: 3}
	rec.complete(usage)
	usage.CacheReadTokens = 99
	entry := j.findBySeqLocked(rec.seq)
	require.NotNil(t, entry)
	require.Equal(t, 3, entry.Usage.CacheReadTokens)
}
