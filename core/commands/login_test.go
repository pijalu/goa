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
	oauth "github.com/pijalu/goa/internal/agentic/provider/oauth"
	"github.com/pijalu/goa/internal/auth"
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

// mustStore builds an auth store in a temp dir for login command tests.
func mustStore(t *testing.T) *auth.Store {
	t.Helper()
	s, err := auth.NewStore(filepath.Join(t.TempDir(), "tokens.json"))
	if err != nil {
		t.Fatalf("auth store: %v", err)
	}
	return s
}

func TestLoginCommandStoreAPIKey(t *testing.T) {
	store := mustStore(t)
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
	store := mustStore(t)
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
	store := mustStore(t)
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
	store := mustStore(t)
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
	store := mustStore(t)
	cmd := &LoginCommand{Store: store}
	if err := cmd.Run(core.Context{}, []string{"kimi", "oauth"}); err == nil {
		t.Fatal("expected error for unsupported OAuth provider")
	}
}

// TestSupportedAuthKinds_AnthropicApikeyOnly verifies Anthropic does not
// advertise OAuth (which the flow factory cannot fulfil), keeping the
// advertised kinds consistent with newOAuthFlow (E4).
func TestSupportedAuthKinds_AnthropicApikeyOnly(t *testing.T) {
	kinds := supportedAuthKinds("anthropic")
	if len(kinds) != 1 || kinds[0] != "apikey" {
		t.Errorf("anthropic kinds = %v, want [apikey]", kinds)
	}
}

func TestLoginCommand_AnthropicOauth_Rejected(t *testing.T) {
	store := mustStore(t)
	cmd := &LoginCommand{Store: store}
	if err := cmd.Run(core.Context{}, []string{"anthropic", "oauth"}); err == nil {
		t.Fatal("expected error for /login anthropic oauth")
	}
}

func TestLoginCommandPromptedAPIKey(t *testing.T) {
	store := mustStore(t)
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
	store := mustStore(t)
	cmd := &LoginCommand{Store: store, prompter: &fakePrompter{ok: false}}
	if err := cmd.Run(core.Context{}, []string{"kimi", "apikey"}); err != nil {
		t.Fatalf("login: %v", err)
	}
	if store.HasAuth("kimi") {
		t.Fatal("expected no credential after cancel")
	}
}

func TestLoginCommandFakeOAuthFlow(t *testing.T) {
	store := mustStore(t)
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
	store := mustStore(t)
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
	store := mustStore(t)
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

// --- W3: OpenAI/Codex apikey+oauth selection ---

func TestSupportedAuthKinds_OpenAIHasBoth(t *testing.T) {
	kinds := supportedAuthKinds("openai")
	if len(kinds) != 2 || kinds[0] != "apikey" || kinds[1] != "oauth" {
		t.Errorf("openai kinds = %v, want [apikey oauth]", kinds)
	}
	kinds = supportedAuthKinds("codex")
	if len(kinds) != 2 || kinds[0] != "apikey" || kinds[1] != "oauth" {
		t.Errorf("codex kinds = %v, want [apikey oauth]", kinds)
	}
}

func TestLoginCommandOpenAIAPIKey(t *testing.T) {
	store := mustStore(t)
	cmd := &LoginCommand{Store: store}
	if err := cmd.Run(core.Context{}, []string{"openai", "apikey", "sk-live"}); err != nil {
		t.Fatalf("login: %v", err)
	}
	got, ok := store.GetAPIKey("openai")
	if !ok || got != "sk-live" {
		t.Errorf("api key = %q", got)
	}
}

func TestLoginCommandOpenAIOAuthBrowserDefault(t *testing.T) {
	store := mustStore(t)
	expected := &oauth.Tokens{AccessToken: "codex-token", AccountID: "acct-1"}
	cmd := &LoginCommand{
		Store:       store,
		prompter:    &fakePrompter{value: "browser", ok: true},
		flowFactory: func(string) oauthFlow { return &fakeOAuthFlow{tokens: expected} },
	}
	if err := cmd.Run(core.Context{}, []string{"openai", "oauth"}); err != nil {
		t.Fatalf("login: %v", err)
	}
	got, ok := store.GetOAuth("openai")
	if !ok || got.AccessToken != "codex-token" || got.AccountID != "acct-1" {
		t.Errorf("token = %+v", got)
	}
}

func TestLoginCommandOpenAIOAuthDeviceSelection(t *testing.T) {
	store := mustStore(t)
	expected := &oauth.Tokens{AccessToken: "device-token"}
	cmd := &LoginCommand{
		Store:       store,
		prompter:    &fakePrompter{value: "device", ok: true},
		flowFactory: func(string) oauthFlow { return &fakeOAuthFlow{tokens: expected} },
	}
	if err := cmd.Run(core.Context{}, []string{"openai", "oauth"}); err != nil {
		t.Fatalf("login: %v", err)
	}
	if !store.HasAuth("openai") {
		t.Error("expected stored oauth credential")
	}
}

func TestLoginCommandOpenAIOAuthExplicitDeviceSuffix(t *testing.T) {
	store := mustStore(t)
	expected := &oauth.Tokens{AccessToken: "device-token"}
	cmd := &LoginCommand{
		Store:       store,
		flowFactory: func(string) oauthFlow { return &fakeOAuthFlow{tokens: expected} },
	}
	if err := cmd.Run(core.Context{}, []string{"openai", "oauth", "device"}); err != nil {
		t.Fatalf("login: %v", err)
	}
	if !store.HasAuth("openai") {
		t.Error("expected stored oauth credential via :device suffix")
	}
}

func TestLoginCommandOpenAIOAuthUnknownVariant(t *testing.T) {
	store := mustStore(t)
	cmd := &LoginCommand{Store: store}
	if err := cmd.Run(core.Context{}, []string{"openai", "oauth", "bogus"}); err == nil {
		t.Fatal("expected error for unknown oauth variant")
	}
}

func TestLoginCommandCodexMethodCancelDefaultsBrowser(t *testing.T) {
	store := mustStore(t)
	expected := &oauth.Tokens{AccessToken: "tok"}
	cmd := &LoginCommand{
		Store:       store,
		prompter:    &fakePrompter{ok: false}, // cancelled prompt
		flowFactory: func(string) oauthFlow { return &fakeOAuthFlow{tokens: expected} },
	}
	if err := cmd.Run(core.Context{}, []string{"codex", "oauth"}); err != nil {
		t.Fatalf("cancelled method prompt must default to browser: %v", err)
	}
	if !store.HasAuth("codex") {
		t.Error("expected stored credential after browser default")
	}
}

func TestLoginCommandCodexMethodUnknown(t *testing.T) {
	store := mustStore(t)
	cmd := &LoginCommand{
		Store:       store,
		prompter:    &fakePrompter{value: "weird", ok: true},
		flowFactory: func(string) oauthFlow { return &fakeOAuthFlow{} },
	}
	if err := cmd.Run(core.Context{}, []string{"codex", "oauth"}); err == nil {
		t.Fatal("expected unknown-method error")
	}
}

func TestLoginCommandNoPrompterNoCrash(t *testing.T) {
	// /login:openai with no ClarifyFunc and no injected prompter must not
	// panic — it lists the supported kinds.
	store := mustStore(t)
	cmd := &LoginCommand{Store: store}
	if err := cmd.Run(core.Context{}, []string{"openai"}); err != nil {
		t.Fatalf("listing kinds must not error: %v", err)
	}
}

func TestLoginCommandAPIKeyNoPrompterNoPanic(t *testing.T) {
	// /login:openai:apikey without prompter must fail gracefully, not panic.
	store := mustStore(t)
	cmd := &LoginCommand{Store: store}
	if err := cmd.Run(core.Context{}, []string{"openai", "apikey"}); err != nil {
		t.Fatalf("cancelled prompt returns nil error: %v", err)
	}
	if store.HasAuth("openai") {
		t.Error("no credential stored after cancelled prompt")
	}
}

func TestDeviceFlowFactoryInjection(t *testing.T) {
	cmd := &LoginCommand{}
	flow := cmd.deviceFlow("openai")
	if flow == nil {
		t.Fatal("openai device flow must exist")
	}
	if _, ok := flow.(*codexDeviceFlow); !ok {
		t.Errorf("flow type = %T, want *codexDeviceFlow", flow)
	}
	if got := cmd.deviceFlow("unknown"); got != nil {
		t.Errorf("unknown provider device flow = %v, want nil", got)
	}
}

func TestNewOAuthFlowCodex(t *testing.T) {
	cmd := &LoginCommand{}
	flow := cmd.newOAuthFlow("openai")
	if flow == nil {
		t.Fatal("openai oauth flow must exist")
	}
	if _, ok := flow.(*codexBrowserFlow); !ok {
		t.Errorf("flow type = %T, want *codexBrowserFlow", flow)
	}
	if got := cmd.newOAuthFlow("kimi"); got != nil {
		t.Errorf("kimi oauth flow = %v, want nil", got)
	}
}
