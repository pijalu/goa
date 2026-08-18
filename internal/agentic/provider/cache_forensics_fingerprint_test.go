package provider

import (
	"testing"

	"github.com/pijalu/goa/internal/agentic/provider/schema"
)

func TestCacheForensicsRequestBodySnapshotSurvivesCallerMutation(t *testing.T) {
	j := newCacheForensicsJournal()
	model := schema.Model{Provider: schema.ProviderOpenAI, ID: "gpt-5-codex"}
	body := []byte(`{"model":"gpt-5-codex","input":[{"role":"user","content":"first"}]}`)
	first := j.record(model, "cache-key", "system", "https://example.test/codex", body)
	body[0] = 'X'
	body[len(body)-3] = 'X'
	first.complete(&schema.Usage{InputTokens: 1, CacheReadTokens: 2048})

	secondBody := []byte(`{"model":"gpt-5-codex","input":[{"role":"user","content":"second"}]}`)
	second := j.record(model, "cache-key", "system", "https://example.test/codex", secondBody)
	secondBody[0] = 'X'
	second.complete(&schema.Usage{InputTokens: 1, CacheReadTokens: 0})

	reports := j.reportsSnapshot()
	if len(reports) != 1 || len(reports[0].Requests) != 2 {
		t.Fatalf("reports = %#v, want one two-request report", reports)
	}
	if string(reports[0].Requests[0].Body) != `{"model":"gpt-5-codex","input":[{"role":"user","content":"first"}]}` {
		t.Fatalf("recorded predecessor mutated: %s", reports[0].Requests[0].Body)
	}
	if string(reports[0].Requests[1].Body) != `{"model":"gpt-5-codex","input":[{"role":"user","content":"second"}]}` {
		t.Fatalf("recorded request mutated: %s", reports[0].Requests[1].Body)
	}
}

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
