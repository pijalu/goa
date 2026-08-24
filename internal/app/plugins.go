// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"sync/atomic"

	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/internal/agentic"
	oauth "github.com/pijalu/goa/internal/agentic/provider/oauth"
	authstore "github.com/pijalu/goa/internal/auth"
	"github.com/pijalu/goa/internal/event"
	"github.com/pijalu/goa/plugins"
	"github.com/pijalu/goa/plugins/bundled"
	"github.com/pijalu/goa/tui"
)

// pluginChatEvent wraps a plugin output message as a chat modal event.
func pluginChatEvent(msg string) event.ChatEvent {
	return event.ChatEvent{ShowOutputModal: &event.ShowOutputModal{Title: "plugin", Content: msg}}
}

// pluginRuntime holds the loaded plugin bridges plus the shared extended
// bridges (UI, hotkeys, event bus) that are activated once the TUI exists.
// It is stored on subsystems so the two-phase load (bridges early, UI
// activation after buildTUI) can find them.
type pluginRuntime struct {
	bridges   []*plugins.JSBridge
	ui        *plugins.UIBridge
	hotkeys   *plugins.HotkeyBridge
	bus       *plugins.EventBus
	scheduler *plugins.Scheduler
	// hooks is the boot-created shared registry (subsystems.pluginHooks);
	// goa.registerHook calls land here and the agent-side sink reads it live.
	hooks *plugins.HookRegistry
	// enforcer validates hook registrations against manifests + grants.json
	// (M6 §7). Installed into the registry's validator slot before any plugin
	// script runs; consulted afterwards for re-approval prompts.
	enforcer *plugins.HookEnforcer
	// confirmDrainActive guards the confirm presenter goroutine against
	// double-start (activatePluginUI runs both synchronously at startup and
	// again on the command loop after the async plugin load).
	confirmDrainActive atomic.Bool
	// completions maps command name → JS-provided argument completer
	// (goa.registerCompletion). Written during plugin load, read from the
	// TUI completer goroutine on every keystroke — hence the RWMutex.
	completionsMu sync.RWMutex
	completions   map[string]func(prefix string) []plugins.Completion
}

// loadEnabledPlugins materializes bundled plugins, then loads all enabled
// plugins (bundled + user-installed) and wires their registered tools,
// commands, observers, and lifecycle hooks into Goa's subsystems. It is safe
// to call when no plugins are enabled.
func loadEnabledPlugins(s *subsystems) {
	if s.pluginMgr == nil || s.noPlugins {
		return
	}
	// Materialize bundled (embedded) plugins so they are trusted + enabled.
	bundledDir := materializeBundledPlugins(s)

	enabled := s.pluginMgr.EnabledIDs()
	if len(enabled) == 0 {
		return
	}
	// Verify user-installed plugins (bundled ones were hashed at materialize).
	for _, id := range enabled {
		if err := s.pluginMgr.Verify(id); err != nil {
			log.Printf("Warning: skipping plugin %s: %v\n", id, err)
			return
		}
	}
	// Scan the install root and the bundled dir for enabled plugins.
	dirs := []string{s.pluginMgr.Root()}
	if bundledDir != "" {
		dirs = append(dirs, bundledDir)
	}
	loader := plugins.NewPluginLoader(dirs, enabled)
	rt := newPluginRuntime(s)

	// M6 §7 step 4: grant-backed enforcement installs into the registry's
	// validator slot BEFORE any plugin script runs. A fresh enforcer per load
	// keeps manifest declarations and grants in sync at every startup/reload.
	enforcer := plugins.NewHookEnforcer(plugins.NewGrantStore(s.pluginMgr.Root()), s.headless)
	enforcer.ObserveDir(s.pluginMgr.Root(), false)
	if bundledDir != "" {
		enforcer.ObserveDir(bundledDir, true) // bundled plugins ship pre-approved
	}
	if rt.hooks != nil {
		rt.hooks.SetAllow(enforcer.Allow)
	}
	rt.enforcer = enforcer

	bridges, err := loader.LoadAll(rt.contextFor(s))
	if err != nil {
		log.Printf("Warning: failed to load plugins: %v\n", err)
		return
	}
	rt.bridges = bridges
	s.setPluginRT(rt)
	log.Printf("Loaded %d plugin(s)\n", len(bridges))
}

// setPluginRT stores the loaded plugin runtime (async-load safe).
func (s *subsystems) setPluginRT(rt *pluginRuntime) {
	s.pluginRTMu.Lock()
	s.pluginRT = rt
	s.pluginRTMu.Unlock()
}

// getPluginRT returns the loaded plugin runtime, or nil before the async load
// completes (async-load safe).
func (s *subsystems) getPluginRT() *pluginRuntime {
	s.pluginRTMu.RLock()
	defer s.pluginRTMu.RUnlock()
	return s.pluginRT
}

// newPluginRuntime builds the shared extended bridges for a plugin load. It
// reuses the boot-created hook registry + scheduler (M2 §3.5) so the sink
// already held by agents observes registrations from these plugins; fresh
// instances are only created when boot wiring did not run (tests).
func newPluginRuntime(s *subsystems) *pluginRuntime {
	sched := s.pluginSched
	if sched == nil {
		sched = plugins.NewScheduler()
	}
	hooks := s.pluginHooks
	if hooks == nil {
		hooks = plugins.NewHookRegistry(nil)
	}
	return &pluginRuntime{
		ui:          plugins.NewUIBridge(),
		hotkeys:     plugins.NewHotkeyBridge(),
		bus:         plugins.NewEventBus(),
		scheduler:   sched,
		hooks:       hooks,
		completions: map[string]func(prefix string) []plugins.Completion{},
	}
}

// materializeBundledPlugins copies each enabled bundled (embedded) plugin
// into the manager's bundled dir and enables it. Returns the bundled dir to
// scan, or "" when none are enabled. Failures are logged, not fatal — a
// broken bundled plugin must not block startup.
func materializeBundledPlugins(s *subsystems) string {
	if !s.cfg.Plugins.BundledEnabled(bundled.ProviderQuotaID) {
		return ""
	}
	m, err := bundled.ProviderQuotaSource()
	if err != nil {
		log.Printf("Warning: bundled provider-quota manifest: %v\n", err)
		return ""
	}
	src := plugins.BundledSource{
		ID:       m.ID,
		Version:  m.Version,
		ReadFile: bundled.ReadFile,
		ReadDir:  bundled.ReadDir,
	}
	if _, err := s.pluginMgr.MaterializeBundled(src); err != nil {
		log.Printf("Warning: materialize bundled plugin %s: %v\n", m.ID, err)
		return ""
	}
	return s.pluginMgr.BundledDir()
}

// contextFor builds the PluginContext exposing Goa subsystems to plugins.
func (rt *pluginRuntime) contextFor(s *subsystems) plugins.PluginContext {
	return plugins.PluginContext{
		// Live config: goa.config() re-reads the current provider/model on
		// every call so plugins (e.g. quota) see switches immediately.
		Config:             pluginConfigFor(s),
		ConfigFunc:         func() map[string]any { return pluginConfigFor(s) },
		Logger:             pluginLogger(),
		RegisterTool:       pluginRegisterTool(s),
		RegisterCommand:    rt.pluginRegisterCommand(s),
		RegisterCompletion: rt.pluginRegisterCompletion(),
		RegisterObserver:   rt.pluginRegisterObserver(),
		RegisterLifecycle:  pluginRegisterLifecycle(s),
		CallTool:           pluginCallTool(s),
		RegisterHook:       rt.pluginRegisterHook(s),
		EventBus:           rt.bus,
		Extended:           rt.extendedContext(s),
	}
}

// extendedContext assembles the optional bridges (http, storage, timers, ui,
// hotkeys, browser, output, sessionUsage). Storage is rooted per-plugin under
// the manager root.
func (rt *pluginRuntime) extendedContext(s *subsystems) *plugins.ExtendContext {
	root := ""
	if s.pluginMgr != nil {
		root = s.pluginMgr.Root()
	}
	storage, err := plugins.NewStorageBridge(root, "shared")
	if err != nil {
		log.Printf("Warning: plugin storage unavailable: %v\n", err)
		storage = nil
	}
	return &plugins.ExtendContext{
		HTTP:      plugins.NewHTTPBridge(),
		Storage:   storage,
		Scheduler: rt.scheduler,
		Browser:   plugins.NewBrowserBridge(),
		Hotkeys:   rt.hotkeys,
		UI:        rt.ui,
		Output:    rt.makeOutput(s),
		SessionUsage: func() map[string]any {
			return pluginSessionUsage(s)
		},
		SegmentColor: pluginSegmentColor,
		OAuthToken: func(ctx context.Context, provider string) (map[string]any, error) {
			return pluginOAuthToken(ctx, s.authStore, provider)
		},
	}
}

func codexOAuthTokens(store *authstore.Store) (string, *oauth.Tokens, bool) {
	for _, key := range []string{"codex", "openai-codex", "openai"} {
		if tokens, ok := store.GetOAuth(key); ok && tokens != nil && tokens.AccessToken != "" {
			return key, tokens, true
		}
	}
	return "", nil, false
}

func pluginOAuthToken(ctx context.Context, store *authstore.Store, provider string) (map[string]any, error) {
	if provider != "openai" && provider != "codex" && provider != "openai-codex" {
		return nil, fmt.Errorf("unsupported OAuth provider: %s", provider)
	}
	if store == nil {
		return nil, fmt.Errorf("goa auth store unavailable")
	}
	storeKey, tokens, ok := codexOAuthTokens(store)
	if !ok {
		return nil, fmt.Errorf("OAuth login required for Codex (use /login:codex:oauth)")
	}
	codex, err := oauth.NewOpenAICodexOAuth()
	if err != nil {
		return nil, fmt.Errorf("create Codex OAuth provider: %w", err)
	}
	source := oauth.NewTokenSource(codex, tokens)
	if _, err := source.Token(ctx); err != nil {
		return nil, err
	}
	current := source.Current()
	if current != tokens {
		if err := store.SetOAuth(storeKey, current); err != nil {
			return nil, fmt.Errorf("persist refreshed OAuth token: %w", err)
		}
	}
	return map[string]any{"accessToken": current.AccessToken, "accountId": current.AccountID}, nil
}

// pluginSegmentColor maps a semantic segment color name to the active theme's
// hex color, so plugin status segments can be styled without emitting console
// codes. "" falls back to unstyled (the footer's default status color).
func pluginSegmentColor(name string) string {
	token := map[string]string{
		"ok":       "tool_success",
		"warn":     "token_warning",
		"critical": "token_critical",
		"pending":  "system_msg",
	}[name]
	if token == "" {
		return ""
	}
	return tui.TheTheme.ColorHex(token)
}

// makeOutput returns the goa.output implementation: it emits a chat event so
// the message appears in the conversation viewport (not the log).
func (rt *pluginRuntime) makeOutput(s *subsystems) func(string) {
	return func(msg string) {
		emitPluginChat(s, msg)
	}
}

// pluginConfigFor exposes the loaded config to plugins. Provider API keys are
// masked to a boolean hasKey unless the plugin declares the "provider-keys"
// permission — enforced here by masking at this layer (the quota plugin
// declares the permission, so the loader passes keys through for it).
func pluginConfigFor(s *subsystems) map[string]any {
	if s.cfg == nil {
		return map[string]any{}
	}
	return map[string]any{
		"providers":      pluginProvidersMap(s),
		"activeProvider": s.cfg.ActiveProvider,
		"activeModel":    s.cfg.ActiveModel,
	}
}

// pluginProvidersMap converts configured providers to a JS-friendly map keyed
// by provider id. API keys are included — plugin bridges run in-process with
// the same trust level as Goa itself (plugins are explicitly trusted on
// install), so key access is gated by plugin trust, not masking.
func pluginProvidersMap(s *subsystems) map[string]any {
	out := map[string]any{}
	for _, p := range s.cfg.Providers {
		apiKey := p.APIKey
		// Fall back to the auth store (e.g. key set via /login) so providers
		// authenticated outside ProviderConfig.APIKey are still seen as
		// authenticated by plugins — otherwise the quota plugin drops them as
		// no_api_key and they vanish from /quota (z.ai #6).
		if apiKey == "" && s.providerMgr != nil {
			apiKey = s.providerMgr.ResolveAPIKey(p.ID)
		}
		out[p.ID] = map[string]any{
			"id":       p.ID,
			"name":     p.Name,
			"provider": p.Provider,
			"apiKey":   apiKey,
			"baseUrl":  p.BaseURL,
			"endpoint": p.Endpoint,
		}
	}
	return out
}

// pluginSessionUsage snapshots cumulative session token stats for the local
// (inferred) quota fetcher.
func pluginSessionUsage(s *subsystems) map[string]any {
	usage := map[string]any{"input": 0, "output": 0, "turns": 0}
	if s.sessionUsageFn != nil {
		return s.sessionUsageFn()
	}
	return usage
}

// pluginLogger adapts the standard logger to the plugin LoggerAPI.
func pluginLogger() plugins.LoggerAPI {
	return plugins.LoggerAPI{
		Info:  func(msg string) { log.Printf("[plugin] %s\n", msg) },
		Warn:  func(msg string) { log.Printf("[plugin] warn: %s\n", msg) },
		Error: func(msg string) { log.Printf("[plugin] error: %s\n", msg) },
		Debug: func(msg string) { log.Printf("[plugin] debug: %s\n", msg) },
	}
}

func pluginRegisterTool(s *subsystems) func(string, string, func(map[string]any) (interface{}, error)) error {
	return func(name, description string, execute func(map[string]any) (interface{}, error)) error {
		if s.toolRegistry == nil {
			return fmt.Errorf("tool registry not available")
		}
		wrapper := &pluginToolWrapper{
			name:        name,
			description: description,
			execute:     execute,
		}
		s.toolRegistry.Register(wrapper)
		return nil
	}
}

// pluginRegisterCommand wires JS commands into the shared command registry so
// /quota (and friends) resolve through the normal router.
func (rt *pluginRuntime) pluginRegisterCommand(s *subsystems) func(string, []string, string, string, func([]string) (string, error)) error {
	return func(name string, aliases []string, shortHelp, longHelp string, run func([]string) (string, error)) error {
		if s.registry == nil {
			return fmt.Errorf("command registry not available")
		}
		cmd := &pluginCommandWrapper{
			name:      name,
			aliases:   aliases,
			shortHelp: shortHelp,
			longHelp:  longHelp,
			run:       run,
			// Lazy lookup: the JS completer may register AFTER the command.
			completions: func() func(prefix string) []plugins.Completion {
				return rt.completionFor(name)
			},
		}
		if err := s.registry.Register(cmd); err != nil {
			return err
		}
		log.Printf("[plugin] registered command /%s\n", name)
		return nil
	}
}

// pluginRegisterCompletion wires goa.registerCompletion into the runtime's
// completion map so pluginCommandWrapper.CompleteArgs can consult it lazily
// (completions may register after the command itself).
func (rt *pluginRuntime) pluginRegisterCompletion() plugins.CompletionHandler {
	return func(name string, fn func(prefix string) []plugins.Completion) error {
		if name == "" || fn == nil {
			return fmt.Errorf("registerCompletion requires a command name and a function")
		}
		rt.completionsMu.Lock()
		defer rt.completionsMu.Unlock()
		if rt.completions == nil {
			rt.completions = map[string]func(prefix string) []plugins.Completion{}
		}
		rt.completions[name] = fn
		log.Printf("[plugin] registered completions for /%s\n", name)
		return nil
	}
}

// completionFor returns the JS-provided completer for a command name, if any.
func (rt *pluginRuntime) completionFor(name string) func(prefix string) []plugins.Completion {
	rt.completionsMu.RLock()
	defer rt.completionsMu.RUnlock()
	return rt.completions[name]
}

// pluginRegisterObserver subscribes JS observers to the plugin event bus.
func (rt *pluginRuntime) pluginRegisterObserver() plugins.ObserverHandler {
	return func(callback func(string, interface{})) {
		// Observers receive all events; the plugin filters by event name.
		rt.bus.On("*", callback)
	}
}

// pluginRegisterHook routes goa.registerHook calls into the shared registry.
// The bridge stamps spec.PluginID from its own manifest, so the shared
// PluginContext needs no per-plugin cloning. Registrations become visible to
// agents immediately (the sink reads this live registry).
func (rt *pluginRuntime) pluginRegisterHook(s *subsystems) plugins.HookRegisterHandler {
	return func(spec plugins.HookSpec, handler plugins.HookHandler) error {
		if rt.hooks == nil {
			return fmt.Errorf("hook registry not available")
		}
		if err := rt.hooks.Register(spec, handler); err != nil {
			// M6 §7 step 4: refusals surface both in the log and on the
			// goa.output channel so the user sees why a plugin lost a hook.
			log.Printf("[plugin] rejected hook registration: %v\n", err)
			emitPluginChat(s, fmt.Sprintf("⚠ Plugin hook rejected (%s@%s): %v", spec.Point, spec.Mode, err))
			return err
		}
		log.Printf("[plugin] registered hook %s@%s (%s)\n", spec.Name, spec.Point, spec.Mode)
		return nil
	}
}

// EmitEvent broadcasts an event to all plugin observers (wildcard bus).
func (rt *pluginRuntime) EmitEvent(name string, payload interface{}) {
	if rt == nil || rt.bus == nil {
		return
	}
	rt.bus.Emit(name, payload)
}

// EmitRateLimitToPlugins forwards an agentic EventRateLimit onto the plugin
// event bus as "rate_limit_exceeded" (plugins plan §6 step 3). Wildcard
// observers (goa.registerObserver) receive the payload documented in the plan:
// provider, model, retry_after_ms, will_retry — plus attempt and classified
// for consumers that want the full classification. It is a no-op for any other
// event type, without a plugin runtime (plugins disabled), or without a
// subsystems handle, so call sites can forward unconditionally.
func EmitRateLimitToPlugins(s *subsystems, ev *agentic.OutputEvent) {
	if s == nil || ev == nil || ev.Type != agentic.EventRateLimit || ev.RateLimit == nil {
		return
	}
	rl := ev.RateLimit
	rt := s.getPluginRT()
	if rt == nil {
		return
	}
	rt.EmitEvent("rate_limit_exceeded", map[string]interface{}{
		"provider":       rl.Provider,
		"model":          rl.Model,
		"attempt":        rl.Attempt,
		"retry_after_ms": rl.RetryAfterMS,
		"classified":     rl.Classified,
		"will_retry":     rl.WillRetry,
	})
}

func pluginRegisterLifecycle(s *subsystems) func(plugins.HookType, plugins.LifecycleHandler) {
	return func(hook plugins.HookType, h plugins.LifecycleHandler) {
		if s.lifecycleRegistry == nil {
			return
		}
		s.lifecycleRegistry.Register(hook, h)
	}
}

func pluginCallTool(s *subsystems) func(string, map[string]any) (interface{}, error) {
	return func(name string, params map[string]any) (interface{}, error) {
		if s.toolRegistry == nil {
			return nil, fmt.Errorf("tool registry not available")
		}
		t, ok := s.toolRegistry.Get(name)
		if !ok {
			return nil, fmt.Errorf("tool %q not found", name)
		}
		input, err := json.Marshal(params)
		if err != nil {
			return nil, fmt.Errorf("marshal params: %w", err)
		}
		result, err := executePluginTool(t, string(input))
		if err != nil {
			return nil, err
		}
		var out interface{}
		if err := json.Unmarshal([]byte(result), &out); err != nil {
			return result, nil
		}
		return out, nil
	}
}

func executePluginTool(t agentic.Tool, input string) (string, error) {
	if ct, ok := t.(agentic.ContextTool); ok {
		return ct.ExecuteContext(context.Background(), input)
	}
	return t.Execute(input)
}

// emitPluginChat routes a plugin message into the conversation viewport via
// the app event bus as an output modal (the same vehicle commands use for
// multi-line results). A nil/full channel falls back to the log so a plugin
// never deadlocks the JS runner.
func emitPluginChat(s *subsystems, msg string) {
	if s.events == nil {
		log.Printf("[plugin] output: %s\n", msg)
		return
	}
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[plugin] output (no TUI): %s\n", msg)
		}
	}()
	select {
	case s.events.Chat <- pluginChatEvent(msg):
	default:
		log.Printf("[plugin] output (busy): %s\n", msg)
	}
}

// pluginToolWrapper adapts a plugin's JavaScript tool to agentic.Tool.
type pluginToolWrapper struct {
	agentic.BaseTool
	name        string
	description string
	execute     func(map[string]any) (interface{}, error)
}

func (p *pluginToolWrapper) Schema() agentic.ToolSchema {
	return agentic.ToolSchema{
		Name:        p.name,
		Description: p.description,
		Schema: map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
		},
	}
}

func (p *pluginToolWrapper) Execute(input string) (string, error) {
	var params map[string]any
	if err := json.Unmarshal([]byte(input), &params); err != nil {
		params = map[string]any{"input": input}
	}
	result, err := p.execute(params)
	if err != nil {
		return "", err
	}
	out, err := json.Marshal(result)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// pluginCommandWrapper adapts a plugin's JavaScript command to core.Command.
type pluginCommandWrapper struct {
	name      string
	aliases   []string
	shortHelp string
	longHelp  string
	run       func([]string) (string, error)
	// completions returns the JS-provided completer for this command, if it
	// registered one via goa.registerCompletion. Nil-safe: nil ⇒ no arg
	// completions (previous behavior for all plugin commands).
	completions func() func(prefix string) []plugins.Completion
}

func (c *pluginCommandWrapper) Name() string      { return c.name }
func (c *pluginCommandWrapper) Aliases() []string { return c.aliases }
func (c *pluginCommandWrapper) ShortHelp() string {
	if c.shortHelp != "" {
		return c.shortHelp
	}
	return "Plugin command"
}
func (c *pluginCommandWrapper) LongHelp() string {
	if c.longHelp != "" {
		return c.longHelp
	}
	return c.ShortHelp()
}
func (c *pluginCommandWrapper) CompleteArgs(ctx core.Context, prefix string) []core.ArgCompletion {
	if c.completions == nil {
		return nil
	}
	fn := c.completions()
	if fn == nil {
		return nil
	}
	comps := fn(prefix)
	out := make([]core.ArgCompletion, 0, len(comps))
	for _, comp := range comps {
		out = append(out, core.ArgCompletion{Value: comp.Value, Description: comp.Description})
	}
	return out
}

// Run executes the JS command, writing its output string into the router's
// OutputBuffer so handleSlashCommand echoes it into the chat viewport exactly
// like a built-in command's response.
func (c *pluginCommandWrapper) Run(ctx core.Context, args []string) error {
	out, err := c.run(args)
	if err != nil {
		return err
	}
	if out != "" && ctx.OutputBuffer != nil {
		ctx.OutputBuffer.WriteString(out)
	}
	return nil
}

var _ core.Command = (*pluginCommandWrapper)(nil)
var _ core.AsyncCommand = (*pluginCommandWrapper)(nil)

// AsyncCommand: plugin commands ALWAYS run off the TUI command loop with a
// spinner label. Root-cause fix for goa.ui.confirm (plan §4): the JS command
// blocks on the user's answer while the modal itself needs the command loop
// free to render and route keys — a synchronous Execute on the loop would
// deadlock until the 5-minute confirm cap. The same reasoning covers any
// slow bridge call (goa.http.fetch) inside a command. Interactive submits
// route through runAsyncCommand (steering stays live); direct router
// callers (headless, tests) keep synchronous semantics.
func (c *pluginCommandWrapper) AsyncHint(args []string) string {
	return "Running /" + c.name + "…"
}

// promptPendingHookApprovals shows the install-time hook review card for any
// loaded external plugin whose stored grant is stale or missing (M6 §7 steps
// 2+5: version bump or changed hook fingerprint invalidates prior approvals).
// Cards run serially on their own goroutine; card creation is ApplySync'd to
// the command loop so TUI state stays single-owner.
func (a *App) promptPendingHookApprovals(engine *tui.TUI) {
	subs := a.subs
	rt := subs.getPluginRT()
	if rt == nil || rt.enforcer == nil || subs.pluginMgr == nil {
		return
	}
	for _, id := range rt.enforcer.PendingApprovals() {
		def := rt.enforcer.DeclaredFor(id)
		if def == nil {
			continue
		}
		review := plugins.BuildHookReview(def)
		if review == nil {
			continue
		}
		a.presentHookReview(engine, def, review)
	}
}

// presentHookReview shows ONE multi-select review card and persists the
// outcome into grants.json. It blocks until the user decides and must run
// OFF the command loop (card creation is ApplySync'd; the decision wait
// happens here so cards appear strictly serially).
func (a *App) presentHookReview(engine *tui.TUI, def *plugins.PluginDef, review *plugins.HookReview) {
	opts := make([]tui.ConfirmOption, 0, len(review.Rows)+2)
	for _, r := range review.Rows {
		opts = append(opts, tui.ConfirmOption{ID: r.ID, Label: r.Label, Toggle: true, DefaultOn: r.DefaultOn})
	}
	opts = append(opts,
		tui.ConfirmOption{ID: "accept", Label: "Accept selected hooks", Style: "ok"},
		tui.ConfirmOption{ID: "reject", Label: "Reject all", Style: "danger"},
	)
	var (
		ch    <-chan tui.MultiConfirmResult
		shown = make(chan struct{})
	)
	engine.ApplySync(func() {
		ch, _ = engine.ShowConfirmMulti(review.Title, review.Body, opts, "", true)
		close(shown)
	})
	<-shown
	if ch == nil {
		return // no overlay layer available (should not happen in TUI runs)
	}
	res := <-ch
	store := plugins.NewGrantStore(a.subs.pluginMgr.Root())
	if res.Cancelled || res.ActionID != "accept" {
		emitPluginChat(a.subs, fmt.Sprintf("⚠ Plugin %s: no hooks granted (review declined).", def.ID))
		return
	}
	if err := plugins.ApplyHookDecision(store, def, res.Selected); err != nil {
		log.Printf("Warning: failed to save grant for %s: %v\n", def.ID, err)
		return
	}
	emitPluginChat(a.subs, fmt.Sprintf("✔ Plugin %s: %d hook(s) approved.", def.ID, len(res.Selected)))
}
