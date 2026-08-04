// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package hooks

import (
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

// NewStreamFrameError wraps an error reported by the provider inside the
// event stream itself — HTTP 200 followed by an "error frame" payload such
// as {"error": {...}} — instead of as an HTTP error status. Such frames
// bypass the transport's error hook, so without classification they reach
// the agent as bare errors: a mid-stream 5xx (llama.cpp/LM Studio
// "Streaming response failed: [503] The request queue is full.") surfaced
// as a fatal, non-retryable failure and paused goal mode without a single
// retry.
//
// Classification mirrors ErrorHook.ApplyError intrinsically: every 5xx is
// retryable (server-side/transient by definition — a bounded retry is the
// correct response even for statuses like 501, since the retry budget caps
// the cost), 408 and 429 are retryable, and the rate-limit / context-
// overflow text patterns enrich the flags so the agent picks the right
// recovery path (compress+retry for overflow, exponential backoff
// otherwise). Other 4xx stay non-retryable: retrying a malformed request
// cannot succeed and only burns the retry budget.
func NewStreamFrameError(err error, statusCode int, responseBody string) *ProviderError {
	if err == nil {
		return nil
	}
	text := strings.ToLower(err.Error() + " " + responseBody)
	pe := &ProviderError{
		Err:          err,
		statusCode:   statusCode,
		responseBody: strings.TrimSpace(responseBody),
	}
	if !isNonOverflow(text, nil) {
		pe.IsContextOverflow = isContextOverflow(text, nil)
	}
	pe.IsRateLimit = statusCode == http.StatusTooManyRequests || containsAny(text, rateLimitPatterns)
	pe.IsRetryable = pe.IsRateLimit || pe.IsContextOverflow ||
		statusCode == http.StatusRequestTimeout ||
		(statusCode >= 500 && statusCode <= 599)
	return pe
}

// ExtractStreamErrorStatus pulls an HTTP status code out of a mid-stream
// provider error frame. Precedence:
//
//  1. structured numeric "code"/"status" fields on the error object
//     (Google: {"error":{"code":503,...}}; some gateways carry "code":"503"),
//  2. a bracketed code in the message text — llama.cpp/LM Studio style:
//     "Streaming response failed: [503] The request queue is full.",
//  3. a bare 3-digit 4xx/5xx in the message text, provided it is not glued
//     to other digits or a decimal point (so "4096 tokens" and "0.400"
//     are not misread as status codes).
//
// Returns 0 when no plausible status is found.
func ExtractStreamErrorStatus(errObj any, msg string) int {
	if m, ok := errObj.(map[string]any); ok {
		if code := numericStatusField(m["code"]); code != 0 {
			return code
		}
		if code := numericStatusField(m["status"]); code != 0 {
			return code
		}
	}
	if code := bracketedStatus(msg); code != 0 {
		return code
	}
	return bareStatus(msg)
}

// numericStatusField accepts a JSON number or numeric string carrying an
// HTTP error status (400-599). Non-numeric codes (OpenAI's
// "context_length_exceeded", Google's gRPC "UNAVAILABLE") return 0, as do
// out-of-range numbers (gRPC canonical codes 1-16 must not be read as HTTP).
func numericStatusField(v any) int {
	var n int
	switch t := v.(type) {
	case float64:
		n = int(t)
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(t))
		if err != nil {
			return 0
		}
		n = parsed
	default:
		return 0
	}
	if n < 400 || n > 599 {
		return 0
	}
	return n
}

var bracketedStatusRe = regexp.MustCompile(`\[([45]\d{2})\]`)

// bracketedStatus extracts a "[NNN]" style status from the message text.
func bracketedStatus(msg string) int {
	m := bracketedStatusRe.FindStringSubmatch(msg)
	if m == nil {
		return 0
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return 0
	}
	return n
}

var bareStatusRe = regexp.MustCompile(`(?:^|[^\d.])([45]\d{2})(?:[^\d.]|$)`)

// bareStatus extracts a standalone 3-digit 4xx/5xx from the message text.
// The boundary guards reject matches adjacent to digits or dots so token
// counts ("4096 tokens") and decimals ("0.400") never classify as statuses.
func bareStatus(msg string) int {
	m := bareStatusRe.FindStringSubmatch(msg)
	if m == nil {
		return 0
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return 0
	}
	return n
}
