// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// SessionIDProvider returns the active session identifier.
type SessionIDProvider interface {
	SessionID() string
}

// WebFetchCache stores converted Markdown per URL, scoped to a session.
type WebFetchCache struct {
	Dir             string
	TTL             time.Duration
	MaxEntries      int
	MaxBytes        int64
	CleanupInterval time.Duration
	sessionProvider SessionIDProvider
	sessionsDir     string
	mu              sync.Mutex
	stopCleanup     chan struct{}
	cleanupFinished chan struct{}
	readOnly        bool  // set when the cache dir cannot be created/written
	initErr         error // the error that put the cache into read-only mode
}

// WebFetchMeta is the sidecar metadata for a cached entry.
type WebFetchMeta struct {
	URL         string    `json:"url"`
	SessionID   string    `json:"session_id"`
	FetchedAt   time.Time `json:"fetched_at"`
	TTLHours    int       `json:"ttl_hours"`
	ETag        string    `json:"etag,omitempty"`
	ContentType string    `json:"content_type,omitempty"`
	ByteSize    int64     `json:"byte_size"`
}

// WebFetchEntry is the result of a cache lookup.
type WebFetchEntry struct {
	URL      string
	Markdown []byte
	Meta     WebFetchMeta
}

// NewWebFetchCache creates a session-scoped disk cache.
func NewWebFetchCache(dir string, ttl time.Duration, maxEntries int, maxBytes int64, cleanupInterval time.Duration, provider SessionIDProvider) *WebFetchCache {
	c := &WebFetchCache{
		Dir:             dir,
		TTL:             ttl,
		MaxEntries:      maxEntries,
		MaxBytes:        maxBytes,
		CleanupInterval: cleanupInterval,
		sessionProvider: provider,
		sessionsDir:     inferSessionsDir(dir),
		stopCleanup:     make(chan struct{}),
		cleanupFinished: make(chan struct{}),
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		// Don't start a broken cache silently: mark it read-only and record the
		// error so Put can surface a clear message instead of later "file not
		// found" confusion. The janitor still runs but no-ops on a missing dir.
		c.readOnly = true
		c.initErr = err
	}
	go c.janitor()
	return c
}

func inferSessionsDir(dir string) string {
	clean := filepath.Clean(dir)
	parts := strings.Split(clean, string(filepath.Separator))
	for i := len(parts) - 1; i >= 0; i-- {
		if parts[i] == ".goa" && i < len(parts)-1 {
			base := filepath.Join(parts[:i+1]...)
			if filepath.IsAbs(clean) {
				base = string(filepath.Separator) + base
			}
			return filepath.Join(base, "sessions")
		}
	}
	return filepath.Join(dir, "sessions")
}

// Close stops the background janitor.
func (c *WebFetchCache) Close() error {
	close(c.stopCleanup)
	<-c.cleanupFinished
	return nil
}

// Get returns a cached entry if it belongs to the current session and has not expired.
func (c *WebFetchCache) Get(ctx context.Context, url string) (WebFetchEntry, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	var empty WebFetchEntry
	sessionID := c.currentSessionID()
	if sessionID == "" {
		return empty, false, nil
	}

	metaPath, mdPath := c.paths(url)
	meta, err := c.readMeta(metaPath)
	if err != nil {
		if os.IsNotExist(err) {
			return empty, false, nil
		}
		return empty, false, err
	}

	if meta.SessionID != sessionID {
		return empty, false, nil
	}
	if time.Since(meta.FetchedAt) > c.ttl(meta) {
		return empty, false, nil
	}

	data, err := os.ReadFile(mdPath)
	if err != nil {
		if os.IsNotExist(err) {
			return empty, false, nil
		}
		return empty, false, err
	}

	return WebFetchEntry{
		URL:      meta.URL,
		Markdown: data,
		Meta:     meta,
	}, true, nil
}

// Put writes a converted page to the cache for the current session.
func (c *WebFetchCache) Put(ctx context.Context, url string, markdown []byte, meta WebFetchMeta) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.readOnly {
		return fmt.Errorf("cache disabled (dir %q unwritable): %w", c.Dir, c.initErr)
	}
	sessionID := c.currentSessionID()
	if sessionID == "" {
		return fmt.Errorf("no active session")
	}
	meta.SessionID = sessionID
	meta.URL = url
	meta.FetchedAt = time.Now()
	meta.ByteSize = int64(len(markdown))

	if err := os.MkdirAll(c.Dir, 0755); err != nil {
		return fmt.Errorf("create cache dir: %w", err)
	}

	metaPath, mdPath := c.paths(url)
	if err := os.WriteFile(mdPath, markdown, 0644); err != nil {
		return fmt.Errorf("write markdown: %w", err)
	}
	if err := c.writeMeta(metaPath, meta); err != nil {
		_ = os.Remove(mdPath)
		return fmt.Errorf("write meta: %w", err)
	}

	if c.MaxEntries > 0 || c.MaxBytes > 0 {
		_ = c.enforceSessionLimitsLocked(sessionID)
	}
	return nil
}

// Cleanup removes entries whose session no longer exists or whose TTL expired.
func (c *WebFetchCache) Cleanup(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cleanupLocked()
}

func (c *WebFetchCache) cleanupLocked() error {
	entries, err := os.ReadDir(c.Dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	sessions := c.listSessions()
	now := time.Now()

	currentID := c.currentSessionID()
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".meta") {
			continue
		}
		metaPath := filepath.Join(c.Dir, e.Name())
		meta, err := c.readMeta(metaPath)
		if err != nil {
			_ = c.removeLocked(metaPath)
			continue
		}
		expired := now.Sub(meta.FetchedAt) > c.ttl(meta)
		orphaned := meta.SessionID != currentID && !sessions[meta.SessionID]
		if expired || orphaned {
			_ = c.removeLocked(metaPath)
		}
	}
	return nil
}

type cacheItem struct {
	metaPath string
	meta     WebFetchMeta
}

func (c *WebFetchCache) enforceSessionLimitsLocked(sessionID string) error {
	if c.MaxEntries <= 0 && c.MaxBytes <= 0 {
		return nil
	}

	items, err := c.sessionItemsLocked(sessionID)
	if err != nil {
		return err
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].meta.FetchedAt.Before(items[j].meta.FetchedAt)
	})

	total := c.totalBytes(items)
	return c.evictOldestLocked(items, total)
}

func (c *WebFetchCache) sessionItemsLocked(sessionID string) ([]cacheItem, error) {
	entries, err := os.ReadDir(c.Dir)
	if err != nil {
		return nil, err
	}
	var items []cacheItem
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".meta") {
			continue
		}
		metaPath := filepath.Join(c.Dir, e.Name())
		meta, err := c.readMeta(metaPath)
		if err != nil || meta.SessionID != sessionID {
			continue
		}
		items = append(items, cacheItem{metaPath: metaPath, meta: meta})
	}
	return items, nil
}

func (c *WebFetchCache) totalBytes(items []cacheItem) int64 {
	var total int64
	for _, it := range items {
		total += it.meta.ByteSize
	}
	return total
}

func (c *WebFetchCache) evictOldestLocked(items []cacheItem, total int64) error {
	for (c.MaxEntries > 0 && len(items) > c.MaxEntries) || (c.MaxBytes > 0 && total > c.MaxBytes) {
		if len(items) == 0 {
			break
		}
		oldest := items[0]
		_ = c.removeLocked(oldest.metaPath)
		total -= oldest.meta.ByteSize
		items = items[1:]
	}
	return nil
}

func (c *WebFetchCache) removeLocked(metaPath string) error {
	mdPath := strings.TrimSuffix(metaPath, ".meta") + ".md"
	_ = os.Remove(mdPath)
	return os.Remove(metaPath)
}

func (c *WebFetchCache) paths(url string) (metaPath, mdPath string) {
	key := fmt.Sprintf("%x", sha256.Sum256([]byte(url)))
	base := filepath.Join(c.Dir, key)
	return base + ".meta", base + ".md"
}

func (c *WebFetchCache) readMeta(path string) (WebFetchMeta, error) {
	var meta WebFetchMeta
	data, err := os.ReadFile(path)
	if err != nil {
		return meta, err
	}
	if err := json.Unmarshal(data, &meta); err != nil {
		return meta, err
	}
	return meta, nil
}

func (c *WebFetchCache) writeMeta(path string, meta WebFetchMeta) error {
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// EffectiveTTL returns the cache's effective TTL for an entry, applying the
// 24h default when neither the cache nor the entry specifies a TTL. Use this
// instead of reading Cache.TTL directly so TTL=0 shows the real default.
func (c *WebFetchCache) EffectiveTTL(meta WebFetchMeta) time.Duration {
	return c.ttl(meta)
}

func (c *WebFetchCache) ttl(meta WebFetchMeta) time.Duration {
	if c.TTL > 0 {
		return c.TTL
	}
	if meta.TTLHours > 0 {
		return time.Duration(meta.TTLHours) * time.Hour
	}
	return 24 * time.Hour
}

func (c *WebFetchCache) currentSessionID() string {
	if c.sessionProvider == nil {
		return ""
	}
	return c.sessionProvider.SessionID()
}

func (c *WebFetchCache) listSessions() map[string]bool {
	m := make(map[string]bool)
	entries, err := os.ReadDir(c.sessionsDir)
	if err != nil {
		return m
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".jsonl") {
			name := strings.TrimSuffix(e.Name(), ".jsonl")
			m[name] = true
		}
	}
	return m
}

func (c *WebFetchCache) janitor() {
	defer close(c.cleanupFinished)
	_ = c.Cleanup(context.Background())

	ticker := time.NewTicker(c.cleanupInterval())
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			_ = c.Cleanup(context.Background())
		case <-c.stopCleanup:
			return
		}
	}
}

func (c *WebFetchCache) cleanupInterval() time.Duration {
	if c.CleanupInterval > 0 {
		return c.CleanupInterval
	}
	return 24 * time.Hour
}
