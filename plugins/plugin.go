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
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/dop251/goja"
	"gopkg.in/yaml.v3"
)

// PluginDef describes a plugin's manifest.
type PluginDef struct {
	ID            string   `yaml:"id"`
	Name          string   `yaml:"name"`
	Version       string   `yaml:"version"`
	Entry         string   `yaml:"entry"`
	Description   string   `yaml:"description"`
	GoaMinVersion string   `yaml:"goa_min_version"`
	SkillsDir     string   `yaml:"skills_dir,omitempty"`
	Permissions   []string `yaml:"permissions,omitempty"`
}

// ToolHandler is called when a JS plugin registers a tool via goa.registerTool.
type ToolHandler func(name, description string, execute func(map[string]any) (interface{}, error)) error

// CommandHandler is called when a JS plugin registers a command via goa.registerCommand.
type CommandHandler func(name string, aliases []string, shortHelp, longHelp string, run func([]string) (string, error)) error

// ObserverHandler receives a callback that will be called for every event.
// The callback receives (eventName string, payload interface{}).
type ObserverHandler func(callback func(string, interface{}))

// CallToolHandler is called when a JS plugin invokes goa.callTool(name, params).
type CallToolHandler func(name string, params map[string]any) (interface{}, error)

// PluginContext provides the JS plugin with access to Goa subsystems.
type PluginContext struct {
	Config map[string]any
	// ConfigFunc, when set, is called on every goa.config() invocation so the
	// plugin always sees the LIVE config (e.g. after a provider/model switch).
	// It takes precedence over the static Config snapshot, which is kept for
	// tests and simple plugins.
	ConfigFunc         func() map[string]any
	Logger             LoggerAPI
	RegisterTool       ToolHandler                             // called when JS calls goa.registerTool
	RegisterCommand    CommandHandler                          // called when JS calls goa.registerCommand
	RegisterObserver   ObserverHandler                         // called when JS calls goa.registerObserver
	RegisterLifecycle  func(hook HookType, h LifecycleHandler) // called when JS calls goa.registerLifecycle
	CallTool           CallToolHandler                         // called when JS calls goa.callTool
	EventBus           *EventBus
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

// vmMu serializes every JavaScript execution across all plugins. Goja
// runtimes are not goroutine-safe, and plugins have asynchronous entry points
// (timers, hotkeys, HTTP completions, command/tool invocations) arriving from
// many goroutines. A plain mutex — rather than a dedicated goroutine queue —
// is used so a JS call that blocks in a bridge (e.g. goa.http.fetch) can
// still be re-entered by that same call chain without deadlock, while async
// callbacks (timers, hotkeys) simply wait their turn on the mutex.
var vmMu sync.Mutex

// lockVM acquires the global JS execution lock. All VM interactions must go
// through this so no two goroutines ever touch a runtime concurrently.
func lockVM() func() {
	vmMu.Lock()
	return vmMu.Unlock
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
	goaObj.Set("registerObserver", b.wrapRegisterObserver())
	goaObj.Set("registerLifecycle", b.wrapRegisterLifecycle())
	goaObj.Set("callTool", b.wrapCallTool())

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

// wrapCallTool returns a JS-callable function that implements goa.callTool.
func (b *JSBridge) wrapCallTool() func(goja.FunctionCall) goja.Value {
	return func(call goja.FunctionCall) goja.Value {
		if b.ctx.CallTool == nil {
			return b.vm.ToValue("error: CallToolHandler not configured")
		}
		name := call.Argument(0).String()
		paramsVal := call.Argument(1).Export()
		params, ok := paramsVal.(map[string]any)
		if !ok {
			params = map[string]any{}
		}
		result, err := b.ctx.CallTool(name, params)
		if err != nil {
			return b.vm.ToValue("error: " + err.Error())
		}
		return b.vm.ToValue(result)
	}
}

// RunFile loads and executes a plugin.js file.
func (b *JSBridge) RunFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read plugin %s: %w", path, err)
	}
	b.installRequire(filepath.Dir(path))
	_, err = b.vm.RunString(string(data))
	if err != nil {
		return fmt.Errorf("execute plugin %s: %w", path, err)
	}
	return nil
}

// installRequire registers a scoped CommonJS-style require() that loads JS
// modules relative to the requiring module's directory (Node semantics). Each
// module is wrapped in a function with (module, exports, require) so modules
// use exports.foo = ... or module.exports = .... The module cache is
// per-bridge so repeated requires return the same exports object and circular
// requires don't infinitely recurse. Paths are confined to the plugin
// directory to prevent reading arbitrary files.
func (b *JSBridge) installRequire(pluginDir string) {
	cache := map[string]goja.Value{}
	var requireAt func(dir string) func(goja.FunctionCall) goja.Value
	requireAt = func(dir string) func(goja.FunctionCall) goja.Value {
		return func(call goja.FunctionCall) goja.Value {
			return b.requireModule(requireAt, cache, dir, pluginDir, call.Argument(0).String())
		}
	}
	b.vm.Set("require", requireAt(pluginDir))
}

// requireModule resolves, loads, and executes one module, returning its
// exports. Split from installRequire to stay within the complexity budget.
func (b *JSBridge) requireModule(requireAt func(string) func(goja.FunctionCall) goja.Value, cache map[string]goja.Value, dir, pluginDir, rel string) goja.Value {
	path, err := resolveModulePath(dir, rel, pluginDir)
	if err != nil {
		throwError(b.vm, err)
	}
	if cached, ok := cache[path]; ok {
		return cached
	}
	data, err := os.ReadFile(path)
	if err != nil {
		throwError(b.vm, fmt.Errorf("require %s: %v", rel, err))
	}
	return b.execModule(requireAt, cache, path, rel, string(data))
}

// execModule wraps, runs, and caches a loaded module's exports.
func (b *JSBridge) execModule(requireAt func(string) func(goja.FunctionCall) goja.Value, cache map[string]goja.Value, path, rel, src string) goja.Value {
	module := b.vm.NewObject()
	exports := b.vm.NewObject()
	module.Set("exports", exports)
	// Register in cache before executing to break circular imports.
	cache[path] = exports

	wrapper, werr := b.buildModuleWrapper(src)
	if werr != nil {
		delete(cache, path)
		throwError(b.vm, fmt.Errorf("require %s: %v", rel, werr))
	}
	// The nested require resolves relative to THIS module's directory.
	nestedRequire := b.vm.ToValue(requireAt(filepath.Dir(path)))
	if _, err := wrapper(exports, exports, module, nestedRequire); err != nil {
		delete(cache, path)
		throwError(b.vm, fmt.Errorf("require %s: %v", rel, err))
	}
	// Support `module.exports = {...}` replacement.
	if finalExports := module.Get("exports"); finalExports != nil && finalExports != exports {
		cache[path] = finalExports
		return finalExports
	}
	return exports
}

// buildModuleWrapper compiles module source into a callable (exports, module,
// require) function.
func (b *JSBridge) buildModuleWrapper(src string) (func(goja.Value, ...goja.Value) (goja.Value, error), error) {
	wrapped := "(function(exports, module, require) {\n" + src + "\n})"
	v, err := b.vm.RunString(wrapped)
	if err != nil {
		return nil, err
	}
	fn, ok := goja.AssertFunction(v)
	if !ok {
		return nil, fmt.Errorf("module wrapper is not a function")
	}
	return fn, nil
}

// resolveModulePath resolves rel against the requiring module's directory
// (dir) while confining the result to the plugin root (pluginDir).
func resolveModulePath(dir, rel, pluginDir string) (string, error) {
	if rel == "" {
		return "", fmt.Errorf("require: empty path")
	}
	clean := filepath.Clean(filepath.Join(dir, rel))
	base := filepath.Clean(pluginDir)
	if clean != base && !hasPathPrefix(clean, base) {
		return "", fmt.Errorf("require: path %q escapes plugin directory", rel)
	}
	if filepath.Ext(clean) == "" {
		clean += ".js"
	}
	return clean, nil
}

// hasPathPrefix reports whether path is inside base (or equal to base),
// using a filepath-aware relative check.
func hasPathPrefix(path, base string) bool {
	if path == base {
		return true
	}
	rel, err := filepath.Rel(base, path)
	if err != nil {
		return false
	}
	// Inside base when the relative path neither escapes (..) nor is absolute.
	return rel != ".." && !filepath.IsAbs(rel) && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// throwError raises a JS exception from a Go error.
func throwError(vm *goja.Runtime, err error) {
	panic(vm.ToValue(err.Error()))
}

// PluginLoader scans plugin directories, loads manifests, and
// initializes JS runtimes for enabled plugins.
type PluginLoader struct {
	dirs    []string
	enabled []string // plugin IDs; ["*"] = all
	bridges []*JSBridge
}

// NewPluginLoader creates a plugin loader.
func NewPluginLoader(dirs, enabled []string) *PluginLoader {
	return &PluginLoader{
		dirs:    dirs,
		enabled: enabled,
	}
}

// LoadAll discovers and loads all enabled plugins.
func (pl *PluginLoader) LoadAll(ctx PluginContext) ([]*JSBridge, error) {
	allEnabled := pl.allEnabled()

	for _, dir := range pl.dirs {
		if err := pl.loadFromDir(dir, ctx, allEnabled); err != nil {
			return pl.bridges, err
		}
	}
	return pl.bridges, nil
}

func (pl *PluginLoader) allEnabled() bool {
	return len(pl.enabled) == 1 && pl.enabled[0] == "*"
}

func (pl *PluginLoader) loadFromDir(dir string, ctx PluginContext, allEnabled bool) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil // skip unreadable dirs
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if err := pl.loadPlugin(dir, entry.Name(), ctx, allEnabled); err != nil {
			return err
		}
	}
	return nil
}

func (pl *PluginLoader) loadPlugin(dir, name string, ctx PluginContext, allEnabled bool) error {
	manifestPath := filepath.Join(dir, name, "plugin.yaml")
	if _, err := os.Stat(manifestPath); err != nil {
		return nil // not a plugin directory
	}

	def, err := loadManifest(manifestPath)
	if err != nil {
		return nil // invalid manifest, skip
	}

	if !allEnabled && !isEnabled(def.ID, pl.enabled) {
		return nil // not enabled
	}

	bridge := NewJSBridge(*def, ctx)
	entryPath := filepath.Join(dir, name, def.Entry)
	if err := bridge.RunFile(entryPath); err != nil {
		return fmt.Errorf("plugin %s: %w", def.ID, err)
	}

	pl.bridges = append(pl.bridges, bridge)
	return nil
}

// loadManifest reads and parses a plugin.yaml file.
func loadManifest(path string) (*PluginDef, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var def PluginDef
	if err := yaml.Unmarshal(data, &def); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if def.ID == "" {
		return nil, fmt.Errorf("plugin %s: missing id", path)
	}
	if def.Entry == "" {
		def.Entry = "plugin.js"
	}
	return &def, nil
}

// isEnabled checks if a plugin ID is in the enabled list.
func isEnabled(id string, enabled []string) bool {
	for _, e := range enabled {
		if e == id {
			return true
		}
	}
	return false
}

// LoadFrom loads a single plugin from a directory containing plugin.yaml.
func LoadFrom(dir string, ctx PluginContext) (*JSBridge, error) {
	manifestPath := filepath.Join(dir, "plugin.yaml")
	def, err := loadManifest(manifestPath)
	if err != nil {
		return nil, err
	}
	if def.Entry != "" && !filepath.IsAbs(def.Entry) {
		def.Entry = filepath.Join(dir, def.Entry)
	}
	bridge := NewJSBridge(*def, ctx)
	if err := bridge.RunFile(def.Entry); err != nil {
		return nil, fmt.Errorf("run plugin: %w", err)
	}
	return bridge, nil
}

// ValidateManifest checks that a plugin.yaml has all required fields.
func ValidateManifest(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var def PluginDef
	if err := yaml.Unmarshal(data, &def); err != nil {
		return fmt.Errorf("yaml error: %w", err)
	}
	if def.ID == "" {
		return fmt.Errorf("plugin manifest missing required field: id")
	}
	if def.Name == "" {
		return fmt.Errorf("plugin manifest missing required field: name")
	}
	return nil
}
