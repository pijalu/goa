// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"context"
	"fmt"
	"strings"

	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/core/commands/help"
	oauth "github.com/pijalu/goa/internal/agentic/provider/oauth"
	"github.com/pijalu/goa/internal/auth"
	"github.com/pijalu/goa/tui"
)

// prompter abstracts a single-line user prompt so the command can be tested
// without a real TUI.
type prompter interface {
	Prompt(title, question string) (string, bool)
}

// clarifier adapts core.Context.ClarifyFunc to the prompter interface.
type clarifier struct {
	ctx *core.Context
}

func (c *clarifier) Prompt(title, question string) (string, bool) {
	if c.ctx == nil || c.ctx.ClarifyFunc == nil {
		return "", false
	}
	card := tui.NewClarifyCard(title, "", question, nil)
	return c.ctx.ClarifyFunc(card)
}

// oauthFlow abstracts the two supported OAuth flows (device code and
// authorization code) so tests can inject a fake flow.
type oauthFlow interface {
	Run(ctx context.Context, writer uiWriter, prompter prompter) (*oauth.Tokens, error)
}

// uiWriter matches the subset of core.Context used for writing output.
type uiWriter interface {
	Writef(format string, args ...any)
}

// LoginCommand handles /login for managing provider credentials.
type LoginCommand struct {
	Store *auth.Store
	// prompter is optional; if nil, the command uses ctx.ClarifyFunc.
	prompter prompter
	// flowFactory is optional; if nil, the command uses the built-in providers.
	flowFactory func(string) oauthFlow
}

// Name returns the command name.
func (c *LoginCommand) Name() string { return "login" }

// Aliases returns command aliases.
func (c *LoginCommand) Aliases() []string { return nil }

// ShortHelp returns a short description.
func (c *LoginCommand) ShortHelp() string { return "Manage OAuth logins and API keys for providers" }

// LongHelp returns usage help.
func (c *LoginCommand) LongHelp() string {
	return help.LongHelp(c.Name())
}

// CompleteArgs provides argument completions for providers and auth kinds.
func (c *LoginCommand) CompleteArgs(ctx core.Context, prefix string) []core.ArgCompletion {
	providers := []string{"copilot", "github", "codex", "anthropic", "openai", "kimi"}
	var comps []core.ArgCompletion
	for _, p := range providers {
		if prefix == "" || strings.HasPrefix(p, prefix) {
			comps = append(comps, core.ArgCompletion{Value: p, Description: "provider"})
		}
	}
	return comps
}

// Run executes the login command.
func (c *LoginCommand) Run(ctx core.Context, args []string) error {
	if c.Store == nil {
		return fmt.Errorf("auth store not configured")
	}

	if len(args) == 0 {
		return c.listProviders(ctx)
	}

	provider := args[0]
	if len(args) == 1 {
		return c.listKindsOrStartDefault(ctx, provider)
	}

	authKind := strings.ToLower(args[1])
	rest := args[2:]

	switch authKind {
	case "apikey":
		return c.handleAPIKey(ctx, provider, rest)
	case "oauth":
		return c.handleOAuth(ctx, provider, rest)
	default:
		// Legacy: /login:<provider>:<token> stored as API key.
		return c.handleAPIKey(ctx, provider, []string{authKind})
	}
}

func (c *LoginCommand) listProviders(ctx uiWriter) error {
	providers := c.Store.Providers()
	if len(providers) == 0 {
		ctx.Writef("No credentials stored.\n")
		return nil
	}
	ctx.Writef("Stored providers:\n")
	for _, p := range providers {
		cred, _ := c.Store.Get(p)
		kind := string(cred.Kind)
		ctx.Writef("  %s (%s)\n", p, kind)
	}
	return nil
}

func (c *LoginCommand) listKindsOrStartDefault(ctx core.Context, provider string) error {
	kinds := supportedAuthKinds(provider)
	if len(kinds) == 0 {
		ctx.Writef("Unknown provider %q. Supported auth: apikey.\n", provider)
		return nil
	}
	if len(kinds) == 1 {
		// Default to the only supported kind.
		args := []string{provider, kinds[0]}
		return c.Run(ctx, args)
	}
	ctx.Writef("Provider %q supports:\n", provider)
	for _, k := range kinds {
		ctx.Writef("  %s\n", k)
	}
	ctx.Writef("Run /login:%s:<kind> to authenticate.\n", provider)
	return nil
}

func (c *LoginCommand) handleAPIKey(ctx core.Context, provider string, rest []string) error {
	key := ""
	if len(rest) > 0 {
		key = rest[0]
	} else {
		p := c.resolvePrompter(ctx)
		var ok bool
		key, ok = p.Prompt(fmt.Sprintf("API key for %s", provider), "Paste the API key and press Enter:")
		if !ok {
			ctx.Writef("API key entry cancelled.\n")
			return nil
		}
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return fmt.Errorf("API key cannot be empty")
	}
	if err := c.Store.SetAPIKey(provider, key); err != nil {
		return fmt.Errorf("store api key: %w", err)
	}
	ctx.Writef("Stored API key for %s\n", provider)
	return nil
}

func (c *LoginCommand) handleOAuth(ctx core.Context, provider string, rest []string) error {
	// Explicit device-code variant: /login:<provider>:oauth:device
	if len(rest) > 0 && strings.EqualFold(rest[0], "device") {
		return c.runOAuthFlow(ctx, provider, c.deviceFlow(provider))
	}
	if len(rest) > 0 {
		return fmt.Errorf("unknown oauth variant %q (supported: device)", rest[0])
	}

	flow := c.resolveFlowFactory()(provider)
	if flow == nil {
		return fmt.Errorf("provider %q does not support OAuth", provider)
	}

	// Codex/OpenAI offers browser (default) and device-code login methods,
	// mirroring pi's method selection prompt.
	if isCodexProvider(provider) {
		method, err := c.selectCodexMethod(ctx)
		if err != nil {
			return err
		}
		if method == codexMethodDevice {
			flow = c.deviceFlow(provider)
		}
	}
	return c.runOAuthFlow(ctx, provider, flow)
}

// codex login method identifiers.
const (
	codexMethodBrowser = "browser"
	codexMethodDevice  = "device"
)

func isCodexProvider(provider string) bool {
	return provider == "codex" || provider == "openai"
}

// selectCodexMethod asks the user to pick browser vs device-code login. A nil
// or cancelled prompter defaults to browser (the common case) so headless
// scripted flows keep working.
func (c *LoginCommand) selectCodexMethod(ctx core.Context) (string, error) {
	p := c.resolvePrompter(ctx)
	choice, ok := p.Prompt(
		"OpenAI Codex login method",
		"Select login method — 'browser' (default) or 'device' (headless):",
	)
	if !ok || strings.TrimSpace(choice) == "" {
		return codexMethodBrowser, nil
	}
	switch strings.ToLower(strings.TrimSpace(choice)) {
	case codexMethodBrowser:
		return codexMethodBrowser, nil
	case codexMethodDevice, "device_code":
		return codexMethodDevice, nil
	default:
		return "", fmt.Errorf("unknown login method %q (browser|device)", choice)
	}
}

// deviceFlow returns the device-code flow for the provider, honouring test
// injection via flowFactory.
func (c *LoginCommand) deviceFlow(provider string) oauthFlow {
	if c.flowFactory != nil {
		return c.flowFactory(provider)
	}
	switch {
	case isCodexProvider(provider):
		return &codexDeviceFlow{login: oauth.LoginCodexDevice}
	case provider == "copilot" || provider == "github":
		return &deviceCodeFlow{provider: oauth.NewGitHubCopilotOAuth()}
	default:
		return nil
	}
}

// runOAuthFlow executes the flow and persists the resulting tokens.
func (c *LoginCommand) runOAuthFlow(ctx core.Context, provider string, flow oauthFlow) error {
	if flow == nil {
		return fmt.Errorf("provider %q does not support OAuth", provider)
	}
	tokens, err := flow.Run(context.Background(), ctx, c.resolvePrompter(ctx))
	if err != nil {
		return fmt.Errorf("oauth flow: %w", err)
	}
	if err := c.Store.SetOAuth(provider, tokens); err != nil {
		return fmt.Errorf("store oauth tokens: %w", err)
	}
	ctx.Writef("OAuth login for %s succeeded.\n", provider)
	return nil
}

func (c *LoginCommand) resolvePrompter(ctx core.Context) prompter {
	if c.prompter != nil {
		return c.prompter
	}
	return &clarifier{ctx: &ctx}
}

// supportedAuthKinds returns the supported authentication kinds for a provider.
// Unknown providers default to API key only.
func supportedAuthKinds(provider string) []string {
	switch provider {
	case "copilot", "github":
		return []string{"oauth"}
	case "codex", "openai":
		return []string{"apikey", "oauth"}
	case "anthropic":
		return []string{"apikey"}
	case "kimi":
		return []string{"apikey"}
	default:
		return []string{"apikey"}
	}
}

// resolveFlowFactory returns the OAuth flow factory, overridable in tests.
func (c *LoginCommand) resolveFlowFactory() func(string) oauthFlow {
	if c.flowFactory != nil {
		return c.flowFactory
	}
	return c.newOAuthFlow
}

func (c *LoginCommand) newOAuthFlow(provider string) oauthFlow {
	switch provider {
	case "copilot", "github":
		return &deviceCodeFlow{provider: oauth.NewGitHubCopilotOAuth()}
	case "codex", "openai":
		// Default to the browser login; the device-code variant is selected by
		// the explicit ":device" suffix in handleOAuth.
		return &codexBrowserFlow{login: oauth.LoginCodexBrowser}
	default:
		return nil
	}
}

// codexBrowserFlow wraps the oauth package browser login so the command layer
// can substitute a fake in tests via LoginCommand.codexLogin.
type codexBrowserFlow struct {
	login func(ctx context.Context) (*oauth.Tokens, error)
}

func (f *codexBrowserFlow) Run(ctx context.Context, _ uiWriter, _ prompter) (*oauth.Tokens, error) {
	return f.login(ctx)
}

// codexDeviceFlow wraps the oauth package device-code login.
type codexDeviceFlow struct {
	login func(ctx context.Context) (*oauth.Tokens, error)
}

func (f *codexDeviceFlow) Run(ctx context.Context, _ uiWriter, _ prompter) (*oauth.Tokens, error) {
	return f.login(ctx)
}

// deviceCodeFlow performs GitHub's device-code OAuth flow.
type deviceCodeFlow struct {
	provider *oauth.GitHubCopilotOAuth
}

func (f *deviceCodeFlow) Run(ctx context.Context, writer uiWriter, _ prompter) (*oauth.Tokens, error) {
	resp, err := f.provider.RequestDeviceCode(ctx)
	if err != nil {
		return nil, fmt.Errorf("request device code: %w", err)
	}
	writer.Writef("Open %s and enter code: %s\n", resp.VerificationURI, resp.UserCode)
	writer.Writef("Waiting for authorization...\n")

	tokens, err := f.provider.PollForToken(ctx, resp.DeviceCode, resp.Interval)
	if err != nil {
		return nil, fmt.Errorf("poll token: %w", err)
	}
	return tokens, nil
}
