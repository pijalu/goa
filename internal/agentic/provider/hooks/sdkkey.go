// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package hooks

import "github.com/pijalu/goa/internal/agentic/provider/schema"

const sdkKeyOrder = 200

// SdkKeyHook routes options into the correct namespace for providers that
// require non-standard parameter placement.
type SdkKeyHook struct {
	profile schema.VariantProfile
}

// Name returns the hook name.
func (h *SdkKeyHook) Name() string { return "sdkkey" }

// Order returns the hook order.
func (h *SdkKeyHook) Order() int { return sdkKeyOrder }

// Init initializes the hook with the variant profile.
func (h *SdkKeyHook) Init(profile schema.VariantProfile) error {
	h.profile = profile
	return nil
}

// ApplyRequest prepares the extra_body/provider namespace if configured.
func (h *SdkKeyHook) ApplyRequest(ctx *RequestContext) error {
	ns := h.profile.SdkKey.Namespace
	if ns == "" || ns == schema.OptionsNamespaceRoot {
		return nil
	}
	if ctx.ExtraBody == nil {
		ctx.ExtraBody = make(map[string]any)
	}
	return nil
}

// ApplyResponse is a no-op for sdkkey.
func (h *SdkKeyHook) ApplyResponse(ctx *ResponseContext) error { return nil }

// ApplyError is a no-op for sdkkey.
func (h *SdkKeyHook) ApplyError(ctx *ErrorContext) error { return nil }
