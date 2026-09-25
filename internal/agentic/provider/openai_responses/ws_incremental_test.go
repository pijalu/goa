// SPDX-License-Identifier: GPL-3.0-or-later

package openairesponses

import (
	"encoding/json"
	"testing"

	"github.com/pijalu/goa/internal/agentic/provider"
)

// boolPtr is a small test helper for optional bool fields.
func boolPtr(v bool) *bool { return &v }

// baseFingerprint returns a fully-populated fingerprint that tests mutate one
// field at a time to prove the property match is exhaustive.
func baseFingerprint() requestFingerprint {
	return requestFingerprint{
		Model:             "gpt-5-codex",
		Instructions:      "You are a helpful assistant.",
		Tools:             json.RawMessage(`[{"type":"function","name":"bash"}]`),
		ToolChoice:        "auto",
		ParallelToolCalls: boolPtr(true),
		Reasoning:         json.RawMessage(`{"summary":"auto"}`),
		Store:             boolPtr(false),
		Stream:            true,
		Include:           json.RawMessage(`["reasoning.encrypted_content"]`),
		ServiceTier:       "default",
		PromptCacheKey:    "sess-abc",
		Text:              json.RawMessage(`{"verbosity":"low"}`),
	}
}

// TestRequestPropertiesMatchExhaustive runs one subtest per comparable field:
// the unchanged fingerprint must match, and each single-field change must
// break the match. A field added to the struct without a matching subtest will
// fail the "unchanged" case by omission only if it defaults non-equal — the
// table forces an explicit decision per field.
func TestRequestPropertiesMatchExhaustive(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*requestFingerprint)
		want   bool
	}{
		{"unchanged", func(*requestFingerprint) {}, true},
		{"model", func(f *requestFingerprint) { f.Model = "gpt-5" }, false},
		{"instructions", func(f *requestFingerprint) { f.Instructions = "other" }, false},
		{"tools", func(f *requestFingerprint) { f.Tools = json.RawMessage(`[{"type":"function","name":"read"}]`) }, false},
		{"tool_choice", func(f *requestFingerprint) { f.ToolChoice = "none" }, false},
		{"parallel_tool_calls", func(f *requestFingerprint) { f.ParallelToolCalls = boolPtr(false) }, false},
		{"parallel_tool_calls_unset", func(f *requestFingerprint) { f.ParallelToolCalls = nil }, false},
		{"reasoning", func(f *requestFingerprint) { f.Reasoning = json.RawMessage(`{"summary":"off"}`) }, false},
		{"store", func(f *requestFingerprint) { f.Store = boolPtr(true) }, false},
		{"stream", func(f *requestFingerprint) { f.Stream = false }, false},
		{"include", func(f *requestFingerprint) { f.Include = json.RawMessage(`[]`) }, false},
		{"service_tier", func(f *requestFingerprint) { f.ServiceTier = "flex" }, false},
		{"prompt_cache_key", func(f *requestFingerprint) { f.PromptCacheKey = "sess-xyz" }, false},
		{"text", func(f *requestFingerprint) { f.Text = json.RawMessage(`{"verbosity":"high"}`) }, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cur := baseFingerprint()
			tc.mutate(&cur)
			if got := requestPropertiesMatch(baseFingerprint(), cur); got != tc.want {
				t.Fatalf("requestPropertiesMatch() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestRequestPropertiesMatchCanonicalJSON verifies structured fields compare
// order-insensitively: the same tools object with keys in a different order
// still matches, and whitespace differences do not break equality.
func TestRequestPropertiesMatchCanonicalJSON(t *testing.T) {
	prev := baseFingerprint()
	cur := baseFingerprint()
	// Same logical tools value, different key order + whitespace.
	cur.Tools = json.RawMessage("[ { \"name\":\"bash\", \"type\":\"function\" } ]")
	// Same reasoning value with extra whitespace.
	cur.Reasoning = json.RawMessage(`{ "summary": "auto" }`)
	if !requestPropertiesMatch(prev, cur) {
		t.Fatal("requestPropertiesMatch() = false, want true for canonically-equal JSON")
	}
}

// deltaBaseline builds a baseline with two last-input messages and one added
// (assistant) output item, so the prefix rule must account for addedItems.
func deltaBaseline() *wsSessionBaseline {
	return &wsSessionBaseline{
		LastInput: []provider.Message{
			provider.NewUserMessage("u1"),
			provider.NewUserMessage("u2"),
		},
		ResponseID: "resp-1",
		AddedItems: []provider.Message{
			provider.NewAssistantMessage([]provider.ContentBlock{{Type: provider.ContentBlockText, Text: "a1"}}),
		},
	}
}

// appendInput returns the baseline's known conversation (u1, u2, a1) plus the
// supplied tail — the canonical strict-append request shape.
func appendInput(tail ...provider.Message) []provider.Message {
	b := deltaBaseline()
	in := append([]provider.Message{}, b.LastInput...)
	in = append(in, b.AddedItems...)
	return append(in, tail...)
}

// TestComputeDeltaStrictAppend covers the append/divergence matrix: exact
// append, shorter input, prefix mismatch (rewritten middle), divergence in an
// added item, and the post-compaction replacement shape.
func TestComputeDeltaStrictAppend(t *testing.T) {
	newTail := provider.NewUserMessage("u3")

	cases := []struct {
		name    string
		input   []provider.Message
		wantOK  bool
		wantLen int
	}{
		{"exact_append_one", appendInput(newTail), true, 1},
		{"exact_append_two", appendInput(newTail, provider.NewUserMessage("u4")), true, 2},
		{"empty_delta", appendInput(), true, 0},
		{"shorter_than_baseline", []provider.Message{provider.NewUserMessage("u1")}, false, 0},
		{"prefix_mismatch_rewritten", appendInputMismatch(), false, 0},
		{"added_item_diverged", appendAddedDiverged(), false, 0},
		{"compaction_replacement", replacementInput(), false, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			delta, ok := computeDelta(deltaBaseline(), tc.input)
			if ok != tc.wantOK {
				t.Fatalf("computeDelta() ok = %v, want %v", ok, tc.wantOK)
			}
			if ok && len(delta) != tc.wantLen {
				t.Fatalf("computeDelta() delta len = %d, want %d", len(delta), tc.wantLen)
			}
		})
	}
}

// appendInputMismatch builds an append-shaped input whose first message is
// rewritten, so the prefix check must fail even though the length is valid.
func appendInputMismatch() []provider.Message {
	in := appendInput(provider.NewUserMessage("u3"))
	in[0] = provider.NewUserMessage("u1-EDITED")
	return in
}

// appendAddedDiverged keeps the two last-input messages but alters the added
// assistant item, so the prefix check fails on the response-derived portion.
func appendAddedDiverged() []provider.Message {
	in := appendInput(provider.NewUserMessage("u3"))
	in[2] = provider.NewAssistantMessage([]provider.ContentBlock{{Type: provider.ContentBlockText, Text: "a1-CHANGED"}})
	return in
}

// replacementInput is the post-compaction shape: a fresh summary message plus
// retained tail, not a strict append of the baseline.
func replacementInput() []provider.Message {
	return []provider.Message{
		provider.NewUserMessage("[summary of earlier turns]"),
		provider.NewUserMessage("u2"),
	}
}

// TestComputeDeltaIgnoresInternalMetadata verifies that messages differing only
// in internal metadata (Usage / Extra / provenance) still satisfy the prefix
// check — mirroring response_items_equal_ignoring_internal_metadata.
func TestComputeDeltaIgnoresInternalMetadata(t *testing.T) {
	baseline := deltaBaseline()
	input := appendInput(provider.NewUserMessage("u3"))
	// Stamp transient metadata onto a prefix message; it must not break the match.
	input[0].Extra = map[string]interface{}{"cache_control": "ephemeral"}
	input[0].SourceModelID = "gpt-5-codex"
	delta, ok := computeDelta(baseline, input)
	if !ok || len(delta) != 1 {
		t.Fatalf("computeDelta() = (%v, %v), want ok with 1-item delta despite internal metadata", len(delta), ok)
	}
}

// TestDecideIncremental exercises the combined decision: baseline presence,
// response id presence, property match, prefix match, and the empty-delta flag.
func TestDecideIncremental(t *testing.T) {
	props := baseFingerprint()
	mismatched := baseFingerprint()
	mismatched.Model = "gpt-5"

	newTail := appendInput(provider.NewUserMessage("u3"))

	cases := []struct {
		name            string
		baseline        *wsSessionBaseline
		prevProps       requestFingerprint
		curProps        requestFingerprint
		input           []provider.Message
		allowEmptyDelta bool
		wantOK          bool
		wantResponseID  string
		wantDeltaLen    int
	}{
		{"ok_append", deltaBaseline(), props, props, newTail, false, true, "resp-1", 1},
		{"nil_baseline", nil, props, props, newTail, false, false, "", 0},
		{"empty_response_id", noResponseIDBaseline(), props, props, newTail, false, false, "", 0},
		{"property_mismatch", deltaBaseline(), props, mismatched, newTail, false, false, "", 0},
		{"prefix_mismatch", deltaBaseline(), props, props, appendInputMismatch(), false, false, "", 0},
		{"empty_delta_disallowed", deltaBaseline(), props, props, appendInput(), false, false, "", 0},
		{"empty_delta_allowed", deltaBaseline(), props, props, appendInput(), true, true, "resp-1", 0},
		{"compaction_replacement", deltaBaseline(), props, props, replacementInput(), false, false, "", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			delta, responseID, ok := decideIncremental(tc.baseline, tc.prevProps, tc.curProps, tc.input, tc.allowEmptyDelta)
			if ok != tc.wantOK {
				t.Fatalf("decideIncremental() ok = %v, want %v", ok, tc.wantOK)
			}
			if responseID != tc.wantResponseID {
				t.Fatalf("decideIncremental() responseID = %q, want %q", responseID, tc.wantResponseID)
			}
			if ok && len(delta) != tc.wantDeltaLen {
				t.Fatalf("decideIncremental() delta len = %d, want %d", len(delta), tc.wantDeltaLen)
			}
		})
	}
}

// noResponseIDBaseline returns a baseline with a valid prefix but no response
// id, which must force ok=false (nothing to chain by).
func noResponseIDBaseline() *wsSessionBaseline {
	b := deltaBaseline()
	b.ResponseID = ""
	return b
}
