// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package plugins

import (
	"fmt"
	"strconv"
	"time"

	"github.com/dop251/goja"
)

// ExtendContext carries the optional subsystems a plugin may use beyond the
// core PluginContext. Nil fields disable the matching goa.* API so plugins
// degrade gracefully (they receive an "api unavailable" string, matching the
// existing handler-not-configured convention).
type ExtendContext struct {
	HTTP     *HTTPBridge
	Storage  *StorageBridge
	Scheduler *Scheduler
	Browser  *BrowserBridge
	Hotkeys  *HotkeyBridge
	UI       *UIBridge
	// Output writes a user-visible message to the chat viewport.
	Output func(msg string)
	// SessionUsage returns cumulative token stats for the local/inferred
	// quota fetcher. Nil disables goa.sessionUsage().
	SessionUsage func() map[string]any
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
		resp := httpDo(httpB, req)
		return b.vm.ToValue(httpResponseToMap(resp))
	})
	goaObj.Set("http", httpObj)
}

// httpDo performs an HTTP request. It's a variable so tests can substitute a
// mock without network access; production code assigns the real Do.
var httpDo = func(b *HTTPBridge, req HTTPRequest) HTTPResponse {
	return b.Do(req)
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
				def.Handler = func() {
					unlock := lockVM()
					defer unlock()
					if _, err := fn(goja.Undefined()); err != nil {
						b.ctx.Logger.Error(fmt.Sprintf("hotkey %s failed: %v", def.KeyName(), err))
					}
				}
			}
		}
		hk.Register(def)
		return b.vm.ToValue("hotkey registered: " + def.KeyName())
	})
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
				def.Render = func() string {
					res, err := fn(goja.Undefined())
					if err != nil {
						return ""
					}
					return res.String()
				}
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
	goaObj.Set("ui", uiObj)
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
