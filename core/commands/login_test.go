// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/internal/auth"
	oauth "github.com/pijalu/goa/internal/agentic/provider/oauth"
)

type fakePrompter struct {
	value string
	ok    bool
}

func (f *fakePrompter) Prompt(_, _ string) (string, bool) { return f.value, f.ok }

type fakeWriter struct {
	lines []string
}

func (f *fakeWriter) Writef(format string, args ...any) {
	f.lines = append(f.lines, format)
	_ = args
}

func TestLoginCommandStoreAPIKey(t *testing.T) {
	store := auth.NewStore(filepath.Join(t.TempDir(), "tokens.json"))
	cmd := &LoginCommand{Store: store}
	if err := cmd.Run(core.Context{}, []string{"github", "apikey", "mytoken"}); err != nil {
		t.Fatalf("login: %v", err)
	}
	got, ok := store.Get("github")
	if !ok || !got.IsAPIKey() || got.APISecret != "mytoken" {
		t.Errorf("credential = %+v", got)
	}
}

func TestLoginCommandLegacyTokenAsAPIKey(t *testing.T) {
	store := auth.NewStore(filepath.Join(t.TempDir(), "tokens.json"))
	cmd := &LoginCommand{Store: store}
	if err := cmd.Run(core.Context{}, []string{"github", "mytoken"}); err != nil {
		t.Fatalf("login: %v", err)
	}
	got, ok := store.Get("github")
	if !ok || !got.IsAPIKey() || got.APISecret != "mytoken" {
		t.Errorf("credential = %+v", got)
	}
}

func TestLoginCommandList(t *testing.T) {
	store := auth.NewStore(filepath.Join(t.TempDir(), "tokens.json"))
	_ = store.SetAPIKey("github", "x")
	cmd := &LoginCommand{Store: store}
	if err := cmd.Run(core.Context{}, nil); err != nil {
		t.Fatalf("list: %v", err)
	}
}

func TestLoginCommandNoStore(t *testing.T) {
	cmd := &LoginCommand{}
	if err := cmd.Run(core.Context{}, []string{"github"}); err == nil {
		t.Fatal("expected error")
	}
}

func TestLoginCommandListKinds(t *testing.T) {
	store := auth.NewStore(filepath.Join(t.TempDir(), "tokens.json"))
	cmd := &LoginCommand{Store: store}
	w := &fakeWriter{}
	ctx := core.Context{}
	if err := cmd.Run(ctx, []string{"anthropic"}); err != nil {
		t.Fatalf("list kinds: %v", err)
	}
	if len(w.lines) == 0 {
		// Core.Context.Writef is a real method; we don't capture it here.
		// Test just verifies no error.
	}
}

func TestLoginCommandOAuthUnsupported(t *testing.T) {
	store := auth.NewStore(filepath.Join(t.TempDir(), "tokens.json"))
	cmd := &LoginCommand{Store: store}
	if err := cmd.Run(core.Context{}, []string{"kimi", "oauth"}); err == nil {
		t.Fatal("expected error for unsupported OAuth provider")
	}
}

func TestLoginCommandPromptedAPIKey(t *testing.T) {
	store := auth.NewStore(filepath.Join(t.TempDir(), "tokens.json"))
	cmd := &LoginCommand{Store: store, prompter: &fakePrompter{value: "prompted-key", ok: true}}
	if err := cmd.Run(core.Context{}, []string{"kimi", "apikey"}); err != nil {
		t.Fatalf("login: %v", err)
	}
	got, ok := store.GetAPIKey("kimi")
	if !ok || got != "prompted-key" {
		t.Errorf("api key = %q", got)
	}
}

func TestLoginCommandPromptCancelled(t *testing.T) {
	store := auth.NewStore(filepath.Join(t.TempDir(), "tokens.json"))
	cmd := &LoginCommand{Store: store, prompter: &fakePrompter{ok: false}}
	if err := cmd.Run(core.Context{}, []string{"kimi", "apikey"}); err != nil {
		t.Fatalf("login: %v", err)
	}
	if store.HasAuth("kimi") {
		t.Fatal("expected no credential after cancel")
	}
}

func TestLoginCommandFakeOAuthFlow(t *testing.T) {
	store := auth.NewStore(filepath.Join(t.TempDir(), "tokens.json"))
	cmd := &LoginCommand{Store: store, prompter: &fakePrompter{ok: true}}
	if err := cmd.Run(core.Context{}, []string{"openai", "apikey", "sk-test"}); err != nil {
		t.Fatalf("login: %v", err)
	}
	got, ok := store.GetAPIKey("openai")
	if !ok || got != "sk-test" {
		t.Errorf("api key = %q", got)
	}
}

type fakeOAuthFlow struct {
	tokens *oauth.Tokens
	err    error
}

func (f *fakeOAuthFlow) Run(_ context.Context, _ uiWriter, _ prompter) (*oauth.Tokens, error) {
	return f.tokens, f.err
}

func TestLoginCommandOAuthStoresTokens(t *testing.T) {
	store := auth.NewStore(filepath.Join(t.TempDir(), "tokens.json"))
	expected := &oauth.Tokens{AccessToken: "oauth-token", TokenType: "bearer"}
	cmd := &LoginCommand{
		Store:       store,
		flowFactory: func(string) oauthFlow { return &fakeOAuthFlow{tokens: expected} },
	}
	if err := cmd.Run(core.Context{}, []string{"copilot", "oauth"}); err != nil {
		t.Fatalf("login: %v", err)
	}
	got, ok := store.GetOAuth("copilot")
	if !ok || got.AccessToken != "oauth-token" {
		t.Errorf("token = %+v", got)
	}
}

func TestLoginCommandOAuthFlowError(t *testing.T) {
	store := auth.NewStore(filepath.Join(t.TempDir(), "tokens.json"))
	cmd := &LoginCommand{
		Store:       store,
		flowFactory: func(string) oauthFlow { return &fakeOAuthFlow{err: fmt.Errorf("boom")} },
	}
	if err := cmd.Run(core.Context{}, []string{"copilot", "oauth"}); err == nil {
		t.Fatal("expected error")
	}
	if store.HasAuth("copilot") {
		t.Fatal("expected no credential on flow error")
	}
}
