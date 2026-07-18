// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package plugins

import (
	"fmt"
	"net/url"
	"os/exec"
	"runtime"
)

// BrowserBridge opens URLs in the user's default browser for OAuth device
// flows. Best-effort: failure is reported to the plugin, which always prints
// the URL as a fallback so the user can open it manually.
type BrowserBridge struct {
	// open is overridable for tests.
	open func(url string) error
}

// NewBrowserBridge creates a platform-aware browser bridge.
func NewBrowserBridge() *BrowserBridge {
	return &BrowserBridge{open: platformOpen}
}

// OpenURL opens raw in the default browser. Only http/https URLs are allowed.
func (b *BrowserBridge) OpenURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid url: %v", err)
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return fmt.Errorf("refusing to open non-http url %q", raw)
	}
	return b.open(raw)
}

// platformOpen dispatches to the OS opener.
func platformOpen(raw string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", raw)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", raw)
	default: // linux, bsd
		cmd = exec.Command("xdg-open", raw)
	}
	// Detach: the browser outlives the command; don't wait on it.
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("open browser: %w", err)
	}
	go func() { _ = cmd.Wait() }()
	return nil
}
