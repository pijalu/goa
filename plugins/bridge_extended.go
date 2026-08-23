// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package plugins

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/dop251/goja"
	"github.com/pijalu/goa/internal/ansi"
)

// ExtendContext carries the optional subsystems a plugin may use beyond the
// core PluginContext. Nil fields disable the matching goa.* API so plugins
// degrade gracefully (they receive an "api unavailable" string, matching the
// existing handler-not-configured convention).
type ExtendContext struct {
	HTTP      *HTTPBridge
	Storage   *StorageBridge
	Scheduler *Scheduler
	Browser   *BrowserBridge
	Hotkeys   *HotkeyBridge
	UI        *UIBridge
	// Output writes a user-visible message to the chat viewport.
	Output func(msg string)
	// SessionUsage returns cumulative token stats for the local/inferred
	// quota fetcher. Nil disables goa.sessionUsage().
	SessionUsage func() map[string]any
	// SegmentColor maps a semantic color name ("ok", "warn", "critical",
	// "pending") to an ANSI-escapable hex color for status-bar segments.
	// Nil disables coloring (segment text renders unstyled). Wired by the
	// app layer to the active TUI theme so plugins never emit console codes.
	SegmentColor func(name string) string
	// OAuthToken returns an opaque, Goa-managed OAuth access token. Plugins do
	// not receive refresh tokens or direct access to the credential store.
	OAuthToken func(ctx context.Context, provider string) (map[string]any, error)
}

// setupExtendedGlobals wires the optional goa.* APIs onto the goa object.
// Called from setupGlobals after the core APIs are registered.
func (b *JSBridge) setupExtendedGlobals(goaObj *goja.Object) {
	ext := b.ctx.Extended
	if ext == nil {
		return
	}
	b.setupHTTP(goaObj, ext.HTTP)
	b.setupStorage(goaObj, ext.Storage)
	b.setupTimers(goaObj, ext.Scheduler)
	b.setupBrowser(goaObj, ext.Browser)
	b.setupHotkeys(goaObj, ext.Hotkeys)
	b.setupUI(goaObj, ext.UI)
	b.setupOutput(goaObj, ext.Output)
	b.setupSessionUsage(goaObj, ext.SessionUsage)
	b.setupSegmentColor(goaObj, ext.SegmentColor)
	b.setupOAuth(goaObj, ext.OAuthToken)
}

// setupOAuth exposes only Goa-owned access tokens to trusted plugins.
func (b *JSBridge) setupOAuth(goaObj *goja.Object, tokenFn func(context.Context, string) (map[string]any, error)) {
	if tokenFn == nil {
		return
	}
	authObj := b.vm.NewObject()
	authObj.Set("oauthToken", func(call goja.FunctionCall) goja.Value {
		provider := call.Argument(0).String()
		var result map[string]any
		var err error
		runOutsideVMLock(func() { result, err = tokenFn(context.Background(), provider) })
		if err != nil {
			return b.vm.ToValue(map[string]any{"error": err.Error()})
		}
		return b.vm.ToValue(result)
	})
	goaObj.Set("auth", authObj)
}

// setupHTTP registers goa.http.fetch(url, opts). The actual request goes
// through httpDo, a package-level hook tests can override to avoid network.
func (b *JSBridge) setupHTTP(goaObj *goja.Object, httpB *HTTPBridge) {
	if httpB == nil {
		return
	}
	httpObj := b.vm.NewObject()
	httpObj.Set("fetch", func(call goja.FunctionCall) goja.Value {
		req := b.buildHTTPRequest(call)
		// The HTTP round-trip runs OUTSIDE the VM lock. A slow/hanging
		// endpoint can block for the full request timeout; holding vmMu
		// across it starves every other JS entry point (segment renders,
		// hotkeys, /quota) — the input freeze that landed exactly when the
		// quota segment appeared (Start-up). vmMu serializes goja
		// access only; the bridge Do is pure Go and needs no VM.
		// This function is only ever invoked from JS execution, which by
		// contract holds vmMu, so the unlock/lock pair is balanced.
		var resp HTTPResponse
		runOutsideVMLock(func() { resp = httpDo()(httpB, req) })
		return b.vm.ToValue(httpResponseToMap(resp))
	})
	goaObj.Set("http", httpObj)
}

// runOutsideVMLock releases the global VM lock for the duration of a blocking
// bridge call and re-acquires it afterwards (panic-safe via defer). It must
// only be called from a goja bridge callback — i.e. with vmMu held by the
// current goroutine — so the unlock/lock pair is balanced. While the lock is
// released the caller must not touch the goja runtime; other JS entry points
// (timers, hotkeys, segment renders) may run in the window.
func runOutsideVMLock(fn func()) {
	vmMu.Unlock()
	defer vmMu.Lock()
	fn()
}

// httpDoFunc performs an HTTP request. Tests substitute a mock via setHTTPDo
// to avoid network access; production uses the real Do.
type httpDoFunc func(b *HTTPBridge, req HTTPRequest) HTTPResponse

// httpDoStore holds the active httpDoFunc. It is accessed atomically because
// plugin scheduler timers (setTimeout/setInterval) invoke goa.http.fetch from
// their own goroutines — an unsynchronized package variable read raced the
// test-side swap (race detected at this call site once quota priming moved to
// a timer).
var httpDoStore atomic.Value // stores httpDoFunc

// httpDo returns the currently installed HTTP hook.
func httpDo() httpDoFunc {
	if v := httpDoStore.Load(); v != nil {
		return v.(httpDoFunc)
	}
	return func(b *HTTPBridge, req HTTPRequest) HTTPResponse { return b.Do(req) }
}

// setHTTPDo installs fn as the HTTP hook and returns a restore function.
// Tests call the restore (typically via t.Cleanup or defer) to put back the
// previous hook.
func setHTTPDo(fn httpDoFunc) (restore func()) {
	prev := httpDo()
	httpDoStore.Store(fn)
	return func() { httpDoStore.Store(prev) }
}

// buildHTTPRequest parses goa.http.fetch arguments.
func (b *JSBridge) buildHTTPRequest(call goja.FunctionCall) HTTPRequest {
	req := HTTPRequest{URL: call.Argument(0).String(), Method: "GET"}
	opts := call.Argument(1)
	if opts == nil || goja.IsUndefined(opts) || goja.IsNull(opts) {
		return req
	}
	obj := opts.ToObject(b.vm)
	if m := obj.Get("method"); m != nil && !goja.IsUndefined(m) {
		req.Method = m.String()
	}
	req.Headers = extractStringMap(obj.Get("headers"))
	req.Body = extractBody(obj.Get("body"))
	if t := obj.Get("timeoutMs"); t != nil && !goja.IsUndefined(t) {
		if ms, err := strconv.Atoi(t.String()); err == nil && ms > 0 {
			req.Timeout = time.Duration(ms) * time.Millisecond
		}
	}
	return req
}

// extractStringMap converts a JS object to map[string]string.
func extractStringMap(v goja.Value) map[string]string {
	if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
		return nil
	}
	exported, ok := v.Export().(map[string]interface{})
	if !ok {
		return nil
	}
	out := make(map[string]string, len(exported))
	for k, val := range exported {
		out[k] = fmt.Sprint(val)
	}
	return out
}

// extractBody converts a JS body (string or object) to a string payload.
func extractBody(v goja.Value) string {
	if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
		return ""
	}
	exported := v.Export()
	switch body := exported.(type) {
	case string:
		return body
	default:
		return JSONBody(exported)
	}
}

// httpResponseToMap shapes the response for JS (body, status, headers, json()).
func httpResponseToMap(resp HTTPResponse) map[string]any {
	return map[string]any{
		"status":  resp.Status,
		"headers": resp.Headers,
		"body":    resp.Body,
		"error":   resp.Error,
	}
}

// setupStorage registers goa.storage.{get,set,delete,keys}.
func (b *JSBridge) setupStorage(goaObj *goja.Object, st *StorageBridge) {
	if st == nil {
		return
	}
	obj := b.vm.NewObject()
	obj.Set("get", func(call goja.FunctionCall) goja.Value {
		return b.vm.ToValue(st.Get(call.Argument(0).String()))
	})
	obj.Set("set", func(call goja.FunctionCall) goja.Value {
		if err := st.Set(call.Argument(0).String(), call.Argument(1).String()); err != nil {
			return b.vm.ToValue("error: " + err.Error())
		}
		return b.vm.ToValue(true)
	})
	obj.Set("delete", func(call goja.FunctionCall) goja.Value {
		if err := st.Delete(call.Argument(0).String()); err != nil {
			return b.vm.ToValue("error: " + err.Error())
		}
		return b.vm.ToValue(true)
	})
	obj.Set("keys", func(call goja.FunctionCall) goja.Value {
		return b.vm.ToValue(st.Keys())
	})
	goaObj.Set("storage", obj)
}

// setupTimers registers goa.setInterval / clearInterval / setTimeout / clearTimeout.
func (b *JSBridge) setupTimers(goaObj *goja.Object, sch *Scheduler) {
	if sch == nil {
		return
	}
	goaObj.Set("setInterval", func(call goja.FunctionCall) goja.Value {
		cb := b.mustFunc(call.Argument(0), "setInterval callback")
		ms := call.Argument(1).ToInteger()
		id := sch.SetInterval(cb, time.Duration(ms)*time.Millisecond)
		return b.vm.ToValue(id)
	})
	goaObj.Set("setTimeout", func(call goja.FunctionCall) goja.Value {
		cb := b.mustFunc(call.Argument(0), "setTimeout callback")
		ms := call.Argument(1).ToInteger()
		id := sch.SetTimeout(cb, time.Duration(ms)*time.Millisecond)
		return b.vm.ToValue(id)
	})
	goaObj.Set("clearInterval", func(call goja.FunctionCall) goja.Value {
		sch.Clear(int(call.Argument(0).ToInteger()))
		return goja.Undefined()
	})
	goaObj.Set("clearTimeout", func(call goja.FunctionCall) goja.Value {
		sch.Clear(int(call.Argument(0).ToInteger()))
		return goja.Undefined()
	})
}

// mustFunc converts a JS value to a Go closure. Timer callbacks already run
// under the global VM lock (scheduler invokeSafe), so the closure calls the
// function directly.
func (b *JSBridge) mustFunc(v goja.Value, what string) func() {
	fn, ok := goja.AssertFunction(v)
	if !ok {
		return func() {}
	}
	return func() {
		if _, err := fn(goja.Undefined()); err != nil {
			b.ctx.Logger.Error(fmt.Sprintf("%s failed: %v", what, err))
		}
	}
}

// jsBool coerces a goja value to bool without triggering the ToBoolean
// panic present on some goja versions for undefined/native values.
func jsBool(v goja.Value) bool {
	if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
		return false
	}
	if b, ok := v.Export().(bool); ok {
		return b
	}
	// Truthiness fallback for numbers/strings.
	switch x := v.Export().(type) {
	case int64:
		return x != 0
	case float64:
		return x != 0
	case string:
		return x != ""
	}
	return false
}

// setupBrowser registers goa.openBrowser(url).
func (b *JSBridge) setupBrowser(goaObj *goja.Object, br *BrowserBridge) {
	if br == nil {
		return
	}
	goaObj.Set("openBrowser", func(call goja.FunctionCall) goja.Value {
		if err := br.OpenURL(call.Argument(0).String()); err != nil {
			return b.vm.ToValue("error: " + err.Error())
		}
		return b.vm.ToValue(true)
	})
}

// setupHotkeys registers goa.registerHotkey(def).
func (b *JSBridge) setupHotkeys(goaObj *goja.Object, hk *HotkeyBridge) {
	if hk == nil {
		return
	}
	goaObj.Set("registerHotkey", func(call goja.FunctionCall) goja.Value {
		obj := call.Argument(0).ToObject(b.vm)
		def := HotkeyDef{
			Key:         obj.Get("key").String(),
			Ctrl:        jsBool(obj.Get("ctrl")),
			Alt:         jsBool(obj.Get("alt")),
			Shift:       jsBool(obj.Get("shift")),
			Description: obj.Get("description").String(),
		}
		if handler := obj.Get("handler"); handler != nil {
			if fn, ok := goja.AssertFunction(handler); ok {
				def.Handler = b.buildHotkeyHandler(def, fn)
			}
		}
		hk.Register(def)
		return b.vm.ToValue("hotkey registered: " + def.KeyName())
	})
}

// buildHotkeyHandler wraps the JS hotkey callback. It defers while a logical
// frame is live (command parked on HTTP/confirm): entering then would
// interleave a second goja frame on this runtime (item E). The user can press
// the hotkey again once the UI is responsive.
func (b *JSBridge) buildHotkeyHandler(def HotkeyDef, fn goja.Callable) func() {
	return func() {
		if vmBusy() {
			b.ctx.Logger.Warn(fmt.Sprintf("hotkey %s deferred: plugin busy", def.KeyName()))
			return
		}
		unlock := lockVM()
		defer unlock()
		if _, err := fn(goja.Undefined()); err != nil {
			b.ctx.Logger.Error(fmt.Sprintf("hotkey %s failed: %v", def.KeyName(), err))
		}
	}
}

// setupUI registers goa.ui.addSegment / refreshSegment / addPane / addModal.
func (b *JSBridge) setupUI(goaObj *goja.Object, ui *UIBridge) {
	if ui == nil {
		return
	}
	uiObj := b.vm.NewObject()
	uiObj.Set("addSegment", func(call goja.FunctionCall) goja.Value {
		obj := call.Argument(0).ToObject(b.vm)
		def := UISegmentDef{
			ID:       obj.Get("id").String(),
			Priority: int(obj.Get("priority").ToInteger()),
		}
		if rv := obj.Get("render"); rv != nil {
			if fn, ok := goja.AssertFunction(rv); ok {
				def.Render = b.buildSegmentRender(fn)
			}
		}
		ui.AddSegment(def)
		return b.vm.ToValue("segment registered: " + def.ID)
	})
	uiObj.Set("refreshSegment", func(call goja.FunctionCall) goja.Value {
		ui.RequestRefresh(call.Argument(0).String())
		return goja.Undefined()
	})
	uiObj.Set("addPane", func(call goja.FunctionCall) goja.Value {
		obj := call.Argument(0).ToObject(b.vm)
		ui.AddPane(UIPaneDef{ID: obj.Get("id").String(), Title: obj.Get("title").String()})
		return b.vm.ToValue("pane registered: " + obj.Get("id").String())
	})
	uiObj.Set("addModal", func(call goja.FunctionCall) goja.Value {
		obj := call.Argument(0).ToObject(b.vm)
		ui.AddModal(UIDialogDef{ID: obj.Get("id").String(), Title: obj.Get("title").String()})
		return b.vm.ToValue("modal registered: " + obj.Get("id").String())
	})
	// goa.ui.confirm(spec) — blocking multiple-choice prompt (plan §4). The
	// wait happens OUTSIDE vmMu (runOutsideVMLock, precedent: setupOAuth)
	// with a fresh logical frame held (enterVM) so scheduler work defers
	// instead of interleaving a second frame on this runtime (item E).
	b.setupConfirm(uiObj, ui)
	goaObj.Set("ui", uiObj)
}

// buildSegmentRender wraps the JS render function for goa.ui.addSegment.
// The app render loop calls this from its own goroutine
// (drainSegmentRefreshes), which does NOT hold the VM lock; serialize with
// timers/hotkeys so goja's single-goroutine rule is preserved, and contain
// panics so a broken plugin cannot crash the UI goroutine (see the
// provider-quota nil-deref crash in vm.halted).
//
// Skip while another logical frame is live (a command parked on HTTP/confirm):
// two frames on one runtime must never overlap (item E). The next refresh
// re-renders, so skipping is safe — only delayed, never lost.
func (b *JSBridge) buildSegmentRender(fn goja.Callable) func() string {
	return func() string {
		if vmBusy() {
			return ""
		}
		unlock := lockVM()
		defer unlock()
		defer func() { _ = recover() }()
		res, err := fn(goja.Undefined())
		if err != nil {
			return ""
		}
		return b.segmentText(res)
	}
}

// setupConfirm registers goa.ui.confirm on the ui namespace object.
// Returns {id} | {cancelled:true} | {error} (the error form also carries
// cancelled:true when the failure is a dismissal flavor — timeout/no-ui).
func (b *JSBridge) setupConfirm(uiObj *goja.Object, ui *UIBridge) {
	uiObj.Set("confirm", func(call goja.FunctionCall) goja.Value {
		// Fail closed on the UI thread: the overlay can only be shown and
		// answered while that thread runs its loop; blocking it here would
		// deadlock until the 5m cap. Plugin commands run async (their
		// wrapper implements core.AsyncCommand), so legitimate callers are
		// never on this goroutine.
		if ui.onForbiddenThread() {
			return b.vm.ToValue(map[string]any{
				"cancelled": true,
				"error":     "goa.ui.confirm cannot block the UI thread; call it from a timer or background context",
			})
		}
		req, verr := b.parseConfirmSpec(call.Argument(0))
		if verr != "" {
			return b.vm.ToValue(map[string]any{"error": verr})
		}
		var resp ConfirmResponse
		leave := enterVM()
		runOutsideVMLock(func() {
			resp = <-ui.RequestConfirm(req)
		})
		leave()
		if resp.Err != "" {
			return b.vm.ToValue(map[string]any{"cancelled": resp.Cancelled, "error": resp.Err})
		}
		if resp.Cancelled {
			return b.vm.ToValue(map[string]any{"cancelled": true})
		}
		return b.vm.ToValue(map[string]any{"id": resp.ID})
	})
}

// parseConfirmSpec converts the JS spec object into a ConfirmRequest using
// plain-Go data BEFORE any lock release (goja values must not be touched once
// vmMu is dropped). Returns a descriptive error string ("" = valid), matching
// the registerHook validation convention.
func (b *JSBridge) parseConfirmSpec(v goja.Value) (ConfirmRequest, string) {
	if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
		return ConfirmRequest{}, "confirm expects an options object"
	}
	obj := v.ToObject(b.vm)
	req := ConfirmRequest{
		PluginID:    b.def.ID,
		Title:       fieldString(obj, "title"),
		Body:        fieldString(obj, "body"),
		DefaultID:   fieldString(obj, "defaultId"),
		AllowCancel: jsBool(obj.Get("allowCancel")),
	}
	opts, errStr := b.parseConfirmOptions(obj.Get("options"))
	if errStr != "" {
		return ConfirmRequest{}, errStr
	}
	req.Options = opts
	req.Timeout = confirmTimeoutFrom(obj)
	return req, ""
}

// parseConfirmOptions converts the JS options array into ConfirmOption rows.
func (b *JSBridge) parseConfirmOptions(v goja.Value) ([]ConfirmOption, string) {
	if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
		return nil, `"options" is required`
	}
	items, ok := v.Export().([]interface{})
	if !ok {
		return nil, `"options" must be an array`
	}
	if len(items) == 0 {
		return nil, "confirm requires at least one option"
	}
	opts := make([]ConfirmOption, 0, len(items))
	for i, item := range items {
		m, ok := item.(map[string]interface{})
		if !ok {
			return nil, fmt.Sprintf("options[%d] must be an object", i)
		}
		id, _ := m["id"].(string)
		label, _ := m["label"].(string)
		style, _ := m["style"].(string)
		opt := ConfirmOption{ID: id, Label: label, Style: style}
		if errStr := validateConfirmOption(opt); errStr != "" {
			return nil, fmt.Sprintf("options[%d]: %s", i, errStr)
		}
		opts = append(opts, opt)
	}
	return opts, ""
}

// confirmTimeoutFrom reads timeoutMs; ≤0/absent keeps the zero Duration
// (= MaxConfirmTimeout cap downstream).
func confirmTimeoutFrom(obj *goja.Object) time.Duration {
	tv := obj.Get("timeoutMs")
	if tv == nil || goja.IsUndefined(tv) || goja.IsNull(tv) {
		return 0
	}
	if ms := tv.ToInteger(); ms > 0 {
		return time.Duration(ms) * time.Millisecond
	}
	return 0
}

// validateConfirmOption checks one option row ("", = valid).
func validateConfirmOption(opt ConfirmOption) string {
	if opt.ID == "" {
		return "id is required"
	}
	if opt.Label == "" {
		return "label is required"
	}
	return ""
}

// setupSegmentColor registers goa.segmentColor(name), returning the active
// theme's hex color for a semantic name ("ok", "warn", "critical", "pending")
// so a plugin can build a pre-colored multi-part segment string. Returns ""
// when coloring is unavailable.
func (b *JSBridge) setupSegmentColor(goaObj *goja.Object, fn func(string) string) {
	if fn == nil {
		return
	}
	goaObj.Set("segmentColor", func(call goja.FunctionCall) goja.Value {
		return b.vm.ToValue(fn(call.Argument(0).String()))
	})
}

// segmentText converts a JS render() return value into the segment string
// pushed to the footer. A plain string passes through unchanged. An object
// {text, color} names a semantic color ("ok", "warn", "critical", "pending")
// resolved through the app-injected SegmentColor mapper, so plugins request a
// meaning, never a console code. Unknown/absent colors render unstyled.
func (b *JSBridge) segmentText(v goja.Value) string {
	obj, isObj := v.Export().(map[string]interface{})
	if !isObj {
		return v.String()
	}
	text, _ := obj["text"].(string)
	if text == "" {
		return ""
	}
	colorName, _ := obj["color"].(string)
	if colorName == "" || b.ctx.Extended == nil || b.ctx.Extended.SegmentColor == nil {
		return text
	}
	hex := b.ctx.Extended.SegmentColor(colorName)
	if hex == "" {
		return text
	}
	return ansi.Fg(hex) + text + ansi.Reset
}

// setupOutput registers goa.output(msg).
func (b *JSBridge) setupOutput(goaObj *goja.Object, out func(string)) {
	if out == nil {
		return
	}
	goaObj.Set("output", func(call goja.FunctionCall) goja.Value {
		out(call.Argument(0).String())
		return goja.Undefined()
	})
}

// setupSessionUsage registers goa.sessionUsage().
func (b *JSBridge) setupSessionUsage(goaObj *goja.Object, fn func() map[string]any) {
	if fn == nil {
		return
	}
	goaObj.Set("sessionUsage", func(call goja.FunctionCall) goja.Value {
		return b.vm.ToValue(fn())
	})
}

// setupHooks registers goa.registerHook(def) — the plugin hook API (plan
// §3.3). Validation errors return descriptive "error: …" strings, matching
// the registerCommand convention. Unknown-but-well-formed point strings are
// rejected against the constant list (typo protection).
//
// v1 reentrancy contract: a hook handler runs UNDER the global VM lock and
// must not call goa.callTool on a JS-plugin tool (that path would re-acquire
// the non-reentrant VM lock on the same goroutine). Pure-Go bridges
// (http/storage/output) release or avoid the lock and are safe.
func (b *JSBridge) setupHooks(goaObj *goja.Object) {
	if b.ctx.RegisterHook == nil {
		return
	}
	goaObj.Set("registerHook", func(call goja.FunctionCall) goja.Value {
		arg := call.Argument(0)
		if arg == nil || goja.IsUndefined(arg) || goja.IsNull(arg) {
			return b.vm.ToValue("error: registerHook expects an object argument")
		}
		obj := arg.ToObject(b.vm)
		spec, handlerVal, verr := b.parseHookDef(obj)
		if verr != "" {
			return b.vm.ToValue("error: " + verr)
		}
		wrapper := b.buildHookWrapper(handlerVal)
		if err := b.ctx.RegisterHook(spec, wrapper); err != nil {
			return b.vm.ToValue("error: " + err.Error())
		}
		return b.vm.ToValue(fmt.Sprintf("hook registered: %s@%s (%s)", spec.Name, spec.Point, spec.Mode))
	})
}

// parseHookDef validates one registerHook definition object. Returns a
// descriptive error string ("" = valid), following the registerCommand
// convention of surfacing mistakes at registration time.
func (b *JSBridge) parseHookDef(obj *goja.Object) (HookSpec, goja.Value, string) {
	name := fieldString(obj, "name")
	if name == "" {
		return HookSpec{}, nil, "name is required"
	}
	point := fieldString(obj, "point")
	if !isValidHookPoint(point) {
		return HookSpec{}, nil, fmt.Sprintf("unknown hook point %q (valid points: %s)", point, strings.Join(ValidHookPoints(), ", "))
	}
	modeStr := fieldString(obj, "mode")
	if modeStr == "" {
		modeStr = string(HookNotify)
	}
	mode := HookMode(modeStr)
	if mode != HookNotify && mode != HookIntercept {
		return HookSpec{}, nil, fmt.Sprintf("mode must be %q or %q, got %q", HookNotify, HookIntercept, modeStr)
	}
	handlerVal := obj.Get("handler")
	if handlerVal == nil || goja.IsUndefined(handlerVal) || goja.IsNull(handlerVal) {
		return HookSpec{}, nil, "handler function is required"
	}
	if _, ok := goja.AssertFunction(handlerVal); !ok {
		return HookSpec{}, nil, "handler must be a function(payload)"
	}
	// priority defaults to 0 when absent; goja's Get returns nil for missing
	// properties, so the value must be nil-guarded before ToInteger.
	priority := 0
	if pv := obj.Get("priority"); pv != nil && !goja.IsUndefined(pv) && !goja.IsNull(pv) {
		priority = int(pv.ToInteger())
	}
	spec := HookSpec{
		PluginID: b.def.ID,
		Name:     name,
		Point:    point,
		Mode:     mode,
		Priority: priority,
	}
	return spec, handlerVal, ""
}

// buildHookWrapper converts a JS hook handler into the Go HookHandler stored
// in the registry. The payload crosses the VM boundary as JSON in BOTH
// directions — no goja value aliasing, nested-map fidelity, trivially
// versionable. The JS call happens UNDER the global VM lock (mirroring
// buildToolWrapper's enterVM+lockVM discipline). Any throw, panic-adjacent
// failure, or non-JSON result degrades to pass-through (nil): availability
// beats enforcement for user-installed plugins.
func (b *JSBridge) buildHookWrapper(handlerVal goja.Value) HookHandler {
	fn, _ := goja.AssertFunction(handlerVal)
	if fn == nil {
		return func(map[string]any) map[string]any { return nil }
	}
	return func(payload map[string]any) map[string]any {
		// Pass through when a logical frame is live (command parked on
		// HTTP/confirm): running now would interleave a second goja frame on
		// this runtime (item E). Availability beats enforcement — the hook
		// simply does not apply to this invocation.
		if vmBusy() {
			return nil
		}
		data, err := json.Marshal(payload)
		if err != nil {
			b.logWarn("hook payload marshal failed: %v", err)
			return nil
		}
		unlock := lockVM()
		defer unlock()
		leave := enterVM()
		defer leave()
		return b.invokeHook(fn, data)
	}
}

// invokeHook runs the JS hook handler over a JSON-marshalled payload and
// converts the result back to a plain map. Every failure mode degrades to
// pass-through (nil).
func (b *JSBridge) invokeHook(fn goja.Callable, data []byte) map[string]any {
	jsPayload, err := jsonParse(b.vm, data)
	if err != nil {
		b.logWarn("hook payload parse failed: %v", err)
		return nil
	}
	res, err := fn(goja.Undefined(), jsPayload)
	if err != nil {
		b.logWarn("hook handler threw: %v", err)
		return nil // JS exception ⇒ pass-through for this handler
	}
	if res == nil || goja.IsUndefined(res) || goja.IsNull(res) {
		return nil // explicit pass-through
	}
	out, err := jsonStringify(b.vm, res)
	if err != nil {
		b.logWarn("hook result stringify failed: %v", err)
		return nil
	}
	var result map[string]any
	if err := json.Unmarshal(out, &result); err != nil {
		b.logWarn("hook result is not an object — ignored")
		return nil
	}
	return result
}

// logWarn emits through the plugin logger when wired; nil-safe by design so
// minimal test contexts never crash hook execution.
func (b *JSBridge) logWarn(format string, args ...any) {
	if b.ctx.Logger.Warn != nil {
		b.ctx.Logger.Warn(fmt.Sprintf(format, args...))
	}
}

// fieldString reads a string property from a JS object, normalizing
// undefined/null to "" so optional fields (e.g. mode) can default.
func fieldString(obj *goja.Object, key string) string {
	v := obj.Get(key)
	if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
		return ""
	}
	return v.String()
}

// jsonParse evaluates JSON bytes into a fresh goja value using the VM's own
// JSON.parse (object literals are ambiguous at program level).
func jsonParse(vm *goja.Runtime, data []byte) (goja.Value, error) {
	parse, err := jsonMethod(vm, "parse")
	if err != nil {
		return nil, err
	}
	return parse(goja.Undefined(), vm.ToValue(string(data)))
}

// jsonStringify serializes a goja value to JSON bytes via the VM's own
// JSON.stringify (guarantees only JSON-safe values cross back to Go).
func jsonStringify(vm *goja.Runtime, v goja.Value) ([]byte, error) {
	stringify, err := jsonMethod(vm, "stringify")
	if err != nil {
		return nil, err
	}
	res, err := stringify(goja.Undefined(), v)
	if err != nil {
		return nil, err
	}
	if res == nil || goja.IsUndefined(res) {
		return nil, fmt.Errorf("value has no JSON representation")
	}
	return []byte(res.String()), nil
}

// jsonMethod resolves a function on the runtime's intrinsic JSON object.
func jsonMethod(vm *goja.Runtime, name string) (goja.Callable, error) {
	jsonObj := vm.Get("JSON")
	if jsonObj == nil || goja.IsUndefined(jsonObj) {
		return nil, fmt.Errorf("JSON intrinsics unavailable")
	}
	m := jsonObj.ToObject(vm).Get(name)
	if m == nil {
		return nil, fmt.Errorf("JSON.%s unavailable", name)
	}
	fn, ok := goja.AssertFunction(m)
	if !ok {
		return nil, fmt.Errorf("JSON.%s is not a function", name)
	}
	return fn, nil
}
