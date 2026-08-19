// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package provider

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// PrefixClassification describes how a request relates to the preceding
// serialized request. It is diagnostic metadata only; it never changes the
// request or cache identity.
type PrefixClassification string

const (
	PrefixExactAppend          PrefixClassification = "exact_append"
	PrefixParamChange          PrefixClassification = "param_change"
	PrefixToolPolicyTransition PrefixClassification = "tool_policy_transition"
	PrefixReplacement          PrefixClassification = "replacement"
	PrefixDivergence           PrefixClassification = "unexpected_divergence"
	PrefixNoPredecessor        PrefixClassification = "no_predecessor"
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
// distinct from a history rewrite. One sub-case is classified separately:
// dropping the tools array while forcing tool_choice "none" (the intentional
// final-step/recovery collapse) is tool_policy_transition, not an opaque
// param_change. Non-JSON bodies (exotic transports) fall back to the
// historical byte-level test.
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
// identical params) > tool_policy_transition (messages-prefix, intentional
// tools → tool_choice "none" collapse) > param_change (messages-prefix,
// params differ) > replacement (flagged) > unexpected_divergence.
func classifyPrefix(previousRequest, request []byte, replacement bool) PrefixClassification {
	if len(previousRequest) == 0 {
		if replacement {
			return PrefixReplacement
		}
		return PrefixNoPredecessor
	}
	if msgsPrefix, paramsEq, ok := compareBodies(previousRequest, request); ok {
		switch {
		case msgsPrefix && paramsEq:
			return PrefixExactAppend
		case msgsPrefix && isToolPolicyTransition(previousRequest, request):
			return PrefixToolPolicyTransition
		case msgsPrefix:
			return PrefixParamChange
		}
	} else if bytes.HasPrefix(request, previousRequest) {
		// Non-JSON bodies: keep the historical byte-level semantics.
		return PrefixExactAppend
	}
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
	var choice string
	_ = json.Unmarshal(cur["tool_choice"], &choice)
	return len(prevTools) > 0 && len(curTools) == 0 && choice == "none"
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
