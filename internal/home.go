// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package internal

import (
	"os"
	"path/filepath"
	"sync"
)

// goaHomeOverride holds the --home flag value (empty = not set). It is set
// once at startup from the CLI flag before any subsystem resolves paths.
var goaHomeOverride struct {
	mu sync.RWMutex
	v  string
}

// SetGoaHome records the --home CLI flag override. Pass an empty string to
// clear it (used by tests and by the relaunch path, which re-applies the
// same parsed value idempotently).
func SetGoaHome(dir string) {
	goaHomeOverride.mu.Lock()
	goaHomeOverride.v = dir
	goaHomeOverride.mu.Unlock()
}

// GoaHome returns the root directory goa treats as the user home for all
// ~/.goa (and ~/.agents) paths. Resolution order:
//
//  1. --home CLI flag (SetGoaHome)
//  2. GOA_HOME environment variable
//  3. os.UserHomeDir()
//
// The boolean result reports whether a home directory could be resolved at
// all (false only when every source failed, matching os.UserHomeDir error
// semantics).
func GoaHome() (string, bool) {
	goaHomeOverride.mu.RLock()
	v := goaHomeOverride.v
	goaHomeOverride.mu.RUnlock()
	if v != "" {
		return v, true
	}
	if env := os.Getenv("GOA_HOME"); env != "" {
		return env, true
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", false
	}
	return home, true
}

// GoaHomeDir returns the goa config/state root (~/.goa under the resolved
// home), or an empty string when no home can be resolved.
func GoaHomeDir() string {
	home, ok := GoaHome()
	if !ok {
		return ""
	}
	return filepath.Join(home, ".goa")
}
