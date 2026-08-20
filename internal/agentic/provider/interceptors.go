// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package provider

import (
	"context"

	"github.com/pijalu/goa/internal/agentic/provider/schema"
)

// MetricsInterceptor preserves the historical per-call metrics observation
// point — the StreamOptions.OnResponse callback invoked with the response
// status and headers after the transport round-trip — by routing it through
// the interceptor seam. The terminal handler only consults
// StreamRequest.OnResponse; this interceptor installs the options callback
// there (composing with any observer an earlier interceptor installed) so the
// behavior is unchanged while remaining observable by the rest of the chain.
// It is the "logging" consumer of the StreamInterceptor seam (dsh's
// `llm/stream` waterfall).
func MetricsInterceptor(next StreamHandler) StreamHandler {
	return func(ctx context.Context, req *StreamRequest) (*schema.AssistantMessageEventStream, error) {
		if cb := req.Options.OnResponse; cb != nil {
			prev := req.OnResponse
			req.OnResponse = func(status int, headers map[string]string) {
				if prev != nil {
					prev(status, headers)
				}
				cb(status, headers)
			}
		}
		return next(ctx, req)
	}
}
