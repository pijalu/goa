// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package provider

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
)

// PrefixClassification describes how a request relates to the preceding
// serialized request. It is diagnostic metadata only; it never changes the
// request or cache identity.
type PrefixClassification string

const (
	PrefixExactAppend   PrefixClassification = "exact_append"
	PrefixReplacement   PrefixClassification = "replacement"
	PrefixDivergence    PrefixClassification = "unexpected_divergence"
	PrefixNoPredecessor PrefixClassification = "no_predecessor"
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
func BuildRequestFingerprint(providerName, model, sessionID string, previousRequest, request []byte, historyGeneration, compactionGeneration uint64, transport, turnID string, replacement bool) RequestFingerprint {
	classification := PrefixNoPredecessor
	if len(previousRequest) > 0 {
		classification = PrefixDivergence
		if bytes.HasPrefix(request, previousRequest) {
			classification = PrefixExactAppend
		} else if replacement {
			classification = PrefixReplacement
		}
	} else if replacement {
		classification = PrefixReplacement
	}
	return RequestFingerprint{
		Provider: providerName, Model: model,
		SessionKeyHash:    hashFingerprint([]byte(sessionID)),
		InputPrefixHash:   hashFingerprint(previousRequest),
		RequestHash:       hashFingerprint(request),
		HistoryGeneration: historyGeneration, CompactionGeneration: compactionGeneration,
		Transport: transport, TurnID: turnID, Classification: classification,
	}
}

func hashFingerprint(value []byte) string {
	if len(value) == 0 {
		return ""
	}
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}
