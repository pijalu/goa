// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package schema

// CacheRetention controls how aggressively the provider caches the conversation.
type CacheRetention string

const (
	CacheRetentionNone  CacheRetention = "none"
	CacheRetentionShort CacheRetention = "short"
	CacheRetentionLong  CacheRetention = "long"
)

// CacheMode selects the breakpoint placement strategy.
type CacheMode string

const (
	CacheModeAuto  CacheMode = "auto"
	CacheModeLong  CacheMode = "long"
	CacheModeShort CacheMode = "short"
	CacheModeNone  CacheMode = "none"
)

// CacheMessagePolicy describes which messages receive cache breakpoints.
type CacheMessagePolicy struct {
	System bool `json:"system,omitempty"`
	Tools  bool `json:"tools,omitempty"`
	First  int  `json:"first,omitempty"`
	Tail   int  `json:"tail,omitempty"`
}

// CachePolicy describes how cache control is applied to a request.
type CachePolicy struct {
	Mode           CacheMode          `json:"mode,omitempty"`
	BreakpointCap  int                `json:"breakpoint_cap,omitempty"`
	Granularity    string             `json:"granularity,omitempty"` // "message" or "content"
	Messages       CacheMessagePolicy `json:"messages,omitempty"`
	Retention      CacheRetention     `json:"retention,omitempty"`
	TTL            string             `json:"ttl,omitempty"`
	SanitizeKey    bool               `json:"sanitize_key,omitempty"`
	AffinityHeader string             `json:"affinity_header,omitempty"`
}
