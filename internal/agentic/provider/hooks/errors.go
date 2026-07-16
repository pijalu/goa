// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package hooks

import (
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/pijalu/goa/internal/agentic/provider/schema"
)

const errorOrder = 700

// ErrorHook classifies provider errors into retryable, context overflow, and
// rate-limit categories.
type ErrorHook struct {
	profile schema.VariantProfile
}

// Name returns the hook name.
func (h *ErrorHook) Name() string { return "errors" }

// Order returns the hook order.
func (h *ErrorHook) Order() int { return errorOrder }

// Init initializes the hook with the variant profile.
func (h *ErrorHook) Init(profile schema.VariantProfile) error {
	h.profile = profile
	return nil
}

// ApplyRequest is a no-op for errors.
func (h *ErrorHook) ApplyRequest(ctx *RequestContext) error { return nil }

// ApplyResponse is a no-op for errors.
func (h *ErrorHook) ApplyResponse(ctx *ResponseContext) error { return nil }

// ApplyError classifies the error context.
func (h *ErrorHook) ApplyError(ctx *ErrorContext) error {
	body := strings.ToLower(ctx.Body)

	if ctx.Err != nil {
		body += " " + strings.ToLower(ctx.Err.Error())
	}

	// Check non-overflow patterns FIRST — these suppress overflow detection
	// for errors like Bedrock "Throttling error: Too many tokens" that
	// would otherwise falsely trigger.
	if isNonOverflow(body, h.profile.ErrorRules.NonOverflowPatterns) {
		ctx.IsContextOverflow = false
	} else {
		ctx.IsContextOverflow = isContextOverflow(body, h.profile.ErrorRules.ContextOverflowPatterns)
	}

	ctx.IsRateLimit = ctx.StatusCode == http.StatusTooManyRequests || containsAny(body, rateLimitPatterns)
	ctx.IsRetryable = ctx.IsRateLimit || ctx.IsContextOverflow || isRetryableStatus(ctx.StatusCode, h.profile.ErrorRules.RetryableStatuses)

	if ctx.IsRetryable {
		ctx.RetryAfter = parseRetryAfter(ctx.Headers, h.profile.ErrorRules.RetryAfterHeader)
		ctx.RetryAfterMs = parseRetryAfterMs(ctx.Headers, h.profile.ErrorRules.RetryAfterMsHeader)
	}

	// Codex and OpenAI specific: 404 is retryable once.
	if ctx.StatusCode == http.StatusNotFound {
		ctx.IsRetryable = true
	}

	if ctx.Err != nil {
		ctx.IsRetryable = ctx.IsRetryable || isRetryableNetworkError(ctx.Err)
	}

	return nil
}

func isRetryableNetworkError(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(err.Error())
	for _, p := range []string{"connection refused", "no such host", "temporary", "timeout", "eof", "reset by peer", "broken pipe", "context canceled"} {
		if strings.Contains(text, p) {
			return true
		}
	}
	return false
}

var defaultContextOverflowPatterns = []string{
	"context length exceeded",
	"context window exceeded",
	"maximum context length",
	"too many tokens",
	"tokens exceed",
	"input length exceeded",
	"prompt is too long",
	"context is too long",
	"token limit",
	"context_length_exceeded",
	"max_tokens",
	"request too large",
	"payload too large",
	"message exceeds",
	"conversation too long",
	"history too long",
	"sequence length",
	"model's context window",
	"exceeds the maximum",
	"context size",           // catches llama.cpp "max context size" / "available context size"
	"context size limit",
	"exceed_context",         // catches llama.cpp type field "exceed_context_size_error"
	"too long",
}

// defaultContextOverflowRegexes catches patterns that substring matching
// would miss due to boundary characters (e.g. "tokens) exceeds" vs.
// "tokens exceed").  Each regex is checked before the substring list.
var defaultContextOverflowRegexes = []*regexp.Regexp{
	regexp.MustCompile(`input\s*\(\d+\s*tokens\)\s*(?:is\s+longer|exceeds)`),           // Together AI, llama.ccp
	regexp.MustCompile(`exceeds\s+the\s+(?:available\s+)?context\s+size`),                    // llama.ccp exact
	regexp.MustCompile(`input\s+token\s+count.*exceeds\s+the\s+maximum`),                      // Google Gemini
	regexp.MustCompile(`exceeds\s+(?:the\s+)?(?:model'?s\s+)?maximum\s+context\s+length`),      // OpenAI, LiteLLM
}

// defaultNonOverflowPatterns are patterns that, when present in an error,
// suppress context-overflow classification.  This prevents false positives
// from providers whose non-overflow errors happen to contain overflow-like
// substrings (e.g. Bedrock "Throttling error: Too many tokens" contains
// "too many tokens" which matches defaultContextOverflowPatterns).
var defaultNonOverflowPatterns = []string{
	"throttling error",    // AWS Bedrock throttling
	"rate limit",          // HTTP 429 rate limiting
	"too many requests",   // HTTP 429 style
}

var rateLimitPatterns = []string{
	"rate limit",
	"too many requests",
	"throttled",
}

func isContextOverflow(body string, custom []string) bool {
	// Check regex-based patterns first — these catch boundary conditions
	// that substring matching would miss.
	for _, re := range defaultContextOverflowRegexes {
		if re.MatchString(body) {
			return true
		}
	}
	// Fallback substring matching for literal patterns.
	for _, p := range append(defaultContextOverflowPatterns, custom...) {
		if p != "" && strings.Contains(body, strings.ToLower(p)) {
			return true
		}
	}
	return false
}

// isNonOverflow returns true when the body matches a known non-overflow
// pattern, indicating that overflow classification should be suppressed
// even if the body also matches an overflow pattern.
func isNonOverflow(body string, custom []string) bool {
	for _, p := range append(defaultNonOverflowPatterns, custom...) {
		if p != "" && strings.Contains(body, strings.ToLower(p)) {
			return true
		}
	}
	return false
}

func isRetryableStatus(code int, configured []int) bool {
	if code == 0 {
		return false
	}
	for _, c := range configured {
		if c == code {
			return true
		}
	}
	return false
}

func containsAny(s string, patterns []string) bool {
	for _, p := range patterns {
		if strings.Contains(s, p) {
			return true
		}
	}
	return false
}

func parseRetryAfter(headers map[string]string, header string) int {
	name := header
	if name == "" {
		name = "Retry-After"
	}
	v := getHeaderCI(headers, name)
	if v == "" {
		return 0
	}
	// Retry-After may be a delay in seconds or an HTTP-date.
	if n, err := strconv.Atoi(v); err == nil {
		return n
	}
	return 0
}

func parseRetryAfterMs(headers map[string]string, header string) int {
	name := header
	if name == "" {
		name = "Retry-After-Ms"
	}
	v := getHeaderCI(headers, name)
	if v == "" {
		return 0
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0
	}
	return n
}

func getHeaderCI(headers map[string]string, name string) string {
	for k, v := range headers {
		if strings.EqualFold(k, name) {
			return v
		}
	}
	return ""
}

// IsContextOverflow returns whether err represents a context overflow.
func IsContextOverflow(err error) bool {
	if err == nil {
		return false
	}
	var pe *ProviderError
	if errors.As(err, &pe) {
		return pe.IsContextOverflow
	}
	return isContextOverflow(strings.ToLower(err.Error()), nil)
}
