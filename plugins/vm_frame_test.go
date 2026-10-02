// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package plugins

import (
	"sync"
	"testing"
	"time"
)

// newExtendedTestContext builds a PluginContext wired to the extended bridges
// (HTTP, storage, timers, UI) rooted at a temp dir.
func newExtendedTestContext(t *testing.T) PluginContext {
	t.Helper()
	return newExtendedContext(t, t.TempDir(), NewHTTPBridge())
}

// TestFrame_SameRuntimeDefersWhileOtherRuns pins the frame contract that keeps
// goja single-goroutine without reintroducing the app-wide freeze:
//
//   - a frame parked inside a bridge hop (goa.http.fetch) keeps the SAME
//     runtime closed, so a timer tick or hotkey on that runtime defers instead
//     of interleaving a second frame;
//   - a DIFFERENT runtime (another plugin) still runs, so a slow endpoint never
//     freezes the app.
//
// The same-runtime half is the regression: two overlapping frames corrupt
// goja's values, which is what surfaced in the z.ai coding-plan fetcher as
//
//	eval "codingPlanClaimOnce(zaiResetSurface())": TypeError:
//	 Cannot read property 'accessToken' of undefined at goaOAuthCredential
//
// — `tok` truthy for the `!tok` guard yet undefined for `tok.accessToken`, a
// state no single correct frame can produce. The old code dropped a
// process-wide lock inside the hop, which let the second frame in.
func TestFrame_SameRuntimeDefersWhileOtherRuns(t *testing.T) {
	ctx := newExtendedTestContext(t)
	bridge, release, frameDone := parkFrameInFetch(t, ctx, "hop-fixture")
	defer release()

	// Same runtime: refused, because the parked frame is live.
	if _, ok := bridge.tryEnterFrame(); ok {
		t.Fatal("a second frame entered the runtime while the first was parked in a bridge hop (two goja frames would overlap)")
	}

	assertOtherRuntimeRuns(t, ctx)
	release() // let the hop return
	assertFrameDrains(t, frameDone)
}

// parkFrameInFetch starts a frame that blocks inside goa.http.fetch and waits
// until it is parked in the hop. The returned release closes the hop (safe to
// call twice) and frameDone carries the frame's exit error.
func parkFrameInFetch(t *testing.T, ctx PluginContext, id string) (bridge *JSBridge, release func(), frameDone <-chan error) {
	t.Helper()
	parkedCh := make(chan struct{})
	releaseCh := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(releaseCh) }) }

	restore := setHTTPDo(func(b *HTTPBridge, req HTTPRequest) HTTPResponse {
		close(parkedCh)
		<-releaseCh
		return HTTPResponse{Status: 200, Body: `{"code":0}`, Headers: map[string]string{}}
	})
	t.Cleanup(restore)

	bridge = NewJSBridge(PluginDef{ID: id, Entry: "plugin.js", Permissions: []string{"network"}}, ctx)
	// The HTTP mock is global; a live timer here would fight it.
	if ctx.Extended.Scheduler != nil {
		ctx.Extended.Scheduler.Stop()
	}
	done := make(chan error, 1)
	go func() {
		leave := bridge.enterFrame()
		defer leave()
		_, err := bridge.vm.RunString(`goa.http.fetch("https://example.test/x", {});`)
		done <- err
	}()

	select {
	case <-parkedCh:
	case err := <-done:
		t.Fatalf("frame finished before parking: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("frame never reached the bridge hop")
	}
	return bridge, unblock, done
}

// assertOtherRuntimeRuns proves a DIFFERENT runtime runs while the first is
// parked — the anti-freeze half (a slow endpoint must not freeze the app).
func assertOtherRuntimeRuns(t *testing.T, ctx PluginContext) {
	t.Helper()
	other := NewJSBridge(PluginDef{ID: "other-fixture"}, ctx)
	done := make(chan error, 1)
	go func() {
		leave := other.enterFrame()
		defer leave()
		_, err := other.vm.RunString(`1 + 1`)
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("JS on the other runtime: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a frame on another runtime starved behind the parked frame — a slow endpoint would freeze the app")
	}
}

// assertFrameDrains waits for the parked frame to finish after its hop is
// released.
func assertFrameDrains(t *testing.T, frameDone <-chan error) {
	t.Helper()
	select {
	case err := <-frameDone:
		if err != nil {
			t.Fatalf("parked frame: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("parked frame did not finish after the hop was released")
	}
}

// TestFrame_TimerDefersThenRunsAfterDrain is the deferred-not-lost half: a
// one-shot tick that finds a live frame is re-armed, not dropped, so the
// quota plugin's cache prime still lands once the frame drains.
func TestFrame_TimerDefersThenRunsAfterDrain(t *testing.T) {
	ctx := newExtendedContext(t, t.TempDir(), NewHTTPBridge())
	sch := ctx.Extended.Scheduler
	defer sch.Stop()

	bridge := NewJSBridge(PluginDef{ID: "prime-fixture"}, ctx)
	ran := make(chan struct{})
	sch.SetTimeoutGated(bridge, func() { close(ran) }, 0)

	// Hold a frame so the tick defers, then drain it.
	leave := bridge.enterFrame()
	select {
	case <-ran:
		leave()
		t.Fatal("one-shot ran while a frame was live on the same runtime")
	case <-time.After(100 * time.Millisecond):
	}
	leave()

	select {
	case <-ran:
	case <-time.After(2 * time.Second):
		t.Fatal("deferred one-shot never ran after the frame drained (delayed, not lost)")
	}
}
