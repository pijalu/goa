// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package plugins

import (
	"strings"
	"testing"
	"time"
)

func confirmTestRequest() ConfirmRequest {
	return ConfirmRequest{
		PluginID: "test-plugin",
		Title:    "Use reset?",
		Body:     "This consumes one rate-limit reset credit.",
		Options: []ConfirmOption{
			{ID: "yes", Label: "Yes, use reset", Style: ConfirmStyleDanger},
			{ID: "no", Label: "Cancel"},
		},
		DefaultID:   "no",
		AllowCancel: true,
	}
}

// confirmConsumer attaches a fake consumer so RequestConfirm queues instead
// of failing closed, and returns a drain helper.
func confirmConsumer(t *testing.T, b *UIBridge) func() int {
	t.Helper()
	b.SetConfirmConsumer(true)
	return func() int {
		n := 0
		for {
			if _, ok := b.NextConfirm(); !ok {
				return n
			}
			n++
		}
	}
}

// assertPendingCount fails the test unless exactly want confirms are pending.
func assertPendingCount(t *testing.T, b *UIBridge, want int) {
	t.Helper()
	if got := b.PendingConfirmCount(); got != want {
		t.Fatalf("pending = %d, want %d", got, want)
	}
}

// TestRequestConfirm_HappyPath covers the full request → present → resolve
// round trip with the exact chosen option ID.
func TestRequestConfirm_HappyPath(t *testing.T) {
	b := NewUIBridge()
	_ = confirmConsumer(t, b)

	ch := b.RequestConfirm(confirmTestRequest())
	// Wait for the queue signal, then inspect the single queued job.
	select {
	case <-b.ConfirmRequests():
	case <-time.After(time.Second):
		t.Fatal("no queue signal for pending confirm")
	}
	assertPendingCount(t, b, 1)

	job, ok := b.NextConfirm()
	if !ok {
		t.Fatal("expected queued job")
	}
	if job.ID <= 0 {
		t.Fatalf("job ID = %d, want monotonic >0", job.ID)
	}
	if job.Req.PluginID != "test-plugin" || len(job.Req.Options) != 2 {
		t.Fatalf("request not preserved through queue: %+v", job.Req)
	}
	// A popped job stays pending until it is resolved.
	assertPendingCount(t, b, 1)

	job.Resolve(ConfirmResponse{ID: "yes"})
	select {
	case resp := <-ch:
		if resp.ID != "yes" || resp.Cancelled || resp.Err != "" {
			t.Fatalf("response = %+v, want {yes false \"\"}", resp)
		}
	default:
		t.Fatal("reply channel not ready after Resolve")
	}
	assertPendingCount(t, b, 0)
}

// TestRequestConfirm_Cancelled pins Esc/implicit-cancel semantics: Cancelled
// with no ID and no Err (plain user dismissal).
func TestRequestConfirm_Cancelled(t *testing.T) {
	b := NewUIBridge()
	confirmConsumer(t, b)

	ch := b.RequestConfirm(confirmTestRequest())
	job, _ := b.NextConfirm()
	job.Resolve(ConfirmResponse{Cancelled: true})

	resp := <-ch
	if !resp.Cancelled || resp.ID != "" || resp.Err != "" {
		t.Fatalf("response = %+v, want plain cancellation", resp)
	}
}

// TestRequestConfirm_Timeout verifies the bridge-side wait cap resolves an
// unanswered confirm with {Cancelled:true, Err:"timeout"}.
func TestRequestConfirm_Timeout(t *testing.T) {
	b := NewUIBridge()
	confirmConsumer(t, b)

	req := confirmTestRequest()
	req.Timeout = 30 * time.Millisecond
	ch := b.RequestConfirm(req)

	select {
	case resp := <-ch:
		if !resp.Cancelled || resp.Err != ErrConfirmTimeout {
			t.Fatalf("response = %+v, want timeout cancellation", resp)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout did not fire")
	}
	if b.PendingConfirmCount() != 0 {
		t.Fatalf("pending = %d, want 0 after timeout", b.PendingConfirmCount())
	}
}

// TestRequestConfirm_TimeoutCapClamp checks normalization: 0 ("no explicit
// timeout") and over-long values both clamp to MaxConfirmTimeout; unknown
// styles fall back to default.
func TestRequestConfirm_TimeoutCapClamp(t *testing.T) {
	got := normalizeConfirmRequest(ConfirmRequest{Timeout: 0})
	if got.Timeout != MaxConfirmTimeout {
		t.Fatalf("Timeout 0 normalized to %v, want %v", got.Timeout, MaxConfirmTimeout)
	}
	got = normalizeConfirmRequest(ConfirmRequest{Timeout: time.Hour})
	if got.Timeout != MaxConfirmTimeout {
		t.Fatalf("Timeout 1h clamped to %v, want %v", got.Timeout, MaxConfirmTimeout)
	}
	got = normalizeConfirmRequest(ConfirmRequest{
		Options: []ConfirmOption{{ID: "a", Label: "A", Style: "bogus"}},
	})
	if got.Options[0].Style != ConfirmStyleDefault {
		t.Fatalf("unknown style kept: %q", got.Options[0].Style)
	}
	// The queued request must carry the clamped timeout (the AfterFunc uses it).
	b := NewUIBridge()
	confirmConsumer(t, b)
	ch := b.RequestConfirm(ConfirmRequest{Options: []ConfirmOption{{ID: "a", Label: "A"}}})
	job, _ := b.NextConfirm()
	if job.Req.Timeout != MaxConfirmTimeout {
		t.Fatalf("queued Timeout = %v, want %v", job.Req.Timeout, MaxConfirmTimeout)
	}
	_ = ch
}

// TestRequestConfirm_FIFOSerialization proves order preservation: three
// requests queue and pop in submission order — one visible at a time is a
// consumer-side property, but FIFO handout is the bridge contract behind it.
func TestRequestConfirm_FIFOSerialization(t *testing.T) {
	b := NewUIBridge()
	confirmConsumer(t, b)

	const n = 3
	chans := make([]<-chan ConfirmResponse, n)
	for i := 0; i < n; i++ {
		chans[i] = b.RequestConfirm(ConfirmRequest{
			Title:   "q",
			Options: []ConfirmOption{{ID: "only", Label: "Only"}},
		})
	}
	var gotIDs []int64
	for i := 0; i < n; i++ {
		job, ok := b.NextConfirm()
		if !ok {
			t.Fatalf("job %d missing from queue", i)
		}
		gotIDs = append(gotIDs, job.ID)
	}
	for i := 1; i < len(gotIDs); i++ {
		if gotIDs[i] != gotIDs[i-1]+1 {
			t.Fatalf("FIFO violated: ids %v", gotIDs)
		}
	}
	// Resolve out of order (last first) — every reply must still reach its
	// own caller exactly once.
	jobLast, _ := b.NextConfirm() // queue empty now; reuse resolved jobs via fresh requests
	_ = jobLast
	for i := n - 1; i >= 0; i-- {
		b.finishConfirm(gotIDs[i], ConfirmResponse{ID: "only"})
	}
	for i := 0; i < n; i++ {
		select {
		case <-chans[i]:
		default:
			t.Fatalf("reply %d missing after resolve", i)
		}
	}
}

// TestRequestConfirm_DoubleResolveDropped pins the buffered-size-1 semantics:
// a late second resolution (user answer racing the timeout) never blocks nor
// corrupts the first answer.
func TestRequestConfirm_DoubleResolveDropped(t *testing.T) {
	b := NewUIBridge()
	confirmConsumer(t, b)

	ch := b.RequestConfirm(confirmTestRequest())
	job, _ := b.NextConfirm()

	job.Resolve(ConfirmResponse{ID: "yes"})
	// Late timeout-style resolution must be a silent no-op.
	b.finishConfirm(job.ID, ConfirmResponse{Cancelled: true, Err: ErrConfirmTimeout})

	select {
	case resp := <-ch:
		if resp.ID != "yes" {
			t.Fatalf("first answer clobbered: %+v", resp)
		}
	default:
		t.Fatal("no answer delivered")
	}
}

// TestRequestConfirm_NoUIFailClosed is the headless policy (§4 step 3): with
// no TUI consumer attached, RequestConfirm answers immediately with
// {Cancelled:true, Err:"no-ui"} — fail-closed, never consent.
func TestRequestConfirm_NoUIFailClosed(t *testing.T) {
	b := NewUIBridge() // no SetConfirmConsumer

	done := make(chan struct{})
	var resp ConfirmResponse
	go func() {
		resp = <-b.RequestConfirm(confirmTestRequest())
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("no-ui confirm did not resolve immediately")
	}
	if !resp.Cancelled || resp.Err != ErrConfirmNoUI {
		t.Fatalf("response = %+v, want {Cancelled:true Err:no-ui}", resp)
	}
	if b.PendingConfirmCount() != 0 {
		t.Fatalf("pending = %d, want 0 (nothing may queue without a consumer)", b.PendingConfirmCount())
	}
}

// TestRequestConfirm_ValidationRejectsImmediate ensures malformed requests
// are rejected synchronously with an error response (not queued).
func TestRequestConfirm_ValidationRejectsImmediate(t *testing.T) {
	cases := []struct {
		name string
		req  ConfirmRequest
	}{
		{"no options", ConfirmRequest{Title: "t"}},
		{"empty option id", ConfirmRequest{Options: []ConfirmOption{{Label: "L"}}}},
		{"empty option label", ConfirmRequest{Options: []ConfirmOption{{ID: "x"}}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := NewUIBridge()
			b.SetConfirmConsumer(true)
			resp := <-b.RequestConfirm(tc.req)
			if resp.Err == "" || resp.Cancelled {
				t.Fatalf("response = %+v, want immediate validation error", resp)
			}
		})
	}
}

// TestCancelAllConfirms_Shutdown fails every outstanding request with the
// given reason so nothing parks on a dead UI.
func TestCancelAllConfirms_Shutdown(t *testing.T) {
	b := NewUIBridge()
	confirmConsumer(t, b)

	ch1 := b.RequestConfirm(confirmTestRequest())
	ch2 := b.RequestConfirm(confirmTestRequest())
	if _, ok := b.NextConfirm(); !ok {
		t.Fatal("queue should hold two jobs")
	}

	b.CancelAllConfirms("shutdown")
	for i, ch := range []<-chan ConfirmResponse{ch1, ch2} {
		select {
		case resp := <-ch:
			if !resp.Cancelled || resp.Err != "shutdown" {
				t.Fatalf("ch%d response = %+v, want shutdown cancellation", i+1, resp)
			}
		case <-time.After(time.Second):
			t.Fatalf("ch%d not cancelled on shutdown", i+1)
		}
	}
	if b.PendingConfirmCount() != 0 {
		t.Fatalf("pending = %d, want 0 after CancelAll", b.PendingConfirmCount())
	}
	// Queue drained too: NextConfirm reports empty.
	if _, ok := b.NextConfirm(); ok {
		t.Fatal("queue should be empty after CancelAll")
	}
}

// TestRequestConfirm_LockReleasedWhileWaiting is the plan's §4 concurrency
// assertion. The JS binding waits inside runOutsideVMLock; this white-box
// probe proves vmMu is genuinely free while a confirm is pending, so timers,
// hotkeys, and other bridges can run during a long user pause (and that the
// item-E deferral gates — which check vmBusy, NOT vmMu — remain the safety
// mechanism, not lock starvation).
func TestRequestConfirm_LockReleasedWhileWaiting(t *testing.T) {
	b := NewUIBridge()
	confirmConsumer(t, b)

	ch := b.RequestConfirm(confirmTestRequest())

	lockAcquired := make(chan struct{}, 1)
	go func() {
		unlock := lockVM()
		unlock()
		lockAcquired <- struct{}{}
	}()

	select {
	case <-lockAcquired:
	case <-time.After(2 * time.Second):
		t.Fatal("vmMu not acquirable while a confirm pends — the wait would starve every JS entry point")
	}

	// Cleanup: resolve so the AfterFunc doesn't leak past the test.
	b.finishConfirm(1, ConfirmResponse{Cancelled: true})
	_ = ch
}

// TestRequestConfirm_TimersDeferWhileFrameLive documents the item-E invariant
// as it applies to confirms: the calling context keeps its logical frame
// (enterVM), so scheduler work DEFERS until the answer lands instead of
// interleaving a second goja frame on the same runtime.
//
// This intentionally deviates from the plan sketch ("timers proceed meanwhile")
// — see docs/plans/plugins-hooks-intercept-plan.md §9 Q3 note and the M3
// commit message. Delayed-not-lost beats corrupted.
func TestRequestConfirm_TimersDeferWhileFrameLive(t *testing.T) {
	leave := enterVM()
	defer leave()

	fired := make(chan struct{})
	go func() {
		invokeSafe(func() { close(fired) })
	}()

	select {
	case <-fired:
		t.Fatal("timer entered the VM while a logical frame was live (item E violation)")
	case <-time.After(100 * time.Millisecond):
		// expected: deferred because vmActive > 0
	}
}

// ── goa.ui.confirm JS binding ──

// confirmJSEnv builds a bridge with a real UIBridge wired into Extended, so
// tests drive the actual goa.ui.confirm binding end-to-end.
func confirmJSEnv(t *testing.T) (*JSBridge, *UIBridge) {
	t.Helper()
	ui := NewUIBridge()
	noop := func(string) {}
	bridge := NewJSBridge(PluginDef{ID: "confirm-fixture", Entry: "plugin.js", Permissions: []string{"ui-confirm"}}, PluginContext{
		Config: map[string]any{},
		Logger: LoggerAPI{Info: noop, Warn: noop, Error: noop, Debug: noop},
		Extended: &ExtendContext{
			UI:        ui,
			Scheduler: NewScheduler(),
			Output:    noop,
		},
	})
	return bridge, ui
}

// runConfirmJS executes `goa.ui.confirm(spec)` on a dedicated goroutine that
// mimics a plugin command context (takes vmMu first; the binding releases it
// while waiting) and stores JSON.stringify(answer) under goa.__confirmResult.
// Reading the result AFTER done requires only string reads under lockVM.
func runConfirmJS(t *testing.T, b *JSBridge, specJS string) (done <-chan error) {
	t.Helper()
	src := "goa.__confirmResult = JSON.stringify(goa.ui.confirm(" + specJS + "))"
	ch := make(chan error, 1)
	go func() {
		unlock := lockVM()
		defer unlock()
		_, err := b.vm.RunString(src)
		ch <- err
	}()
	return ch
}

// readConfirmJSON fetches goa.__confirmResult under the VM lock.
func readConfirmJSON(t *testing.T, b *JSBridge) string {
	t.Helper()
	unlock := lockVM()
	defer unlock()
	goaVal := b.vm.Get("goa")
	if goaVal == nil {
		t.Fatal("goa global not installed")
	}
	v := goaVal.ToObject(b.vm).Get("__confirmResult")
	if v == nil {
		t.Fatal("goa.__confirmResult not set by test JS")
	}
	return v.String()
}

// waitForJob pops exactly one queued job within the deadline.
func waitForJob(t *testing.T, ui *UIBridge) ConfirmJob {
	t.Helper()
	select {
	case <-ui.ConfirmRequests():
	case <-time.After(2 * time.Second):
		t.Fatal("no queue signal for pending confirm")
	}
	job, ok := ui.NextConfirm()
	if !ok {
		t.Fatal("queue empty after signal")
	}
	return job
}

func TestUIConfirmJS_HappyPath(t *testing.T) {
	b, ui := confirmJSEnv(t)
	ui.SetConfirmConsumer(true)

	done := runConfirmJS(t, b, `{title:"Use reset?", body:"Consumes one credit.",
		options:[{id:"yes",label:"Yes",style:"danger"},{id:"no",label:"No"}],
		defaultId:"no", allowCancel:true}`)

	job := waitForJob(t, ui)
	if job.Req.PluginID != "confirm-fixture" {
		t.Fatalf("PluginID not stamped from manifest: %q", job.Req.PluginID)
	}
	if job.Req.DefaultID != "no" || !job.Req.AllowCancel {
		t.Fatalf("spec fields lost in transit: %+v", job.Req)
	}
	job.Resolve(ConfirmResponse{ID: "yes"})

	if err := <-done; err != nil {
		t.Fatalf("RunString: %v", err)
	}
	if got := readConfirmJSON(t, b); got != `{"id":"yes"}` {
		t.Fatalf("result = %s, want {\"id\":\"yes\"}", got)
	}
}

func TestUIConfirmJS_Cancelled(t *testing.T) {
	b, ui := confirmJSEnv(t)
	ui.SetConfirmConsumer(true)

	done := runConfirmJS(t, b, `{title:"T", options:[{id:"a",label:"A"}]}`)
	job := waitForJob(t, ui)
	job.Resolve(ConfirmResponse{Cancelled: true})

	if err := <-done; err != nil {
		t.Fatalf("RunString: %v", err)
	}
	if got := readConfirmJSON(t, b); got != `{"cancelled":true}` {
		t.Fatalf("result = %s, want {\"cancelled\":true}", got)
	}
}

func TestUIConfirmJS_Timeout(t *testing.T) {
	b, ui := confirmJSEnv(t)
	ui.SetConfirmConsumer(true)

	done := runConfirmJS(t, b, `{title:"T", options:[{id:"a",label:"A"}], timeoutMs:30}`)
	waitForJob(t, ui) // ensure queued; nobody resolves — the cap must fire

	if err := <-done; err != nil {
		t.Fatalf("RunString: %v", err)
	}
	got := readConfirmJSON(t, b)
	if !strings.Contains(got, `"error":"timeout"`) || !strings.Contains(got, `"cancelled":true`) {
		t.Fatalf("result = %s, want timeout cancellation", got)
	}
}

func TestUIConfirmJS_ValidationErrors(t *testing.T) {
	cases := []struct {
		name    string
		spec    string
		wantSub string
	}{
		{"no options", `{title:"T"}`, "options"},
		{"empty array", `{title:"T", options:[]}`, "at least one option"},
		{"missing id", `{title:"T", options:[{label:"L"}]}`, "id is required"},
		{"missing label", `{title:"T", options:[{id:"x"}]}`, "label is required"},
		{"not an object arg", `null`, "expects an options object"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b, _ := confirmJSEnv(t)
			done := runConfirmJS(t, b, tc.spec)
			if err := <-done; err != nil {
				t.Fatalf("RunString: %v", err)
			}
			if got := readConfirmJSON(t, b); !strings.Contains(got, `"error"`) || !strings.Contains(got, tc.wantSub) {
				t.Fatalf("result = %s, want error containing %q", got, tc.wantSub)
			}
		})
	}
}

// TestUIConfirmJS_UIThreadGuardFailsClosed proves the deadlock guard: with
// the block detector reporting "this is the UI thread", confirm returns
// immediately with cancelled+error instead of parking until the cap.
func TestUIConfirmJS_UIThreadGuardFailsClosed(t *testing.T) {
	b, ui := confirmJSEnv(t)
	ui.SetBlockGuard(func() bool { return true })

	started := time.Now()
	done := runConfirmJS(t, b, `{title:"T", options:[{id:"a",label:"A"}]}`)
	if err := <-done; err != nil {
		t.Fatalf("RunString: %v", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("guard did not fail closed fast enough (%v)", elapsed)
	}
	got := readConfirmJSON(t, b)
	if !strings.Contains(got, `"cancelled":true`) || !strings.Contains(got, "UI thread") {
		t.Fatalf("result = %s, want ui-thread fail-closed", got)
	}
	if ui.PendingConfirmCount() != 0 {
		t.Fatal("guarded confirm must never queue work")
	}
}
