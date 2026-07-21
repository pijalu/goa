// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package plugins

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dop251/goja"
)

// testLogger captures plugin log output.
func testLogger() LoggerAPI {
	noop := func(string) {}
	return LoggerAPI{Info: noop, Warn: noop, Error: noop, Debug: noop}
}

// newExtendedContext builds a PluginContext with all extended bridges wired
// to real implementations rooted at dir.
func newExtendedContext(t *testing.T, dir string, httpB *HTTPBridge) PluginContext {
	t.Helper()
	st, err := NewStorageBridge(dir, "test")
	if err != nil {
		t.Fatal(err)
	}
	return PluginContext{
		Config: map[string]any{"providers": map[string]any{}},
		Logger: testLogger(),
		Extended: &ExtendContext{
			HTTP:      httpB,
			Storage:   st,
			Scheduler: NewScheduler(),
			Browser:   NewBrowserBridge(),
			Hotkeys:   NewHotkeyBridge(),
			UI:        NewUIBridge(),
			Output:    func(string) {},
			SessionUsage: func() map[string]any {
				return map[string]any{"input": 142300, "output": 28900, "cost": 0.89}
			},
		},
	}
}

// runJS executes src in a fresh bridge under the VM lock and returns the
// global result value.
func runJS(t *testing.T, ctx PluginContext, src string) *JSBridge {
	t.Helper()
	bridge := NewJSBridge(PluginDef{ID: "test", Entry: "plugin.js"}, ctx)
	unlock := lockVM()
	defer unlock()
	if _, err := bridge.vm.RunString(src); err != nil {
		t.Fatalf("RunString: %v", err)
	}
	return bridge
}

// goaResult reads a __result-style property the test JS assigned on the goa
// object (e.g. `goa.__result = ...`). Tests must read it off the goa object,
// not the global scope — `goa.x = v` never creates a JS global.
func goaResult(t *testing.T, bridge *JSBridge, prop string) goja.Value {
	t.Helper()
	unlock := lockVM()
	defer unlock()
	goaVal := bridge.vm.Get("goa")
	if goaVal == nil {
		t.Fatal("goa global not installed")
	}
	v := goaVal.ToObject(bridge.vm).Get(prop)
	if v == nil {
		t.Fatalf("goa.%s not set by test JS", prop)
	}
	return v
}

func TestJS_HTTPFetch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"plan":"pro","used":42}`))
	}))
	defer srv.Close()

	ctx := newExtendedContext(t, t.TempDir(), NewHTTPBridge())
	bridge := runJS(t, ctx, `
		var resp = goa.http.fetch("`+srv.URL+`", { method: "GET" });
		goa.__result = resp.status + ":" + resp.body;
	`)
	got := goaResult(t, bridge, "__result").String()
	if !strings.HasPrefix(got, "200:") || !strings.Contains(got, `"plan":"pro"`) {
		t.Fatalf("__result = %q", got)
	}
}

func TestJS_HTTPFetchErrorShape(t *testing.T) {
	ctx := newExtendedContext(t, t.TempDir(), NewHTTPBridge())
	bridge := runJS(t, ctx, `
		var resp = goa.http.fetch("http://example.com/x", {});
		goa.__result = resp.error;
	`)
	if got := goaResult(t, bridge, "__result").String(); !strings.Contains(got, "https required") {
		t.Fatalf("error = %q", got)
	}
}

func TestJS_StorageRoundTrip(t *testing.T) {
	ctx := newExtendedContext(t, t.TempDir(), NewHTTPBridge())
	bridge := runJS(t, ctx, `
		goa.storage.set("token", "abc123");
		goa.__result = goa.storage.get("token") + ":" + goa.storage.keys().length;
		goa.storage.delete("token");
		goa.__after = goa.storage.get("token");
	`)
	if got := goaResult(t, bridge, "__result").String(); got != "abc123:1" {
		t.Fatalf("__result = %q", got)
	}
	if got := goaResult(t, bridge, "__after").String(); got != "" {
		t.Fatalf("__after = %q, want empty", got)
	}
}

func TestJS_SetIntervalFires(t *testing.T) {
	ctx := newExtendedContext(t, t.TempDir(), NewHTTPBridge())
	var fired atomic.Int32
	done := make(chan struct{})
	ctx.Extended.Scheduler = NewScheduler()
	bridge := NewJSBridge(PluginDef{ID: "test"}, ctx)
	unlock := lockVM()
	bridge.vm.Set("__externalDone", func() {
		if fired.Add(1) >= 2 {
			close(done)
		}
	})
	if _, err := bridge.vm.RunString(`
		goa.setInterval(function() { __externalDone(); }, 250);
	`); err != nil {
		unlock()
		t.Fatal(err)
	}
	unlock()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("setInterval callback did not fire twice")
	}
	ctx.Extended.Scheduler.Stop()
}

func TestJS_SessionUsage(t *testing.T) {
	ctx := newExtendedContext(t, t.TempDir(), NewHTTPBridge())
	bridge := runJS(t, ctx, `
		var u = goa.sessionUsage();
		goa.__result = u.input + ":" + u.output;
	`)
	if got := goaResult(t, bridge, "__result").String(); got != "142300:28900" {
		t.Fatalf("__result = %q", got)
	}
}

func TestJS_RegisterSegmentAndHotkey(t *testing.T) {
	ctx := newExtendedContext(t, t.TempDir(), NewHTTPBridge())
	ui := ctx.Extended.UI
	hk := ctx.Extended.Hotkeys
	runJS(t, ctx, `
		goa.ui.addSegment({ id: "quota", priority: 10, render: function() { return "5h:42%"; } });
		goa.registerHotkey({ key: "q", ctrl: true, shift: true, description: "Refresh", handler: function() {} });
	`)
	segs := ui.Segments()
	if len(segs) != 1 || segs[0].ID != "quota" {
		t.Fatalf("Segments = %v", segs)
	}
	// Render acquires the VM lock itself (app render loop calls it unlocked);
	// do NOT wrap it in lockVM here — vmMu is not reentrant.
	rendered := segs[0].Render()
	if rendered != "5h:42%" {
		t.Fatalf("Render = %q", rendered)
	}
	defs := hk.Registered()
	if len(defs) != 1 || defs[0].KeyName() != "ctrl+shift+q" {
		t.Fatalf("Hotkeys = %v", defs)
	}
}

func TestJS_UIRefreshSegmentDoesNotBlock(t *testing.T) {
	ctx := newExtendedContext(t, t.TempDir(), NewHTTPBridge())
	ui := ctx.Extended.UI
	// Flood refresh requests beyond the channel buffer; must not block JS.
	runJS(t, ctx, `
		for (var i = 0; i < 100; i++) { goa.ui.refreshSegment("quota"); }
		goa.__result = "ok";
	`)
	select {
	case id := <-ui.RefreshRequests():
		if id != "quota" {
			t.Fatalf("refresh id = %q", id)
		}
	default:
		t.Fatal("expected at least one refresh request")
	}
}

// TestJS_HTTPFetchReleasesVMLock is the regression test for the startup /
// mid-session input freeze: a JS call blocked in goa.http.fetch (slow or
// hanging quota endpoint) must NOT keep the global VM lock, otherwise every
// other JS entry point — including the command loop's segment render — stalls
// behind the fetch and the input line freezes exactly when the quota segment
// lands (bugs.md "Start-up: delay matches the status bar quota appearing").
func TestJS_HTTPFetchReleasesVMLock(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock() // unblock the fetch if the test fails before the end
	restore := setHTTPDo(func(b *HTTPBridge, req HTTPRequest) HTTPResponse {
		close(entered)
		<-release // simulate a slow/hanging endpoint
		return HTTPResponse{Status: 200, Body: "ok"}
	})
	defer restore()

	ctx := newExtendedContext(t, t.TempDir(), NewHTTPBridge())
	bridge := NewJSBridge(PluginDef{ID: "test"}, ctx)

	// Goroutine A: run JS that blocks inside goa.http.fetch (mirrors the
	// quota prime firing on a scheduler timer).
	fetchDone := make(chan struct{})
	go func() {
		defer close(fetchDone)
		unlock := lockVM()
		defer unlock()
		_, _ = bridge.vm.RunString(`goa.http.fetch("https://example.com/quota");`)
	}()

	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("fetch never reached the HTTP hook")
	}

	// Goroutine B: while A is blocked in HTTP, the command loop's segment
	// render must be able to acquire the VM lock and run JS. Before the fix
	// this blocked until the fetch returned — the observed input freeze.
	rendered := make(chan struct{})
	go func() {
		defer close(rendered)
		unlock := lockVM()
		defer unlock()
		_, _ = bridge.vm.RunString(`1 + 1`)
	}()

	select {
	case <-rendered:
		// VM stayed responsive while HTTP was in flight — the fix works.
	case <-time.After(500 * time.Millisecond):
		t.Fatal("VM lock held across blocking goa.http.fetch — another JS entry point starved")
	}

	// Let the fetch finish so goroutine A can exit cleanly.
	unblock()
	restore()
	select {
	case <-fetchDone:
	case <-time.After(3 * time.Second):
		t.Fatal("fetch goroutine did not finish after HTTP release")
	}
}

// TestJS_GojaFunctionInterop guards the goja AssertFunction usage in timers.
func TestJS_GojaFunctionInterop(t *testing.T) {
	vm := goja.New()
	v, err := vm.RunString(`(function() { return 7; })`)
	if err != nil {
		t.Fatal(err)
	}
	fn, ok := goja.AssertFunction(v)
	if !ok {
		t.Fatal("AssertFunction failed")
	}
	res, err := fn(goja.Undefined())
	if err != nil {
		t.Fatal(err)
	}
	if res.ToInteger() != 7 {
		t.Fatalf("result = %v", res)
	}
}
