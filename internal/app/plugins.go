// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/internal/agentic"
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

// newPluginRuntime builds the shared extended bridges for a plugin load.
func newPluginRuntime(s *subsystems) *pluginRuntime {
	return &pluginRuntime{
		ui:        plugins.NewUIBridge(),
		hotkeys:   plugins.NewHotkeyBridge(),
		bus:       plugins.NewEventBus(),
		scheduler: plugins.NewScheduler(),
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
		Config:            pluginConfigFor(s),
		ConfigFunc:        func() map[string]any { return pluginConfigFor(s) },
		Logger:            pluginLogger(),
		RegisterTool:      pluginRegisterTool(s),
		RegisterCommand:   rt.pluginRegisterCommand(s),
		RegisterObserver:  rt.pluginRegisterObserver(),
		RegisterLifecycle: pluginRegisterLifecycle(s),
		CallTool:          pluginCallTool(s),
		EventBus:          rt.bus,
		Extended:          rt.extendedContext(s),
	}
}

// extendedContext assembles the optional bridges (http, storage, timers, ui,
// hotkeys, browser, output, sessionUsage). Storage is rooted per-plugin under
// the manager root; the loader swaps in the per-plugin id at RunFile time, so
// a single shared StorageBridge rooted at the plugins dir suffices (each
// plugin namespaced by its own id directory).
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
	}
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
		out[p.ID] = map[string]any{
			"id":       p.ID,
			"name":     p.Name,
			"provider": p.Provider,
			"apiKey":   p.APIKey,
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
		}
		if err := s.registry.Register(cmd); err != nil {
			return err
		}
		log.Printf("[plugin] registered command /%s\n", name)
		return nil
	}
}

// pluginRegisterObserver subscribes JS observers to the plugin event bus.
func (rt *pluginRuntime) pluginRegisterObserver() plugins.ObserverHandler {
	return func(callback func(string, interface{})) {
		// Observers receive all events; the plugin filters by event name.
		rt.bus.On("*", callback)
	}
}

// EmitEvent broadcasts an event to all plugin observers (wildcard bus).
func (rt *pluginRuntime) EmitEvent(name string, payload interface{}) {
	if rt == nil || rt.bus == nil {
		return
	}
	rt.bus.Emit(name, payload)
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
	return nil
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
