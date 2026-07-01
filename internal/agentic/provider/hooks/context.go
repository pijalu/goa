// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package hooks

import (
	"net/http"

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
func (c *ErrorContext) ToError() error {
	if c.Err == nil {
		return nil
	}
	return &ProviderError{
		Err:               c.Err,
		IsRetryable:       c.IsRetryable,
		IsContextOverflow: c.IsContextOverflow,
		IsRateLimit:       c.IsRateLimit,
		RetryAfter:        c.RetryAfter,
		RetryAfterMs:      c.RetryAfterMs,
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
}

func (e *ProviderError) Error() string {
	return e.Err.Error()
}

func (e *ProviderError) Unwrap() error {
	return e.Err
}
