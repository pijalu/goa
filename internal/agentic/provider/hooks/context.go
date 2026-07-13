// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package hooks

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/pijalu/goa/internal/agentic/provider/schema"
)

// RequestContext carries the canonical request state through the hook pipeline.
type RequestContext struct {
	Model     schema.Model
	Context   schema.Context
	Options   schema.StreamOptions
	Profile   schema.VariantProfile
	Headers   map[string]string
	Body      []byte
	Request   *http.Request
	ExtraBody map[string]any
	Pipeline  *Pipeline
}

// ResponseContext carries the canonical response state through the hook pipeline.
type ResponseContext struct {
	Model    schema.Model
	Profile  schema.VariantProfile
	Response *http.Response
	Body     []byte
	Events   []schema.AssistantMessageEvent
}

// ErrorContext carries error state for classification.
type ErrorContext struct {
	Model             schema.Model
	Profile           schema.VariantProfile
	StatusCode        int
	Body              string
	Headers           map[string]string
	Err               error
	IsRetryable       bool
	IsContextOverflow bool
	IsRateLimit       bool
	RetryAfter        int
	RetryAfterMs      int
}

// ToError returns a structured provider error.
// When Err is nil but the error context has classification fields set
// (IsContextOverflow, IsRateLimit, IsRetryable) or a non-empty Body,
// it synthesises an error from the body text so that the classification
// is not silently lost. Without this, HTTP-level errors (for example
// LM Studio returning 400 with context_length_exceeded) close the
// stream with a nil error, and the agent never triggers compression.
func (c *ErrorContext) ToError() error {
	err := c.Err
	body := strings.TrimSpace(c.Body)
	if err == nil {
		if body != "" || c.IsContextOverflow || c.IsRateLimit || c.IsRetryable || c.StatusCode != 0 {
			if body == "" {
				body = fmt.Sprintf("provider error (HTTP %d)", c.StatusCode)
			}
			err = fmt.Errorf("%s", body)
		} else {
			return nil
		}
	}
	return &ProviderError{
		Err:               err,
		IsRetryable:       c.IsRetryable,
		IsContextOverflow: c.IsContextOverflow,
		IsRateLimit:       c.IsRateLimit,
		RetryAfter:        c.RetryAfter,
		RetryAfterMs:      c.RetryAfterMs,
		statusCode:        c.StatusCode,
		responseBody:      body,
	}
}

// ProviderError wraps an error with provider classification metadata.
type ProviderError struct {
	Err               error
	IsRetryable       bool
	IsContextOverflow bool
	IsRateLimit       bool
	RetryAfter        int
	RetryAfterMs      int
	statusCode        int
	responseBody      string
}

func (e *ProviderError) Error() string {
	if e == nil || e.Err == nil {
		return "provider error"
	}
	return e.Err.Error()
}

func (e *ProviderError) Unwrap() error {
	return e.Err
}

// StatusCode returns the HTTP status code that produced the error, or 0 if
// the error was not HTTP-related.
func (e *ProviderError) StatusCode() int { return e.statusCode }

// ResponseBody returns the raw response body associated with the error.
func (e *ProviderError) ResponseBody() string { return e.responseBody }
