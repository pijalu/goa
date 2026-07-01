// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package update

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// Checker checks for newer Goa releases.
type Checker struct {
	CurrentVersion string
	CachePath      string
	HTTPClient     *http.Client
}

// NewChecker creates a checker for the current version.
func NewChecker(current, cacheDir string) *Checker {
	return &Checker{
		CurrentVersion: current,
		CachePath:      filepath.Join(cacheDir, "update-check.json"),
		HTTPClient:     &http.Client{Timeout: 10 * time.Second},
	}
}

// CheckResult holds update information.
type CheckResult struct {
	LatestVersion string    `json:"latest_version"`
	URL           string    `json:"url"`
	CheckedAt     time.Time `json:"checked_at"`
}

// Check queries GitHub for the latest release, using a local cache.
func (c *Checker) Check(ctx context.Context) (*CheckResult, error) {
	if cached, ok := c.ReadCache(); ok {
		return cached, nil
	}

	req, err := http.NewRequestWithContext(ctx, "GET", "https://api.github.com/repos/pijalu/goa/releases/latest", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github.v3+json")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("update check: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("update check status: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var release struct {
		TagName string `json:"tag_name"`
		HTMLURL string `json:"html_url"`
	}
	if err := json.Unmarshal(body, &release); err != nil {
		return nil, fmt.Errorf("parse release: %w", err)
	}

	result := &CheckResult{
		LatestVersion: release.TagName,
		URL:           release.HTMLURL,
		CheckedAt:     time.Now(),
	}
	_ = c.WriteCache(result)
	return result, nil
}

// ReadCache returns a cached result if it is still fresh.
func (c *Checker) ReadCache() (*CheckResult, bool) {
	data, err := os.ReadFile(c.CachePath)
	if err != nil {
		return nil, false
	}
	var r CheckResult
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, false
	}
	if time.Since(r.CheckedAt) > 24*time.Hour {
		return nil, false
	}
	return &r, true
}

// WriteCache persists a check result to disk.
func (c *Checker) WriteCache(r *CheckResult) error {
	if err := os.MkdirAll(filepath.Dir(c.CachePath), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(c.CachePath, data, 0o644)
}

// IsNewer reports whether latest is newer than current.
func (c *Checker) IsNewer(latest string) bool {
	return latest != "" && latest != c.CurrentVersion
}
