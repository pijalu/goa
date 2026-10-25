// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package provider

import (
	"testing"

	oauth "github.com/pijalu/goa/internal/agentic/provider/oauth"
	"github.com/pijalu/goa/internal/auth"
)

// resolveStartPlanAPIKey contract: the provider's own stored API key is an
// explicit override and wins; the family login's ZcodeJWT is the working
// credential; the catalog env vars are the headless fallback. The OAuth
// ACCESS token is deliberately NOT a fallback — the plan backend validates
// the zcode JWT identity, and sending the business token would produce an
// auth failure that looks like a server bug.
func TestResolveStartPlanAPIKey(t *testing.T) {
	newStore := func(t *testing.T, oauth *oauth.Tokens) *auth.Store {
		t.Helper()
		st := mustAuthStore(t)
		if oauth != nil {
			if err := st.SetOAuth("zai", oauth); err != nil {
				t.Fatal(err)
			}
		}
		return st
	}

	t.Run("own stored key wins over family JWT", func(t *testing.T) {
		store := newStore(t, &oauth.Tokens{AccessToken: "acc", ZcodeJWT: "zjwt"})
		if err := store.SetAPIKey("zai-start-plan", "own-key"); err != nil {
			t.Fatal(err)
		}
		if got := resolveStartPlanAPIKey(store, "zai-start-plan", "zai"); got != "own-key" {
			t.Errorf("got %q, want the explicit own key", got)
		}
	})

	t.Run("family zcode JWT is the working credential", func(t *testing.T) {
		store := newStore(t, &oauth.Tokens{AccessToken: "acc", ZcodeJWT: "zjwt"})
		if got := resolveStartPlanAPIKey(store, "zai-start-plan", "zai"); got != "zjwt" {
			t.Errorf("got %q, want the family zcode JWT", got)
		}
	})

	t.Run("access token is not a fallback", func(t *testing.T) {
		store := newStore(t, &oauth.Tokens{AccessToken: "acc-only"})
		t.Setenv("ZAI_START_PLAN_API_KEY", "")
		if got := resolveStartPlanAPIKey(store, "zai-start-plan", "zai"); got != "" {
			t.Errorf("got %q, want empty: the OAuth access token must never reach the plan backend", got)
		}
	})

	t.Run("env fallback", func(t *testing.T) {
		store := newStore(t, nil)
		t.Setenv("ZAI_START_PLAN_API_KEY", "env-key")
		if got := resolveStartPlanAPIKey(store, "zai-start-plan", "zai"); got != "env-key" {
			t.Errorf("got %q, want the env key", got)
		}
	})

	t.Run("bigmodel family resolves its own JWT", func(t *testing.T) {
		store := newStore(t, nil)
		if err := store.SetOAuth("bigmodel", &oauth.Tokens{AccessToken: "bm-acc", ZcodeJWT: "bmjwt"}); err != nil {
			t.Fatal(err)
		}
		t.Setenv("BIGMODEL_START_PLAN_API_KEY", "")
		if got := resolveStartPlanAPIKey(store, "bigmodel-start-plan", "bigmodel"); got != "bmjwt" {
			t.Errorf("got %q, want the bigmodel zcode JWT", got)
		}
	})

	t.Run("families do not cross", func(t *testing.T) {
		store := newStore(t, nil)
		t.Setenv("ZAI_START_PLAN_API_KEY", "")
		t.Setenv("BIGMODEL_START_PLAN_API_KEY", "")
		// zai JWT must not satisfy a bigmodel provider.
		if err := store.SetOAuth("zai", &oauth.Tokens{AccessToken: "acc", ZcodeJWT: "zjwt"}); err != nil {
			t.Fatal(err)
		}
		if got := resolveStartPlanAPIKey(store, "bigmodel-start-plan", "bigmodel"); got != "" {
			t.Errorf("got %q, want empty: a zai JWT must never serve the bigmodel family", got)
		}
	})
}

// The generic resolver must route Start Plan providers into the dedicated
// chain — a plain OAuth access token in the family slot must not leak into
// the plan runtime via the standard path.
func TestResolveAPIKeyRoutesStartPlan(t *testing.T) {
	store := mustAuthStore(t)
	if err := store.SetOAuth("zai", &oauth.Tokens{AccessToken: "acc-only"}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ZAI_START_PLAN_API_KEY", "")
	if got := resolveAPIKey(store, "zai-start-plan"); got != "" {
		t.Errorf("got %q, want empty (access token must not leak into Start Plan)", got)
	}
	if err := store.SetOAuth("zai", &oauth.Tokens{AccessToken: "acc", ZcodeJWT: "zjwt"}); err != nil {
		t.Fatal(err)
	}
	if got := resolveAPIKey(store, "zai-start-plan"); got != "zjwt" {
		t.Errorf("got %q, want the family zcode JWT", got)
	}
}
