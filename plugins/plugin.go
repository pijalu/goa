// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

// Package plugins implements the JS plugin system using Goja (Go JavaScript
// engine). Plugins are defined by a plugin.yaml manifest and a plugin.js
// entry point. They can register tools, commands, event observers, and
// skills.
package plugins

import (
	"fmt"
	"strconv"
	"sync"

	"github.com/dop251/goja"
)

// PluginHookDecl is one declared agent hook in a plugin manifest (plugins
// plan §7 step 1). The declaration list is the approval contract: the user
// reviews exactly these (point, mode) rows at install time, and M6's
// enforcement layer rejects any goa.registerHook call not covered by both the
// declaration and a stored grant.
type PluginHookDecl struct {
	Point       string `yaml:"point"`                 // wire id — see ValidHookPoints
	Mode        string `yaml:"mode"`                  // notify | intercept
	Description string `yaml:"description,omitempty"` // shown on the review card
}

// PluginDef describes a plugin's manifest.
type PluginDef struct {
	ID            string           `yaml:"id"`
	Name          string           `yaml:"name"`
	Version       string           `yaml:"version"`
	Entry         string           `yaml:"entry"`
	Description   string           `yaml:"description"`
	GoaMinVersion string           `yaml:"goa_min_version"`
	SkillsDir     string           `yaml:"skills_dir,omitempty"`
	Permissions   []string         `yaml:"permissions,omitempty"`
	Hooks         []PluginHookDecl `yaml:"hooks,omitempty"`
}

// ToolHandler is called when a JS plugin registers a tool via goa.registerTool.
type ToolHandler func(name, description string, execute func(map[string]any) (interface{}, error)) error

// CommandHandler is called when a JS plugin registers a command via goa.registerCommand.
type CommandHandler func(name string, aliases []string, shortHelp, longHelp string, run func([]string) (string, error)) error

// Completion is one argument-completion candidate a plugin offers for its
// command: Value is the segment inserted after "/<cmd>:" (WITHOUT the command
// prefix — the TUI engine prepends it), Description is shown in the picker.
type Completion struct {
	Value       string
	Description string
}

// CompletionHandler is called when JS calls goa.registerCompletion(name, fn).
// fn(prefix) receives the raw arg prefix — everything the user typed after
// "/<cmd>:" (e.g. "", "re", "login:op") — and returns candidates. Nested
// levels re-invoke fn with the parent path plus colon ("login:").
type CompletionHandler func(name string, fn func(prefix string) []Completion) error

// ObserverHandler receives a callback that will be called for every event.
// The callback receives (eventName string, payload interface{}).
type ObserverHandler func(callback func(string, interface{}))

// HookRegisterHandler is invoked when a JS plugin calls goa.registerHook.
// The spec's PluginID is stamped by the bridge from its own manifest, so the
// shared PluginContext needs no per-plugin cloning.
type HookRegisterHandler func(spec HookSpec, handler HookHandler) error

// CallToolHandler is called when a JS plugin invokes goa.callTool(name, params).
type CallToolHandler func(name string, params map[string]any) (interface{}, error)

// PluginContext provides the JS plugin with access to Goa subsystems.
type PluginContext struct {
	Config map[string]any
	// ConfigFunc, when set, is called on every goa.config() invocation so the
	// plugin always sees the LIVE config (e.g. after a provider/model switch).
	// It takes precedence over the static Config snapshot, which is kept for
	// tests and simple plugins.
	ConfigFunc      func() map[string]any
	Logger          LoggerAPI
	RegisterTool    ToolHandler    // called when JS calls goa.registerTool
	RegisterCommand CommandHandler // called when JS calls goa.registerCommand
	// RegisterCompletion is called when JS calls goa.registerCompletion(name,
	// fn) to provide argument completions for a command. Nil disables the API
	// (older hosts / minimal harnesses); plugins must degrade gracefully.
	RegisterCompletion CompletionHandler
	RegisterObserver   ObserverHandler                         // called when JS calls goa.registerObserver
	RegisterLifecycle  func(hook HookType, h LifecycleHandler) // called when JS calls goa.registerLifecycle
	CallTool           CallToolHandler                         // called when JS calls goa.callTool
	// RegisterHook is called when JS calls goa.registerHook. Nil disables the
	// API (plugins receive the standard "not configured" error string).
	RegisterHook HookRegisterHandler
	EventBus     *EventBus
	// Extended carries optional bridges (http, storage, timers, ui, hotkeys,
	// browser, output, sessionUsage). Nil disables those goa.* APIs.
	Extended *ExtendContext
}

// LoggerAPI exposes logging functions to JS plugins.
type LoggerAPI struct {
	Info  func(msg string)
	Warn  func(msg string)
	Error func(msg string)
	Debug func(msg string)
}

// JSBridge manages the Goja runtime for a single plugin, exposing
// goa.* globals to JavaScript code.
type JSBridge struct {
	vm  *goja.Runtime
	ctx PluginContext
	def PluginDef
}

// hasPermission reports whether the bridge's manifest declares the named
// permission (M6 §7 capability gating). Unknown permission names never reach
// this check — validatePluginDef rejects them at discovery.
func (b *JSBridge) hasPermission(perm string) bool {
	for _, p := range b.def.Permissions {
		if p == perm {
			return true
		}
	}
	return false
}

// vmMu serializes every JavaScript execution across all plugins. Goja
// runtimes are not goroutine-safe, and plugins have asynchronous entry points
// (timers, hotkeys, HTTP completions, command/tool invocations) arriving from
// many goroutines. The mutex guarantees goja's single-goroutine rule; bridge
// callbacks that perform blocking work OUTSIDE the runtime (e.g. the HTTP
// round-trip in goa.http.fetch) must release it via runOutsideVMLock so a
// slow endpoint cannot starve the other entry points — vmMu is not
// reentrant, so holding it across a blocking call freezes the whole VM.
var vmMu sync.Mutex

// vmActive counts in-flight JS executions (re-entrant across the HTTP hop:
// runOutsideVMLock does NOT decrement it). Scheduler timers use tryEnterVM
// to detect that a synchronous command/tool is mid-execution and defer their
// best-effort work instead of interleaving a second goja frame — the race
// that made TestPluginCommandExecutesThroughRouter flaky (item E).
var vmActive int
var vmActiveMu sync.Mutex

// lockVM acquires the global JS execution lock. All VM interactions must go
// through this so no two goroutines ever touch a runtime concurrently.
func lockVM() func() {
	vmMu.Lock()
	return vmMu.Unlock
}

// enterVM marks a JS execution as active for the whole logical call (it is
// NOT released by runOutsideVMLock). Returns the leave func.
func enterVM() func() {
	vmActiveMu.Lock()
	vmActive++
	vmActiveMu.Unlock()
	return func() {
		vmActiveMu.Lock()
		vmActive--
		vmActiveMu.Unlock()
	}
}

// vmBusy reports whether any JS execution is currently active.
func vmBusy() bool {
	vmActiveMu.Lock()
	defer vmActiveMu.Unlock()
	return vmActive > 0
}

// NewJSBridge creates a new JS bridge for the given plugin definition.
func NewJSBridge(def PluginDef, ctx PluginContext) *JSBridge {
	vm := goja.New()
	bridge := &JSBridge{
		vm:  vm,
		ctx: ctx,
		def: def,
	}
	bridge.setupGlobals()
	return bridge
}

// setupGlobals registers goa.* APIs in the JS runtime.
func (b *JSBridge) setupGlobals() {
	goaObj := b.vm.NewObject()

	// goa.config() — returns config as JS object. Prefer the live ConfigFunc
	// (so provider/model switches are visible) over the static snapshot.
	goaObj.Set("config", func() map[string]any {
		if b.ctx.ConfigFunc != nil {
			return b.ctx.ConfigFunc()
		}
		return b.ctx.Config
	})

	// goa.logger() — logging interface
	goaObj.Set("logger", func() map[string]any {
		return map[string]any{
			"info":  b.ctx.Logger.Info,
			"warn":  b.ctx.Logger.Warn,
			"error": b.ctx.Logger.Error,
			"debug": b.ctx.Logger.Debug,
		}
	})

	goaObj.Set("registerTool", b.wrapRegisterTool())
	goaObj.Set("registerCommand", b.wrapRegisterCommand())
	if b.ctx.RegisterCompletion != nil {
		goaObj.Set("registerCompletion", b.wrapRegisterCompletion())
	}
	goaObj.Set("registerObserver", b.wrapRegisterObserver())
	goaObj.Set("registerLifecycle", b.wrapRegisterLifecycle())
	goaObj.Set("callTool", b.wrapCallTool())
	b.setupHooks(goaObj)

	b.setupExtendedGlobals(goaObj)

	b.vm.Set("goa", goaObj)
}

// wrapRegisterTool returns a JS-callable function that implements goa.registerTool.
func (b *JSBridge) wrapRegisterTool() func(goja.FunctionCall) goja.Value {
	return func(call goja.FunctionCall) goja.Value {
		if b.ctx.RegisterTool == nil {
			return b.vm.ToValue("error: ToolHandler not configured")
		}
		obj := call.Argument(0).ToObject(b.vm)
		name := obj.Get("name").String()
		desc := obj.Get("description").String()
		executeFn := obj.Get("execute").Export()

		wrapper, err := b.buildToolWrapper(executeFn)
		if err != nil {
			return b.vm.ToValue("error: " + err.Error())
		}
		if err := b.ctx.RegisterTool(name, desc, wrapper); err != nil {
			return b.vm.ToValue("error: " + err.Error())
		}
		return b.vm.ToValue("tool registered: " + name)
	}
}

// buildToolWrapper converts a JS execute function into a Go-compatible wrapper.
func (b *JSBridge) buildToolWrapper(executeFn interface{}) (func(map[string]any) (interface{}, error), error) {
	switch fn := executeFn.(type) {
	case func(goja.FunctionCall) goja.Value:
		return func(params map[string]any) (interface{}, error) {
			leave := enterVM()
			defer leave()
			unlock := lockVM()
			defer unlock()
			jsParams := b.vm.NewObject()
			for k, v := range params {
				jsParams.Set(k, v)
			}
			call := goja.FunctionCall{}
			call.Arguments = append(call.Arguments, jsParams)
			result := fn(call)
			return result.Export(), nil
		}, nil
	case func(map[string]any) interface{}:
		return func(params map[string]any) (interface{}, error) {
			return fn(params), nil
		}, nil
	default:
		return nil, fmt.Errorf("execute must be a function")
	}
}

// wrapRegisterCommand returns a JS-callable function that implements goa.registerCommand.
func (b *JSBridge) wrapRegisterCommand() func(goja.FunctionCall) goja.Value {
	return func(call goja.FunctionCall) goja.Value {
		if b.ctx.RegisterCommand == nil {
			return b.vm.ToValue("error: CommandHandler not configured")
		}
		obj := call.Argument(0).ToObject(b.vm)
		name := obj.Get("name").String()
		shortHelp := obj.Get("shortHelp").String()
		longHelp := obj.Get("longHelp").String()

		var aliases []string
		if arr := b.extractAliases(obj); arr != nil {
			aliases = arr
		}

		runFn := obj.Get("run").Export()
		wrapper, err := b.buildCommandWrapper(runFn)
		if err != nil {
			return b.vm.ToValue("error: " + err.Error())
		}
		if err := b.ctx.RegisterCommand(name, aliases, shortHelp, longHelp, wrapper); err != nil {
			return b.vm.ToValue("error: " + err.Error())
		}
		return b.vm.ToValue("command registered: " + name)
	}
}

// wrapRegisterCompletion returns a JS-callable function that implements
// goa.registerCompletion(name, fn): fn(prefix) supplies argument completions
// for the named command. The JS function is invoked on the completer's
// goroutine under the VM lock (buildCompletionWrapper owns locking), so it may
// read plugin state (_fetchers, _cache) freely.
func (b *JSBridge) wrapRegisterCompletion() func(goja.FunctionCall) goja.Value {
	return func(call goja.FunctionCall) goja.Value {
		if b.ctx.RegisterCompletion == nil {
			return b.vm.ToValue("error: CompletionHandler not configured")
		}
		name := call.Argument(0).String()
		fnVal := call.Argument(1).Export()
		wrapper, err := b.buildCompletionWrapper(fnVal)
		if err != nil {
			return b.vm.ToValue("error: " + err.Error())
		}
		if err := b.ctx.RegisterCompletion(name, wrapper); err != nil {
			return b.vm.ToValue("error: " + err.Error())
		}
		return b.vm.ToValue("completions registered: " + name)
	}
}

// completionsFromExport converts a JS completer's exported return value into
// Completion candidates. Only an array of {value, description} objects is
// recognized; any other shape (nil, a bare map, scalars) degrades to an empty
// list. Entries whose value key is missing (renders as "<nil>") or empty are
// dropped so they never surface as blank suggestions.
func completionsFromExport(v interface{}) []Completion {
	out := []Completion{}
	arr, ok := v.([]interface{})
	if !ok {
		return out
	}
	for _, item := range arr {
		m, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		c := Completion{
			Value:       fmt.Sprint(m["value"]),
			Description: fmt.Sprint(m["description"]),
		}
		if c.Value != "" && c.Value != "<nil>" {
			out = append(out, c)
		}
	}
	return out
}

// buildCompletionWrapper converts a JS prefix→completions function into a Go
// callable. Runs the JS frame under the global VM lock (the TUI completer
// calls this off the command path); malformed return shapes degrade to an
// empty candidate list rather than erroring the keystroke.
func (b *JSBridge) buildCompletionWrapper(fn interface{}) (func(prefix string) []Completion, error) {
	jsFn, ok := fn.(func(goja.FunctionCall) goja.Value)
	if !ok {
		return nil, fmt.Errorf("complete must be a function")
	}
	return func(prefix string) []Completion {
		leave := enterVM()
		defer leave()
		unlock := lockVM()
		defer unlock()

		out := []Completion{}
		func() {
			defer func() { _ = recover() }() // a throwing completer must not break typing
			call := goja.FunctionCall{}
			call.Arguments = append(call.Arguments, b.vm.ToValue(prefix))
			out = completionsFromExport(jsFn(call).Export())
		}()
		return out
	}, nil
}

// extractAliases parses the "aliases" field from a JS command object.
func (b *JSBridge) extractAliases(obj *goja.Object) []string {
	aliasesVal := obj.Get("aliases")
	if aliasesVal == nil || goja.IsUndefined(aliasesVal) || goja.IsNull(aliasesVal) {
		return nil
	}
	arr, ok := aliasesVal.Export().([]interface{})
	if !ok {
		return nil
	}
	aliases := make([]string, 0, len(arr))
	for _, a := range arr {
		aliases = append(aliases, fmt.Sprint(a))
	}
	return aliases
}

// buildCommandWrapper converts a JS run function into a Go-compatible wrapper.
func (b *JSBridge) buildCommandWrapper(runFn interface{}) (func([]string) (string, error), error) {
	switch fn := runFn.(type) {
	case func(goja.FunctionCall) goja.Value:
		return func(args []string) (string, error) {
			leave := enterVM()
			defer leave()
			unlock := lockVM()
			defer unlock()
			jsArgs := b.vm.NewArray()
			for i, a := range args {
				jsArgs.Set(strconv.Itoa(i), a)
			}
			call := goja.FunctionCall{}
			call.Arguments = append(call.Arguments, jsArgs)
			result := fn(call)
			return result.String(), nil
		}, nil
	case func([]string) string:
		return func(args []string) (string, error) {
			return fn(args), nil
		}, nil
	default:
		return nil, fmt.Errorf("run must be a function")
	}
}

// wrapRegisterLifecycle returns a JS-callable function that implements goa.registerLifecycle.
func (b *JSBridge) wrapRegisterLifecycle() func(goja.FunctionCall) goja.Value {
	return func(call goja.FunctionCall) goja.Value {
		if b.ctx.RegisterLifecycle == nil {
			return b.vm.ToValue("error: lifecycle registry not configured")
		}
		hook := HookType(call.Argument(0).String())
		callback, err := b.buildLifecycleWrapper(call.Argument(1).Export())
		if err != nil {
			return b.vm.ToValue("error: " + err.Error())
		}
		b.ctx.RegisterLifecycle(hook, callback)
		return b.vm.ToValue("lifecycle registered: " + string(hook))
	}
}

// buildLifecycleWrapper converts a JS callback into a Go-compatible lifecycle handler.
func (b *JSBridge) buildLifecycleWrapper(callbackVal interface{}) (LifecycleHandler, error) {
	switch cb := callbackVal.(type) {
	case func(goja.FunctionCall) goja.Value:
		return func(hook HookType, payload map[string]any) {
			unlock := lockVM()
			defer unlock()
			call := goja.FunctionCall{}
			call.Arguments = append(call.Arguments, b.vm.ToValue(string(hook)))
			call.Arguments = append(call.Arguments, b.vm.ToValue(payload))
			cb(call)
		}, nil
	case func(string, map[string]any):
		return func(hook HookType, payload map[string]any) {
			cb(string(hook), payload)
		}, nil
	default:
		return nil, fmt.Errorf("callback must be a function(hook, payload)")
	}
}

// wrapRegisterObserver returns a JS-callable function that implements goa.registerObserver.
func (b *JSBridge) wrapRegisterObserver() func(goja.FunctionCall) goja.Value {
	return func(call goja.FunctionCall) goja.Value {
		if b.ctx.RegisterObserver == nil {
			return b.vm.ToValue("error: ObserverHandler not configured")
		}
		callback, err := b.buildObserverWrapper(call.Argument(0).Export())
		if err != nil {
			return b.vm.ToValue("error: " + err.Error())
		}
		b.ctx.RegisterObserver(callback)
		return b.vm.ToValue("observer registered")
	}
}

// buildObserverWrapper converts a JS callback into a Go-compatible observer wrapper.
func (b *JSBridge) buildObserverWrapper(callbackVal interface{}) (func(string, interface{}), error) {
	switch cb := callbackVal.(type) {
	case func(goja.FunctionCall) goja.Value:
		return func(eventName string, payload interface{}) {
			unlock := lockVM()
			defer unlock()
			call := goja.FunctionCall{}
			call.Arguments = append(call.Arguments, b.vm.ToValue(eventName))
			call.Arguments = append(call.Arguments, b.vm.ToValue(payload))
			cb(call)
		}, nil
	case func(string, interface{}):
		return cb, nil
	default:
		return nil, fmt.Errorf("callback must be a function(event, payload)")
	}
}
