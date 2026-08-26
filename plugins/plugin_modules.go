// SPDX-License-Identifier: GPL-3.0-or-later

package plugins

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/dop251/goja"
	"gopkg.in/yaml.v3"
)

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
	// Hold the VM lock across script execution: a plugin may start a 0-delay
	// setTimeout during load (e.g. the quota plugin priming its cache), and
	// that timer fires on a scheduler goroutine that takes lockVM. Running the
	// script lock-free lets the timer interleave with load on the same goja
	// runtime — a data race (scheduler fireOnce vs RunString).
	b.installRequire(filepath.Dir(path))
	unlock := lockVM()
	defer unlock()
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
	// loaded tracks plugin ids already instantiated in this LoadAll pass so
	// the same id found in a second scanned directory never loads twice.
	loaded map[string]bool
}

// NewPluginLoader creates a plugin loader.
func NewPluginLoader(dirs, enabled []string) *PluginLoader {
	return &PluginLoader{
		dirs:    dirs,
		enabled: enabled,
		loaded:  map[string]bool{},
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
		// Malformed manifests refuse the plugin but never abort the scan
		// (consistent with the existing skip behavior); the reason is logged
		// so config errors are diagnosable instead of silently vanishing
		// (M6 §7 step 1).
		log.Printf("Warning: refusing plugin %s: %v\n", filepath.Join(dir, name), err)
		return nil
	}

	if !allEnabled && !isEnabled(def.ID, pl.enabled) {
		return nil // not enabled
	}

	// One VM per plugin id: a duplicate id means the same plugin exists in
	// more than one scanned directory (e.g. a stale materialized version dir
	// that survived an upgrade). Loading both would double every side effect
	// (outputs, segments, observers) and the first-registered command wins —
	// possibly the STALE copy. Skip with a loud log; MaterializeBundled's
	// version pruning makes this unreachable for bundled plugins in practice.
	if pl.loaded[def.ID] {
		log.Printf("Warning: duplicate plugin id %s at %s — already loaded from another directory, skipping stale copy\n",
			def.ID, filepath.Join(dir, name))
		return nil
	}
	pl.loaded[def.ID] = true

	bridge := NewJSBridge(*def, ctx)
	entryPath := filepath.Join(dir, name, def.Entry)
	if err := bridge.RunFile(entryPath); err != nil {
		return fmt.Errorf("plugin %s: %w", def.ID, err)
	}

	pl.bridges = append(pl.bridges, bridge)
	return nil
}

// loadManifest reads and parses a plugin.yaml file, enforcing the semantic
// validation shared with ValidateManifest so malformed manifests refuse the
// plugin everywhere manifests are honored.
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
	if err := validatePluginDef(&def); err != nil {
		return nil, fmt.Errorf("plugin %s: %w", path, err)
	}
	if def.Entry == "" {
		def.Entry = "plugin.js"
	}
	return &def, nil
}

// knownPermissions lists every permission name Goa understands in a plugin
// manifest. Unknown names fail validation so typos surface as config errors
// instead of silently granting nothing (or worse, being read as consent).
var knownPermissions = map[string]bool{
	"provider-keys": true,
	"oauth-token":   true,
	"ui-confirm":    true,
	"account-write": true,
}

// validatePluginDef checks the semantic constraints of the M6 manifest
// extensions: hook declarations must name known points and modes without
// duplicates (the review card and grant store key on (mode, point)), and
// permissions must be recognized names (§7 step 1).
func validatePluginDef(def *PluginDef) error {
	seen := make(map[string]bool, len(def.Hooks))
	for i, h := range def.Hooks {
		if !isValidHookPoint(h.Point) {
			return fmt.Errorf("hook #%d: unknown point %q (valid points: %s)", i+1, h.Point, strings.Join(ValidHookPoints(), ", "))
		}
		mode := HookMode(h.Mode)
		if mode != HookNotify && mode != HookIntercept {
			return fmt.Errorf("hook #%d (%s): mode must be %q or %q, got %q", i+1, h.Point, HookNotify, HookIntercept, h.Mode)
		}
		key := string(mode) + "\x00" + h.Point
		if seen[key] {
			return fmt.Errorf("duplicate hook declaration %q at point %q", h.Mode, h.Point)
		}
		seen[key] = true
	}
	for _, p := range def.Permissions {
		if !knownPermissions[p] {
			return fmt.Errorf("unknown permission %q (valid permissions: oauth-token, provider-keys, ui-confirm, account-write)", p)
		}
	}
	return nil
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

// ValidateManifest checks that a plugin.yaml has all required fields and
// that its hooks/permissions declarations satisfy the M6 semantic contract.
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
	if err := validatePluginDef(&def); err != nil {
		return fmt.Errorf("invalid plugin manifest: %w", err)
	}
	return nil
}
