// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package hooks

import (
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/pijalu/goa/internal/agentic/provider/schema"
)

const authOrder = 100

// AuthHook resolves credentials and injects auth headers.
type AuthHook struct {
	profile schema.VariantProfile
}

// Name returns the hook name.
func (h *AuthHook) Name() string { return "auth" }

// Order returns the hook order.
func (h *AuthHook) Order() int { return authOrder }

// Init initializes the hook with the variant profile.
func (h *AuthHook) Init(profile schema.VariantProfile) error {
	h.profile = profile
	return nil
}

// ApplyRequest resolves the API key and injects auth headers.
func (h *AuthHook) ApplyRequest(ctx *RequestContext) error {
	if ctx.Headers == nil {
		ctx.Headers = make(map[string]string)
	}

	if ctx.Headers["User-Agent"] == "" {
		ctx.Headers["User-Agent"] = userAgent()
	}

	// If options provide an explicit API key, use it.
	if ctx.Options.APIKey != "" {
		injectAuth(ctx.Headers, h.profile.Auth, ctx.Options.APIKey)
		h.applyProviderHeaders(ctx)
		return nil
	}

	// If Authorization header is already present, treat key as unused.
	if hasHeader(ctx.Headers, "Authorization") || hasHeader(ctx.Headers, "Cf-Aig-Authorization") {
		injectAuth(ctx.Headers, h.profile.Auth, "unused")
		h.applyProviderHeaders(ctx)
		return nil
	}

	// Resolve from env vars declared in the profile.
	for _, env := range h.profile.Auth.EnvVars {
		if v := os.Getenv(env); v != "" {
			injectAuth(ctx.Headers, h.profile.Auth, v)
			h.applyProviderHeaders(ctx)
			return nil
		}
	}

	if h.profile.Auth.Required {
		return fmt.Errorf("no API key found for provider %q", ctx.Model.Provider)
	}
	return nil
}

// ApplyResponse is a no-op for auth.
func (h *AuthHook) ApplyResponse(ctx *ResponseContext) error { return nil }

// ApplyError is a no-op for auth.
func (h *AuthHook) ApplyError(ctx *ErrorContext) error { return nil }

func (h *AuthHook) applyProviderHeaders(ctx *RequestContext) {
	for _, rule := range h.profile.Headers {
		value := rule.Value
		if rule.EnvVar != "" {
			value = os.Getenv(rule.EnvVar)
		}
		value = schema.ApplyTemplate(value, buildEnv(ctx))
		if value != "" || rule.IfSet == "" {
			ctx.Headers[rule.Name] = value
		}
	}

	if ctx.Model.Provider == schema.ProviderGitHub {
		applyGitHubCopilotHeaders(ctx)
	}

	if ctx.Model.Provider == schema.ProviderOpenAI && h.profile.Auth.Method == schema.AuthMethodOAuth {
		ctx.Profile.Compat.SystemAsInstructions = true
	}
}

func injectAuth(headers map[string]string, auth schema.AuthConfig, token string) {
	if auth.Header == "" {
		return
	}
	prefix := auth.Prefix
	if prefix == "" && auth.Header == "Authorization" {
		prefix = "Bearer "
	}
	headers[auth.Header] = prefix + token
}

func hasHeader(headers map[string]string, name string) bool {
	for k := range headers {
		if strings.EqualFold(k, name) {
			return true
		}
	}
	return false
}

func userAgent() string {
	return fmt.Sprintf("goa/0.0.0 (%s/%s)", runtime.GOOS, runtime.GOARCH)
}

func applyGitHubCopilotHeaders(ctx *RequestContext) {
	ctx.Headers["User-Agent"] = fmt.Sprintf("goa (%s)", runtime.GOOS)
	if hasImage(ctx.Context.Messages) {
		ctx.Headers["X-Vision-Preview"] = "true"
	}
}

func hasImage(messages []schema.Message) bool {
	for _, m := range messages {
		for _, b := range m.Content {
			if b.Type == schema.ContentBlockImage {
				return true
			}
		}
	}
	return false
}
