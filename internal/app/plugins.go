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
	"github.com/pijalu/goa/plugins"
)

// loadEnabledPlugins loads plugins that are enabled by the plugin manager and
// wires their registered tools, commands, observers, and lifecycle hooks into
// Goa's subsystems. It is safe to call when no plugins are enabled.
func loadEnabledPlugins(s *subsystems) {
	if s.pluginMgr == nil {
		return
	}
	enabled := s.pluginMgr.EnabledIDs()
	if len(enabled) == 0 {
		return
	}
	for _, id := range enabled {
		if err := s.pluginMgr.Verify(id); err != nil {
			log.Printf("Warning: skipping plugin %s: %v\n", id, err)
			return
		}
	}
	loader := plugins.NewPluginLoader([]string{s.pluginMgr.Root()}, enabled)
	ctx := pluginContextFor(s)
	bridges, err := loader.LoadAll(ctx)
	if err != nil {
		log.Printf("Warning: failed to load plugins: %v\n", err)
		return
	}
	log.Printf("Loaded %d plugin(s)\n", len(bridges))
}

// pluginContextFor builds a PluginContext that exposes the minimum Goa surface
// to loaded plugins: tool registration, lifecycle hooks, and tool invocation.
// Command and observer registration are stubbed to avoid cross-package wiring
// complexity for this first pass; they can be promoted once the plugin model
// matures.
func pluginContextFor(s *subsystems) plugins.PluginContext {
	return plugins.PluginContext{
		Config:          map[string]any{},
		Logger:          pluginLogger(),
		RegisterTool:    pluginRegisterTool(s),
		RegisterCommand: pluginRegisterCommand(),
		RegisterObserver: func(func(string, interface{})) {
			// Observers receive all events as raw payloads; the plugin is
			// responsible for filtering by event name.
		},
		RegisterLifecycle: pluginRegisterLifecycle(s),
		CallTool:          pluginCallTool(s),
		EventBus:          nil,
	}
}

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

func pluginRegisterCommand() func(string, []string, string, string, func([]string) (string, error)) error {
	return func(string, []string, string, string, func([]string) (string, error)) error {
		// Command registration from plugins is not yet wired to avoid
		// lifecycle conflicts with the command router. Plugins can still
		// expose commands via the goa_command tool or as skills.
		return nil
	}
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
// Not used until plugin command registration is fully wired.
type pluginCommandWrapper struct {
	name    string
	aliases []string
	run     func([]string) (string, error)
}

func (c *pluginCommandWrapper) Name() string { return c.name }
func (c *pluginCommandWrapper) Aliases() []string { return c.aliases }
func (c *pluginCommandWrapper) ShortHelp() string { return "" }
func (c *pluginCommandWrapper) LongHelp() string { return "" }
func (c *pluginCommandWrapper) CompleteArgs(ctx core.Context, prefix string) []core.ArgCompletion { return nil }
func (c *pluginCommandWrapper) Run(ctx core.Context, args []string) error {
	_, err := c.run(args)
	return err
}

var _ core.Command = (*pluginCommandWrapper)(nil)
