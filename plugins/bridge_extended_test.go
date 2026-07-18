// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package plugins

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// --- HTTP bridge ---------------------------------------------------------

func TestHTTPBridge_Get(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Plan", "pro")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"used":42}`))
	}))
	defer srv.Close()

	b := NewHTTPBridge()
	resp := b.Do(HTTPRequest{URL: srv.URL, Method: "GET"})
	if resp.Error != "" {
		t.Fatalf("unexpected error: %s", resp.Error)
	}
	if resp.Status != 200 {
		t.Fatalf("status = %d, want 200", resp.Status)
	}
	if !strings.Contains(resp.Body, `"used":42`) {
		t.Fatalf("body = %q", resp.Body)
	}
	if resp.Headers["x-plan"] != "pro" {
		t.Fatalf("headers = %v", resp.Headers)
	}
}

func TestHTTPBridge_PostJSONBody(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, r.ContentLength)
		r.Body.Read(buf)
		gotBody = string(buf)
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	b := NewHTTPBridge()
	resp := b.Do(HTTPRequest{
		URL:     srv.URL,
		Method:  "POST",
		Headers: map[string]string{"Content-Type": "application/json"},
		Body:    JSONBody(map[string]any{"grant_type": "refresh_token"}),
	})
	if resp.Status != 201 {
		t.Fatalf("status = %d", resp.Status)
	}
	var decoded map[string]string
	if err := json.Unmarshal([]byte(gotBody), &decoded); err != nil {
		t.Fatalf("body not JSON: %v (%q)", err, gotBody)
	}
	if decoded["grant_type"] != "refresh_token" {
		t.Fatalf("body = %v", decoded)
	}
}

func TestHTTPBridge_RejectsInsecureHTTP(t *testing.T) {
	b := NewHTTPBridge()
	resp := b.Do(HTTPRequest{URL: "http://example.com/api"})
	if resp.Error == "" {
		t.Fatal("expected refusal of plain http for non-loopback host")
	}
	if !strings.Contains(resp.Error, "https required") {
		t.Fatalf("error = %q", resp.Error)
	}
}

func TestHTTPBridge_AllowsLoopbackHTTP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	}))
	defer srv.Close()
	// httptest uses 127.0.0.1 — loopback, so plain http is permitted.
	b := NewHTTPBridge()
	resp := b.Do(HTTPRequest{URL: srv.URL})
	if resp.Error != "" {
		t.Fatalf("loopback http should be allowed, got %s", resp.Error)
	}
	if resp.Body != "ok" {
		t.Fatalf("body = %q", resp.Body)
	}
}

func TestHTTPBridge_RejectsBadScheme(t *testing.T) {
	b := NewHTTPBridge()
	resp := b.Do(HTTPRequest{URL: "ftp://example.com/x"})
	if !strings.Contains(resp.Error, "unsupported url scheme") {
		t.Fatalf("error = %q", resp.Error)
	}
}

func TestHTTPBridge_Timeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(500 * time.Millisecond)
	}))
	defer srv.Close()
	b := NewHTTPBridge()
	start := time.Now()
	resp := b.Do(HTTPRequest{URL: srv.URL, Timeout: 50 * time.Millisecond})
	if resp.Error == "" {
		t.Fatal("expected timeout error")
	}
	if time.Since(start) > 300*time.Millisecond {
		t.Fatalf("timeout not enforced, took %v", time.Since(start))
	}
}

func TestIsLoopbackHost(t *testing.T) {
	cases := map[string]bool{
		"localhost":   true,
		"LOCALHOST":   true,
		"127.0.0.1":   true,
		"::1":         true,
		"example.com": false,
		"10.0.0.5":    false,
	}
	for host, want := range cases {
		if got := isLoopbackHost(host); got != want {
			t.Errorf("isLoopbackHost(%q) = %v, want %v", host, got, want)
		}
	}
}

// --- Storage bridge ------------------------------------------------------

func TestStorageBridge_SetGetDelete(t *testing.T) {
	st, err := NewStorageBridge(t.TempDir(), "quota")
	if err != nil {
		t.Fatal(err)
	}
	if got := st.Get("missing"); got != "" {
		t.Fatalf("Get(missing) = %q, want empty", got)
	}
	if err := st.Set("opencode.access_token", "tok123"); err != nil {
		t.Fatal(err)
	}
	if got := st.Get("opencode.access_token"); got != "tok123" {
		t.Fatalf("Get = %q", got)
	}
	keys := st.Keys()
	if len(keys) != 1 || keys[0] != "opencode.access_token" {
		t.Fatalf("Keys = %v", keys)
	}
	if err := st.Delete("opencode.access_token"); err != nil {
		t.Fatal(err)
	}
	if got := st.Get("opencode.access_token"); got != "" {
		t.Fatalf("after delete Get = %q", got)
	}
	// Delete of absent key is not an error.
	if err := st.Delete("nope"); err != nil {
		t.Fatal(err)
	}
}

func TestStorageBridge_PersistsAcrossInstances(t *testing.T) {
	dir := t.TempDir()
	st1, _ := NewStorageBridge(dir, "quota")
	st1.Set("k", "v")
	st2, _ := NewStorageBridge(dir, "quota")
	if got := st2.Get("k"); got != "v" {
		t.Fatalf("reload Get = %q, want v", got)
	}
}

func TestStorageBridge_FilePermissions(t *testing.T) {
	dir := t.TempDir()
	st, _ := NewStorageBridge(dir, "quota")
	st.Set("token", "secret")
	info, err := os.Stat(st.path())
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("storage file perm = %o, want 600 (holds tokens)", perm)
	}
}

func TestStorageBridge_CorruptFileRecovers(t *testing.T) {
	dir := t.TempDir()
	st, _ := NewStorageBridge(dir, "quota")
	if err := os.WriteFile(st.path(), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Corrupt load yields empty, not a crash; Set overwrites cleanly.
	if got := st.Get("k"); got != "" {
		t.Fatalf("corrupt Get = %q", got)
	}
	if err := st.Set("k", "v"); err != nil {
		t.Fatal(err)
	}
	if got := st.Get("k"); got != "v" {
		t.Fatalf("Get = %q", got)
	}
}

func TestStorageBridge_IsolatedPerPlugin(t *testing.T) {
	dir := t.TempDir()
	a, _ := NewStorageBridge(dir, "plugin-a")
	b, _ := NewStorageBridge(dir, "plugin-b")
	a.Set("k", "a-value")
	if got := b.Get("k"); got != "" {
		t.Fatalf("plugin-b sees plugin-a key: %q", got)
	}
}

// --- Scheduler -----------------------------------------------------------

func TestScheduler_IntervalFires(t *testing.T) {
	s := NewScheduler()
	defer s.Stop()
	var count atomic.Int32
	done := make(chan struct{})
	s.SetInterval(func() {
		if count.Add(1) >= 3 {
			close(done)
		}
	}, minInterval)
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("interval callback did not fire 3 times")
	}
}

func TestScheduler_TimeoutFiresOnce(t *testing.T) {
	s := NewScheduler()
	defer s.Stop()
	var count atomic.Int32
	s.SetTimeout(func() { count.Add(1) }, minInterval)
	time.Sleep(3 * minInterval)
	if got := count.Load(); got != 1 {
		t.Fatalf("timeout fired %d times, want 1", got)
	}
}

func TestScheduler_ClearStopsTimer(t *testing.T) {
	s := NewScheduler()
	defer s.Stop()
	var count atomic.Int32
	id := s.SetInterval(func() { count.Add(1) }, minInterval)
	s.Clear(id)
	if s.Count() != 0 {
		t.Fatalf("Count = %d, want 0", s.Count())
	}
	time.Sleep(2 * minInterval)
	if got := count.Load(); got != 0 {
		t.Fatalf("cleared timer fired %d times", got)
	}
}

func TestScheduler_ClampsMinimumInterval(t *testing.T) {
	s := NewScheduler()
	defer s.Stop()
	// 0ms must be clamped to minInterval, not busy-spin.
	id := s.SetInterval(func() {}, 0)
	if s.timers[id].period < minInterval {
		t.Fatalf("period = %v, want >= %v", s.timers[id].period, minInterval)
	}
}

func TestScheduler_StopCancelsAll(t *testing.T) {
	s := NewScheduler()
	s.SetInterval(func() {}, minInterval)
	s.SetInterval(func() {}, minInterval)
	s.Stop()
	if s.Count() != 0 {
		t.Fatalf("Count after Stop = %d", s.Count())
	}
}

func TestScheduler_CallbackPanicContained(t *testing.T) {
	s := NewScheduler()
	defer s.Stop()
	fired := make(chan struct{})
	s.SetTimeout(func() {
		defer close(fired)
		panic("boom")
	}, minInterval)
	select {
	case <-fired:
	case <-time.After(time.Second):
		t.Fatal("panicking callback did not run")
	}
	// Scheduler still usable after a panicking callback.
	s.SetTimeout(func() {}, minInterval)
	if s.Count() == 0 {
		t.Fatal("scheduler dead after panic")
	}
}

// --- Browser bridge ------------------------------------------------------

func TestBrowserBridge_RejectsNonHTTP(t *testing.T) {
	b := NewBrowserBridge()
	if err := b.OpenURL("file:///etc/passwd"); err == nil {
		t.Fatal("expected refusal of file:// url")
	}
	if err := b.OpenURL("javascript:alert(1)"); err == nil {
		t.Fatal("expected refusal of javascript: url")
	}
}

func TestBrowserBridge_AcceptsHTTPS(t *testing.T) {
	b := NewBrowserBridge()
	var opened string
	b.open = func(u string) error { opened = u; return nil }
	if err := b.OpenURL("https://console.opencode.ai/activate"); err != nil {
		t.Fatal(err)
	}
	if opened != "https://console.opencode.ai/activate" {
		t.Fatalf("opened = %q", opened)
	}
}

// --- Hotkey bridge -------------------------------------------------------

func TestHotkeyBridge_RegisterAndKeyName(t *testing.T) {
	b := NewHotkeyBridge()
	b.Register(HotkeyDef{Key: "q", Ctrl: true, Shift: true, Description: "Refresh quota"})
	defs := b.Registered()
	if len(defs) != 1 {
		t.Fatalf("Registered = %d, want 1", len(defs))
	}
	if got := defs[0].KeyName(); got != "ctrl+shift+q" {
		t.Fatalf("KeyName = %q", got)
	}
}

func TestHotkeyDef_KeyNameOrdering(t *testing.T) {
	cases := []struct {
		def  HotkeyDef
		want string
	}{
		{HotkeyDef{Key: "r", Ctrl: true}, "ctrl+r"},
		{HotkeyDef{Key: "q", Ctrl: true, Shift: true}, "ctrl+shift+q"},
		{HotkeyDef{Key: "f5"}, "f5"},
		{HotkeyDef{Key: "x", Alt: true}, "alt+x"},
	}
	for _, c := range cases {
		if got := c.def.KeyName(); got != c.want {
			t.Errorf("KeyName() = %q, want %q", got, c.want)
		}
	}
}
