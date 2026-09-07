// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package plugins

import (
	"fmt"
	"os"
	"sync"

	"github.com/pijalu/gpython/compile"
	"github.com/pijalu/gpython/py"
	_ "github.com/pijalu/gpython/stdlib"
)

// ExtendedHTTP returns the optional HTTP bridge (nil when unconfigured).
func (c PluginContext) ExtendedHTTP() *HTTPBridge {
	if c.Extended == nil {
		return nil
	}
	return c.Extended.HTTP
}

// networkPermissionGatedFetchError is the closed-gate result both bridges
// return when goa.http.fetch runs without the "network" manifest permission.
// The message must contain both "network" and "permission" (pinned by the
// bridge permission tests on both runtimes).
func networkPermissionGatedFetchError() map[string]any {
	return map[string]any{
		"error": `goa.http.fetch requires the "network" permission in plugin.yaml`,
	}
}

// PythonBridge manages a gpython interpreter context for a single Python
// plugin, exposing a goa module mirroring the JS bridge's goa.* globals.
// The context is persistent across RunSource/RunFile calls (plugin state
// survives between entry points); access is serialized with mu because
// gpython contexts are not goroutine-safe. Callbacks into Python (tool
// execute, command run) take mu as well; Python code must not re-enter the
// bridge synchronously from those callbacks.
type PythonBridge struct {
	mu      sync.Mutex
	ctx     py.Context
	mainMod *py.Module
	goaMod  *py.Module
	def     PluginDef
	pctx    PluginContext
}

// Kind reports the Python runtime kind.
func (b *PythonBridge) Kind() PluginKind { return PluginKindPython }

// hasPermission mirrors JSBridge.hasPermission for the Python runtime.
func (b *PythonBridge) hasPermission(perm string) bool {
	for _, p := range b.def.Permissions {
		if p == perm {
			return true
		}
	}
	return false
}

// NewPythonBridge creates a Python bridge for the given plugin definition.
// The goa module (register_tool, register_command, call_tool, http.fetch) is
// installed at construction and pre-bound as a `goa` global in the plugin's
func NewPythonBridge(def PluginDef, ctx PluginContext) *PythonBridge {
	b := &PythonBridge{def: def, pctx: ctx}
	b.ctx = py.NewContext(py.DefaultContextOpts())
	if err := b.installGoaModule(); err != nil {
		// Module installation only fails on internal errors (method
		// construction); without goa the plugin cannot register anything.
		// Keep the bridge usable for GlobalString probes instead of nil.
		_ = err
	}
	// Pre-create the persistent main module and bind `goa` as a global so
	// plugin code uses it directly, like the JS bridge's pre-defined `goa`.
	if code, err := compile.Compile("pass\n", "<init>", py.ExecMode, 0, true); err == nil {
		if m, err := py.RunCode(b.ctx, code, "<init>", nil); err == nil && m != nil {
			b.mainMod = m
			if b.goaMod != nil {
				m.Globals["goa"] = b.goaMod
			}
		}
	}
	return b
}

// RunSource compiles and executes Python source in the bridge's persistent
// main module namespace.
func (b *PythonBridge) RunSource(src string) error {
	code, err := compile.Compile(src, "<plugin>", py.ExecMode, 0, true)
	if err != nil {
		return fmt.Errorf("compile plugin: %w", err)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	var inModule interface{}
	if b.mainMod != nil {
		inModule = b.mainMod
	}
	m, err := py.RunCode(b.ctx, code, "<plugin>", inModule)
	if err != nil {
		return fmt.Errorf("run plugin: %w", err)
	}
	if m != nil {
		b.mainMod = m
		if b.goaMod != nil {
			m.Globals["goa"] = b.goaMod
		}
	}
	return nil
}

// RunFile reads a .py entry file and executes it in the bridge namespace.
func (b *PythonBridge) RunFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return b.RunSource(string(data))
}

// GlobalString returns the string form of a main-module global ("" when
// absent). Used by tests and the quota demo to read plugin results.
func (b *PythonBridge) GlobalString(name string) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.mainMod == nil {
		return ""
	}
	v, ok := b.mainMod.Globals[name]
	if !ok || v == nil {
		return ""
	}
	s, err := py.Str(v)
	if err != nil {
		return ""
	}
	ps, ok := s.(py.String)
	if !ok {
		return ""
	}
	return string(ps)
}

// installGoaModule builds the goa module (plus the goa.http submodule) and
// registers both in the interpreter store so `import goa` resolves.
func (b *PythonBridge) installGoaModule() error {
	store := b.ctx.Store()
	httpMod, err := store.NewModule(b.ctx, &py.ModuleImpl{
		Info: py.ModuleInfo{Name: "goa.http", Doc: "goa.http plugin HTTP client."},
		Methods: []*py.Method{
			py.MustNewMethod("fetch", b.pyFetch, 0, "fetch(url, opts)"),
		},
	})
	if err != nil {
		return err
	}
	goaMod, err := store.NewModule(b.ctx, &py.ModuleImpl{
		Info:    py.ModuleInfo{Name: "goa", Doc: "goa plugin host API."},
		Globals: py.StringDict{"http": httpMod},
		Methods: []*py.Method{
			py.MustNewMethod("register_tool", b.pyRegisterTool, 0, "register_tool"),
			py.MustNewMethod("register_command", b.pyRegisterCommand, 0, "register_command"),
			py.MustNewMethod("call_tool", b.pyCallTool, 0, "call_tool"),
		},
	})
	if err != nil {
		return err
	}
	b.goaMod = goaMod
	return nil
}

// pctxHTTP returns the optional HTTP bridge (nil when unconfigured).
func (b *PythonBridge) pctxHTTP() *HTTPBridge {
	return b.pctx.ExtendedHTTP()
}

// pyFetch implements goa.http.fetch(url, opts) for Python plugins. Without
// the "network" manifest permission it fails closed (never reaching the HTTP
// hook), mirroring the JS gate.
func (b *PythonBridge) pyFetch(self py.Object, args py.Tuple, kwargs py.StringDict) (py.Object, error) {
	if !b.hasPermission("network") {
		return goMapToPy(networkPermissionGatedFetchError()), nil
	}
	httpB := b.pctxHTTP()
	if httpB == nil {
		return goMapToPy(map[string]any{"error": "goa.http.fetch unavailable: HTTP bridge not configured"}), nil
	}
	req := HTTPRequest{Method: "GET"}
	if len(args) > 0 {
		if s, ok := args[0].(py.String); ok {
			req.URL = string(s)
		} else {
			return goMapToPy(map[string]any{"error": "goa.http.fetch: url must be a string"}), nil
		}
	}
	if len(args) > 1 {
		if d, ok := args[1].(py.StringDict); ok {
			applyPyFetchOpts(&req, d)
		}
	}
	resp := httpDo()(httpB, req)
	return goMapToPy(httpResponseToGoMap(resp)), nil
}

// applyPyFetchOpts overlays the optional opts dict onto req.
func applyPyFetchOpts(req *HTTPRequest, opts py.StringDict) {
	req.Method = pyOptStr(opts, "method", req.Method)
	if h, ok := opts["headers"]; ok {
		if d, ok := h.(py.StringDict); ok {
			req.Headers = pyStrMap(d)
		}
	}
	req.Body = pyOptStr(opts, "body", req.Body)
}

// pyOptStr reads an optional string field with a default.
func pyOptStr(d py.StringDict, key, def string) string {
	v, ok := d[key]
	if !ok {
		return def
	}
	s, ok := v.(py.String)
	if !ok || string(s) == "" {
		return def
	}
	return string(s)
}

// pyStrMap converts a Python string dict to a Go string map.
func pyStrMap(d py.StringDict) map[string]string {
	out := make(map[string]string, len(d))
	for k, v := range d {
		if s, ok := v.(py.String); ok {
			out[k] = string(s)
		}
	}
	return out
}

// httpResponseToGoMap converts an HTTPResponse to a plain Go map for the
// Python boundary.
func httpResponseToGoMap(resp HTTPResponse) map[string]any {
	headers := map[string]any{}
	for k, v := range resp.Headers {
		headers[k] = v
	}
	m := map[string]any{
		"status":  int64(resp.Status),
		"headers": headers,
		"body":    resp.Body,
	}
	if resp.Error != "" {
		m["error"] = resp.Error
	}
	return m
}

// pyRegisterTool implements goa.register_tool({name, description, execute}).
func (b *PythonBridge) pyRegisterTool(self py.Object, args py.Tuple, kwargs py.StringDict) (py.Object, error) {
	if b.pctx.RegisterTool == nil {
		return nil, py.ExceptionNewf(py.TypeError, "goa.register_tool unavailable: ToolHandler not configured")
	}
	if len(args) < 1 {
		return nil, py.ExceptionNewf(py.TypeError, "goa.register_tool requires a spec dict")
	}
	spec, ok := args[0].(py.StringDict)
	if !ok {
		return nil, py.ExceptionNewf(py.TypeError, "goa.register_tool requires a spec dict")
	}
	name := pyDictStr(spec, "name")
	desc := pyDictStr(spec, "description")
	execObj, ok := spec["execute"]
	if !ok || execObj == nil {
		return nil, py.ExceptionNewf(py.TypeError, "goa.register_tool requires an execute function")
	}
	wrapper := func(params map[string]any) (interface{}, error) {
		b.mu.Lock()
		defer b.mu.Unlock()
		res, err := py.Call(execObj, py.Tuple{goMapToPy(params)}, nil)
		if err != nil {
			return nil, err
		}
		return pyToGo(res), nil
	}
	if err := b.pctx.RegisterTool(name, desc, wrapper); err != nil {
		return nil, py.ExceptionNewf(py.RuntimeError, "register_tool: %s", err.Error())
	}
	return py.String("tool registered: " + name), nil
}

// pyRegisterCommand implements goa.register_command({name, aliases,
// shortHelp, longHelp, run}).
func (b *PythonBridge) pyRegisterCommand(self py.Object, args py.Tuple, kwargs py.StringDict) (py.Object, error) {
	if b.pctx.RegisterCommand == nil {
		return nil, py.ExceptionNewf(py.TypeError, "goa.register_command unavailable: CommandHandler not configured")
	}
	if len(args) < 1 {
		return nil, py.ExceptionNewf(py.TypeError, "goa.register_command requires a spec dict")
	}
	spec, ok := args[0].(py.StringDict)
	if !ok {
		return nil, py.ExceptionNewf(py.TypeError, "goa.register_command requires a spec dict")
	}
	parsed := parsePyCommandSpec(spec)
	wrapper := b.buildPyCommandRunner(parsed.name, parsed.run)
	if err := b.pctx.RegisterCommand(parsed.name, parsed.aliases, parsed.shortHelp, parsed.longHelp, wrapper); err != nil {
		return nil, py.ExceptionNewf(py.RuntimeError, "register_command: %s", err.Error())
	}
	return py.String("command registered: " + parsed.name), nil
}

// pyCommandSpec is the parsed form of a goa.register_command spec dict.
type pyCommandSpec struct {
	name      string
	aliases   []string
	shortHelp string
	longHelp  string
	run       py.Object
}

// parsePyCommandSpec extracts command fields from a spec dict.
func parsePyCommandSpec(spec py.StringDict) pyCommandSpec {
	parsed := pyCommandSpec{
		name:      pyDictStr(spec, "name"),
		shortHelp: pyDictStr(spec, "shortHelp"),
		longHelp:  pyDictStr(spec, "longHelp"),
	}
	if a, ok := spec["aliases"]; ok {
		parsed.aliases = pyToStringSlice(a)
	}
	if run, ok := spec["run"]; ok && run != nil {
		parsed.run = run
	}
	return parsed
}

// buildPyCommandRunner wraps a Python run callable as a Go command runner.
func (b *PythonBridge) buildPyCommandRunner(name string, runObj py.Object) func([]string) (string, error) {
	return func(argv []string) (string, error) {
		if runObj == nil {
			return "", fmt.Errorf("command %q has no run function", name)
		}
		b.mu.Lock()
		defer b.mu.Unlock()
		items := make([]py.Object, len(argv))
		for i, a := range argv {
			items[i] = py.String(a)
		}
		res, err := py.Call(runObj, py.Tuple{&py.List{Items: items}}, nil)
		if err != nil {
			return "", err
		}
		s, err := py.Str(res)
		if err != nil {
			return "", err
		}
		ps, ok := s.(py.String)
		if !ok {
			return "", fmt.Errorf("command %q returned non-string", name)
		}
		return string(ps), nil
	}
}

// pyCallTool implements goa.call_tool(name, params).
func (b *PythonBridge) pyCallTool(self py.Object, args py.Tuple, kwargs py.StringDict) (py.Object, error) {
	if b.pctx.CallTool == nil {
		return nil, py.ExceptionNewf(py.TypeError, "goa.call_tool unavailable: CallToolHandler not configured")
	}
	if len(args) < 1 {
		return nil, py.ExceptionNewf(py.TypeError, "goa.call_tool requires a tool name")
	}
	name, ok := args[0].(py.String)
	if !ok {
		return nil, py.ExceptionNewf(py.TypeError, "goa.call_tool requires a tool name")
	}
	params := map[string]any{}
	if len(args) > 1 && args[1] != nil {
		if d, ok := args[1].(py.StringDict); ok {
			params = pyDictToGo(d)
		}
	}
	res, err := b.pctx.CallTool(string(name), params)
	if err != nil {
		return nil, py.ExceptionNewf(py.RuntimeError, "call_tool: %s", err.Error())
	}
	return goValueToPy(res), nil
}

// pyDictStr reads an optional string field from a spec dict.
func pyDictStr(d py.StringDict, key string) string {
	if v, ok := d[key]; ok {
		if s, ok := v.(py.String); ok {
			return string(s)
		}
	}
	return ""
}

// pyToStringSlice converts a Python list/tuple of strings to []string.
func pyToStringSlice(v py.Object) []string {
	switch t := v.(type) {
	case *py.List:
		out := make([]string, 0, len(t.Items))
		for _, item := range t.Items {
			if s, ok := item.(py.String); ok {
				out = append(out, string(s))
			}
		}
		return out
	case py.Tuple:
		out := make([]string, 0, len(t))
		for _, item := range t {
			if s, ok := item.(py.String); ok {
				out = append(out, string(s))
			}
		}
		return out
	default:
		return nil
	}
}

// pyDictToGo converts a Python string dict to a Go map.
func pyDictToGo(d py.StringDict) map[string]any {
	out := make(map[string]any, len(d))
	for k, v := range d {
		out[k] = pyToGo(v)
	}
	return out
}

// pyToGo converts Python values to plain Go values.
func pyToGo(v py.Object) interface{} {
	switch t := v.(type) {
	case py.String:
		return string(t)
	case py.Int:
		return int64(t)
	case py.Float:
		return float64(t)
	case py.Bool:
		return bool(t)
	case py.NoneType:
		return nil
	case *py.List:
		out := make([]interface{}, 0, len(t.Items))
		for _, item := range t.Items {
			out = append(out, pyToGo(item))
		}
		return out
	case py.Tuple:
		out := make([]interface{}, 0, len(t))
		for _, item := range t {
			out = append(out, pyToGo(item))
		}
		return out
	case py.StringDict:
		return pyDictToGo(t)
	default:
		s, err := py.Str(v)
		if err != nil {
			return fmt.Sprintf("%v", v)
		}
		return string(s.(py.String))
	}
}

// goMapToPy converts a Go string map to a Python dict.
func goMapToPy(m map[string]any) py.StringDict {
	out := make(py.StringDict, len(m))
	for k, v := range m {
		out[k] = goValueToPy(v)
	}
	return out
}

// goStrMapToPy converts a Go string map to a Python dict.
func goStrMapToPy(m map[string]string) py.Object {
	out := make(py.StringDict, len(m))
	for k, s := range m {
		out[k] = py.String(s)
	}
	return out
}

// goStrSliceToPy converts a Go string slice to a Python list.
func goStrSliceToPy(items []string) py.Object {
	objs := make([]py.Object, len(items))
	for i, s := range items {
		objs[i] = py.String(s)
	}
	return &py.List{Items: objs}
}

// goValueToPy converts plain Go values to Python objects.
func goValueToPy(v interface{}) py.Object {
	switch t := v.(type) {
	case nil:
		return py.None
	case string:
		return py.String(t)
	case bool:
		return py.Bool(t)
	case int:
		return py.Int(t)
	case int64:
		return py.Int(t)
	case float64:
		return py.Float(t)
	case map[string]any:
		return goMapToPy(t)
	case map[string]string:
		return goStrMapToPy(t)
	case []string:
		return goStrSliceToPy(t)
	case []interface{}:
		items := make([]py.Object, len(t))
		for i, e := range t {
			items[i] = goValueToPy(e)
		}
		return &py.List{Items: items}
	default:
		return py.String(fmt.Sprintf("%v", v))
	}
}
