// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/core"
)

// TestLoginProviders_IncludeCatalogProviders pins the catalog-driven sign-on
// surface (bugs.md "Vercel AI Gateway: no way to add an API key"): /login must
// advertise every catalog provider that can be given a key — vercel first among
// the gateway-style ones — while the curated OAuth/device-code entries keep
// their richer kinds.
func TestLoginProviders_IncludeCatalogProviders(t *testing.T) {
	cmd := &LoginCommand{Store: mustStore(t)}
	ctx := core.Context{}

	vercel, ok := findCompletion(cmd.CompleteArgs(ctx, ""), "vercel")
	if !ok {
		t.Fatalf("the /login provider list must offer the catalog provider %q", "vercel")
	}
	// The completion hint names the catalog env var, so "where do I put the
	// key?" is answerable at the point of use.
	if !strings.Contains(vercel.Description, "AI_GATEWAY_API_KEY") {
		t.Errorf("vercel completion description = %q, want the catalog env var AI_GATEWAY_API_KEY", vercel.Description)
	}

	// vercel is API-key addressable, so its kind completions must offer apikey.
	kinds := completionValues(cmd.CompleteArgs(ctx, "vercel "))
	if !containsStr(kinds, "apikey") {
		t.Errorf("/login:vercel kinds = %v, want apikey", kinds)
	}

	// Curated providers keep the capabilities the bare catalog cannot express.
	copilotKinds := completionValues(cmd.CompleteArgs(ctx, "copilot "))
	if !containsStr(copilotKinds, "oauth") {
		t.Errorf("/login:copilot kinds = %v, want oauth to survive catalog derivation", copilotKinds)
	}
	codexKinds := completionValues(cmd.CompleteArgs(ctx, "openai-codex "))
	if !containsStr(codexKinds, "apikey") || !containsStr(codexKinds, "oauth") {
		t.Errorf("/login:openai-codex kinds = %v, want apikey+oauth", codexKinds)
	}
}

// TestLoginCompletions_CatalogProvider asserts the prefix completion of a
// catalog-only provider (nothing in the curated list mentions it).
func TestLoginCompletions_CatalogProvider(t *testing.T) {
	cmd := &LoginCommand{Store: mustStore(t)}
	got := completionValues(cmd.CompleteArgs(core.Context{}, "ver"))
	if !containsStr(got, "vercel") {
		t.Errorf("/login:ver* completions = %v, want vercel", got)
	}
}

// TestLogin_AliasStillResolves guards the credential aliasing the catalog
// derivation must not break: /login:codex and /login:openai-codex store under
// the canonical "openai" credential.
func TestLogin_AliasStillResolves(t *testing.T) {
	for _, alias := range []string{"codex", "openai-codex"} {
		if got := normalizeProviderID(alias); got != "openai" {
			t.Errorf("normalizeProviderID(%q) = %q, want openai", alias, got)
		}
		store := mustStore(t)
		cmd := &LoginCommand{Store: store}
		if err := cmd.Run(core.Context{}, []string{alias, "apikey", "sk-codex"}); err != nil {
			t.Fatalf("/login:%s:apikey: %v", alias, err)
		}
		key, ok := store.GetAPIKey("openai")
		if !ok || key != "sk-codex" {
			t.Errorf("/login:%s:apikey stored %q (ok=%v), want the openai credential", alias, key, ok)
		}
	}
}

func findCompletion(comps []core.ArgCompletion, value string) (core.ArgCompletion, bool) {
	for _, c := range comps {
		if c.Value == value {
			return c, true
		}
	}
	return core.ArgCompletion{}, false
}

// (completionValues and containsStr live in review_file_test.go.)
