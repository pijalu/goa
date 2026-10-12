// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package provider

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
)

// PrefixClassification describes how a request relates to the preceding
// serialized request. It is diagnostic metadata only; it never changes the
// request or cache identity.
type PrefixClassification string

const (
	PrefixExactAppend          PrefixClassification = "exact_append"
	PrefixParamChange          PrefixClassification = "param_change"
	PrefixToolPolicyTransition PrefixClassification = "tool_policy_transition"
	// PrefixToolChoiceCollapse: messages append and the only non-message field
	// that changed is tool_choice, set to its text-only value — the P7
	// final-step/recovery collapse expressed on a prompt-neutral control field.
	// Cache-neutral by construction (the cached prompt is unchanged), so it is
	// reported as metadata, never as a miss cause.
	PrefixToolChoiceCollapse PrefixClassification = "tool_choice_collapse"
	PrefixReplacement        PrefixClassification = "replacement"
	PrefixDivergence         PrefixClassification = "unexpected_divergence"
	PrefixNoPredecessor      PrefixClassification = "no_predecessor"
)

// RequestFingerprint contains bounded, non-sensitive request diagnostics.
// Hashes are deliberately one-way: session identifiers, prompts, and request
// bodies are never retained in this structure.
type RequestFingerprint struct {
	Provider             string               `json:"provider,omitempty"`
	Model                string               `json:"model,omitempty"`
	SessionKeyHash       string               `json:"session_key_hash,omitempty"`
	InputPrefixHash      string               `json:"input_prefix_hash,omitempty"`
	RequestHash          string               `json:"request_hash,omitempty"`
	HistoryGeneration    uint64               `json:"history_generation,omitempty"`
	CompactionGeneration uint64               `json:"compaction_generation,omitempty"`
	Transport            string               `json:"transport,omitempty"`
	TurnID               string               `json:"turn_id,omitempty"`
	Classification       PrefixClassification `json:"classification,omitempty"`
}

// BuildRequestFingerprint computes debug-only metadata for a request. The
// previous request is used solely to classify prefix integrity; callers may
// mark a deliberate reset/replacement explicitly via replacement.
//
// Classification is semantic, not byte-level: Go's encoding/json marshals map
// keys alphabetically, so `messages` is the first key of a marshaled body but
// a history APPEND lands inside the array — the new body is never a byte
// prefix of the old one, and a whole-body bytes.HasPrefix test can never
// report exact_append for a real append (only for byte-identical retries;
// observed 2026-08-19: 10 provably append-only export request pairs were all
// classified unexpected_divergence). The classifier therefore decomposes both
// bodies: the previous messages must canonically prefix the current ones and
// every non-message field must be canonically equal. A messages-prefix with a
// changed field (tools, thinking, …) is param_change — cache-relevant but
// distinct from a history rewrite. Two sub-cases are classified separately:
// toggling tool_choice alone to its text-only value while keeping the tool
// surface is tool_choice_collapse (cache-neutral by construction), while
// dropping the tools array with it — the P7 shape before 2026-10-05 — is
// tool_policy_transition, the shape that re-bills the whole prompt. Non-JSON
// bodies (exotic transports) fall back to the historical byte-level test.
func BuildRequestFingerprint(providerName, model, sessionID string, previousRequest, request []byte, historyGeneration, compactionGeneration uint64, transport, turnID string, replacement bool) RequestFingerprint {
	classification := classifyPrefix(previousRequest, request, replacement)
	return RequestFingerprint{
		Provider: providerName, Model: model,
		SessionKeyHash:    hashFingerprint([]byte(sessionID)),
		InputPrefixHash:   hashFingerprint(previousRequest),
		RequestHash:       hashFingerprint(request),
		HistoryGeneration: historyGeneration, CompactionGeneration: compactionGeneration,
		Transport: transport, TurnID: turnID, Classification: classification,
	}
}

// classifyPrefix derives the prefix classification for a request relative
// to its predecessor. Precedence: exact_append (canonical messages-prefix +
// identical params) > tool_choice_collapse (messages-prefix, only the
// text-only control field changed) > tool_policy_transition (messages-prefix,
// the cached tool surface itself was dropped — the P7 shape before
// 2026-10-05, still flagged because it re-bills the whole prompt) >
// param_change (messages-prefix, params differ) > replacement (flagged) >
// unexpected_divergence. Non-JSON bodies (exotic transports) keep the
// historical byte-level semantics.
func classifyPrefix(previousRequest, request []byte, replacement bool) PrefixClassification {
	if len(previousRequest) == 0 {
		return classifyWithoutPredecessor(replacement)
	}
	msgsPrefix, paramsEqual, decomposable := compareBodies(previousRequest, request)
	if decomposable {
		return classifyDecomposed(previousRequest, request, msgsPrefix, paramsEqual, replacement)
	}
	if bytes.HasPrefix(request, previousRequest) {
		return PrefixExactAppend
	}
	return classifyUnrelated(replacement)
}

// classifyDecomposed classifies two bodies whose messages array and non-message
// fields could be compared; msgsPrefix reports whether the previous messages
// canonically prefix the current ones and paramsEqual whether every
// non-message field is canonically equal.
func classifyDecomposed(previousRequest, request []byte, msgsPrefix, paramsEqual, replacement bool) PrefixClassification {
	switch {
	case msgsPrefix && paramsEqual:
		return PrefixExactAppend
	case msgsPrefix && isToolChoiceCollapse(previousRequest, request):
		return PrefixToolChoiceCollapse
	case msgsPrefix && isToolPolicyTransition(previousRequest, request):
		return PrefixToolPolicyTransition
	case msgsPrefix:
		return PrefixParamChange
	}
	return classifyUnrelated(replacement)
}

// classifyWithoutPredecessor is the classification of the first request of a
// sequence.
func classifyWithoutPredecessor(replacement bool) PrefixClassification {
	if replacement {
		return PrefixReplacement
	}
	return PrefixNoPredecessor
}

// classifyUnrelated is the classification of a request sharing no prefix
// relation with its predecessor.
func classifyUnrelated(replacement bool) PrefixClassification {
	if replacement {
		return PrefixReplacement
	}
	return PrefixDivergence
}

// compareBodies decomposes two serialized JSON request bodies into their
// messages arrays and non-message fields, reporting whether the previous
// messages canonically prefix the current ones and whether every other field
// is canonically equal. ok is false when either body is not a JSON object
// carrying a messages array.
func compareBodies(previousRequest, request []byte) (msgsPrefix, paramsEqual, ok bool) {
	var prev, cur map[string]json.RawMessage
	if json.Unmarshal(previousRequest, &prev) != nil || json.Unmarshal(request, &cur) != nil {
		return false, false, false
	}
	prevMsgs, okPrev := splitMessages(prev)
	curMsgs, okCur := splitMessages(cur)
	if !okPrev || !okCur {
		return false, false, false
	}
	return messagesArePrefix(prevMsgs, curMsgs), nonMessageFieldsEqual(prev, cur), true
}

func isToolPolicyTransition(previousRequest, request []byte) bool {
	var prev, cur map[string]json.RawMessage
	if json.Unmarshal(previousRequest, &prev) != nil || json.Unmarshal(request, &cur) != nil {
		return false
	}
	var prevTools, curTools []json.RawMessage
	_ = json.Unmarshal(prev["tools"], &prevTools)
	_ = json.Unmarshal(cur["tools"], &curTools)
	return len(prevTools) > 0 && len(curTools) == 0 && isTextOnlyToolChoice(cur["tool_choice"])
}

// isToolChoiceCollapse reports whether request is an append of previousRequest
// whose only non-message difference is a prompt-neutral collapse control value
// set to its text-only form ("none" / {"type":"none"} / NONE function
// calling). That is the P7 collapse as expressed on a control field only, so
// the cached prompt and its prefix are untouched. It accepts the field being
// added (the normal round carried none) and changed (codex pins "auto").
func isToolChoiceCollapse(previousRequest, request []byte) bool {
	prev, cur, ok := decodeBodies(previousRequest, request)
	if !ok {
		return false
	}
	field := changedControlField(prev, cur)
	return field != "" && isTextOnlyToolChoice(cur[field])
}

// collapseControlFields names the request fields a text-only collapse may move
// without touching the provider's cached prompt: the OpenAI/Mistral/Anthropic
// tool_choice and Google's function-calling config.
var collapseControlFields = map[string]bool{
	"tool_choice": true,
	"toolConfig":  true,
}

// decodeBodies unmarshals two serialized bodies into their raw field maps.
func decodeBodies(previousRequest, request []byte) (map[string]json.RawMessage, map[string]json.RawMessage, bool) {
	var prev, cur map[string]json.RawMessage
	if json.Unmarshal(previousRequest, &prev) != nil || json.Unmarshal(request, &cur) != nil {
		return nil, nil, false
	}
	return prev, cur, true
}

// changedControlField returns the single non-message field that differs between
// two bodies when that field is a prompt-neutral collapse control. It returns
// "" when the bodies differ in no such field, in more than one, or in any
// prompt-bearing field (tools above all).
func changedControlField(prev, cur map[string]json.RawMessage) string {
	field, single := differingField(prev, cur)
	if !single || !collapseControlFields[field] {
		return ""
	}
	return field
}

// differingField reports the one non-message field that separates two bodies,
// counting a field missing from cur as differing. single is false when two or
// more fields differ.
func differingField(prev, cur map[string]json.RawMessage) (string, bool) {
	differing := ""
	for key, prevValue := range prev {
		if key == "messages" {
			continue
		}
		if curValue, present := cur[key]; present && rawJSONEqual(prevValue, curValue) {
			continue
		}
		if differing != "" {
			return "", false
		}
		differing = key
	}
	for key := range cur {
		if key == "messages" {
			continue
		}
		if _, existed := prev[key]; existed {
			continue
		}
		if differing != "" {
			return "", false
		}
		differing = key
	}
	return differing, true
}

// rawJSONEqual compares two raw JSON values byte-wise first, then canonically
// (key order and whitespace differences are not differences).
func rawJSONEqual(a, b json.RawMessage) bool {
	return bytes.Equal(a, b) || canonicalEqual(a, b)
}

// isTextOnlyToolChoice reports whether a serialized collapse control value is
// one of the per-flavor text-only forms: "none" (OpenAI completions, Responses,
// Mistral), {"type":"none"} (Anthropic) or Google's function-calling mode NONE
// (plain or wrapped in functionCallingConfig).
func isTextOnlyToolChoice(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	if value, ok := jsonString(raw); ok {
		return strings.EqualFold(value, "none")
	}
	object, ok := jsonObject(raw)
	if !ok {
		return false
	}
	return hasTextOnlyMarker(object)
}

// hasTextOnlyMarker reports whether a control object carries the text-only
// value at its top level or inside Google's functionCallingConfig wrapper.
func hasTextOnlyMarker(object map[string]json.RawMessage) bool {
	if isNoneMarker(object["type"]) || isNoneMarker(object["mode"]) {
		return true
	}
	inner, ok := jsonObject(object["functionCallingConfig"])
	if !ok {
		return false
	}
	return isNoneMarker(inner["mode"])
}

// isNoneMarker reports whether a raw JSON value is the string "none".
func isNoneMarker(raw json.RawMessage) bool {
	value, ok := jsonString(raw)
	return ok && strings.EqualFold(value, "none")
}

func jsonString(raw json.RawMessage) (string, bool) {
	if len(raw) == 0 {
		return "", false
	}
	var value string
	if json.Unmarshal(raw, &value) != nil {
		return "", false
	}
	return value, true
}

func jsonObject(raw json.RawMessage) (map[string]json.RawMessage, bool) {
	if len(raw) == 0 {
		return nil, false
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) != nil {
		return nil, false
	}
	return object, true
}

// splitMessages extracts the messages array as raw per-message JSON values.
func splitMessages(body map[string]json.RawMessage) ([]json.RawMessage, bool) {
	raw, ok := body["messages"]
	if !ok {
		return nil, false
	}
	var msgs []json.RawMessage
	if err := json.Unmarshal(raw, &msgs); err != nil {
		return nil, false
	}
	return msgs, true
}

// messagesArePrefix reports whether prev canonically prefixes cur, message by
// message. Byte-identical values short-circuit the canonical comparison.
func messagesArePrefix(prev, cur []json.RawMessage) bool {
	if len(prev) > len(cur) {
		return false
	}
	for i := range prev {
		if !bytes.Equal(prev[i], cur[i]) && !canonicalEqual(prev[i], cur[i]) {
			return false
		}
	}
	return true
}

// nonMessageFieldsEqual reports whether every field except messages is
// canonically equal across both bodies.
func nonMessageFieldsEqual(a, b map[string]json.RawMessage) bool {
	if len(a) != len(b) {
		return false
	}
	for k, av := range a {
		if k == "messages" {
			continue
		}
		bv, ok := b[k]
		if !ok {
			return false
		}
		if !bytes.Equal(av, bv) && !canonicalEqual(av, bv) {
			return false
		}
	}
	return true
}

// canonicalEqual compares two JSON values by decoding and re-marshaling both
// (json.Marshal of decoded maps sorts object keys), so raw key order and
// whitespace differences do not affect equality.
func canonicalEqual(a, b json.RawMessage) bool {
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

func hashFingerprint(value []byte) string {
	if len(value) == 0 {
		return ""
	}
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}
