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

// ApplyRequest resolves the API key and injects auth headers. Every resolved
// key is validated before first use (DS5/P15): a set-but-malformed key yields
// an *schema.InvalidCredentialError naming the config entry point it came
// from (never the key itself); a required-but-absent key yields an
// *schema.MissingCredentialError listing every source that was checked.
func (h *AuthHook) ApplyRequest(ctx *RequestContext) error {
	if ctx.Headers == nil {
		ctx.Headers = make(map[string]string)
	}

	if ctx.Headers["User-Agent"] == "" {
		ctx.Headers["User-Agent"] = userAgent()
	}

	// If options provide an explicit API key, use it. The key is validated
	// before first use; the config entry point is the explicit option.
	auth := h.authFor(ctx.Model.Api)

	if ctx.Options.APIKey != "" {
		key, err := schema.ValidateAPIKey("options.api_key", ctx.Options.APIKey)
		if err != nil {
			return err
		}
		injectAuth(ctx.Headers, auth, key)
		h.applyProviderHeaders(ctx)
		return nil
	}

	// If Authorization header is already present, treat key as unused.
	if hasHeader(ctx.Headers, "Authorization") || hasHeader(ctx.Headers, "Cf-Aig-Authorization") {
		injectAuth(ctx.Headers, auth, "unused")
		h.applyProviderHeaders(ctx)
		return nil
	}

	// Resolve from env vars declared in the profile. Each candidate is
	// validated before use: a set-but-malformed value is an InvalidCredential
	// naming the variable; a silent lookup is a checked-but-unset source and
	// is listed if the provider requires a credential.
	checked := []string{"options.api_key"}
	for _, env := range auth.EnvVars {
		checked = append(checked, env)
		if raw := os.Getenv(env); raw != "" {
			key, err := schema.ValidateAPIKey(env, raw)
			if err != nil {
				return err
			}
			injectAuth(ctx.Headers, auth, key)
			h.applyProviderHeaders(ctx)
			return nil
		}
	}

	if h.profile.Auth.Required {
		return &schema.MissingCredentialError{
			Provider: string(ctx.Model.Provider),
			Sources:  checked,
		}
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

	if isOpenCodeGateway(ctx.Model) {
		applyOpenCodeHeaders(ctx)
	}

	if ctx.Model.Provider == schema.ProviderOpenAI && h.profile.Auth.Method == schema.AuthMethodOAuth {
		ctx.Profile.Compat.SystemAsInstructions = true
	}

	if ctx.Model.Api == schema.ApiOpenAICodexResponses {
		applyCodexHeaders(ctx)
	}
}

// applyCodexHeaders sets the Codex identity headers on the subscription
// transport, mirroring Pi's buildBaseCodexHeaders/buildSSEHeaders and
// OpenCode's codex fetch wrapper: both transports tag the originator; the
// OAuth transport (opts.CodexAccountID set) additionally carries the ChatGPT
// account id, the responses beta flag, and — when a session is active — the
// session-id affinity header the codex backend uses for cache locality.
func applyCodexHeaders(ctx *RequestContext) {
	if !hasHeader(ctx.Headers, "originator") {
		ctx.Headers["originator"] = "goa"
	}
	if ctx.Options.SessionID != "" && !hasHeader(ctx.Headers, "session-id") {
		ctx.Headers["session-id"] = ctx.Options.SessionID
	}
	if ctx.Options.CodexAccountID == "" {
		return
	}
	ctx.Headers["chatgpt-account-id"] = ctx.Options.CodexAccountID
	if !hasHeader(ctx.Headers, "OpenAI-Beta") {
		ctx.Headers["OpenAI-Beta"] = "responses=experimental"
	}
	if !hasHeader(ctx.Headers, "accept") {
		ctx.Headers["accept"] = "text/event-stream"
	}
}

// isOpenCodeGateway reports whether the model's traffic terminates at an
// OpenCode gateway: either a catalog opencode identity, or a custom/provider-
// less config whose base URL matches the catalog opencode URL patterns
// (MatchProviderByNameOrURL defers generic identities to the URL).
func isOpenCodeGateway(model schema.Model) bool {
	if model.Provider == schema.ProviderOpenCode || model.Provider == schema.ProviderOpenCodeGo {
		return true
	}
	def := schema.MatchProviderByNameOrURL(model.Provider, model.BaseURL)
	if def == nil {
		return false
	}
	return def.Provider == schema.ProviderOpenCode || def.Provider == schema.ProviderOpenCodeGo
}

// applyOpenCodeHeaders tags requests to the OpenCode Zen/Go gateways with
// the identity headers their handler reads (opencode
// packages/console/app/src/routes/zen/util/handler.ts): x-opencode-session —
// one stable id per conversation — drives the gateway's sticky-provider
// routing (its fallback is workspace/IP) and is required by the gateway;
// x-opencode-client identifies this client (opencode itself defaults it to
// "cli" via OPENCODE_CLIENT). The session id mirrors the Codex session-id
// source: StreamOptions.SessionID, the conversation id minted per context
// and rotated by ResetConversationID (Rule 7). Headers the caller already
// set (provider/model config) win, matching the codex guard behavior.
func applyOpenCodeHeaders(ctx *RequestContext) {
	if ctx.Options.SessionID != "" && !hasHeader(ctx.Headers, "x-opencode-session") {
		ctx.Headers["x-opencode-session"] = ctx.Options.SessionID
	}
	if !hasHeader(ctx.Headers, "x-opencode-client") {
		ctx.Headers["x-opencode-client"] = "goa"
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

// authFor returns the effective AuthConfig for the given wire API. The base
// profile auth is used unless the profile declares a per-API override
// (AuthConfig.PerAPI) for that API — e.g. opencode zen serves anthropic
// /messages (x-api-key) alongside chat/responses (Bearer) under one
// provider identity.
func (h *AuthHook) authFor(api schema.Api) schema.AuthConfig {
	base := h.profile.Auth
	if len(base.PerAPI) == 0 {
		return base
	}
	ov, ok := base.PerAPI[string(api)]
	if !ok {
		return base
	}
	if ov.Header != "" {
		base.Header = ov.Header
		// When the header changes, the override's prefix wins (possibly
		// empty); injectAuth re-derives "Bearer " for Authorization.
		base.Prefix = ov.Prefix
	}
	return base
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
