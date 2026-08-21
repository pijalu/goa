// SPDX-License-Identifier: GPL-3.0-or-later

package openairesponses

import (
	"bytes"
	"encoding/json"
	"reflect"

	"github.com/pijalu/goa/internal/agentic/provider"
)

// requestFingerprint captures the comparable non-input fields of a built
// Codex WebSocket request. It mirrors Codex's responses_request_properties_match
// field set exactly (client.rs): model, instructions, tools, tool_choice,
// parallel_tool_calls, reasoning, store, stream, include, service_tier,
// prompt_cache_key, text. The input is deliberately excluded — it is compared
// separately as a strict-append prefix (computeDelta). Keep this exhaustive so
// a new request body field added in protocol/openai_responses.go forces an
// explicit reuse decision here.
//
// Structured body values (tools, reasoning, include, text) are carried as
// canonical JSON so equality is order-insensitive and schema-agnostic.
type requestFingerprint struct {
	Model             string
	Instructions      string
	Tools             json.RawMessage
	ToolChoice        string
	ParallelToolCalls *bool
	Reasoning         json.RawMessage
	Store             *bool
	Stream            bool
	Include           json.RawMessage
	ServiceTier       string
	PromptCacheKey    string
	Text              json.RawMessage
}

// fingerprintFromBody extracts the comparable non-input properties of a built
// request body into a requestFingerprint. Structured values (tools, reasoning,
// include, text) are carried as canonical-JSON raw messages so the comparison
// in requestPropertiesMatch is order-insensitive. Pure.
func fingerprintFromBody(body map[string]interface{}) requestFingerprint {
	return requestFingerprint{
		Model:             stringField(body, "model"),
		Instructions:      stringField(body, "instructions"),
		Tools:             rawJSONField(body, "tools"),
		ToolChoice:        stringField(body, "tool_choice"),
		ParallelToolCalls: boolPtrField(body, "parallel_tool_calls"),
		Reasoning:         rawJSONField(body, "reasoning"),
		Store:             boolPtrField(body, "store"),
		Stream:            boolField(body, "stream"),
		Include:           rawJSONField(body, "include"),
		ServiceTier:       stringField(body, "service_tier"),
		PromptCacheKey:    stringField(body, "prompt_cache_key"),
		Text:              rawJSONField(body, "text"),
	}
}

func stringField(body map[string]interface{}, key string) string {
	s, _ := body[key].(string)
	return s
}

func boolField(body map[string]interface{}, key string) bool {
	b, _ := body[key].(bool)
	return b
}

func boolPtrField(body map[string]interface{}, key string) *bool {
	v, ok := body[key]
	if !ok {
		return nil
	}
	b, isBool := v.(bool)
	if !isBool {
		return nil
	}
	return &b
}

// rawJSONField re-marshals a structured body value into canonical JSON so the
// fingerprint comparison is order-insensitive. Absent values stay nil.
func rawJSONField(body map[string]interface{}, key string) json.RawMessage {
	v, ok := body[key]
	if !ok || v == nil {
		return nil
	}
	data, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return data
}

// requestPropertiesMatch reports whether prev and cur agree on every
// comparable non-input request property. A true result means the current
// request can reference prev's response via previous_response_id. Pure and
// deterministic.
func requestPropertiesMatch(prev, cur requestFingerprint) bool {
	return prev.Model == cur.Model &&
		prev.Instructions == cur.Instructions &&
		canonicalJSONEqual(prev.Tools, cur.Tools) &&
		prev.ToolChoice == cur.ToolChoice &&
		boolPtrEqual(prev.ParallelToolCalls, cur.ParallelToolCalls) &&
		canonicalJSONEqual(prev.Reasoning, cur.Reasoning) &&
		boolPtrEqual(prev.Store, cur.Store) &&
		prev.Stream == cur.Stream &&
		canonicalJSONEqual(prev.Include, cur.Include) &&
		prev.ServiceTier == cur.ServiceTier &&
		prev.PromptCacheKey == cur.PromptCacheKey &&
		canonicalJSONEqual(prev.Text, cur.Text)
}

// computeDelta decides whether newInput is a strict append of the baseline's
// known conversation (lastInput + addedItems) and, if so, returns the appended
// tail. Server-returned output items (addedItems) count toward the baseline
// length so they are not resent. Pure and deterministic.
//
// prevLen = len(lastInput) + len(addedItems); if len(newInput) < prevLen the
// request cannot be a delta. newInput[:prevLen] must equal lastInput +
// addedItems item-by-item, ignoring internal metadata. A compaction (history
// replacement) naturally fails the prefix check, forcing a full send.
func computeDelta(baseline *wsSessionBaseline, newInput []provider.Message) (delta []provider.Message, ok bool) {
	prevLen := len(baseline.LastInput) + len(baseline.AddedItems)
	if len(newInput) < prevLen {
		return nil, false
	}
	if !itemsMatchPrefix(baseline, newInput[:prevLen]) {
		return nil, false
	}
	return newInput[prevLen:], true
}

// decideIncremental combines the property match and the strict-append prefix
// delta into a single decision: can curReq be sent as an incremental delta
// chained by the baseline's response id? It returns ok=false on: no baseline,
// empty baseline response id, property mismatch, prefix mismatch, or an empty
// delta when empty deltas are disallowed. Pure and deterministic.
func decideIncremental(baseline *wsSessionBaseline, prevProps, curProps requestFingerprint, newInput []provider.Message, allowEmptyDelta bool) (delta []provider.Message, responseID string, ok bool) {
	if baseline == nil || baseline.ResponseID == "" {
		return nil, "", false
	}
	if !requestPropertiesMatch(prevProps, curProps) {
		return nil, "", false
	}
	delta, ok = computeDelta(baseline, newInput)
	if !ok {
		return nil, "", false
	}
	if !allowEmptyDelta && len(delta) == 0 {
		return nil, "", false
	}
	return delta, baseline.ResponseID, true
}

// itemsMatchPrefix reports whether the baseline's known conversation (lastInput
// followed by addedItems) equals the leading prevLen items of current,
// compared item-by-item ignoring internal metadata.
func itemsMatchPrefix(baseline *wsSessionBaseline, current []provider.Message) bool {
	prev := make([]provider.Message, 0, len(baseline.LastInput)+len(baseline.AddedItems))
	prev = append(prev, baseline.LastInput...)
	prev = append(prev, baseline.AddedItems...)
	if len(prev) != len(current) {
		return false
	}
	for i := range prev {
		if !messagesEqualIgnoringInternalMetadata(prev[i], current[i]) {
			return false
		}
	}
	return true
}

// messagesEqualIgnoringInternalMetadata mirrors Codex's
// response_items_equal_ignoring_internal_metadata: two messages are equal when
// their role and content blocks match, ignoring transient internal metadata
// (Usage, StopReason, Extra, and the Source* provenance fields) that is not
// part of the wire item and may legitimately differ between turns.
func messagesEqualIgnoringInternalMetadata(a, b provider.Message) bool {
	return a.Role == b.Role && reflect.DeepEqual(a.Content, b.Content)
}

// canonicalJSONEqual compares two optional JSON values canonically: both empty
// (nil/blank) are equal; otherwise each is decoded and re-marshaled so object
// key order and whitespace do not affect equality.
func canonicalJSONEqual(a, b json.RawMessage) bool {
	aEmpty, bEmpty := len(bytes.TrimSpace(a)) == 0, len(bytes.TrimSpace(b)) == 0
	if aEmpty || bEmpty {
		return aEmpty == bEmpty
	}
	var av, bv any
	if err := json.Unmarshal(a, &av); err != nil {
		return false
	}
	if err := json.Unmarshal(b, &bv); err != nil {
		return false
	}
	ab, errA := json.Marshal(av)
	bb, errB := json.Marshal(bv)
	return errA == nil && errB == nil && bytes.Equal(ab, bb)
}

// boolPtrEqual reports whether two optional booleans are equal (both nil, or
// both set to the same value).
func boolPtrEqual(a, b *bool) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}
