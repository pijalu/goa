package provider

import (
	"testing"

	"github.com/pijalu/goa/internal/agentic/provider/schema"
)

func TestCacheForensicsRecordCarriesBoundedFingerprint(t *testing.T) {
	j := newCacheForensicsJournal()
	model := schema.Model{Provider: schema.ProviderCustom, ID: "m"}
	fp := BuildRequestFingerprint("custom", "m", "secret-session", nil, []byte(`{"input":[]}`), 3, 2, "sse", "turn-1", false)
	rec := j.record(model, "opaque-key", "system", "https://example.test", []byte(`{"input":[]}`), fp)
	entry := j.findBySeqLocked(rec.seq)
	if entry == nil || entry.Fingerprint.Classification != PrefixNoPredecessor {
		t.Fatalf("fingerprint was not retained: %#v", entry)
	}
	if entry.Fingerprint.SessionKeyHash == "opaque-key" || entry.Fingerprint.SessionKeyHash == "secret-session" {
		t.Fatal("fingerprint retained a raw session identifier")
	}
}
