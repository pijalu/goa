// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import "strings"

// cancellationHistoryMessage formats structured cancellation provenance for
// persistent chat history. Missing source is deliberately not attributed to a user.
func cancellationHistoryMessage(metadata map[string]string) string {
	source := strings.TrimSpace(metadata["cancel_source"])
	reason := strings.TrimSpace(metadata["cancel_reason"])
	if source == "" {
		source = "unknown"
	}
	if reason == "" {
		reason = "no cancellation reason recorded"
	}
	return "Generation cancelled [source: " + source + "]: " + reason
}
