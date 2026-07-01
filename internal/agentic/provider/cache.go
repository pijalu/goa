// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

// Package provider provides cache helpers for prompt caching.
package provider

// CacheControlMarkers holds information about where to apply cache_control markers.
type CacheControlMarkers struct {
	SystemPrompt bool
	LastTools    bool
	LastMessage  bool
}

// ShouldApplyCacheControl determines whether cache_control markers should be
// applied given the retention setting and provider compat.
func ShouldApplyCacheControl(retention CacheRetention, supportsLong bool) bool {
	switch retention {
	case CacheRetentionShort:
		return true
	case CacheRetentionLong:
		return supportsLong
	default:
		return false
	}
}
