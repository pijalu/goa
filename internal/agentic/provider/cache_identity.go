// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package provider

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
)

// CacheIdentity describes the non-secret inputs that delimit a provider cache
// sequence. Generation must advance when a history replacement or explicit
// context boundary occurs; ordinary append-only turns keep it unchanged.
type CacheIdentity struct {
	ContextID      string
	Generation     uint64
	Provider       string
	Model          string
	ToolSchemaHash string
}

// NewCacheKey returns a stable, opaque provider cache key. Length-prefixed
// fields prevent concatenation collisions, and hashing keeps context IDs,
// prompts, and other ownership metadata out of requests and diagnostics.
func NewCacheKey(identity CacheIdentity) string {
	var b strings.Builder
	for _, field := range []string{
		"goa-cache-v1", identity.ContextID, strconv.FormatUint(identity.Generation, 10),
		identity.Provider, identity.Model, identity.ToolSchemaHash,
	} {
		b.WriteString(strconv.Itoa(len(field)))
		b.WriteByte(':')
		b.WriteString(field)
		b.WriteByte('|')
	}
	sum := sha256.Sum256([]byte(b.String()))
	return "goa_" + hex.EncodeToString(sum[:])
}
