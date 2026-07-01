// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeSessionProvider struct {
	mu sync.RWMutex
	id string
}

func (f *fakeSessionProvider) SessionID() string {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.id
}

func (f *fakeSessionProvider) setID(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.id = id
}

func TestCachePutAndGet(t *testing.T) {
	dir := t.TempDir()
	provider := &fakeSessionProvider{id: "session-abc"}

	cache := NewWebFetchCache(
		filepath.Join(dir, ".goa", "cache", "webfetch"),
		1*time.Hour,
		10,
		1024*1024,
		1*time.Hour,
		provider,
	)
	defer cache.Close()

	ctx := context.Background()
	url := "https://example.com"
	if _, ok, _ := cache.Get(ctx, url); ok {
		t.Fatal("expected cache miss")
	}

	if err := cache.Put(ctx, url, []byte("hello"), WebFetchMeta{}); err != nil {
		t.Fatalf("Put error: %v", err)
	}

	entry, ok, err := cache.Get(ctx, url)
	if err != nil {
		t.Fatalf("Get error: %v", err)
	}
	if !ok {
		t.Fatal("expected cache hit")
	}
	if string(entry.Markdown) != "hello" {
		t.Errorf("markdown = %q, want hello", string(entry.Markdown))
	}
	if entry.Meta.SessionID != provider.SessionID() {
		t.Errorf("session id = %q, want %q", entry.Meta.SessionID, provider.SessionID())
	}
}

func TestCacheSessionIsolation(t *testing.T) {
	dir := t.TempDir()
	provider := &fakeSessionProvider{id: "session-1"}

	cache := NewWebFetchCache(
		filepath.Join(dir, ".goa", "cache", "webfetch"),
		1*time.Hour,
		10,
		1024*1024,
		1*time.Hour,
		provider,
	)
	defer cache.Close()

	ctx := context.Background()
	url := "https://example.com"
	if err := cache.Put(ctx, url, []byte("session1"), WebFetchMeta{}); err != nil {
		t.Fatalf("Put error: %v", err)
	}

	// New session should not see the entry.
	provider.setID("session-2")
	_, ok, _ := cache.Get(ctx, url)
	if ok {
		t.Fatal("expected cache miss for different session")
	}
}

func TestCacheExpiry(t *testing.T) {
	dir := t.TempDir()
	provider := &fakeSessionProvider{id: "session-abc"}

	cache := NewWebFetchCache(
		filepath.Join(dir, ".goa", "cache", "webfetch"),
		1*time.Nanosecond,
		10,
		1024*1024,
		1*time.Hour,
		provider,
	)
	defer cache.Close()

	ctx := context.Background()
	url := "https://example.com"
	if err := cache.Put(ctx, url, []byte("old"), WebFetchMeta{}); err != nil {
		t.Fatalf("Put error: %v", err)
	}
	time.Sleep(10 * time.Millisecond)
	_, ok, _ := cache.Get(ctx, url)
	if ok {
		t.Fatal("expected expired cache miss")
	}
}

func TestCacheJanitorRemovesOrphanedSession(t *testing.T) {
	dir := t.TempDir()
	provider := &fakeSessionProvider{id: "session-orphan"}

	cache := NewWebFetchCache(
		filepath.Join(dir, ".goa", "cache", "webfetch"),
		1*time.Hour,
		10,
		1024*1024,
		1*time.Hour,
		provider,
	)
	defer cache.Close()

	ctx := context.Background()
	url := "https://example.com"
	if err := cache.Put(ctx, url, []byte("orphan"), WebFetchMeta{}); err != nil {
		t.Fatalf("Put error: %v", err)
	}

	// Simulate session deletion by changing current session and removing the session directory.
	provider.setID("other-session")
	if err := os.RemoveAll(filepath.Join(dir, ".goa", "sessions")); err != nil {
		t.Fatalf("remove sessions dir: %v", err)
	}

	if err := cache.Cleanup(ctx); err != nil {
		t.Fatalf("Cleanup error: %v", err)
	}

	_, ok, _ := cache.Get(ctx, url)
	if ok {
		t.Fatal("expected cache miss after orphaned session cleanup")
	}
}

func TestCacheMaxEntries(t *testing.T) {
	dir := t.TempDir()
	provider := &fakeSessionProvider{id: "session-abc"}

	cache := NewWebFetchCache(
		filepath.Join(dir, ".goa", "cache", "webfetch"),
		1*time.Hour,
		2,
		1024*1024,
		1*time.Hour,
		provider,
	)
	defer cache.Close()

	ctx := context.Background()
	for i := 0; i < 3; i++ {
		url := "https://example.com/" + string(rune('a'+i))
		if err := cache.Put(ctx, url, []byte(url), WebFetchMeta{}); err != nil {
			t.Fatalf("Put error: %v", err)
		}
		time.Sleep(5 * time.Millisecond)
	}

	_, ok, _ := cache.Get(ctx, "https://example.com/a")
	if ok {
		t.Fatal("expected oldest entry evicted")
	}
	_, ok, _ = cache.Get(ctx, "https://example.com/c")
	if !ok {
		t.Fatal("expected newest entry retained")
	}
}

func TestInferSessionsDir(t *testing.T) {
	got := inferSessionsDir("/project/.goa/cache/webfetch")
	want := "/project/.goa/sessions"
	if got != want {
		t.Errorf("inferSessionsDir = %q, want %q", got, want)
	}
	got = inferSessionsDir("random/dir")
	want = "random/dir/sessions"
	if got != want {
		t.Errorf("inferSessionsDir fallback = %q, want %q", got, want)
	}
}

func TestCacheTTLFromMeta(t *testing.T) {
	dir := t.TempDir()
	provider := &fakeSessionProvider{id: "session-abc"}
	cache := NewWebFetchCache(
		filepath.Join(dir, ".goa", "cache", "webfetch"),
		0, // rely on meta TTL
		10,
		1024*1024,
		1*time.Hour,
		provider,
	)
	defer cache.Close()

	ctx := context.Background()
	if err := cache.Put(ctx, "https://example.com", []byte("x"), WebFetchMeta{TTLHours: 1}); err != nil {
		t.Fatalf("Put error: %v", err)
	}
	_, ok, _ := cache.Get(ctx, "https://example.com")
	if !ok {
		t.Fatal("expected cache hit using meta TTL")
	}
}

func TestCacheNoSessionProvider(t *testing.T) {
	dir := t.TempDir()
	cache := NewWebFetchCache(
		filepath.Join(dir, ".goa", "cache", "webfetch"),
		1*time.Hour,
		10,
		1024*1024,
		1*time.Hour,
		nil,
	)
	defer cache.Close()

	ctx := context.Background()
	if err := cache.Put(ctx, "https://example.com", []byte("x"), WebFetchMeta{}); err == nil {
		t.Fatal("expected error when no session provider")
	}
	_, ok, _ := cache.Get(ctx, "https://example.com")
	if ok {
		t.Fatal("expected cache miss when no session provider")
	}
}

func TestCacheListSessions(t *testing.T) {
	dir := t.TempDir()
	provider := &fakeSessionProvider{id: "session-abc"}
	cache := NewWebFetchCache(
		filepath.Join(dir, ".goa", "cache", "webfetch"),
		1*time.Hour,
		10,
		1024*1024,
		1*time.Hour,
		provider,
	)
	defer cache.Close()

	sessionsDir := filepath.Join(dir, ".goa", "sessions")
	_ = os.MkdirAll(sessionsDir, 0755)
	_ = os.WriteFile(filepath.Join(sessionsDir, "abc.jsonl"), []byte("{}"), 0644)

	sessions := cache.listSessions()
	if !sessions["abc"] {
		t.Errorf("expected abc session to be listed, got %v", sessions)
	}
}

func TestCacheMaxBytes(t *testing.T) {
	dir := t.TempDir()
	provider := &fakeSessionProvider{id: "session-abc"}

	cache := NewWebFetchCache(
		filepath.Join(dir, ".goa", "cache", "webfetch"),
		1*time.Hour,
		100,
		15,
		1*time.Hour,
		provider,
	)
	defer cache.Close()

	ctx := context.Background()
	if err := cache.Put(ctx, "https://example.com/a", []byte("0123456789"), WebFetchMeta{}); err != nil {
		t.Fatalf("Put error: %v", err)
	}
	time.Sleep(5 * time.Millisecond)
	if err := cache.Put(ctx, "https://example.com/b", []byte("0123456789"), WebFetchMeta{}); err != nil {
		t.Fatalf("Put error: %v", err)
	}

	_, ok, _ := cache.Get(ctx, "https://example.com/a")
	if ok {
		t.Fatal("expected oldest entry evicted by byte limit")
	}
	_, ok, _ = cache.Get(ctx, "https://example.com/b")
	if !ok {
		t.Fatal("expected newest entry retained")
	}
}

// --- BUG-06: NewWebFetchCache must surface an unwritable dir as read-only
// (Put returns a clear error) instead of silently starting a broken cache. ---

func TestWebFetchCache_UnwritableDirMarksReadOnly(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("cannot test unwritable dir as root")
	}
	parent := t.TempDir()
	// Make the parent read-only so MkdirAll of a child fails.
	if err := os.Chmod(parent, 0555); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(parent, 0755)

	cacheDir := filepath.Join(parent, "cache")
	cache := NewWebFetchCache(cacheDir, 1*time.Hour, 10, 1024, 1*time.Hour, &fakeSessionProvider{id: "s"})
	defer cache.Close()

	// Put must report a clear, cache-related error rather than a confusing
	// "file not found" later.
	err := cache.Put(context.Background(), "https://example.com", []byte("data"), WebFetchMeta{})
	if err == nil {
		t.Fatal("expected Put to fail on read-only cache")
	}
	if !strings.Contains(err.Error(), "cache disabled") {
		t.Errorf("expected 'cache disabled' error, got %v", err)
	}
}

// --- BUG-11: EffectiveTTL exposes the 24h default for TTL=0. ---

func TestWebFetchCache_EffectiveTTL_Default24h(t *testing.T) {
	cache := NewWebFetchCache(t.TempDir(), 0, 10, 1024, 1*time.Hour, &fakeSessionProvider{id: "s"})
	defer cache.Close()

	got := cache.EffectiveTTL(WebFetchMeta{})
	if got != 24*time.Hour {
		t.Errorf("EffectiveTTL() = %v, want 24h", got)
	}
}
