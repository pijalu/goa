// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"fmt"
	"strings"
)

func friendlyConnectionHint(raw string) string {
	if raw == "" {
		return ""
	}
	switch {
	case strings.Contains(raw, "SSE stream ended prematurely"),
		strings.Contains(raw, "finish_reason"):
		return "[connection error] The LLM stream ended unexpectedly before the response was complete.\n" +
			"  • This may be a temporary server hiccup — goa will retry automatically\n" +
			"  • If the problem persists, check your LLM server logs and network connection"
	case strings.Contains(raw, "context deadline exceeded"),
		strings.Contains(raw, "timeout"),
		strings.Contains(raw, "Client.Timeout"):
		return "[connection error] The request timed out — the LLM server is taking too long to respond.\n" +
			"  • goa will retry automatically, but if this persists check that your local LLM server (LM Studio, llama.cpp, etc.) is running\n" +
			"  • The model may still be loading — wait and try again\n" +
			"  • Try a smaller/faster model if this persists"
	case strings.Contains(raw, "connection refused"),
		strings.Contains(raw, "connect: connection refused"):
		return "[connection error] Could not connect to the LLM server.\n" +
			"  • Make sure the server is running and the URL/port is correct\n" +
			"  • Check your provider configuration with /config"
	case strings.Contains(raw, "connection reset"),
		strings.Contains(raw, "reset by peer"),
		strings.Contains(raw, "broken pipe"),
		strings.Contains(raw, "unexpected EOF"),
		strings.Contains(raw, "connection lost"),
		strings.Contains(raw, "EOF"):
		return "[connection error] The connection to the LLM server was interrupted.\n" +
			"  • This is usually a temporary network or server hiccup — goa retries automatically\n" +
			"  • If it persists, check your LLM server logs and network connection"
	case strings.Contains(raw, "no such host"),
		strings.Contains(raw, "lookup"):
		return "[connection error] Could not resolve the LLM server hostname.\n" +
			"  • Check your network connection\n" +
			"  • Verify the provider URL in your configuration"
	case strings.Contains(raw, "401"),
		strings.Contains(raw, "unauthorized"),
		strings.Contains(raw, "invalid API key"):
		return "[connection error] Authentication failed.\n" +
			"  • Check your API key in the provider configuration\n" +
			"  • Run /config to update your credentials"
	default:
		// The default must NOT claim "connection lost": the raw text may carry a
		// structured non-connection error (e.g. "Error: 404 - model 'x' not
		// found", a 400 malformed request, a schema error). Mislabeling those as
		// a connection problem sends the user chasing the wrong fix. Detect the
		// structured "Error: <status> - <message>" shape produced by
		// formatFatalStreamMessage/formatRetryMessage and surface it verbatim;
		// only fall back to the generic connection line when nothing better is
		// available.
		if cause := extractHTTPErrorCause(raw); cause != "" {
			return "[request error] " + cause + "\n" +
				"  • This is not a connection problem — the server rejected the request\n" +
				"  • Check the model name and provider configuration with /config"
		}
		return fmt.Sprintf("[error] The LLM request failed.\n  %s", raw)
	}
}

// extractHTTPErrorCause pulls the human-readable "<status> - <message>" cause
// out of a structured stream-error string produced by formatStreamMessage
// ("Error: 404 - model 'x' not found (code)"). The raw text may prefix it with
// wrapping context ("LLM request failed (not retryable): Error: 404 - ..."),
// so we locate the "Error: " marker and return from there. Returns "" when the
// text does not carry an HTTP-status-style error, so callers can fall back.
func extractHTTPErrorCause(raw string) string {
	const marker = "Error: "
	idx := strings.Index(raw, marker)
	if idx < 0 {
		return ""
	}
	cause := strings.TrimSpace(raw[idx+len(marker):])
	// Must start with a 3-digit HTTP status to qualify (avoids matching
	// arbitrary "Error: ..." prose that is not an HTTP rejection).
	if len(cause) < 3 || cause[0] < '0' || cause[0] > '9' || cause[1] < '0' || cause[1] > '9' || cause[2] < '0' || cause[2] > '9' {
		return ""
	}
	// Strip a trailing " - retrying" suffix so the shown cause is the bare error.
	cause = strings.TrimSuffix(cause, " - retrying")
	return cause
}

// computeCost computes cumulative cost from token totals and the model's
// pricing config. Each bucket is charged at its own rate: fresh input at
// InputPer1M, output at OutputPer1M, cache reads at the (much cheaper)
// CacheReadPer1M, and cache writes at the CacheWritePer1M premium.
//
// Bucket semantics are per-provider but the formula is correct for both:
//   - OpenAI-style: computePromptN subtracts cached tokens from PromptN, so
//     cacheRead is added back here at the cheap cache-read rate (not omitted,
//     and not double-charged at the full input rate).
//   - Anthropic-style: input/cache buckets are non-overlapping on the wire, so
//     each is charged independently at its own rate.
