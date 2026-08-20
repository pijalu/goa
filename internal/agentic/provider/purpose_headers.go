// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package provider

import (
	"context"
	"strings"

	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/internal/agentic/provider/schema"
)

// Attribution/correlation header names carried on every protocol-backed
// provider request (P13, CA2/CA3). They mirror the dsh harness's
// x-deepseek-harness-* attribution headers (packages/llm/llm-deepseek
// README §App attribution); the x-goa-* names reflect that these ride on
// every protocol-backed call, not only DeepSeek-compat routes.
const (
	HeaderGoaUserID    = "x-goa-user-id"
	HeaderGoaSessionID = "x-goa-session-id"
	HeaderGoaCompact   = "x-goa-compact"
)

// PurposeHeadersInterceptor is the P13 (CA2/CA3) consumer of the
// StreamInterceptor seam. It tags every protocol-backed streaming request
// with the goa correlation headers:
//
//   - x-goa-user-id: the stable anonymous id (internal.AnonymousUserID);
//   - x-goa-session-id: the exact SessionID value, omitted when unset
//     (mirrors dsh: "a request carrying GenerateOptions.sessionId also sends
//     that exact value ... a direct call without a session omits the session
//     header");
//   - x-goa-compact: 1 on compaction calls over DeepSeek-compat routes, so
//     the host can separate compaction traffic from conversation requests.
//
// The ids ride only in transport headers — never in the request body or
// model-visible content.
func PurposeHeadersInterceptor(next StreamHandler) StreamHandler {
	return func(ctx context.Context, req *StreamRequest) (*schema.AssistantMessageEventStream, error) {
		if req.Headers == nil {
			req.Headers = make(map[string]string)
		}
		req.Headers[HeaderGoaUserID] = internal.AnonymousUserID()
		if req.Options.SessionID != "" {
			req.Headers[HeaderGoaSessionID] = req.Options.SessionID
		}
		if req.Options.Purpose == schema.PurposeCompaction && isDeepSeekRoute(req.Model) {
			req.Headers[HeaderGoaCompact] = "1"
		}
		return next(ctx, req)
	}
}

// isDeepSeekRoute reports whether the model resolves to a DeepSeek-compat
// route: a DeepSeek provider/endpoint fingerprint, or a DeepSeek model
// served under another provider. The model-id check mirrors compat_detect's
// isDeepSeekModel reasoning — the DeepSeek wire contract follows the model,
// not the endpoint (a deepseek model proxied under another provider must
// keep DeepSeek behavior).
func isDeepSeekRoute(model schema.Model) bool {
	fp := fingerprintProvider(model.Provider, model.BaseURL)
	return fp.isDeepSeek || strings.Contains(strings.ToLower(model.ID), "deepseek")
}
