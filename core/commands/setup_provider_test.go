// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core"
)

// setupProviderTestContext builds a host whose API-key prompt is captured
// instead of rendered, plus the recorder for what was asked.
type credentialRecorder struct {
	prompted bool
	prompt   string
	prefill  string
	onSubmit func(string, bool)
}

func newCredentialHost(rec *credentialRecorder) core.Context {
	return core.Context{
		ShowInputFunc: func(prompt, current string, onSubmit func(string, bool)) {
			rec.prompted = true
			rec.prompt = prompt
			rec.prefill = current
			rec.onSubmit = onSubmit
		},
	}
}

// TestSetupProvider_PromptsForKeyWhenMissing covers the core gap in bugs.md
// "Vercel AI Gateway: no way to add an API key": a provider with no credential
// anywhere must ASK for the key, and the answer must land in the auth store —
// the same vault /login:<provider>:apikey writes.
func TestSetupProvider_PromptsForKeyWhenMissing(t *testing.T) {
	t.Setenv("AI_GATEWAY_API_KEY", "")
	store := mustStore(t)
	registerLoginStore(store)
	t.Cleanup(func() { registerLoginStore(nil) })

	rec := &credentialRecorder{}
	ctx := newCredentialHost(rec)
	cfg := &config.Config{}

	ready, apiKey := false, "unset"
	setupProviderCredential(ctx, cfg, "vercel", "Vercel AI Gateway", func(k string) {
		ready, apiKey = true, k
	})

	if !rec.prompted {
		t.Fatal("a provider with no credential must prompt for its API key")
	}
	if !strings.Contains(rec.prompt, "Vercel AI Gateway") {
		t.Errorf("prompt = %q, want it to name the provider", rec.prompt)
	}
	rec.onSubmit("sk-gw-1", true)

	if !ready {
		t.Error("onReady must run after the key is entered")
	}
	if apiKey != "" {
		t.Errorf("onReady key = %q, want \"\" (the auth store holds it)", apiKey)
	}
	if got, ok := store.GetAPIKey("vercel"); !ok || got != "sk-gw-1" {
		t.Errorf("stored key = %q (ok=%v), want sk-gw-1", got, ok)
	}
}

// TestSetupProvider_SkipsPromptWhenKeyPresent asserts an already-credentialed
// provider is never asked again — from config first, then from the auth store.
func TestSetupProvider_SkipsPromptWhenKeyPresent(t *testing.T) {
	t.Setenv("AI_GATEWAY_API_KEY", "")

	t.Run("config api_key", func(t *testing.T) {
		store := mustStore(t)
		registerLoginStore(store)
		t.Cleanup(func() { registerLoginStore(nil) })

		rec := &credentialRecorder{}
		cfg := &config.Config{Providers: []config.ProviderConfig{{ID: "vercel", APIKey: "cfg-key"}}}
		ready := false
		setupProviderCredential(newCredentialHost(rec), cfg, "vercel", "Vercel AI Gateway", func(string) { ready = true })

		if rec.prompted {
			t.Errorf("config api_key must skip the prompt, got %q", rec.prompt)
		}
		if !ready {
			t.Error("onReady must still run")
		}
		if _, ok := store.GetAPIKey("vercel"); ok {
			t.Error("no key must be stored when one already exists")
		}
	})

	t.Run("auth store", func(t *testing.T) {
		store := mustStore(t)
		if err := store.SetAPIKey("vercel", "stored-key"); err != nil {
			t.Fatalf("SetAPIKey: %v", err)
		}
		registerLoginStore(store)
		t.Cleanup(func() { registerLoginStore(nil) })

		rec := &credentialRecorder{}
		setupProviderCredential(newCredentialHost(rec), &config.Config{}, "vercel", "Vercel AI Gateway", func(string) {})

		if rec.prompted {
			t.Errorf("a stored credential must skip the prompt, got %q", rec.prompt)
		}
	})
}

// TestSetupProvider_EnvVarPrefill pins the catalog env-var half: the prompt
// pre-fill comes from the provider's catalog env var, and when that variable is
// set the credential resolves from the environment (nothing to ask).
func TestSetupProvider_EnvVarPrefill(t *testing.T) {
	store := mustStore(t)
	registerLoginStore(store)
	t.Cleanup(func() { registerLoginStore(nil) })
	cfg := &config.Config{}

	t.Run("set", func(t *testing.T) {
		t.Setenv("AI_GATEWAY_API_KEY", "  gw-env-key  ")

		if got := providerKeyPrefill("vercel"); got != "gw-env-key" {
			t.Errorf("providerKeyPrefill(vercel) = %q, want the trimmed env value", got)
		}
		cred := resolveProviderCredential(cfg, "vercel")
		if cred.Key != "gw-env-key" || cred.Source != credentialEnv {
			t.Errorf("credential = %+v, want the env-sourced key", cred)
		}

		rec := &credentialRecorder{}
		setupProviderCredential(newCredentialHost(rec), cfg, "vercel", "Vercel AI Gateway", func(string) {})
		if rec.prompted {
			t.Errorf("the env var already provides the key; prompt = %q", rec.prompt)
		}
	})

	t.Run("unset falls back to the declared name", func(t *testing.T) {
		t.Setenv("AI_GATEWAY_API_KEY", "")
		if got := providerKeyPrefill("vercel"); got != "" {
			t.Errorf("providerKeyPrefill(vercel) = %q, want empty with the env unset", got)
		}
		if got := loginEnvKeys("vercel"); len(got) == 0 || got[0] != "AI_GATEWAY_API_KEY" {
			t.Errorf("loginEnvKeys(vercel) = %v, want the catalog env name", got)
		}
	})
}

// TestSetupProvider_HeadlessIsActionable asserts a host that cannot prompt
// (no input callback: scripts, piped runs) does not wedge the flow and reports
// the three ways to add a key.
func TestSetupProvider_HeadlessIsActionable(t *testing.T) {
	t.Setenv("AI_GATEWAY_API_KEY", "")
	store := mustStore(t)
	registerLoginStore(store)
	t.Cleanup(func() { registerLoginStore(nil) })

	ready := false
	setupProviderCredential(core.Context{}, &config.Config{}, "vercel", "Vercel AI Gateway", func(string) { ready = true })
	if !ready {
		t.Fatal("a headless host must complete the flow, not park on an impossible prompt")
	}

	msg := missingProviderKeyMessage("vercel", loginEnvKeys("vercel"))
	for _, want := range []string{"vercel", "/login:vercel:apikey", "api_key", "AI_GATEWAY_API_KEY"} {
		if !strings.Contains(msg, want) {
			t.Errorf("headless message %q must name %q", msg, want)
		}
	}
}
