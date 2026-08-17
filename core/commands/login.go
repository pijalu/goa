// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"context"
	"fmt"
	"runtime"
	"strings"

	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/core/commands/help"
	oauth "github.com/pijalu/goa/internal/agentic/provider/oauth"
	"github.com/pijalu/goa/internal/ansi"
	"github.com/pijalu/goa/internal/auth"
	"github.com/pijalu/goa/plugins"
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

// NewLoginCommand builds a login command bound to an auth store.
func NewLoginCommand(store *auth.Store) *LoginCommand { return &LoginCommand{Store: store} }

// loginStoreFactory supplies the shared auth store to other commands (the
// provider picker) that need to launch a codex OAuth login without holding a
// *LoginCommand. Set during RegisterAll; nil until then.
var loginStoreFactory func() *auth.Store

// registerLoginStore wires the auth-store accessor used by the provider
// picker's codex auth choice.
func registerLoginStore(store *auth.Store) { loginStoreFactory = func() *auth.Store { return store } }

// sharedAuthStore returns the registered auth store, or nil.
func sharedAuthStore() *auth.Store {
	if loginStoreFactory == nil {
		return nil
	}
	return loginStoreFactory()
}

// loginFlowRunner runs a provider OAuth login from another command. It is a
// package-level seam so tests can substitute a fake flow and avoid real
// network/browser interaction. Defaults to the real login command.
var loginFlowRunner = func(ctx core.Context, provider string) error {
	store := sharedAuthStore()
	if store == nil {
		return fmt.Errorf("auth store not configured")
	}
	return NewLoginCommand(store).Run(ctx, []string{provider, "oauth"})
}

// ShortHelp returns a short description.
func (c *LoginCommand) ShortHelp() string { return "Manage OAuth logins and API keys for providers" }

// LongHelp returns usage help.
func (c *LoginCommand) LongHelp() string {
	return help.LongHelp(c.Name())
}

// loginProviders lists the providers that expose a sign-on flow, in display
// order. openai-codex is the canonical Codex entry; codex is kept as an alias.
var loginProviders = []string{"copilot", "github", "openai", "openai-codex", "codex", "anthropic", "kimi"}

// CompleteArgs provides argument completions for providers and auth kinds.
// prefix is the raw text after "/login:" (e.g. "openai-codex o"). When the
// first token is a known provider, the second token completes to that
// provider's auth kinds.
func (c *LoginCommand) CompleteArgs(ctx core.Context, prefix string) []core.ArgCompletion {
	provider, kindPrefix, completingKind := splitLoginPrefix(prefix)
	if completingKind {
		return completeAuthKinds(provider, kindPrefix)
	}
	var comps []core.ArgCompletion
	for _, p := range loginProviders {
		if prefix == "" || strings.HasPrefix(p, prefix) {
			comps = append(comps, core.ArgCompletion{Value: p, Description: "provider"})
		}
	}
	return comps
}

// splitLoginPrefix splits the raw completion prefix into the provider token and
// the in-progress kind token. completingKind is true once the user has typed a
// provider followed by a space.
func splitLoginPrefix(prefix string) (provider, kindPrefix string, completingKind bool) {
	i := strings.IndexByte(prefix, ' ')
	if i < 0 {
		return prefix, "", false
	}
	return prefix[:i], prefix[i+1:], true
}

// completeAuthKinds returns kind completions for a provider. Codex providers
// additionally expose the explicit oauth:device variant.
func completeAuthKinds(provider, prefix string) []core.ArgCompletion {
	kinds := supportedAuthKinds(normalizeProviderID(provider))
	var comps []core.ArgCompletion
	for _, k := range kinds {
		if prefix == "" || strings.HasPrefix(k, prefix) {
			comps = append(comps, core.ArgCompletion{Value: k, Description: "auth kind"})
		}
		if k == "oauth" && isCodexProvider(normalizeProviderID(provider)) {
			if prefix == "" || strings.HasPrefix("oauth:device", prefix) {
				comps = append(comps, core.ArgCompletion{Value: "oauth:device", Description: "headless device code"})
			}
		}
	}
	return comps
}

// normalizeProviderID maps login aliases onto the canonical auth-store key.
// /login:codex and /login:openai-codex share the "openai" Codex credential.
func normalizeProviderID(provider string) string {
	switch strings.ToLower(provider) {
	case "codex", "openai-codex":
		return "openai"
	default:
		return strings.ToLower(provider)
	}
}

// Run executes the login command.
func (c *LoginCommand) Run(ctx core.Context, args []string) error {
	if c.Store == nil {
		return fmt.Errorf("auth store not configured")
	}

	if len(args) == 0 {
		return c.listProviders(ctx)
	}

	provider := normalizeProviderID(args[0])
	if len(args) == 1 {
		return c.listKindsOrStartDefault(ctx, provider, args[0])
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
	if len(providers) > 0 {
		ctx.Writef("Stored providers:\n")
		for _, p := range providers {
			cred, _ := c.Store.Get(p)
			ctx.Writef("  %s (%s)\n", p, string(cred.Kind))
		}
		ctx.Writef("\n")
	}
	// Always advertise the available sign-on options so /login doubles as
	// discovery, not only a view of stored credentials.
	ctx.Writef("Available sign-on:\n")
	for _, p := range loginProviders {
		ctx.Writef("  %-14s %s\n", p, strings.Join(supportedAuthKinds(normalizeProviderID(p)), ", "))
	}
	ctx.Writef("Run /login:<provider>:<kind> to authenticate.\n")
	return nil
}

func (c *LoginCommand) listKindsOrStartDefault(ctx core.Context, provider, display string) error {
	kinds := supportedAuthKinds(provider)
	if len(kinds) == 0 {
		ctx.Writef("Unknown provider %q. Supported auth: apikey.\n", provider)
		return nil
	}
	if len(kinds) == 1 {
		// Default to the only supported kind.
		return c.Run(ctx, []string{provider, kinds[0]})
	}
	// Multi-kind provider: offer an interactive picker when a selector callback
	// is available (pi wizard style); otherwise print the kinds as text.
	if ctx.SelectOptionFunc != nil {
		return c.pickAuthKind(ctx, provider, display, kinds)
	}
	ctx.Writef("Provider %q supports:\n", provider)
	for _, k := range kinds {
		ctx.Writef("  %s\n", k)
	}
	ctx.Writef("Run /login:%s:<kind> to authenticate.\n", provider)
	return nil
}

// pickAuthKind renders a selectable auth-kind menu for a multi-kind provider
// and dispatches the chosen kind into the normal Run path. display is the
// user-typed provider alias shown in the title; provider is the store key.
func (c *LoginCommand) pickAuthKind(ctx core.Context, provider, display string, kinds []string) error {
	items := make([]tui.SelectorItem, 0, len(kinds)+1)
	for _, k := range kinds {
		items = append(items, tui.SelectorItem{
			Value:       k,
			Label:       authKindLabel(k),
			Description: authKindDescription(k, provider),
		})
	}
	if isCodexProvider(provider) {
		items = append(items, tui.SelectorItem{
			Value:       "oauth:device",
			Label:       "Sign in with ChatGPT (device code)",
			Description: "Headless login — paste a code on another device",
		})
	}
	ctx.SelectOption("Sign in to "+display+":", items, kinds[0], func(v string, ok bool) {
		if !ok || v == "" {
			return
		}
		args := []string{provider, v}
		if v == "oauth:device" {
			args = []string{provider, "oauth", "device"}
		}
		_ = c.Run(ctx, args)
	})
	return nil
}

func authKindLabel(kind string) string {
	switch kind {
	case "oauth":
		return "Sign in with OAuth (browser)"
	case "apikey":
		return "Use an API key"
	default:
		return kind
	}
}

func authKindDescription(kind, provider string) string {
	switch kind {
	case "oauth":
		return "Browser-based sign-in for " + provider
	case "apikey":
		return "Paste a pre-generated API key"
	default:
		return ""
	}
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
	switch provider {
	case "codex", "openai", "openai-codex":
		return true
	default:
		return false
	}
}

// codexMethodSelectorItems renders the browser-vs-device choice as a
// navigable list, mirroring pi's showAuthSelect option list (arrow-key
// navigate, enter to pick, esc cancels → default).
func codexMethodSelectorItems() []tui.SelectorItem {
	return []tui.SelectorItem{
		{
			Value:         codexMethodBrowser,
			Label:         "Sign in with browser",
			Description:   "Opens a browser tab; paste the redirect URL if the callback is missed",
			PreserveOrder: true,
		},
		{
			Value:         codexMethodDevice,
			Label:         "Use a device code",
			Description:   "Headless login — paste a code shown here on another device",
			PreserveOrder: true,
		},
	}
}

// selectCodexMethod asks the user to pick browser vs device-code login. In a
// live TUI (SelectOptionFunc wired) the choice is a navigable option list
// (Pi showAuthSelect style), not a free-text prompt; headless falls back to
// the text prompter. A nil/cancelled prompt defaults to browser (the common
// case) so scripted flows keep working.
//
// SelectOptionFunc is async (the production wiring returns immediately and
// delivers via a callback goroutine), so this blocks on a result channel.
// That is safe: for codex OAuth, handleOAuth runs on the runOAuthFlow
// background goroutine whenever an EventBus is wired, and the selector
// callback itself marshals onto the TUI commandLoop (app.apply) — blocking
// here cannot deadlock the loop.
func (c *LoginCommand) selectCodexMethod(ctx core.Context) (string, error) {
	if ctx.SelectOptionFunc != nil {
		type result struct {
			v  string
			ok bool
		}
		resCh := make(chan result, 1)
		ctx.SelectOption("OpenAI Codex login method", codexMethodSelectorItems(), codexMethodBrowser,
			func(v string, ok bool) { resCh <- result{v, ok} })
		r := <-resCh
		if !r.ok || r.v == "" {
			return codexMethodBrowser, nil
		}
		return r.v, nil
	}
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
		return &codexDeviceFlow{login: oauth.LoginCodexDevice, loginUI: oauth.LoginCodexDeviceUI}
	case provider == "copilot" || provider == "github":
		return &deviceCodeFlow{provider: oauth.NewGitHubCopilotOAuth()}
	default:
		return nil
	}
}

// runOAuthFlow executes the flow and persists the resulting tokens.
//
// The flow is blocking — it waits for a browser callback or device-code poll.
// In a live TUI (EventBus wired) it must run on a background goroutine so it
// does not freeze the UI; progress and the result are delivered via
// ctx.Writef / ctx.Flash, which post to the event bus and are goroutine-safe.
// Headless (no EventBus — tests, scripts) runs synchronously so the caller
// observes completion and errors directly.
func (c *LoginCommand) runOAuthFlow(ctx core.Context, provider string, flow oauthFlow) error {
	if flow == nil {
		return fmt.Errorf("provider %q does not support OAuth", provider)
	}
	if ctx.EventBus == nil {
		return c.runOAuthFlowSync(ctx, provider, flow)
	}
	ctx.Writef("Starting OAuth login for %s...\n", provider)
	go func() {
		if err := c.runOAuthFlowSync(ctx, provider, flow); err != nil {
			ctx.Flash(fmt.Sprintf("OAuth login for %s failed: %v", provider, err))
		}
	}()
	return nil
}

// runOAuthFlowSync runs the flow to completion and persists the tokens.
func (c *LoginCommand) runOAuthFlowSync(ctx core.Context, provider string, flow oauthFlow) error {
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
	case "codex", "openai", "openai-codex":
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
		return &codexBrowserFlow{login: oauth.LoginCodexBrowser, loginUI: oauth.LoginCodexBrowserUI}
	default:
		return nil
	}
}

// codexBrowserFlow wraps the oauth package browser login so the command layer
// can substitute a fake in tests via LoginCommand.flowFactory. When running
// against a real TUI it bridges the auth URL and manual-code prompt into the
// host UI via CodexUIOpts.
type codexBrowserFlow struct {
	login func(ctx context.Context) (*oauth.Tokens, error)
	// loginUI, when set, is the UI-bridged variant preferred in a TUI context.
	loginUI func(ctx context.Context, ui oauth.CodexUIOpts) (*oauth.Tokens, error)
}

func (f *codexBrowserFlow) Run(ctx context.Context, w uiWriter, p prompter) (*oauth.Tokens, error) {
	if f.loginUI != nil {
		return f.loginUI(ctx, codexUIFromWriter(w, p))
	}
	return f.login(ctx)
}

// codexDeviceFlow wraps the oauth package device-code login.
type codexDeviceFlow struct {
	login   func(ctx context.Context) (*oauth.Tokens, error)
	loginUI func(ctx context.Context, ui oauth.CodexUIOpts) (*oauth.Tokens, error)
}

func (f *codexDeviceFlow) Run(ctx context.Context, w uiWriter, p prompter) (*oauth.Tokens, error) {
	if f.loginUI != nil {
		return f.loginUI(ctx, codexUIFromWriter(w, p))
	}
	return f.login(ctx)
}

// codexUIFromWriter builds CodexUIOpts that surface the auth URL / device code
// through the command's writer and the manual-paste prompt through the
// prompter, mirroring Pi's login dialog:
//   - the authorization URL is an OSC-8 clickable hyperlink with a dim
//     "Ctrl/Cmd+click to open" hint, and the browser is auto-opened
//     (best-effort — failures fall back to the printed link),
//   - the device flow shows the verification URL as a hyperlink plus a
//     prominent "Enter code: <userCode>" line and a waiting message,
//   - the manual-paste prompt advertises "(esc to cancel, enter to submit)".
// A nil prompter disables the manual-paste fallback.
func codexUIFromWriter(w uiWriter, p prompter) oauth.CodexUIOpts {
	opts := oauth.CodexUIOpts{}
	if w != nil {
		opts.NotifyURL = func(u string) {
			w.Writef("Open this URL to sign in:\n%s\n", ansi.Hyperlink(u, u))
			w.Writef("(%s — browser opened automatically)\n", clickHint(u))
		}
		opts.NotifyDevice = func(a oauth.CodexDeviceAuth) {
			w.Writef("Open %s\n", ansi.Hyperlink(oauth.CodexDeviceVerificationURI, oauth.CodexDeviceVerificationURI))
			w.Writef("(%s)\n", clickHint(oauth.CodexDeviceVerificationURI))
			w.Writef("Enter code: %s\n", a.UserCode)
			w.Writef("Waiting for authentication...\n")
		}
		// Auto-open the browser (pi showAuth behavior). Best-effort: the
		// printed hyperlink remains as the manual fallback on failure.
		bridge := plugins.NewBrowserBridge()
		opts.OpenURL = func(u string) { _ = bridge.OpenURL(u) }
	}
	if p != nil {
		opts.PromptManualCode = func() (string, bool) {
			return p.Prompt("Paste authorization code",
				"Paste the redirect URL or code and press Enter (esc to cancel, enter to submit):")
		}
	}
	return opts
}

// clickHint renders the platform-appropriate "click to open" hint as a dim
// clickable hyperlink (pi showAuth/showDeviceCode style).
func clickHint(u string) string {
	hint := "Ctrl+click to open"
	if runtime.GOOS == "darwin" {
		hint = "Cmd+click to open"
	}
	return ansi.Hyperlink(u, hint)
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
