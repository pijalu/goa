// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package update

import (
	"path/filepath"
	"testing"
	"time"
)

func TestCheckerCacheRoundTrip(t *testing.T) {
	dir := t.TempDir()
	checker := NewChecker("v0.0.1", dir)
	res := &CheckResult{LatestVersion: "v1.0.0", URL: "http://example.com", CheckedAt: time.Now()}
	if err := checker.WriteCache(res); err != nil {
		t.Fatalf("write cache: %v", err)
	}
	cached, ok := checker.ReadCache()
	if !ok || cached.LatestVersion != "v1.0.0" {
		t.Errorf("cached = %+v, ok=%v", cached, ok)
	}
}

func TestCheckerCacheExpiration(t *testing.T) {
	dir := t.TempDir()
	checker := NewChecker("v0.0.1", dir)
	res := &CheckResult{LatestVersion: "v1.0.0", CheckedAt: time.Now().Add(-48 * time.Hour)}
	_ = checker.WriteCache(res)
	_, ok := checker.ReadCache()
	if ok {
		t.Error("expected stale cache to be ignored")
	}
}

func TestCheckerIsNewer(t *testing.T) {
	c := NewChecker("v1.0.0", t.TempDir())
	if !c.IsNewer("v2.0.0") {
		t.Error("expected v2.0.0 newer")
	}
	if c.IsNewer("v1.0.0") {
		t.Error("expected same version not newer")
	}
}

func TestCachePath(t *testing.T) {
	c := NewChecker("v1", filepath.Join("a", "b"))
	if filepath.Base(c.CachePath) != "update-check.json" {
		t.Errorf("cache path = %s", c.CachePath)
	}
}
